package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// snapshot is one load of an engine: what calls run against until the next
// Replace. It is never changed after it is stored.
type snapshot struct {
	engine Engine
	// tasks holds the exposed tasks by tool name. A call to a name not here
	// is a call to an unknown tool.
	tasks map[string]TaskInfo
	// exposed counts the tasks registered as tools, exposable the tasks the
	// engine offered.
	exposed, exposable int
	outputLimit        int // brought into range
}

// Change counts what a Replace did to the tool list.
type Change struct{ Added, Removed, Changed int }

// Replace makes engine the server's engine and its tasks the server's tools,
// under the Options given to New. outputLimit is as Options.OutputLimit.
// Calls in progress finish on the engine they started with. Connected clients
// are notified when the tool list differs. Safe for concurrent use.
func (s *Server) Replace(engine Engine, outputLimit int) Change {
	next := &snapshot{
		engine:      engine,
		tasks:       map[string]TaskInfo{},
		outputLimit: clampLimit(outputLimit),
	}
	tools := map[string]*mcp.Tool{}
	for _, t := range engine.Tasks() {
		next.exposable++
		// A hidden task is never registered, so the SDK answers a call to it
		// exactly as it answers a name no task has (spec 041 FR-004).
		if !s.exposes(t.Name) {
			continue
		}
		tool := toolFor(t)
		tools[tool.Name] = tool
		next.tasks[tool.Name] = t
		next.exposed++
	}

	s.swapMu.Lock()
	defer s.swapMu.Unlock()
	s.state.Store(next)

	var change Change
	var removed []string
	for name := range s.registered {
		if _, ok := tools[name]; !ok {
			removed = append(removed, name)
			delete(s.registered, name)
		}
	}
	change.Removed = len(removed)
	if len(removed) > 0 {
		s.mcp.RemoveTools(removed...)
	}
	for name, tool := range tools {
		// The JSON is what a client sees, so equal bytes mean the client has
		// nothing to learn. AddTool always notifies; skipping it here is what
		// keeps a body-only edit silent (spec 043 FR-017).
		advertised, err := json.Marshal(tool)
		if err != nil {
			// toolFor builds from strings, booleans, and maps of them.
			panic("mcpserver: tool " + name + " does not marshal: " + err.Error())
		}
		old, known := s.registered[name]
		if known && bytes.Equal(old, advertised) {
			continue
		}
		if known {
			change.Changed++
		} else {
			change.Added++
		}
		s.registered[name] = advertised
		s.mcp.AddTool(tool, s.handler(name))
	}
	return change
}

// SetInstructions sets the instructions that sessions started from now on
// receive. It must already be masked, as Options.Instructions.
func (s *Server) SetInstructions(text string) {
	s.instructions.Store(&text)
}

// middleware gives two requests the server's current state: tools/list waits
// out a Replace, so it never returns part of one, and initialize carries the
// instructions in force now. The SDK reads its own instructions once, when
// the server is built.
func (s *Server) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			s.swapMu.RLock()
			defer s.swapMu.RUnlock()
		case "initialize":
			res, err := next(ctx, method, req)
			if init, ok := res.(*mcp.InitializeResult); ok && err == nil {
				init.Instructions = *s.instructions.Load()
			}
			return res, err
		}
		return next(ctx, method, req)
	}
}
