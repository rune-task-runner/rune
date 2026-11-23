package mcpserver

import (
	"bytes"
	"strconv"
	"sync"
	"unicode/utf8"
)

// StreamReport is what a stream had and what of it a kept result holds
// (spec 057). KeptBytes leaves out the omission line.
type StreamReport struct {
	Lines     int64 `json:"lines"`
	Bytes     int64 `json:"bytes"`
	KeptLines int64 `json:"keptLines"`
	KeptBytes int64 `json:"keptBytes"`
}

// omissionReserve is the longest omission line with the line feed that can
// precede it: the counts are int64, so 19 digits each. Reserving it whole
// keeps the sizes of the kept parts independent of the counts.
const omissionReserve = 64

// omissionLine is the line that stands where a kept stream left lines out.
func omissionLine(lines, n int64) string {
	unit := func(n int64, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return strconv.FormatInt(n, 10) + " " + many
	}
	return "[omitted " + unit(lines, "line", "lines") + ", " + unit(n, "byte", "bytes") + "]\n"
}

// LineBuffer is an io.Writer that keeps the first head lines and the last
// tail lines of what is written to it, and never holds more than its limit in
// bytes, however long a line is (spec 057). It is safe for concurrent use.
//
// It holds the first hb bytes written and a ring of the last limit - hb bytes
// after them. A stream of up to limit bytes is therefore held whole, and a
// longer one keeps everything its result can contain: at most hb bytes of
// head and limit - omissionReserve - hb bytes of tail.
type LineBuffer struct {
	mu          sync.Mutex
	limit       int
	head, tail  int
	hb, ringMax int
	first       []byte
	// ring holds the bytes after first. Once it is full, start is the offset
	// of its oldest byte.
	ring  []byte
	start int
	total int64
	feeds int64
	last  byte
}

// NewLineBuffer returns a buffer that keeps the first head and the last tail
// lines within limit bytes. The limit is adjusted as Options.OutputLimit is,
// a negative head is 0, and a tail below 1 is 1.
func NewLineBuffer(limit, head, tail int) *LineBuffer {
	b := &LineBuffer{limit: clampLimit(limit), head: max(head, 0), tail: max(tail, 1)}
	if b.head > 0 {
		b.hb = (b.limit - omissionReserve) / 2
	}
	b.ringMax = b.limit - b.hb
	return b
}

// Write keeps what the policy can need and counts the rest. It always
// returns len(p), nil.
func (b *LineBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(n)
	b.feeds += int64(bytes.Count(p, []byte{'\n'}))
	b.last = p[n-1]
	if room := b.hb - len(b.first); room > 0 {
		k := min(room, len(p))
		b.first = appendCapped(b.first, p[:k], b.hb)
		p = p[k:]
	}
	if room := b.ringMax - len(b.ring); room > 0 {
		k := min(room, len(p))
		b.ring = appendCapped(b.ring, p[:k], b.ringMax)
		p = p[k:]
	}
	if len(p) == 0 {
		return n, nil
	}
	if len(p) >= len(b.ring) {
		copy(b.ring, p[len(p)-len(b.ring):])
		b.start = 0
		return n, nil
	}
	k := copy(b.ring[b.start:], p)
	copy(b.ring, p[k:])
	b.start = (b.start + len(p)) % len(b.ring)
	return n, nil
}

// appendCapped appends p to buf without letting its capacity pass limit,
// which append alone would.
func appendCapped(buf, p []byte, limit int) []byte {
	if need := len(buf) + len(p); need > cap(buf) {
		grown := make([]byte, len(buf), min(max(2*cap(buf), need, 512), limit))
		copy(grown, buf)
		buf = grown
	}
	return append(buf, p...)
}

// held is the memory the buffer holds, for tests of the bound.
func (b *LineBuffer) held() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return cap(b.first) + cap(b.ring)
}

