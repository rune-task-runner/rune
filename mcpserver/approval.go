package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A call that can run a destructive task the operator has not granted is put
// to the person at the client, through MCP form elicitation, and runs once on
// an explicit yes (spec 049). Everything in this file is on that path.

const (
	// maxApprovalMessageRunes cuts a [confirm] message. It describes the
	// task and is not run, so cutting it hides nothing the run uses. An
	// argument value is never cut for that reason.
	maxApprovalMessageRunes = 500
	// maxApprovalBytes is the most a request holds. A call whose request
	// would be larger is refused, so that nothing is approved unseen.
	maxApprovalBytes = 8192

	reasonNoForm   = "the client does not support MCP form elicitation"
	reasonTooLarge = "the approval request would be too large to show"

	// A request shows this much of a plan's rendered summary: its head and
	// its tail, because a plan tool prints its totals last (spec 051).
	summaryHeadBytes = 3072
	summaryTailBytes = 1024
)

// outcome is how one approval request ended. Only outcomeApproved runs the
// call.
type outcome int

const (
	outcomeApproved  outcome = iota
	outcomeNotGiven          // accepted with approved false
	outcomeDeclined          // the person said no
	outcomeDismissed         // closed without a choice
	outcomeTimedOut          // no answer within the wait
	outcomeNoAnswer          // an answer Rune cannot read, or a client error
	outcomeCancelled         // the call itself ended
	outcomeChanged           // the server's tasks were replaced meanwhile
)

// decision is the word an audit record holds for how a request ended.
func (o outcome) decision() string {
	switch o {
	case outcomeApproved:
		return AuditDecisionApproved
	case outcomeNotGiven:
		return AuditDecisionNotGiven
	case outcomeDeclined:
		return AuditDecisionDeclined
	case outcomeDismissed:
		return AuditDecisionDismissed
	case outcomeTimedOut:
		return AuditDecisionTimedOut
	case outcomeCancelled:
		return AuditDecisionCancelled
	case outcomeChanged:
		return AuditDecisionChanged
	}
	return AuditDecisionNoAnswer
}

// approvalSchema is the form every request asks for: one required boolean
// that defaults to false, so submitting a form untouched approves nothing.
// Each call returns a new map.
func approvalSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"approved": map[string]any{
				"type":        "boolean",
				"title":       "Approve this call",
				"description": "Run this task call once.",
				"default":     false,
			},
		},
		"required": []string{"approved"},
	}
}

// formSupport reports whether the client of a session stated that it can
// show a form request.
func formSupport(session *mcp.ServerSession) bool {
	if session == nil {
		return false
	}
	return formCapability(session.InitializeParams())
}

// formCapability reads form support from what a client sent in initialize.
// Elicitation stated with no mode is form support: the 2025-06-18 revision
// has no modes, and 2025-11-25 defines the empty object that way. A client
// that states only the URL mode must not be sent a form.
func formCapability(params *mcp.InitializeParams) bool {
	if params == nil || params.Capabilities == nil || params.Capabilities.Elicitation == nil {
		return false
	}
	caps := params.Capabilities.Elicitation
	return caps.Form != nil || caps.URL == nil
}

