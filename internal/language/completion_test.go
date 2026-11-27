package language

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/parser"
)

// completeAt returns completions at the cursor marker "|" in src. It tolerates
// parse errors on purpose: completion runs on mid-edit text that often does not
// parse cleanly (e.g. an open "[" or "("), so it builds an index from whatever
// parsed — exactly as the analysis service does.
func completeAt(t *testing.T, srcWithCursor string) []CompletionItem {
	t.Helper()
	offset := strings.IndexByte(srcWithCursor, '|')
	if offset < 0 {
		t.Fatal("no cursor marker | in source")
	}
	src := srcWithCursor[:offset] + srcWithCursor[offset+1:]
	f, _ := parser.Parse("Runefile", src)
	config.Compose(f, nil)
	ix := BuildIndex(f)
	return Complete(ix, f, "Runefile", src, offset)
}

func labels(items []CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

func contains(items []CompletionItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}

func TestCompleteDependency(t *testing.T) {
	// deploy: bu|  -> suggests build, build-release
	src := "# B.\nbuild:\n    @echo b\n# BR.\nbuild-release:\n    @echo br\n# D.\ndeploy: bu|\n    @echo d\n"
	items := completeAt(t, src)
	if !contains(items, "build") || !contains(items, "build-release") {
		t.Errorf("dependency completions = %v, want build + build-release", labels(items))
	}
	// Each task item carries a signature and is a task kind.
	for _, it := range items {
		if it.Kind != CompletionTask {
			t.Errorf("item %q kind = %v, want task", it.Label, it.Kind)
		}
	}
}

func TestCompleteSetting(t *testing.T) {
	items := completeAt(t, "set wor|\n")
	if len(items) != 1 || items[0].Label != "working-directory" {
		t.Errorf("setting completions = %v, want [working-directory]", labels(items))
	}
	if items[0].Kind != CompletionSetting {
		t.Errorf("kind = %v, want setting", items[0].Kind)
	}
}

func TestCompleteAttribute(t *testing.T) {
	items := completeAt(t, "[conf|\n# B.\nbuild:\n    @echo b\n")
	if !contains(items, "confirm") {
		t.Errorf("attribute completions = %v, want confirm", labels(items))
	}
}

func TestCompleteExecutor(t *testing.T) {
	items := completeAt(t, "# B.\nbuild (py|):\n    print(1)\n")
	if len(items) != 1 || items[0].Label != "python" {
		t.Errorf("executor completions = %v, want [python]", labels(items))
	}
}

func TestCompleteBuiltinInExpression(t *testing.T) {
	items := completeAt(t, "platform := os_|\n")
	if !contains(items, "os_family") {
		t.Errorf("expression completions = %v, want os_family", labels(items))
	}
}

func TestCompleteVariablesAndParamsInInterpolation(t *testing.T) {
	// Inside a body interpolation: the task's parameter ranks above globals.
	src := "output := \"dist\"\n# B.\nbuild target=\"debug\":\n    @echo {{ tar|}}\n"
	items := completeAt(t, src)
	if !contains(items, "target") {
		t.Errorf("expected 'target' parameter, got %v", labels(items))
	}
}

func TestCompleteParamsRankAboveGlobals(t *testing.T) {
	// Both a param and a global start with "x"; the parameter must come first.
	src := "xtra := \"1\"\n# B.\nbuild xarg=\"a\":\n    @echo {{ x|}}\n"
	items := completeAt(t, src)
	if len(items) < 2 {
		t.Fatalf("expected param + global, got %v", labels(items))
	}
	if items[0].Label != "xarg" {
		t.Errorf("first completion = %q, want parameter 'xarg' ranked first (%v)", items[0].Label, labels(items))
	}
}

func TestCompletePrivateTaskSameFileOnly(t *testing.T) {
	// A private task in the same file IS offered as a dependency (FR-019a).
	src := "_helper:\n    @echo h\n# D.\ndeploy: _hel|\n    @echo d\n"
	items := completeAt(t, src)
	if !contains(items, "_helper") {
		t.Errorf("same-file private task should be offered, got %v", labels(items))
	}
}

func TestCompleteAgentOutputAttributes(t *testing.T) {
	items := completeAt(t, "[agent-|\n# B.\nbuild:\n    @echo b\n")
	for _, want := range []string{"agent-output", "agent-summary"} {
		if !contains(items, want) {
			t.Errorf("attribute completions = %v, want %s", labels(items), want)
		}
	}
}

func TestCompleteReuseAttribute(t *testing.T) {
	items := completeAt(t, "[reu|\n# B.\nbuild:\n    @echo b\n")
	if !contains(items, "reuse") {
		t.Errorf("attribute completions = %v, want reuse", labels(items))
	}
}
