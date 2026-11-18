package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rune-task-runner/rune/internal/audit"
	"github.com/rune-task-runner/rune/mcpserver"
)

// Every Rune MCP server records what agents ask of it in the audit log of
// its project (spec 052). This file joins the server's reports to the log:
// where the log is, how a report becomes a record, and what the operator is
// told when a record cannot be written.

const (
	// invocationVar carries the id of an agent's call to the processes of
	// its tasks, so that a rune one of them starts can name the call as the
	// parent of what it records.
	invocationVar = "RUNE_INVOCATION_ID"

	// auditTransportAgent is the transport recorded for the endpoint an
	// agent task opens for its own agent.
	auditTransportAgent = "agent-endpoint"

	maxAuditErrorBytes = 2048

	gitStateCommit      = "commit"
	gitStateUnborn      = "unborn"
	gitStateNone        = "none"
	gitStateUnavailable = "unavailable"
)

// auditBase is the directory the logs live under: the operator's choice, or
// the user's state directory. The operator's choice must be absolute: a
// server, `rune log` and a rune started by a task each have a working
// directory of their own, and a relative path would name a different
// directory in each.
func auditBase(opts Options) (string, error) {
	if opts.AuditDir != "" {
		if !filepath.IsAbs(opts.AuditDir) {
			return "", usagef("RUNE_AUDIT_DIR must be an absolute path, not %q", opts.AuditDir)
		}
		return opts.AuditDir, nil
	}
	return audit.DefaultBase(os.Getenv, runtime.GOOS, os.UserHomeDir)
}

