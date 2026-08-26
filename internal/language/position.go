package language

import (
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/token"
)

// TargetKind classifies what the cursor is on.
type TargetKind int

const (
	TargetNone       TargetKind = iota
	TargetDependency            // a dependency/post-hook task reference
	TargetVarRef                // a variable or parameter reference (expr or interpolation)
	TargetTaskName              // a task declaration's name
	TargetParamDecl             // a parameter declaration in a header
	TargetAttribute             // a task attribute
	TargetBuiltin               // a built-in function call
)

// Target describes the symbol under a cursor offset.
type Target struct {
	Kind  TargetKind
	Name  string
	Scope ScopeID    // enclosing task scope (for variable/parameter resolution)
	Span  token.Span // the hovered token's span
}

// TargetAt returns what is at byte offset within the given file (the document
// the cursor is in), checking the most specific constructs first (expression
// references beat the dependency or body line that contains them).
//
// The file guard matters because the composed AST contains nodes from imported
// files, whose byte offsets share the same integer space as the cursor's file;
// only nodes originating in `file` are hit-tested.
func TargetAt(f *ast.File, file string, offset int) (Target, bool) {
	if f == nil {
		return Target{}, false
	}

	// Module-level expressions.
	for _, a := range f.Assignments {
		if a.Sp.File != file {
			continue
		}
		if t, ok := exprAt(a.Expr, offset, ModuleScope); ok {
			return t, ok
		}
	}
	for _, s := range f.Settings {
		if s.Sp.File != file {
			continue
		}
		if t, ok := exprAt(s.Value, offset, ModuleScope); ok {
			return t, ok
		}
		for _, e := range s.List {
			if t, ok := exprAt(e, offset, ModuleScope); ok {
				return t, ok
			}
		}
	}

	for _, tk := range f.Tasks {
		if tk.Sp.File != file {
			continue // an imported/module task; not in the cursor's document
		}
		scope := ScopeID(tk.Name)

		// Attributes.
		for _, a := range tk.Attributes {
			if inSpan(a.Sp, offset) {
				return Target{Kind: TargetAttribute, Name: a.Kind, Span: a.Sp}, true
			}
		}

		// Parameter defaults (expressions) then the parameter name itself.
		for _, p := range tk.Params {
			if t, ok := exprAt(p.Default, offset, scope); ok {
				return t, ok
			}
			if ns := paramNameSpan(p); inSpan(ns, offset) {
				return Target{Kind: TargetParamDecl, Name: p.Name, Scope: scope, Span: ns}, true
			}
		}

		// The task name.
		if ns := nameSpan(tk.Sp, tk.Name); inSpan(ns, offset) {
			return Target{Kind: TargetTaskName, Name: tk.Name, Span: ns}, true
		}

		// Dependencies + post-hooks + failure hooks: arguments (expressions)
		// beat the name.
		for _, dep := range tk.Edges() {
			for _, arg := range dep.Args {
				if t, ok := exprAt(arg, offset, scope); ok {
					return t, ok
				}
			}
			if inSpan(dep.Sp, offset) {
				return Target{Kind: TargetDependency, Name: dep.Name, Span: dep.Sp}, true
			}
		}

		// Body interpolations: {{ name }} references.
		for _, bl := range tk.Body {
			if t, ok := interpTargetAt(bl, offset, scope); ok {
				return t, ok
			}
		}
	}
	return Target{}, false
}

