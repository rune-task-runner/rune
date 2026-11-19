package agentdocs

import (
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/parser"
)

// parseTasks reads Runefile text into tasks. Nothing is composed or
// analyzed: a test that needs a module task sets the name itself.
func parseTasks(t *testing.T, src string) []*ast.Task {
	t.Helper()
	file, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("the test Runefile does not parse: %v", diags)
	}
	return file.Tasks
}

// buildFrom builds the catalog of src with a walk that follows every edge
// once, which is all these tests need of a reach.
func buildFrom(t *testing.T, src string, commands ...string) Catalog {
	t.Helper()
	tasks := parseTasks(t, src)
	byName := map[string]*ast.Task{}
	for _, task := range tasks {
		byName[task.Name] = task
	}
	reach := func(start *ast.Task) []*ast.Task {
		out := []*ast.Task{start}
		for i := 0; i < len(out); i++ {
			for _, e := range out[i].Edges() {
				if next := byName[e.Name]; next != nil && !slices.Contains(out, next) {
					out = append(out, next)
				}
			}
		}
		return out
	}
	cmds := map[string]bool{}
	for _, c := range commands {
		cmds[c] = true
	}
	return Build(Source{Tasks: tasks, Reach: reach, Commands: cmds})
}

func entry(t *testing.T, c Catalog, name string) Entry {
	t.Helper()
	for _, e := range c.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("the catalog has no entry %q", name)
	return Entry{}
}

func names(c Catalog) []string {
	var out []string
	for _, e := range c.Entries {
		out = append(out, e.Name)
	}
	return out
}

