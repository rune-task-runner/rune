package agentinit

import (
	"fmt"
	"slices"
	"strings"
)

// Report is what a run prints: what was done to each file, the client and
// its version, the outcome, and what is left to the person. The outcome is
// one of three, in the words of the setup outcome contract of spec 047; no
// other word describes a setup.
func Report(res *Result, dryRun bool) string {
	var b strings.Builder
	for _, f := range res.Files {
		fmt.Fprintf(&b, "%s: %s", f.Path, f.Status())
		if len(f.Changes) > 0 {
			fmt.Fprintf(&b, ": %s", strings.Join(f.Changes, "; "))
		}
		b.WriteByte('\n')
	}
	a := res.adapter
	if res.Version == "" {
		fmt.Fprintf(&b, "client: %s, no version found (checked: %s)\n", a.name, a.checkedText())
	} else {
		fmt.Fprintf(&b, "client: %s %s (checked: %s)\n", a.name, res.Version, a.checkedText())
	}

	out := res.Outcome
	steps := "steps"
	if len(out.Steps) == 1 {
		steps = "step"
	}
	fmt.Fprintf(&b, "outcome: %s, pending %d %s\n", outcomeText(out), len(out.Steps), steps)
	if out.Kind == RegisteredOnly {
		fmt.Fprintf(&b, "reason: %s\nremains: %s\n", out.Reason, out.Remains)
	}
	for _, step := range out.Steps {
		fmt.Fprintf(&b, "step: %s\n", step)
	}
	for _, note := range res.Notes {
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	writeManualCheck(&b, a)
	if dryRun {
		b.WriteString("dry run: nothing written\n")
	}
	return b.String()
}

func outcomeText(out Outcome) string {
	switch out.Kind {
	case RuneOnly:
		return "Rune-only"
	case RuneOnlyWithin:
		return "Rune-only within " + out.Scope
	}
	return "Registered only"
}

func writeManualCheck(b *strings.Builder, a adapter) {
	for _, line := range a.check {
		fmt.Fprintf(b, "check: %s\n", line)
	}
}

// CheckReport is what `--check` prints: the state of every owned entry, one
// summary line, and the manual check. It names no outcome, because a check
// does not read the client's version. complete is false when an entry is
// missing or different, or when the plan has a conflict that is not about
// an owned entry.
func CheckReport(res *Result) (text string, complete bool) {
	var b strings.Builder
	total, wrong := 0, 0
	for _, f := range res.Files {
		for _, e := range f.Entries {
			fmt.Fprintf(&b, "%s: %s: %s\n", f.Path, e.Name, e.State)
			total++
			if e.State != StatePresent {
				wrong++
			}
		}
	}
	// A conflict --force does not replace stops the run a count would name.
	blocked := slices.ContainsFunc(res.Conflicts, func(c Conflict) bool { return !c.Forceable })
	switch {
	case blocked:
		b.WriteString("incomplete: the setup has a conflict\n")
	case wrong > 0:
		// The run that completes the setup takes the options of the check.
		run := "rune agent init " + string(res.Client)
		if res.unpinned {
			run += " --unpinned"
		}
		fmt.Fprintf(&b, "incomplete: %d of %d entries are missing or different. Run: %s\n", wrong, total, run)
	default:
		fmt.Fprintf(&b, "complete: all %d entries are present\n", total)
	}
	writeManualCheck(&b, res.adapter)
	return b.String(), wrong == 0 && len(res.Conflicts) == 0
}

// Diff is the unified difference of every file a run would change.
func Diff(res *Result) string {
	var b strings.Builder
	for _, f := range res.Files {
		b.WriteString(unifiedDiff(f.Path, f.Old, f.New, f.Existed))
	}
	return b.String()
}