// String returns the kept stream: the stream whole when it has at most
// head + tail lines and limit bytes, and otherwise its head part, one
// omission line, and its tail part, at most limit bytes in all.
func (b *LineBuffer) String() string {
	s, _ := b.Kept()
	return s
}

// Report returns what the stream had and what String keeps of it.
func (b *LineBuffer) Report() StreamReport {
	_, r := b.Kept()
	return r
}

// Kept returns String and Report read at one moment, so the report
// describes the kept stream even while a process left behind by the task
// still writes.
func (b *LineBuffer) Kept() (string, StreamReport) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ring := append(b.ring[b.start:len(b.ring):len(b.ring)], b.ring[:b.start]...)
	dropped := b.total - int64(len(b.first)) - int64(len(ring))
	rep := StreamReport{Lines: b.feeds, Bytes: b.total}
	if b.total > 0 && b.last != '\n' {
		rep.Lines++
	}
	if rep.Lines <= int64(b.head+b.tail) && b.total <= int64(b.limit) {
		rep.KeptLines, rep.KeptBytes = rep.Lines, rep.Bytes
		return string(b.first) + string(ring), rep
	}

	head := b.first[:headEnd(b.first, b.head, b.hb)]
	// The bytes after the head part that the buffer holds, and the byte
	// before them when it is known: it decides whether the tail starts a line.
	rest, before := ring, byte(0)
	if dropped == 0 {
		rest = append(b.first[len(head):len(b.first):len(b.first)], ring...)
		before = '\n'
		if len(head) > 0 {
			before = head[len(head)-1]
		}
	}
	from := tailStart(rest, b.tail)
	if tb := b.limit - omissionReserve - b.hb; len(rest)-from > tb {
		from = len(rest) - tb
		for skip := 0; skip < utf8.UTFMax-1 && from < len(rest) && !utf8.RuneStart(rest[from]); skip++ {
			from++
		}
	}
	tail := rest[from:]
	if from > 0 {
		before = rest[from-1]
	}

	headLines := lineCount(head)
	tailLines := lineCount(tail)
	rep.KeptLines = headLines + tailLines
	omittedFeeds := b.feeds - int64(bytes.Count(head, []byte{'\n'})) - int64(bytes.Count(tail, []byte{'\n'}))
	if len(head) > 0 && head[len(head)-1] != '\n' && len(tail) > 0 && before != '\n' && omittedFeeds == 0 {
		// The head and the tail cut one line: it is kept once.
		rep.KeptLines--
	}
	rep.KeptBytes = int64(len(head) + len(tail))

	out := make([]byte, 0, len(head)+omissionReserve+len(tail))
	out = append(out, head...)
	if len(head) > 0 && head[len(head)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, omissionLine(rep.Lines-rep.KeptLines, rep.Bytes-rep.KeptBytes)...)
	out = append(out, tail...)
	return string(out), rep
}

// headEnd is the length of the first lines of p, at most hb bytes and never
// inside a UTF-8 character.
func headEnd(p []byte, lines, hb int) int {
	if lines == 0 {
		return 0
	}
	end := 0
	for range lines {
		i := bytes.IndexByte(p[end:], '\n')
		if i < 0 {
			// The line goes on past what p holds, so it is cut.
			return len(trimPartialRune(p[:min(len(p), hb)]))
		}
		end += i + 1
	}
	if end > hb {
		end = len(trimPartialRune(p[:hb]))
	}
	return end
}

// tailStart is the offset in p of its last lines: a line feed that ends p
// belongs to the last line.
func tailStart(p []byte, lines int) int {
	end := len(p)
	if end > 0 && p[end-1] == '\n' {
		end--
	}
	for range lines {
		i := bytes.LastIndexByte(p[:end], '\n')
		if i < 0 {
			return 0
		}
		end = i
	}
	return end + 1
}

// lineCount is the number of lines p touches.
func lineCount(p []byte) int64 {
	n := int64(bytes.Count(p, []byte{'\n'}))
	if len(p) > 0 && p[len(p)-1] != '\n' {
		n++
	}
	return n
}
