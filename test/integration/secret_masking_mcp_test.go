package integration

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The secret reaches the task via [env(...)] so the test is self-contained;
// its name matches the built-in TOKEN pattern (contract §2.1).
const mcpMaskRunefile = `[env("DEMO_API_TOKEN", "hunter2-mcp-secret")]
leak:
    @echo "token is $DEMO_API_TOKEN"
    @echo "again $DEMO_API_TOKEN" >&2
`

// mcpCall drives one interactive stdio MCP session: initialize handshake,
// then a tools/call, reading line-delimited responses until the reply with
// id 2 arrives (the stdio session drops pending replies on stdin EOF, so the
// pipe must stay open until then).
func mcpCall(t *testing.T, dir, task string) (toolResult string, stderr string) {
	t.Helper()
	return mcpCallArgs(t, dir, task, "{}")
}

// mcpCallArgs is mcpCall with a JSON object of tool arguments.
func mcpCallArgs(t *testing.T, dir, task, arguments string) (toolResult string, stderr string) {
	t.Helper()
	cmd := exec.Command(runeBin, "mcp")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	watchdog := time.AfterFunc(15*time.Second, func() { _ = cmd.Process.Kill() })
	defer watchdog.Stop()

	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"it","version":"0.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, task, arguments),
	}
	if _, err := io.WriteString(stdin, strings.Join(frames, "\n")+"\n"); err != nil {
		t.Fatalf("writing MCP frames: %v", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	var result string
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, `"id":2`) || strings.Contains(line, `"id": 2`) {
			result = line
			break
		}
	}
	// Join the process before touching errb: exec's copier goroutine writes it
	// until Wait returns.
	_ = stdin.Close()
	_ = cmd.Wait()
	if result == "" {
		t.Fatalf("no tools/call response before stream end; stderr=%q", errb.String())
	}
	return result, errb.String()
}

// TestSecretMasking_MCPToolResultMasked drives the stdio MCP server end to
// end: the tool result an agent receives must contain the mask and never the
// raw credential, in both the stdout and stderr sections (contract §3, SC-002).
func TestSecretMasking_MCPToolResultMasked(t *testing.T) {
	dir := writeRunefile(t, mcpMaskRunefile)
	result, stderr := mcpCall(t, dir, "leak")
	if strings.Contains(result, "hunter2-mcp-secret") || strings.Contains(stderr, "hunter2-mcp-secret") {
		t.Fatalf("raw secret leaked through the MCP transport:\nresult=%q\nstderr=%q", result, stderr)
	}
	if !strings.Contains(result, "token is ***") {
		t.Errorf("tool result should mask the stdout section: %q", result)
	}
	if !strings.Contains(result, "again ***") {
		t.Errorf("tool result should mask the stderr section: %q", result)
	}
}
