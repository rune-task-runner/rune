package eval

import (
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/parser"
	"github.com/rune-task-runner/rune/internal/token"
)

// evalFragment parses and evaluates a standalone expression against the scope.
func evalFragment(t *testing.T, src string, scope *Scope) (string, *Error) {
	t.Helper()
	expr, diags := parser.ParseExprFragment("test", src)
	if diags.HasErrors() {
		t.Fatalf("parse error for %q: %v", src, diags)
	}
	return New(scope).Eval(expr)
}

func newTestScope() *Scope {
	s := NewScope(map[string]*ast.Assignment{}, map[string]string{})
	s.GOOS = "linux"
	s.Arch = "arm64"
	s.Env = func(k string) (string, bool) {
		if k == "HOME" {
			return "/home/ada", true
		}
		return "", false
	}
	return s
}

func TestEvalLiteralsAndConcat(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{`"hello"`, "hello"},
		{`"a" + "b"`, "ab"},
		{`"a" + "b" + "c"`, "abc"},
		{`"dist" / "rune"`, "dist/rune"},
		{`"a/" / "/b"`, "a/b"},
		{`uppercase("abc")`, "ABC"},
		{`lowercase("ABC")`, "abc"},
		{`trim("  x  ")`, "x"},
		{`replace("a-b-c", "-", "_")`, "a_b_c"},
		{`join("a", "b", "c")`, "a/b/c"},
		{`extension("foo.tar.gz")`, "gz"},
		{`file_stem("dir/foo.txt")`, "foo"},
		{`file_name("dir/foo.txt")`, "foo.txt"},
		{`parent_dir("dir/foo.txt")`, "dir"},
		{`os()`, "linux"},
		{`arch()`, "arm64"},
		{`os_family()`, "unix"},
		{`env("HOME")`, "/home/ada"},
		{`env("MISSING", "fallback")`, "fallback"},
		{`sha256("abc")`, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	}
	scope := newTestScope()
	for _, tt := range tests {
		got, err := evalFragment(t, tt.src, scope)
		if err != nil {
			t.Errorf("%s -> error %v", tt.src, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestEvalConditional(t *testing.T) {
	scope := newTestScope()
	tests := []struct {
		src  string
		want string
	}{
		{`if "a" == "a" { "yes" } else { "no" }`, "yes"},
		{`if "a" == "b" { "yes" } else { "no" }`, "no"},
		{`if "a" != "b" { "yes" } else { "no" }`, "yes"},
		{`if "foobar" =~ "^foo" { "match" } else { "nomatch" }`, "match"},
		{`if os() == "windows" { "win" } else if os() == "linux" { "lin" } else { "other" }`, "lin"},
	}
	for _, tt := range tests {
		got, err := evalFragment(t, tt.src, scope)
		if err != nil {
			t.Errorf("%s -> error %v", tt.src, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestEvalParamsAndOverridesAndAssigns(t *testing.T) {
	// build_dir := "dist"; out := build_dir + "/bin"
	assigns := map[string]*ast.Assignment{
		"build_dir": {Name: "build_dir", Expr: &ast.StringLit{Value: "dist"}},
	}
	scope := NewScope(assigns, map[string]string{})

	// Module assignment reference.
	got, err := evalFragment(t, `build_dir / "bin"`, scope)
	if err != nil || got != "dist/bin" {
		t.Fatalf("assign ref = %q err=%v", got, err)
	}

	// Override beats assignment.
	scope2 := NewScope(assigns, map[string]string{"build_dir": "out"})
	got, _ = evalFragment(t, `build_dir`, scope2)
	if got != "out" {
		t.Errorf("override = %q, want out", got)
	}

	// Param beats both.
	withParam := scope2.WithArgs(Args{Values: map[string]string{"build_dir": "p"}})
	got, _ = evalFragment(t, `build_dir`, withParam)
	if got != "p" {
		t.Errorf("param = %q, want p", got)
	}
}

func TestEvalUndefinedVariable(t *testing.T) {
	scope := newTestScope()
	_, err := evalFragment(t, `nope`, scope)
	if err == nil {
		t.Fatal("expected undefined variable error")
	}
	if err.Span.File != "test" {
		t.Errorf("error span file = %q", err.Span.File)
	}
}

func TestEvalAssignmentCycle(t *testing.T) {
	assigns := map[string]*ast.Assignment{
		"a": {Name: "a", Expr: &ast.VarRef{Name: "b"}},
		"b": {Name: "b", Expr: &ast.VarRef{Name: "a"}},
	}
	scope := NewScope(assigns, map[string]string{})
	_, err := evalFragment(t, `a`, scope)
	if err == nil {
		t.Fatal("expected a cycle error")
	}
}

func TestInterpolate(t *testing.T) {
	scope := newTestScope().WithArgs(Args{Values: map[string]string{"name": "Ada"}})
	e := New(scope)
	base := token.Span{File: "Runefile"}
	tests := []struct {
		raw  string
		want string
	}{
		{`echo "hello, {{name}}"`, `echo "hello, Ada"`},
		{`echo {{ uppercase(name) }}`, `echo ADA`},
		{`literal {{{{name}}}}`, `literal {{name}}`},
		{`no interp here`, `no interp here`},
	}
	for _, tt := range tests {
		got, err := e.Interpolate(tt.raw, base)
		if err != nil {
			t.Errorf("%q -> error %v", tt.raw, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Interpolate(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestInterpolateUnterminated(t *testing.T) {
	e := New(newTestScope())
	_, err := e.Interpolate(`echo {{ name`, token.Span{File: "Runefile"})
	if err == nil {
		t.Fatal("expected unterminated interpolation error")
	}
}
