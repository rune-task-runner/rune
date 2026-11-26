package mcpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/mask"
)

// A server with Options.Audit reports every request it handles to an
// Auditor (spec 052). A tool call is reported before anything of it runs and
// again when it ends, so a call that never ended stays visible. Everything
// in this file is on that path, and nothing of it runs without an Auditor.

// The outcomes of a tool call.
const (
	AuditCompleted        = "completed"         // the task ran and exited 0
	AuditFailed           = "failed"            // the task ran and exited non-zero
	AuditRefused          = "refused"           // a policy or a missing approval stopped it
	AuditInvalidArguments = "invalid-arguments" // the arguments did not fit the task
	AuditUnknownTool      = "unknown-tool"      // no task has the name
	AuditCancelled        = "cancelled"         // the call was cancelled
	AuditTimedOut         = "timed-out"         // an approval or the call ran out of time
	AuditError            = "error"             // anything else that stopped it
)

// The kinds of error that stop a call before a task runs.
const (
	AuditKindProtocol   = "protocol"
	AuditKindValidation = "validation"
	AuditKindPolicy     = "policy"
	// AuditKindSandbox is recorded by an engine that refuses a call because
	// no sandbox can confine it (spec 053).
	AuditKindSandbox  = "sandbox"
	AuditKindApproval = "approval"
	AuditKindInternal = "internal"
)

// The roles of a plan in a call.
const (
	AuditPlanStored  = "stored"
	AuditPlanApplied = "applied"
)

// The decisions about a call that needed approval.
const (
	AuditDecisionApproved  = "approved"
	AuditDecisionDeclined  = "declined"
	AuditDecisionDismissed = "dismissed"
	AuditDecisionNotGiven  = "not-given"
	AuditDecisionTimedOut  = "timed-out"
	AuditDecisionNoAnswer  = "no-answer"
	AuditDecisionCancelled = "cancelled"
	AuditDecisionChanged   = "runefile-changed"
	AuditDecisionCannotAsk = "cannot-ask"
)

// The bounds of what an Auditor is handed. An agent chooses the size of its
// arguments, and a record must stay small whatever it sends.
const (
	maxAuditValueBytes = 1024
	maxAuditErrorBytes = 2048
	maxAuditNames      = 64
	maxAuditDepth      = 8

	// minHiddenStart is the shortest start of a secret-named value that is
	// taken for the value in a text that cut it. A shorter start is too
	// common to be the value.
	minHiddenStart = 8

	// maxAuthFailuresPerMinute bounds the reports of refused bearer tokens,
	// so a flood of bad requests cannot fill the operator's disk.
	maxAuthFailuresPerMinute = 10
)

// Auditor records what a server is asked to do. Every value it receives is
// already redacted and bounded. It is called from many goroutines.
type Auditor interface {
	// CallStarted records the start of a tool call. When it returns an
	// error the call is refused: nothing runs, and the error's text is the
	// tool result.
	CallStarted(ctx context.Context, call AuditCall) error
	// CallEnded records how the call ended. When it returns an error the
	// result of the call is unchanged, and one text block that carries the
	// error's text is added to it.
	CallEnded(ctx context.Context, call AuditCall, end AuditEnd) error
	// Request records a handled request that is not a tool call. A failure
	// to record it is the Auditor's to report: the client is served.
	Request(ctx context.Context, req AuditRequest)
	// AuthFailed records a request refused for its bearer token.
	AuthFailed(ctx context.Context, failure AuditAuthFailure)
}

// AuditSession is the session a request arrived on.
type AuditSession struct {
	// ID is derived from the server and the protocol session. It is never
	// the protocol's own session id, which a client presents as a handle.
	ID        string
	Transport string // "stdio" or "http"
	Auth      string // "process" or "bearer-token"
	// ClientName and ClientVersion are what the client reported in the
	// handshake. They are attribution, not a verified identity.
	ClientName    string
	ClientVersion string
}

// AuditCall is what is known about a tool call when it starts.
type AuditCall struct {
	ID       string    // unique invocation id, 16 hex digits
	Start    time.Time // carries the monotonic reading
	Session  AuditSession
	Tool     string // the name the call asked for
	Task     string // the task of that tool; "" for an unknown tool
	Supplied any    // decoded arguments, redacted and bounded; nil when none
	Identity AuditIdentity
}

// AuditEnd is how a tool call ended.
type AuditEnd struct {
	Time     time.Time
	Duration time.Duration
	Outcome  string
	// ExitCode is nil unless a run produced one. A refused call has none.
	ExitCode  *int
	ErrorKind string // "" when a task ran
	Error     string // redacted and bounded
	Effective []AuditArg
	Approval  *AuditApproval
	Grants    []string
	Plan      *AuditPlan
}