// auditRetention reads RUNE_AUDIT_RETENTION_DAYS: empty is the default
// period, and 0 keeps every file.
func auditRetention(value string) (int, error) {
	if value == "" {
		return audit.DefaultRetentionDays, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 {
		return 0, fmt.Errorf("RUNE_AUDIT_RETENTION_DAYS: %q is not a whole number of days, zero or more", value)
	}
	return days, nil
}

// openAuditLog opens the audit log of the project whose Runefile has the
// resolved path runefile. outside are the directories a task of the project
// normally writes in: the log may not be inside them. Every failure is a
// usage error that starts with "audit log:", so a server that cannot record
// does not start.
func openAuditLog(opts Options, runefile string, warn func(error), outside ...string) (*audit.Log, error) {
	fail := func(err error) (*audit.Log, error) {
		return nil, &UsageError{Err: fmt.Errorf("audit log: %w", err)}
	}
	days, err := auditRetention(opts.AuditRetention)
	if err != nil {
		return fail(err)
	}
	base, err := auditBase(opts)
	if err != nil {
		return fail(err)
	}
	log, err := audit.Open(audit.Options{
		Base:          base,
		Runefile:      runefile,
		Outside:       outside,
		RetentionDays: days,
		Warn:          warn,
	})
	if err != nil {
		return fail(err)
	}
	return log, nil
}

// auditWarnings prints what the audit log cannot report through a call's
// result, on the operator's standard error. A record that could not be
// written is reported once until a write succeeds again, so a broken log
// does not fill the terminal with one line per request.
type auditWarnings struct {
	w     io.Writer // nil drops every line
	label string    // the styled word "warning"

	mu     sync.Mutex
	warned bool
}

func newAuditWarnings(w io.Writer, label string) *auditWarnings {
	return &auditWarnings{w: operatorStderr(w), label: label}
}

func (a *auditWarnings) line(text string) {
	if a.w != nil {
		fmt.Fprintln(a.w, text)
	}
}

// notWritten reports a record that could not be written, once.
func (a *auditWarnings) notWritten(err error, dir string) {
	a.mu.Lock()
	first := !a.warned
	a.warned = true
	a.mu.Unlock()
	if first {
		a.always(err, dir)
	}
}

// always reports a record that could not be written, every time: the
// completion of a call is reported for each call.
func (a *auditWarnings) always(err error, dir string) {
	a.line(fmt.Sprintf("%s: audit log: a record could not be written (%v); the log directory is %s", a.label, err, dir))
}

// wrote notes a successful write: the next failure is reported again.
func (a *auditWarnings) wrote() {
	a.mu.Lock()
	a.warned = false
	a.mu.Unlock()
}

// prune reports a day file that could not be removed. It is what the log
// is given as its Warn function.
func (a *auditWarnings) prune(err error) {
	a.line(fmt.Sprintf("%s: audit log: an old day file could not be removed (%v)", a.label, err))
}

// auditor implements mcpserver.Auditor on the audit log of a project.
type auditor struct {
	log  *audit.Log
	warn *auditWarnings
	// transport, when set, is recorded in place of "http": the endpoint of
	// an agent task is an HTTP server, and is not the operator's.
	transport string
	// parent is the id of the agent run whose endpoint this is, or "".
	parent string
}

var _ mcpserver.Auditor = (*auditor)(nil)

func (a *auditor) transportOf(s mcpserver.AuditSession) string {
	if a.transport != "" && s.Transport == "http" {
		return a.transport
	}
	return s.Transport
}

// CallStarted writes the start of a call and flushes it, so the start is on
// the disk before anything of the call runs.
func (a *auditor) CallStarted(_ context.Context, call mcpserver.AuditCall) error {
	rec := audit.Record{
		Type:      audit.TypeCall,
		Phase:     audit.PhaseStart,
		ID:        call.ID,
		Time:      call.Start.UTC(),
		Session:   call.Session.ID,
		Parent:    a.parent,
		Transport: a.transportOf(call.Session),
		Auth:      call.Session.Auth,
		Client:    auditClient(call.Session),
		Tool:      call.Tool,
		Task:      call.Task,
		Project:   auditProject(call.Identity),
		Sandbox:   &audit.Sandbox{},
	}
	if call.Supplied != nil {
		rec.Arguments = &audit.Arguments{Supplied: jsonText(call.Supplied)}
	}
	if err := a.log.Append(call.Start, rec, true); err != nil {
		text := fmt.Sprintf("audit log: this call was not run: the record of its start could not be written (%v). The log directory is %s. The operator must make it writable or set RUNE_AUDIT_DIR.", err, a.log.Dir())
		a.warn.line(text)
		return errors.New(text)
	}
	a.warn.wrote()
	return nil
}

// CallEnded writes the end of a call into the file of its start.
func (a *auditor) CallEnded(_ context.Context, call mcpserver.AuditCall, end mcpserver.AuditEnd) error {
	ms := end.Duration.Milliseconds()
	rec := audit.Record{
		Type:       audit.TypeCall,
		Phase:      audit.PhaseEnd,
		ID:         call.ID,
		Time:       end.Time.UTC(),
		DurationMs: &ms,
		Outcome:    end.Outcome,
		ExitCode:   end.ExitCode,
		Grants:     end.Grants,
	}
	if effective := effectiveJSON(end.Effective); effective != nil {
		rec.Arguments = &audit.Arguments{Effective: effective}
	}
	if end.ErrorKind != "" || end.Error != "" {
		rec.Error = &audit.Error{Kind: end.ErrorKind, Message: end.Error}
	}
	if end.Approval != nil {
		rec.Approval = &audit.Approval{Asked: end.Approval.Asked, Decision: end.Approval.Decision, Tasks: end.Approval.Tasks}
	}
	if end.Plan != nil {
		rec.Plan = &audit.Plan{Role: end.Plan.Role, ID: end.Plan.ID, Digest: digestText(end.Plan.Digest)}
	}
	if err := a.log.Append(call.Start, rec, true); err != nil {
		a.warn.always(err, a.log.Dir())
		// The text is a block of the tool result, written for the agent.
		text := fmt.Sprintf("[audit log] The completion of this call was not recorded (%v). The result above is the task's own. Do not retry the call because of this message.", err)
		return errors.New(text)
	}
	a.warn.wrote()
	return nil
}

// Request writes one record for a request that is not a tool call.
func (a *auditor) Request(_ context.Context, req mcpserver.AuditRequest) {
	ms := req.Duration.Milliseconds()
	rec := audit.Record{
		Type:       audit.TypeRequest,
		ID:         audit.NewID(),
		Time:       req.Time.UTC(),
		DurationMs: &ms,
		Outcome:    audit.OutcomeOK,
		Session:    req.Session.ID,
		Parent:     a.parent,
		Transport:  a.transportOf(req.Session),
		Auth:       req.Session.Auth,
		Client:     auditClient(req.Session),
		Method:     req.Method,
	}
	if req.Failed {
		rec.Outcome = audit.OutcomeError
		rec.Error = &audit.Error{Kind: mcpserver.AuditKindProtocol, Message: req.Error}
	}
	a.append(req.Time, rec)
}

// AuthFailed writes one record for a request refused for its token.
func (a *auditor) AuthFailed(_ context.Context, failure mcpserver.AuditAuthFailure) {
	a.append(failure.Time, audit.Record{
		Type:       audit.TypeAuthFailure,
		ID:         audit.NewID(),
		Time:       failure.Time.UTC(),
		Transport:  a.transportOf(mcpserver.AuditSession{Transport: "http"}),
		Remote:     failure.Remote,
		Suppressed: failure.Suppressed,
	})
}

// append writes a record nobody waits for. A failure is the operator's to
// see: the request it belongs to is served either way.
func (a *auditor) append(start time.Time, rec audit.Record) {
	if err := a.log.Append(start, rec, false); err != nil {
		a.warn.notWritten(err, a.log.Dir())
		return
	}
	a.warn.wrote()
}

func auditClient(s mcpserver.AuditSession) *audit.Client {
	if s.ClientName == "" && s.ClientVersion == "" {
		return nil
	}
	return &audit.Client{Name: s.ClientName, Version: s.ClientVersion}
}

// auditProject turns an identity into the project part of a record, or nil
// for an engine that gave none.
func auditProject(id mcpserver.AuditIdentity) *audit.Project {
	if id == (mcpserver.AuditIdentity{}) {
		return nil
	}
	p := &audit.Project{
		Runefile:         id.Runefile,
		DefinitionDigest: digestText(id.DefinitionDigest),
		ConfigDigest:     digestText(id.ConfigDigest),
	}
	if id.GitState != "" {
		p.Git = &audit.Git{State: id.GitState, Commit: id.GitCommit, Dirty: id.GitDirty}
	}
	return p
}

// digestText writes a hex SHA-256 as a record holds it.
func digestText(hexDigest string) string {
	if hexDigest == "" {
		return ""
	}
	return "sha256:" + hexDigest
}

// jsonText marshals v as a record holds it: <, > and & as written. The
// values come from the server's own redaction and always marshal.
func jsonText(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

// effectiveJSON writes the bound arguments as one object in declaration
// order: a name to its value, or to the list of a variadic's elements. An
// object is written by hand because a Go map would sort the names.
func effectiveJSON(args []mcpserver.AuditArg) json.RawMessage {
	if len(args) == 0 {
		return nil
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, arg := range args {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonText(arg.Name))
		b.WriteByte(':')
		switch {
		case arg.Variadic:
			// A variadic with no element is an empty list, not null.
			b.Write(jsonText(append([]string{}, arg.Values...)))
		case len(arg.Values) > 0:
			b.Write(jsonText(arg.Values[0]))
		default:
			b.Write(jsonText(""))
		}
	}
	b.WriteByte('}')
	return b.Bytes()
}

// gitState asks git for the state of the repository dir is in. It tells
// apart what the plan store's gitInfo folds into "empty": a directory that
// is no repository, a repository with no commit yet, and a git that could
// not be asked. A dirty flag git did not give is left nil, never false.
func gitState(ctx context.Context, dir string) (state, commit string, dirty *bool) {
	run := func(args ...string) (stdout, stderr string, err error) {
		ctx, cancel := context.WithTimeout(ctx, gitTimeout)
		defer cancel()
		// core.fsmonitor can name a program, and a status call would start it.
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)...)
		// The message that says "not a repository" is matched below.
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err = cmd.Run()
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return strings.TrimSpace(out.String()), errOut.String(), err
	}
	inside, stderr, err := run("rev-parse", "--is-inside-work-tree")
	var exit *exec.ExitError
	switch {
	case err == nil && inside == "true":
	case err == nil, errors.As(err, &exit) && strings.Contains(stderr, "not a git repository"):
		// Not in a work tree: a plain directory, or the inside of .git.
		return gitStateNone, "", nil
	default:
		// No git, a timeout, or a repository git refuses to read.
		return gitStateUnavailable, "", nil
	}
	state = gitStateCommit
	if commit, _, err = run("rev-parse", "--verify", "--quiet", "HEAD"); err != nil || commit == "" {
		if !errors.As(err, &exit) {
			return gitStateUnavailable, "", nil
		}
		state, commit = gitStateUnborn, ""
	}
	if status, _, err := run("status", "--porcelain"); err == nil {
		isDirty := status != ""
		dirty = &isDirty
	}
	return state, commit, dirty
}

