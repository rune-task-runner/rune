package language

import (
	"sort"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
)

// CompletionKind classifies a completion item (mapped to an LSP kind by the
// server).
type CompletionKind int

const (
	CompletionTask CompletionKind = iota
	CompletionVariable
	CompletionParameter
	CompletionSetting
	CompletionAttribute
	CompletionExecutor
	CompletionFunction
)

// CompletionItem is one suggestion.
type CompletionItem struct {
	Label         string
	Detail        string // signature
	Documentation string
	Kind          CompletionKind
}

// completionContext is the syntactic context detected at the cursor.
type completionContext int

const (
	ctxNone completionContext = iota
	ctxDependency
	ctxExpression // variables + parameters + built-in functions
	ctxSetting
	ctxAttribute
	ctxExecutor
)

// Complete returns context-aware completions at byte offset. Context is detected
// from the text around the cursor (which is mid-edit and may not parse), while
// candidates come from the symbol index and the language registry. Results are
// filtered by the identifier prefix being typed (spec FR-018).
func Complete(ix *Index, f *ast.File, file string, text string, offset int) []CompletionItem {
	ctx, prefix := detectContext(text, offset)
	scope := enclosingScope(f, file, offset)

	switch ctx {
	case ctxDependency:
		return taskCompletions(ix, file, prefix)
	case ctxExpression:
		return expressionCompletions(ix, scope, prefix)
	case ctxSetting:
		return registryCompletions(BuiltinSetting, CompletionSetting, prefix)
	case ctxAttribute:
		return registryCompletions(BuiltinAttribute, CompletionAttribute, prefix)
	case ctxExecutor:
		return registryCompletions(BuiltinExecutor, CompletionExecutor, prefix)
	default:
		return nil
	}
}

// detectContext inspects the current line up to the cursor. Order matters:
// interpolation and assignment RHS are expression contexts; an open '[' is an
// attribute; `set NAME` is a setting; an open '(' in a header is an executor;
// a header ':' is a dependency list.
func detectContext(text string, offset int) (completionContext, string) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(text) {
		offset = len(text)
	}
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	line := text[lineStart:offset]
	prefix := trailingIdent(line)

	switch {
	case strings.Count(line, "{{") > strings.Count(line, "}}"): // inside an open interpolation
		return ctxExpression, prefix
	case strings.Contains(line, ":="):
		return ctxExpression, prefix
	case strings.Count(line, "[") > strings.Count(line, "]"):
		return ctxAttribute, prefix
	case isSettingLine(line):
		return ctxSetting, prefix
	case strings.Count(line, "(") > strings.Count(line, ")") && !strings.Contains(line, ":"):
		return ctxExecutor, prefix
	case headerColon(line):
		return ctxDependency, prefix
	default:
		return ctxNone, prefix
	}
}

// trailingIdent returns the identifier characters immediately before the cursor.
func trailingIdent(line string) string {
	i := len(line)
	for i > 0 && isNameByte(line[i-1]) {
		i--
	}
	return line[i:]
}

func isNameByte(b byte) bool {
	return b == '_' || b == '-' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// isSettingLine reports whether line is `set NAME` still being typed (no `:=`).
func isSettingLine(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return t == "set" || strings.HasPrefix(t, "set ")
}

// headerColon reports whether line contains a task-header ':' before the cursor
// (excluding ':=' assignment and '::' namespace separators).
func headerColon(line string) bool {
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		if i+1 < len(line) && (line[i+1] == '=' || line[i+1] == ':') {
			i++ // skip := and ::
			continue
		}
		if i > 0 && line[i-1] == ':' {
			continue // second ':' of '::'
		}
		return true
	}
	return false
}

// enclosingScope returns the parameter scope of the task whose span contains
// offset in the cursor's file, or ModuleScope.
func enclosingScope(f *ast.File, file string, offset int) ScopeID {
	if f == nil {
		return ModuleScope
	}
	for _, t := range f.Tasks {
		if t.Sp.File != file {
			continue
		}
		if offset >= t.Sp.Start.Offset && offset <= t.Sp.End.Offset {
			return ScopeID(t.Name)
		}
	}
	return ModuleScope
}

// taskCompletions returns task-name suggestions (dependency position). Private
// tasks are offered only within their own file (spec FR-019a).
func taskCompletions(ix *Index, file, prefix string) []CompletionItem {
	var items []CompletionItem
	for name, sym := range ix.ByQualified {
		if sym.Kind != SymbolTask {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if !sym.Exported && sym.Definition.File != file {
			continue // private task from another file
		}
		detail := sym.Signature
		if mod := moduleOf(name); mod != "" {
			detail += "  (module " + mod + ")"
		}
		items = append(items, CompletionItem{
			Label:         name,
			Detail:        detail,
			Documentation: sym.Documentation,
			Kind:          CompletionTask,
		})
	}
	sortItems(items)
	return items
}

// expressionCompletions returns parameters (of the enclosing scope) first, then
// module variables, then built-in functions.
func expressionCompletions(ix *Index, scope ScopeID, prefix string) []CompletionItem {
	var params, vars, fns []CompletionItem
	if scope != ModuleScope {
		for _, s := range ix.byKind(SymbolParameter) {
			if s.Scope == scope && strings.HasPrefix(s.Name, prefix) {
				detail := "parameter"
				// The signature carries the inline type annotation (spec 023),
				// e.g. `env:enum("a","b")` — show it so the agent-facing
				// contract is visible while authoring. CutPrefix (not
				// TrimPrefix) so a signature that ever stops starting with
				// the bare name degrades to the plain detail instead of
				// echoing the whole signature.
				if anno, ok := strings.CutPrefix(s.Signature, s.Name); ok && anno != "" {
					detail = "parameter " + anno
				}
				params = append(params, CompletionItem{Label: s.Name, Detail: detail, Kind: CompletionParameter})
			}
		}
	}
	for _, s := range ix.byKind(SymbolVariable) {
		if strings.HasPrefix(s.Name, prefix) {
			vars = append(vars, CompletionItem{Label: s.Name, Detail: "variable", Kind: CompletionVariable})
		}
	}
	for _, b := range MatchKind(BuiltinFunction, prefix) {
		fns = append(fns, CompletionItem{Label: b.Name, Detail: b.Signature, Documentation: b.Documentation, Kind: CompletionFunction})
	}
	sortItems(params)
	sortItems(vars)
	sortItems(fns)
	return append(append(params, vars...), fns...) // params rank above globals
}

func registryCompletions(kind BuiltinKind, ck CompletionKind, prefix string) []CompletionItem {
	var items []CompletionItem
	for _, b := range MatchKind(kind, prefix) {
		items = append(items, CompletionItem{Label: b.Name, Detail: b.Signature, Documentation: b.Documentation, Kind: ck})
	}
	sortItems(items)
	return items
}

func moduleOf(qualified string) string {
	if i := strings.Index(qualified, "::"); i >= 0 {
		return qualified[:i]
	}
	return ""
}

func sortItems(items []CompletionItem) {
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
}
