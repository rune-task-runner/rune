package taskimport

import (
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/analyzer"
	runeparser "github.com/rune-task-runner/rune/internal/parser"
)

var update = flag.Bool("update", false, "regenerate golden files")

// sourceNames is the name each format's source has in the golden reports.
var sourceNames = map[string]string{
	"justfile": "justfile", "make": "Makefile", "task": "Taskfile.yml", "npm": "package.json",
}

// goldenCases lists testdata/<format>/<case> directories.
func goldenCases(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("testdata", "*", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no golden cases found: %v", err)
	}
	return dirs
}

func caseOptions(dir string) Options {
	format := filepath.Base(filepath.Dir(dir))
	_, err := os.Stat(filepath.Join(dir, "wrap"))
	return Options{
		Format:     Format(format),
		SourceName: sourceNames[format],
		RunnerPath: sourceNames[format],
		Wrap:       err == nil,
	}
}

// checkRunefile fails when Rune's own parser or analyzer rejects text.
func checkRunefile(t *testing.T, text string) {
	t.Helper()
	file, diags := runeparser.Parse("Runefile", text)
	if !diags.HasErrors() {
		diags = append(diags, analyzer.Analyze(file)...)
	}
	if diags.HasErrors() {
		t.Errorf("the Runefile does not analyze: %v\n%s", diags, text)
	}
}

// TestGolden converts every case and compares the Runefile and the report
// with the committed files. A case with a file named "wrap" is converted
// with wrappers.
func TestGolden(t *testing.T) {
	for _, dir := range goldenCases(t) {
		t.Run(filepath.ToSlash(strings.TrimPrefix(dir, "testdata"+string(filepath.Separator))), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "source"))
			if err != nil {
				t.Fatal(err)
			}
			var runefile, report string
			res, err := Convert(data, caseOptions(dir))
			if err != nil {
				report = "error: " + err.Error() + "\n"
			} else {
				runefile, report = res.Runefile, res.Report
				checkRunefile(t, runefile)
			}
			compareGolden(t, filepath.Join(dir, "want.Runefile"), runefile)
			compareGolden(t, filepath.Join(dir, "want.report"), report)
		})
	}
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run the test with -update to create it)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s differs:\n%s\nwant:\n%s", path, got, want)
	}
}

// TestConvertTruncated cuts every golden source at every byte. A reader
// must never panic on a broken file, and whatever it accepts must still give
// a Runefile that analyzes.
func TestConvertTruncated(t *testing.T) {
	for _, dir := range goldenCases(t) {
		data, err := os.ReadFile(filepath.Join(dir, "source"))
		if err != nil {
			t.Fatal(err)
		}
		opts := caseOptions(dir)
		for n := range len(data) {
			res, err := Convert(data[:n], opts)
			if err != nil {
				continue
			}
			checkRunefile(t, res.Runefile)
			if t.Failed() {
				t.Fatalf("%s cut at byte %d", dir, n)
			}
		}
	}
}

// TestStaticImports keeps the package unable to run or fetch anything: it
// converts the bytes it is given and nothing else.
func TestStaticImports(t *testing.T) {
	forbidden := map[string]bool{
		`"os"`: true, `"os/exec"`: true, `"net"`: true, `"net/http"`: true, `"mvdan.cc/sh/v3/interp"`: true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if forbidden[imp.Path.Value] {
				t.Errorf("%s imports %s: this package must only read the text it is given", name, imp.Path.Value)
			}
		}
	}
}
