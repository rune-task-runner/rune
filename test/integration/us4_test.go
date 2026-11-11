package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const us4Runefile = `set agent_cmd := ["definitely-not-a-real-agent-xyz", "-p"]

# Show recent git log.
logs:
    @echo log-line-1

[private]
secret_token:
    @echo SHOULD-NEVER-BE-EXPOSED

[confirm("Really clean?")]
clean:
    @echo cleaning

triage (agent):
    Summarize the last commits. You may call the logs task.
`

func TestUS4_MCPStdioStarts(t *testing.T) {
	// `rune mcp` blocks serving on stdio; with no client and closed stdin it
	// should exit cleanly (EOF) rather than crash. We assert it starts by
	// closing stdin immediately.
	dir := writeRunefile(t, us4Runefile)
	r := runWithStdin(t, dir, "", "mcp")
	// Exit code may be 0 (clean EOF) or a transport close; just ensure it did
	// not panic / report a usage error.
	if strings.Contains(r.stderr, "panic") {
		t.Fatalf("mcp server panicked: %s", r.stderr)
	}
}

func TestUS4_AgentMissingCLIExits1(t *testing.T) {
	dir := writeRunefile(t, us4Runefile)
	r := run(t, dir, nil, "triage")
	if r.code != 1 {
		t.Fatalf("exit = %d, want 1 (missing agent CLI); stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "not found on PATH") {
		t.Errorf("error should name the missing agent CLI: %q", r.stderr)
	}
}

func TestUS4_ServeHTTPRequiresToken(t *testing.T) {
	dir := writeRunefile(t, us4Runefile)
	// No --token-file -> usage error (exit 2), never starts unauthenticated.
	r := run(t, dir, nil, "serve", "--http", "--addr", "127.0.0.1:0")
	if r.code != 2 {
		t.Errorf("exit = %d, want 2 (missing token); stderr=%s", r.code, r.stderr)
	}
}

func TestUS4_MCPFileFlagLoadsRunefileOutsideWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	runefile := filepath.Join(root, "Runefile")
	if err := os.WriteFile(runefile, []byte("status:\n    @echo loaded > loaded.done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workdir := filepath.Join(root, "nested", "client")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	reply := mcpToolsList(t, workdir, "-f", runefile)
	if !strings.Contains(reply, `"name":"status"`) {
		t.Fatalf("mcp -f did not load the Runefile outside the working directory: %s", reply)
	}
	text, isError, stderr := toolCall(t, workdir, []string{"-f", runefile}, "status")
	if isError || !strings.Contains(text, "[exit 0]") {
		t.Fatalf("mcp -f tool call failed: isError=%v text=%q stderr=%q", isError, text, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "loaded.done")); err != nil {
		t.Fatalf("task output was not created beside the Runefile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "loaded.done")); err == nil {
		t.Fatal("task output was created in the client working directory")
	}
}