func TestBuildVisibility(t *testing.T) {
	tasks := parseTasks(t, `
build:
    @true

[private]
secret:
    @true

_helper:
    @true

[context]
health:
    @true

[windows]
winonly:
    @true

mod_helper:
    @true
`)
	// What Compose does to a task of the module ops that is named _helper:
	// the full name no longer starts with an underscore.
	tasks[len(tasks)-1].Name = "ops::_helper"
	c := Build(Source{Tasks: tasks})
	if got, want := names(c), []string{"build", "winonly", "ops::_helper"}; !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestBuildOrder(t *testing.T) {
	c := buildFrom(t, `
[group("b")]
b1:
    @true

plain1:
    @true

[group("a")]
a1:
    @true

[group("b")]
b2:
    @true

plain2:
    @true
`)
	if got, want := names(c), []string{"b1", "b2", "plain1", "plain2", "a1"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if got := entry(t, c, "b2").Group; got != "b" {
		t.Errorf("group of b2 = %q", got)
	}
}

func TestBuildFields(t *testing.T) {
	c := buildFrom(t, `
# Deploy the service.
# To one environment.
[group("ops")]
[linux]
[macos]
[linux]
[returns("The URL.")]
[confirm("Deploy now?")]
[readonly]
[network]
deploy:
    @true

[confirm]
wipe:
    @true

[plan(file="tfplan")]
plan:
    @true

[applies("plan")]
apply:
    @true

log:
    @true
`, "log")
	d := entry(t, c, "deploy")
	if d.Doc != "Deploy the service. To one environment." {
		t.Errorf("Doc = %q", d.Doc)
	}
	if d.Group != "ops" || d.Returns != "The URL." || !d.Confirm || d.Question != "Deploy now?" || !d.ReadOnly || !d.Network {
		t.Errorf("deploy = %+v", d)
	}
	if !slices.Equal(d.OS, []string{"linux", "macos"}) {
		t.Errorf("OS = %v, want each system once in source order", d.OS)
	}
	if d.Separator || d.Plan || d.Applies != "" {
		t.Errorf("deploy has a field of another task: %+v", d)
	}
	if w := entry(t, c, "wipe"); !w.Confirm || w.Question != "" {
		t.Errorf("wipe = %+v, want a confirmation with no question", w)
	}
	if !entry(t, c, "plan").Plan {
		t.Error("plan is not marked as a plan task")
	}
	if got := entry(t, c, "apply").Applies; got != "plan" {
		t.Errorf("apply applies %q", got)
	}
	if !entry(t, c, "log").Separator {
		t.Error("a task named as a command has no separator")
	}
}

func TestBuildParams(t *testing.T) {
	c := buildFrom(t, `
region := "eu"

[param-doc("env", "Where to deploy.")]
[agent-allow(env="staging", tag="v1")]
deploy env:enum("staging","prod") n:number tag="latest" where=region stamp=env("STAMP", "x"):
    @true

pack +files:path:
    @true

sweep *names:
    @true
`)
	p := append(entry(t, c, "deploy").Params, entry(t, c, "pack").Params...)
	if len(p) != 6 {
		t.Fatalf("deploy has %d parameters", len(p))
	}
	staging, v1 := "staging", "v1"
	want := []Param{
		{Name: "env", Kind: Required, Type: "enum", Enum: []string{"staging", "prod"}, Doc: "Where to deploy.", AgentValue: &staging},
		{Name: "n", Kind: Required, Type: "number"},
		{Name: "tag", Kind: Optional, HasDefault: true, Literal: true, Default: "latest", AgentValue: &v1},
		{Name: "where", Kind: Optional, HasDefault: true, Default: "region"},
		{Name: "stamp", Kind: Optional, HasDefault: true, Default: `env("STAMP", "x")`},
		{Name: "files", Kind: OneOrMore, Type: "path"},
	}
	for i := range want {
		if !reflect.DeepEqual(p[i], want[i]) {
			t.Errorf("parameter %d = %+v, want %+v", i, p[i], want[i])
		}
		if p[i].HasDefault != (p[i].Kind == Optional) {
			t.Errorf("parameter %s: HasDefault and Optional disagree", p[i].Name)
		}
	}
	if got := entry(t, c, "sweep").Params[0]; got.Kind != ZeroOrMore || got.AgentValue != nil {
		t.Errorf("sweep parameter = %+v", got)
	}
}

func TestBuildReaches(t *testing.T) {
	c := buildFrom(t, `
[confirm]
[agent-allow(env="staging", region="eu")]
deploy env region:
    @true

test:
    @true

[confirm("Wipe?")]
_wipe:
    @true

ship: test (deploy "prod" "eu") && _wipe
    @true

loner:
    @true
`)
	want := []Reached{
		{Name: "deploy", Confirm: true, Allow: []Allowed{{"env", "staging"}, {"region", "eu"}}},
		{Name: "_wipe", Confirm: true},
	}
	if got := entry(t, c, "ship").Reaches; !reflect.DeepEqual(got, want) {
		t.Errorf("ship reaches %+v, want %+v", got, want)
	}
	// The entry's own limits are on the entry, not among what it reaches.
	if got := entry(t, c, "deploy").Reaches; got != nil {
		t.Errorf("deploy reaches %+v, want nothing", got)
	}
	if got := entry(t, c, "loner").Reaches; got != nil {
		t.Errorf("loner reaches %+v", got)
	}
	// With no walk given, nothing is reached.
	if got := Build(Source{Tasks: parseTasks(t, "a: b\n    @true\n[confirm]\nb:\n    @true\n")}).Entries[0].Reaches; got != nil {
		t.Errorf("without a walk, a reaches %+v", got)
	}
}

func TestUsage(t *testing.T) {
	c := buildFrom(t, `
build:
    @true

deploy env region="eu" *files:
    @true

pack +files:
    @true

sweep *names:
    @true

log level:
    @true

restart:
    @true
`, "log")
	c.Entries[5].Name = "ops::restart"
	want := map[string]string{
		"build":        "rune build",
		"deploy":       "rune deploy <env> [region] [files]...",
		"pack":         "rune pack <files>...",
		"sweep":        "rune sweep [names]...",
		"log":          "rune -- log <level>",
		"ops::restart": "rune ops::restart",
	}
	for _, e := range c.Entries {
		if got := e.Usage(); got != want[e.Name] {
			t.Errorf("usage of %s = %q, want %q", e.Name, got, want[e.Name])
		}
	}
}

func TestExample(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"no parameter", "t:\n    @true\n", ""},
		{"only optional parameters", "t a=\"x\" b:enum(\"y\")=\"y\":\n    @true\n", ""},
		{"first enum value", "t env:enum(\"staging\",\"prod\"):\n    @true\n", "rune t staging"},
		{"agent value wins over the enum", "[agent-allow(env=\"prod\")]\nt env:enum(\"staging\",\"prod\"):\n    @true\n", "rune t prod"},
		{"required boolean", "t force:boolean:\n    @true\n", "rune t true"},
		{"a default is kept before an agent value", "[agent-allow(env=\"staging\")]\nt force:boolean=\"false\" env=\"prod\":\n    @true\n", "rune t false staging"},
		{"a default before an agent value", "[agent-allow(env=\"staging\")]\nt a=\"x y\" env=\"prod\":\n    @true\n", "rune t 'x y' staging"},
		{"a default that is the agent value", "[agent-allow(env=\"staging\")]\nt env=\"staging\":\n    @true\n", ""},
		{"a computed default with an agent value", "[agent-allow(env=\"staging\")]\nt env=env(\"E\", \"x\"):\n    @true\n", "rune t staging"},
		{"free string", "t name:\n    @true\n", ""},
		{"number", "t n:number:\n    @true\n", ""},
		{"path", "t p:path:\n    @true\n", ""},
		{"raw", "t r:raw:\n    @true\n", ""},
		{"a computed default in the range", "[agent-allow(env=\"staging\")]\nt a=env(\"A\", \"x\") env=\"prod\":\n    @true\n", ""},
		{"an enum fills in for a computed default", "[agent-allow(env=\"staging\")]\nt a:enum(\"p\",\"q\")=env(\"A\", \"p\") env=\"prod\":\n    @true\n", "rune t p staging"},
		{"an unsafe value", "t env:enum(\"a`b\"):\n    @true\n", ""},
		{"a value that looks like a flag", "t env:enum(\"--yes\", \"no\"):\n    @true\n", ""},
		{"quoted values", "t a:enum(\"us east\") b:enum(\"it's\") c:enum(\"a;b\") d:enum(\"\"):\n    @true\n", `rune t 'us east' 'it'\''s' 'a;b' ''`},
		{"one value for a variadic", "t env:enum(\"s\") +f:enum(\"y\",\"z\"):\n    @true\n", "rune t s y"},
		{"nothing for zero or more", "t env:enum(\"s\") *f:\n    @true\n", "rune t s"},
		{"trailing optional parameters are left out", "t env:enum(\"s\") region=\"eu\":\n    @true\n", "rune t s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := entry(t, buildFrom(t, c.src), "t").Example(); got != c.want {
				t.Errorf("example = %q, want %q", got, c.want)
			}
		})
	}
	if got := entry(t, buildFrom(t, "log env:enum(\"staging\"):\n    @true\n", "log"), "log").Example(); got != "rune -- log staging" {
		t.Errorf("example of a command name = %q", got)
	}
}

