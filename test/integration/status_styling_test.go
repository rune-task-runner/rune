package integration

import (
	"strings"
	"testing"
)

// 014 US2 (C3): the remaining status/informational messages — formatted:,
// cleared:, the confirm prompt (now in confirm_pty_test.go: it needs a
// terminal since spec 027), the bare-`rune` overview, and the MCP server
// banner — join the shared theme, with plain bytes frozen.

// styledVsPlain runs the same invocation twice in dir (--color=always, then
// --color=never), asserting ANSI presence, plain purity, strip-equality on the
// requested stream, and equal exit codes. Returns (styled, plain).
func styledVsPlain(t *testing.T, dir string, env []string, args ...string) (result, result) {
	t.Helper()
	styled := run(t, dir, env, append([]string{"--color=always"}, args...)...)
	plain := run(t, dir, env, append([]string{"--color=never"}, args...)...)
	if styled.code != plain.code {
		t.Fatalf("exit diverged: styled=%d plain=%d (args=%v)", styled.code, plain.code, args)
	}
	if hasANSI(plain.stdout) || hasANSI(plain.stderr) {
		t.Errorf("plain run carried ANSI: stdout=%q stderr=%q", plain.stdout, plain.stderr)
	}
	if got := stripANSI(styled.stdout); got != plain.stdout {
		t.Errorf("styled stdout (stripped) != plain:\n stripped=%q\n plain=%q", got, plain.stdout)
	}
	if got := stripANSI(styled.stderr); got != plain.stderr {
		t.Errorf("styled stderr (stripped) != plain:\n stripped=%q\n plain=%q", got, plain.stderr)
	}
	return styled, plain
}

func TestFmtNoticeStyled(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	// Same dir for both runs so the printed path is identical (fmt is idempotent).
	styled, plain := styledVsPlain(t, dir, nil, "--fmt")
	if !strings.Contains(plain.stderr, "formatted: ") {
		t.Fatalf("missing formatted notice: %q", plain.stderr)
	}
	if !hasANSI(styled.stderr) {
		t.Errorf("formatted: label should carry ANSI: %q", styled.stderr)
	}
}

func TestClearCacheNoticeStyled(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	styled, plain := styledVsPlain(t, dir, nil, "--clear-cache")
	if !strings.Contains(plain.stderr, "cleared: ") {
		t.Fatalf("missing cleared notice: %q", plain.stderr)
	}
	if !hasANSI(styled.stderr) {
		t.Errorf("cleared: label should carry ANSI: %q", styled.stderr)
	}
}

func TestOverviewStyled(t *testing.T) {
	dir := writeRunefile(t, "# Build the thing.\nbuild:\n    @echo hi\n")
	styled, plain := styledVsPlain(t, dir, nil)
	if !strings.HasPrefix(plain.stdout, "rune version: ") || !strings.Contains(plain.stdout, "Available tasks:") {
		t.Fatalf("overview shape changed: %q", plain.stdout)
	}
	// The header line itself is themed now — ANSI before the task list begins.
	header := styled.stdout[:strings.Index(styled.stdout, "\n")]
	if !hasANSI(header) {
		t.Errorf("overview header should carry ANSI: %q", header)
	}
}

func TestOverviewNoTasksStyled(t *testing.T) {
	dir := writeRunefile(t, "_hidden:\n    @echo h\n")
	styled, plain := styledVsPlain(t, dir, nil)
	if !strings.Contains(plain.stdout, "No available tasks found in this Runefile.") {
		t.Fatalf("no-tasks fallback shape changed: %q", plain.stdout)
	}
	if !hasANSI(styled.stdout) {
		t.Errorf("no-tasks overview should carry ANSI (header/docs URL): %q", styled.stdout)
	}
}

func TestServeBannerStyled(t *testing.T) {
	dir := writeRunefile(t, "build:\n    @echo hi\n")
	styled := runWithStdin(t, dir, "", "serve", "--color=always")
	plain := runWithStdin(t, dir, "", "serve", "--color=never")
	if !strings.Contains(plain.stderr, "rune MCP server on stdio") {
		t.Fatalf("serve banner shape changed: %q", plain.stderr)
	}
	if hasANSI(plain.stderr) {
		t.Errorf("plain serve banner carried ANSI: %q", plain.stderr)
	}
	if !hasANSI(styled.stderr) {
		t.Errorf("styled serve banner should carry ANSI: %q", styled.stderr)
	}
	if got := stripANSI(styled.stderr); got != plain.stderr {
		t.Errorf("styled serve banner (stripped) != plain:\n stripped=%q\n plain=%q", got, plain.stderr)
	}
}
