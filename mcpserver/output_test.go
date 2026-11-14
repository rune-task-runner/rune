package mcpserver

import (
	"bytes"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

var markerRE = regexp.MustCompile(`\n\[truncated (\d+) bytes\]\n`)

// numbered returns n bytes in which every position is recognizable, so a
// test can tell which part of the input a piece of output came from.
func numbered(n int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%09d\n", i)
	}
	return b.Bytes()[:n]
}

// splitTruncated splits a truncated rendering into head, N, and tail. It
// fails the test when got does not hold exactly one marker.
func splitTruncated(t *testing.T, got string) (head string, dropped int64, tail string) {
	t.Helper()
	locs := markerRE.FindAllStringSubmatchIndex(got, -1)
	if len(locs) != 1 {
		t.Fatalf("want exactly one marker, got %d in %d bytes", len(locs), len(got))
	}
	loc := locs[0]
	n, err := strconv.ParseInt(got[loc[2]:loc[3]], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return got[:loc[0]], n, got[loc[1]:]
}

// checkTruncated asserts the contract of a truncated stream: within the
// limit, head a prefix, tail a suffix, and shown plus dropped equal to input.
func checkTruncated(t *testing.T, got string, input []byte, limit int) (head, tail string) {
	t.Helper()
	if len(got) > limit {
		t.Errorf("rendered %d bytes, limit %d", len(got), limit)
	}
	head, dropped, tail := splitTruncated(t, got)
	if !bytes.HasPrefix(input, []byte(head)) {
		t.Errorf("head is not a prefix of the input")
	}
	if !bytes.HasSuffix(input, []byte(tail)) {
		t.Errorf("tail is not a suffix of the input")
	}
	if shown := int64(len(head) + len(tail)); shown+dropped != int64(len(input)) {
		t.Errorf("shown %d + dropped %d != written %d", shown, dropped, len(input))
	}
	return head, tail
}

func render(limit int, input []byte) string {
	b := NewOutputBuffer(limit)
	_, _ = b.Write(input)
	return b.String()
}

func TestOutputLimitConstants(t *testing.T) {
	if DefaultOutputLimit != 65536 || MinOutputLimit != 1024 || MaxOutputLimit != 1073741824 {
		t.Errorf("constants = %d, %d, %d", DefaultOutputLimit, MinOutputLimit, MaxOutputLimit)
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{
		0: 65536, -5: 65536, 10: 1024, 1023: 1024, 1024: 1024, 4096: 4096,
		1 << 30: 1 << 30, 1<<30 + 1: 1 << 30,
	}
	for in, want := range cases {
		if got := clampLimit(in); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestNewOutputBufferClamps(t *testing.T) {
	for in, want := range map[int]int{0: 65536, -5: 65536, 10: 1024, 1<<30 + 7: 1 << 30, 2048: 2048} {
		if got := NewOutputBuffer(in).limit; got != want {
			t.Errorf("NewOutputBuffer(%d).limit = %d, want %d", in, got, want)
		}
	}
}

func TestOutputBufferWhole(t *testing.T) {
	const limit = 4096
	for _, n := range []int{0, 1, limit - 1, limit} {
		input := numbered(n)
		if got := render(limit, input); got != string(input) {
			t.Errorf("%d bytes: output differs from input (got %d bytes)", n, len(got))
		}
	}
}

func TestOutputBufferTruncates(t *testing.T) {
	input := numbered(3_000_000)
	got := render(DefaultOutputLimit, input)
	if len(got) > DefaultOutputLimit {
		t.Fatalf("rendered %d bytes", len(got))
	}
	want := string(input[:32748]) + "\n[truncated 2934504 bytes]\n" + string(input[len(input)-32748:])
	if got != want {
		t.Errorf("rendering differs from the contract: got %d bytes, want %d", len(got), len(want))
	}
}

func TestOutputBufferOneByteOver(t *testing.T) {
	const limit = 2048
	input := numbered(limit + 1)
	got := render(limit, input)
	if got == string(input) {
		t.Fatal("limit+1 bytes were returned whole")
	}
	checkTruncated(t, got, input, limit)
}

func TestOutputBufferMarkerArithmetic(t *testing.T) {
	for _, limit := range []int{1024, 1025, 4096, 65536} {
		for _, total := range []int{limit + 1, limit + 39, limit + 41, 2 * limit, 10*limit + 7} {
			input := numbered(total)
			head, tail := checkTruncated(t, render(limit, input), input, limit)
			headMax := (limit - 40) / 2
			if len(head) != headMax || len(tail) != limit-40-headMax {
				t.Errorf("limit %d total %d: head %d tail %d", limit, total, len(head), len(tail))
			}
		}
	}
}

func TestOutputBufferChunkingInvariant(t *testing.T) {
	const limit = 1024
	input := numbered(20_000)
	want := render(limit, input)

	byByte := NewOutputBuffer(limit)
	for i := range input {
		_, _ = byByte.Write(input[i : i+1])
	}
	if got := byByte.String(); got != want {
		t.Errorf("byte-by-byte rendering differs")
	}

	rng := rand.New(rand.NewSource(42))
	for round := 0; round < 200; round++ {
		b := NewOutputBuffer(limit)
		for rest := input; len(rest) > 0; {
			// Sizes go past the ring and past the limit.
			n := min(1+rng.Intn(3000), len(rest))
			_, _ = b.Write(rest[:n])
			rest = rest[n:]
		}
		if got := b.String(); got != want {
			t.Fatalf("round %d: chunked rendering differs", round)
		}
	}
}

func TestOutputBufferUTF8(t *testing.T) {
	const limit = 1024
	for _, char := range []string{"é", "€", "😀"} {
		// Each pad shifts the characters against both cuts.
		for pad := 0; pad < 4; pad++ {
			input := []byte(strings.Repeat("a", pad) + strings.Repeat(char, 2000))
			got := render(limit, input)
			if !utf8.ValidString(got) {
				t.Errorf("%q pad %d: result is not valid UTF-8", char, pad)
			}
			checkTruncated(t, got, input, limit)
		}
	}
}

func TestOutputBufferBinary(t *testing.T) {
	const limit = 1024
	for _, fill := range []byte{0xff, 0x80} {
		input := bytes.Repeat([]byte{fill}, 5000)
		checkTruncated(t, render(limit, input), input, limit)
	}
}

func TestOutputBufferLooksLikeMarker(t *testing.T) {
	input := []byte("before\n[truncated 5 bytes]\nafter\n")
	if got := render(1024, input); got != string(input) {
		t.Errorf("got %q", got)
	}
}

func TestOutputBufferMemoryBound(t *testing.T) {
	const limit = DefaultOutputLimit
	b := NewOutputBuffer(limit)
	if cap(b.buf) != 0 {
		t.Errorf("a new buffer allocated %d bytes up front", cap(b.buf))
	}
	_, _ = b.Write(make([]byte, 100))
	if cap(b.buf) > 1024 {
		t.Errorf("100 bytes cost a capacity of %d", cap(b.buf))
	}
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	for written := 0; written < 200<<20; written += len(chunk) {
		_, _ = b.Write(chunk)
		if cap(b.buf) > limit {
			t.Fatalf("capacity %d passed the limit %d after %d bytes", cap(b.buf), limit, written)
		}
	}
	if got := b.String(); len(got) > limit {
		t.Errorf("rendered %d bytes", len(got))
	}
}

func TestOutputBufferConcurrent(t *testing.T) {
	const limit, writers, each = 4096, 8, 1 << 20
	b := NewOutputBuffer(limit)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chunk := bytes.Repeat([]byte("y"), 1000)
			for written := 0; written < each; written += len(chunk) {
				_, _ = b.Write(chunk[:min(len(chunk), each-written)])
			}
		}()
	}
	wg.Wait()
	got := b.String()
	if len(got) > limit {
		t.Errorf("rendered %d bytes", len(got))
	}
	head, dropped, tail := splitTruncated(t, got)
	if int64(len(head)+len(tail))+dropped != writers*each {
		t.Errorf("shown %d + dropped %d != %d", len(head)+len(tail), dropped, writers*each)
	}
}

func TestOutputBufferStringRepeatable(t *testing.T) {
	const limit = 1024
	input := numbered(5000)
	b := NewOutputBuffer(limit)
	_, _ = b.Write(input)
	first := b.String()
	if second := b.String(); second != first {
		t.Error("String changed between two calls")
	}
	more := numbered(777)
	_, _ = b.Write(more)
	all := append(append([]byte{}, input...), more...)
	if got := b.String(); got != render(limit, all) {
		t.Error("a write after String gave a different rendering than one buffer")
	}
}

func TestOutputBufferWriteReturns(t *testing.T) {
	b := NewOutputBuffer(1024)
	for _, n := range []int{0, 1, 1023, 1, 5000, 3} {
		got, err := b.Write(make([]byte, n))
		if got != n || err != nil {
			t.Errorf("Write(%d bytes) = %d, %v", n, got, err)
		}
	}
}

func TestTrimPartialRune(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"ab€", "ab€"},
		{"ab\xe2\x82", "ab"},
		{"ab\xe2", "ab"},
		{"ab\xf0\x9f\x98", "ab"},
		{"ab😀", "ab😀"},
		{"ab\xff", "ab\xff"},
		{"\x80\x80\x80\x80", "\x80\x80\x80\x80"},
	}
	for _, c := range cases {
		if got := string(trimPartialRune([]byte(c.in))); got != c.want {
			t.Errorf("trimPartialRune(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCapOutput(t *testing.T) {
	const limit = 2048
	small := string(numbered(limit))
	if got := capOutput(small, limit); got != small {
		t.Error("a string at the limit was changed")
	}
	big := numbered(100_000)
	if got, want := capOutput(string(big), limit), render(limit, big); got != want {
		t.Error("capOutput differs from an OutputBuffer's rendering")
	}
	if got := capOutput(string(big), 0); len(got) > DefaultOutputLimit || len(got) <= limit {
		t.Errorf("limit 0 gave %d bytes, want the default limit", len(got))
	}
	// capOutput counts the middle of a long string without copying it. The
	// sizes sit on both sides of where that starts: limit plus the kept end.
	const tailMax = limit - 40 - (limit-40)/2
	for _, n := range []int{limit + 1, limit + tailMax - 1, limit + tailMax, limit + tailMax + 1, 3 * limit} {
		for _, input := range [][]byte{numbered(n), []byte(strings.Repeat("€", n/3+1))} {
			if got, want := capOutput(string(input), limit), render(limit, input); got != want {
				t.Errorf("%d bytes: capOutput differs from an OutputBuffer's rendering", len(input))
			}
		}
	}
}

// A zero OutputBuffer is used the way a bytes.Buffer is, so it must keep
// output and not drop everything.
func TestOutputBufferZeroValue(t *testing.T) {
	var b OutputBuffer
	if got := b.String(); got != "" {
		t.Errorf("an unwritten zero buffer returned %q", got)
	}
	var small OutputBuffer
	_, _ = small.Write([]byte("kept\n"))
	if got := small.String(); got != "kept\n" {
		t.Errorf("a zero buffer returned %q, want the output", got)
	}
	input := numbered(3_000_000)
	var big OutputBuffer
	_, _ = big.Write(input)
	if got, want := big.String(), render(DefaultOutputLimit, input); got != want {
		t.Errorf("a zero buffer returned %d bytes, want the default limit's rendering of %d", len(got), len(want))
	}
}