// agentRun is the audit record of one run of an (agent) task: the parent of
// the calls its agent makes through the endpoint.
type agentRun struct {
	log   *audit.Log
	warn  *auditWarnings
	id    string
	start time.Time
}

// startAgentRun records the start of an agent run and returns the run with
// the auditor of its endpoint. When the run cannot be recorded it returns an
// error, and the caller opens no endpoint: an agent with no task tools does
// nothing through Rune that would need a record.
func (e *engine) startAgentRun(task string, adapter *mcpAdapter) (*agentRun, *auditor, error) {
	warn := newAuditWarnings(e.opts.Stderr, e.theme.Warning.Render("warning"))
	log, err := openAuditLog(e.opts, adapter.ident.runefile, warn.prune, e.root, e.workDir)
	if err != nil {
		return nil, nil, err
	}
	run := &agentRun{log: log, warn: warn, id: audit.NewID(), start: time.Now()}
	rec := audit.Record{
		Type:    audit.TypeAgentRun,
		Phase:   audit.PhaseStart,
		ID:      run.id,
		Time:    run.start.UTC(),
		Parent:  e.invocation,
		Task:    task,
		Project: auditProject(adapter.AuditIdentity(e.ctx)),
	}
	if err := log.Append(run.start, rec, true); err != nil {
		return nil, nil, fmt.Errorf("audit log: %w", err)
	}
	return run, &auditor{log: log, warn: warn, transport: auditTransportAgent, parent: run.id}, nil
}

