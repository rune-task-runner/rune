// Failure-hook capture for the MCP surface (spec 022 US1): hook stdout is
// collected in a dedicated masked buffer and delivered to agents as the
// fix-suggestion section of the tool result. Masking happens at the writer
// as bytes arrive, so it always precedes the size cap (FR-007).
package cli

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/rune-task-runner/rune/internal/mask"
)

// syncWriter serializes writes from hooks fired by concurrently failing
// [parallel] dependencies; the masking writer beneath it is stateful and the
// bytes.Buffer beneath that is not concurrency-safe.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// maskWriter wraps w in the emission-time masking writer, mirroring
// maskOptions: untouched (and a no-op flush) for an empty set so secret-free
// runs cost nothing.
func maskWriter(w io.Writer, set *mask.Set) (io.Writer, func()) {
	if set.Empty() {
		return w, func() {}
	}
	mw := mask.NewWriter(w, set)
	return mw, func() { _ = mw.Flush() }
}

// cappedBuffer stores at most max bytes and silently discards the rest, so a
// runaway hook cannot grow the long-lived MCP server's memory while the run
// captures output. Masking happens upstream at the mask writer, so discarded
// bytes were already masked. Keep max a little above the rendered cap so
// capAgentText can still detect the overflow and place the marker.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

// capAgentText trims trailing CR/LF and caps agent-facing text at
// contextMaxBytes of content, the cut backed up to a UTF-8 rune boundary,
// the [truncated] marker excluded from the cap. It is the single truncation
// discipline shared by the [context] instructions and the || fix suggestion
// — their contracts promise the two stay identical.
func capAgentText(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) > contextMaxBytes {
		cut := contextMaxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "\n[truncated]"
	}
	return s
}
