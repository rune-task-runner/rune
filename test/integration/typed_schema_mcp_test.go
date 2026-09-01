package integration

import (
	"bufio"
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Typed parameter schemas over MCP (spec 023, US1/SC-001): the tool schema an
// agent receives must carry the enum value list, the number type, the path
// format, and the [param-doc] description machine-readably.
const typedSchemaRunefile = `# Deploy the service.
[returns("URL of the deployed environment on stdout")]
[param-doc("env", "Target environment")]
deploy env:enum("staging","prod") replicas:number="2":
    @echo "deploying {{env}} x{{replicas}}"

test +packages:path:
    @echo "testing {{packages}}"
`

// mcpToolsList drives one stdio MCP session through initialize + tools/list
// and returns the raw JSON reply carrying id 2.
func mcpToolsList(t *testing.T, dir string) string {
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
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}
	if _, err := io.WriteString(stdin, strings.Join(frames, "\n")+"\n"); err != nil {
		t.Fatalf("writing MCP frames: %v", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	var reply string
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, `"id":2`) || strings.Contains(line, `"id": 2`) {
			reply = line
			break
		}
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	if reply == "" {
		t.Fatalf("no tools/list reply (stderr: %s)", errb.String())
	}
	return reply
}

func TestTypedSchema_ToolsList(t *testing.T) {
	dir := writeRunefile(t, typedSchemaRunefile)
	reply := mcpToolsList(t, dir)

	for _, want := range []string{
		`"enum":["staging","prod"]`,
		`"type":"number"`,
		`"format":"path"`,
		`"description":"Target environment"`,
		`"type":"array"`,
		`Returns: URL of the deployed environment on stdout`,
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("tools/list reply missing %s\nreply: %s", want, reply)
		}
	}
	// required semantics unchanged: env is required, replicas (defaulted) is not.
	if !strings.Contains(reply, `"required":["env"]`) {
		t.Errorf("deploy required list wrong\nreply: %s", reply)
	}
}

// FR-011: schema text originates only from static Runefile string literals —
// no environment-sourced value can appear in a tool definition, even when the
// task itself handles a secret.
func TestTypedSchema_NoSecretsInSchema(t *testing.T) {
	const src = `[env("DEMO_API_TOKEN", "hunter2-mcp-secret")]
[param-doc("env", "Target environment")]
deploy env:enum("staging","prod"):
    @echo "token is $DEMO_API_TOKEN deploying {{env}}"
`
	dir := writeRunefile(t, src)
	reply := mcpToolsList(t, dir)
	if strings.Contains(reply, "hunter2") {
		t.Errorf("schema leaked an environment value:\n%s", reply)
	}
	if !strings.Contains(reply, `"enum":["staging","prod"]`) {
		t.Errorf("typed schema missing from reply:\n%s", reply)
	}
}

// Spec 023 FR-010: the outcome description serves humans too — it appears in
// the --list output as an aligned continuation line, and only when declared.
func TestReturns_ListShowsOutcome(t *testing.T) {
	dir := writeRunefile(t, typedSchemaRunefile)
	res := run(t, dir, nil, "--list")
	if res.code != 0 {
		t.Fatalf("--list failed: %s", res.stderr)
	}
	if !strings.Contains(res.stdout, "# returns: URL of the deployed environment on stdout") {
		t.Errorf("--list missing returns line:\n%s", res.stdout)
	}
	if strings.Count(res.stdout, "returns:") != 1 {
		t.Errorf("returns line should appear once (only deploy declares it):\n%s", res.stdout)
	}
}
