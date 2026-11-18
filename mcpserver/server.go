// Package mcpserver exposes a Runefile's non-private tasks to AI agents and IDEs
// as Model Context Protocol tools. Tools are run through the SAME engine the CLI
// uses (FR-026); secrets never appear in any tool name, description, schema, or
// result (FR-029, Principle VII). It is a public package so it can be embedded.
package mcpserver

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Result is the outcome of running a task on behalf of an agent.
type Result struct {
	// Stdout and Stderr must already be masked by the engine: this package
	// only caps them, and every tool's output schema tells clients they hold
	// no secret (spec 044).
	Stdout   string
	Stderr   string
	ExitCode int
	// FixSuggestion is the captured output of the task's || failure hooks
	// (spec 022). Like Options.Instructions, it must already be masked and
	// size-capped by the engine; this package never processes it, only
	// renders it as a delimited section after the exit marker and returns it
	// as the fixSuggestion field of the structured content (spec 044). Empty
	// means no section and no field.
	FixSuggestion string
}

// ParamInfo describes a task parameter for input-schema derivation.
type ParamInfo struct {
	Name     string
	Required bool
	Variadic bool
	// Kind is the declared constraint kind: "", "string", "number", "boolean",
	// "path", or "enum". "" means unannotated and produces the pre-023 schema
	// byte-for-byte (spec 023).
	Kind string
	// Enum is the closed value list when Kind is "enum", in source order.
	Enum []string
	// Description is the author's [param-doc] text for this parameter.
	Description string
	// AgentValues, when not empty, lists the only values an agent may pass for
	// this parameter. The tool's schema enumerates them in place of Enum, and
	// the tool's description states them. The server does not refuse a call by
	// them: the Engine must, because only it knows the value a default gives.
	// Not for a variadic parameter.
	AgentValues []string
}

// TaskInfo is the agent-facing view of a task. Private tasks are never included.
// Params lists the only argument names a call may use: a call naming any other
// is refused before the engine runs (spec 032).
type TaskInfo struct {
	Name   string // display name (namespaced mod::task becomes mod__task as a tool)
	Doc    string
	Params []ParamInfo
	// Destructive marks the task itself destructive. A call is refused unless
	// the task is granted or, with Options.AskApproval, the person at the
	// client approves the call. The tool advertises DestructiveHint.
	Destructive bool
	// DestructiveReach names the destructive tasks a run of this task can
	// reach through its dependencies and hooks: each once, not the task
	// itself, in the order a run first reaches them. A call is refused unless
	// every one is granted or, with Options.AskApproval, named in an approval
	// the person gives for the call. A non-empty list sets DestructiveHint. Nil
	// leaves the engine alone to decline such a task when a run reaches it.
	DestructiveReach []string
	// Network says the task, or anything a run of it can reach, uses the
	// network. The tool advertises OpenWorldHint.
	Network bool
	// ReadOnly says the task declares itself read-only. The tool advertises
	// ReadOnlyHint. A descriptor that leaves it false makes no read-only claim.
	ReadOnly bool
	// Returns is the task's [returns] outcome description (spec 023 US3),
	// composed into the tool description as a "Returns:" trailer so agents can
	// verify results against it. Like Doc and Instructions, it must already be
	// masked and size-capped by the engine; this package never processes it.
	Returns string
	// AppliesPlan, when not empty, names the task whose stored plan this task
	// applies (spec 051). The tool is advertised as destructive and says so in
	// its description. Every call is put to the person at the client through
	// the ApprovalEngine, whatever Options.AllowDestructive names: a grant
	// does not approve a plan. Without Options.AskApproval, an ApprovalEngine
	// and a client that can show a form, the call is refused.
	AppliesPlan string
}

// check validates one string value against the parameter's advertised
// constraint — the server-side half of spec 023 FR-005, run before the engine
// sees the call (the engine re-validates at bind time as defense in depth).
// The message carries the parameter, the quoted value, and the accepted set
// (FR-006).
func (p ParamInfo) check(value string) error {
	switch p.Kind {
	case "number":
		if !validNumber(value) {
			return fmt.Errorf("parameter %q: invalid value %q (expected a number)", p.Name, value)
		}
	case "boolean":
		if value != "true" && value != "false" {
			return fmt.Errorf(`parameter %q: invalid value %q (expected "true" or "false")`, p.Name, value)
		}
	case "path":
		if value == "" {
			return fmt.Errorf("parameter %q: invalid value %q (expected a non-empty path)", p.Name, value)
		}
	case "enum":
		for _, v := range p.Enum {
			if value == v {
				return nil
			}
		}
		quoted := make([]string, len(p.Enum))
		for i, v := range p.Enum {
			quoted[i] = strconv.Quote(v)
		}
		return fmt.Errorf("parameter %q: invalid value %q (allowed values: %s)",
			p.Name, value, strings.Join(quoted, ", "))
	}
	return nil
}

