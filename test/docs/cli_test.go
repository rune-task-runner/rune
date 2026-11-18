package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var longFlagRe = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)

// longFlags returns the set of long flag names (without the leading --) in text.
func longFlags(text string) map[string]bool {
	set := make(map[string]bool)
	for _, m := range longFlagRe.FindAllStringSubmatch(text, -1) {
		set[m[1]] = true
	}
	return set
}

// TestCLIReferenceMatchesBinary keeps docs/cli.md honest (FR-013): its global-flags
// section must list exactly the binary's `--help` flags — neither missing nor
// inventing one — and it must document every exit code.
func TestCLIReferenceMatchesBinary(t *testing.T) {
	help := runRune(t, "", "--help")
	if help.code != 0 {
		t.Fatalf("`rune --help` exit = %d, want 0\nstderr:\n%s", help.code, help.stderr)
	}
	binFlags := longFlags(help.stdout)

	cliBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "cli.md"))
	if err != nil {
		t.Fatalf("read docs/cli.md: %v", err)
	}
	cli := string(cliBytes)

	section := globalFlagsSection(cli)
	if section == "" {
		t.Fatal("docs/cli.md: '## Global flags' section not found")
	}
	docFlags := longFlags(section)

	for f := range binFlags {
		if !docFlags[f] {
			t.Errorf("flag --%s is in `rune --help` but undocumented in docs/cli.md global flags", f)
		}
	}
	for f := range docFlags {
		if !binFlags[f] {
			t.Errorf("flag --%s documented in docs/cli.md but absent from `rune --help`", f)
		}
	}

	for _, code := range []string{"`0`", "`1`", "`2`", "`3`", "`130`"} {
		if !strings.Contains(cli, code) {
			t.Errorf("docs/cli.md is missing exit code %s", code)
		}
	}
}

// globalFlagsSection returns the text of the "## Global flags" section (up to the
// next "## " heading), which holds the flag table the binary is compared against.
func globalFlagsSection(cli string) string {
	const heading = "## Global flags"
	i := strings.Index(cli, heading)
	if i < 0 {
		return ""
	}
	rest := cli[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestCLIReferenceImportFlags keeps the row of `rune import` in docs/cli.md
// equal to the command: every flag of `rune import --help` is named there, and
// the row names no flag the command does not have (spec 054, SC-008).
func TestCLIReferenceImportFlags(t *testing.T) {
	checkCommandRow(t, "rune import", "import", "--help")
}

// TestCLIReferenceAgentInitFlags does the same for `rune agent init`
// (spec 055, FR-036).
func TestCLIReferenceAgentInitFlags(t *testing.T) {
	checkCommandRow(t, "rune agent init", "agent", "init", "--help")
}

// checkCommandRow compares the flags a command's help lists with the flags
// its row of docs/cli.md names. command is the command as the row starts,
// and helpArgs asks the binary for its help.
func checkCommandRow(t *testing.T, command string, helpArgs ...string) {
	t.Helper()
	help := runRune(t, "", helpArgs...)
	if help.code != 0 {
		t.Fatalf("`%s --help` exit = %d, want 0\nstderr:\n%s", command, help.code, help.stderr)
	}
	cliBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "cli.md"))
	if err != nil {
		t.Fatalf("read docs/cli.md: %v", err)
	}
	row := ""
	for line := range strings.SplitSeq(string(cliBytes), "\n") {
		if strings.HasPrefix(line, "| `"+command+" ") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("docs/cli.md has no row for `%s`", command)
	}
	_, flags, found := strings.Cut(help.stdout, "Flags:")
	if !found {
		t.Fatalf("`%s --help` has no Flags section:\n%s", command, help.stdout)
	}
	flags, _, _ = strings.Cut(flags, "Global Flags:")
	binFlags, docFlags := longFlags(flags), longFlags(row)
	delete(binFlags, "help")
	for f := range binFlags {
		if !docFlags[f] {
			t.Errorf("flag --%s of `%s` is not in its row of docs/cli.md", f, command)
		}
	}
	for f := range docFlags {
		if !binFlags[f] {
			t.Errorf("the row of `%s` in docs/cli.md names --%s, which the command does not have", command, f)
		}
	}
}
