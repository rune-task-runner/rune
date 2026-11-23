package mcpserver

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// keepRef is the rule of contracts/mcp-results.md applied to a whole stream
// held in memory. LineBuffer must give the same result while holding at
// most limit bytes.
func keepRef(s []byte, limit, head, tail int) (string, StreamReport) {
	limit = clampLimit(limit)
	var lines [][]byte
	for rest := s; len(rest) > 0; {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			lines = append(lines, rest)
			break
		}
		lines = append(lines, rest[:i+1])
		rest = rest[i+1:]
	}
	n := len(lines)
	rep := StreamReport{Lines: int64(n), Bytes: int64(len(s))}
	if n <= head+tail && len(s) <= limit {
		rep.KeptLines, rep.KeptBytes = rep.Lines, rep.Bytes
		return string(s), rep
	}
	hb := 0
	if head > 0 {
		hb = (limit - omissionReserve) / 2
	}
	tb := limit - omissionReserve - hb
	h := bytes.Join(lines[:min(head, n)], nil)
	if len(h) > hb {
		h = trimPartialRune(h[:hb])
	}
	var restLines [][]byte
	for rest := s[len(h):]; len(rest) > 0; {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			restLines = append(restLines, rest)
			break
		}
		restLines = append(restLines, rest[:i+1])
		rest = rest[i+1:]
	}
	t := bytes.Join(restLines[max(0, len(restLines)-tail):], nil)
	if len(t) > tb {
		t = t[len(t)-tb:]
		for skip := 0; skip < utf8.UTFMax-1 && len(t) > 0 && !utf8.RuneStart(t[0]); skip++ {
			t = t[1:]
		}
	}
	tailFrom := len(s) - len(t)
	off := 0
	for _, l := range lines {
		end := off + len(l)
		if off < len(h) || end > tailFrom {
			rep.KeptLines++
		}
		off = end
	}
	rep.KeptBytes = int64(len(h) + len(t))
	var out strings.Builder
	out.Write(h)
	if len(h) > 0 && h[len(h)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteString(omissionLine(rep.Lines-rep.KeptLines, rep.Bytes-rep.KeptBytes))
	out.Write(t)
	return out.String(), rep
}

func numberedLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func keep(s string, limit, head, tail int) (string, StreamReport) {
	b := NewLineBuffer(limit, head, tail)
	_, _ = b.Write([]byte(s))
	return b.String(), b.Report()
}

// Spec 057 US1: the last lines of each stream, with one omission line.
func TestLineBufferTail(t *testing.T) {
	long := strings.Repeat("x", 1<<20)
	cases := []struct {
		name   string
		in     string
		limit  int
		tail   int
		want   string
		report StreamReport
	}{
		{"empty", "", 0, 3, "", StreamReport{}},
		{"fewer lines than tail", "a\nb\n", 0, 3, "a\nb\n", StreamReport{2, 4, 2, 4}},
		{"exactly tail lines", "a\nb\nc\n", 0, 3, "a\nb\nc\n", StreamReport{3, 6, 3, 6}},
		{"one line more", "a\nb\nc\nd\n", 0, 3, "[omitted 1 line, 2 bytes]\nb\nc\nd\n", StreamReport{4, 8, 3, 6}},
		{
			"the contract example", numberedLines(2000), 0, 3,
			"[omitted 1997 lines, 18863 bytes]\nline 1998\nline 1999\nline 2000\n",
			StreamReport{2000, 18893, 3, 30},
		},
		{"no final line feed", "a\nb\nc", 0, 2, "[omitted 1 line, 2 bytes]\nb\nc", StreamReport{3, 5, 2, 3}},
		{"no final line feed, whole", "a\nb", 0, 2, "a\nb", StreamReport{2, 3, 2, 3}},
		{"CR LF", "a\r\nb\r\nc\r\n", 0, 1, "[omitted 2 lines, 6 bytes]\nc\r\n", StreamReport{3, 9, 1, 3}},
		{"lone CR is one line", "10%\r20%\r30%\n", 0, 1, "10%\r20%\r30%\n", StreamReport{1, 12, 1, 12}},
		{
			"one line longer than the limit", long, 1024, 3,
			fmt.Sprintf("[omitted 0 lines, %d bytes]\n", 1<<20-(1024-omissionReserve)) + long[:1024-omissionReserve],
			StreamReport{1, 1 << 20, 1, 1024 - omissionReserve},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rep := keep(c.in, c.limit, 0, c.tail)
			if got != c.want {
				t.Errorf("String() = %q, want %q", trunc(got), trunc(c.want))
			}
			if rep != c.report {
				t.Errorf("Report() = %+v, want %+v", rep, c.report)
			}
			wantS, wantR := keepRef([]byte(c.in), c.limit, 0, c.tail)
			if got != wantS || rep != wantR {
				t.Errorf("differs from the reference: %+v vs %+v", rep, wantR)
			}
		})
	}
}

