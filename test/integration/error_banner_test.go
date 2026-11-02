package integration

import (
	"strings"
	"testing"
)

// 014 US1 (C1): the top-level failure banner — the most prominent message Rune
// prints — uses the shared error styling on a color stream, stays byte-frozen
// plain otherwise, and never changes exit codes.

const bannerRunefile = "boom:\n    @exit 7\n"

// TestBannerStyledOnFailure: under --color=always the `rune:` prefix carries
// ANSI, the message text is escape-free, and stripping recovers the plain
// banner byte-for-byte with the same exit code.
func TestBannerStyledOnFailure(t *testing.T) {
	dir := writeRunefile(t, bannerRunefile)
	styled := run(t, dir, nil, "--color=always", "boom")
	plain := run(t, dir, nil, "--color=never", "boom")
	if styled.code != 7 || plain.code != 7 {
		t.Fatalf("exit codes: styled=%d plain=%d, want 7 (stderr=%q)", styled.code, plain.code, styled.stderr)
	}
	assertStyledStderrMatchesPlain(t, styled, plain)
	if !strings.HasPrefix(plain.stderr, `rune: task "boom" failed`) {
		t.Fatalf("plain banner changed shape: %q", plain.stderr)
	}
	// Only the `rune:` prefix is emphasized: the styled line must still contain
	// the message text as contiguous plain bytes after the prefix's reset.
	if !strings.Contains(styled.stderr, `task "boom" failed`) {
		t.Errorf("message text should be plain inside the styled banner: %q", styled.stderr)
	}
	prefix := styled.stderr[:strings.Index(styled.stderr, "task")]
	if !hasANSI(prefix) {
		t.Errorf("`rune:` prefix should carry ANSI under --color=always: %q", styled.stderr)
	}
}

// TestBannerPlainInvariant: piped / NO_COLOR / --color=never all produce the
// identical pre-014 plain banner with the body's exit 7 (FR-006, C1).
func TestBannerPlainInvariant(t *testing.T) {
	dir := writeRunefile(t, bannerRunefile)
	r := assertPlainInvariant(t, dir, "boom")
	if r.code != 7 {
		t.Fatalf("exit = %d, want 7", r.code)
	}
	if !strings.HasPrefix(r.stderr, `rune: task "boom" failed`) {
		t.Errorf("plain banner bytes changed: %q", r.stderr)
	}
}

// TestBannerNoExitMessageStaysSilent: [no-exit-message] suppresses the banner
// in colored mode too — styling must not resurrect a suppressed banner (C1).
func TestBannerNoExitMessageStaysSilent(t *testing.T) {
	dir := writeRunefile(t, "[no-exit-message]\nboom:\n    @exit 7\n")
	r := run(t, dir, nil, "--color=always", "boom")
	if r.code != 7 {
		t.Fatalf("exit = %d, want 7", r.code)
	}
	if r.stderr != "" {
		t.Errorf("suppressed banner reappeared: %q", r.stderr)
	}
}

// TestBannerIgnoresTaskColorFlag: a --color passed after the task name belongs
// to the task (SetInterspersed(false)), not to Rune, so it must not theme the
// failure banner. Regression for the code-review finding on rawColorMode
// over-reaching past the first positional.
func TestBannerIgnoresTaskColorFlag(t *testing.T) {
	dir := writeRunefile(t, bannerRunefile)

	// Trailing (task-region) --color=always must NOT force ANSI onto the banner:
	// piped stderr under the effective auto default stays plain.
	taskFlag := run(t, dir, nil, "boom", "--color=always")
	if hasANSI(taskFlag.stderr) {
		t.Errorf("a task-region --color=always must not color Rune's banner: %q", taskFlag.stderr)
	}

	// A global (pre-task) --color=always still colors the banner even piped.
	global := run(t, dir, nil, "--color=always", "boom")
	if !hasANSI(global.stderr) {
		t.Errorf("a global --color=always should color the banner: %q", global.stderr)
	}
}

// TestBannerColorSurvivesFlagParseError: when flag parsing aborts before
// --color is applied (an unknown flag earlier on the line), the banner must
// still honor an explicit --color from the raw args — otherwise `--color=never`
// is silently ignored (and `--color=always` silently dropped). Regression for
// the code-review finding on bannerTheme's tolerant fallback (C8/FR-008).
func TestBannerColorSurvivesFlagParseError(t *testing.T) {
	dir := writeRunefile(t, bannerRunefile)

	always := run(t, dir, nil, "--bogusflag", "--color=always")
	if always.code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%q", always.code, always.stderr)
	}
	if !hasANSI(always.stderr) {
		t.Errorf("explicit --color=always must style the banner even after a flag-parse error: %q", always.stderr)
	}

	never := run(t, dir, nil, "--bogusflag", "--color=never")
	if hasANSI(never.stderr) {
		t.Errorf("explicit --color=never must keep the banner plain even after a flag-parse error: %q", never.stderr)
	}
}

// TestBannerInvalidColorFallsBack: an invalid --color value is still a plain
// usage error (exit 2) — the banner path tolerates the bad flag by falling
// back to auto instead of erroring twice or emitting ANSI into a pipe (C8).
func TestBannerInvalidColorFallsBack(t *testing.T) {
	dir := writeRunefile(t, bannerRunefile)
	r := run(t, dir, nil, "--color=sometimes", "boom")
	if r.code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%q", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "invalid --color") {
		t.Errorf("missing usage error: %q", r.stderr)
	}
	if hasANSI(r.stderr) {
		t.Errorf("piped banner must not carry ANSI on the fallback path: %q", r.stderr)
	}
}
