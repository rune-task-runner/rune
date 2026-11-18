package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const importJustfile = "# Build it.\nbuild:\n    echo build\n\ntest: build\n    echo test\n"

// importProject makes a directory with the given files.
func importProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runImport runs Import in dir and returns what it printed.
func runImport(t *testing.T, dir string, flags ImportFlags, source string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	err = Import(Options{Cwd: dir, Stdout: &out, Stderr: &errb}, flags, source)
	return out.String(), errb.String(), err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// dirNames lists the entries of dir.
func dirNames(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, " ")
}

func TestImportWritesRunefileNextToSource(t *testing.T) {
	dir := importProject(t, map[string]string{"justfile": importJustfile})
	stdout, stderr, err := runImport(t, dir, ImportFlags{}, "")
	if err != nil {
		t.Fatalf("Import: %v\n%s", err, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing: the report goes to stderr", stdout)
	}
	wantReport := "justfile:2: converted: build\njustfile:5: converted: test\n" +
		"summary: 2 converted, 0 review, 0 unsupported, 0 wrappers\nwrote Runefile\n"
	if stderr != wantReport {
		t.Errorf("stderr = %q\nwant     %q", stderr, wantReport)
	}
	want := "# Imported from justfile by rune import.\n\n# Build it.\nbuild:\n    echo build\n\ntest: build\n    echo test\n"
	if got := readFile(t, filepath.Join(dir, "Runefile")); got != want {
		t.Errorf("Runefile = %q\nwant       %q", got, want)
	}
	if got := readFile(t, filepath.Join(dir, "justfile")); got != importJustfile {
		t.Errorf("the source changed: %q", got)
	}
	if err := checkGenerated(Options{Stderr: &bytes.Buffer{}}, filepath.Join(dir, "Runefile"), want); err != nil {
		t.Errorf("the written Runefile does not analyze: %v", err)
	}
}

func TestImportSourceArgument(t *testing.T) {
	files := map[string]string{"sub/justfile": importJustfile, "sub/tasks.txt": importJustfile}
	t.Run("a directory", func(t *testing.T) {
		dir := importProject(t, files)
		_, stderr, err := runImport(t, dir, ImportFlags{}, "sub")
		if err != nil || !strings.HasPrefix(stderr, "sub/justfile:2: converted: build\n") || !strings.HasSuffix(stderr, "wrote sub/Runefile\n") {
			t.Errorf("err = %v, stderr = %q", err, stderr)
		}
	})
	t.Run("a file", func(t *testing.T) {
		dir := importProject(t, files)
		_, stderr, err := runImport(t, dir, ImportFlags{}, filepath.Join("sub", "justfile"))
		if err != nil || !strings.HasPrefix(stderr, "sub/justfile:2: converted: build\n") {
			t.Errorf("err = %v, stderr = %q", err, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "sub", "Runefile")); err != nil {
			t.Errorf("sub/Runefile was not written: %v", err)
		}
	})
	t.Run("a file with another name and --from", func(t *testing.T) {
		dir := importProject(t, files)
		_, stderr, err := runImport(t, dir, ImportFlags{From: "justfile"}, "sub/tasks.txt")
		if err != nil || !strings.HasPrefix(stderr, "sub/tasks.txt:2: converted: build\n") {
			t.Errorf("err = %v, stderr = %q", err, stderr)
		}
	})
	t.Run("--from picks among several", func(t *testing.T) {
		dir := importProject(t, map[string]string{"justfile": importJustfile, "Makefile": ".PHONY: all\nall:\n\techo all\n"})
		_, stderr, err := runImport(t, dir, ImportFlags{From: "make"}, "")
		if err != nil || !strings.HasPrefix(stderr, "Makefile:2: converted: all\n") {
			t.Errorf("err = %v, stderr = %q", err, stderr)
		}
	})
}

func TestImportUsageErrors(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string
		flags  ImportFlags
		source string
		want   string
	}{
		{
			name: "no source", files: map[string]string{"README.md": "x"},
			want: "no task file found in . (looked for justfile, Justfile, .justfile, GNUmakefile, makefile, Makefile, Taskfile.yml, Taskfile.yaml, taskfile.yml, taskfile.yaml, package.json); name one: rune import <path>",
		},
		{
			name: "no source of the format", files: map[string]string{"justfile": importJustfile}, flags: ImportFlags{From: "npm"},
			want: "no task file found in . (looked for package.json); name one: rune import <path>",
		},
		{
			name: "several sources", files: map[string]string{"justfile": importJustfile, "package.json": "{}", "sub/x": ""}, source: "",
			want: "more than one task file in .: justfile, package.json; name one: rune import <path>",
		},
		{
			name: "an unknown format", files: map[string]string{"justfile": importJustfile}, flags: ImportFlags{From: "gradle"},
			want: `unknown format "gradle" (want justfile, make, task or npm)`,
		},
		{
			name: "a file name that tells no format", files: map[string]string{"tasks.txt": importJustfile}, source: "tasks.txt",
			want: "cannot tell the format of tasks.txt; pass --from justfile, make, task or npm",
		},
		{
			name: "no tasks", files: map[string]string{"package.json": `{"name": "x"}`},
			want: "package.json has no tasks to import",
		},
		{
			name: "an existing Runefile", files: map[string]string{"justfile": importJustfile, "Runefile": "old\n"},
			want: "Runefile already exists; write to another file with --output <path>, or replace it with --force",
		},
		{
			name: "an existing .runefile", files: map[string]string{"justfile": importJustfile, ".runefile": "old\n"},
			want: ".runefile already exists; write to another file with --output <path>, or replace it with --force",
		},
		{
			name: "an existing output", files: map[string]string{"justfile": importJustfile, "out.rune": "old\n"}, flags: ImportFlags{Output: "out.rune"},
			want: "out.rune already exists; write to another file with --output <path>, or replace it with --force",
		},
		{
			name:  "a second Runefile in the output directory",
			files: map[string]string{"justfile": importJustfile, "other/.runefile": "old\n"}, flags: ImportFlags{Output: "other/Runefile", Force: true},
			want: "other already has .runefile; a directory holds one Runefile",
		},
		{
			name:  "wrappers away from the source",
			files: map[string]string{"justfile": importJustfile, "other/x": ""}, flags: ImportFlags{Output: "other/Runefile", Wrap: true},
			want: "--wrap needs the Runefile next to justfile, because a wrapper runs the original runner there",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := importProject(t, tt.files)
			before := dirNames(t, dir)
			stdout, _, err := runImport(t, dir, tt.flags, tt.source)
			var usage *UsageError
			if !errors.As(err, &usage) || err.Error() != tt.want {
				t.Fatalf("err = %v\nwant a usage error: %s", err, tt.want)
			}
			if CodeFor(err) != ExitUsage {
				t.Errorf("exit = %d, want %d", CodeFor(err), ExitUsage)
			}
			if after := dirNames(t, dir); after != before || stdout != "" {
				t.Errorf("something was written: %q -> %q, stdout %q", before, after, stdout)
			}
			for name, text := range tt.files {
				if got := readFile(t, filepath.Join(dir, filepath.FromSlash(name))); got != text {
					t.Errorf("%s changed to %q", name, got)
				}
			}
		})
	}
}