// exprAt returns the innermost variable reference or built-in call at offset.
func exprAt(e ast.Expr, offset int, scope ScopeID) (Target, bool) {
	switch x := e.(type) {
	case nil:
		return Target{}, false
	case *ast.VarRef:
		if inSpan(x.Sp, offset) {
			return Target{Kind: TargetVarRef, Name: x.Name, Scope: scope, Span: x.Sp}, true
		}
	case *ast.FuncCall:
		for _, a := range x.Args {
			if t, ok := exprAt(a, offset, scope); ok {
				return t, ok
			}
		}
		if inSpan(x.Sp, offset) {
			return Target{Kind: TargetBuiltin, Name: x.Name, Span: x.Sp}, true
		}
	case *ast.Binary:
		if t, ok := exprAt(x.Left, offset, scope); ok {
			return t, ok
		}
		if t, ok := exprAt(x.Right, offset, scope); ok {
			return t, ok
		}
	case *ast.Conditional:
		for _, br := range x.Branches {
			for _, sub := range []ast.Expr{br.Left, br.Right, br.Result} {
				if t, ok := exprAt(sub, offset, scope); ok {
					return t, ok
				}
			}
		}
		if t, ok := exprAt(x.Else, offset, scope); ok {
			return t, ok
		}
	}
	return Target{}, false
}

// interpTargetAt finds an identifier under offset inside a {{ ... }} region of a
// body line, returning it as a variable/parameter reference.
func interpTargetAt(bl *ast.BodyLine, offset int, scope ScopeID) (Target, bool) {
	base := bl.Sp.Start.Offset
	// bl.Raw prepends the line's extra (deeper-than-base) indentation, while the
	// span starts at the first content byte. On a more-nested body line those
	// leading spaces shift every Raw index by len(extra) relative to the source,
	// so offsets must be adjusted both ways or hit-testing lands too early.
	extra := len(bl.Raw) - (bl.Sp.End.Offset - bl.Sp.Start.Offset)
	if extra < 0 {
		extra = 0
	}
	rel := offset - base + extra
	if rel < 0 || rel > len(bl.Raw) {
		return Target{}, false
	}
	name, relStart, relEnd, ok := interpIdentAt(bl.Raw, rel)
	if !ok {
		return Target{}, false
	}
	sp := token.Span{
		File:  bl.Sp.File,
		Start: token.Position{Offset: base + relStart - extra, Line: bl.Sp.Start.Line, Col: bl.Sp.Start.Col + relStart - extra},
		End:   token.Position{Offset: base + relEnd - extra, Line: bl.Sp.Start.Line, Col: bl.Sp.Start.Col + relEnd - extra},
	}
	return Target{Kind: TargetVarRef, Name: name, Scope: scope, Span: sp}, true
}

// interpIdentAt returns the identifier under rel if rel is inside a {{ ... }}
// region of raw. Identifier characters are letters, digits, and underscore.
func interpIdentAt(raw string, rel int) (name string, start, end int, ok bool) {
	if !insideInterp(raw, rel) {
		return "", 0, 0, false
	}
	// Expand to identifier boundaries around rel.
	start = rel
	for start > 0 && isIdentByte(raw[start-1]) {
		start--
	}
	end = rel
	for end < len(raw) && isIdentByte(raw[end]) {
		end++
	}
	if start == end {
		return "", 0, 0, false
	}
	return raw[start:end], start, end, true
}

// insideInterp reports whether rel falls within an open {{ ... }} region.
func insideInterp(raw string, rel int) bool {
	depth := 0
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] == '{' && raw[i+1] == '{' {
			if rel > i+1 {
				depth++
			}
			i++
			continue
		}
		if raw[i] == '}' && raw[i+1] == '}' {
			if rel > i+1 {
				depth--
			}
			i++
			continue
		}
	}
	return depth > 0
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func inSpan(sp token.Span, offset int) bool {
	return offset >= sp.Start.Offset && offset < sp.End.Offset
}

// nameSpan synthesizes the span of a leading identifier of length len(name)
// starting at sp.Start (task/param names live on a single line).
func nameSpan(sp token.Span, name string) token.Span {
	end := sp.Start
	end.Offset += len(name)
	end.Col += len(name)
	return token.Span{File: sp.File, Start: sp.Start, End: end}
}

// paramNameSpan returns the span of a parameter's name, accounting for a leading
// +/* variadic sigil.
func paramNameSpan(p *ast.Param) token.Span {
	start := p.Sp.Start
	if p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar {
		start.Offset++
		start.Col++
	}
	end := start
	end.Offset += len(p.Name)
	end.Col += len(p.Name)
	return token.Span{File: p.Sp.File, Start: start, End: end}
}