// finish records how the agent ended. A failure to write it is a warning:
// the agent has already run.
func (r *agentRun) finish(ctx context.Context, runErr error, hide func(string) string) {
	now := time.Now()
	ms := now.Sub(r.start).Milliseconds()
	rec := audit.Record{
		Type:       audit.TypeAgentRun,
		Phase:      audit.PhaseEnd,
		ID:         r.id,
		Time:       now.UTC(),
		DurationMs: &ms,
		Outcome:    audit.OutcomeCompleted,
	}
	var exit *exec.ExitError
	switch {
	case runErr == nil:
		code := ExitSuccess
		rec.ExitCode = &code
	case ctx.Err() != nil:
		rec.Outcome = audit.OutcomeCancelled
	default:
		rec.Outcome = audit.OutcomeFailed
		if errors.As(runErr, &exit) && exit.ExitCode() > 0 {
			code := exit.ExitCode()
			rec.ExitCode = &code
		}
		text := runErr.Error()
		if hide != nil {
			text = hide(text)
		}
		if len(text) > maxAuditErrorBytes {
			// Cut at a character boundary, with the mark every cut string
			// of a record ends with.
			kept := strings.ToValidUTF8(text[:maxAuditErrorBytes], "")
			text = kept + fmt.Sprintf("[%d bytes cut]", len(text)-len(kept))
		}
		rec.Error = &audit.Error{Kind: mcpserver.AuditKindInternal, Message: text}
	}
	if err := r.log.Append(r.start, rec, true); err != nil {
		r.warn.always(err, r.log.Dir())
	}
}
