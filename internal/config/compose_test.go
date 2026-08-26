package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/parser"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func srcProvider(_ string) ([]byte, bool) { return nil, false }

func TestComposeImportSplice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "common.rune", "shared:\n    @echo shared\n")
	main := writeFile(t, dir, "Runefile", "import \"common.rune\"\n\nbuild: shared\n    @echo build\n")

	src, _ := os.ReadFile(main)
	f, pd := parser.Parse(main, string(src))
	if pd.HasErrors() {
		t.Fatal(pd)
	}
	diags := Compose(f, diag.SourceProvider(srcProvider))
	if diags.HasErrors() {
		t.Fatalf("unexpected: %v", diags)
	}
	names := map[string]bool{}
	for _, tk := range f.Tasks {
		names[tk.Name] = true
	}
	if !names["shared"] || !names["build"] {
		t.Errorf("spliced tasks = %v, want shared+build", names)
	}
}

func TestComposeImportCollision(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "common.rune", "build:\n    @echo other\n")
	main := writeFile(t, dir, "Runefile", "import \"common.rune\"\n\nbuild:\n    @echo build\n")
	src, _ := os.ReadFile(main)
	f, _ := parser.Parse(main, string(src))
	diags := Compose(f, diag.SourceProvider(srcProvider))
	if !diags.HasErrors() {
		t.Fatal("expected an import collision diagnostic")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "collision") && strings.Contains(d.Message, "build") {
			found = true
		}
	}
	if !found {
		t.Errorf("diags = %v, want a build collision", diags)
	}
}

// TestComposeModCycleTerminates guards against unbounded recursion on a mod
// cycle (a mods b, b mods a). Reaching the assertion at all proves composition
// terminated rather than overflowing the stack.
func TestComposeModCycleTerminates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.rune", "mod b \"b.rune\"\n\natask:\n    @echo a\n")
	writeFile(t, dir, "b.rune", "mod a \"a.rune\"\n\nbtask:\n    @echo b\n")
	main := writeFile(t, dir, "Runefile", "mod a \"a.rune\"\n\nmain: a::atask\n    @echo main\n")
	src, _ := os.ReadFile(main)
	f, _ := parser.Parse(main, string(src))
	Compose(f, diag.SourceProvider(srcProvider))
	names := map[string]bool{}
	for _, tk := range f.Tasks {
		names[tk.Name] = true
	}
	if !names["a::atask"] {
		t.Errorf("namespaced tasks = %v, want a::atask", names)
	}
}

func TestComposeModNamespacing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docker.rune", "push:\n    @echo push\nbuild: push\n    @echo dbuild\n")
	main := writeFile(t, dir, "Runefile", "mod docker \"docker.rune\"\n\ndeploy: docker::push\n    @echo deploy\n")
	src, _ := os.ReadFile(main)
	f, _ := parser.Parse(main, string(src))
	diags := Compose(f, diag.SourceProvider(srcProvider))
	if diags.HasErrors() {
		t.Fatalf("unexpected: %v", diags)
	}
	names := map[string]bool{}
	for _, tk := range f.Tasks {
		names[tk.Name] = true
	}
	if !names["docker::push"] || !names["docker::build"] {
		t.Errorf("namespaced tasks = %v, want docker::push and docker::build", names)
	}
	// Intra-module dependency was rewritten to the namespaced form.
	for _, tk := range f.Tasks {
		if tk.Name == "docker::build" {
			if len(tk.Deps) != 1 || tk.Deps[0].Name != "docker::push" {
				t.Errorf("docker::build deps = %+v, want [docker::push]", tk.Deps)
			}
		}
	}
}

// Spec 022: || failure-hook references into module-local tasks are
// namespace-rewritten exactly like deps and && post-hooks.
func TestComposeImportRewritesFailHooks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ci.rune", "test: || diagnose\n    @echo t\ndiagnose:\n    @echo d\n")
	main := writeFile(t, dir, "Runefile", "mod ci \"ci.rune\"\n\nall: ci::test\n    @echo all\n")

	src, _ := os.ReadFile(main)
	f, pd := parser.Parse(main, string(src))
	if pd.HasErrors() {
		t.Fatal(pd)
	}
	if diags := Compose(f, diag.SourceProvider(srcProvider)); diags.HasErrors() {
		t.Fatalf("unexpected: %v", diags)
	}
	for _, tk := range f.Tasks {
		if tk.Name == "ci::test" {
			if len(tk.FailHooks) != 1 || tk.FailHooks[0].Name != "ci::diagnose" {
				t.Fatalf("fail hook not rewritten: %+v", tk.FailHooks)
			}
			return
		}
	}
	t.Fatal("ci::test not found after compose")
}
