package language

import (
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
)

// Index is the symbol index over a composed *ast.File. The MVP provides lookup
// by base name, by qualified name, and by document (spec FR-026); a references
// index and full scope tree are deferred (rename/find-refs are out of scope).
//
// ByDocument is keyed by the origin file path (token.Span.File), which every
// AST node retains through import composition — this is what makes cross-file
// attribution and navigation fall out for free (research R6).
type Index struct {
	ByName      map[string][]Symbol
	ByQualified map[string]Symbol
	ByDocument  map[string][]Symbol
}

// BuildIndex constructs the symbol index from a composed file. It must be called
// after config.Compose, so imported/module tasks are present with their origin
// spans intact.
func BuildIndex(f *ast.File) *Index {
	ix := &Index{
		ByName:      map[string][]Symbol{},
		ByQualified: map[string]Symbol{},
		ByDocument:  map[string][]Symbol{},
	}
	if f == nil {
		return ix
	}

	for _, s := range f.Settings {
		ix.add(Symbol{
			Name:       s.Name,
			Kind:       SymbolSetting,
			Definition: s.Sp,
			Selection:  s.Sp,
			Scope:      ModuleScope,
			Signature:  "set " + s.Name,
			Exported:   true,
		})
	}
	for _, a := range f.Assignments {
		ix.add(Symbol{
			Name:       a.Name,
			Kind:       SymbolVariable,
			Definition: a.Sp,
			Selection:  a.Sp,
			Scope:      ModuleScope,
			Signature:  a.Name,
			Exported:   true,
		})
	}
	for _, im := range f.Imports {
		ix.add(Symbol{
			Name:       im.Path,
			Kind:       SymbolImport,
			Definition: im.Sp,
			Selection:  im.Sp,
			Scope:      ModuleScope,
			Signature:  "import " + im.Path,
			Exported:   true,
		})
	}
	for _, m := range f.Mods {
		ix.add(Symbol{
			Name:       m.Name,
			Kind:       SymbolModule,
			Definition: m.Sp,
			Selection:  m.Sp,
			Scope:      ModuleScope,
			Signature:  "mod " + m.Name,
			Exported:   true,
		})
	}
	for _, t := range f.Tasks {
		ix.add(Symbol{
			Name:          baseName(t.Name),
			QualifiedName: t.Name,
			Kind:          SymbolTask,
			Definition:    t.Sp,
			Selection:     t.Sp,
			Scope:         ModuleScope,
			Documentation: t.Doc,
			Signature:     TaskSignature(t),
			Exported:      !t.IsPrivate(),
		})
		for _, p := range t.Params {
			ix.add(Symbol{
				Name:          p.Name,
				Kind:          SymbolParameter,
				Definition:    p.Sp,
				Selection:     p.Sp,
				Scope:         ScopeID(t.Name),
				Signature:     p.Name + p.Constraint.String(),
				Documentation: t.ParamDoc(p.Name),
				Exported:      false,
			})
		}
	}
	return ix
}

// add inserts a symbol into every lookup map.
func (ix *Index) add(s Symbol) {
	ix.ByName[s.Name] = append(ix.ByName[s.Name], s)
	q := s.QualifiedName
	if q == "" {
		q = s.Name
	}
	// Qualified names are unique per kind+name; tasks (the namespaced case) never
	// collide because Compose already rejected duplicates.
	if s.Kind == SymbolTask {
		ix.ByQualified[q] = s
	}
	if file := s.Definition.File; file != "" {
		ix.ByDocument[file] = append(ix.ByDocument[file], s)
	}
}

// Tasks returns every task symbol.
func (ix *Index) Tasks() []Symbol { return ix.byKind(SymbolTask) }

// baseName returns the unqualified tail of a possibly-namespaced task name
// (docker::build -> build).
func baseName(name string) string {
	if i := strings.LastIndex(name, "::"); i >= 0 {
		return name[i+2:]
	}
	return name
}