// approvalMessage writes the text a person is shown for one call, and
// reports whether it is within maxApprovalBytes. names are the destructive
// tasks the approval would cover. They come from the server, not from req,
// so every one of them is on the page whatever the engine reports.
func approvalMessage(called string, names []string, req ApprovalRequest) (string, bool) {
	messages := make(map[string]string, len(req.Confirmations))
	for _, c := range req.Confirmations {
		messages[c.Task] = c.Message
	}
	var b strings.Builder
	b.WriteString("Approve this Rune task call?\n\nTask: ")
	b.WriteString(escapeLine(called))
	b.WriteByte('\n')
	if len(req.Args) > 0 {
		b.WriteString("Arguments:\n")
	}
	for _, arg := range req.Args {
		b.WriteString("- ")
		b.WriteString(escapeLine(arg.Name))
		b.WriteByte(':')
		if len(arg.Values) == 0 {
			b.WriteString(" (none)")
		}
		for _, value := range arg.Values {
			// Quoting never shortens a value, so one that cannot fit is not
			// quoted: the agent chooses its size.
			if b.Len()+len(value) > maxApprovalBytes {
				return "", false
			}
			// Quoted, so a value the agent filled with line breaks stays
			// on its line and cannot pass for part of the request.
			b.WriteByte(' ')
			b.WriteString(strconv.Quote(value))
		}
		b.WriteByte('\n')
	}
	if plan := req.Plan; plan != nil {
		fmt.Fprintf(&b, "Plan: %s made by \"%s\" at %s\n", escapeLine(plan.ID), escapeLine(plan.Task), escapeLine(plan.Created))
		fmt.Fprintf(&b, "Artifact: sha256:%s (%d bytes)\n", escapeLine(plan.Digest), plan.Size)
		for _, note := range plan.Notes {
			b.WriteString("Note: ")
			b.WriteString(escapeLine(note))
			b.WriteByte('\n')
		}
		b.WriteString("Summary:\n")
		b.WriteString(SummaryLines(plan.Summary, summaryHeadBytes, summaryTailBytes))
		b.WriteByte('\n')
	}
	b.WriteString("Destructive tasks it can run:\n")
	for _, name := range names {
		b.WriteString("- ")
		b.WriteString(escapeLine(name))
		b.WriteString(": ")
		b.WriteString(confirmationLine(name, messages[name]))
		b.WriteByte('\n')
	}
	if req.Plan != nil {
		b.WriteString("\nApproving applies this plan once.")
	} else {
		b.WriteString("\nApproving runs this call once.")
	}
	return b.String(), b.Len() <= maxApprovalBytes
}

// SummaryLines renders a plan's summary for a person: each line indented by
// two spaces with characters that are not printable escaped, and "  (empty)"
// for a summary with no text. A rendering longer than head+tail bytes shows
// the lines that fit in head, a mark with the number of rendered bytes left
// out, and the lines that fit in tail. A line longer than its share is cut
// inside the line. The result does not end with a line break.
func SummaryLines(summary string, head, tail int) string {
	if strings.TrimSpace(summary) == "" {
		return "  (empty)"
	}
	var lines []string
	total := 0
	for line := range strings.SplitSeq(strings.TrimRight(summary, "\r\n"), "\n") {
		rendered := "  " + escapeLine(strings.TrimSuffix(line, "\r"))
		lines = append(lines, rendered)
		total += len(rendered) + 1
	}
	if total-1 <= head+tail {
		return strings.Join(lines, "\n")
	}

	var shown []string
	used, first := 0, 0
	for ; first < len(lines) && used+len(lines[first])+1 <= head; first++ {
		shown = append(shown, lines[first])
		used += len(lines[first]) + 1
	}
	if first == 0 && head > 0 {
		cut := cutBytes(lines[0], head)
		shown = append(shown, cut)
		used += len(cut) + 1
		first = 1
	}

	var end []string
	endUsed := 0
	last := len(lines)
	for last > first && endUsed+len(lines[last-1])+1 <= tail {
		last--
		end = append(end, lines[last])
		endUsed += len(lines[last]) + 1
	}
	if len(end) == 0 && last > first && tail > 2 {
		cut := "  " + cutBytesEnd(lines[last-1], tail-2)
		end = append(end, cut)
		endUsed = len(cut) + 1
	}
	slices.Reverse(end)
	shown = append(shown, fmt.Sprintf("  [summary cut: %d bytes not shown]", total-used-endUsed))
	return strings.Join(append(shown, end...), "\n")
}

