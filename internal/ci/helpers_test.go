package ci

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/parser"
)

var update = flag.Bool("update", false, "rewrite the golden workflows")

// fakeResolver binds as the engine does, for the forms the tests use:
// positional values, defaults, and the elements of a variadic parameter.
type fakeResolver struct {
	tasks       map[string]*ast.Task
	scope       *eval.Scope
	unavailable map[string]bool
	evaluated   []string // the dependency calls ResolveDep evaluated
}

func (r *fakeResolver) BindRoot(t *ast.Task, args []string) (eval.Args, error) {
	if r.unavailable[t.Name] {
		return eval.Args{}, &Refusal{Diags: diag.List{diag.New(t.Sp, "not available")}}
	}
	return bind(t, args, r.scope)
}

func (r *fakeResolver) ResolveDep(_ *ast.Task, args eval.Args, dep *ast.DepCall) (*ast.Task, eval.Args, error) {
	target, ok := r.tasks[dep.Name]
	if !ok {
		return nil, eval.Args{}, fmt.Errorf("unknown task %q", dep.Name)
	}
	r.evaluated = append(r.evaluated, dep.Name)
	ev := eval.New(r.scope.WithArgs(args))
	var vals []string
	for _, a := range dep.Args {
		v, err := ev.Eval(a)
		if err != nil {
			return nil, eval.Args{}, err
		}
		vals = append(vals, v)
	}
	bound, err := bind(target, vals, r.scope)
	return target, bound, err
}

func (r *fakeResolver) Available(t *ast.Task) bool { return !r.unavailable[t.Name] }

func bind(t *ast.Task, vals []string, scope *eval.Scope) (eval.Args, error) {
	args := eval.Args{Values: map[string]string{}, Lists: map[string][]string{}}
	for i, p := range t.Params {
		switch {
		case p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar:
			rest := []string{}
			if i < len(vals) {
				rest = vals[i:]
			}
			args.Lists[p.Name] = rest
			args.Values[p.Name] = strings.Join(rest, " ")
		case i < len(vals):
			args.Values[p.Name] = vals[i]
		case p.Kind == ast.ParamDefaulted:
			v, err := eval.New(scope.WithArgs(args)).Eval(p.Default)
			if err != nil {
				return eval.Args{}, err
			}
			args.Values[p.Name] = v
		default:
			return eval.Args{}, errors.New("missing argument " + p.Name)
		}
	}
	return args, nil
}

// parse reads a Runefile and indexes it.
func parse(t *testing.T, src string) (map[string]*ast.Task, map[string]*ast.Assignment) {
	t.Helper()
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	tasks := map[string]*ast.Task{}
	for _, task := range f.Tasks {
		tasks[task.Name] = task
	}
	assigns := map[string]*ast.Assignment{}
	for _, a := range f.Assignments {
		assigns[a.Name] = a
	}
	return tasks, assigns
}

// build builds the graph of roots ("name arg...") on goos.
func build(t *testing.T, src, goos string, unavailable []string, roots ...string) (*Graph, *fakeResolver, error) {
	t.Helper()
	tasks, assigns := parse(t, src)
	scope := eval.NewScope(assigns, map[string]string{})
	scope.GOOS = goos
	r := &fakeResolver{tasks: tasks, scope: scope, unavailable: map[string]bool{}}
	for _, u := range unavailable {
		r.unavailable[u] = true
	}
	in := Input{OS: goos, Resolver: r, Tasks: tasks, Assigns: assigns}
	for _, root := range roots {
		words := strings.Fields(root)
		task, ok := tasks[words[0]]
		if !ok {
			t.Fatalf("no task %q", words[0])
		}
		in.Roots = append(in.Roots, Root{Task: task, Args: words[1:]})
	}
	g, err := Build(in)
	return g, r, err
}

// mustBuild builds a graph that must have no refusal.
func mustBuild(t *testing.T, src string, roots ...string) *Graph {
	t.Helper()
	g, _, err := build(t, src, "linux", nil, roots...)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

// shape lists each node as "label<-need,need".
func shape(g *Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		s := Label(n, g.OS, false)
		var needs []string
		for _, m := range n.Needs {
			needs = append(needs, Label(m, g.OS, false))
		}
		if len(needs) > 0 {
			s += "<-" + strings.Join(needs, ",")
		}
		out = append(out, s)
	}
	return out
}
