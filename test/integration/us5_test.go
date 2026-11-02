package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUS5_CacheRunThenSkip(t *testing.T) {
	dir := writeRunefile(t, "[cache(inputs = [\"in.txt\"], outputs = [\"out.txt\"])]\nbuild:\n    cp in.txt out.txt\n")
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	r1 := run(t, dir, nil, "build")
	if r1.code != 0 {
		t.Fatalf("first run exit=%d stderr=%s", r1.code, r1.stderr)
	}
	if !strings.Contains(r1.stderr, "running: build") {
		t.Errorf("first run should log 'running: build': %q", r1.stderr)
	}

	r2 := run(t, dir, nil, "build")
	if r2.code != 0 {
		t.Fatalf("second run exit=%d", r2.code)
	}
	if !strings.Contains(r2.stderr, "cached: build") {
		t.Errorf("second run should log 'cached: build': %q", r2.stderr)
	}
}

func TestUS5_CacheInvalidatesOnInputChange(t *testing.T) {
	dir := writeRunefile(t, "[cache(inputs = [\"in.txt\"], outputs = [\"out.txt\"])]\nbuild:\n    cp in.txt out.txt\n")
	_ = os.WriteFile(filepath.Join(dir, "in.txt"), []byte("v1"), 0o644)
	run(t, dir, nil, "build")
	_ = os.WriteFile(filepath.Join(dir, "in.txt"), []byte("v2-changed"), 0o644)
	r := run(t, dir, nil, "build")
	if !strings.Contains(r.stderr, "running: build") {
		t.Errorf("changed input should re-run: %q", r.stderr)
	}
}

func TestUS5_DryRunExecutesNothing(t *testing.T) {
	dir := writeRunefile(t, "greet:\n    @echo SHOULD-NOT-RUN\nbuild: greet\n    @echo NOPE\n")
	r := run(t, dir, nil, "--dry-run", "build")
	if r.code != 0 {
		t.Fatalf("exit=%d", r.code)
	}
	if strings.Contains(r.stdout, "SHOULD-NOT-RUN") || strings.Contains(r.stdout, "NOPE") {
		t.Errorf("--dry-run executed a task: %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "would run: greet") || !strings.Contains(r.stderr, "would run: build") {
		t.Errorf("--dry-run plan missing tasks: %q", r.stderr)
	}
}

func TestUS5_Parallel(t *testing.T) {
	src := "[parallel]\nchecks: lint test\n    @echo checks-done\nlint:\n    @echo lint\ntest:\n    @echo test\n"
	dir := writeRunefile(t, src)
	r := run(t, dir, nil, "checks")
	if r.code != 0 {
		t.Fatalf("exit=%d stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "lint") || !strings.Contains(r.stdout, "test") || !strings.Contains(r.stdout, "checks-done") {
		t.Errorf("parallel deps output incomplete: %q", r.stdout)
	}
}

func TestUS5_DumpJSON(t *testing.T) {
	dir := writeRunefile(t, "set default := \"greet\"\ngreet name=\"world\":\n    @echo hi {{name}}\n")
	r := run(t, dir, nil, "--dump", "--format", "json")
	if r.code != 0 {
		t.Fatalf("exit=%d stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "\"tasks\"") || !strings.Contains(r.stdout, "\"greet\"") {
		t.Errorf("--dump json missing structure: %q", r.stdout)
	}
	// Nothing executed.
	if strings.Contains(r.stdout, "hi world") {
		t.Errorf("--dump executed a task: %q", r.stdout)
	}
}

func TestUS5_ClearCache(t *testing.T) {
	dir := writeRunefile(t, "[cache(inputs = [\"in.txt\"], outputs = [\"out.txt\"])]\nbuild:\n    cp in.txt out.txt\n")
	_ = os.WriteFile(filepath.Join(dir, "in.txt"), []byte("x"), 0o644)
	run(t, dir, nil, "build")
	if _, err := os.Stat(filepath.Join(dir, ".rune", "cache")); err != nil {
		t.Fatalf("cache dir should exist after a cached run: %v", err)
	}
	r := run(t, dir, nil, "--clear-cache")
	if r.code != 0 {
		t.Fatalf("--clear-cache exit=%d", r.code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rune", "cache")); !os.IsNotExist(err) {
		t.Errorf("cache dir should be removed after --clear-cache")
	}
}

func TestUS5_ImportSplice(t *testing.T) {
	dir := writeRunefile(t, "import \"common.rune\"\n\nbuild: shared\n    @echo build-done\n")
	if err := os.WriteFile(filepath.Join(dir, "common.rune"), []byte("shared:\n    @echo shared-ran\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, dir, nil, "build")
	if r.code != 0 {
		t.Fatalf("exit=%d stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "shared-ran") || !strings.Contains(r.stdout, "build-done") {
		t.Errorf("import splice output: %q", r.stdout)
	}
}

// A variable interpolated as {{ v }}, with spaces, is part of the cache
// fingerprint: changing it re-runs the task (issue #69).
func TestUS5_CacheInvalidatesOnSpacedInterpolation(t *testing.T) {
	dir := writeRunefile(t, "[cache(inputs = [\"in.txt\"], outputs = [\"out.txt\"])]\nbuild v=\"1\":\n    @echo {{ v }} > out.txt\n")
	_ = os.WriteFile(filepath.Join(dir, "in.txt"), []byte("v1"), 0o644)
	if r := run(t, dir, nil, "build", "1"); r.code != 0 || !strings.Contains(r.stderr, "running: build") {
		t.Fatalf("first run: exit=%d stderr=%q", r.code, r.stderr)
	}
	r := run(t, dir, nil, "build", "2")
	if !strings.Contains(r.stderr, "running: build") {
		t.Errorf("changed interpolated value should re-run: exit=%d stderr=%q", r.code, r.stderr)
	}
	if out, _ := os.ReadFile(filepath.Join(dir, "out.txt")); strings.TrimSpace(string(out)) != "2" {
		t.Errorf("out.txt = %q, want 2", out)
	}
}
