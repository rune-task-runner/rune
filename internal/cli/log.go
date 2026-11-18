package cli

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/audit"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/parser"
	"github.com/rune-task-runner/rune/internal/sandbox"
	"github.com/rune-task-runner/rune/mcpserver"
)

// `rune log` reads the audit log of a project: what agents asked Rune's MCP
// servers to do (spec 052). It runs no task and writes nothing.

const (
	logDayLayout  = "2006-01-02"
	logTimeLayout = "15:04:05"
	// A range can span days, so its lines carry the day too.
	logDateTimeLayout = "2006-01-02 15:04:05"
	logBoundLayout    = "2006-01-02 15:04"

	maxLogArgsRunes  = 80
	maxLogErrorRunes = 200

	// maxAgoDays is the most days a duration can hold.
	maxAgoDays = int(math.MaxInt64 / (24 * time.Hour))
)

// LogFlags are the flags of `rune log`, as the command line gave them.
type LogFlags struct {
	Date  string // --date
	Since string // --since
	Until string // --until
	TZ    string // --tz

	// The list flags, each value possibly comma-separated.
	Tasks    []string // --task
	Outcomes []string // --outcome
	Clients  []string // --client
	Sessions []string // --session

	All  bool // --all
	JSON bool // --json
	Dir  bool // --dir

	// now is the clock that defines "today". Nil means time.Now; tests set
	// it.
	now func() time.Time
}

// logRange is the range of start times a listing covers, in the zone the
// operator reads it in.
type logRange struct {
	loc    *time.Location
	zone   string // the --tz value as given, or "local"
	now    time.Time
	since  time.Time // zero: from the oldest file
	until  time.Time // zero: to now
	oneDay bool
}

// Log prints the actions of the audit log of the Runefile found from the
// working directory, or of the one opts.File names.
func Log(opts Options, flags LogFlags) error {
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err != nil {
		return &UsageError{Err: err}
	}
	if flags.Dir && flags.others() {
		return usagef("--dir cannot be combined with other flags")
	}
	rng, err := flags.timeRange()
	if err != nil {
		return err
	}
	filter, err := flags.filter(rng)
	if err != nil {
		return err
	}
	base, err := auditBase(opts)
	if err != nil {
		// A wrong variable is the operator's to fix, like a wrong flag. A
		// host with no state directory is a failure to read the log.
		var usage *UsageError
		if errors.As(err, &usage) {
			return usagef("audit log: %v", err)
		}
		return &CommandFailure{Err: fmt.Errorf("audit log: %w", err)}
	}
	// The path a server resolves for the same Runefile, so both name one
	// directory whatever path the project was reached by.
	project := runefilePath(&ast.File{Path: runefile}, filepath.Dir(runefile))
	dir := audit.ProjectDir(base, project)
	if flags.Dir {
		fmt.Fprintln(opts.Stdout, dir)
		return nil
	}
	if hasLogTask(runefile) {
		fmt.Fprintln(opts.Stderr, `note: "log" is also a task of this Runefile; run it with: rune -- log`)
	}

	to := rng.until
	if !to.IsZero() {
		// Until is exclusive, and the files to read are named by day.
		to = to.Add(-time.Nanosecond)
	}
	actions, damage, err := audit.Read(dir, rng.since, to)
	if err != nil {
		return &CommandFailure{Err: err}
	}
	for _, d := range damage {
		fmt.Fprintln(opts.Stderr, damageWarning(d))
	}
	actions = slices.DeleteFunc(actions, func(a audit.Action) bool { return !filter.Match(a) })
	if flags.JSON {
		return printLogJSON(opts.Stdout, actions)
	}
	printLogText(opts.Stdout, actions, rng, project)
	return nil
}

// others reports whether any flag beside --dir was given.
func (f LogFlags) others() bool {
	return f.Date != "" || f.Since != "" || f.Until != "" || f.TZ != "" ||
		len(f.Tasks)+len(f.Outcomes)+len(f.Clients)+len(f.Sessions) > 0 || f.All || f.JSON
}