func TestImportUnreadableSource(t *testing.T) {
	_, _, err := runImport(t, t.TempDir(), ImportFlags{}, "nowhere/justfile")
	var usage *UsageError
	if !errors.As(err, &usage) {
		t.Errorf("err = %v, want a usage error", err)
	}
}

func TestImportOutputAndForce(t *testing.T) {
	files := map[string]string{"justfile": importJustfile, "Runefile": "old\n", "out.rune": "old out\n"}
	t.Run("--output keeps the Runefile", func(t *testing.T) {
		dir := importProject(t, files)
		_, stderr, err := runImport(t, dir, ImportFlags{Output: "imported.rune"}, "")
		if err != nil || !strings.HasSuffix(stderr, "wrote imported.rune\n") || strings.Contains(stderr, "note:") {
			t.Fatalf("err = %v, stderr = %q", err, stderr)
		}
		if got := readFile(t, filepath.Join(dir, "Runefile")); got != "old\n" {
			t.Errorf("the Runefile changed: %q", got)
		}
		if got := readFile(t, filepath.Join(dir, "imported.rune")); !strings.Contains(got, "build:") {
			t.Errorf("imported.rune = %q", got)
		}
	})
	t.Run("--force replaces the default output", func(t *testing.T) {
		dir := importProject(t, files)
		if _, _, err := runImport(t, dir, ImportFlags{Force: true}, ""); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, filepath.Join(dir, "Runefile")); !strings.Contains(got, "build:") {
			t.Errorf("Runefile = %q, want it replaced", got)
		}
	})
	t.Run("--force replaces an existing .runefile and adds no Runefile", func(t *testing.T) {
		dir := importProject(t, map[string]string{"justfile": importJustfile, ".runefile": "old\n"})
		_, stderr, err := runImport(t, dir, ImportFlags{Force: true}, "")
		if err != nil || !strings.HasSuffix(stderr, "wrote .runefile\n") {
			t.Fatalf("err = %v, stderr = %q", err, stderr)
		}
		if got := dirNames(t, dir); got != ".runefile justfile" {
			t.Errorf("directory = %q", got)
		}
	})
	t.Run("--force replaces the named output", func(t *testing.T) {
		dir := importProject(t, files)
		if _, _, err := runImport(t, dir, ImportFlags{Output: "out.rune", Force: true}, ""); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, filepath.Join(dir, "out.rune")); !strings.Contains(got, "build:") {
			t.Errorf("out.rune = %q, want it replaced", got)
		}
	})
	t.Run("--force never replaces the source", func(t *testing.T) {
		dir := importProject(t, map[string]string{"justfile": importJustfile})
		for _, output := range []string{"justfile", "./sub/../justfile"} {
			_, stderr, err := runImport(t, dir, ImportFlags{Output: output, Force: true}, "")
			var usage *UsageError
			want := "justfile is the source, which an import never changes; write to another file with --output <path>"
			if !errors.As(err, &usage) || err.Error() != want {
				t.Errorf("--output %s: err = %v, stderr = %q, want %q", output, err, stderr, want)
			}
		}
		if err := os.Symlink("justfile", filepath.Join(dir, "link.rune")); err == nil {
			if _, _, err := runImport(t, dir, ImportFlags{Output: "link.rune", Force: true}, ""); err == nil {
				t.Error("--output with a link to the source: no error")
			}
		}
		if got := readFile(t, filepath.Join(dir, "justfile")); got != importJustfile {
			t.Errorf("the source changed: %q", got)
		}
		if _, _, err := runImport(t, dir, ImportFlags{Output: "justfile", DryRun: true}, ""); err != nil {
			t.Errorf("--dry-run writes nothing, so it is not refused: %v", err)
		}
	})
	t.Run("an output in another directory adds a note", func(t *testing.T) {
		dir := importProject(t, map[string]string{"justfile": importJustfile, "other/x": ""})
		_, stderr, err := runImport(t, dir, ImportFlags{Output: "other/Runefile"}, "")
		want := "summary: 2 converted, 0 review, 0 unsupported, 0 wrappers\n" +
			"note: tasks run from other, the directory of the Runefile\nwrote other/Runefile\n"
		if err != nil || !strings.HasSuffix(stderr, want) {
			t.Errorf("err = %v, stderr = %q", err, stderr)
		}
	})
	t.Run("an output that cannot be written", func(t *testing.T) {
		dir := importProject(t, map[string]string{"justfile": importJustfile})
		_, _, err := runImport(t, dir, ImportFlags{Output: "missing/dir/Runefile"}, "")
		var failure *CommandFailure
		if !errors.As(err, &failure) || CodeFor(err) != ExitTaskFail {
			t.Errorf("err = %v, want a command failure", err)
		}
	})
}

