package mcpserver

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// OutputReport is the output policy of a call and what it kept of each
// stream (spec 057).
type OutputReport struct {
	Head   int          `json:"head"`
	Tail   int          `json:"tail"`
	Stdout StreamReport `json:"stdout"`
	Stderr StreamReport `json:"stderr"`
}

// The kinds of a failure summary, in the order the engine decides them.
const (
	FailureCancelled = "cancelled"
	FailureStep      = "step"
	FailureScript    = "script"
	FailureError     = "error"
	FailureUnknown   = "unknown"
)

// FailureSummary is what Rune observed about a failed call (spec 057). The
// engine sets Kind, Task, FailedTask, File, Line, Command and Reason, the
// last two masked. The server sets FixSuggestion, StdoutTail and StderrTail,
// and bounds every field before the summary reaches the text or the
// structured content.
type FailureSummary struct {
	Kind          string `json:"kind"`
	Task          string `json:"task"`
	FailedTask    string `json:"failedTask,omitempty"`
	File          string `json:"file,omitempty"`
	Line          int    `json:"line,omitempty"`
	Command       string `json:"command,omitempty"`
	Reason        string `json:"reason"`
	FixSuggestion bool   `json:"fixSuggestion"`
	StdoutTail    string `json:"stdoutTail,omitempty"`
	StderrTail    string `json:"stderrTail,omitempty"`
}

// The bounds of a failure summary (spec 057). The sum of the field sizes and
// the fixed text is under maxSummaryBytes, so the section never needs a cut
// of its own and the structured object stays equal to it.
const (
	maxNameBytes    = 128
	maxFileBytes    = 256
	maxCommandBytes = 256
	maxReasonBytes  = 512
	maxTailBytes    = 1024
	tailLineCount   = 5
	maxSummaryBytes = 4096
)

// The reasons of the kinds with no error to quote. The server sets them, so
// every engine says the same.
const (
	cancelledReason = "the call was cancelled"
	unknownReason   = "not known, Rune has only the exit status"
)

// completeSummary returns f bounded, with only the fields its kind shows,
// the reasons the server owns, whether r has a fix suggestion, and the last
// lines of r's streams, which must already be capped.
func completeSummary(f FailureSummary, r Result) FailureSummary {
	switch f.Kind {
	case FailureStep, FailureScript, FailureError, FailureCancelled, FailureUnknown:
	default:
		f.Kind = FailureUnknown
	}
	if f.Kind != FailureStep && f.Kind != FailureScript {
		f.File, f.Line = "", 0
	}
	if f.Kind != FailureStep {
		f.Command = ""
	}
	switch f.Kind {
	case FailureCancelled:
		f.Reason = cancelledReason
	case FailureUnknown:
		f.Reason = unknownReason
	}
	f.Task = boundField(f.Task, maxNameBytes)
	f.FailedTask = boundField(f.FailedTask, maxNameBytes)
	f.File = boundField(f.File, maxFileBytes)
	f.Command = boundField(f.Command, maxCommandBytes)
	f.Reason = boundField(f.Reason, maxReasonBytes)
	f.FixSuggestion = r.FixSuggestion != ""
	f.StdoutTail = lastLines(r.Stdout)
	f.StderrTail = lastLines(r.Stderr)
	return f
}

// boundField makes s one line of valid UTF-8 and at most size bytes. A
// longer one keeps its beginning and says how much was cut.
func boundField(s string, size int) string {
	s = strings.Map(func(r rune) rune {
		if r != '\t' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, string(utf8.RuneError)))
	if len(s) <= size {
		return s
	}
	// The count has at most as many digits as len(s), so the marker that
	// is reserved for here is never shorter than the one written.
	keep := size - len(" [cut  bytes]") - len(strconv.Itoa(len(s)))
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + " [cut " + strconv.Itoa(len(s)-keep) + " bytes]"
}

// lastLines is the last tailLineCount lines of a kept stream, at most
// maxTailBytes. A longer one keeps its end after a line that says how much
// was cut.
func lastLines(stream string) string {
	end := len(stream)
	if end > 0 && stream[end-1] == '\n' {
		end--
	}
	from := 0
	for range tailLineCount {
		i := strings.LastIndexByte(stream[:end], '\n')
		if i < 0 {
			from = 0
			break
		}
		end, from = i, i+1
	}
	t := stream[from:]
	if len(t) <= maxTailBytes {
		return t
	}
	cut := len(t) - (maxTailBytes - len("[cut  bytes]\n") - len(strconv.Itoa(len(t))))
	for cut < len(t) && !utf8.RuneStart(t[cut]) {
		cut++
	}
	return "[cut " + strconv.Itoa(cut) + " bytes]\n" + t[cut:]
}

// renderSummary is the failure summary section of the text: its lines in the
// order of contracts/mcp-results.md, with no final line feed.
func renderSummary(f FailureSummary) string {
	var b strings.Builder
	b.WriteString("[failure summary - from rune]\ntask: " + f.Task + "\n")
	if f.FailedTask != "" {
		b.WriteString("failed task: " + f.FailedTask + "\n")
	}
	switch f.Kind {
	case FailureStep:
		b.WriteString("step: " + f.File + ":" + strconv.Itoa(f.Line) + ": " + f.Command + "\n")
	case FailureScript:
		b.WriteString("step: " + f.File + ":" + strconv.Itoa(f.Line) + ": the task's script\n")
	case FailureError:
		b.WriteString("step: none, the run stopped before a body line failed\n")
	}
	b.WriteString("reason: " + f.Reason + "\n")
	if f.FixSuggestion {
		b.WriteString("fix suggestion: follows, from the task's || failure hooks\n")
	}
	for _, s := range []struct{ name, tail string }{{"stdout", f.StdoutTail}, {"stderr", f.StderrTail}} {
		if s.tail == "" {
			continue
		}
		b.WriteString("last lines of " + s.name + ":\n" + s.tail)
		if !strings.HasSuffix(s.tail, "\n") {
			b.WriteByte('\n')
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
