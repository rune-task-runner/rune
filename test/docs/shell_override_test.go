package docs

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// setShellRe matches a `set shell` setting with a quoted list. Prose such as
// `set shell := [...]` has no quote inside the brackets and is not matched.
var setShellRe = regexp.MustCompile(`set shell := (\[[^\]]*"[^\]]*\])`)

// Spec 029 SC-004: every `set shell` example in the docs stops at a failing line,
// which under a system shell takes the shell's own -e.
func TestShellOverride_DocsExamplesStopOnFailure(t *testing.T) {
	// bash on Windows CI may be the WSL launcher or Git Bash (spec 029 research R7).
	runExamples := runtime.GOOS != "windows"
	found := 0
	for _, f := range docMarkdownFiles(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range setShellRe.FindAllStringSubmatch(string(src), -1) {
			found++
			var list []string
			if err := json.Unmarshal([]byte(m[1]), &list); err != nil || len(list) == 0 {
				t.Errorf("%s: %s: want a non-empty list of strings (%v)", relPath(f), m[0], err)
				continue
			}
			if !hasErrexit(list[1:]) {
				t.Errorf("%s: %s: no -e flag, so a failing line would not stop the task", relPath(f), m[0])
			}
			if !runExamples {
				continue
			}
			if _, err := exec.LookPath(list[0]); err != nil {
				t.Logf("%s: %s: %s is not installed, so the example is not run", relPath(f), m[0], list[0])
				continue
			}
			dir := t.TempDir()
			runefile := m[0] + "\n\nt:\n    @echo before\n    @false\n    @echo after\n"
			if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(runefile), 0o644); err != nil {
				t.Fatal(err)
			}
			if r := runRune(t, dir, "t"); r.code == 0 || strings.Contains(r.stdout, "after") {
				t.Errorf("%s: %s: a failing middle line did not stop the task (exit %d, stdout %q)",
					relPath(f), m[0], r.code, r.stdout)
			}
		}
	}
	if found == 0 {
		t.Fatal("no `set shell := [...]` example found in the docs; the pattern no longer matches them")
	}
}

// The link checker skips anchors (links_test.go), so the Shell override heading
// and the links to it are checked here.
func TestShellOverride_DocsSection(t *testing.T) {
	heading := regexp.MustCompile("(?m)^### Shell override\r?$")
	if !heading.MatchString(readDoc(t, "docs/runefile.md")) {
		t.Error("docs/runefile.md has no `### Shell override` heading")
	}
	for _, page := range []string{"docs/how-to/settings-and-dotenv.md", "docs/how-to/executors.md"} {
		if !strings.Contains(readDoc(t, page), "runefile.md#shell-override") {
			t.Errorf("%s does not link to runefile.md#shell-override", page)
		}
	}
}

func TestHasErrexit(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"-euo", "pipefail"}, true},
		{[]string{"-e"}, true},
		{[]string{"-o", "errexit"}, true},
		{[]string{"-uo", "errexit"}, true},
		{[]string{"-u", "-o", "pipefail"}, false},
		{[]string{"errexit"}, false},
		{[]string{"--norc"}, false},
	}
	for _, tt := range tests {
		if got := hasErrexit(tt.args); got != tt.want {
			t.Errorf("hasErrexit(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

// hasErrexit reports whether args turn on errexit, as -e in a cluster such as
// -eu or -euo, or as -o errexit.
func hasErrexit(args []string) bool {
	for i, arg := range args {
		if isFlagCluster(arg) && strings.Contains(arg, "e") {
			return true
		}
		if arg == "errexit" && i > 0 && isFlagCluster(args[i-1]) && strings.Contains(args[i-1], "o") {
			return true
		}
	}
	return false
}

// isFlagCluster reports whether arg is a single dash followed only by letters,
// which rules out long options such as --norc.
func isFlagCluster(arg string) bool {
	rest, ok := strings.CutPrefix(arg, "-")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