func trunc(s string) string {
	if len(s) > 200 {
		return s[:100] + "..." + s[len(s)-100:]
	}
	return s
}

// The limit is a hard ceiling: total == L is whole, one byte more is not.
func TestLineBufferAtTheLimit(t *testing.T) {
	const limit = 1024
	exact := strings.Repeat("y", limit-1) + "\n"
	if got, rep := keep(exact, limit, 0, 5); got != exact || rep.KeptBytes != limit {
		t.Errorf("a stream of exactly the limit is not whole: %d bytes, %+v", len(got), rep)
	}
	over := exact + "z"
	got, rep := keep(over, limit, 0, 5)
	if len(got) > limit || !strings.HasSuffix(got, "\nz") || rep.Bytes != limit+1 {
		t.Errorf("one byte over: %d bytes, %+v", len(got), rep)
	}
	if !strings.HasPrefix(got, "[omitted 0 lines, ") {
		t.Errorf("one byte over: %q", trunc(got))
	}
}

// A cut never splits a UTF-8 character, whatever its length, and bytes that
// are not UTF-8 are kept as they are.
func TestLineBufferUnicodeCuts(t *testing.T) {
	const limit = 1024
	tb := limit - omissionReserve
	for _, r := range []string{"é", "€", "😀"} {
		for shift := range len(r) {
			in := strings.Repeat("a", 3000+shift) + strings.Repeat(r, 1000)
			got, rep := keep(in, limit, 0, 1)
			if !utf8.ValidString(got) {
				t.Errorf("%s shift %d: result is not valid UTF-8", r, shift)
			}
			if len(got) > limit || rep.KeptBytes > int64(tb) || rep.KeptBytes < int64(tb-utf8.UTFMax+1) {
				t.Errorf("%s shift %d: %d bytes, %+v", r, shift, len(got), rep)
			}
			if wantS, wantR := keepRef([]byte(in), limit, 0, 1); got != wantS || rep != wantR {
				t.Errorf("%s shift %d: differs from the reference", r, shift)
			}
		}
	}
	bad := "ok\n\xff\xfe\n"
	if got, _ := keep(bad, 0, 0, 5); got != bad {
		t.Errorf("invalid UTF-8 changed: %q", got)
	}
}