func TestImportDryRun(t *testing.T) {
	dir := importProject(t, map[string]string{"justfile": importJustfile, "Runefile": "old\n"})
	before := dirNames(t, dir)
	stdout, stderr, err := runImport(t, dir, ImportFlags{DryRun: true, Output: "x.rune"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "# Imported from justfile by rune import.\n") || !strings.Contains(stdout, "build:") {
		t.Errorf("stdout = %q, want the Runefile", stdout)
	}
	if !strings.HasSuffix(stderr, "dry run: nothing written\n") {
		t.Errorf("stderr = %q", stderr)
	}
	if after := dirNames(t, dir); after != before {
		t.Errorf("a dry run changed the directory: %q -> %q", before, after)
	}
	if got := readFile(t, filepath.Join(dir, "Runefile")); got != "old\n" {
		t.Errorf("the Runefile changed: %q", got)
	}
}

func TestImportPartial(t *testing.T) {
	source := "ok:\n    echo ok\n\nbad:\n    echo {{uppercase(\"x\")}}\n"
	dir := importProject(t, map[string]string{"justfile": source})
	_, stderr, err := runImport(t, dir, ImportFlags{}, "")
	var failure *CommandFailure
	if !errors.As(err, &failure) || err.Error() != "import: 1 of 2 items were not converted" {
		t.Fatalf("err = %v, want the partial failure", err)
	}
	if CodeFor(err) != ExitTaskFail {
		t.Errorf("exit = %d, want %d", CodeFor(err), ExitTaskFail)
	}
	if !strings.Contains(stderr, "justfile:4: unsupported: bad: ") || !strings.HasSuffix(stderr, "wrote Runefile\n") {
		t.Errorf("stderr = %q", stderr)
	}
	if got := readFile(t, filepath.Join(dir, "Runefile")); !strings.Contains(got, "ok:") || !strings.Contains(got, "bad *args:") {
		t.Errorf("Runefile = %q, want both tasks", got)
	}

	t.Run("wrappers complete the import", func(t *testing.T) {
		dir := importProject(t, map[string]string{"justfile": source})
		_, stderr, err := runImport(t, dir, ImportFlags{Wrap: true}, "")
		if err != nil || !strings.Contains(stderr, "justfile:4: wrapper: bad: ") {
			t.Errorf("err = %v, stderr = %q", err, stderr)
		}
	})
}

func TestImportSourceThatDoesNotParse(t *testing.T) {
	dir := importProject(t, map[string]string{"justfile": "build:\n    echo build\n\n\t??? what\n"})
	stdout, stderr, err := runImport(t, dir, ImportFlags{}, "")
	var invalid *ValidationError
	if !errors.As(err, &invalid) || CodeFor(err) != ExitValidation {
		t.Fatalf("err = %v, want a validation error", err)
	}
	want := "justfile:4:1: error: the line is indented less than the first line of the task\n4 | \t??? what\n  | ^^^^^^^^^\n"
	if stderr != want || stdout != "" {
		t.Errorf("stderr = %q\nwant     %q", stderr, want)
	}
	if got := dirNames(t, dir); got != "justfile" {
		t.Errorf("directory = %q, want nothing written", got)
	}
}

func TestCheckGenerated(t *testing.T) {
	var errb bytes.Buffer
	opts := Options{Stderr: &errb}
	if err := checkGenerated(opts, "Runefile", "a:\n    echo a\n"); err != nil || errb.Len() != 0 {
		t.Errorf("a valid Runefile: err = %v, stderr = %q", err, errb.String())
	}
	// A warning is not a failure and is not printed.
	if err := checkGenerated(opts, "Runefile", "a p:\n    echo {{p}}\n"); err != nil || errb.Len() != 0 {
		t.Errorf("a Runefile with warnings: err = %v, stderr = %q", err, errb.String())
	}
	err := checkGenerated(opts, "Runefile", "a: missing\n    echo a\n")
	var failure *CommandFailure
	if !errors.As(err, &failure) || err.Error() != "import: the generated Runefile is not valid; this is a bug in rune import" {
		t.Errorf("err = %v, want the bug failure", err)
	}
	if !strings.Contains(errb.String(), "Runefile:1:") || strings.Contains(errb.String(), "warning") {
		t.Errorf("stderr = %q, want the error diagnostics only", errb.String())
	}
}

func TestImportCandidatesComeFromTheListing(t *testing.T) {
	// One entry is one candidate, whatever names the file system would also
	// open it by.
	dir := importProject(t, map[string]string{"Justfile": importJustfile})
	_, stderr, err := runImport(t, dir, ImportFlags{DryRun: true}, "")
	if err != nil || !strings.HasPrefix(stderr, "Justfile:2: converted: build\n") {
		t.Errorf("err = %v, stderr = %q", err, stderr)
	}
}
