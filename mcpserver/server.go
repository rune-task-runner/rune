// Package mcpserver exposes a Runefile's non-private tasks to AI agents and IDEs
// as Model Context Protocol tools. Tools are run through the SAME engine the CLI
// uses (FR-026); secrets never appear in any tool name, description, schema, or
// result (FR-029, Principle VII). It is a public package so it can be embedded.
package mcpserver

import (
	"context"

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
}

// TaskInfo is the agent-facing view of a task. Private tasks are never included.
type TaskInfo struct {
	Name        string // display name (namespaced mod::task becomes mod__task as a tool)
	Doc         string
	Params      []ParamInfo
	Destructive bool // has [confirm]/destructive => DestructiveHint
	Network     bool // has [network] => OpenWorldHint
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
		srv.mcp.AddTool(toolFor(t), srv.handler(t.Name))
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
	return &mcp.Tool{
		Name:        toolName(t.Name),
		Description: t.Doc,
		InputSchema: inputSchema(t.Params),
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			OpenWorldHint:   &network,
			ReadOnlyHint:    !destructive,
		},
	}
}

// inputSchema derives a JSON Schema (2020-12) object from task parameters:
// required→required, defaulted→optional, variadic→array.
func inputSchema(params []ParamInfo) map[string]any {
	properties := map[string]any{}
	var required []string
	for _, p := range params {
		if p.Variadic {
			properties[p.Name] = map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			}
		} else {
			properties[p.Name] = map[string]any{"type": "string"}
		}
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