// Spec 057 US3: the first and the last lines, with one omission line between.
func TestLineBufferHeadAndTail(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		head, tail int
		want       string
		report     StreamReport
	}{
		{"the contract example", "a\nb\nc\nd\ne\nf\n", 2, 2, "a\nb\n[omitted 2 lines, 4 bytes]\ne\nf\n", StreamReport{6, 12, 4, 8}},
		{"whole", numberedLines(45), 10, 40, numberedLines(45), StreamReport{45, int64(len(numberedLines(45))), 45, int64(len(numberedLines(45)))}},
		{"exactly head plus tail", "a\nb\nc\n", 1, 2, "a\nb\nc\n", StreamReport{3, 6, 3, 6}},
		{"no final line feed", "a\nb\nc\nd", 1, 1, "a\n[omitted 2 lines, 4 bytes]\nd", StreamReport{4, 7, 2, 3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rep := keep(c.in, 0, c.head, c.tail)
			if got != c.want || rep != c.report {
				t.Errorf("got %q %+v, want %q %+v", got, rep, c.want, c.report)
			}
		})
	}

	t.Run("500 lines", func(t *testing.T) {
		got, rep := keep(numberedLines(500), 0, 10, 40)
		all := strings.SplitAfter(numberedLines(500), "\n")
		omitted := strings.Join(all[10:460], "")
		want := strings.Join(all[:10], "") + fmt.Sprintf("[omitted 450 lines, %d bytes]\n", len(omitted)) + strings.Join(all[460:], "")
		if got != want || rep.Lines != 500 || rep.KeptLines != 50 {
			t.Errorf("got %q %+v", trunc(got), rep)
		}
	})

	t.Run("a head line longer than its half", func(t *testing.T) {
		const limit = 1024
		in := strings.Repeat("h", 2000) + "\n" + numberedLines(10)
		got, rep := keep(in, limit, 1, 2)
		hb := (limit - omissionReserve) / 2
		if !strings.HasPrefix(got, strings.Repeat("h", hb)+"\n[omitted ") || len(got) > limit {
			t.Errorf("got %q", trunc(got))
		}
		if wantS, wantR := keepRef([]byte(in), limit, 1, 2); got != wantS || rep != wantR {
			t.Errorf("differs from the reference: %+v vs %+v", rep, wantR)
		}
	})

	t.Run("one long line is counted once", func(t *testing.T) {
		in := strings.Repeat("q", 5000)
		got, rep := keep(in, 1024, 1, 1)
		if rep.Lines != 1 || rep.KeptLines != 1 || len(got) > 1024 {
			t.Errorf("got %d bytes, %+v", len(got), rep)
		}
		if !strings.Contains(got, "[omitted 0 lines, ") {
			t.Errorf("got %q", trunc(got))
		}
	})

	t.Run("between L minus the reserve and L", func(t *testing.T) {
		const limit = 1024
		in := strings.Repeat("ab\n", (limit-20)/3)
		got, rep := keep(in, limit, 3, 3)
		if len(got) > limit {
			t.Errorf("%d bytes", len(got))
		}
		if wantS, wantR := keepRef([]byte(in), limit, 3, 3); got != wantS || rep != wantR {
			t.Errorf("differs from the reference: %+v vs %+v", rep, wantR)
		}
	})
}

func randomStream(r *rand.Rand) []byte {
	pieces := []string{"a", "bc", "\n", "\n", "\n", "é", "€", "😀", "\r", "\xff", strings.Repeat("z", 700)}
	var b []byte
	for range r.IntN(400) {
		b = append(b, pieces[r.IntN(len(pieces))]...)
	}
	return b
}

// Spec 057 FR-010, FR-012: for any stream and any split into writes the
// result is the reference's, it fits the limit, and its report agrees with
// its omission line.
func TestLineBufferProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(57, 2026))
	for i := range 3000 {
		s := randomStream(r)
		limit := []int{1024, 1500, 4096}[r.IntN(3)]
		head := []int{0, 0, 1, 2, 5}[r.IntN(5)]
		tail := []int{1, 2, 5, 50}[r.IntN(4)]
		b := NewLineBuffer(limit, head, tail)
		for rest := s; len(rest) > 0; {
			k := 1 + r.IntN(min(len(rest), 900))
			_, _ = b.Write(rest[:k])
			rest = rest[k:]
		}
		got, rep := b.String(), b.Report()
		wantS, wantR := keepRef(s, limit, head, tail)
		if got != wantS || rep != wantR {
			t.Fatalf("case %d (limit %d, head %d, tail %d, %d bytes): got %q %+v, want %q %+v",
				i, limit, head, tail, len(s), trunc(got), rep, trunc(wantS), wantR)
		}
		if len(got) > limit {
			t.Fatalf("case %d: %d bytes over limit %d", i, len(got), limit)
		}
		marker := ""
		if rep.KeptBytes < rep.Bytes {
			marker = omissionLine(rep.Lines-rep.KeptLines, rep.Bytes-rep.KeptBytes)
			if !strings.Contains(got, marker) {
				t.Fatalf("case %d: no %q in %q", i, marker, trunc(got))
			}
		}
		if extra := len(got) - int(rep.KeptBytes) - len(marker); extra != 0 && extra != 1 {
			t.Fatalf("case %d: kept %d bytes and a marker of %d, result %d", i, rep.KeptBytes, len(marker), len(got))
		}
	}
}

