package tasksuggest

import (
	"bytes"
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/rune-task-runner/rune/internal/taskimport"
)

// Task is the Runefile text of the suggestion (research R12), its lines
// ended with nl.
func (s *Suggestion) Task(nl string) string {
	lines := []string{"# " + s.Description}
	if s.Dir != s.defaultDir {
		// The directory was checked when the group was made (writable).
		dir, _ := taskimport.Quote(cmp.Or(s.Dir, "."))
		lines = append(lines, "[working-directory("+dir+")]")
	}
	head := s.Name
	if s.Param != nil {
		// A default is a plain word, which always quotes.
		def, _ := taskimport.Quote(s.Param.Default)
		head += " " + s.Param.Name + "=" + def
	}
	lines = append(lines, head+":", "    "+s.Body)
	return strings.Join(lines, nl) + nl
}

// Append is the Runefile text old with the tasks of picked added at the
// end: every byte of old stays, a missing final line feed is added, and a
// blank line comes before each task. Empty text gets the tasks alone.
func Append(old []byte, picked []*Suggestion) []byte {
	nl := "\n"
	if bytes.Contains(old, []byte("\r\n")) {
		nl = "\r\n"
	}
	out := slices.Clone(old)
	blank := len(bytes.TrimSpace(old)) > 0
	if blank && !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, nl...)
	}
	for i, s := range picked {
		if blank || i > 0 {
			out = append(out, nl...)
		}
		out = append(out, s.Task(nl)...)
	}
	return out
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// count writes n and the noun, in the singular for 1.
func count(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// outcomeText names the non-zero outcomes in a fixed order.
func outcomeText(outcomes map[Outcome]int) string {
	var parts []string
	for _, o := range []struct {
		kind Outcome
		word string
	}{{Succeeded, "succeeded"}, {Failed, "failed"}, {Interrupted, "interrupted"}, {Started, "started in the background"}} {
		if n := outcomes[o.kind]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, o.word))
		}
	}
	return strings.Join(parts, ", ")
}

// maxValues is how many values of a family a listing shows.
const maxValues = 5

// Listing is the text of `rune suggest` (contracts/cli.md).
func (r *Report) Listing(root string, minRuns int) string {
	var b strings.Builder
	if r.Commands == 0 && r.Attempts == 0 {
		fmt.Fprintf(&b, "Nothing recorded for %s.\nTurn recording on with: rune agent observe claude\n", root)
		r.writeNotes(&b)
		return b.String()
	}
	dates := day(r.First) + " to " + day(r.Last)
	if day(r.First) == day(r.Last) {
		dates = "on " + day(r.Last)
	}
	fmt.Fprintf(&b, "Recorded %s in %s, %s.\n", count(int64(r.Commands), "command", "commands"), count(int64(r.Sessions), "session", "sessions"), dates)
	if r.Attempts == 1 {
		b.WriteString("1 attempt has no recorded outcome.\n")
	} else if r.Attempts > 1 {
		fmt.Fprintf(&b, "%d attempts have no recorded outcome.\n", r.Attempts)
	}
	for _, s := range r.Suggestions {
		b.WriteString("\n")
		writeSuggestion(&b, s)
	}
	if len(r.Reviews) > 0 {
		b.WriteString("\nFor review, not turned into a task:\n")
		for _, v := range r.Reviews {
			fmt.Fprintf(&b, "  ran %s: %s", count(int64(v.Group.Runs), "time", "times"), v.Group.Text)
			if v.Group.Dir != "" {
				fmt.Fprintf(&b, " (in %s)", v.Group.Dir)
			}
			fmt.Fprintf(&b, "\n    reason: %s\n", v.Reason)
		}
	}
	// A command that reached the bar and is already a task is listed under
	// Already tasks; lowering the bar is no advice then.
	if len(r.Suggestions) == 0 && len(r.Reviews) == 0 && len(r.Covered) == 0 {
		fmt.Fprintf(&b, "\nNo command ran %d times or more. Lower the bar with --min-runs.\n", minRuns)
	}
	r.writeNotes(&b)
	if len(r.Suggestions) > 0 {
		b.WriteString("\nApply with: rune suggest apply ID...\n")
	}
	return b.String()
}

func writeSuggestion(b *strings.Builder, s *Suggestion) {
	values := ""
	if s.Param != nil {
		values = " with " + count(int64(len(s.Param.Values)), "value", "values")
	}
	fmt.Fprintf(b, "[%s] the agent ran this %s%s (%s) in %s, last on %s", s.ID, count(int64(s.Runs), "time", "times"),
		values, outcomeText(s.Outcomes), count(int64(s.Sessions()), "session", "sessions"), day(s.Last))
	if s.Dir != "" {
		fmt.Fprintf(b, ", in %s", s.Dir)
	}
	b.WriteString(":\n")
	if s.Param == nil {
		fmt.Fprintf(b, "    %s\n", s.Groups[0].Text)
	} else {
		shown := s.Param.Values[:min(len(s.Param.Values), maxValues)]
		width := 0
		for _, v := range shown {
			width = max(width, len(v.Text))
		}
		for _, v := range shown {
			fmt.Fprintf(b, "    %-*s   (%s)\n", width, v.Text, count(int64(v.Runs), "run", "runs"))
		}
		if more := len(s.Param.Values) - len(shown); more > 0 {
			fmt.Fprintf(b, "    and %d more\n", more)
		}
	}
	b.WriteString("  suggested task:\n")
	for line := range strings.Lines(s.Task("\n")) {
		b.WriteString("    " + line)
	}
}

// writeNotes writes the lines about what was left out.
func (r *Report) writeNotes(b *strings.Builder) {
	var lines []string
	if len(r.Covered) > 0 {
		parts := make([]string, len(r.Covered))
		for i, c := range r.Covered {
			parts[i] = fmt.Sprintf("%s (%s)", c.Task, count(int64(c.Runs), "run", "runs"))
		}
		lines = append(lines, "Already tasks: "+strings.Join(parts, ", ")+".")
	}
	var total int64
	var parts []string
	for _, reason := range slices.Sorted(maps.Keys(r.Dropped)) {
		if n := r.Dropped[reason]; n > 0 {
			total += n
			parts = append(parts, fmt.Sprintf("%s %d", reason, n))
		}
	}
	if total > 0 {
		lines = append(lines, fmt.Sprintf("Not recorded: %s (%s).", count(total, "event", "events"), strings.Join(parts, ", ")))
	}
	if r.Damaged > 0 {
		lines = append(lines, fmt.Sprintf("Skipped %s in the store.", count(int64(r.Damaged), "damaged line", "damaged lines")))
	}
	if len(lines) > 0 {
		b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	}
}