// timeRange reads --tz, --date, --since and --until. With none of the last
// three the range is today in the display zone.
func (f LogFlags) timeRange() (logRange, error) {
	rng := logRange{loc: time.Local, zone: "local"}
	switch {
	case f.TZ == "" || strings.EqualFold(f.TZ, "local"):
	case strings.EqualFold(f.TZ, "UTC"):
		rng.loc, rng.zone = time.UTC, "UTC"
	default:
		loc, err := time.LoadLocation(f.TZ)
		if err != nil {
			return logRange{}, usagef("--tz: unknown time zone %q (an IANA name such as Europe/Kyiv, UTC, or local)", f.TZ)
		}
		rng.loc, rng.zone = loc, f.TZ
	}
	now := time.Now
	if f.now != nil {
		now = f.now
	}
	rng.now = now().In(rng.loc)

	if f.Date != "" && (f.Since != "" || f.Until != "") {
		return logRange{}, usagef("--date cannot be combined with --since or --until")
	}
	if f.Since == "" && f.Until == "" {
		day := rng.now
		if f.Date != "" {
			var err error
			if day, err = time.ParseInLocation(logDayLayout, f.Date, rng.loc); err != nil {
				return logRange{}, usagef("--date: %q is not a day (YYYY-MM-DD)", f.Date)
			}
		}
		rng.since = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, rng.loc)
		rng.until = rng.since.AddDate(0, 0, 1)
		rng.oneDay = true
		return rng, nil
	}
	var err error
	if f.Since != "" {
		if rng.since, err = parseWhen("--since", f.Since, rng, false); err != nil {
			return logRange{}, err
		}
	}
	if f.Until != "" {
		if rng.until, err = parseWhen("--until", f.Until, rng, true); err != nil {
			return logRange{}, err
		}
	}
	if !rng.since.IsZero() && !rng.until.IsZero() && rng.since.After(rng.until) {
		return logRange{}, usagef("--since is after --until")
	}
	return rng, nil
}

// parseWhen reads one bound of a range: a day, an RFC 3339 time, or a
// duration counted back from now. A day given as the end includes that day.
func parseWhen(flag, value string, rng logRange, end bool) (time.Time, error) {
	if day, err := time.ParseInLocation(logDayLayout, value, rng.loc); err == nil {
		if end {
			day = day.AddDate(0, 0, 1)
		}
		return day, nil
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at, nil
	}
	if ago, ok := parseAgo(value); ok {
		return rng.now.Add(-ago), nil
	}
	return time.Time{}, usagef("%s: %q is not a day (YYYY-MM-DD), a time (RFC 3339) or a duration (such as 2h or 7d)", flag, value)
}

