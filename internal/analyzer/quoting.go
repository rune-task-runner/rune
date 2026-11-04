package analyzer

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/runtime"
)

// CheckQuoting reports RUNE2017 for each interpolation that puts a string or
// path parameter into a shell body outside quote() and quote_each() (spec 030
// FR-012). Like CheckDocumentation, it is separate from Analyze: the warning
// belongs to `rune analyze` and the editor, never to a task run.
func CheckQuoting(f *ast.File) diag.List {
	var diags diag.List
	if f == nil {
		return diags
	}
	shell := shellElems(f)
	for _, t := range f.Tasks {
		warned := paramsToWarn(t)
		if len(warned) == 0 || !isShellBody(t, shell) {
			continue
		}
		for _, bl := range t.Body {
			for _, seg := range extractInterps(bl.Raw, bl.Sp) {
				expr, pdiags := parseFragment(seg.text, seg.span)
				if pdiags.HasErrors() {
					continue // Analyze already reports it
				}
				for _, p := range unquotedParams(expr, warned) {
					diags.Add(diag.Warn(seg.span, unquotedMessage(p)).WithCode(diag.CodeUnquotedParam))
				}
			}
		}
	}
	return diags
}

// shellElems returns the elements of the `set shell` that applies, as
// config.ResolveSettings reads them: the last one wins, and a scalar is one
// element. Nil means the built-in shell.
func shellElems(f *ast.File) []ast.Expr {
	var elems []ast.Expr
	for _, s := range f.Settings {
		if s.Name != "shell" {
			continue
		}
		elems = s.List
		if elems == nil && s.Value != nil {
			elems = []ast.Expr{s.Value}
		}
	}
	return elems
}

// isShellBody mirrors runtime.Select with static information only (research
// R7). Non-literal `set shell` elements are not evaluated, so the editor's
// diagnostics never depend on its environment.
func isShellBody(t *ast.Task, shell []ast.Expr) bool {
	if a := t.Attr(ast.AttrScript); a != nil && a.Str != "" {
		return slices.ContainsFunc(strings.Fields(a.Str), runtime.IsKnownShell)
	}
	switch t.Executor {
	case "", ast.ExecSh:
		if len(shell) == 0 {
			return true
		}
		return slices.ContainsFunc(shell, func(e ast.Expr) bool {
			lit, ok := e.(*ast.StringLit)
			return ok && runtime.IsKnownShell(lit.Value)
		})
	case ast.ExecPython, ast.ExecNode, ast.ExecAgent:
		return false
	default:
		// runtime.Select runs a custom executor as the interpreter command.
		return runtime.IsKnownShell(t.Executor)
	}
}

// paramsToWarn returns the parameters whose values can carry shell syntax:
// unannotated, string, and path. Unknown kinds are RUNE2012 errors already.
func paramsToWarn(t *ast.Task) map[string]*ast.Param {
	warned := map[string]*ast.Param{}
	for _, p := range t.Params {
		if c := p.Constraint; c != nil {
			kind, known := ast.ConstraintKindFromName(c.KindName)
			if !known || (kind != ast.KindString && kind != ast.KindPath) {
				continue
			}
		}
		warned[p.Name] = p
	}
	return warned
}

// unquotedParams walks an interpolation per research R6 and returns each
// warned parameter whose text can reach the line unquoted, once, in order of
// first appearance.
func unquotedParams(e ast.Expr, warned map[string]*ast.Param) []*ast.Param {
	var out []*ast.Param
	seen := map[string]bool{}
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.VarRef:
			if p, ok := warned[x.Name]; ok && !seen[x.Name] {
				seen[x.Name] = true
				out = append(out, p)
			}
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.FuncCall:
			if x.Name == "quote" || x.Name == "quote_each" {
				return
			}
			// Other functions are not modeled: assume the text passes through.
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Conditional:
			// Comparison operands only pick a branch; the results reach the line.
			for _, br := range x.Branches {
				walk(br.Result)
			}
			walk(x.Else)
		}
	}
	walk(e)
	return out
}

func unquotedMessage(p *ast.Param) string {
	fix, decl := "quote", p.Name
	switch p.Kind {
	case ast.ParamVariadicStar:
		fix, decl = "quote_each", "*"+p.Name
	case ast.ParamVariadicPlus:
		fix, decl = "quote_each", "+"+p.Name
	}
	return fmt.Sprintf("parameter %q is interpolated into a shell command unquoted; write {{%s(%s)}} outside any shell quotes, or declare %s:raw if word splitting is intended", p.Name, fix, p.Name, decl)
}
