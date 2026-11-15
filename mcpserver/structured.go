package mcpserver

// structuredResult is the structured content of a tool result: what the
// text carries, as fields (spec 044). A client reads the exit code and the
// two streams from it with no parsing of the [exit N] line.
type structuredResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	// Left out when empty, so the key says the text has a fix-suggestion
	// section.
	FixSuggestion string `json:"fixSuggestion,omitempty"`
}

// structuredFrom builds the structured content of a result whose streams
// are already capped.
func structuredFrom(r Result) structuredResult {
	return structuredResult{
		ExitCode:      r.ExitCode,
		Stdout:        r.Stdout,
		Stderr:        r.Stderr,
		FixSuggestion: r.FixSuggestion,
	}
}

// capResult returns r with each of stdout and stderr capped at limit. The
// fix suggestion is the engine's to cap, like Result says.
func capResult(r Result, limit int) Result {
	r.Stdout = capOutput(r.Stdout, limit)
	r.Stderr = capOutput(r.Stderr, limit)
	return r
}

// The truncation note is the same for both streams.
const truncationNote = " Output over the server's limit keeps its beginning and its end around a [truncated N bytes] line."

// outputSchema is the JSON Schema of structuredResult that every tool
// declares. It allows no other property, so a client can validate a result
// strictly. The descriptions are fixed text: nothing from a Runefile or the
// environment reaches them. Each call returns a new map, so no two tools
// share one.
func outputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"exitCode": map[string]any{
				"type":        "integer",
				"description": "Exit status of the run: 0 on success. The same number as the [exit N] line of the text result.",
			},
			"stdout": map[string]any{
				"type":        "string",
				"description": "What the run wrote to standard output, with secrets masked." + truncationNote,
			},
			"stderr": map[string]any{
				"type":        "string",
				"description": "What the run wrote to standard error, with secrets masked." + truncationNote,
			},
			"fixSuggestion": map[string]any{
				"type":        "string",
				"description": "Output of the task's || failure hooks. Present only when a failed run produced one.",
			},
		},
		"required":             []string{"exitCode", "stdout", "stderr"},
		"additionalProperties": false,
	}
}
