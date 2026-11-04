// Package eval is the total tree-walking evaluator for the Runefile expression
// sublanguage: it turns an ast.Expr into a string Value. It has no loops or
// recursion in the language itself (Principle III) — the only branching is the
// if/else conditional. Variable resolution consults task params first, then
// run-time overrides, then module assignments (FR-006).
package eval

import (
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/token"
)

// Error is an evaluation failure carrying a source span for diagnostics.
type Error struct {
	Span token.Span
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Scope holds the bindings visible to an expression: task parameters, run-time
// overrides, and module-level assignments (evaluated lazily, with a cycle
// guard). Host hooks (env/os/arch/now) are injectable for deterministic tests.
type Scope struct {
	Params map[string]string
	// Lists holds the current invocation's variadic elements; only
	// quote_each reads it (spec 030).
	Lists     map[string][]string
	Overrides map[string]string
	Assigns   map[string]*ast.Assignment

	GOOS string
	Arch string

	Env func(string) (string, bool)
	Now func() string

	cache      map[string]string
	evaluating map[string]bool
}

// NewScope builds a scope from module assignments and run-time overrides.
func NewScope(assigns map[string]*ast.Assignment, overrides map[string]string) *Scope {
	return &Scope{
		Assigns:    assigns,
		Overrides:  overrides,
		cache:      map[string]string{},
		evaluating: map[string]bool{},
	}
}

// WithArgs returns a shallow copy of the scope with an invocation's bound
// arguments, elements included. The assignment cache is shared (assignments do
// not see params).
func (s *Scope) WithArgs(a Args) *Scope {
	cp := *s
	cp.Params = a.Values
	cp.Lists = a.Lists
	return &cp
}

// Evaluator walks expressions against a Scope.
type Evaluator struct {
	scope *Scope
}

// New builds an Evaluator. The scope's host hooks default to the real host.
func New(scope *Scope) *Evaluator {
	if scope.cache == nil {
		scope.cache = map[string]string{}
	}
	if scope.evaluating == nil {
		scope.evaluating = map[string]bool{}
	}
	return &Evaluator{scope: scope}
}

// Eval evaluates an expression to its string value.
func (e *Evaluator) Eval(expr ast.Expr) (string, *Error) {
	switch x := expr.(type) {
	case nil:
		return "", nil
	case *ast.StringLit:
		return x.Value, nil
	case *ast.VarRef:
		return e.resolve(x.Name, x.Sp)
	case *ast.Binary:
		return e.evalBinary(x)
	case *ast.FuncCall:
		return e.callBuiltin(x)
	case *ast.Conditional:
		return e.evalConditional(x)
	default:
		return "", &Error{Span: expr.Span(), Msg: "internal: unknown expression node"}
	}
}

func (e *Evaluator) evalBinary(b *ast.Binary) (string, *Error) {
	l, err := e.Eval(b.Left)
	if err != nil {
		return "", err
	}
	r, err := e.Eval(b.Right)
	if err != nil {
		return "", err
	}
	switch b.Op {
	case token.PLUS:
		return l + r, nil
	case token.SLASH:
		return joinPath(l, r), nil
	default:
		return "", &Error{Span: b.Sp, Msg: "internal: unknown binary operator"}
	}
}

func (e *Evaluator) evalConditional(c *ast.Conditional) (string, *Error) {
	for _, br := range c.Branches {
		l, err := e.Eval(br.Left)
		if err != nil {
			return "", err
		}
		r, err := e.Eval(br.Right)
		if err != nil {
			return "", err
		}
		match, err := compare(br.Op, l, r, br.Left.Span().To(br.Right.Span()))
		if err != nil {
			return "", err
		}
		if match {
			return e.Eval(br.Result)
		}
	}
	return e.Eval(c.Else)
}

// resolve looks up a bare name: params, then overrides, then module assignments.
func (e *Evaluator) resolve(name string, span token.Span) (string, *Error) {
	if e.scope.Params != nil {
		if v, ok := e.scope.Params[name]; ok {
			return v, nil
		}
	}
	if v, ok := e.scope.Overrides[name]; ok {
		return v, nil
	}
	if v, ok := e.scope.cache[name]; ok {
		return v, nil
	}
	if a, ok := e.scope.Assigns[name]; ok {
		if e.scope.evaluating[name] {
			return "", &Error{Span: span, Msg: "variable assignment cycle through " + name}
		}
		e.scope.evaluating[name] = true
		// Module assignments cannot see task params.
		sub := &Evaluator{scope: &Scope{
			Assigns:    e.scope.Assigns,
			Overrides:  e.scope.Overrides,
			GOOS:       e.scope.GOOS,
			Arch:       e.scope.Arch,
			Env:        e.scope.Env,
			Now:        e.scope.Now,
			cache:      e.scope.cache,
			evaluating: e.scope.evaluating,
		}}
		v, err := sub.Eval(a.Expr)
		delete(e.scope.evaluating, name)
		if err != nil {
			return "", err
		}
		e.scope.cache[name] = v
		return v, nil
	}
	return "", &Error{Span: span, Msg: "undefined variable: " + name}
}

// joinPath joins two path segments with a single forward slash (Principle V).
func joinPath(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return strings.TrimRight(a, "/") + "/" + strings.TrimLeft(b, "/")
}