// validNumber mirrors ast.IsDecimalNumber — integers and decimals with an
// optional exponent, rejecting the extra spellings strconv.ParseFloat
// tolerates (NaN, ±Inf, hex floats, digit-separating underscores). The two
// must stay identical so no value is accepted on one transport and rejected
// on another; TestParamCheckMatchesASTConstraint pins them together.
func validNumber(value string) bool {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return !strings.ContainsAny(value, "xXpP_")
}

// Engine is the host the MCP server runs tasks against. The CLI implements it.
type Engine interface {
	// Tasks returns the exposable (non-private) tasks with their metadata.
	Tasks() []TaskInfo
	// Call runs a task by display name with string arguments, capturing output.
	Call(ctx context.Context, name string, args map[string]string) (Result, error)
}

// ListEngine is an optional Engine extension. When the engine implements it,
// array arguments reach it with their elements intact (spec 030); a plain
// string for a variadic is one element, or none when empty. args still holds
// every value, arrays joined by single spaces, exactly as Call receives it.
type ListEngine interface {
	CallLists(ctx context.Context, name string, args map[string]string, lists map[string][]string) (Result, error)
}

// ApprovalEngine is an optional Engine extension. An engine that implements
// it can run a call a person approved through the client (spec 049).
type ApprovalEngine interface {
	// CallApproved prepares the call as CallLists would, without running
	// anything, and calls approve with what the person must see. When
	// approve returns an error, CallApproved runs nothing and returns that
	// error unchanged. When it returns nil, CallApproved runs the call once,
	// with the [confirm] gate of exactly the tasks named in tasks passed for
	// this run only. It must not approve any other task, and must not keep
	// the approval after it returns.
	CallApproved(ctx context.Context, name string, args map[string]string,
		lists map[string][]string, tasks []string,
		approve func(context.Context, ApprovalRequest) error) (Result, error)
}

// ApprovalRequest is what an engine reports about a call that needs
// approval. Every string must already be masked: this package escapes and
// cuts them but knows no secret.
type ApprovalRequest struct {
	// Args are the called task's parameters with the values it will run
	// with, defaults included, in declaration order.
	Args []ApprovalArg
	// Confirmations give the confirmation message of each task named in
	// CallApproved's tasks. A task with no entry is shown with a default
	// question.
	Confirmations []Confirmation
	// Plan describes the stored plan the call would apply. The engine of a
	// task with AppliesPlan sets it. Nil for every other call.
	Plan *PlanApproval
}

// PlanApproval is what a person is shown about a plan (spec 051). Every
// string must already be masked: this package escapes and cuts them but knows
// no secret.
type PlanApproval struct {
	ID      string // the plan's id
	Task    string // the task that made it
	Created string // RFC 3339, UTC
	Digest  string // hex SHA-256 of the artifact
	Size    int64  // bytes of the artifact
	Summary string // the stored summary, in full; the server cuts it
	// Notes are shown above the summary, one line each, after "Note: ".
	Notes []string
}

// ApprovalArg is one parameter and what it is bound to: one value, or a
// variadic's elements, which may be none.
type ApprovalArg struct {
	Name   string
	Values []string
}

// Confirmation is one destructive task and the message its author wrote.
type Confirmation struct{ Task, Message string }

// DefaultApprovalTimeout is how long an approval request waits when
// Options.ApprovalTimeout is not set.
const DefaultApprovalTimeout = 5 * time.Minute

