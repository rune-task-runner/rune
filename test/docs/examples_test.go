package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestExamplesStaticallyValidate is Tier A: every example Runefile must parse
// and analyze cleanly (`rune --file <path> --list`, exit 0). Runs on every OS;
// needs no interpreter.
func TestExamplesStaticallyValidate(t *testing.T) {
	dirs := exampleDirs(t)
	if len(dirs) == 0 {
		t.Fatal("no example directories with a Runefile under docs/examples")
	}
	for _, name := range dirs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rf := filepath.Join(repoRoot, "docs", "examples", name, "Runefile")
			if got := validate(t, rf); got.code != 0 {
				t.Errorf("`rune --file %s --list` exit = %d, want 0\nstderr:\n%s",
					relPath(rf), got.code, got.stderr)
			}
		})
	}
}

// TestExamplesRun is Tier B: run each example's documented command and assert it
// exits 0. An example whose README declares a prerequisite tool (node, python,
// docker, an agent CLI) is skipped — with a logged reason, never silently — when
// that tool is absent.
func TestExamplesRun(t *testing.T) {
	for _, name := range exampleDirs(t) {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(repoRoot, "docs", "examples", name)
			readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
			if err != nil {
				t.Fatalf("read README: %v", err)
			}
			for _, pr := range examplePrereqs(string(readme)) {
				bin := prereqBinary(pr)
				if bin == "" {
					t.Skipf("%s: needs an agent CLI; skipping run", name)
				}
				if _, err := exec.LookPath(bin); err != nil {
					t.Skipf("%s: %s not installed; skipping run", name, bin)
				}
			}
			args, ok := exampleRunArgs(string(readme))
			if !ok {
				t.Skipf("%s: no runnable `rune` command in the README's \"Run it\" block", name)
			}
			if got := runRune(t, dir, args...); got.code != 0 {
				t.Errorf("`rune %s` in %s exit = %d, want 0\nstderr:\n%s",
					strings.Join(args, " "), name, got.code, got.stderr)
			}
		})
	}
}

// TestGettingStartedExampleRuns additionally asserts the getting-started
// example's documented output, as a representative exact-output check.
func TestGettingStartedExampleRuns(t *testing.T) {
	dir := filepath.Join(repoRoot, "docs", "examples", "getting-started")
	got := runRune(t, dir, "greet")
	if got.code != 0 {
		t.Fatalf("`rune greet` exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if want := "Hello, world!"; !strings.Contains(got.stdout, want) {
		t.Errorf("`rune greet` stdout = %q, want it to contain %q", got.stdout, want)
	}
}

// TestSettingsDotenvExampleOutput holds the settings-dotenv example to the
// output its README documents. The example once printed "greeting=unset" while
// its README promised the .env value, and exiting 0 was all that was checked.
func TestSettingsDotenvExampleOutput(t *testing.T) {
	dir := filepath.Join(repoRoot, "docs", "examples", "settings-dotenv")
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	const want = "app=demo greeting=hello\n"
	if !strings.Contains(string(readme), "```text\n"+want+"```") {
		t.Fatalf("the README no longer documents the output %q", want)
	}
	got := runRune(t, dir, "show")
	if got.code != 0 || got.stdout != want {
		t.Errorf("`rune show` exit = %d, stdout = %q, want %q\nstderr:\n%s", got.code, got.stdout, want, got.stderr)
	}
}

// examplePrereqs scans a README for the prerequisite tools it declares. An empty
// result means "none" (runnable everywhere).
func examplePrereqs(readme string) []string {
	for _, line := range strings.Split(readme, "\n") {
		if !strings.Contains(line, "Prerequisites:") {
			continue
		}
		lower := strings.ToLower(line)
		var prereqs []string
		for _, tok := range []string{"node", "python", "docker", "agent", "opentofu", "terraform"} {
			if strings.Contains(lower, tok) {
				prereqs = append(prereqs, tok)
			}
		}
		return prereqs
	}
	return nil
}

// prereqBinary maps a prerequisite token to the executable to look for on PATH.
// An agent CLI has no fixed name, so it returns "" (always skip Tier-B).
func prereqBinary(prereq string) string {
	switch prereq {
	case "node":
		return "node"
	case "python":
		return "python3"
	case "docker":
		return "docker"
	case "opentofu":
		return "tofu"
	case "terraform":
		return "terraform"
	default:
		return ""
	}
}

// TestExamplePrereqBinaries holds a README's prerequisite line to the binary
// Tier B looks for. A tool the scan does not know reads as "none", and the
// example would then run, and fail, on a host without that tool.
func TestExamplePrereqBinaries(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{"**Prerequisites:** none", nil},
		{"**Prerequisites:** docker", []string{"docker"}},
		{"**Prerequisites:** python3, node", []string{"node", "python3"}},
		{"**Prerequisites:** opentofu", []string{"tofu"}},
		{"**Prerequisites:** terraform", []string{"terraform"}},
		{"**Prerequisites:** an agent CLI (e.g. `claude`, `codex`)", []string{""}},
	}
	for _, tt := range tests {
		var got []string
		for _, pr := range examplePrereqs("# Title\n\n" + tt.line + "\n") {
			got = append(got, prereqBinary(pr))
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%q: binaries = %q, want %q", tt.line, got, tt.want)
		}
	}
}

// exampleRunArgs extracts the arguments of the first `rune <args…>` command in
// the README's "## Run it" section (the fenced sh block).
func exampleRunArgs(readme string) ([]string, bool) {
	const heading = "## Run it"
	start := strings.Index(readme, heading)
	if start < 0 {
		return nil, false
	}
	section := readme[start+len(heading):]
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}
	for _, block := range fencedBlocks(section, "sh") {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "rune ") {
				return strings.Fields(line)[1:], true
			}
		}
	}
	return nil, false
}
