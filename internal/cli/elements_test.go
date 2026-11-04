package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
)

// checkJoin asserts the data-model invariant Values[k] == join(Lists[k]).
func checkJoin(t *testing.T, a eval.Args) {
	t.Helper()
	for k, elems := range a.Lists {
		if a.Values[k] != strings.Join(elems, " ") {
			t.Errorf("Values[%q] = %q, but Lists joins to %q", k, a.Values[k], strings.Join(elems, " "))
		}
	}
}

// Spec 030 FR-006: binding keeps each command-line argument as one element.
func TestBindParamsKeepsElements(t *testing.T) {
	c, scope := typedTask(t, "c *names:\n    @echo hi\n")
	for _, pos := range [][]string{{}, {"a b"}, {"a", "b"}} {
		args, err := bindParams(c, pos, scope)
		if err != nil {
			t.Fatalf("%q: %v", pos, err)
		}
		got, ok := args.Lists["names"]
		if !ok || got == nil {
			t.Fatalf("%q: Lists has no non-nil entry for names: %#v", pos, args.Lists)
		}
		if !reflect.DeepEqual(got, pos) {
			t.Errorf("%q: Lists[names] = %q", pos, got)
		}
		checkJoin(t, args)
	}

	plus, scope := typedTask(t, "c +names:\n    @echo hi\n")
	_, err := bindParams(plus, nil, scope)
	if err == nil || err.Error() != `task "c" expects at least one "names" argument` {
		t.Errorf("empty + error = %v", err)
	}

	enum, scope := typedTask(t, "t *xs:enum(\"a\",\"b\"):\n    @echo hi\n")
	if _, err := bindParams(enum, []string{"a", "c"}, scope); err == nil {
		t.Error("element outside the enum accepted")
	}
	if _, err := bindParams(enum, []string{"a", "b"}, scope); err != nil {
		t.Errorf("valid elements rejected: %v", err)
	}
}

// Spec 030 FR-006 over MCP: lists from the server are the elements; a plain
// value is one element; an omitted * is none; an empty + is still an error.
func TestBindNamedParamsElements(t *testing.T) {
	star, scope := typedTask(t, "c *names:\n    @echo hi\n")
	tests := []struct {
		name  string
		named map[string]string
		lists map[string][]string
		want  []string
	}{
		{"server lists", map[string]string{"names": "a b c"}, map[string][]string{"names": {"a b", "c"}}, []string{"a b", "c"}},
		{"plain value", map[string]string{"names": "a b"}, nil, []string{"a b"}},
		{"omitted", map[string]string{}, nil, []string{}},
		{"empty array", map[string]string{"names": ""}, map[string][]string{"names": {}}, []string{}},
		{"empty string", map[string]string{"names": ""}, nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := bindNamedParams(star, tt.named, tt.lists, scope)
			if err != nil {
				t.Fatal(err)
			}
			if got := args.Lists["names"]; got == nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Lists[names] = %#v, want %#v", got, tt.want)
			}
			checkJoin(t, args)
		})
	}

	plus, scope := typedTask(t, "c +names:\n    @echo hi\n")
	for _, named := range []map[string]string{{}, {"names": ""}} {
		if _, err := bindNamedParams(plus, named, nil, scope); err == nil {
			t.Errorf("empty + accepted: %v", named)
		}
	}

	enum, scope := typedTask(t, "deploy +hosts:enum(\"web\"):\n    @echo hi\n")
	_, err := bindNamedParams(enum, map[string]string{"hosts": "web"}, map[string][]string{"hosts": {"prod-db"}}, scope)
	if err == nil || !strings.Contains(err.Error(), `invalid value "prod-db"`) {
		t.Errorf("element outside the enum: err = %v", err)
	}
}

