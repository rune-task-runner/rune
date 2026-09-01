package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// handler builds the tool-call handler for a task: it enforces authorization,
// unmarshals and validates arguments against the advertised schema (spec 023
// FR-005 — a violation never reaches the engine), runs the task through the
// shared engine, and returns {stdout, stderr, exitCode} as the tool result.
func (s *Server) handler(t TaskInfo) mcp.ToolHandler {
	taskName := t.Name
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !s.authorized(taskName) {
			return errorResult(fmt.Sprintf("task %q requires explicit approval and is not authorized for this session", taskName)), nil
		}
		args, err := decodeArgs(req.Params.Arguments, t.Params)
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

// decodeArgs converts the raw JSON arguments into string parameters and
// validates each against its declared constraint (spec 023 FR-005 — a
// violation never reaches the engine). Array (variadic) values are validated
// PER ELEMENT and then space-joined — the join destroys element boundaries,
// so this is the only point where per-value semantics exist on the MCP path
// (spec 023 FR-008). A scalar supplied for a variadic parameter is validated
// as a single element, exactly as the CLI treats one quoted argument, so no
// value form bypasses the constraint. Numbers are decoded with UseNumber so
// their source spelling survives verbatim (fmt.Sprint on a float64 would
// render 1000000 as "1e+06").
func decodeArgs(raw json.RawMessage, params []ParamInfo) (map[string]string, error) {
	out := map[string]string{}
	if len(raw) == 0 {
		return out, nil
	}
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	byName := make(map[string]ParamInfo, len(params))
	for _, p := range params {
		byName[p.Name] = p
	}
	for k, v := range m {
		p, known := byName[k]
		if arr, isArr := v.([]any); isArr {
			parts := make([]string, 0, len(arr))
			for _, e := range arr {
				s := fmt.Sprint(e)
				if known {
					if err := p.check(s); err != nil {
						return nil, err
					}
				}
				parts = append(parts, s)
			}
			out[k] = strings.Join(parts, " ")
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			s = fmt.Sprint(v) // json.Number, bool, or null
		}
		if known {
			if err := p.check(s); err != nil {
				return nil, err
			}
		}
		out[k] = s
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