// parseAgo reads a duration that is not negative. It accepts Go's units and
// a whole number of days written with d, which Go's durations lack.
func parseAgo(value string) (time.Duration, bool) {
	if days, found := strings.CutSuffix(value, "d"); found {
		n, err := strconv.Atoi(days)
		// More days than a duration holds would wrap to another time.
		if err != nil || n < 0 || n > maxAgoDays {
			return 0, false
		}
		return time.Duration(n) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// filter turns the list flags and the range into the filter a listing uses.
func (f LogFlags) filter(rng logRange) (audit.Filter, error) {
	filter := audit.Filter{Since: rng.since, Until: rng.until, All: f.All}
	var err error
	if filter.Tasks, err = listValues("--task", f.Tasks); err != nil {
		return audit.Filter{}, err
	}
	if filter.Outcomes, err = listValues("--outcome", f.Outcomes); err != nil {
		return audit.Filter{}, err
	}
	for _, outcome := range filter.Outcomes {
		if !slices.Contains(audit.Outcomes, outcome) {
			return audit.Filter{}, usagef("--outcome: unknown outcome %q (one of %s)", outcome, strings.Join(audit.Outcomes, ", "))
		}
	}
	if filter.Clients, err = listValues("--client", f.Clients); err != nil {
		return audit.Filter{}, err
	}
	if filter.Sessions, err = listValues("--session", f.Sessions); err != nil {
		return audit.Filter{}, err
	}
	return filter, nil
}

// listValues splits the values of a list flag on commas and trims them.
func listValues(flag string, values []string) ([]string, error) {
	var out []string
	for _, value := range values {
		for item := range strings.SplitSeq(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, usagef("%s: empty value", flag)
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// hasLogTask reports whether the Runefile defines a task named log, which
// the command now shadows.
func hasLogTask(runefile string) bool { return hasTask(runefile, "log") }

// hasTask reports whether the Runefile defines a task of that name, in its
// own text or in a file it imports. A Runefile that cannot be read, parsed
// or composed is not the caller's to report.
func hasTask(runefile, name string) bool {
	source, err := os.ReadFile(runefile)
	if err != nil {
		return false
	}
	file, _ := parser.Parse(runefile, string(source))
	if file == nil {
		return false
	}
	config.Compose(file, newSourceProvider(runefile, source))
	return slices.ContainsFunc(file.Tasks, func(t *ast.Task) bool { return t.Name == name })
}

func damageWarning(d audit.Damage) string {
	if d.Count == 1 {
		return fmt.Sprintf("warning: 1 record in %s could not be read and was skipped", d.File)
	}
	return fmt.Sprintf("warning: %d records in %s could not be read and were skipped", d.Count, d.File)
}

// printLogJSON prints one action per line and nothing else.
func printLogJSON(w io.Writer, actions []audit.Action) error {
	enc := json.NewEncoder(w)
	// The values keep their <, > and &, as the stored records do.
	enc.SetEscapeHTML(false)
	for _, a := range actions {
		if err := enc.Encode(a); err != nil {
			return &CommandFailure{Err: fmt.Errorf("write audit log: %w", err)}
		}
	}
	return nil
}

// zoneText names the display zone with its offset from UTC at at.
func (r logRange) zoneText(at time.Time) string {
	if r.loc == time.UTC {
		return "UTC"
	}
	_, offset := at.In(r.loc).Zone()
	sign := '+'
	if offset < 0 {
		sign, offset = '-', -offset
	}
	return fmt.Sprintf("%s, UTC%c%02d:%02d", r.zone, sign, offset/3600, offset%3600/60)
}

// header is the first line of a listing: what it covers, in which zone, for
// which Runefile.
func (r logRange) header(runefile string) string {
	if r.oneDay {
		return fmt.Sprintf("Agent actions on %s (%s) for %s", r.since.Format(logDayLayout), r.zoneText(r.since), runefile)
	}
	start, at := "the start of the log", r.now
	if !r.since.IsZero() {
		start, at = r.since.In(r.loc).Format(logBoundLayout), r.since
	}
	end := r.now
	if !r.until.IsZero() {
		end = r.until
	}
	return fmt.Sprintf("Agent actions from %s to %s (%s) for %s", start, end.In(r.loc).Format(logBoundLayout), r.zoneText(at), runefile)
}

// logRow is one action as a listing shows it: the columns of its line and
// the detail lines under it.
type logRow struct {
	cols    [7]string
	details []string
}

// printLogText prints the listing a person reads.
func printLogText(w io.Writer, actions []audit.Action, rng logRange, runefile string) {
	fmt.Fprintln(w, rng.header(runefile))
	fmt.Fprintln(w)
	if len(actions) == 0 {
		fmt.Fprintln(w, "No agent actions.")
		return
	}
	layout := logDateTimeLayout
	if rng.oneDay {
		layout = logTimeLayout
	}
	rows := make([]logRow, len(actions))
	var width [7]int
	for i, a := range actions {
		rows[i] = rowOf(a, rng.loc, layout)
		for c, text := range rows[i].cols {
			width[c] = max(width[c], utf8.RuneCountInString(text))
		}
	}
	indent := strings.Repeat(" ", width[0]+2)
	for _, row := range rows {
		var line strings.Builder
		for c, text := range row.cols {
			line.WriteString(text)
			// The last column is not padded: no line ends with spaces.
			if c < len(row.cols)-1 {
				line.WriteString(strings.Repeat(" ", width[c]-utf8.RuneCountInString(text)+2))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(line.String(), " "))
		for _, detail := range row.details {
			fmt.Fprintln(w, indent+detail)
		}
	}
}

// rowOf lays one action out: the time it started, how it ended, what it
// was, its exit code, how long it took, who asked, and its id.
func rowOf(a audit.Action, loc *time.Location, layout string) logRow {
	row := logRow{cols: [7]string{
		a.Start.In(loc).Format(layout), printableLine(a.Outcome), "", "-", "-", clientText(a.Client), printableLine(a.ID),
	}}
	if a.ExitCode != nil {
		row.cols[3] = fmt.Sprintf("exit %d", *a.ExitCode)
	}
	if a.DurationMs != nil {
		row.cols[4] = durationText(*a.DurationMs)
	}
	switch a.Type {
	case audit.TypeCall:
		row.cols[2] = strings.TrimSpace(printableLine(cmp.Or(a.Task, a.Tool)) + " " + logArgsText(a.Arguments))
	case audit.TypeAgentRun:
		row.cols[2] = "agent " + printableLine(a.Task)
	case audit.TypeAuthFailure:
		row.cols[1] = audit.TypeAuthFailure
		row.cols[2] = printableLine(a.Remote)
		if a.Suppressed > 0 {
			row.cols[2] += fmt.Sprintf(" (+%d not recorded)", a.Suppressed)
		}
	case audit.TypeRequest:
		row.cols[1] = audit.TypeRequest
		row.cols[2] = printableLine(a.Method)
	default:
		row.cols[1] = printableLine(a.Type)
	}

	if a.Error != nil && (a.Error.Kind != "" || a.Error.Message != "") {
		message, _, _ := strings.Cut(a.Error.Message, "\n")
		row.details = append(row.details, strings.TrimSuffix(printableLine(a.Error.Kind)+": "+cutLine(printableLine(message), maxLogErrorRunes), ": "))
	}
	var notes []string
	if a.Approval != nil {
		if a.Approval.Decision == mcpserver.AuditDecisionApproved {
			notes = append(notes, "approved at the client")
		} else {
			notes = append(notes, "approval "+printableLine(a.Approval.Decision))
		}
	}
	if len(a.Grants) > 0 {
		notes = append(notes, "granted by the operator: "+printableLine(strings.Join(a.Grants, ", ")))
	}
	if a.Plan != nil {
		digest := strings.TrimPrefix(a.Plan.Digest, "sha256:")
		note := fmt.Sprintf("plan %s sha256:%s", printableLine(a.Plan.ID), printableLine(digest[:min(len(digest), 12)]))
		if a.Plan.Role == mcpserver.AuditPlanStored {
			note += " stored"
		}
		notes = append(notes, note)
	}
	if len(notes) > 0 {
		row.details = append(row.details, strings.Join(notes, "; "))
	}
	// Only a call that ran a task: a refusal ran nothing, sandboxed or not.
	if a.Sandbox != nil && a.Sandbox.Mode == string(sandbox.ModeOff) && a.ExitCode != nil {
		row.details = append(row.details, "ran without a sandbox (the server was started with the sandbox off)")
	}
	if a.Parent != "" {
		row.details = append(row.details, "parent "+printableLine(a.Parent))
	}
	if a.Outcome == audit.OutcomeIncomplete {
		what := "call"
		if a.Type == audit.TypeAgentRun {
			what = "run"
		}
		row.details = append(row.details, fmt.Sprintf("no completion was recorded: the %s is still running or the server stopped", what))
	}
	return row
}

func clientText(c *audit.Client) string {
	if c == nil || (c.Name == "" && c.Version == "") {
		return "-"
	}
	return strings.TrimSpace(printableLine(c.Name) + " " + printableLine(c.Version))
}

// durationText writes a duration the way a person compares them: seconds
// with one decimal, then minutes and hours.
func durationText(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// logArgsText writes a call's arguments on one line: the ones the task ran
// with, or the supplied ones when it never bound any.
func logArgsText(args *audit.Arguments) string {
	if args == nil {
		return ""
	}
	raw := args.Effective
	if len(raw) == 0 {
		raw = args.Supplied
	}
	if len(raw) == 0 {
		return ""
	}
	return cutLine(printableLine(argPairs(raw)), maxLogArgsRunes)
}

// argPairs writes a JSON object as name=value pairs in the order it was
// stored. Anything else is written as it is.
func argPairs(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return compactJSON(raw)
	}
	var pairs []string
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return compactJSON(raw)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return compactJSON(raw)
		}
		pairs = append(pairs, fmt.Sprintf("%v=%s", name, compactJSON(value)))
	}
	return strings.Join(pairs, " ")
}

func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// cutLine cuts s to n characters and marks the cut.
func cutLine(s string, n int) string {
	text, cut := cutRunes(s, n)
	if cut {
		return text + "..."
	}
	return text
}
