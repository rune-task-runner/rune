# Data Model: Reject Unknown MCP Tool Arguments

**Feature**: 032-mcp-unknown-arguments | **Date**: 2026-11-05 | **Spec**: [spec.md](spec.md)

No storage, no file formats, no Runefile syntax. The model is one list the server already
holds and the decision made from it on each call.

## Declared parameters

`TaskInfo.Params`, in declaration order. It is the only source for both the tool's input
schema and the check on a call, so the two cannot disagree ([research R1](research.md)).

| Field | Use here |
|---|---|
| `Name` | The exact, case-sensitive name a call may use. Also a candidate for the suggestion. |

## Order of checks on a tool call

The first row that matches decides the result. Rows 1 and 2 already exist; row 3 is new, and
rows 4 to 6 are unchanged.

| # | Condition | Result | Engine runs? |
|---|---|---|---|
| 1 | Task outside `AllowList` | `task "NAME" is not authorized for this session` | No |
| 2 | Destructive task not granted | The destructive refusal (spec 031) | No |
| 3 | Arguments are a JSON object naming an undeclared parameter | The unknown-argument refusal ([contract](contracts/mcp-tools.md)) | No |
| 4 | Arguments are not a JSON object, or a declared parameter has an invalid value, or a null, object, or nested-array value | `invalid arguments: ...`, for the parameter declared first | No |
| 5 | A required parameter is missing | `task "NAME" is missing required argument "PARAM"` | Reached, refuses before any body |
| 6 | Otherwise | The task's normal result | Yes |

## Suggestion

For each undeclared name, a parameter that differs only in case, or else the closest declared
parameter within two edits, using the rule in `internal/suggest` ([research R3](research.md)).
A tie resolves to a parameter the call did not supply, then to the one declared first. With no
parameter that close, or for a name over 64 characters, the line has no suggestion.
