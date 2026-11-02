package docs

import (
	"path/filepath"
	"strings"
	"testing"
)

// The ci-cd example's release-gate task documents how CI tells "refused"
// (exit 2) from "failed" by the exit code alone (spec 028 SC-004).
func TestCICDReleaseGateExitCodes(t *testing.T) {
	dir := filepath.Join(repoRoot, "docs", "examples", "ci-cd")

	closed := runRune(t, dir, "release-gate", "closed")
	if closed.code != 2 {
		t.Errorf("release-gate closed: exit = %d, want 2\nstderr:\n%s", closed.code, closed.stderr)
	}
	if !strings.Contains(closed.stderr, "refused") {
		t.Errorf("release-gate closed: stderr does not say refused:\n%s", closed.stderr)
	}

	if open := runRune(t, dir, "release-gate"); open.code != 0 {
		t.Errorf("release-gate: exit = %d, want 0\nstderr:\n%s", open.code, open.stderr)
	}
}
