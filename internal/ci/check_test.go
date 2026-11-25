package ci

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/language"
)

// refusals builds the graph and returns the messages of its refusal.
func refusals(t *testing.T, src string, roots ...string) []string {
	t.Helper()
	_, _, err := build(t, src, "linux", nil, roots...)
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	var msgs []string
	for _, d := range r.Diags {
		msgs = append(msgs, d.Message)
	}
	return msgs
}

func wantRefusal(t *testing.T, msgs []string, parts ...string) {
	t.Helper()
	for _, m := range msgs {
		if containsAll(m, parts...) {
			return
		}
	}
	t.Errorf("no refusal with %q in %q", parts, msgs)
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func TestRefusesTasksThatNeedAPersonOrAnAgent(t *testing.T) {
	src := `[confirm("Deploy?")]
deploy:
    @echo d

[plan(file="p")]
plan:
    @echo p

[applies("plan")]
apply:
    @echo a

review (agent):
    look

test: || diagnose
    @echo t

[confirm]
diagnose:
    @echo x
`
	msgs := refusals(t, src, "deploy", "apply", "review", "test")
	wantRefusal(t, msgs, `"deploy" has [confirm]`, "no person to ask")
	wantRefusal(t, msgs, `"apply" has [applies]`)
	wantRefusal(t, msgs, `"review" runs with the agent executor`)
	wantRefusal(t, msgs, `"diagnose" has [confirm]`)
}

func TestRefusalSpansPointAtTheAttribute(t *testing.T) {
	_, _, err := build(t, "[confirm]\ndeploy:\n    @echo d\n", "linux", nil, "deploy")
	var r *Refusal
	if !errors.As(err, &r) || len(r.Diags) != 1 {
		t.Fatalf("err = %v", err)
	}
	if sp := r.Diags[0].Span; sp.Start.Line != 1 {
		t.Errorf("span = %+v, want the attribute on line 1", sp)
	}
}

func TestRefusesFailureHookWithDependencies(t *testing.T) {
	src := `setup:
    @echo s

diagnose: setup
    @echo d

notify:
    @echo n

report: && notify
    @echo r

test: || diagnose
    @echo t

lint: || report
    @echo l
`
	msgs := refusals(t, src, "test", "lint")
	wantRefusal(t, msgs, `failure hook "diagnose" of task "test"`)
	wantRefusal(t, msgs, `failure hook "report" of task "lint"`)
}

func TestRefusesTextAJobCannotCarry(t *testing.T) {
	src := "t x:\n    @echo {{x}}\n"
	for _, arg := range []string{"a${{ github.token }}", "a\tb", "a\nb"} {
		tasks, assigns := parse(t, src)
		r := &fakeResolver{tasks: tasks, scope: eval.NewScope(assigns, map[string]string{}), unavailable: map[string]bool{}}
		_, buildErr := Build(Input{OS: "linux", Resolver: r, Tasks: tasks, Assigns: assigns, Roots: []Root{{Task: tasks["t"], Args: []string{arg}}}})
		var ref *Refusal
		if !errors.As(buildErr, &ref) || !strings.Contains(ref.Error(), `an argument of task "t"`) {
			t.Errorf("%q: err = %v, want a refusal", arg, buildErr)
		}
	}
}

func TestRefusesLiteralOutputsOutsideTheProject(t *testing.T) {
	src := `[cache(inputs=["a"], outputs=["/abs", "../up", "dist/**", "ok"])]
build:
    @echo b
`
	msgs := refusals(t, src, "build")
	wantRefusal(t, msgs, `"/abs" is outside the project`)
	wantRefusal(t, msgs, `"../up" is outside the project`)
	if len(msgs) != 2 {
		t.Errorf("refusals = %q, want two", msgs)
	}
}

func TestRefusesBackgroundPrerequisite(t *testing.T) {
	src := `server port="8080":
    @echo start
    ./serve {{port}} &

test: server
    @echo t

[script("python3")]
py:
    print("x") &

odd:
    echo "unclosed &

alone:
    sleep 1 &
`
	msgs := refusals(t, src, "test")
	wantRefusal(t, msgs, `"server" starts a background process`)

	// A root nothing needs, an interpreter body, and a line that does not
	// parse are not judged.
	for _, root := range []string{"alone", "py", "odd"} {
		if _, _, err := build(t, src, "linux", nil, root); err != nil {
			t.Errorf("%s: %v", root, err)
		}
	}
}

func TestBackgroundLineIgnoresOtherExecutors(t *testing.T) {
	tasks, _ := parse(t, "p (python):\n    x &\n")
	if line := backgroundLine(tasks["p"]); line != nil {
		t.Errorf("a python body was judged as shell: %v", line.Raw)
	}
}

func TestRefusesMachineDependentArguments(t *testing.T) {
	for _, call := range []string{
		`env("HOME")`, `arch()`, `num_cpus()`, `path_exists("x")`, `read("x")`,
		`sha256_file("x")`, `absolute_path("x")`, `require("sh")`, `which("sh")`,
		`datetime()`, `uuid()`,
	} {
		name := call[:strings.IndexByte(call, '(')]
		src := "home := " + call + "\nlabel := \"at-\" + home\n\nt x:\n    @echo {{x}}\n\ntop: (t label)\n    @echo\n"
		msgs := refusals(t, src, "top")
		wantRefusal(t, msgs, name+"() reads this machine", `task "top"`)
	}
}

func TestRefusesMachineDependentDefaults(t *testing.T) {
	src := `t x=env("X"):
    @echo {{x}}

top: t
    @echo
`
	wantRefusal(t, refusals(t, src, "top"), "env() reads this machine", `task "t"`)
	wantRefusal(t, refusals(t, src, "t"), "env() reads this machine")

	// A supplied value means the default is never evaluated.
	src2 := "t x=env(\"X\"):\n    @echo {{x}}\n\ntop: (t \"given\")\n    @echo\n"
	if _, _, err := build(t, src2, "linux", nil, "top"); err != nil {
		t.Errorf("a default that is not used was refused: %v", err)
	}
	if _, _, err := build(t, src2, "linux", nil, "t given"); err != nil {
		t.Errorf("a root default that is not used was refused: %v", err)
	}
}

func TestOSIsEvaluatedForTheTarget(t *testing.T) {
	src := "t x:\n    @echo {{x}}\n\ntop: (t os() + \"-\" + os_family())\n    @echo\n"
	for goos, want := range map[string]string{"linux": "linux-unix", "darwin": "darwin-unix"} {
		g, _, err := build(t, src, goos, nil, "top")
		if err != nil {
			t.Fatal(err)
		}
		if got := g.Nodes[0].Argv[0]; got != want {
			t.Errorf("%s: argument = %q, want %q", goos, got, want)
		}
	}
}

func TestParameterReferencesAreNotWalkedAsVariables(t *testing.T) {
	src := "x := env(\"X\")\n\nt v:\n    @echo {{v}}\n\ntop x=\"given\": (t x)\n    @echo\n"
	if _, _, err := build(t, src, "linux", nil, "top"); err != nil {
		t.Errorf("a parameter that shadows a variable was walked as the variable: %v", err)
	}
}

// Inside a variable's expression a parameter's name is the variable of that
// name, as eval resolves it, so a machine read behind it is refused.
func TestVariablesDoNotSeeParameters(t *testing.T) {
	src := "x := env(\"X\")\nlabel := \"at-\" + x\n\nt v:\n    @echo {{v}}\n\ntop x=\"given\": (t label)\n    @echo\n"
	wantRefusal(t, refusals(t, src, "top"), "env() reads this machine", `task "top"`)
}

func TestEveryBuiltinIsClassified(t *testing.T) {
	for _, b := range language.OfKind(language.BuiltinFunction) {
		if machineBuiltins[b.Name] == pureBuiltins[b.Name] {
			t.Errorf("built-in %s must be in exactly one of machineBuiltins and pureBuiltins", b.Name)
		}
	}
	known := slices.Collect(maps.Keys(machineBuiltins))
	known = append(known, slices.Collect(maps.Keys(pureBuiltins))...)
	for _, name := range known {
		if _, ok := language.Lookup(language.BuiltinFunction, name); !ok {
			t.Errorf("%s is classified but is not a built-in", name)
		}
	}
}

func TestCheckText(t *testing.T) {
	for _, ok := range []string{"", "plain", "a b", "it's", "$HOME", "${HOME}", "{{x}}"} {
		if err := CheckText(ok); err != nil {
			t.Errorf("CheckText(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"${{x}}", "a\x00", "a\r", "a\n", "a\tb"} {
		if err := CheckText(bad); err == nil {
			t.Errorf("CheckText(%q) accepted it", bad)
		}
	}
}

func TestCheckOutput(t *testing.T) {
	for _, ok := range []string{"dist", "dist/app", "**/*.o", "a/../b"} {
		if err := CheckOutput(ok); err != nil {
			t.Errorf("CheckOutput(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"/abs", "..", "../x", "a/../../x", ""} {
		if err := CheckOutput(bad); err == nil {
			t.Errorf("CheckOutput(%q) accepted it", bad)
		}
	}
}

func TestMachineDependenceInsideExpressions(t *testing.T) {
	src := "t x:\n    @echo {{x}}\n\ntop: (t if os() == \"linux\" { \"a\" } else { which(\"sh\") })\n    @echo\n"
	wantRefusal(t, refusals(t, src, "top"), "which() reads this machine")
}

func TestARefusalIsReportedOnce(t *testing.T) {
	src := "t x=env(\"X\"):\n    @echo {{x}}\n\na: t\n    @echo\n\nb: t\n    @echo\n"
	if msgs := refusals(t, src, "a", "b"); len(msgs) != 1 {
		t.Errorf("refusals = %q, want one", msgs)
	}
}