// cutBytes returns the longest prefix of s within n bytes that ends at a
// character boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// cutBytesEnd returns the longest suffix of s within n bytes that starts at
// a character boundary.
func cutBytesEnd(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// confirmationLine is what a request shows for one destructive task: its
// author's message on one line, or the question the command line asks when
// the task has none.
func confirmationLine(task, message string) string {
	if message == "" {
		return fmt.Sprintf("Run %q?", task)
	}
	text, cut := cutRunes(message, maxApprovalMessageRunes)
	text = escapeLine(text)
	if cut {
		text += " [truncated]"
	}
	return text
}

// escapeLine writes every character that is not printable as its Go escape,
// so the text stays on one line. That covers the direction overrides, which
// are not control characters but can reorder what a person reads.
func escapeLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strconv.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteRune(r)
		b.WriteString(quoted[1 : len(quoted)-1])
	}
	return b.String()
}

// cutRunes returns the first n characters of s and whether any were left
// out.
func cutRunes(s string, n int) (string, bool) {
	count := 0
	for i := range s {
		if count == n {
			return s[:i], true
		}
		count++
	}
	return s, false
}

// readAnswer reads a client's reply. Only an accept whose approved field is
// the boolean true is an approval. The library checks an accepted reply
// against the schema before this sees it, and this does not rely on that.
func readAnswer(res *mcp.ElicitResult) outcome {
	if res == nil {
		return outcomeNoAnswer
	}
	switch res.Action {
	case "accept":
		approved, isBool := res.Content["approved"].(bool)
		switch {
		case !isBool:
			return outcomeNoAnswer
		case approved:
			return outcomeApproved
		}
		return outcomeNotGiven
	case "decline":
		return outcomeDeclined
	case "cancel":
		return outcomeDismissed
	}
	return outcomeNoAnswer
}

// settled returns o unless the call or the wait is over, which wins: the
// library waits on the reply and on the context at once, so an approval that
// arrives together with the deadline could otherwise count (spec 049
// FR-018). It also tells a client error from the two ends Rune causes.
func settled(call, wait context.Context, o outcome) outcome {
	switch {
	case call.Err() != nil:
		return outcomeCancelled
	case wait.Err() != nil:
		return outcomeTimedOut
	}
	return o
}

// approvalTimeout is how long a request waits for an answer.
func (s *Server) approvalTimeout() time.Duration {
	if s.opts.ApprovalTimeout <= 0 {
		return DefaultApprovalTimeout
	}
	return s.opts.ApprovalTimeout
}

// approver returns what an ApprovalEngine calls to ask the person about one
// call to t: it writes the request, sends it on the calling session, waits,
// and returns nil only for an approval of names given while snap was still
// the server's state. Every other end is an error whose text is the tool
// result. Nothing is kept: a call sent again asks again.
func (s *Server) approver(session *mcp.ServerSession, snap *snapshot, t TaskInfo, names []string) func(context.Context, ApprovalRequest) error {
	applies := t.AppliesPlan != ""
	return func(ctx context.Context, req ApprovalRequest) error {
		// What the audit record says about the decision. A request that was
		// never sent is recorded as one that could not be.
		rec := AuditFromContext(ctx)
		cannotAsk := func(text string) error {
			rec.decided(false, AuditDecisionCannotAsk, names)
			rec.SetErrorKind(AuditKindPolicy)
			return errors.New(text)
		}
		if applies {
			// The engine has checked the call by now. Only here is it known
			// that the one thing missing is somebody to ask.
			switch {
			case !s.opts.AskApproval:
				return cannotAsk(s.planNobody(t.Name))
			case !formSupport(session):
				return cannotAsk(s.planCannotAsk(t.Name, reasonNoForm))
			case req.Plan == nil:
				// A request without a plan would ask the person to approve
				// a digest they are not shown.
				return fmt.Errorf("task %q was not run: the engine reported no plan to approve", t.Name)
			}
		}
		message, fits := approvalMessage(t.Name, names, req)
		if !fits {
			if applies {
				return cannotAsk(s.planCannotAsk(t.Name, reasonTooLarge))
			}
			return cannotAsk(s.refusal(t) + s.cannotAsk(t.Name, reasonTooLarge))
		}
		wait, cancel := context.WithTimeout(ctx, s.approvalTimeout())
		defer cancel()
		res, err := session.Elicit(wait, &mcp.ElicitParams{
			Mode:            "form",
			Message:         message,
			RequestedSchema: approvalSchema(),
		})
		// The client's own error text is not passed on: it is not Rune's,
		// and the result goes to the agent.
		o := outcomeNoAnswer
		if err == nil {
			o = readAnswer(res)
		}
		if err != nil || o == outcomeApproved {
			o = settled(ctx, wait, o)
		}
		// The person approved what snap serves. After a Replace the same
		// name may run something else.
		if o == outcomeApproved && s.state.Load() != snap {
			o = outcomeChanged
		}
		rec.decided(true, o.decision(), names)
		if o == outcomeApproved {
			return nil
		}
		rec.SetErrorKind(AuditKindApproval)
		return errors.New(s.outcomeText(o, t.Name, names, !applies))
	}
}

