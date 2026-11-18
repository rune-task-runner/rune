//go:build linux

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The same directory mounted at a second path is read-only there.
func TestSandboxRuleSecondMount(t *testing.T) {
	requireSandbox(t)
	if os.Getuid() != 0 {
		t.Skip("mounting needs root")
	}
	f := sandboxProject(t, hostRunefile)
	second := filepath.Join(f.root, "second")
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mount(f.dir, second, "", syscall.MS_BIND, ""); err != nil {
		t.Skipf("this host does not let the test mount: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Unmount(second, 0) })
	cs, _, _ := f.ruleServer(t, nil)
	res := callTask(t, cs, "write", map[string]any{"path": filepath.Join(second, "through-second-mount")})
	if !strings.HasPrefix(res.stdout, "error:") || hasMarker(f.dir, "through-second-mount") {
		t.Errorf("a write through the second mount: %q", res.stdout)
	}
}
