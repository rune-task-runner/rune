package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// handler builds the tool-call handler for a task: it enforces authorization,
// unmarshals arguments, runs the task through the shared engine, and returns
// {stdout, stderr, exitCode} as the tool result.
func (s *Server) handler(taskName string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !s.authorized(taskName) {
			return errorResult(fmt.Sprintf("task %q requires explicit approval and is not authorized for this session", taskName)), nil
		}
		args, err := decodeArgs(req.Params.Arguments)
		if err != nil {
			return errorResult("invalid arguments: " + err.Error()), nil
		}
		res, runErr := s.engine.Call(ctx, taskName, args)
		if runErr != nil {
			return errorResult(runErr.Error()), nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatResult(res)}},
			IsError: res.ExitCode != 0,
		}, nil
	}
}

// decodeArgs converts the raw JSON arguments into string parameters. Array
// (variadic) values are space-joined.
func decodeArgs(raw json.RawMessage) (map[string]string, error) {
	out := map[string]string{}
	if len(raw) == 0 {
		return out, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for k, v := range m {
		switch x := v.(type) {
		case string:
			out[k] = x
		case []any:
			parts := make([]string, 0, len(x))
			for _, e := range x {
				parts = append(parts, fmt.Sprint(e))
			}
			out[k] = strings.Join(parts, " ")
		default:
			out[k] = fmt.Sprint(x)
		}
	}
	return out, nil
}

func formatResult(r Result) string {
	var b strings.Builder
	if r.Stdout != "" {
		b.WriteString(r.Stdout)
	}
	if r.Stderr != "" {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(r.Stderr)
	}
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "[exit %d]", r.ExitCode)
	if r.FixSuggestion != "" {
		b.WriteString("\n[fix suggestion - from Runefile || failure hooks]\n")
		b.WriteString(r.FixSuggestion)
	}
	return b.String()
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}
