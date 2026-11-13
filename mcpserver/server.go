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

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Result is the outcome of running a task on behalf of an agent.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// FixSuggestion is the captured output of the task's || failure hooks
	// (spec 022). Like Options.Instructions, it must already be masked and
	// size-capped by the engine; this package never processes it, only
	// renders it as a delimited section after the exit marker. Empty means
	// no section is rendered.
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
}

// TaskInfo is the agent-facing view of a task. Private tasks are never included.
// Params lists the only argument names a call may use: a call naming any other
// is refused before the engine runs (spec 032).
type TaskInfo struct {
	Name   string // display name (namespaced mod::task becomes mod__task as a tool)
	Doc    string
	Params []ParamInfo
	// Destructive marks the task itself destructive. A call is refused unless
	// the task is granted, and the tool advertises DestructiveHint.
	Destructive bool
	// DestructiveReach names the destructive tasks a run of this task can
	// reach through its dependencies and hooks: each once, not the task
	// itself, in the order a run first reaches them. A call is refused unless
	// every one is granted, and a non-empty list sets DestructiveHint. Nil
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

// Options configures authorization for the server.
type Options struct {
	// AllowDestructive names the destructive ([confirm]) tasks that may be
	// called or run. A call is refused before the engine runs when the task
	// itself, or any task in its DestructiveReach, is destructive and not named
	// here. There is no value that permits all of them. The Engine must still
	// approve the confirmation of exactly these tasks, wherever a run reaches
	// them; the rune CLI adapter does.
	AllowDestructive []string
	// AllowDestructiveFlag is the command-line flag that sets AllowDestructive,
	// such as "--allow-destructive". When set, a destructive refusal tells the
	// caller to add the task to it. Leave it empty where no grant is possible,
	// such as the endpoint of a rune agent task: the refusal then offers no
	// remedy.
	AllowDestructiveFlag string
	// AllowList, when non-empty, exposes only these tasks as tools. A task
	// outside it is not listed and cannot be called. It never adds a task the
	// Engine does not offer.
	AllowList []string
	// DenyList names tasks that are not exposed as tools, whether or not
	// AllowList names them.
	DenyList []string
	// OutputLimit caps the bytes of Result.Stdout and of Result.Stderr that a
	// tool result carries, marker included. Zero or less means
	// DefaultOutputLimit. Other values are raised to MinOutputLimit or lowered
	// to MaxOutputLimit.
	OutputLimit int
	// Version reported to clients.
	Version string
	// Instructions, when non-empty, is delivered to clients as the MCP
	// server instructions in the initialize result. Rune uses it for the
	// [context] hook's project-health text (spec 021 FR-002). It must
	// already be masked; this package never processes it.
	Instructions string
}

// Server wraps an mcp.Server built from a Runefile's tasks.
type Server struct {
	engine  Engine
	opts    Options
	granted map[string]bool // opts.AllowDestructive as a set
	mcp     *mcp.Server
	// exposed counts the tasks registered as tools, exposable the tasks the
	// engine offered.
	exposed, exposable int
	outputLimit        int // opts.OutputLimit, brought into range
}

// New builds an MCP server exposing engine's tasks as tools, except those
// opts.AllowList and opts.DenyList hide.
func New(engine Engine, opts Options) *Server {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	srv := &Server{
		engine:      engine,
		opts:        opts,
		granted:     make(map[string]bool, len(opts.AllowDestructive)),
		outputLimit: clampLimit(opts.OutputLimit),
	}
	for _, name := range opts.AllowDestructive {
		srv.granted[name] = true
	}
	srv.mcp = mcp.NewServer(
		&mcp.Implementation{Name: "rune", Version: opts.Version},
		&mcp.ServerOptions{Instructions: opts.Instructions, Capabilities: capabilities(opts)},
	)
	for _, t := range engine.Tasks() {
		srv.exposable++
		// A hidden task is never registered, so the SDK answers a call to it
		// exactly as it answers a name no task has (spec 041 FR-004).
		if !srv.exposes(t.Name) {
			continue
		}
		srv.mcp.AddTool(toolFor(t), srv.handler(t))
		srv.exposed++
	}
	return srv
}

// capabilities returns the capabilities a filtered server states outright, or
// nil to let the SDK infer them. The SDK advertises tools only when one is
// registered, and a filter that leaves none must still read as an empty tool
// list, not as a server without tool support (spec 041 FR-013). The values are
// the ones the SDK infers for a server with tools, so a filtered server looks
// the same whether or not any tool is left.
func capabilities(opts Options) *mcp.ServerCapabilities {
	if len(opts.AllowList) == 0 && len(opts.DenyList) == 0 {
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
func (s *Server) Exposure() (exposed, exposable int) { return s.exposed, s.exposable }

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
	destructive := t.Destructive || len(t.DestructiveReach) > 0
	network := t.Network
	desc := t.Doc
	if t.Returns != "" {
		// No trailer is invented when [returns] is absent (spec 023 US3).
		if desc != "" {
			desc += "\n\n"
		}
		desc += "Returns: " + t.Returns
	}
	return &mcp.Tool{
		Name:        toolName(t.Name),
		Description: desc,
		InputSchema: inputSchema(t.Params),
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			OpenWorldHint:   &network,
			ReadOnlyHint:    t.ReadOnly,
		},
	}
}

// inputSchema derives a JSON Schema (2020-12) object from task parameters:
// required→required, defaulted→optional, variadic→array. Declared constraint
// kinds map per specs/023-mcp-typed-schemas/contracts/mcp-schema.md: enum
// value lists are enumerated, numbers/booleans typed, paths format-tagged —
// so a conforming client can construct only valid calls (SC-001). Every schema
// disallows other names, so a client can catch a misspelled one (spec 032).
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