// Spec 057 FR-011: memory never passes the limit, however much is written.
func TestLineBufferMemoryBound(t *testing.T) {
	for _, head := range []int{0, 10} {
		b := NewLineBuffer(DefaultOutputLimit, head, 50)
		chunk := []byte(strings.Repeat("0123456789abcdef", 255) + "\n")
		for written := 0; written < 50<<20; written += len(chunk) {
			_, _ = b.Write(chunk)
			if held := b.held(); held > DefaultOutputLimit {
				t.Fatalf("head %d: holds %d bytes after %d written", head, held, written)
			}
		}
		if got := b.String(); len(got) > DefaultOutputLimit {
			t.Errorf("head %d: result %d bytes", head, len(got))
		}
	}
}

// Writers of [parallel] tasks share one buffer.
func TestLineBufferConcurrentWriters(t *testing.T) {
	b := NewLineBuffer(4096, 2, 5)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 1000 {
				_, _ = fmt.Fprintf(b, "writer %d line %d\n", w, i)
			}
		})
	}
	wg.Wait()
	rep := b.Report()
	if rep.Lines != 8000 || rep.KeptLines != 7 {
		t.Errorf("report = %+v", rep)
	}
	if got := b.String(); len(got) > 4096 || strings.Count(got, "\n") != 8 {
		t.Errorf("result %q", trunc(got))
	}
}

func TestLineBufferWriteReturnsLength(t *testing.T) {
	b := NewLineBuffer(0, 0, 1)
	for _, p := range [][]byte{nil, {}, []byte("x"), bytes.Repeat([]byte("y"), 1<<17)} {
		if n, err := b.Write(p); n != len(p) || err != nil {
			t.Errorf("Write(%d bytes) = %d, %v", len(p), n, err)
		}
	}
}

// Kept is String and Report of the same moment.
func TestLineBufferKept(t *testing.T) {
	b := NewLineBuffer(0, 1, 1)
	_, _ = b.Write([]byte("a\nb\nc\n"))
	s, r := b.Kept()
	if s != b.String() || r != b.Report() {
		t.Errorf("Kept = %q, %+v; String = %q, Report = %+v", s, r, b.String(), b.Report())
	}
	if s != "a\n[omitted 1 line, 2 bytes]\nc\n" || r.KeptLines != 2 || r.KeptBytes != 4 {
		t.Errorf("Kept = %q, %+v", s, r)
	}
}

func TestOmissionLine(t *testing.T) {
	for _, c := range []struct {
		lines, bytes int64
		want         string
	}{
		{1, 1, "[omitted 1 line, 1 byte]\n"},
		{0, 2, "[omitted 0 lines, 2 bytes]\n"},
		{1950, 98765, "[omitted 1950 lines, 98765 bytes]\n"},
	} {
		if got := omissionLine(c.lines, c.bytes); got != c.want {
			t.Errorf("omissionLine(%d, %d) = %q, want %q", c.lines, c.bytes, got, c.want)
		}
	}
	const maxInt64 = 1<<63 - 1
	if got := len(omissionLine(maxInt64, maxInt64)) + 1; got != omissionReserve {
		t.Errorf("the longest omission line and its line feed are %d bytes, the reserve is %d", got, omissionReserve)
	}
}

func TestNewLineBufferBounds(t *testing.T) {
	b := NewLineBuffer(10, -3, 0)
	if b.limit != MinOutputLimit || b.head != 0 || b.tail != 1 {
		t.Errorf("limit %d, head %d, tail %d", b.limit, b.head, b.tail)
	}
}
