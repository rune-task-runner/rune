package parser

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
)

var update = flag.Bool("update", false, "regenerate golden AST dumps")

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, diags := Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics:\n%v", diags)
	}
	return f
}

func TestParseSettingAndAssignment(t *testing.T) {
	f := mustParse(t, "set default := \"greet\"\nbuild_dir := \"dist\"\n")
	if len(f.Settings) != 1 || f.Settings[0].Name != "default" {
		t.Fatalf("settings = %+v", f.Settings)
	}
	if len(f.Assignments) != 1 || f.Assignments[0].Name != "build_dir" {
		t.Fatalf("assignments = %+v", f.Assignments)
	}
}

func TestParseTaskWithParamsDepsBody(t *testing.T) {
	src := "# Say hello.\ngreet name=\"world\":\n    @echo \"hi {{name}}\"\n\nbuild: greet\n    @echo build\n"
	f := mustParse(t, src)
	if len(f.Tasks) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(f.Tasks))
	}
	greet := f.Tasks[0]
	if greet.Name != "greet" {
		t.Errorf("name = %q", greet.Name)
	}
	if greet.Doc != "Say hello." {
		t.Errorf("doc = %q", greet.Doc)
	}
	if len(greet.Params) != 1 || greet.Params[0].Kind != ast.ParamDefaulted {
		t.Errorf("params = %+v", greet.Params)
	}
	if len(greet.Body) != 1 || !greet.Body[0].NoEcho {
		t.Errorf("body = %+v", greet.Body)
	}
	build := f.Tasks[1]
	if len(build.Deps) != 1 || build.Deps[0].Name != "greet" {
		t.Errorf("deps = %+v", build.Deps)
	}
}

func TestParseExecutor(t *testing.T) {
	f := mustParse(t, "analyze (python):\n    print(1)\n")
	if f.Tasks[0].Executor != "python" {
		t.Errorf("executor = %q", f.Tasks[0].Executor)
	}
}

func TestParseVariadicAndDefaultedOrder(t *testing.T) {
	f := mustParse(t, "run cmd +args:\n    echo {{cmd}}\n")
	ps := f.Tasks[0].Params
	if len(ps) != 2 {
		t.Fatalf("params = %+v", ps)
	}
	if ps[0].Kind != ast.ParamRequired || ps[1].Kind != ast.ParamVariadicPlus {
		t.Errorf("param kinds = %v, %v", ps[0].Kind, ps[1].Kind)
	}
}

func TestParseDepWithArgs(t *testing.T) {
	f := mustParse(t, "all:\n    echo x\nx: (greet \"Ada\")\n    echo y\n")
	x := f.Tasks[1]
	if len(x.Deps) != 1 || x.Deps[0].Name != "greet" || len(x.Deps[0].Args) != 1 {
		t.Fatalf("dep = %+v", x.Deps)
	}
}

func TestParsePostHook(t *testing.T) {
	f := mustParse(t, "deploy: build && notify\n    echo deploy\n")
	d := f.Tasks[0]
	if len(d.Deps) != 1 || d.Deps[0].Name != "build" {
		t.Errorf("deps = %+v", d.Deps)
	}
	if len(d.PostHooks) != 1 || d.PostHooks[0].Name != "notify" {
		t.Errorf("posthooks = %+v", d.PostHooks)
	}
}

func TestParseAttributes(t *testing.T) {
	src := "[private]\n[confirm(\"sure?\")]\nclean:\n    rm -rf dist\n"
	f := mustParse(t, src)
	tk := f.Tasks[0]
	if !tk.IsPrivate() {
		t.Error("expected private")
	}
	if tk.Attr(ast.AttrConfirm) == nil || tk.Attr(ast.AttrConfirm).Str != "sure?" {
		t.Errorf("confirm attr = %+v", tk.Attr(ast.AttrConfirm))
	}
}

func TestParseCacheAttr(t *testing.T) {
	src := "[cache(inputs = [\"go.mod\", \"**/*.go\"], outputs = [\"dist/rune\"])]\nbuild:\n    go build\n"
	f := mustParse(t, src)
	c := f.Tasks[0].Attr(ast.AttrCache)
	if c == nil || len(c.Inputs) != 2 || !c.HasOutputs || len(c.Outputs) != 1 {
		t.Fatalf("cache attr = %+v", c)
	}
}

func TestParseGolden(t *testing.T) {
	matches, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "parser", "*.rune"))
	if len(matches) == 0 {
		t.Skip("no golden fixtures yet")
	}
	for _, in := range matches {
		in := in
		t.Run(filepath.Base(in), func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			f, diags := Parse(filepath.Base(in), string(src))
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics:\n%v", diags)
			}
			got := ast.Dump(f)
			golden := strings.TrimSuffix(in, ".rune") + ".ast"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden %s (run with -update): %v", golden, err)
			}
			if got != string(want) {
				t.Errorf("AST mismatch for %s:\n got:\n%s\nwant:\n%s", in, got, want)
			}
		})
	}
}

func TestParseFailHook(t *testing.T) {
	f := mustParse(t, "test: build && notify || diagnose\n    echo test\n")
	tk := f.Tasks[0]
	if len(tk.Deps) != 1 || tk.Deps[0].Name != "build" {
		t.Errorf("deps = %+v", tk.Deps)
	}
	if len(tk.PostHooks) != 1 || tk.PostHooks[0].Name != "notify" {
		t.Errorf("posthooks = %+v", tk.PostHooks)
	}
	if len(tk.FailHooks) != 1 || tk.FailHooks[0].Name != "diagnose" {
		t.Errorf("failhooks = %+v", tk.FailHooks)
	}
}

func TestParseFailHookOnly(t *testing.T) {
	f := mustParse(t, "test: || diagnose report\n    echo test\n")
	tk := f.Tasks[0]
	if len(tk.Deps) != 0 || len(tk.PostHooks) != 0 {
		t.Errorf("deps/posthooks = %+v / %+v", tk.Deps, tk.PostHooks)
	}
	if len(tk.FailHooks) != 2 || tk.FailHooks[0].Name != "diagnose" || tk.FailHooks[1].Name != "report" {
		t.Errorf("failhooks = %+v", tk.FailHooks)
	}
}

func TestParseFailHookWithArgs(t *testing.T) {
	f := mustParse(t, "test: || (diagnose \"verbose\")\n    echo test\n")
	tk := f.Tasks[0]
	if len(tk.FailHooks) != 1 || tk.FailHooks[0].Name != "diagnose" || len(tk.FailHooks[0].Args) != 1 {
		t.Fatalf("failhooks = %+v", tk.FailHooks)
	}
}

// TestParseFailHookClauseOrderFixed: the || clause must come last —
// "|| ... && ..." is a parse error (contracts/grammar.md).
func TestParseFailHookClauseOrderFixed(t *testing.T) {
	_, diags := Parse("Runefile", "test: a || c && b\n    echo test\n")
	if !diags.HasErrors() {
		t.Fatal("expected parse error for '|| ... && ...' clause order, got none")
	}
}