// Spec 030 FR-010, FR-011: forms that avoid quote_each interpolate to the
// same text as v0.6.0 (contracts/runefile-surface.md, "Unchanged forms").
func TestUnchangedFormsInterpolate(t *testing.T) {
	a, scope := typedTask(t, "a *names:\n    @echo hi\n")
	args, err := bindParams(a, []string{"yq", "x; echo pwned", "tofu"}, scope)
	if err != nil {
		t.Fatal(err)
	}
	ev := eval.New(scope.WithArgs(args))
	tests := []struct{ body, want string }{
		{`printf "[%s]\n" {{names}}`, `printf "[%s]\n" yq x; echo pwned tofu`},
		{`printf "[%s]\n" {{quote(names)}}`, `printf "[%s]\n" 'yq x; echo pwned tofu'`},
		{`echo {{"x" + names}}`, `echo xyq x; echo pwned tofu`},
	}
	for _, tt := range tests {
		got, evErr := ev.Interpolate(tt.body, a.Sp)
		if evErr != nil || got != tt.want {
			t.Errorf("%s = %q, %v; want %q", tt.body, got, evErr, tt.want)
		}
	}

	for _, pos := range [][]string{{"a", "b"}, {"a b"}} {
		args, err := bindParams(a, pos, scope)
		if err != nil {
			t.Fatal(err)
		}
		got, evErr := eval.New(scope.WithArgs(args)).Interpolate(`{{ if names == "a b" { "yes" } else { "no" } }}`, a.Sp)
		if evErr != nil || got != "yes" {
			t.Errorf("%q: conditional = %q, %v; want yes", pos, got, evErr)
		}
	}

	s, scope := typedTask(t, "s env:\n    @echo hi\n")
	args, err = bindParams(s, []string{"x; echo pwned"}, scope)
	if err != nil {
		t.Fatal(err)
	}
	if got, evErr := eval.New(scope.WithArgs(args)).Interpolate(`deploy {{env}}`, s.Sp); evErr != nil || got != "deploy x; echo pwned" {
		t.Errorf("scalar = %q, %v", got, evErr)
	}
}

// Spec 030 FR-007, FR-008 (contracts/runefile-surface.md, "Dependency
// forwarding"): a bare variadic reference fills the target's variadic element
// by element, while the target's text stays v0.6.0's.
func TestResolveDepForwardsElements(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		pos        []string
		target     string
		wantValues map[string]string
		wantList   []string
	}{
		{
			"spread", "ci *pkgs: (test pkgs)\n    @echo ci\n\ntest *pkgs:\n    @echo t\n",
			[]string{"./a", "./b c"},
			"pkgs",
			map[string]string{"pkgs": "./a ./b c"},
			[]string{"./a", "./b c"},
		},
		{
			"scalar before variadic", "release *targets: (build \"linux\" targets)\n    @echo r\n\nbuild os *targets:\n    @echo b\n",
			[]string{"a", "b"},
			"targets",
			map[string]string{"os": "linux", "targets": "a b"},
			[]string{"a", "b"},
		},
		{
			"into a scalar", "tag *names: (label names)\n    @echo t\n\nlabel text:\n    @echo l\n",
			[]string{"a", "b"},
			"",
			map[string]string{"text": "a b"},
			nil,
		},
		{
			"per-element enum", "ci *pkgs: (test pkgs)\n    @echo ci\n\ntest *pkgs:enum(\"a\",\"b\"):\n    @echo t\n",
			[]string{"a", "b"},
			"pkgs",
			map[string]string{"pkgs": "a b"},
			[]string{"a", "b"},
		},
		{
			"empty into +", "ci *pkgs: (test pkgs)\n    @echo ci\n\ntest +pkgs:\n    @echo t\n",
			nil, "pkgs",
			map[string]string{"pkgs": ""},
			[]string{},
		},
		{
			"element fails, text v0.6.0 accepted", "ci *pkgs: (test pkgs)\n    @echo ci\n\ntest +pkgs:path:\n    @echo t\n",
			[]string{"./a", ""},
			"pkgs",
			map[string]string{"pkgs": "./a "},
			[]string{"./a", ""},
		},
		{
			"literal then spread", "ci *pkgs: (test \"x\" pkgs)\n    @echo ci\n\ntest *pkgs:\n    @echo t\n",
			[]string{"a"},
			"pkgs",
			map[string]string{"pkgs": "x a"},
			[]string{"x", "a"},
		},
		{
			"literal then empty spread", "ci *pkgs: (test \"x\" pkgs)\n    @echo ci\n\ntest *pkgs:\n    @echo t\n",
			nil, "pkgs",
			map[string]string{"pkgs": "x "},
			[]string{"x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, _, _ := confirmEngine(t, tt.src, nil, false, true)
			caller := eng.file.Tasks[0]
			cur, err := bindParams(caller, tt.pos, eng.scope)
			if err != nil {
				t.Fatal(err)
			}
			_, args, err := eng.ResolveDep(caller, cur, caller.Deps[0])
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(args.Values, tt.wantValues) {
				t.Errorf("Values = %q, want %q", args.Values, tt.wantValues)
			}
			if tt.target == "" {
				if len(args.Lists) != 0 {
					t.Errorf("Lists = %q, want none", args.Lists)
				}
				return
			}
			if got := args.Lists[tt.target]; !reflect.DeepEqual(got, tt.wantList) {
				t.Errorf("Lists[%s] = %q, want %q", tt.target, got, tt.wantList)
			}
		})
	}
}

