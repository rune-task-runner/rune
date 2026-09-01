package language

import (
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
)

// TestRegistryMatchesEvalBuiltins guarantees the language registry's function
// set stays exactly in sync with the evaluator's built-ins (spec FR-027): a new
// eval built-in without a registry entry (or vice versa) fails the build. This
// prevents completion/hover/validation from drifting from evaluation.
func TestRegistryMatchesEvalBuiltins(t *testing.T) {
	inRegistry := map[string]bool{}
	for _, b := range OfKind(BuiltinFunction) {
		inRegistry[b.Name] = true
	}
	inEval := map[string]bool{}
	for _, name := range eval.BuiltinNames() {
		inEval[name] = true
	}

	for name := range inEval {
		if !inRegistry[name] {
			t.Errorf("eval built-in %q is missing from the language registry", name)
		}
	}
	for name := range inRegistry {
		if !inEval[name] {
			t.Errorf("registry function %q is not a real eval built-in", name)
		}
	}
}

// TestRegistryMatchesASTAttributes locks the attribute registry to the
// canonical ast.KnownAttributes set the same way functions are locked to the
// evaluator (spec 023 T032 — this drift existed before: doc/script/context
// were parseable but unlisted, invisible to hover/completion).
func TestRegistryMatchesASTAttributes(t *testing.T) {
	inRegistry := map[string]bool{}
	for _, b := range OfKind(BuiltinAttribute) {
		inRegistry[b.Name] = true
	}
	inAST := map[string]bool{}
	for _, name := range ast.KnownAttributes {
		inAST[name] = true
	}
	for name := range inAST {
		if !inRegistry[name] {
			t.Errorf("attribute %q (ast.KnownAttributes) is missing from the language registry", name)
		}
	}
	for name := range inRegistry {
		if !inAST[name] {
			t.Errorf("registry attribute %q is not in ast.KnownAttributes", name)
		}
	}
}
