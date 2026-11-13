package integration

import (
	"strings"
	"testing"
)

// US1: built-in commands are discoverable and documented; `version` matches
// `--version`.

func TestUS1Help_ListsBuiltinCommandsAndPointsToTasks(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	r := run(t, dir, nil, "--help")
	if r.code != 0 {
		t.Fatalf("`rune --help` exit = %d, want 0; stderr=%s", r.code, r.stderr)
	}
	for _, name := range []string{"serve", "version", "completion", "help"} {
		if !strings.Contains(r.stdout, name) {
			t.Errorf("`rune --help` should list built-in command %q; got:\n%s", name, r.stdout)
		}
	}
	// Distinguish tasks from commands by pointing users at task discovery.
	if !strings.Contains(r.stdout, "rune --list") {
		t.Errorf("`rune --help` should tell users to run `rune --list` for tasks; got:\n%s", r.stdout)
	}
}

func TestUS1Help_ServeHasFlagsExampleAndAlias(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	r := run(t, dir, nil, "serve", "--help")
	if r.code != 0 {
		t.Fatalf("`rune serve --help` exit = %d, want 0; stderr=%s", r.code, r.stderr)
	}
	for _, want := range []string{"--http", "--addr", "--token-file", "--allow stringArray", "--deny stringArray", "Examples:", "mcp"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("`rune serve --help` missing %q; got:\n%s", want, r.stdout)
		}
	}
}

func TestUS1Version_ParityWithFlag(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	cmd := run(t, dir, nil, "version")
	flag := run(t, dir, nil, "--version")
	if cmd.code != 0 || flag.code != 0 {
		t.Fatalf("version exit=%d, --version exit=%d; want 0 each", cmd.code, flag.code)
	}
	// `rune version` now appends a "runefile language N" line, so its FIRST line
	// (the version string) must still equal `rune --version`.
	firstLine := strings.SplitN(cmd.stdout, "\n", 2)[0] + "\n"
	if firstLine != flag.stdout {
		t.Errorf("`rune version` first line (%q) != `rune --version` (%q)", firstLine, flag.stdout)
	}
	if !strings.Contains(cmd.stdout, "runefile language") {
		t.Errorf("`rune version` missing language line: %q", cmd.stdout)
	}
}

// 014 US3 (C5): every subcommand's --help uses the grouped layout (description,
// Usage, Examples where defined, Flags) with themed section headings; piped
// help stays ANSI-free and stripping recovers the plain bytes.
func TestSubcommandHelpGroupedAndStyled(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	descriptions := map[string]string{
		"serve":      "Model Context Protocol",
		"version":    "Runefile language version",
		"analyze":    "Nothing is executed",
		"lsp":        "language server",
		"completion": "completion script",
	}
	for _, name := range []string{"serve", "version", "analyze", "lsp", "completion"} {
		t.Run(name, func(t *testing.T) {
			// styledVsPlain covers exit parity, plain purity, and strip-equality
			// across both streams; the checks below add the help-specific shape.
			styled, plain := styledVsPlain(t, dir, nil, name, "--help")
			if plain.code != 0 {
				t.Fatalf("`rune %s --help` exit = %d; stderr=%s", name, plain.code, plain.stderr)
			}
			for _, sec := range []string{"Usage:", "Flags:"} {
				if !strings.Contains(plain.stdout, sec) {
					t.Errorf("missing section %q:\n%s", sec, plain.stdout)
				}
			}
			if !strings.Contains(plain.stdout, descriptions[name]) {
				t.Errorf("missing long description (%q):\n%s", descriptions[name], plain.stdout)
			}
			if !hasANSI(styled.stdout) {
				t.Errorf("--color=always help emitted no ANSI: %q", styled.stdout)
			}
		})
	}
}

// `rune help <cmd>` renders through the same grouped help path as `--help`.
func TestHelpCommandMatchesHelpFlag(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	viaCmd := run(t, dir, nil, "help", "serve")
	viaFlag := run(t, dir, nil, "serve", "--help")
	if viaCmd.code != 0 || viaFlag.code != 0 {
		t.Fatalf("exit codes: help=%d --help=%d", viaCmd.code, viaFlag.code)
	}
	if viaCmd.stdout != viaFlag.stdout {
		t.Errorf("`rune help serve` != `rune serve --help`:\n cmd=%q\n flag=%q", viaCmd.stdout, viaFlag.stdout)
	}
}

// TestUS6_HelpRedesign: the root help has grouped sections and a worked example
// for each common workflow, renders ANSI-free when piped, and colorizes section
// headings under --color=always (FR-019, FR-020, FR-021, SC-006).
func TestUS6_HelpRedesign(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	r := run(t, dir, nil, "--help")
	if r.code != 0 {
		t.Fatalf("--help exit = %d; stderr=%s", r.code, r.stderr)
	}
	for _, sec := range []string{"Usage:", "Commands:", "Examples:", "Flags:"} {
		if !strings.Contains(r.stdout, sec) {
			t.Errorf("--help missing section %q; got:\n%s", sec, r.stdout)
		}
	}
	for _, ex := range []string{"rune --list", "rune build", "rune --choose", "rune serve"} {
		if !strings.Contains(r.stdout, ex) {
			t.Errorf("--help missing example %q; got:\n%s", ex, r.stdout)
		}
	}
	if !strings.Contains(r.stdout, "--color") {
		t.Errorf("--help should document --color; got:\n%s", r.stdout)
	}
	if hasANSI(r.stdout) {
		t.Errorf("piped --help contained ANSI: %q", r.stdout)
	}
	styled := run(t, dir, nil, "--color=always", "--help")
	if !hasANSI(styled.stdout) {
		t.Errorf("--color=always --help emitted no ANSI: %q", styled.stdout)
	}
	if stripANSI(styled.stdout) != r.stdout {
		t.Errorf("styled --help (stripped) != plain:\n stripped=%q\n plain=%q", stripANSI(styled.stdout), r.stdout)
	}
}
