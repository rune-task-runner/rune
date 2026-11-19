package agentinit

import (
	"bytes"
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines a hunk shows around a change.
const diffContext = 3

// diffOp is one line of a comparison: kept, removed or added.
type diffOp struct {
	kind byte // ' ', '-' or '+'
	text string
}

// unifiedDiff is the difference between two texts in unified format, as
// `git apply` and `patch` read it. It is "" when they are equal. The files
// are a few dozen lines, so the comparison is the plain table of the longest
// common subsequence, over the lines between the common head and tail.
func unifiedDiff(path string, old, edited []byte, existed bool) string {
	if bytes.Equal(old, edited) {
		return ""
	}
	a, b := splitLines(old), splitLines(edited)
	// The lines both texts start and end with are kept as they are, so the
	// table below is only as large as the part that changed.
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	var ops []diffOp
	for _, line := range a[:head] {
		ops = append(ops, diffOp{' ', line})
	}
	kept := a[len(a)-tail:]
	a, b = a[head:len(a)-tail], b[head:len(b)-tail]
	// lcs[i][j] is the length of the longest common subsequence of a[i:]
	// and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for _, line := range kept {
		ops = append(ops, diffOp{' ', line})
	}

	var out strings.Builder
	from := "a/" + path
	if !existed {
		from = "/dev/null"
	}
	fmt.Fprintf(&out, "--- %s\n+++ b/%s\n", from, path)
	for start := 0; start < len(ops); {
		if ops[start].kind == ' ' {
			start++
			continue
		}
		// A hunk runs from the context before this change to the context
		// after the last change that is close enough to share it.
		end, last := start, start
		for end < len(ops) && end-last <= 2*diffContext {
			if ops[end].kind != ' ' {
				last = end
			}
			end++
		}
		lo, hi := max(0, start-diffContext), min(len(ops), last+1+diffContext)
		writeHunk(&out, ops, lo, hi)
		start = hi
	}
	return out.String()
}

// writeHunk writes ops[lo:hi] with its @@ line.
func writeHunk(out *strings.Builder, ops []diffOp, lo, hi int) {
	oldStart, newStart := 1, 1
	for _, op := range ops[:lo] {
		if op.kind != '+' {
			oldStart++
		}
		if op.kind != '-' {
			newStart++
		}
	}
	oldCount, newCount := 0, 0
	for _, op := range ops[lo:hi] {
		if op.kind != '+' {
			oldCount++
		}
		if op.kind != '-' {
			newCount++
		}
	}
	// An empty side is named by the line before it.
	if oldCount == 0 {
		oldStart--
	}
	if newCount == 0 {
		newStart--
	}
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
	for _, op := range ops[lo:hi] {
		out.WriteByte(op.kind)
		out.WriteString(op.text)
		if !strings.HasSuffix(op.text, "\n") {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
}

// splitLines cuts a text into lines that keep their line break.
func splitLines(text []byte) []string {
	var lines []string
	for line := range bytes.SplitAfterSeq(text, []byte("\n")) {
		if len(line) > 0 {
			lines = append(lines, string(line))
		}
	}
	return lines
}
