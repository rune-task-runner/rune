package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func specFor(root string) Spec {
	return Spec{
		Key:      "build",
		Root:     root,
		Inputs:   []string{"src/**/*.go", "go.mod"},
		Outputs:  []string{"out/bin"},
		Body:     "go build",
		Vars:     map[string]string{"target": "linux"},
		Executor: "sh",
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCacheMissThenHit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module x")
	writeFile(t, filepath.Join(root, "src", "a.go"), "package a")
	writeFile(t, filepath.Join(root, "out", "bin"), "binary")

	spec := specFor(root)

	d1, err := Decide(spec)
	if err != nil {
		t.Fatal(err)
	}
	if d1.Skip {
		t.Fatal("first run should not skip (no stored record)")
	}
	if err := Store(spec, d1.Hash, "2026-06-08T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	d2, err := Decide(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !d2.Skip {
		t.Fatal("second run with unchanged inputs should skip")
	}
}

func TestCacheInputChangeForcesRun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module x")
	writeFile(t, filepath.Join(root, "src", "a.go"), "package a")
	writeFile(t, filepath.Join(root, "out", "bin"), "binary")
	spec := specFor(root)

	d, _ := Decide(spec)
	_ = Store(spec, d.Hash, "t")

	writeFile(t, filepath.Join(root, "src", "a.go"), "package a // changed")
	d2, _ := Decide(spec)
	if d2.Skip {
		t.Error("changed input must force a re-run")
	}
}

func TestCacheMissingOutputForcesRun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module x")
	writeFile(t, filepath.Join(root, "out", "bin"), "binary")
	spec := specFor(root)
	d, _ := Decide(spec)
	_ = Store(spec, d.Hash, "t")

	_ = os.Remove(filepath.Join(root, "out", "bin"))
	d2, _ := Decide(spec)
	if d2.Skip {
		t.Error("missing output must force a re-run")
	}
}

func TestCacheVarChangeForcesRun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module x")
	writeFile(t, filepath.Join(root, "out", "bin"), "binary")
	spec := specFor(root)
	d, _ := Decide(spec)
	_ = Store(spec, d.Hash, "t")

	spec.Vars["target"] = "windows"
	d2, _ := Decide(spec)
	if d2.Skip {
		t.Error("changed variable must force a re-run")
	}
}

func TestCacheCorruptionIsMiss(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module x")
	writeFile(t, filepath.Join(root, "out", "bin"), "binary")
	spec := specFor(root)
	d, _ := Decide(spec)
	_ = Store(spec, d.Hash, "t")
	// Corrupt the record.
	writeFile(t, RecordPath(spec), "{not valid json")
	d2, err := Decide(spec)
	if err != nil {
		t.Fatalf("corruption must not error: %v", err)
	}
	if d2.Skip {
		t.Error("corruption must be treated as a miss")
	}
}

func TestClear(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module x")
	writeFile(t, filepath.Join(root, "out", "bin"), "binary")
	spec := specFor(root)
	d, _ := Decide(spec)
	_ = Store(spec, d.Hash, "t")
	if err := Clear(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cacheDir(root)); !os.IsNotExist(err) {
		t.Error("cache dir should be gone after Clear")
	}
}

// Spec 059: a CI bundle carries the record and the files of the outputs.

func TestRecordPathIsWhereStoreWrites(t *testing.T) {
	root := t.TempDir()
	spec := specFor(root)
	spec.Key = "api::build"
	if err := Store(spec, "h", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(RecordPath(spec)); err != nil {
		t.Fatalf("RecordPath(%q) = %q: %v", spec.Key, RecordPath(spec), err)
	}
	if want := filepath.Join(root, ".rune", "cache", "api__build.json"); RecordPath(spec) != want {
		t.Errorf("RecordPath = %q, want %q", RecordPath(spec), want)
	}
}

func TestOutputFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "out", "bin"), "x")
	writeFile(t, filepath.Join(root, "dist", "a.txt"), "a")
	writeFile(t, filepath.Join(root, "dist", "sub", "b.txt"), "b")
	writeFile(t, filepath.Join(root, "gen", "x.go"), "x")
	writeFile(t, filepath.Join(root, "gen", "y.txt"), "y")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := OutputFiles(root, []string{"out/bin", "dist", "gen/*.go", "missing/*", "empty", "out/bin"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dist", "dist/a.txt", "dist/sub", "dist/sub/b.txt", "empty", "gen/x.go", "out/bin"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("OutputFiles = %v, want %v", got, want)
	}
}

func TestOutputFilesNone(t *testing.T) {
	got, err := OutputFiles(t.TempDir(), []string{"nothing", "none/**"})
	if err != nil || len(got) != 0 {
		t.Errorf("OutputFiles = %v, %v; want none", got, err)
	}
}

func TestRecordPathWithNamespace(t *testing.T) {
	spec := specFor(t.TempDir())
	spec.Namespace = "api"
	if got := filepath.Base(RecordPath(spec)); got != "api__build.json" {
		t.Errorf("RecordPath = %q", got)
	}
}
