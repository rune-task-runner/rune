package language

import (
	"strconv"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/token"
)

// SymbolKind classifies an indexed language entity.
type SymbolKind int

const (
	SymbolTask SymbolKind = iota
	SymbolVariable
	SymbolParameter
	SymbolSetting
	SymbolAttribute
	SymbolBuiltin
	SymbolImport
	SymbolModule
)

func (k SymbolKind) String() string {
	switch k {
	case SymbolTask:
		return "task"
	case SymbolVariable:
		return "variable"
	case SymbolParameter:
		return "parameter"
	case SymbolSetting:
		return "setting"
	case SymbolAttribute:
		return "attribute"
	case SymbolBuiltin:
		return "builtin"
	case SymbolImport:
		return "import"
	case SymbolModule:
		return "module"
	default:
		return "symbol"
	}
}

// ScopeID identifies the scope a symbol belongs to: the empty string is module
// (file) scope; a task's name is its parameter scope.
type ScopeID string

const ModuleScope ScopeID = ""

// Symbol is a named language entity with its declaration location.
type Symbol struct {
	Name          string
	QualifiedName string // namespaced name (e.g. docker::build); == Name when unqualified
	Kind          SymbolKind
	Definition    token.Span // declaration span (carries the origin file via Span.File)
	Selection     token.Span // precise span to navigate to (name)
	Scope         ScopeID
	Documentation string
	Signature     string
	Exported      bool // false for [private] tasks
}

// TaskSignature renders a task's signature line, e.g. `build target="debug"`.
func TaskSignature(t *ast.Task) string {
	var b strings.Builder
	b.WriteString(t.Name)
	for _, p := range t.Params {
		b.WriteByte(' ')
		switch p.Kind {
		case ast.ParamVariadicPlus:
			b.WriteByte('+')
			b.WriteString(p.Name)
			b.WriteString(p.Constraint.String())
		case ast.ParamVariadicStar:
			b.WriteByte('*')
			b.WriteString(p.Name)
			b.WriteString(p.Constraint.String())
		case ast.ParamDefaulted:
			b.WriteString(p.Name)
			b.WriteString(p.Constraint.String())
			b.WriteByte('=')
			b.WriteString(defaultLiteral(p.Default))
		default:
			b.WriteString(p.Name)
			b.WriteString(p.Constraint.String())
		}
	}
	if t.Executor != "" {
		b.WriteString(" (")
		b.WriteString(t.Executor)
		b.WriteByte(')')
	}
	return b.String()
}

// defaultLiteral renders a parameter default for a signature. Only string
// literals get a faithful rendering; anything else is shown generically.
func defaultLiteral(e ast.Expr) string {
	if s, ok := e.(*ast.StringLit); ok {
		return strconv.Quote(s.Value)
	}
	if e == nil {
		return "\"\""
	}
	return "…"
}