// outcomeText is the tool result for a request that did not end in an
// approval. It adds the other ways to run the task, because a "no" is not
// always a person's: clients run with nobody at them answer by themselves
// (Claude Code dismisses, Codex declines), and their operator would
// otherwise be left with no way forward. A cancelled call has no reader, and
// a changed Runefile has its own way forward.
//
// grantHelps is false for a task that applies a plan, which no grant can run:
// the remedy then names the command only.
func (s *Server) outcomeText(o outcome, called string, names []string, grantHelps bool) string {
	var reason string
	switch o {
	case outcomeNotGiven:
		reason = "approval was not given"
	case outcomeDeclined:
		reason = "the request was declined"
	case outcomeDismissed:
		reason = "the request was dismissed"
	case outcomeTimedOut:
		reason = fmt.Sprintf("no answer arrived within %s", s.approvalTimeout())
	case outcomeCancelled:
		return fmt.Sprintf("task %q was not approved: the call was cancelled. Nothing ran.", called)
	case outcomeChanged:
		return fmt.Sprintf("task %q was not approved: the Runefile changed while the request was pending; call again. Nothing ran.", called)
	default:
		reason = "the client returned no valid answer"
	}
	return fmt.Sprintf("task %q was not approved: %s. Nothing ran.", called, reason) + s.remedy(called, names, grantHelps)
}

// remedy names the ways to run a call without a request: the grant flag with
// the tasks to add, and the command for the called task. Each is left out
// where the server has none. It names the operator, because the agent reads
// it too and must not take it for an instruction to itself. Without
// grantHelps the grant flag is left out too.
func (s *Server) remedy(called string, names []string, grantHelps bool) string {
	var ways []string
	if grantHelps && s.opts.AllowDestructiveFlag != "" {
		ways = append(ways, fmt.Sprintf("add %s to %s and restart the server", strings.Join(names, ","), s.opts.AllowDestructiveFlag))
	}
	if s.opts.RunCommand != "" {
		ways = append(ways, "run "+s.runHint(called)+" themselves")
	}
	if len(ways) == 0 {
		return ""
	}
	return " To run it without a request, the operator can " + strings.Join(ways, ", or ") + "."
}

// runHint is the command for the called task, between quotes. The command
// is the operator's and is written as it is: it can hold a Windows path,
// whose backslashes a Go-quoted string would double.
func (s *Server) runHint(called string) string {
	return `"` + s.opts.RunCommand + " " + escapeLine(called) + `"`
}

// cannotAsk is what follows the refusal of a call the person could not be
// asked about: the command to run for the called task, when the server has
// one, and the reason.
func (s *Server) cannotAsk(called, reason string) string {
	var b strings.Builder
	b.WriteByte('.')
	if s.opts.RunCommand != "" {
		// "Or" continues the flag sentence of the refusal. A server with no
		// flag offers nothing before it.
		lead := "Or the operator"
		if s.opts.AllowDestructiveFlag == "" {
			lead = "The operator"
		}
		fmt.Fprintf(&b, " %s can run %s themselves.", lead, s.runHint(called))
	}
	fmt.Fprintf(&b, " Rune could not ask for approval here: %s.", reason)
	return b.String()
}
