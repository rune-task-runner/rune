// Package integration runs the compiled rune binary against fixture Runefiles
// and asserts stdout, stderr, and exit code (Constitution Principle VI).
package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// runeBin is the path to the binary built once for the whole package.
var runeBin string

func TestMain(m *testing.M) {
	// A run of this binary as the stand-in agent of an agent task: it calls
	// a tool and ends, and builds nothing (audit_log_mcp_test.go).
	if os.Getenv(helperAgentVar) == "1" {
		os.Exit(helperAgent())
	}
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration: cannot find module root:", err)
		os.Exit(1)
	}
	dir, err := os.MkdirTemp("", "rune-it")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, "rune")
	if runtime.GOOS == "windows" {
		bin += ".exe" // go build writes -o verbatim; Windows needs the extension to exec.
	}
	// Build with -tags runetest so RUNE_TEST_VERSION can inject the installed
	// version for the minimum_version gate; production builds omit this tag and
	// never honor the env var.
	build := exec.Command("go", "build", "-tags", "runetest", "-o", bin, "./cmd/rune")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: build failed: %v\n%s\n", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	runeBin = bin
	// Every server a test starts writes an audit log (spec 052). It goes
	// under the build's temporary directory, never into a real home.
	if err := os.Setenv("RUNE_AUDIT_DIR", filepath.Join(dir, "audit")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	// The existing tests exercise the server unsandboxed, so that they test
	// what they tested before spec 053. The sandbox tests take the variable
	// out of their server's environment.
	if err := os.Setenv("RUNE_SANDBOX", "off"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// What a server started with the sandbox off adds to its startup line and
// prints before it (spec 053).
const (
	sandboxOffPart    = "sandbox: off, tasks run unsandboxed"
	sandboxOffWarning = "warning: sandbox: off. The tasks agents call run without a sandbox: they can use the network and write wherever this user can."
)

// offLine is a startup line as a server started unsandboxed prints it: the
// line of before spec 053 with the sandbox's part added last. parts is what
// stood in the parentheses, or "" for none.
func offLine(transport, parts string) string {
	if parts != "" {
		parts += "; "
	}
	return "rune MCP server on " + transport + " (" + parts + sandboxOffPart + ")\n"
}

// offStderr is the standard error of such a server: its warning, then
// whatever else it printed.
func offStderr(rest string) string {
	return sandboxOffWarning + "\n" + rest
}

// moduleRoot walks up from the working directory to the directory holding go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

// result captures one binary invocation.
type result struct {
	stdout string
	stderr string
	code   int
}

// run executes the rune binary in dir with args and optional extra env.
func run(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(runeBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %v failed to start: %v", args, err)
		}
	}
	return result{stdout: out.String(), stderr: errb.String(), code: code}
}

// runWithStdin runs the binary with the given stdin, bounded by a timeout (used
// for the blocking stdio MCP server, which exits on EOF).
func runWithStdin(t *testing.T, dir, stdin string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runeBin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			code = ee.ExitCode()
		}
	}
	return result{stdout: out.String(), stderr: errb.String(), code: code}
}

func asExit(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// writeRunefile creates dir/Runefile with the given content and returns dir.
func writeRunefile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