// Options configures authorization for the server.
type Options struct {
	// AllowDestructive names the destructive ([confirm]) tasks that may be
	// called or run. A call is refused before the engine runs when the task
	// itself, or any task in its DestructiveReach, is destructive and not named
	// here, unless AskApproval puts the call to the person at the client.
	// There is no value that permits all of them. The Engine must still
	// approve the confirmation of exactly these tasks, wherever a run reaches
	// them; the rune CLI adapter does.
	AllowDestructive []string
	// AllowDestructiveFlag is the command-line flag that sets AllowDestructive,
	// such as "--allow-destructive". When set, a destructive refusal tells the
	// caller to add the task to it. Leave it empty where no grant is possible,
	// such as the endpoint of a rune agent task: the refusal then offers no
	// remedy.
	AllowDestructiveFlag string
	// AskApproval asks the person at the client to approve a call that can run
	// a destructive task AllowDestructive does not name, when the client stated
	// MCP form elicitation and the Engine is an ApprovalEngine. The call runs
	// once only on an explicit yes. Without both, and when this is false, such
	// a call is refused.
	AskApproval bool
	// ApprovalTimeout is how long an approval request waits for an answer.
	// Zero or less means DefaultApprovalTimeout.
	ApprovalTimeout time.Duration
	// RunCommand is the command a person runs a task of this server with,
	// such as "rune" or "rune -f /repo/Runefile". When set, the refusal given
	// to a client that cannot be asked, and the result of a request that was
	// not approved, say to run it with the called task's name.
	RunCommand string
	// AllowList, when non-empty, exposes only these tasks as tools. A task
	// outside it is not listed and cannot be called. It never adds a task the
	// Engine does not offer.
	AllowList []string
	// DenyList names tasks that are not exposed as tools, whether or not
	// AllowList names them.
	DenyList []string
	// OutputLimit caps the bytes of Result.Stdout and of Result.Stderr that a
	// tool result carries, marker included. A result carries each stream
	// twice, in its text and in its structured content (spec 044), and the
	// cap holds for each. Zero or less means DefaultOutputLimit. Other values
	// are raised to MinOutputLimit or lowered to MaxOutputLimit.
	OutputLimit int
	// Version reported to clients.
	Version string
	// Instructions, when non-empty, is delivered to clients as the MCP
	// server instructions in the initialize result. Rune uses it for the
	// [context] hook's project-health text (spec 021 FR-002). It must
	// already be masked; this package never processes it.
	Instructions string
	// Reloadable says the server's tasks may be replaced with Replace. The
	// server then states the tools capability with listChanged from the
	// start, even with no tool.
	Reloadable bool
	// Audit, when set, is told about every request the server handles. A
	// tool call is reported before anything of it runs, and again when it
	// ends (spec 052). Nil records nothing.
	Audit Auditor
}

// Server wraps an mcp.Server built from a Runefile's tasks.
type Server struct {
	opts    Options
	granted map[string]bool // opts.AllowDestructive as a set
	mcp     *mcp.Server
	// state is what calls run against. A call loads it once, so it never
	// sees the engine of one load and the tasks of another (spec 043).
	state atomic.Pointer[snapshot]
	// swapMu makes a Replace one step for tools/list: Replace holds it for
	// writing, a list for reading. Calls do not take it, so a Replace never
	// waits for a task.
	swapMu sync.RWMutex
	// registered is the JSON of each registered tool by tool name, guarded
	// by swapMu.
	registered   map[string][]byte
	instructions atomic.Pointer[string]

	// instance tells this server's sessions from those of another process
	// in the audit records. authFailures bounds the reports of refused
	// bearer tokens, on the clock now, which tests replace.
	instance     string
	authFailures authLimiter
	now          func() time.Time
}

// New builds an MCP server exposing engine's tasks as tools, except those
// opts.AllowList and opts.DenyList hide.
func New(engine Engine, opts Options) *Server {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	srv := &Server{
		opts:       opts,
		granted:    make(map[string]bool, len(opts.AllowDestructive)),
		registered: map[string][]byte{},
		instance:   newAuditID(),
		now:        time.Now,
	}
	for _, name := range opts.AllowDestructive {
		srv.granted[name] = true
	}
	srv.instructions.Store(&opts.Instructions)
	srv.mcp = mcp.NewServer(
		&mcp.Implementation{Name: "rune", Version: opts.Version},
		&mcp.ServerOptions{Capabilities: capabilities(opts)},
	)
	srv.mcp.AddReceivingMiddleware(srv.middleware)
	srv.Replace(engine, opts.OutputLimit)
	return srv
}

// capabilities returns the capabilities a filtered or reloadable server
// states outright, or nil to let the SDK infer them. The SDK advertises tools only when one is
// registered, and a filter that leaves none must still read as an empty tool
// list, not as a server without tool support (spec 041 FR-013). The values are
// the ones the SDK infers for a server with tools, so a filtered server looks
// the same whether or not any tool is left. A reloadable server may gain its
// first tool later, so it states them too (spec 043 FR-020).
func capabilities(opts Options) *mcp.ServerCapabilities {
	if !opts.Reloadable && len(opts.AllowList) == 0 && len(opts.DenyList) == 0 {
		return nil
	}
	return &mcp.ServerCapabilities{
		Logging: &mcp.LoggingCapabilities{},
		Tools:   &mcp.ToolCapabilities{ListChanged: true},
	}
}

// exposes reports whether the filters leave a task a tool: named in AllowList
// when one is given, and not named in DenyList, which wins.
func (s *Server) exposes(name string) bool {
	if len(s.opts.AllowList) > 0 && !slices.Contains(s.opts.AllowList, name) {
		return false
	}
	return !slices.Contains(s.opts.DenyList, name)
}

// Exposure reports how many tasks are exposed as tools and how many the
// Engine offered.
func (s *Server) Exposure() (exposed, exposable int) {
	snap := s.state.Load()
	return snap.exposed, snap.exposable
}

