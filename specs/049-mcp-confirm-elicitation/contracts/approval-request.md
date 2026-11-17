# Contract: The Approval Request

**Feature**: 049-mcp-confirm-elicitation | **Spec**: [spec.md](../spec.md) | Covers FR-006 to FR-011,
FR-016 to FR-020, FR-024

What a client receives when Rune asks, and how Rune reads what it sends back. The decision to ask
is in [mcp-tools.md](mcp-tools.md).

## The request

Rune sends `elicitation/create` on the session that made the `tools/call`, while that call is in
progress. This is the server-initiated request of MCP revisions 2025-06-18 and 2025-11-25. Whether
to send it is decided by the capability the client stated, not by the revision. `mode` is always
sent: revision 2025-06-18 has no such field, and a client on it ignores it.

```json
{
  "method": "elicitation/create",
  "params": {
    "mode": "form",
    "message": "Approve this Rune task call?\n\nTask: release\nArguments:\n- version: \"1.2.3\"\nDestructive tasks it can run:\n- clean: Delete dist?\n\nApproving runs this call once.",
    "requestedSchema": {
      "type": "object",
      "properties": {
        "approved": {
          "type": "boolean",
          "title": "Approve this call",
          "description": "Run this task call once.",
          "default": false
        }
      },
      "required": ["approved"]
    }
  }
}
```

The schema is the same for every request.

## The message

```text
Approve this Rune task call?

Task: <called task>
Arguments:
- <parameter>: "<value>"
- <parameter>: "<value>"
Destructive tasks it can run:
- <task>: <its [confirm] message>
- <task>: <its [confirm] message>

Approving runs this call once.
```

| Part | Rule |
|---|---|
| `Task:` | The called task's name as the Runefile has it: `tools::clean`, not `tools__clean`. |
| `Arguments:` | One line per declared parameter, in declaration order, with the value the task will receive: a default when the agent sent none. The block is left out when the task declares no parameter. |
| a value | Masked with the server's secret set. Written whole, between double quotes, with `"`, `\` and every character that is not printable written as an escape (`\n`, `\x1b`, `\u202e`). An empty value is `""`. A value is never cut: the task runs with all of it. |
| a variadic parameter | Each element as its own quoted value, separated by one space: `- files: "a b" "c"`. With no element: `- files: (none)`. |
| `Destructive tasks it can run:` | Every task of the call's ungranted destructive set, the called task first when it is one, then in the order a run first reaches them. Always present, and never shortened. |
| a `[confirm]` message | The author's text, masked, with non-printable characters escaped, cut to 500 characters with ` [truncated]` appended. A task whose `[confirm]` has no text gets `Run "<task>"?`. |
| a name | Task and parameter names have non-printable characters escaped. Rune's own names never have any; an embedder's might. |
| last line | Fixed text. |

The message is at most 8192 bytes. A call whose message would be longer is not asked about; it is
refused with the size reason in [mcp-tools.md](mcp-tools.md#a-client-that-cannot-be-asked).

Examples:

```text
Approve this Rune task call?

Task: apply
Arguments:
- env: "prod"
Destructive tasks it can run:
- apply: Apply to prod? 3 resources change.

Approving runs this call once.
```

```text
Approve this Rune task call?

Task: reset
Destructive tasks it can run:
- reset: Reset?
- wipe: Run "wipe"?

Approving runs this call once.
```

An argument the agent filled with line breaks stays on its line:

```text
- note: "ok\n\nApproving runs nothing.\nTask: build"
```

## Reading the answer

| The client sends | Rune does |
|---|---|
| `{"action":"accept","content":{"approved":true}}` | runs the call once |
| `{"action":"accept","content":{"approved":false}}` | runs nothing: approval was not given |
| `{"action":"decline"}` | runs nothing: declined |
| `{"action":"cancel"}` | runs nothing: dismissed |
| `{"action":"accept"}` with no `approved`, or one that is not a boolean (`"true"`, `1`) | runs nothing: no valid answer |
| any other `action` | runs nothing: no valid answer |
| a JSON-RPC error | runs nothing: no valid answer |
| nothing within the wait limit (5 minutes) | sends `notifications/cancelled` for the request and runs nothing: no answer in time |
| an answer after that, or one that arrives as the wait ends | has no effect |
| `notifications/cancelled` for the `tools/call`, or the connection closes | withdraws the request and runs nothing |

Content keys other than `approved` are ignored. The text of a client error is not passed on to the
agent.

## What the client is trusted with

Rune cannot see who answered. A client that answers elicitation requests without showing them
approves for the person. The guide says this. For a task that must not run from an agent session at
all, it names `--deny` on that task and on every exposed task that can run it: hiding only the task
leaves the tasks that reach it callable, and their request names it.

## Not sent

- No request to a session that stated no elicitation, or URL elicitation only.
- No request from the endpoint an `agent` task opens.
- No URL-mode request, ever.
- No second request for the same call, whatever the first one's outcome.