// AuditArg is one parameter and what the task was bound to.
type AuditArg struct {
	Name     string
	Values   []string
	Variadic bool
}

// AuditApproval is the decision about a call that needed approval.
type AuditApproval struct {
	Asked    bool // whether a request was sent to the client
	Decision string
	Tasks    []string // the tasks the request covered
}

// AuditPlan is the stored plan a call made or applied.
type AuditPlan struct{ Role, ID, Digest string }

// AuditRequest is a handled request that is not a tool call.
type AuditRequest struct {
	Time     time.Time
	Duration time.Duration
	Session  AuditSession
	Method   string
	Failed   bool
	Error    string // redacted and bounded; "" when it succeeded
}

// AuditAuthFailure is a request refused for its bearer token. Nothing the
// request asserted about itself is in it.
type AuditAuthFailure struct {
	Time       time.Time
	Remote     string
	Suppressed int // failures not reported since the last one
}

// AuditIdentity is what a call ran against.
type AuditIdentity struct {
	Runefile         string
	DefinitionDigest string // hex SHA-256
	ConfigDigest     string // hex SHA-256
	GitState         string // "commit", "unborn", "none" or "unavailable"
	GitCommit        string
	GitDirty         *bool // nil when git did not say
}

// AuditEngine is an optional Engine extension. The server asks the engine
// of the load a call runs against, so the answers belong to that load.
type AuditEngine interface {
	// AuditIdentity reports the project identity now.
	AuditIdentity(ctx context.Context) AuditIdentity
	// Redact masks the engine's secrets in s.
	Redact(s string) string
}

// CallAudit is the record of the call a context belongs to. An engine uses
// it to add what only it knows. Every method is safe on a nil receiver, so
// an engine need not check whether the server records.
type CallAudit struct {
	id      string
	session AuditSession
	redact  func(string) string

	mu sync.Mutex
	// named holds the values under secret-like names, as supplied and as
	// bound. A refusal quotes the value it refused, and the record of that
	// text must not hold one of these.
	named     []string
	unknown   bool
	kind      string
	errText   string
	exit      *int
	effective []AuditArg
	approval  *AuditApproval
	grants    []string
	plan      *AuditPlan
}

type (
	auditCallKey struct{}
	snapshotKey  struct{}
)

// AuditFromContext returns the record of the tool call ctx belongs to, or
// nil when the server records nothing.
func AuditFromContext(ctx context.Context) *CallAudit {
	c, _ := ctx.Value(auditCallKey{}).(*CallAudit)
	return c
}

// snapshotFrom returns the snapshot a recorded call was started against, or
// nil for a call that is not recorded.
func snapshotFrom(ctx context.Context) *snapshot {
	snap, _ := ctx.Value(snapshotKey{}).(*snapshot)
	return snap
}

// ID is the invocation id of the call, or "" when nothing is recorded.
func (c *CallAudit) ID() string {
	if c == nil {
		return ""
	}
	return c.id
}

// Session is the session the call arrived on, as it was recorded: the
// client's name and version are what it reported, not a verified identity.
// The zero value when nothing is recorded.
func (c *CallAudit) Session() AuditSession {
	if c == nil {
		return AuditSession{}
	}
	return c.session
}