// MCP returns the underlying mcp.Server (for transports/tests).
func (s *Server) MCP() *mcp.Server { return s.mcp }

// toolName maps a (possibly namespaced) task name to a tool name: mod::task
// becomes mod__task (FR-025).
func toolName(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		if name[i] == ':' && i+1 < len(name) && name[i+1] == ':' {
			out = append(out, '_', '_')
			i++
			continue
		}
		out = append(out, name[i])
	}
	return string(out)
}

// toolFor derives an mcp.Tool from a task's metadata.
func toolFor(t TaskInfo) *mcp.Tool {
	destructive := t.Destructive || len(t.DestructiveReach) > 0 || t.AppliesPlan != ""
	network := t.Network
	desc := t.Doc
	if t.Returns != "" {
		// No trailer is invented when [returns] is absent (spec 023 US3).
		if desc != "" {
			desc += "\n\n"
		}
		desc += "Returns: " + t.Returns
	}
	if policy := agentPolicy(t.Params); policy != "" {
		if desc != "" {
			desc += "\n\n"
		}
		desc += policy
	}
	if t.AppliesPlan != "" {
		if desc != "" {
			desc += "\n\n"
		}
		desc += fmt.Sprintf("Plan approval: applies the plan made by task %q. Every call asks the person at the client to approve that plan.", t.AppliesPlan)
	}
	return &mcp.Tool{
		Name:        toolName(t.Name),
		Description: desc,
		InputSchema: inputSchema(t.Params),
		// The same for every tool, on every load, so it never makes a
		// reload look like a change to the tool list.
		OutputSchema: outputSchema(),
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			OpenWorldHint:   &network,
			ReadOnlyHint:    t.ReadOnly,
		},
	}
}

// restricted reports whether an agent may pass only AgentValues for p.
func (p ParamInfo) restricted() bool {
	return len(p.AgentValues) > 0 && !p.Variadic
}

// agentPolicy states, for a tool's description, the values an agent may pass
// for the restricted parameters, or "" when there is none (spec 050). The
// schema shows the same, and an agent that reads only the description still
// learns why a call would be refused.
func agentPolicy(params []ParamInfo) string {
	var predicates []string
	for _, p := range params {
		if !p.restricted() {
			continue
		}
		quoted := make([]string, len(p.AgentValues))
		for i, v := range p.AgentValues {
			quoted[i] = strconv.Quote(v)
		}
		predicates = append(predicates, p.Name+"="+strings.Join(quoted, " or "))
	}
	if len(predicates) == 0 {
		return ""
	}
	return "Agent policy: agents may run this task only with " + strings.Join(predicates, " and ") + "."
}

// inputSchema derives a JSON Schema (2020-12) object from task parameters:
// required→required, defaulted→optional, variadic→array. Declared constraint
// kinds map per specs/023-mcp-typed-schemas/contracts/mcp-schema.md: enum
// value lists are enumerated, numbers/booleans typed, paths format-tagged —
// so a conforming client can construct only valid calls (SC-001). Every schema
// disallows other names, so a client can catch a misspelled one (spec 032). A
// parameter an agent may pass only some values for lists those alone (spec
// 050).
func inputSchema(params []ParamInfo) map[string]any {
	properties := map[string]any{}
	var required []string
	for _, p := range params {
		prop := map[string]any{"type": scalarType(p.Kind)}
		switch p.Kind {
		case "path":
			prop["format"] = "path"
		case "enum":
			prop["enum"] = p.Enum
		}
		if p.restricted() {
			prop["enum"] = p.AgentValues
		}
		if p.Variadic {
			prop = map[string]any{"type": "array", "items": prop}
		}
		if desc := describe(p); desc != "" {
			prop["description"] = desc
		}
		properties[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// rawNote tells an agent that a raw value is split into shell words, so it
// sends flags as one string rather than expecting them quoted (spec 030).
const rawNote = "Split into shell words without quoting."

// describe returns the property description: the author's text, then rawNote
// for a raw parameter, joined as one paragraph.
func describe(p ParamInfo) string {
	if p.Kind != "raw" {
		return p.Description
	}
	if p.Description == "" {
		return rawNote
	}
	if strings.HasSuffix(p.Description, ".") || strings.HasSuffix(p.Description, "!") || strings.HasSuffix(p.Description, "?") {
		return p.Description + " " + rawNote
	}
	return p.Description + ". " + rawNote
}

// scalarType maps a constraint kind to its JSON Schema scalar type. Unknown
// and empty kinds are strings — the analyzer rejects unknown kinds before a
// server is ever built, so this is only a defensive default.
func scalarType(kind string) string {
	switch kind {
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	default:
		return "string"
	}
}
