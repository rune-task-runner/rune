// Package mcpserver exposes a Runefile's non-private tasks to AI agents and IDEs
// as Model Context Protocol tools. Tools are run through the SAME engine the CLI
// uses (FR-026); secrets never appear in any tool name, description, schema, or
// result (FR-029, Principle VII). It is a public package so it can be embedded.
package mcpserver

import (
	"context"
	"fmt"
	"math"
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
type TaskInfo struct {
	Name        string // display name (namespaced mod::task becomes mod__task as a tool)
	Doc         string
	Params      []ParamInfo
	Destructive bool // has [confirm]/destructive => DestructiveHint
	Network     bool // has [network] => OpenWorldHint
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

// Options configures authorization for the server.
type Options struct {
	// AllowDestructive permits calling tasks marked destructive ([confirm]).
	AllowDestructive bool
	// AllowList, when non-empty, narrows callable tasks to these names.
	AllowList []string
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
	engine Engine
	opts   Options
	mcp    *mcp.Server
}

// New builds an MCP server exposing engine's non-private tasks as tools.
func New(engine Engine, opts Options) *Server {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	srv := &Server{engine: engine, opts: opts}
	srv.mcp = mcp.NewServer(
		&mcp.Implementation{Name: "rune", Version: opts.Version},
		&mcp.ServerOptions{Instructions: opts.Instructions},
	)
	for _, t := range engine.Tasks() {
		srv.mcp.AddTool(toolFor(t), srv.handler(t))
	}
	return srv
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
	destructive := t.Destructive
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
			ReadOnlyHint:    !destructive,
		},
	}
}

// inputSchema derives a JSON Schema (2020-12) object from task parameters:
// required→required, defaulted→optional, variadic→array. Declared constraint
// kinds map per specs/023-mcp-typed-schemas/contracts/mcp-schema.md: enum
// value lists are enumerated, numbers/booleans typed, paths format-tagged —
// so a conforming client can construct only valid calls (SC-001).
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
		if p.Description != "" {
			prop["description"] = p.Description
		}
		properties[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
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