// SetEffective records the arguments the task was bound to. The values must
// already be masked. A value of a secret-named parameter is replaced
// whatever it holds, and long values and long lists are cut.
func (c *CallAudit) SetEffective(args []AuditArg) {
	if c == nil {
		return
	}
	bounded := make([]AuditArg, len(args))
	var named []string
	for i, arg := range args {
		bounded[i] = AuditArg{Name: arg.Name, Variadic: arg.Variadic, Values: boundValues(arg)}
		if mask.SensitiveName(arg.Name) {
			// A default is a value of the parameter too.
			for _, v := range arg.Values {
				named = appendHidden(named, v)
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.effective = bounded
	c.named = append(c.named, named...)
}

func boundValues(arg AuditArg) []string {
	values := make([]string, 0, min(len(arg.Values), maxAuditNames+1))
	for i, v := range arg.Values {
		if i == maxAuditNames {
			values = append(values, fmt.Sprintf("[%d more]", len(arg.Values)-maxAuditNames))
			break
		}
		if mask.SensitiveName(arg.Name) {
			v = mask.Placeholder
		}
		values = append(values, cutValue(v, maxAuditValueBytes))
	}
	return values
}

// SetErrorKind records why the engine ran nothing: AuditKindValidation or
// AuditKindPolicy. The first kind recorded for a call stays.
func (c *CallAudit) SetErrorKind(kind string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kind == "" {
		c.kind = kind
	}
}

// SetPlan records the stored plan the call made (AuditPlanStored) or
// applied (AuditPlanApplied).
func (c *CallAudit) SetPlan(role, id, digest string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plan = &AuditPlan{Role: role, ID: id, Digest: digest}
}

// fail records an error result: its kind, unless one is already recorded,
// and its text.
func (c *CallAudit) fail(kind, text string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kind == "" {
		c.kind = kind
	}
	c.errText = c.errorText(text)
}

// errorText is an error text as a record may hold it: cleared of the values
// under secret-like names, then masked and cut. The values go first, whole,
// before the mask can change a part of one. The caller holds c.mu.
func (c *CallAudit) errorText(text string) string {
	for _, v := range c.named {
		text = hideValue(text, v)
	}
	return redactText(text, c.redact, maxAuditErrorBytes)
}

// ran records that a task ran and how it exited, with the grants that let
// it.
func (c *CallAudit) ran(exit int, grants []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exit = &exit
	c.grants = grants
}

// decided records the decision about a call that needed approval.
func (c *CallAudit) decided(asked bool, decision string, tasks []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.approval = &AuditApproval{Asked: asked, Decision: decision, Tasks: slices.Clone(tasks)}
}

// end reports how the call ended. callErr is the protocol error the chain
// returned, if any. A context that ended decides the outcome whatever else
// was recorded: the caller gave up, so nothing later counts as an answer.
func (c *CallAudit) end(ctx context.Context, start time.Time, callErr error) AuditEnd {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	e := AuditEnd{
		Time:      now,
		Duration:  now.Sub(start),
		ExitCode:  c.exit,
		ErrorKind: c.kind,
		Error:     c.errText,
		Effective: c.effective,
		Approval:  c.approval,
		Grants:    c.grants,
		Plan:      c.plan,
	}
	if callErr != nil && e.Error == "" {
		e.Error = c.errorText(callErr.Error())
	}
	switch {
	case c.exit != nil:
		e.ErrorKind = ""
		e.Outcome = AuditCompleted
		if *c.exit != 0 {
			e.Outcome = AuditFailed
		}
	case c.unknown:
		e.ErrorKind = AuditKindProtocol
		e.Outcome = AuditUnknownTool
	case c.kind == AuditKindValidation:
		e.Outcome = AuditInvalidArguments
	case c.kind == AuditKindPolicy, c.kind == AuditKindSandbox:
		e.Outcome = AuditRefused
	case c.kind == AuditKindApproval:
		e.Outcome = AuditRefused
		if c.approval != nil {
			switch c.approval.Decision {
			case AuditDecisionTimedOut:
				e.Outcome = AuditTimedOut
			case AuditDecisionCancelled:
				e.Outcome = AuditCancelled
			}
		}
	default:
		if e.ErrorKind == "" {
			e.ErrorKind = AuditKindInternal
			if callErr != nil {
				e.ErrorKind = AuditKindProtocol
			}
		}
		e.Outcome = AuditError
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		e.Outcome = AuditCancelled
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		e.Outcome = AuditTimedOut
	}
	return e
}

// newAuditID returns a new invocation or instance id: 16 hex digits.
func newAuditID() string {
	var b [8]byte
	// crypto/rand does not fail on the supported systems.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// sessionID derives the id a record carries for a session. The protocol's
// own id is a handle a client presents to resume a session, so it is not
// stored: a digest correlates equally well. A stdio session has no protocol
// id and is identified by the server alone.
func sessionID(instance, protocol string) string {
	sum := sha256.Sum256([]byte(instance + "\x00" + protocol))
	return hex.EncodeToString(sum[:8])
}

// redactorOf returns the engine's redactor, or one that changes nothing for
// an engine that has none.
func redactorOf(engine Engine) func(string) string {
	if ae, ok := engine.(AuditEngine); ok {
		return ae.Redact
	}
	return func(s string) string { return s }
}

// auditSession describes the session of req. The transport is read from the
// request: only one that came over HTTP carries headers.
func (s *Server) auditSession(req mcp.Request, redact func(string) string) AuditSession {
	sess := AuditSession{Transport: "stdio", Auth: "process"}
	if extra := req.GetExtra(); extra != nil && extra.Header != nil {
		sess.Transport, sess.Auth = "http", "bearer-token"
	}
	protocol := ""
	if ss, ok := req.GetSession().(*mcp.ServerSession); ok && ss != nil {
		protocol = ss.ID()
		if params := ss.InitializeParams(); params != nil && params.ClientInfo != nil {
			sess.ClientName = redactText(params.ClientInfo.Name, redact, maxAuditValueBytes)
			sess.ClientVersion = redactText(params.ClientInfo.Version, redact, maxAuditValueBytes)
		}
	}
	sess.ID = sessionID(s.instance, protocol)
	return sess
}

// auditCall reports a tool call around the rest of the chain. The start is
// reported before the SDK looks the tool up, so a call to a name no task
// has is recorded too. The snapshot loaded here travels with the context,
// so the handler runs against the load the record names.
func (s *Server) auditCall(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler) (mcp.Result, error) {
	snap := s.state.Load()
	redact := redactorOf(snap.engine)
	name, raw := callParams(req)
	rec := &CallAudit{id: newAuditID(), session: s.auditSession(req, redact), redact: redact, named: namedSecrets(raw)}
	call := AuditCall{
		ID:       rec.id,
		Start:    time.Now(),
		Session:  rec.session,
		Tool:     redactText(name, redact, maxAuditValueBytes),
		Supplied: redactSupplied(raw, redact),
	}
	if t, known := snap.tasks[name]; known {
		call.Task = t.Name
	} else {
		rec.unknown = true
	}
	if ae, ok := snap.engine.(AuditEngine); ok {
		call.Identity = ae.AuditIdentity(ctx)
	}
	if err := s.opts.Audit.CallStarted(ctx, call); err != nil {
		// Nothing has run and nothing will: a call that cannot be recorded
		// is not made.
		return errorResult(err.Error()), nil
	}
	ctx = context.WithValue(ctx, auditCallKey{}, rec)
	ctx = context.WithValue(ctx, snapshotKey{}, snap)
	res, err := next(ctx, method, req)
	if auditErr := s.opts.Audit.CallEnded(ctx, call, rec.end(ctx, call.Start, err)); auditErr != nil {
		// The result stays the task's own. Only a block is added, so the
		// failure is seen without turning a failure into a success or the
		// other way round.
		if result, ok := res.(*mcp.CallToolResult); ok && result != nil {
			result.Content = append(result.Content, &mcp.TextContent{Text: auditErr.Error()})
		}
	}
	return res, err
}

// callParams reads the tool name and the raw arguments of a tools/call.
func callParams(req mcp.Request) (name string, args json.RawMessage) {
	if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && params != nil {
		return params.Name, params.Arguments
	}
	return "", nil
}

// auditRequest reports a request that is not a tool call, after it was
// handled: the session of an initialize request has no client before that.
func (s *Server) auditRequest(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler) (mcp.Result, error) {
	start := time.Now()
	res, err := next(ctx, method, req)
	redact := redactorOf(s.state.Load().engine)
	report := AuditRequest{
		Time:     start,
		Duration: time.Since(start),
		Session:  s.auditSession(req, redact),
		Method:   method,
		Failed:   err != nil,
	}
	if err != nil {
		report.Error = redactText(err.Error(), redact, maxAuditErrorBytes)
	}
	s.opts.Audit.Request(ctx, report)
	return res, err
}

// grantsOf lists the destructive tasks a call to t can run that the
// operator granted: t itself first, then what it reaches.
func (s *Server) grantsOf(t TaskInfo) []string {
	var names []string
	if t.Destructive && s.granted[t.Name] {
		names = append(names, t.Name)
	}
	for _, name := range t.DestructiveReach {
		if name != t.Name && s.granted[name] && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// auditFail records why a call returns an error result and returns that
// result. kind is "" where the engine or the approver already recorded it.
func auditFail(ctx context.Context, kind, text string) *mcp.CallToolResult {
	AuditFromContext(ctx).fail(kind, text)
	return errorResult(text)
}

// redactText masks s and cuts it to limit bytes.
func redactText(s string, redact func(string) string, limit int) string {
	if redact != nil {
		s = redact(s)
	}
	return cutValue(s, limit)
}

// cutValue cuts s to limit bytes at a character boundary and says how many
// bytes it left out.
func cutValue(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	kept := cutBytes(s, limit)
	return kept + fmt.Sprintf("[%d bytes cut]", len(s)-len(kept))
}

// redactSupplied turns a call's raw arguments into what a record may hold:
// every string masked and cut, the value under a secret-like name replaced
// whatever it is, long objects and arrays counted past a limit, and deep
// nesting replaced by a mark. It returns nil when the call sent none.
func redactSupplied(raw json.RawMessage, redact func(string) string) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	// The spelling of a number is kept, as the handler keeps it.
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return "[not JSON]"
	}
	return redactValue(value, redact, 1)
}

func redactValue(value any, redact func(string) string, level int) any {
	switch v := value.(type) {
	case string:
		return redactText(v, redact, maxAuditValueBytes)
	case json.Number:
		// A secret can be all digits.
		if masked := redactText(v.String(), redact, maxAuditValueBytes); masked != v.String() {
			return masked
		}
		return v
	case []any:
		if level > maxAuditDepth {
			return "[nested]"
		}
		out := make([]any, 0, min(len(v), maxAuditNames+1))
		for i, element := range v {
			if i == maxAuditNames {
				out = append(out, fmt.Sprintf("[%d more]", len(v)-maxAuditNames))
				break
			}
			out = append(out, redactValue(element, redact, level+1))
		}
		return out
	case map[string]any:
		if level > maxAuditDepth {
			return "[nested]"
		}
		out := make(map[string]any, min(len(v), maxAuditNames+1))
		// Sorted, so the same call always lists the same names.
		for i, name := range slices.Sorted(maps.Keys(v)) {
			if i == maxAuditNames {
				out["[more]"] = len(v) - maxAuditNames
				break
			}
			shown := redactText(name, redact, maxAuditValueBytes)
			if mask.SensitiveName(name) {
				out[shown] = mask.Placeholder
				continue
			}
			out[shown] = redactValue(v[name], redact, level+1)
		}
		return out
	}
	// A boolean or null.
	return value
}

// namedSecrets lists the values a call supplied under secret-like names, at
// any depth below such a name, in the forms a text can quote them in.
func namedSecrets(raw json.RawMessage) []string {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&value) != nil {
		return nil
	}
	return collectNamed(value, false, nil)
}

func collectNamed(value any, secret bool, out []string) []string {
	switch v := value.(type) {
	case string:
		if secret {
			out = appendHidden(out, v)
		}
	case json.Number:
		if secret {
			out = appendHidden(out, v.String())
		}
	case []any:
		for _, element := range v {
			out = collectNamed(element, secret, out)
		}
	case map[string]any:
		for name, element := range v {
			out = collectNamed(element, secret || mask.SensitiveName(name), out)
		}
	}
	return out
}

// appendHidden adds v in the two forms a text holds it in: quoted with its
// escapes, as %q writes it, and as it is. The quoted form goes first: where
// it differs, the plain form matches only the start of it.
func appendHidden(out []string, v string) []string {
	if v == "" {
		return out
	}
	if quoted := strconv.Quote(v); quoted[1:len(quoted)-1] != v {
		out = append(out, quoted[1:len(quoted)-1])
	}
	return append(out, v)
}

// hideValue replaces v in text with the mask. A text can cut a long value,
// so a start of v of minHiddenStart bytes or more is replaced as far as it
// goes. A shorter v is replaced only whole. A v too short for the mask set
// to track is replaced only between quotes, which is how a refusal writes a
// value: replacing every "1" or "a" would leave no text to read.
func hideValue(text, v string) string {
	if v == "" {
		return text
	}
	if len(v) < mask.MinLen {
		return strings.ReplaceAll(text, `"`+v+`"`, `"`+mask.Placeholder+`"`)
	}
	start := v[:min(len(v), minHiddenStart)]
	var b strings.Builder
	for {
		i := strings.Index(text, start)
		if i < 0 {
			break
		}
		n := len(start)
		for i+n < len(text) && n < len(v) && text[i+n] == v[n] {
			n++
		}
		// The rest of a character that matched in part goes too, so no
		// broken character is left behind.
		for i+n < len(text) && !utf8.RuneStart(text[i+n]) {
			n++
		}
		b.WriteString(text[:i])
		b.WriteString(mask.Placeholder)
		text = text[i+n:]
	}
	b.WriteString(text)
	return b.String()
}

// authLimiter bounds the reports of refused bearer tokens to
// maxAuthFailuresPerMinute and counts the rest.
type authLimiter struct {
	mu         sync.Mutex
	window     time.Time
	reported   int
	suppressed int
}

// allow reports whether a failure at now may be reported, and how many were
// left out since the last report.
func (l *authLimiter) allow(now time.Time) (ok bool, suppressed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= time.Minute {
		l.window, l.reported = now, 0
	}
	if l.reported >= maxAuthFailuresPerMinute {
		l.suppressed++
		return false, 0
	}
	l.reported++
	suppressed, l.suppressed = l.suppressed, 0
	return true, suppressed
}
