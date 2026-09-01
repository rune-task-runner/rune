package cli

import (
	"regexp"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
)

// overridePattern matches a NAME=VALUE run-time variable override.
var overridePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*=`)

// rawInvocation is a task name plus the positional arguments captured for it.
type rawInvocation struct {
	name string
	args []string
}

// splitArgs separates NAME=VALUE overrides from task invocations. Positional
// arguments are assigned to a task greedily by its parameter arity (a variadic
// param consumes all remaining tokens), matching how just splits a chained
// command line. Unknown task names are a usage error (exit 2).
func splitArgs(args []string, tasks map[string]*ast.Task, commands []string) (map[string]string, []rawInvocation, error) {
	overrides := map[string]string{}
	var invs []rawInvocation

	i := 0
	for i < len(args) {
		tok := args[i]
		if overridePattern.MatchString(tok) {
			eq := strings.IndexByte(tok, '=')
			overrides[tok[:eq]] = tok[eq+1:]
			i++
			continue
		}
		task, ok := tasks[tok]
		if !ok {
			if s, found := nearest(tok, candidateNames(tasks, commands)); found {
				return nil, nil, usagef("unknown task: %s (did you mean %q?)", tok, s)
			}
			return nil, nil, usagef("unknown task: %s", tok)
		}
		i++
		capacity := paramCapacity(task)
		var pos []string
		for (capacity < 0 || len(pos) < capacity) && i < len(args) {
			pos = append(pos, args[i])
			i++
		}
		invs = append(invs, rawInvocation{name: tok, args: pos})
	}
	return overrides, invs, nil
}

// candidateNames returns the set of names a mistyped token might have meant: the
// Runefile's task names plus the reserved built-in command names.
func candidateNames(tasks map[string]*ast.Task, commands []string) []string {
	names := make([]string, 0, len(tasks)+len(commands))
	for n := range tasks {
		names = append(names, n)
	}
	return append(names, commands...)
}

// paramCapacity returns how many positional args a task consumes: -1 (all
// remaining) if it has a trailing variadic param, else the parameter count.
func paramCapacity(t *ast.Task) int {
	for _, p := range t.Params {
		if p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar {
			return -1
		}
	}
	return len(t.Params)
}

// checkParamValue validates one bound value against the parameter's declared
// constraint (spec 023 FR-005). The error carries the task, parameter, quoted
// value, and accepted set (FR-006) as a usage error: exit 2, zero side effects.
func checkParamValue(t *ast.Task, p *ast.Param, val string) error {
	if err := p.Constraint.Check(val); err != nil {
		return usagef("task %q: invalid value %q for parameter %q (%s)", t.Name, val, p.Name, err)
	}
	return nil
}

// bindParams binds positional arguments to a task's parameters, evaluating
// defaults (which may reference earlier params) against scope. It enforces the
// task's arity and every declared constraint — variadic values are validated
// per element BEFORE the space-join collapses them (spec 023 FR-008).
func bindParams(t *ast.Task, pos []string, scope *eval.Scope) (map[string]string, error) {
	params := map[string]string{}
	i := 0
	for _, p := range t.Params {
		switch p.Kind {
		case ast.ParamRequired:
			if i >= len(pos) {
				return nil, usagef("task %q is missing required argument %q", t.Name, p.Name)
			}
			if err := checkParamValue(t, p, pos[i]); err != nil {
				return nil, err
			}
			params[p.Name] = pos[i]
			i++
		case ast.ParamDefaulted:
			if i < len(pos) {
				if err := checkParamValue(t, p, pos[i]); err != nil {
					return nil, err
				}
				params[p.Name] = pos[i]
				i++
			} else {
				v, err := eval.New(scope.WithParams(copyMap(params))).Eval(p.Default)
				if err != nil {
					return nil, &ValidationError{Err: err}
				}
				// Literal defaults are checked statically (RUNE2014); this is
				// the identical runtime check for defaults that evaluate.
				if err := checkParamValue(t, p, v); err != nil {
					return nil, err
				}
				params[p.Name] = v
			}
		case ast.ParamVariadicPlus:
			if i >= len(pos) {
				return nil, usagef("task %q expects at least one %q argument", t.Name, p.Name)
			}
			for _, v := range pos[i:] {
				if err := checkParamValue(t, p, v); err != nil {
					return nil, err
				}
			}
			params[p.Name] = strings.Join(pos[i:], " ")
			i = len(pos)
		case ast.ParamVariadicStar:
			for _, v := range pos[i:] {
				if err := checkParamValue(t, p, v); err != nil {
					return nil, err
				}
			}
			params[p.Name] = strings.Join(pos[i:], " ")
			i = len(pos)
		}
	}
	if i < len(pos) {
		return nil, usagef("task %q got %d arguments but accepts %d", t.Name, len(pos), len(t.Params))
	}
	return params, nil
}

// bindNamedParams binds named arguments (from an MCP tool call) to a task's
// parameters, evaluating defaults for any omitted optional parameter. Scalar
// constraints are enforced here (defense in depth behind the MCP server's own
// schema check); variadic values arrive already space-joined, so their
// per-element validation happens in mcpserver before the join (spec 023 R6).
func bindNamedParams(t *ast.Task, named map[string]string, scope *eval.Scope) (map[string]string, error) {
	params := map[string]string{}
	for _, p := range t.Params {
		val, provided := named[p.Name]
		switch p.Kind {
		case ast.ParamRequired:
			if !provided || val == "" {
				return nil, usagef("task %q is missing required argument %q", t.Name, p.Name)
			}
			if err := checkParamValue(t, p, val); err != nil {
				return nil, err
			}
			params[p.Name] = val
		case ast.ParamVariadicPlus:
			if !provided || val == "" {
				return nil, usagef("task %q is missing required argument %q", t.Name, p.Name)
			}
			params[p.Name] = val
		case ast.ParamVariadicStar:
			params[p.Name] = val // may be empty
		case ast.ParamDefaulted:
			if provided {
				if err := checkParamValue(t, p, val); err != nil {
					return nil, err
				}
				params[p.Name] = val
			} else {
				v, err := eval.New(scope.WithParams(copyMap(params))).Eval(p.Default)
				if err != nil {
					return nil, &ValidationError{Err: err}
				}
				if err := checkParamValue(t, p, v); err != nil {
					return nil, err
				}
				params[p.Name] = v
			}
		}
	}
	return params, nil
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
