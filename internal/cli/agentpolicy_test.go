package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/parser"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/mcpserver"
)

// Spec 050: [agent-allow] limits the values an agent run of a task may have.
// Every body here writes a marker file, so "ran" is read from the disk.

const (
	deployRefused = `task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"`
	shipRefused   = `task "ship" runs "deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"`
)

// policySrc is the restricted part every case below builds on.
const policySrc = `# Deploy the service.
[agent-allow(env="staging")]
deploy env:enum("staging","prod") tag="none":
    @echo {{env}} > deploy.done

# Roll out to one region.
[agent-allow(env="staging", region="eu-west-1")]
rollout env:enum("staging","prod") region="eu-west-1":
    @echo {{env}}-{{quote(region)}} > rollout.done

# Deploy to production by default.
[agent-allow(env="staging")]
publish env:enum("staging","prod")="prod":
    @echo {{env}} > publish.done

# Run the tests.
test:
    @echo tested > test.done
`

// markers lists the marker files in the adapter's working directory, without
// the .done suffix.
func markers(t *testing.T, a *mcpAdapter) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(a.workDir, "*.done"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".done"))
	}
	slices.Sort(names)
	return names
}

// markerText is the trimmed content of one marker file.
func markerText(t *testing.T, a *mcpAdapter, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(a.workDir, name+".done"))
	if err != nil {
		t.Fatalf("reading %s.done: %v", name, err)
	}
	return strings.TrimSpace(string(data))
}

// callText runs a call and returns its result and the error's text, or "".
func callText(t *testing.T, a *mcpAdapter, name string, args map[string]string) (mcpserver.Result, string) {
	t.Helper()
	res, err := a.Call(t.Context(), name, args)
	if err != nil {
		return res, err.Error()
	}
	return res, ""
}

func taskOf(t *testing.T, src, name string) *ast.Task {
	t.Helper()
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	task := indexTasks(f)[name]
	if task == nil {
		t.Fatalf("no task %q", name)
	}
	return task
}

