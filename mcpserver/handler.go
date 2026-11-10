package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/suggest"
)

// The refusal for undeclared names goes into the calling agent's context, so
// these bound it however large the call is.
const (
	maxUnknownLines = 20
	maxUnknownRunes = 64
)

// handler builds the tool-call handler for a task: it enforces authorization,
// including for the destructive tasks t says a run can reach (spec 034),
// refuses argument names the task does not declare (spec 032), validates the
// arguments against the advertised schema (spec 023 FR-005: a violation never
// reaches the engine), runs the task through the shared engine, and returns
// {stdout, stderr, exitCode} as the tool result.
func (s *Server) handler(t TaskInfo) mcp.ToolHandler {
	taskName := t.Name
	names := make([]string, len(t.Params))
	for i, p := range t.Params {
		names[i] = p.Name
	}
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if msg := s.refusal(t); msg != "" {
			return errorResult(msg), nil
		}
		decoded, err := decodeArgs(req.Params.Arguments)
		if err != nil {
			return errorResult("invalid arguments: " + err.Error()), nil
		}
		if msg := unknownArgs(taskName, decoded, names); msg != "" {
			return errorResult(msg), nil
		}
		args, lists, err := stringArgs(decoded, t.Params)
		if err != nil {
			return errorResult("invalid arguments: " + err.Error()), nil
		}
		var res Result
		var runErr error
		if le, ok := s.engine.(ListEngine); ok {
			res, runErr = le.CallLists(ctx, taskName, args, lists)
		} else {
			res, runErr = s.engine.Call(ctx, taskName, args)
		}
		if runErr != nil {
			return errorResult(runErr.Error()), nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatResult(res)}},
			IsError: res.ExitCode != 0,
		}, nil
	}
}

// decodeArgs parses a call's raw JSON arguments. Numbers are decoded with
// UseNumber so their source spelling survives verbatim (fmt.Sprint on a float64
// would render 1000000 as "1e+06"). Absent arguments and null give a nil map.
func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// unknownArgs returns one line for each argument name the task does not declare,
// or "" when there is none. It reads only the names, so a value can never reach
// the text. Names are sorted because a Go map's order varies and the same call
// must get the same text.
func unknownArgs(task string, args map[string]any, names []string) string {
	var unknown []string
	for name := range args {
		if !slices.Contains(names, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	slices.Sort(unknown)

	// A tie goes to the first candidate, so listing the parameters the call left
	// out first keeps a hint from naming one already in use: following it would
	// overwrite that value.
	candidates := make([]string, 0, len(names))
	for _, inUse := range []bool{false, true} {
		for _, name := range names {
			if _, ok := args[name]; ok == inUse {
				candidates = append(candidates, name)
			}
		}
	}

	lines := make([]string, 0, maxUnknownLines+1)
	for _, name := range unknown[:min(len(unknown), maxUnknownLines)] {
		lines = append(lines, unknownLine(task, name, candidates))
	}
	if hidden := len(unknown) - maxUnknownLines; hidden > 0 {
		lines = append(lines, fmt.Sprintf("unknown arguments not shown for task %q: %d", task, hidden))
	}
	return strings.Join(lines, "\n")
}

func unknownLine(task, name string, candidates []string) string {
	// A name this long is not a typo of a parameter, so it gets no suggestion.
	if r := []rune(name); len(r) > maxUnknownRunes {
		return fmt.Sprintf("unknown argument %q for task %q", string(r[:maxUnknownRunes])+"...", task)
	}
	line := fmt.Sprintf("unknown argument %q for task %q", name, task)
	if s, ok := closestParam(name, candidates); ok {
		line += fmt.Sprintf(" (did you mean %q?)", s)
	}
	return line
}

// closestParam adds one rule to suggest.Nearest: a name that differs from a
// parameter only in case matches it, however many letters that is. ENV is three
// edits from env, so Nearest alone would call it close to nothing.
func closestParam(name string, candidates []string) (string, bool) {
	for _, c := range candidates {
		if strings.EqualFold(name, c) {
			return c, true
		}
	}
	return suggest.Nearest(name, candidates)
}

// stringArgs converts decoded arguments into the string parameters a task
// receives and validates each against its declared constraint (spec 023 FR-005:
// a violation never reaches the engine). It goes in declaration order, so the
// same call always reports the same invalid value. Array values are validated
// PER ELEMENT and then space-joined into args, as Engine.Call has always received
// them. For a variadic parameter the elements are also returned in lists, so a
// ListEngine keeps their boundaries (spec 030). A scalar supplied for a variadic
// parameter is validated as a single element, exactly as the CLI treats one
// quoted argument, so no value form bypasses the constraint; it is listed as one
// element, or as none when it is empty.
func stringArgs(decoded map[string]any, params []ParamInfo) (map[string]string, map[string][]string, error) {
	out := map[string]string{}
	lists := map[string][]string{}
	for _, p := range params {
		v, given := decoded[p.Name]
		if !given {
			continue
		}
		if arr, isArr := v.([]any); isArr {
			parts := make([]string, 0, len(arr))
			for _, e := range arr {
				s, err := scalarArg(p, e)
				if err != nil {
					return nil, nil, err
				}
				if err := p.check(s); err != nil {
					return nil, nil, err
				}
				parts = append(parts, s)
			}
			out[p.Name] = strings.Join(parts, " ")
			if p.Variadic {
				lists[p.Name] = parts
			}
			continue
		}
		s, err := scalarArg(p, v)
		if err != nil {
			return nil, nil, err
		}
		if err := p.check(s); err != nil {
			return nil, nil, err
		}
		out[p.Name] = s
		if p.Variadic {
			// An empty string carries no element, like [] and like the CLI
			// with no arguments; one empty element would reach quote_each as ''.
			lists[p.Name] = []string{}
			if s != "" {
				lists[p.Name] = []string{s}
			}
		}
	}
	return out, lists, nil
}

// scalarArg gives the text a task receives for one JSON value. Strings, numbers,
// and booleans have one. Null, objects, and nested arrays do not: fmt.Sprint would
// produce <nil> or map[k:v], and the task would run on that (spec 032 FR-015).
func scalarArg(p ParamInfo, v any) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case json.Number, bool:
		return fmt.Sprint(v), nil
	case nil:
		return "", fmt.Errorf("parameter %q: null is not a valid value", p.Name)
	case map[string]any:
		return "", fmt.Errorf("parameter %q: an object is not a valid value", p.Name)
	default:
		return "", fmt.Errorf("parameter %q: a nested array is not a valid value", p.Name)
	}
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
