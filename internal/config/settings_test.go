package config

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/parser"
)

// resolve parses src and resolves its settings with an empty override scope.
func resolve(t *testing.T, src string) (Settings, diag.List) {
	t.Helper()
	file, diags := parser.Parse("test.rune", src)
	if diags.HasErrors() {
		t.Fatalf("parse errors: %v", diags)
	}
	assigns := make(map[string]*ast.Assignment, len(file.Assignments))
	for _, a := range file.Assignments {
		assigns[a.Name] = a
	}
	scope := eval.NewScope(assigns, map[string]string{})
	return ResolveSettings(file, eval.New(scope))
}

func TestResolveSettings_SecretsList(t *testing.T) {
	s, diags := resolve(t, "set secrets := [\"DEPLOY_CFG\", \"UPLOAD_URL\"]\n\nok:\n    @true\n")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(s.Secrets) != 2 || s.Secrets[0] != "DEPLOY_CFG" || s.Secrets[1] != "UPLOAD_URL" {
		t.Errorf("Secrets = %v", s.Secrets)
	}
}

func TestResolveSettings_UnmaskedList(t *testing.T) {
	s, diags := resolve(t, "set unmasked := [\"OAUTH_METHOD\"]\n\nok:\n    @true\n")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(s.Unmasked) != 1 || s.Unmasked[0] != "OAUTH_METHOD" {
		t.Errorf("Unmasked = %v", s.Unmasked)
	}
}

func TestResolveSettings_MalformedElementIsPositioned(t *testing.T) {
	_, diags := resolve(t, "set secrets := [nope_undefined]\n\nok:\n    @true\n")
	if !diags.HasErrors() {
		t.Fatalf("undefined variable in the list produced no diagnostic")
	}
	if sp := diags[0].Span; !sp.IsValid() || sp.Start.Line != 1 {
		t.Errorf("diagnostic not positioned at the offending element: %+v", sp)
	}
}

func TestResolveSettings_ConflictingDeclarationAndExemption(t *testing.T) {
	src := "set secrets := [\"BOTH_LISTED\"]\nset unmasked := [\"BOTH_LISTED\"]\n\nok:\n    @true\n"
	_, diags := resolve(t, src)
	if !diags.HasErrors() {
		t.Fatalf("conflicting declaration produced no diagnostic")
	}
	var found *diag.Diagnostic
	for i := range diags {
		if strings.Contains(diags[i].Message, "BOTH_LISTED") {
			found = &diags[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no diagnostic names the conflicting variable: %v", diags)
	}
	if !found.Span.IsValid() {
		t.Errorf("conflict diagnostic has no primary span")
	}
	if len(found.Related) == 0 || !found.Related[0].Span.IsValid() {
		t.Errorf("conflict diagnostic lacks the related declaration span: %+v", found)
	}
}

// Spec 042 (contracts/cli-and-setting.md): set mcp_output_limit.
func TestResolveSettingsMCPOutputLimit(t *testing.T) {
	const task = "\nok:\n    @true\n"

	s, diags := resolve(t, "set mcp_output_limit := \"256KiB\"\n"+task)
	if diags.HasErrors() || s.MCPOutputLimit != 262144 {
		t.Errorf("256KiB: limit = %d, diagnostics = %v", s.MCPOutputLimit, diags)
	}

	s, diags = resolve(t, task)
	if diags.HasErrors() || s.MCPOutputLimit != 0 {
		t.Errorf("absent: limit = %d, diagnostics = %v", s.MCPOutputLimit, diags)
	}

	s, diags = resolve(t, "size := \"4KiB\"\nset mcp_output_limit := size\n"+task)
	if diags.HasErrors() || s.MCPOutputLimit != 4096 {
		t.Errorf("variable: limit = %d, diagnostics = %v", s.MCPOutputLimit, diags)
	}
}

func TestResolveSettingsMCPOutputLimitInvalid(t *testing.T) {
	const task = "\nok:\n    @true\n"
	src := "set mcp_output_limit := \"lots\"\n" + task
	s, diags := resolve(t, src)
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %v, want one", diags)
	}
	const want = `invalid mcp_output_limit: invalid size "lots": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB`
	if diags[0].Message != want {
		t.Errorf("message = %q\nwant %q", diags[0].Message, want)
	}
	// The span is the value, quotes included: columns 25 to 30 of line 1.
	sp := diags[0].Span
	if sp.Start.Line != 1 || sp.Start.Col != 25 || src[sp.Start.Offset:sp.End.Offset] != `"lots"` {
		t.Errorf("span = %+v, want the value", sp)
	}
	if s.MCPOutputLimit != 0 {
		t.Errorf("an invalid size left limit %d", s.MCPOutputLimit)
	}

	for _, src := range []string{
		"set mcp_output_limit\n" + task,
		"set mcp_output_limit := [\"1KiB\"]\n" + task,
	} {
		_, diags := resolve(t, src)
		if len(diags) != 1 || !strings.HasPrefix(diags[0].Message, "invalid mcp_output_limit: it needs a size") {
			t.Errorf("%q: diagnostics = %v", src, diags)
		}
	}

	// An undefined variable is reported once, by the evaluator.
	_, diags = resolve(t, "set mcp_output_limit := nope\n"+task)
	if len(diags) != 1 || strings.Contains(diags[0].Message, "invalid size") {
		t.Errorf("undefined variable: diagnostics = %v", diags)
	}
}
