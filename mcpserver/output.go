package mcpserver

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Bounds of the output limit: the bytes of each of stdout and stderr that a
// tool result carries, truncation marker included.
const (
	DefaultOutputLimit = 64 << 10
	MinOutputLimit     = 1 << 10
	MaxOutputLimit     = 1 << 30
)

// markerReserve is the longest truncation marker: "\n[truncated ", the 20
// digits of the largest count, and " bytes]\n". Reserving it whole keeps the
// sizes of the kept beginning and end independent of the count, so the same
// output is cut the same way however it was written.
const markerReserve = 40

// clampLimit turns any int into a usable limit: zero or less means the
// default, and other values are brought into the allowed range.
func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultOutputLimit
	case limit < MinOutputLimit:
		return MinOutputLimit
	case limit > MaxOutputLimit:
		return MaxOutputLimit
	}
	return limit
}

// OutputBuffer is an io.Writer that keeps the beginning and the end of what
// is written to it and never holds more than its limit in bytes. It is safe
// for concurrent use. The zero value has DefaultOutputLimit.
type OutputBuffer struct {
	mu               sync.Mutex
	limit            int
	headMax, tailMax int
	// buf holds everything written while that fits the limit. After the first
	// overflow, buf[:headMax] is the beginning and buf[headMax:] is a ring of
	// the last tailMax bytes, so the capture stays within limit.
	buf   []byte
	total int64
	// start is the offset in the ring of its oldest byte.
	start int
}

// NewOutputBuffer returns a buffer with the given limit, adjusted as
// Options.OutputLimit is.
func NewOutputBuffer(limit int) *OutputBuffer {
	b := &OutputBuffer{}
	b.setLimit(limit)
	return b
}

func (b *OutputBuffer) setLimit(limit int) {
	b.limit = clampLimit(limit)
	b.headMax = (b.limit - markerReserve) / 2
	b.tailMax = b.limit - markerReserve - b.headMax
}

// ready gives a zero OutputBuffer its limit. Without it a zero value would
// keep nothing and return only a marker.
func (b *OutputBuffer) ready() {
	if b.limit == 0 {
		b.setLimit(0)
	}
}

// Write keeps what fits and drops the rest. It always returns len(p), nil.
func (b *OutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ready()
	n := len(p)
	if !b.truncated() {
		room := b.limit - len(b.buf)
		if n <= room {
			b.grow(n)
			b.buf = append(b.buf, p...)
			b.total += int64(n)
			return n, nil
		}
		b.grow(room)
		b.buf = append(b.buf, p[:room]...)
		b.total += int64(room)
		p = p[room:]
		// Move the end of the full buffer next to the beginning. The region
		// after headMax is the ring from here on.
		copy(b.buf[b.headMax:], b.buf[b.limit-b.tailMax:])
		b.buf = b.buf[:b.headMax+b.tailMax]
		b.start = 0
	}
	b.total += int64(len(p))
	ring := b.buf[b.headMax:]
	if len(p) >= len(ring) {
		copy(ring, p[len(p)-len(ring):])
		b.start = 0
		return n, nil
	}
	k := copy(ring[b.start:], p)
	copy(ring, p[k:])
	b.start = (b.start + len(p)) % len(ring)
	return n, nil
}

// writeString is Write for a string, copied through a small array so a large
// string is never duplicated.
func (b *OutputBuffer) writeString(s string) {
	var chunk [4096]byte
	for len(s) > 0 {
		n := copy(chunk[:], s)
		_, _ = b.Write(chunk[:n])
		s = s[n:]
	}
}

func (b *OutputBuffer) truncated() bool { return b.total > int64(b.limit) }

// grow makes room for n more bytes without letting the capacity pass limit,
// which append alone would.
func (b *OutputBuffer) grow(n int) {
	need := len(b.buf) + n
	if need <= cap(b.buf) {
		return
	}
	grown := make([]byte, len(b.buf), min(max(2*cap(b.buf), need, 512), b.limit))
	copy(grown, b.buf)
	b.buf = grown
}

// String returns everything written when it is within the limit. Otherwise
// it returns the beginning, a "[truncated N bytes]" line, and the end, at
// most limit bytes in all.
func (b *OutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ready()
	if !b.truncated() {
		return string(b.buf)
	}
	head := trimPartialRune(b.buf[:b.headMax])
	ring := b.buf[b.headMax:]
	// A cut inside a character leaves its continuation bytes at the front.
	skip := 0
	for skip < utf8.UTFMax-1 && skip < len(ring) && !utf8.RuneStart(ring[(b.start+skip)%len(ring)]) {
		skip++
	}
	tailLen := len(ring) - skip
	dropped := b.total - int64(len(head)) - int64(tailLen)

	var out strings.Builder
	out.Grow(b.limit)
	out.Write(head)
	out.WriteString("\n[truncated ")
	out.WriteString(strconv.FormatInt(dropped, 10))
	out.WriteString(" bytes]\n")
	from := b.start + skip
	if from >= len(ring) {
		out.Write(ring[from-len(ring) : b.start])
	} else {
		out.Write(ring[from:])
		out.Write(ring[:b.start])
	}
	return out.String()
}

// trimPartialRune drops a UTF-8 sequence that starts at the end of p and is
// cut short there. It needs only p, because the bytes after the cut are gone.
func trimPartialRune(p []byte) []byte {
	for i := len(p) - 1; i >= 0 && i >= len(p)-(utf8.UTFMax-1); i-- {
		if utf8.RuneStart(p[i]) {
			if !utf8.FullRune(p[i:]) {
				return p[:i]
			}
			break
		}
	}
	return p
}

// capOutput returns s when it is within limit, and otherwise what an
// OutputBuffer with that limit would return for it.
func capOutput(s string, limit int) string {
	limit = clampLimit(limit)
	if len(s) <= limit {
		return s
	}
	b := NewOutputBuffer(limit)
	// Past the first limit bytes only the last tailMax can be kept, so the
	// bytes between are counted and not copied: the work is bounded by limit
	// however long s is.
	b.writeString(s[:limit])
	rest := s[limit:]
	skipped := max(len(rest)-b.tailMax, 0)
	b.writeString(rest[skipped:])
	b.total += int64(skipped)
	return b.String()
}
