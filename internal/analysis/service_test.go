package analysis

import (
	"context"
	"reflect"
	"testing"

	"github.com/rune-task-runner/rune/internal/analyzer"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/parser"
)

func TestServiceAnalyzeDiagnosticsAndSymbols(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "Runefile", "output := \"dist\"\n# Build.\nbuild:\n    @echo build\ndeploy: missing\n    @echo deploy\n")

	svc := NewService(DiskSourceStore{})
	snap, err := svc.Analyze(context.Background(), AnalyzeRequest{URI: path, Version: 1})
	if err != nil {
		t.Fatal(err)
	}

	// The unknown dependency is reported with its stable code (FR-010).
	if firstCode(snap.Diagnostics, diag.CodeUnknownDependency) == nil {
		t.Errorf("expected RUNE2001 unknown dependency, got %v", snap.Diagnostics)
	}
	if !snap.HasErrors() {
		t.Error("snapshot should have errors")
	}
	// Symbols are indexed.
	if len(snap.Symbols.Tasks()) != 2 {
		t.Errorf("expected 2 tasks indexed, got %d", len(snap.Symbols.Tasks()))
	}
	if len(snap.Symbols.ByName["output"]) != 1 {
		t.Error("variable 'output' should be indexed")
	}
}

// TestServiceParityWithExecutionAnalyzer asserts the service reports the same
// semantic diagnostics as the execution-path analyzer for the same source
// (spec FR-002 / SC-002).
func TestServiceParityWithExecutionAnalyzer(t *testing.T) {
	src := "a: b\n    @echo a\n"
	dir := t.TempDir()
	path := writeTemp(t, dir, "Runefile", src)

	// Execution-path diagnostics (parse + analyze), as `rune` run would compute.
	f, _ := parser.Parse(path, src)
	want := analyzer.Analyze(f)

	svc := NewService(DiskSourceStore{})
	snap, err := svc.Analyze(context.Background(), AnalyzeRequest{URI: path, Version: 1})
	if err != nil {
		t.Fatal(err)
	}

	// Every execution-path diagnostic code appears in the snapshot.
	for _, d := range want {
		if d.Code != "" && firstCode(snap.Diagnostics, d.Code) == nil {
			t.Errorf("snapshot missing execution-path diagnostic %s (%s)", d.Code, d.Message)
		}
	}
}

func TestServiceContentOverride(t *testing.T) {
	// Content override analyzes unsaved buffer text without touching disk.
	svc := NewService(DiskSourceStore{})
	snap, err := svc.Analyze(context.Background(), AnalyzeRequest{
		URI:     "/virtual/Runefile",
		Content: "greet:\n    @echo {{nope}}\n",
		Version: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != 7 {
		t.Errorf("version = %d, want 7", snap.Version)
	}
	if firstCode(snap.Diagnostics, diag.CodeUndefinedVariable) == nil {
		t.Errorf("expected undefined-variable diagnostic, got %v", snap.Diagnostics)
	}
}

func firstCode(diags diag.List, code string) *diag.Diagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}

// Spec 030 FR-014: RUNE2017 reaches `rune analyze` and the editor as a
// warning, at the path of the file that declares the task, and never the run
// path.
func TestServiceReportsUnquotedParam(t *testing.T) {
	dir := t.TempDir()
	common := writeTemp(t, dir, "common.rune", "# Deploy.\ndeploy env:\n    ./deploy.sh {{env}}\n")
	src := "import \"common.rune\"\n\n# Greet.\ngreet name:\n    echo {{name}}\n"
	path := writeTemp(t, dir, "Runefile", src)

	snap, err := NewService(DiskSourceStore{}).Analyze(context.Background(), AnalyzeRequest{URI: path, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, d := range snap.Diagnostics {
		if d.Code != diag.CodeUnquotedParam {
			continue
		}
		if d.Severity != diag.Warning {
			t.Errorf("severity = %v, want warning", d.Severity)
		}
		files[d.Span.File] = true
	}
	if want := map[string]bool{path: true, common: true}; !reflect.DeepEqual(files, want) {
		t.Errorf("RUNE2017 reported in %v, want %v", files, want)
	}

	f, _ := parser.Parse(path, src)
	if d := firstCode(analyzer.Analyze(f), diag.CodeUnquotedParam); d != nil {
		t.Errorf("the run path reported RUNE2017: %+v", d)
	}
}