// FR-007, FR-008: every predicate must hold, on the exact string.
func TestPolicyBreach(t *testing.T) {
	rollout := taskOf(t, policySrc, "rollout")
	empty := taskOf(t, "[agent-allow(env=\"\")]\nt env=\"\":\n    @echo hi\n", "t")
	cases := []struct {
		name string
		task *ast.Task
		args map[string]string
		want []refusedValue
	}{
		{"no policy", taskOf(t, policySrc, "test"), map[string]string{"x": "y"}, nil},
		{"all met", rollout, map[string]string{"env": "staging", "region": "eu-west-1"}, nil},
		{"one of two", rollout, map[string]string{"env": "staging", "region": "us-east-1"}, []refusedValue{{"region", "us-east-1"}}},
		{"both, in the attribute's order", rollout, map[string]string{"region": "us", "env": "prod"}, []refusedValue{{"env", "prod"}, {"region", "us"}}},
		{"absent parameter", rollout, map[string]string{"region": "eu-west-1"}, []refusedValue{{"env", ""}}},
		{"empty value required and given", empty, map[string]string{"env": ""}, nil},
		{"case matters", rollout, map[string]string{"env": "Staging", "region": "eu-west-1"}, []refusedValue{{"env", "Staging"}}},
		{"no trimming", rollout, map[string]string{"env": " staging", "region": "eu-west-1"}, []refusedValue{{"env", " staging"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := policyBreach(c.task, eval.Args{Values: c.args}); !slices.Equal(got, c.want) {
				t.Errorf("policyBreach = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPrintableLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"two\nlines\tand a tab", `two\nlines\tand a tab`},
		{"override \u202e here", `override \u202e here`},
		{"no-break\u00a0space", `no-break\u00a0space`},
		{"préprod", "préprod"},
		{"", ""},
	}
	for _, c := range cases {
		if got := printableLine(c.in); got != c.want {
			t.Errorf("printableLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// FR-025: a value is masked, then cut, then quoted onto one line.
func TestRenderPolicyValue(t *testing.T) {
	const secret = "supersecretvalue123"
	mask := func(s string) string { return strings.ReplaceAll(s, secret, "***") }
	long := strings.Repeat("a", 200)
	cases := []struct {
		name  string
		value string
		mask  func(string) string
		want  string
	}{
		{"plain", "prod", nil, `"prod"`},
		{"line break", "a\nb", nil, `"a\nb"`},
		{"long", long, nil, `"` + strings.Repeat("a", 64) + `"...`},
		{"exactly the limit", strings.Repeat("a", 64), nil, `"` + strings.Repeat("a", 64) + `"`},
		{"secret", "pre " + secret, mask, `"pre ***"`},
		{"secret across the cut", strings.Repeat("a", 60) + secret, mask, `"` + strings.Repeat("a", 60) + `***"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderPolicyValue(c.value, c.mask); got != c.want {
				t.Errorf("renderPolicyValue = %s, want %s", got, c.want)
			}
		})
	}
}

// FR-023, FR-024, FR-026: the texts of contracts/mcp-tools.md.
func TestPolicyDenialText(t *testing.T) {
	one := []*ast.AllowPredicate{{Name: "env", Value: "staging"}}
	two := []*ast.AllowPredicate{{Name: "env", Value: "staging"}, {Name: "region", Value: "eu-west-1"}}
	cases := []struct {
		name string
		d    policyDenial
		want string
	}{
		{
			"called task",
			policyDenial{kind: denialBreach, called: "deploy", task: "deploy", refused: []refusedValue{{"env", "prod"}}, policy: one},
			deployRefused,
		},
		{
			"two refused",
			policyDenial{kind: denialBreach, called: "rollout", task: "rollout", refused: []refusedValue{{"env", "prod"}, {"region", "us-east-1"}}, policy: two},
			`task "rollout" is not allowed for agents with env="prod", region="us-east-1"; agent policy requires env="staging", region="eu-west-1"`,
		},
		{
			"one refused of two",
			policyDenial{kind: denialBreach, called: "rollout", task: "rollout", refused: []refusedValue{{"region", "us-east-1"}}, policy: two},
			`task "rollout" is not allowed for agents with region="us-east-1"; agent policy requires env="staging", region="eu-west-1"`,
		},
		{
			"reached task",
			policyDenial{kind: denialBreach, called: "ship", task: "deploy", refused: []refusedValue{{"env", "prod"}}, policy: one},
			shipRefused,
		},
		{
			"volatile",
			policyDenial{kind: denialVolatile, called: "nightly", task: "deploy", refused: []refusedValue{{name: "env"}}, policy: one},
			`task "nightly" runs "deploy" with a value of env that is not known before the run; agent policy requires env="staging"`,
		},
		{
			"unresolved",
			policyDenial{kind: denialUnresolved, called: "broken", owner: "broken", task: "deploy", reason: "environment variable not set: X\nsecond line"},
			`task "broken" was not run: the arguments "broken" passes to "deploy" could not be worked out before the run (environment variable not set: X\nsecond line), and an agent policy has to be checked on that path`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.d.text(nil)
			if got != c.want {
				t.Errorf("text = %q\nwant   %q", got, c.want)
			}
			if again := c.d.text(nil); again != got {
				t.Errorf("the same denial gave two texts: %q, %q", got, again)
			}
		})
	}

	// A reason can carry text the agent sent: nothing in it may reorder or
	// break the line a person reads.
	hostile := policyDenial{kind: denialUnresolved, called: "a", owner: "a", task: "b", reason: "bad \u202eevil\x00\u00a0"}
	if got := hostile.text(nil); !strings.Contains(got, `(bad \u202eevil\x00\u00a0)`) {
		t.Errorf("a reason with characters that are not printable is not escaped: %q", got)
	}

	long := policyDenial{kind: denialUnresolved, called: "a", owner: "a", task: "b", reason: strings.Repeat("x", 300)}
	if got := long.text(nil); !strings.Contains(got, "("+strings.Repeat("x", 200)+"...)") {
		t.Errorf("a long reason is not cut to 200 characters: %q", got)
	}
}

// US1: a refused value runs nothing, an allowed one runs.
func TestAdapterRefusesForbiddenValue(t *testing.T) {
	a := adapterFor(t, policySrc)
	if _, got := callText(t, a, "deploy", map[string]string{"env": "prod"}); got != deployRefused {
		t.Errorf("deploy prod: error = %q, want %q", got, deployRefused)
	}
	if ran := markers(t, a); len(ran) != 0 {
		t.Errorf("a refused call ran %v", ran)
	}

	res, errText := callText(t, a, "deploy", map[string]string{"env": "staging", "tag": "anything at all"})
	if errText != "" || res.ExitCode != 0 {
		t.Fatalf("deploy staging: exit %d, error %q", res.ExitCode, errText)
	}
	if got := markerText(t, a, "deploy"); got != "staging" {
		t.Errorf("deploy.done = %q, want staging", got)
	}

	// A missing required argument is that error, as before: there is no
	// value to check yet, and none is made up.
	b := adapterFor(t, policySrc)
	if _, got := callText(t, b, "deploy", map[string]string{}); got != `task "deploy" is missing required argument "env"` {
		t.Errorf("deploy with no arguments: error = %q", got)
	}
	if ran := markers(t, b); len(ran) != 0 {
		t.Errorf("a call with a missing argument ran %v", ran)
	}
}

// FR-025, SC-006: a refused value that is a secret is shown masked.
func TestAdapterMasksRefusedValue(t *testing.T) {
	const secret = "supersecretvalue123"
	a := adapterFor(t, "[agent-allow(channel=\"beta\")]\ntag channel:\n    @echo tagged > tag.done\n")
	a.baseEnv = []string{"MY_API_TOKEN=" + secret}
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, a.settings.Secrets, a.settings.Unmasked)
	_, got := callText(t, a, "tag", map[string]string{"channel": secret})
	want := `task "tag" is not allowed for agents with channel="***"; agent policy requires channel="beta"`
	if got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	if strings.Contains(got, secret) {
		t.Errorf("the refusal holds the secret: %q", got)
	}
}

// US2: the default the task would use is what is checked, and it is never
// replaced.
func TestAdapterChecksDefaults(t *testing.T) {
	t.Run("a refused literal default", func(t *testing.T) {
		a := adapterFor(t, policySrc)
		want := `task "publish" is not allowed for agents with env="prod"; agent policy requires env="staging"`
		for _, args := range []map[string]string{nil, {}} {
			if _, got := callText(t, a, "publish", args); got != want {
				t.Errorf("publish with %v: error = %q, want %q", args, got, want)
			}
		}
		if ran := markers(t, a); len(ran) != 0 {
			t.Errorf("a refused call ran %v", ran)
		}
		if _, errText := callText(t, a, "publish", map[string]string{"env": "staging"}); errText != "" {
			t.Fatalf("publish staging: %s", errText)
		}
		if got := markerText(t, a, "publish"); got != "staging" {
			t.Errorf("publish.done = %q, want staging", got)
		}
	})

	t.Run("an allowed default", func(t *testing.T) {
		a := adapterFor(t, policySrc)
		if _, errText := callText(t, a, "rollout", map[string]string{"env": "staging"}); errText != "" {
			t.Fatalf("rollout: %s", errText)
		}
		if got := markerText(t, a, "rollout"); got != "staging-eu-west-1" {
			t.Errorf("rollout.done = %q, want staging-eu-west-1", got)
		}
	})

	t.Run("a default that is an expression", func(t *testing.T) {
		src := "[agent-allow(env=\"staging\")]\ngo env=env(\"RUNE_POLICY_TEST\", \"prod\"):\n    @echo {{quote(env)}} > go.done\n"
		// Setenv first, so the test restores the variable, then unset it.
		t.Setenv("RUNE_POLICY_TEST", "")
		if err := os.Unsetenv("RUNE_POLICY_TEST"); err != nil {
			t.Fatal(err)
		}
		a := adapterFor(t, src)
		want := `task "go" is not allowed for agents with env="prod"; agent policy requires env="staging"`
		if _, got := callText(t, a, "go", nil); got != want {
			t.Errorf("unset: error = %q, want %q", got, want)
		}
		t.Setenv("RUNE_POLICY_TEST", "staging")
		if _, errText := callText(t, a, "go", nil); errText != "" {
			t.Fatalf("set to staging: %s", errText)
		}
		if got := markerText(t, a, "go"); got != "staging" {
			t.Errorf("go.done = %q, want staging", got)
		}
	})
}

// walkFor builds a walk on an engine for src, as preflightPolicy does.
func walkFor(t *testing.T, src string, overrides map[string]string) *policyWalk {
	t.Helper()
	eng, _, _ := confirmEngine(t, src, nil, false, false)
	for name, value := range overrides {
		eng.scope.Overrides[name] = value
	}
	return newPolicyWalk(eng, "")
}

// Research R7: which expressions can have another value when the run reaches
// them.
func TestVolatileExpr(t *testing.T) {
	src := "stamp := datetime()\nlater := stamp + \"-x\"\nfixed := \"v1\"\npinned := uuid()\nloop := loop\n" +
		"t:\n    @echo hi\n"
	w := walkFor(t, src, map[string]string{"pinned": "from-the-command-line"})
	params := map[string]bool{"safe": false, "risky": true}
	cases := []struct {
		expr string
		want bool
	}{
		{`"literal"`, false},
		{`env("X")`, false},
		{`os() + "-" + arch()`, false},
		{`uuid()`, true},
		{`datetime()`, true},
		{`path_exists("f")`, true},
		{`read("f")`, true},
		{`sha256_file("f")`, true},
		{`which("x")`, true},
		{`require("x")`, true},
		{`absolute_path("x")`, true},
		{`"a" + uuid()`, true},
		{`"a" / read("f")`, true},
		{`uppercase(uuid())`, true},
		{`if uuid() == "x" { "a" } else { "b" }`, true},
		{`if "x" == uuid() { "a" } else { "b" }`, true},
		{`if "x" == "y" { uuid() } else { "b" }`, true},
		{`if "x" == "y" { "a" } else { uuid() }`, true},
		{`if "x" == "y" { "a" } else { "b" }`, false},
		{`safe`, false},
		{`risky`, true},
		{`"a" + risky`, true},
		{`stamp`, true},
		{`later`, true},
		{`fixed`, false},
		{`pinned`, false},
		{`loop`, false},
		{`nosuch`, false},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			expr, diags := parser.ParseExprFragment("Runefile", c.expr)
			if diags.HasErrors() {
				t.Fatalf("parse: %v", diags)
			}
			if got := w.volatileExpr(expr, params); got != c.want {
				t.Errorf("volatileExpr(%s) = %v, want %v", c.expr, got, c.want)
			}
		})
	}
	if w.volatileExpr(nil, params) {
		t.Error("a missing expression is volatile")
	}
}

// Which parameters of a target can have another value at run time, by where
// each one gets its value.
func TestPolicyWalkVolatility(t *testing.T) {
	src := "target first second=first third=\"fixed\" *rest:\n    @echo hi\n" +
		"caller safe risky: (target safe) (target risky) (target safe \"b\" \"c\" \"d\" risky) (target safe \"b\" \"c\" \"d\" \"e\")\n    @echo hi\n"
	w := walkFor(t, src, nil)
	caller, target := w.e.tasks["caller"], w.e.tasks["target"]
	params := map[string]bool{"safe": false, "risky": true}
	cases := []struct {
		name string
		edge int
		want map[string]bool
	}{
		{"fixed arguments and defaults", 0, map[string]bool{"first": false, "second": false, "third": false, "rest": false}},
		{"a default that reads a changing parameter", 1, map[string]bool{"first": true, "second": true, "third": false, "rest": false}},
		{"a changing element of the variadic", 2, map[string]bool{"first": false, "second": false, "third": false, "rest": true}},
		{"fixed elements of the variadic", 3, map[string]bool{"first": false, "second": false, "third": false, "rest": false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := w.volatility(params, caller.Deps[c.edge], target)
			for name, want := range c.want {
				if got[name] != want {
					t.Errorf("%s volatile = %v, want %v", name, got[name], want)
				}
			}
		})
	}
}

// Every built-in function is classified, so a new one cannot be added
// without choosing. One that is not classified counts as volatile.
func TestBuiltinsClassified(t *testing.T) {
	for _, name := range eval.BuiltinNames() {
		if _, known := builtinVolatility(name); !known {
			t.Errorf("built-in %q is not classified as volatile or stable in builtinVolatility", name)
		}
	}
	if volatile, known := builtinVolatility("no_such_builtin"); !volatile || known {
		t.Errorf("an unknown function: volatile=%v known=%v, want true, false", volatile, known)
	}
}

func TestPolicyReaches(t *testing.T) {
	src := policySrc + "wrapper: (deploy \"staging\")\n    @echo w\nouter: wrapper\n    @echo o\nhook: || outer\n    @echo h\n"
	w := walkFor(t, src, nil)
	want := map[string]bool{"deploy": true, "wrapper": true, "outer": true, "hook": true, "test": false, "nosuch": false}
	for range 2 { // the second pass reads the memo
		for name, reaches := range want {
			if got := w.reaches(name); got != reaches {
				t.Errorf("reaches(%s) = %v, want %v", name, got, reaches)
			}
		}
	}
}

// US3: what a call can reach is checked before anything runs.
func TestPreflightPolicy(t *testing.T) {
	missing := filepath.ToSlash(filepath.Join(t.TempDir(), "no-such-file"))
	volatileArg := `if path_exists("` + missing + `") == "true" { "prod" } else { "staging" }`
	volatileRefused := func(called string) string {
		return `task "` + called + `" runs "deploy" with a value of env that is not known before the run; agent policy requires env="staging"`
	}
	unresolved := func(called, owner string) string {
		return `task "` + called + `" was not run: the arguments "` + owner + `" passes to "deploy" could not be worked out before the run (environment variable not set: RUNE_NO_SUCH_SETTING), and an agent policy has to be checked on that path`
	}
	cases := []struct {
		name    string
		tasks   string
		call    string
		args    map[string]string
		wantErr string
		wantRan []string
		failed  bool // the run itself fails, with no denial
	}{
		{
			name:    "a dependency with a refused value",
			tasks:   "ship: test (deploy \"prod\")\n    @echo shipped > ship.done\n",
			call:    "ship",
			wantErr: shipRefused,
		},
		{
			name:    "a dependency with the allowed value",
			tasks:   "preview: test (deploy \"staging\")\n    @echo previewed > preview.done\n",
			call:    "preview",
			wantRan: []string{"deploy", "preview", "test"},
		},
		{
			name:    "a forwarded argument, refused",
			tasks:   "promote target: (deploy target)\n    @echo promoted > promote.done\n",
			call:    "promote",
			args:    map[string]string{"target": "prod"},
			wantErr: `task "promote" runs "deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"`,
		},
		{
			name:    "a forwarded argument, allowed",
			tasks:   "promote target: (deploy target)\n    @echo promoted > promote.done\n",
			call:    "promote",
			args:    map[string]string{"target": "staging"},
			wantRan: []string{"deploy", "promote"},
		},
		{
			name:    "an && hook",
			tasks:   "announce: && (deploy \"prod\")\n    @echo announced > announce.done\n",
			call:    "announce",
			wantErr: `task "announce" runs "deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"`,
		},
		{
			name:    "a || hook of a task that succeeds",
			tasks:   "attempt: || (deploy \"prod\")\n    @echo attempted > attempt.done\n",
			call:    "attempt",
			wantErr: `task "attempt" runs "deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"`,
		},
		{
			name:    "an allowed and a refused run of one task",
			tasks:   "both: (deploy \"staging\") (deploy \"prod\")\n    @echo both > both.done\n",
			call:    "both",
			wantErr: `task "both" runs "deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"`,
		},
		{
			name:    "two levels down",
			tasks:   "outer: mid\n    @echo outer > outer.done\nmid: test (deploy \"prod\")\n    @echo mid > mid.done\n",
			call:    "outer",
			wantErr: `task "outer" runs "deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"`,
		},
		{
			name:    "a private restricted task",
			tasks:   "[private]\n[agent-allow(env=\"staging\")]\nhidden env:\n    @echo {{quote(env)}} > hidden.done\npub: (hidden \"prod\")\n    @echo pub > pub.done\n",
			call:    "pub",
			wantErr: `task "pub" runs "hidden", which is not allowed for agents with env="prod"; agent policy requires env="staging"`,
		},
		{
			name:    "a restricted task for another OS is skipped",
			tasks:   "[windows]\n[agent-allow(env=\"staging\")]\nwin env:\n    @echo win > win.done\ndispatch: (win \"prod\")\n    @echo dispatched > dispatch.done\n",
			call:    "dispatch",
			wantRan: []string{"dispatch"},
		},
		{
			name:    "the failure hook of a failure hook never runs",
			tasks:   "first: || second\n    @echo first > first.done\nsecond: || (deploy \"prod\")\n    @echo second > second.done\n",
			call:    "first",
			wantRan: []string{"first"},
		},
		{
			name:    "the same run reached by two paths",
			tasks:   "left: (deploy \"staging\")\n    @echo left > left.done\nright: (deploy \"staging\")\n    @echo right > right.done\ndiamond: left right\n    @echo diamond > diamond.done\n",
			call:    "diamond",
			wantRan: []string{"deploy", "diamond", "left", "right"},
		},
		{
			name:    "a dependency that runs with its refused default",
			tasks:   "wrap: publish\n    @echo wrapped > wrap.done\n",
			call:    "wrap",
			wantErr: `task "wrap" runs "publish", which is not allowed for agents with env="prod"; agent policy requires env="staging"`,
		},
		{
			name:    "an argument that can change during the run",
			tasks:   "nightly: test (deploy " + volatileArg + ")\n    @echo nightly > nightly.done\n",
			call:    "nightly",
			wantErr: volatileRefused("nightly"),
		},
		{
			name:    "a changing argument forwarded through a task",
			tasks:   "mid e: (deploy e)\n    @echo mid > mid.done\nfwd: (mid " + volatileArg + ")\n    @echo fwd > fwd.done\n",
			call:    "fwd",
			wantErr: volatileRefused("fwd"),
		},
		{
			name:    "the same run reached again with a changing argument",
			tasks:   "mid e: (deploy e)\n    @echo mid > mid.done\ntwice: (mid \"staging\") (mid " + volatileArg + ")\n    @echo twice > twice.done\n",
			call:    "twice",
			wantErr: volatileRefused("twice"),
		},
		{
			name:    "a changing argument for a parameter the policy does not name",
			tasks:   "stamped: (deploy \"staging\" " + volatileArg + ")\n    @echo stamped > stamped.done\n",
			call:    "stamped",
			wantRan: []string{"deploy", "stamped"},
		},
		{
			name: "an empty variadic shadows a changing variable of its name",
			tasks: "rest := uuid()\n" +
				"mid *rest: (deploy if rest == \"\" { \"staging\" } else { \"prod\" })\n    @echo mid > mid.done\n" +
				"outer: mid\n    @echo outer > outer.done\n",
			call:    "outer",
			wantRan: []string{"deploy", "mid", "outer"},
		},
		{
			name:    "an argument that does not resolve",
			tasks:   "broken: test (deploy env(\"RUNE_NO_SUCH_SETTING\"))\n    @echo broken > broken.done\n",
			call:    "broken",
			wantErr: unresolved("broken", "broken"),
		},
		{
			name:    "an argument that does not resolve, for a parameter the policy does not name",
			tasks:   "tagged: test (deploy \"staging\" env(\"RUNE_NO_SUCH_SETTING\"))\n    @echo tagged > tagged.done\n",
			call:    "tagged",
			wantErr: unresolved("tagged", "tagged"),
		},
		{
			name:    "an argument that does not resolve, two levels down",
			tasks:   "top: low\n    @echo top > top.done\nlow: (deploy env(\"RUNE_NO_SUCH_SETTING\"))\n    @echo low > low.done\n",
			call:    "top",
			wantErr: unresolved("top", "low"),
		},
		{
			name:   "an unresolvable edge that reaches no policy is left to the run",
			tasks:  "other x:\n    @echo other > other.done\nloose: (other env(\"RUNE_NO_SUCH_SETTING\")) (deploy \"staging\")\n    @echo loose > loose.done\n",
			call:   "loose",
			failed: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := adapterFor(t, policySrc+c.tasks)
			a.goos = "linux"
			res, errText := callText(t, a, c.call, c.args)
			if errText != c.wantErr {
				t.Errorf("error = %q\nwant    %q", errText, c.wantErr)
			}
			if c.wantErr == "" && (res.ExitCode != 0) != c.failed {
				t.Errorf("exit %d, stderr %q", res.ExitCode, res.Stderr)
			}
			if ran := markers(t, a); !slices.Equal(ran, c.wantRan) {
				t.Errorf("ran %v, want %v", ran, c.wantRan)
			}
		})
	}
}

// FR-018a: checking a call does not fix a module variable before the run
// reads it.
func TestPreflightDoesNotFixVariables(t *testing.T) {
	version := filepath.ToSlash(filepath.Join(t.TempDir(), "VERSION"))
	if err := os.WriteFile(version, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "version := read(\"" + version + "\")\n" +
		"bump:\n    @echo two > '" + version + "'\n" +
		"[agent-allow(env=\"staging\")]\ndeploy env tag:\n    @echo {{quote(env)}}-{{quote(trim(tag))}} > deploy.done\n" +
		"release: bump (deploy \"staging\" version)\n    @echo released > release.done\n"
	a := adapterFor(t, src)
	res, errText := callText(t, a, "release", nil)
	if errText != "" || res.ExitCode != 0 {
		t.Fatalf("release: exit %d, error %q, stderr %q", res.ExitCode, errText, res.Stderr)
	}
	if got := markerText(t, a, "deploy"); got != "staging-two" {
		t.Errorf("deploy.done = %q, want staging-two: the check read the variable before bump ran", got)
	}
}

// agentEngine is confirmEngine marked as an agent run.
func agentEngine(t *testing.T, src string) (*engine, *bytes.Buffer) {
	t.Helper()
	eng, _, stderr := confirmEngine(t, src, nil, false, false)
	eng.agent = true
	return eng, stderr
}

// Research R8: the second check, where a restricted body would start.
func TestPolicyGate(t *testing.T) {
	prod := eval.Args{Values: map[string]string{"env": "prod"}}
	staging := eval.Args{Values: map[string]string{"env": "staging"}}

	eng, _, stderr := confirmEngine(t, policySrc, nil, false, false)
	if f := eng.policyGate(eng.tasks["deploy"], prod); f != nil {
		t.Errorf("a command-line engine refused: %v", f)
	}

	agent, agentErr := agentEngine(t, policySrc)
	if f := agent.policyGate(agent.tasks["deploy"], staging); f != nil {
		t.Errorf("an allowed value was refused: %v", f)
	}
	if f := agent.policyGate(agent.tasks["test"], prod); f != nil {
		t.Errorf("a task with no policy was refused: %v", f)
	}
	f := agent.policyGate(agent.tasks["deploy"], prod)
	if f == nil || f.Error() != deployRefused {
		t.Errorf("gate = %v, want %q", f, deployRefused)
	}
	if stderr.Len() != 0 || agentErr.Len() != 0 {
		t.Errorf("the gate wrote: %q %q", stderr.String(), agentErr.String())
	}
}

func TestExecuteRunsGate(t *testing.T) {
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "deploy.done"))
	src := "[agent-allow(env=\"staging\")]\ndeploy env:\n    @echo deployed > '" + marker + "'\n"
	prod := eval.Args{Values: map[string]string{"env": "prod"}}
	want := `task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"`

	eng, stderr := agentEngine(t, src)
	err := eng.Execute(eng.tasks["deploy"], prod)
	var failure *TaskFailure
	if !errors.As(err, &failure) || !failure.Silent || !errors.Is(err, scheduler.ErrBodyNotRun) {
		t.Errorf("Execute = %v, want a silent failure marked as body-not-run", err)
	}
	if got := strings.Count(stderr.String(), want); got != 1 {
		t.Errorf("the refusal is on stderr %d times, want once: %q", got, stderr.String())
	}

	hook, hookErr := agentEngine(t, src)
	err = hook.ExecuteFailHook(hook.tasks["deploy"], prod, scheduler.Failure{TaskName: "x", ExitCode: 1})
	if err == nil || err.Error() != want {
		t.Errorf("ExecuteFailHook = %v, want %q", err, want)
	}
	if hookErr.Len() != 0 {
		t.Errorf("ExecuteFailHook wrote %q: the scheduler prints the hook's failure", hookErr.String())
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("a refused body ran")
	}

	// The same engine runs the body for the allowed value.
	if err := eng.Execute(eng.tasks["deploy"], eval.Args{Values: map[string]string{"env": "staging"}}); err != nil {
		t.Fatalf("Execute with staging: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("the allowed body did not run")
	}
}

// runCommandLine runs the command-line entry on a Runefile in dir.
func runCommandLine(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var stderr bytes.Buffer
	// No stdin: the shell copies a reader that is not a file on a goroutine
	// per body, and one reader shared by several bodies is a data race.
	err := Run(Options{
		Cwd:    dir,
		Stdout: &bytes.Buffer{},
		Stderr: &stderr,
		Quiet:  true,
	}, args)
	return stderr.String(), err
}

func doneIn(t *testing.T, dir string) []string {
	t.Helper()
	return markers(t, &mcpAdapter{workDir: dir})
}

// FR-013: a person's run is not limited, whatever it reaches.
func TestCommandLineIgnoresPolicy(t *testing.T) {
	t.Setenv(agentRunVar, "")
	src := policySrc + "ship: test (deploy \"prod\")\n    @echo shipped > ship.done\n"

	dir, _ := writeRunefile(t, src)
	if stderr, err := runCommandLine(t, dir, "deploy", "prod"); err != nil {
		t.Fatalf("rune deploy prod: %v\n%s", err, stderr)
	}
	if got := doneIn(t, dir); !slices.Equal(got, []string{"deploy"}) {
		t.Errorf("ran %v, want deploy", got)
	}

	dir, _ = writeRunefile(t, src)
	if stderr, err := runCommandLine(t, dir, "ship"); err != nil {
		t.Fatalf("rune ship: %v\n%s", err, stderr)
	}
	if got := doneIn(t, dir); !slices.Equal(got, []string{"deploy", "ship", "test"}) {
		t.Errorf("ran %v, want deploy, ship, test", got)
	}

	// Any value but 1 is not the mark.
	t.Setenv(agentRunVar, "true")
	dir, _ = writeRunefile(t, src)
	if stderr, err := runCommandLine(t, dir, "deploy", "prod"); err != nil {
		t.Fatalf("rune deploy prod with %s=true: %v\n%s", agentRunVar, err, stderr)
	}
}

// US3: the nested endpoint, and the [context] hook, are agent runs.
func TestAgentEndpointAppliesPolicy(t *testing.T) {
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "deploy.done"))
	src := "[agent-allow(env=\"staging\")]\ndeploy env:enum(\"staging\",\"prod\"):\n    @echo {{env}} > '" + marker + "'\n"
	eng, _, _ := confirmEngine(t, src, nil, false, false)
	adapter := eng.newAgentAdapter()
	cs := serverSession(t, mcpserver.New(adapter, eng.agentMCPOptions(adapter.limit())))

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "deploy", Arguments: map[string]any{"env": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || resultText(res) != deployRefused {
		t.Errorf("isError=%v text=%q, want %q", res.IsError, resultText(res), deployRefused)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("deploy ran with prod on the agent endpoint")
	}

	res, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "deploy", Arguments: map[string]any{"env": "staging"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("deploy staging on the agent endpoint: %q", resultText(res))
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("deploy did not run with staging")
	}
}

func TestContextHookAppliesPolicy(t *testing.T) {
	a := adapterFor(t, policySrc+"[context]\nhealth: (deploy \"prod\")\n    @echo healthy > health.done\n")
	var stderr bytes.Buffer
	want := `(context hook "health" failed; proceeding without project context)`
	if got := a.gatherContext(t.Context(), &stderr); got != want {
		t.Errorf("context = %q, want %q", got, want)
	}
	if ran := markers(t, a); len(ran) != 0 {
		t.Errorf("the hook ran %v", ran)
	}
}

// Research R15: every body of an agent run is marked, and a rune started
// with the mark is an agent run.
func TestAgentRunEnv(t *testing.T) {
	t.Setenv("RUNE_POLICY_PROBE", "here")
	inherited := agentRunEnv(nil)
	if !slices.Contains(inherited, "RUNE_POLICY_PROBE=here") || inherited[len(inherited)-1] != agentRunVar+"=1" {
		t.Errorf("agentRunEnv(nil) lost the process environment or the mark: %d entries, last %q", len(inherited), inherited[len(inherited)-1])
	}

	base := make([]string, 1, 4)
	base[0] = "A=1"
	marked := agentRunEnv(base)
	if !slices.Equal(marked, []string{"A=1", agentRunVar + "=1"}) {
		t.Errorf("agentRunEnv(base) = %v", marked)
	}
	if spare := base[:2]; spare[1] != "" {
		t.Errorf("agentRunEnv wrote into its argument: %v", spare)
	}

	a := adapterFor(t, "probe:\n    @echo \"$"+agentRunVar+"\" > probe.done\n")
	if _, errText := callText(t, a, "probe", nil); errText != "" {
		t.Fatal(errText)
	}
	if got := markerText(t, a, "probe"); got != "1" {
		t.Errorf("a body of an agent run saw %s=%q, want 1", agentRunVar, got)
	}
}

func TestCommandLineHonorsAgentMark(t *testing.T) {
	t.Setenv(agentRunVar, "1")
	src := policySrc + "ship: test (deploy \"prod\")\n    @echo shipped > ship.done\n"

	dir, _ := writeRunefile(t, src)
	_, err := runCommandLine(t, dir, "deploy", "prod")
	var failure *TaskFailure
	if !errors.As(err, &failure) || BannerMessage(err) != deployRefused {
		t.Errorf("rune deploy prod = %v, want a task failure with %q", err, deployRefused)
	}
	if got := CodeFor(err); got != ExitTaskFail {
		t.Errorf("exit code = %d, want %d", got, ExitTaskFail)
	}
	if got := doneIn(t, dir); len(got) != 0 {
		t.Errorf("a refused run ran %v", got)
	}

	_, err = runCommandLine(t, dir, "ship")
	if BannerMessage(err) != shipRefused {
		t.Errorf("rune ship = %v, want %q", err, shipRefused)
	}
	if got := doneIn(t, dir); len(got) != 0 {
		t.Errorf("a refused run ran %v", got)
	}

	// The second of two tasks on one command line is checked before the
	// first runs.
	_, err = runCommandLine(t, dir, "test", "deploy", "prod")
	if BannerMessage(err) != deployRefused {
		t.Errorf("rune test deploy prod = %v, want %q", err, deployRefused)
	}
	if got := doneIn(t, dir); len(got) != 0 {
		t.Errorf("a refused run ran %v", got)
	}

	if stderr, err := runCommandLine(t, dir, "deploy", "staging"); err != nil {
		t.Fatalf("rune deploy staging: %v\n%s", err, stderr)
	}
	if got := doneIn(t, dir); !slices.Equal(got, []string{"deploy"}) {
		t.Errorf("ran %v, want deploy", got)
	}

	// Plan modes run nothing, so there is nothing to refuse.
	var stderr bytes.Buffer
	if err := Run(Options{Cwd: dir, Stdout: &bytes.Buffer{}, Stderr: &stderr, DryRun: true}, []string{"deploy", "prod"}); err != nil {
		t.Errorf("rune --dry-run deploy prod: %v", err)
	}

	const secret = "hunter2-policy-secret"
	masked, _ := writeRunefile(t, "[env(\"DEMO_API_TOKEN\", \""+secret+"\")]\n[agent-allow(channel=\"beta\")]\ntag channel:\n    @echo tagged > tag.done\n")
	_, err = runCommandLine(t, masked, "tag", secret)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), `channel="***"`) {
		t.Errorf("the refusal is not masked: %v", err)
	}
}

// A dotenv file and an exported variable of the mark's name do not take the
// mark from the bodies of a command-line agent run, as they do not on a tool
// call: a rune such a body starts is still an agent run.
func TestCommandLineKeepsAgentMark(t *testing.T) {
	probe := "probe:\n    @echo \"$" + agentRunVar + "\" > probe.done\n"
	cases := []struct {
		name, src, dotenv string
	}{
		{"dotenv", "set dotenv := \".env\"\n" + probe, agentRunVar + "=0\n"},
		{"export", "set export\n" + agentRunVar + " := \"0\"\n" + probe, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(mark string) string {
				t.Helper()
				t.Setenv(agentRunVar, mark)
				dir, _ := writeRunefile(t, c.src)
				if c.dotenv != "" {
					if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(c.dotenv), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if stderr, err := runCommandLine(t, dir, "probe"); err != nil {
					t.Fatalf("rune probe: %v\n%s", err, stderr)
				}
				return markerText(t, &mcpAdapter{workDir: dir}, "probe")
			}
			if got := run("1"); got != "1" {
				t.Errorf("a body of an agent run saw %s=%q, want 1", agentRunVar, got)
			}
			// A person's run is not marked, so the file's own value stands.
			if got := run(""); got != "0" {
				t.Errorf("a body of a person's run saw %s=%q, want 0", agentRunVar, got)
			}
		})
	}
}

// US4: approval and grants answer another question.
func TestPolicyBeforeApproval(t *testing.T) {
	src := "[confirm(\"Release for real?\")]\n[agent-allow(env=\"staging\")]\nrelease env:enum(\"staging\",\"prod\"):\n    @echo {{env}} > release.done\n"
	a := adapterFor(t, src)
	who := &asked{}
	want := `task "release" is not allowed for agents with env="prod"; agent policy requires env="staging"`
	_, err := a.CallApproved(t.Context(), "release", map[string]string{"env": "prod"}, nil, []string{"release"}, who.approve)
	if err == nil || err.Error() != want {
		t.Errorf("CallApproved with prod = %v, want %q", err, want)
	}
	if len(who.requests) != 0 {
		t.Errorf("the person was asked about a call the policy refuses: %+v", who.requests)
	}
	if ran := markers(t, a); len(ran) != 0 {
		t.Errorf("a refused call ran %v", ran)
	}

	res, err := a.CallApproved(t.Context(), "release", map[string]string{"env": "staging"}, nil, []string{"release"}, who.approve)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("CallApproved with staging: exit %d, %v", res.ExitCode, err)
	}
	if len(who.requests) != 1 || markerText(t, a, "release") != "staging" {
		t.Errorf("requests = %d, ran %v", len(who.requests), markers(t, a))
	}
}

func TestGrantDoesNotLiftPolicy(t *testing.T) {
	src := "[confirm]\n[agent-allow(env=\"staging\")]\nrelease env:enum(\"staging\",\"prod\"):\n    @echo {{env}} > release.done\n"
	a := adapterFor(t, src)
	a.grants = map[string]bool{"release": true}
	want := `task "release" is not allowed for agents with env="prod"; agent policy requires env="staging"`
	if _, got := callText(t, a, "release", map[string]string{"env": "prod"}); got != want {
		t.Errorf("granted release with prod: error = %q, want %q", got, want)
	}
	if ran := markers(t, a); len(ran) != 0 {
		t.Errorf("a refused call ran %v", ran)
	}
	if res, errText := callText(t, a, "release", map[string]string{"env": "staging"}); errText != "" || res.ExitCode != 0 {
		t.Fatalf("granted release with staging: exit %d, %q", res.ExitCode, errText)
	}
	if got := markerText(t, a, "release"); got != "staging" {
		t.Errorf("release.done = %q", got)
	}
}

// FR-011, SC-005: a Runefile with no policy is not walked.
func TestNoPolicyNoWalk(t *testing.T) {
	src := "dep x:\n    @echo dep > dep.done\nbuild: (dep env(\"RUNE_NO_SUCH_SETTING\"))\n    @echo built > build.done\nplain:\n    @echo plain > plain.done\n"
	eng, _ := agentEngine(t, src)
	for name, task := range eng.tasks {
		if d := eng.preflightPolicy(task, eval.Args{Values: map[string]string{"x": "1"}}); d != nil {
			t.Errorf("preflightPolicy(%s) = %+v on a Runefile with no policy", name, d)
		}
	}

	a := adapterFor(t, src)
	res, errText := callText(t, a, "build", nil)
	if errText != "" || res.ExitCode == 0 {
		t.Errorf("build: exit %d, error %q: the unresolvable dependency should fail in the run, as before", res.ExitCode, errText)
	}
	if ran := markers(t, a); len(ran) != 0 {
		t.Errorf("ran %v", ran)
	}
}

// US6: what the adapter tells the server about a restricted parameter.
func TestAdapterTasksAgentValues(t *testing.T) {
	src := policySrc +
		"[agent-allow(channel=\"beta\")]\ntag channel name=\"nightly\":\n    @echo tagged\n" +
		"[agent-allow(env=\"staging\")]\ndyn env=env(\"RUNE_POLICY_TEST\", \"prod\"):\n    @echo dyn\n"
	params := map[string]mcpserver.ParamInfo{}
	for _, task := range adapterFor(t, src).Tasks() {
		for _, p := range task.Params {
			params[task.Name+"."+p.Name] = p
		}
	}
	cases := []struct {
		param    string
		values   []string
		required bool
	}{
		{"deploy.env", []string{"staging"}, true},
		{"deploy.tag", nil, false},
		{"rollout.env", []string{"staging"}, true},
		{"rollout.region", []string{"eu-west-1"}, false}, // the default is the allowed value
		{"publish.env", []string{"staging"}, true},       // the literal default is refused
		{"tag.channel", []string{"beta"}, true},
		{"tag.name", nil, false},
		{"dyn.env", []string{"staging"}, false}, // the default is not known when tools are listed
	}
	for _, c := range cases {
		p, ok := params[c.param]
		if !ok {
			t.Errorf("no parameter %s", c.param)
			continue
		}
		if !slices.Equal(p.AgentValues, c.values) || (p.AgentValues == nil) != (c.values == nil) {
			t.Errorf("%s: AgentValues = %v, want %v", c.param, p.AgentValues, c.values)
		}
		if p.Required != c.required {
			t.Errorf("%s: Required = %v, want %v", c.param, p.Required, c.required)
		}
	}
	if got := params["deploy.env"].Enum; !slices.Equal(got, []string{"staging", "prod"}) {
		t.Errorf("deploy.env keeps Enum %v, want both values", got)
	}
}
