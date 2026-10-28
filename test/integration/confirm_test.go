package integration

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Spec 027: a [confirm] answer counts only when a person can give it, so any
// non-terminal stdin declines, whatever it holds.
const confirmRunefile = `
[confirm("Apply to live infrastructure?")]
apply:
    @echo would-apply

deploy: apply
    @echo deployed

build:
    @echo built

[confirm]
publish:
    @echo published

release: build publish
    @echo released

fails: || diag
    @exit 1

[confirm]
diag:
    @echo diagnosed

ship: && notify
    @echo shipped

[confirm]
notify:
    @echo notified

[confirm]
echo-stdin:
    @cat
`

// runWithInput runs the binary with stdin read from r; unlike runWithStdin it
// accepts any reader, including real files and unbounded streams.
func runWithInput(t *testing.T, dir string, r io.Reader, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runeBin, args...)
	cmd.Dir = dir
	cmd.Stdin = r
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !asExit(err, &ee) {
			t.Fatalf("run %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return result{stdout: out.String(), stderr: errb.String(), code: code}
}

// yesStream is what `yes | rune apply` feeds: "y\n" forever.
type yesStream struct{}

func (yesStream) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = "y\n"[i%2]
	}
	return len(p), nil
}

func answerFile(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answer.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestConfirmNonTerminalInputNeverApproves(t *testing.T) {
	dir := writeRunefile(t, confirmRunefile)
	tests := []struct {
		name  string
		stdin func(t *testing.T) io.Reader
	}{
		{"piped y", func(*testing.T) io.Reader { return strings.NewReader("y\n") }},
		{"piped YES", func(*testing.T) io.Reader { return strings.NewReader("YES\n") }},
		{"unbounded y stream", func(*testing.T) io.Reader { return yesStream{} }},
		{"file containing yes", func(t *testing.T) io.Reader { return answerFile(t, "yes\n") }},
		{"empty stdin", func(*testing.T) io.Reader { return strings.NewReader("") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runWithInput(t, dir, tt.stdin(t), "apply")
			if r.code != 1 {
				t.Errorf("exit = %d, want 1\nstderr: %s", r.code, r.stderr)
			}
			if strings.Contains(r.stdout, "would-apply") {
				t.Errorf("body ran on non-terminal input: %q", r.stdout)
			}
		})
	}
}

// FR-007 and Clarification Q1: the gate applies wherever a [confirm] task is
// reached, and work scheduled before it still runs.
func TestConfirmNonTerminalAppliesOnEveryPath(t *testing.T) {
	dir := writeRunefile(t, confirmRunefile)
	tests := []struct {
		name    string
		task    string
		want    []string
		notWant []string
	}{
		{"dependency", "deploy", nil, []string{"would-apply", "deployed"}},
		{"tasks ahead of the gate run", "release", []string{"built"}, []string{"published", "released"}},
		{"failure hook", "fails", nil, []string{"diagnosed"}},
		{"success hook", "ship", []string{"shipped"}, []string{"notified"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runWithInput(t, dir, strings.NewReader("y\n"), tt.task)
			if r.code != 1 {
				t.Errorf("exit = %d, want 1\nstderr: %s", r.code, r.stderr)
			}
			for _, s := range tt.want {
				if !strings.Contains(r.stdout, s) {
					t.Errorf("stdout missing %q: %q", s, r.stdout)
				}
			}
			for _, s := range tt.notWant {
				if strings.Contains(r.stdout, s) {
					t.Errorf("stdout has %q from a gated body: %q", s, r.stdout)
				}
			}
		})
	}
}

// FR-010: plan modes neither prompt nor decline.
func TestConfirmPlanModesUnchanged(t *testing.T) {
	dir := writeRunefile(t, confirmRunefile)
	for _, mode := range []string{"--dry-run", "--summary"} {
		t.Run(mode, func(t *testing.T) {
			r := runWithInput(t, dir, strings.NewReader("y\n"), mode, "deploy")
			if r.code != 0 {
				t.Errorf("exit = %d, want 0\nstderr: %s", r.code, r.stderr)
			}
			if strings.Contains(r.stderr, "rune:") || strings.Contains(r.stderr, "[y/N]") {
				t.Errorf("plan mode prompted or declined: %q", r.stderr)
			}
		})
	}
}

// FR-006, SC-004: --yes approves and leaves all of stdin to the body.
func TestConfirmYesPassesStdinThrough(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	dir := writeRunefile(t, confirmRunefile)
	r := runWithInput(t, dir, strings.NewReader("payload"), "--yes", "echo-stdin")
	if r.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", r.code, r.stderr)
	}
	if r.stdout != "payload" {
		t.Errorf("stdout = %q, want %q", r.stdout, "payload")
	}
}
