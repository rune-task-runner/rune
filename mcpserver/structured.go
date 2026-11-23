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
	// Present only for a task with an output policy, and only for a
	// failure with a summary (spec 057), so a task with neither attribute
	// has the four fields it had before.
	Output         *OutputReport   `json:"output,omitempty"`
	FailureSummary *FailureSummary `json:"failureSummary,omitempty"`
}

// structuredFrom builds the structured content of a result capResult
// returned.
func structuredFrom(r Result) structuredResult {
	return structuredResult{
		ExitCode:       r.ExitCode,
		Stdout:         r.Stdout,
		Stderr:         r.Stderr,
		FixSuggestion:  r.FixSuggestion,
		Output:         r.Output,
		FailureSummary: r.Failure,
	}
}

// capResult returns r with each of stdout and stderr capped at limit, and
// its failure summary completed from the capped streams, or dropped when the
// run succeeded (spec 057). The fix suggestion is the engine's to cap, like
// Result says. The engine's summary is copied, never changed.
func capResult(r Result, limit int) Result {
	r.Stdout = capOutput(r.Stdout, limit)
	r.Stderr = capOutput(r.Stderr, limit)
	if r.Failure != nil {
		if r.ExitCode == 0 {
			r.Failure = nil
		} else {
			f := completeSummary(*r.Failure, r)
			r.Failure = &f
		}
	}
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
			"output":         outputObjectSchema(),
			"failureSummary": failureSummarySchema(),
		},
		"required":             []string{"exitCode", "stdout", "stderr"},
		"additionalProperties": false,
	}
}

// typed is one property of a schema: its JSON type and its description.
func typed(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

// closedObject is an object schema that requires the named properties and
// allows no other. Each call returns new maps.
func closedObject(description string, properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

// outputObjectSchema describes OutputReport (spec 057). Every part is
// written out, with no reference to resolve.
func outputObjectSchema() map[string]any {
	stream := func(stream, field string) map[string]any {
		return closedObject("What "+stream+" had and what of it the "+field+" field holds.", map[string]any{
			"lines":     typed("integer", "Lines the stream had."),
			"bytes":     typed("integer", "Bytes the stream had, after masking."),
			"keptLines": typed("integer", "Lines with at least one byte in the result."),
			"keptBytes": typed("integer", "Bytes of the stream in the result, the omission line not counted."),
		}, "lines", "bytes", "keptLines", "keptBytes")
	}
	return closedObject("Present when the task keeps only some lines of its output for agents: the lines kept from the start and the end of each stream, and for each stream how many lines and bytes it had and how many are in the result.", map[string]any{
		"head":   typed("integer", "Lines kept from the start of each stream, 0 when none."),
		"tail":   typed("integer", "Lines kept from the end of each stream."),
		"stdout": stream("standard output", "stdout"),
		"stderr": stream("standard error", "stderr"),
	}, "head", "tail", "stdout", "stderr")
}

// failureSummarySchema describes FailureSummary (spec 057).
func failureSummarySchema() map[string]any {
	kind := typed("string", "What Rune observed: a body line failed, a script failed, Rune stopped the run, the call was cancelled, or only the exit status is known.")
	kind["enum"] = []string{FailureStep, FailureScript, FailureError, FailureCancelled, FailureUnknown}
	return closedObject("Present when a failed run of a task with [agent-summary] has a failure summary: the same facts as the summary section of the text.", map[string]any{
		"kind":          kind,
		"task":          typed("string", "The task the agent called."),
		"failedTask":    typed("string", "The task whose body failed, when Rune knows it."),
		"file":          typed("string", "The file of the failed step, relative to the directory of the Runefile."),
		"line":          typed("integer", "The line of the failed step in that file."),
		"command":       typed("string", "The command that failed, with secrets masked."),
		"reason":        typed("string", "Why the run failed: an exit status, Rune's message, or what Rune could not tell."),
		"fixSuggestion": typed("boolean", "Whether the result has a fix suggestion from the task's || failure hooks."),
		"stdoutTail":    typed("string", "The last lines of the stdout field."),
		"stderrTail":    typed("string", "The last lines of the stderr field."),
	}, "kind", "task", "reason", "fixSuggestion")
}