// A forwarded call that both the elements and v0.6.0's text fail reports the
// failing element.
func TestResolveDepRejectsFailingElements(t *testing.T) {
	src := "ci *pkgs: (test pkgs)\n    @echo ci\n\ntest *pkgs:enum(\"a\",\"b\"):\n    @echo t\n"
	eng, _, _ := confirmEngine(t, src, nil, false, true)
	caller := eng.file.Tasks[0]
	cur, err := bindParams(caller, []string{"a", "c"}, eng.scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = eng.ResolveDep(caller, cur, caller.Deps[0])
	if err == nil || !strings.Contains(err.Error(), `invalid value "c" for parameter "pkgs"`) {
		t.Errorf("err = %v, want the element error for \"c\"", err)
	}
}

// Post-hooks and failure hooks forward the same way as dependencies.
func TestResolveDepForwardsIntoHooks(t *testing.T) {
	src := "ci *pkgs: && (post pkgs) || (fix pkgs)\n    @echo ci\n\npost *pkgs:\n    @echo p\n\nfix *pkgs:\n    @echo f\n"
	eng, _, _ := confirmEngine(t, src, nil, false, true)
	caller := eng.file.Tasks[0]
	cur, err := bindParams(caller, []string{"a b", "c"}, eng.scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range append(append([]*ast.DepCall{}, caller.PostHooks...), caller.FailHooks...) {
		_, args, err := eng.ResolveDep(caller, cur, dep)
		if err != nil {
			t.Fatal(err)
		}
		if got := args.Lists["pkgs"]; !reflect.DeepEqual(got, []string{"a b", "c"}) {
			t.Errorf("%s: Lists[pkgs] = %q", dep.Name, got)
		}
	}
}

// Spec 030 FR-009: the cache fingerprint and run-once both see elements.
func TestIdentitySeesElements(t *testing.T) {
	src := "[cache(inputs = [\"x\"])]\nlint *names:\n    @echo {{quote_each(names)}}\n\n[cache(inputs = [\"x\"])]\nplain v:\n    @echo {{v}}\n"
	eng, _, _ := confirmEngine(t, src, nil, false, true)
	lint, plain := eng.tasks["lint"], eng.tasks["plain"]

	vars := func(task *ast.Task, pos ...string) map[string]string {
		t.Helper()
		args, err := bindParams(task, pos, eng.scope)
		if err != nil {
			t.Fatal(err)
		}
		return eng.cacheSpec(task, args, task.Attr(ast.AttrCache)).Vars
	}
	one, two := vars(lint, "a b"), vars(lint, "a", "b")
	if one["names[]"] == "" || one["names[]"] == two["names[]"] {
		t.Errorf("names[] entries %q and %q must exist and differ", one["names[]"], two["names[]"])
	}
	if want := eval.ElementsKey([]string{"a b"}); one["names[]"] != want {
		t.Errorf("names[] = %q, want the run-once encoding %q", one["names[]"], want)
	}
	if one["names"] != two["names"] {
		t.Errorf("joined names entries differ: %q, %q", one["names"], two["names"])
	}
	if got := vars(plain, "x"); !reflect.DeepEqual(got, map[string]string{"v": "x"}) {
		t.Errorf("plain Vars = %q, want only v", got)
	}
}

// Run-once: dependencies with the same text but different elements run twice;
// an identical call runs once.
func TestRunOnceSeesElements(t *testing.T) {
	src := "ci: (lint \"a b\") (lint \"a\" \"b\") (lint \"a\") (lint \"a\")\n    @echo ci\n\nlint *names:\n    @printf \"[%s]\" {{quote_each(names)}}\n    @echo\n"
	eng, stdout, _ := confirmEngine(t, src, nil, false, true)
	if err := scheduler.Run(eng, []scheduler.Invocation{{Task: eng.tasks["ci"]}}); err != nil {
		t.Fatal(err)
	}
	if want := "[a b]\n[a][b]\n[a]\nci\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}
