package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
)

// Context-hook processing limits (spec 021 NFR-002): fixed, with no
// configuration surface. Tests shrink the timeout per adapter via the
// mcpAdapter.contextTimeout field.
const defaultContextTimeout = 10 * time.Second

// contextMaxBytes caps the injected content; the "[truncated]" marker is
// appended after the cut and is excluded from the cap. The cut backs up to
// a rune boundary, so it may keep up to 3 bytes fewer than the cap.
const contextMaxBytes = 8 * 1024

// contextTask returns the file's [context] task when one exists and is
// available on goos; an OS-mismatched hook is treated as absent (FR-008).
func contextTask(f *ast.File, goos string) *ast.Task {
	if f == nil {
		return nil
	}
	for _, t := range f.Tasks {
		if t.Attr(ast.AttrContext) != nil && t.AvailableOn(goos) {
			return t
		}
	}
	return nil
}

// gatherContext runs the [context] hook through the adapter's masked Call
// path and returns the processed text; empty means no context (no hook, an
// OS-skipped hook, or a hook that printed nothing — FR-008/FR-009). The hook
// is best-effort: failures and timeouts degrade to a one-line notice and a
// stderr warning (FR-005) — never an error, never a blocked surface.
func (a *mcpAdapter) gatherContext(ctx context.Context, stderr io.Writer) string {
	t := contextTask(a.file, a.goos)
	if t == nil {
		return ""
	}
	timeout := a.contextTimeout
	if timeout <= 0 {
		timeout = defaultContextTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := a.Call(cctx, t.Name, map[string]string{})
	if err != nil || res.ExitCode != ExitSuccess {
		// One message, two surfaces: the stderr warning and the in-context
		// notice must stay identical so users can grep for either.
		msg := fmt.Sprintf("context hook %q failed; proceeding without project context", t.Name)
		fmt.Fprintf(stderr, "warning: %s\n", msg)
		return "(" + msg + ")"
	}
	// Masking already happened inside Call, so the cap cannot expose a
	// secret that the mask would have caught; capAgentText is the shared
	// truncation discipline (also used for the || fix suggestion).
	return capAgentText(res.Stdout)
}