func TestFingerprint(t *testing.T) {
	base := `
# Deploy.
[group("ops")]
[linux]
[returns("URL")]
[confirm("Sure?")]
[param-doc("env", "Where.")]
[agent-allow(env="staging")]
deploy env:enum("staging","prod") tag="latest":
    @echo deploy

[agent-allow(x="1")]
_inner x:
    @true

ship: (_inner "1")
    @true

_unreached:
    @echo a
`
	fp := buildFrom(t, base).Fingerprint()
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(fp) {
		t.Fatalf("fingerprint = %q", fp)
	}
	if again := buildFrom(t, base).Fingerprint(); again != fp {
		t.Error("two builds of one Runefile have different fingerprints")
	}
	replace := func(old, new string) string {
		t.Helper()
		if !regexp.MustCompile(regexp.QuoteMeta(old)).MatchString(base) {
			t.Fatalf("the base Runefile has no %q", old)
		}
		return regexp.MustCompile(regexp.QuoteMeta(old)).ReplaceAllLiteralString(base, new)
	}
	changes := map[string]string{
		"name":          replace("ship:", "shipit:"),
		"description":   replace("# Deploy.", "# Deploy it."),
		"type":          replace(`env:enum("staging","prod")`, `env:enum("staging","live")`),
		"default":       replace(`tag="latest"`, `tag="stable"`),
		"parameter doc": replace(`"Where."`, `"Where to."`),
		"group":         replace(`group("ops")`, `group("run")`),
		"system":        replace("[linux]", "[macos]"),
		"returns":       replace(`returns("URL")`, `returns("The URL")`),
		"question":      replace(`confirm("Sure?")`, `confirm("Really?")`),
		"agent value":   replace(`agent-allow(env="staging")`, `agent-allow(env="prod")`),
		"reached limit": replace(`agent-allow(x="1")`, `agent-allow(x="2")`),
	}
	for what, src := range changes {
		if buildFrom(t, src).Fingerprint() == fp {
			t.Errorf("a change of the %s leaves the fingerprint as it was", what)
		}
	}
	same := map[string]string{
		"task body":          replace("@echo deploy", "@echo deploying"),
		"unreached private":  replace("@echo a", "@echo b"),
		"a plain dependency": replace(`ship: (_inner "1")`, `ship: (_inner "1") _unreached`),
		"a comment":          replace("\n[agent-allow(x=\"1\")]\n_inner", "\n\n# About nothing.\n\n[agent-allow(x=\"1\")]\n_inner"),
	}
	for what, src := range same {
		if buildFrom(t, src).Fingerprint() != fp {
			t.Errorf("a change of %s changes the fingerprint", what)
		}
	}
}

// A description that is wrapped at another column is the same description:
// the text shows the same words, so the fingerprint is the same (a commit
// that only re-wraps a comment does not make an export stale).
func TestFingerprintIgnoresTheWrappingOfADescription(t *testing.T) {
	wrapped := buildFrom(t, "# Build the\n# project.\n[returns(\"The  binary.\")]\n[param-doc(\"mode\", \"Fast\\nor slow.\")]\nbuild mode:\n    @true\n")
	oneLine := buildFrom(t, "# Build the project.\n[returns(\"The binary.\")]\n[param-doc(\"mode\", \"Fast or slow.\")]\nbuild mode:\n    @true\n")
	if wrapped.Fingerprint() != oneLine.Fingerprint() {
		t.Errorf("the entries are %+v and %+v", wrapped.Entries, oneLine.Entries)
	}
	if entryText(wrapped.Entries[0]) != entryText(oneLine.Entries[0]) {
		t.Error("the two Runefiles do not give the same text, so the test compares nothing")
	}
}
