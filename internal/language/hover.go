package language

import (
	"fmt"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/token"
)

// Hover returns markdown documentation for the symbol at offset, along with the
// hovered span, or ok=false when there is nothing to show.
func Hover(f *ast.File, file string, offset int) (markdown string, span token.Span, ok bool) {
	target, found := TargetAt(f, file, offset)
	if !found {
		return "", token.Span{}, false
	}
	switch target.Kind {
	case TargetTaskName, TargetDependency:
		if md, ok := taskHover(f, target.Name); ok {
			return md, target.Span, true
		}
	case TargetVarRef, TargetParamDecl:
		return varHover(f, target.Name, target.Scope), target.Span, true
	case TargetAttribute:
		if b, ok := Lookup(BuiltinAttribute, target.Name); ok {
			return builtinHover(b), target.Span, true
		}
	case TargetBuiltin:
		if b, ok := Lookup(BuiltinFunction, target.Name); ok {
			return builtinHover(b), target.Span, true
		}
	}
	return "", token.Span{}, false
}

// taskHover renders a task's signature, documentation, executor, group, and
// definition location.
func taskHover(f *ast.File, name string) (string, bool) {
	t := findTask(f, name)
	if t == nil {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "```rune\n%s\n```\n", TaskSignature(t))
	if t.Doc != "" {
		b.WriteString(t.Doc)
		b.WriteString("\n\n")
	}
	executor := t.Executor
	if executor == "" {
		executor = "sh"
	}
	fmt.Fprintf(&b, "Executor: %s\n", executor)
	if g := attrValue(t, ast.AttrGroup); g != "" {
		fmt.Fprintf(&b, "Group: %s\n", g)
	}
	fmt.Fprintf(&b, "Defined in: %s:%d", shortFile(t.Sp.File), t.Sp.Start.Line)
	return b.String(), true
}

// varHover renders a variable or parameter hover. A parameter's hover carries
// its declared type annotation and [param-doc] description (spec 023).
func varHover(f *ast.File, name string, scope ScopeID) string {
	if scope != ModuleScope {
		sig := name
		desc := ""
		if t := findTask(f, string(scope)); t != nil {
			for _, p := range t.Params {
				if p.Name == name {
					sig = name + p.Constraint.String()
					break
				}
			}
			desc = t.ParamDoc(name)
		}
		out := fmt.Sprintf("```rune\n%s\n```\nparameter of task `%s`", sig, string(scope))
		if desc != "" {
			out += "\n\n" + desc
		}
		return out
	}
	return fmt.Sprintf("```rune\n%s\n```\nvariable", name)
}

func builtinHover(b Builtin) string {
	return fmt.Sprintf("```rune\n%s\n```\n%s", b.Signature, b.Documentation)
}

func findTask(f *ast.File, name string) *ast.Task {
	for _, t := range f.Tasks {
		if t.Name == name || baseName(t.Name) == name {
			return t
		}
	}
	return nil
}

func attrValue(t *ast.Task, kind string) string {
	for _, a := range t.Attributes {
		if a.Kind == kind {
			return a.Str
		}
	}
	return ""
}

// shortFile returns the base name of a path for compact hover display.
func shortFile(path string) string {
	if i := strings.LastIndexAny(path, "/\\"); i >= 0 {
		return path[i+1:]
	}
	return path
}
