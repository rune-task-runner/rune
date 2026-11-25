# Contract: Hook Handlers, Hook Input, Records

## The handlers in `.claude/settings.local.json`

`rune agent observe claude` makes sure each of these events has a handler whose `command`
is exactly `rune agent record`. Where one is missing it appends this group to the event's
list, creating `hooks` and the list when missing:

```json
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }
    ],
    "PostToolUse": [
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }
    ],
    "PostToolUseFailure": [
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }
    ]
  }
}
```

A handler counts as Rune's when it is an object in a group's `hooks` list whose `command`
is the string `rune agent record`, whatever its other keys. Rune never changes the other
keys of such a handler, nor any other handler, group, event, setting or permission rule.

Removal deletes each of Rune's handlers, then a group whose `hooks` list is empty because of
it, then an event key whose list is empty because of it. `hooks` stays, also when empty. The
file is never deleted.

Values in the way: `hooks` that is not an object, an event value that is not a list, or a
group's `hooks` that is not a list, is reported like a conflict of `rune agent init`, with
its line, and nothing is written (exit 2).

## The input `rune agent record` accepts

One JSON object on standard input, at most 4 MiB. The fields read are in research R1.
Examples, from the hooks reference:

```json
{
  "session_id": "abc123",
  "cwd": "/home/user/my-project",
  "hook_event_name": "PreToolUse",
  "tool_name": "Bash",
  "tool_input": { "command": "npm test", "description": "Run test suite", "timeout": 120000, "run_in_background": false },
  "tool_use_id": "toolu_01ABC123"
}
```

```json
{
  "session_id": "abc123",
  "cwd": "/home/user/my-project",
  "hook_event_name": "PostToolUseFailure",
  "tool_name": "Bash",
  "tool_input": { "command": "npm test", "description": "Run test suite" },
  "tool_use_id": "toolu_01ABC123",
  "error": "Exit code 1\nError: Cannot find module 'express'",
  "is_interrupt": false,
  "duration_ms": 4187
}
```

The project directory comes from the `CLAUDE_PROJECT_DIR` environment variable.

| Input | Result |
|---|---|
| another `hook_event_name` or `tool_name` | ignored, nothing written |
| not JSON, or over 4 MiB | counted: `not-json`, `too-large` |
| input not ended within 5 seconds | counted: `slow-input` |
| no `tool_use_id`, `session_id` or `tool_input.command`, or an id over 128 bytes | counted: `invalid-event` |
| no `CLAUDE_PROJECT_DIR`, or not absolute | counted in the shared file: `no-project` |
| `cwd` outside the root | counted: `outside-project` |
| a Runefile that cannot be loaded | counted: `runefile-unreadable` |
| a command that does not parse, has a here-document, is over 4096 bytes, or has a control character | counted: `unparsable`, `here-document`, `too-long`, `control-character` |
| the store's lock not taken in 2 s | counted: `store-busy` |
| any other write failure | counted: `store-error` |
| otherwise | one record appended |

`error` is read only up to its first line feed. `Exit code N` there, with N a decimal
integer, sets `exit`; anything else leaves it out.

## The record

One line of `events.jsonl`, at most 8 KiB:

```json
{"v":1,"time":"2026-11-23T10:15:02Z","client":"claude","session":"abc123","toolUse":"toolu_01ABC123","event":"failure","exit":1,"command":"npm test","description":"Run test suite"}
```

Keys and rules: data-model `observe.Record`. A line that does not decode as a record of
version 1 is skipped by readers and counted as damaged.

## Diagnostics

`diagnostics.json` in the project's directory, and one shared in the store's base:

```json
{"reasons":{"here-document":{"n":2,"last":"2026-11-23T10:15:02Z"}}}
```

It holds reason names, counts and times only.

## Files and modes

```text
<state>/rune/observe/                 0700
<state>/rune/observe/diagnostics.json 0600  shared counts
<state>/rune/observe/<id>/            0700  one project, <id> = audit.ProjectID(root)
<state>/rune/observe/<id>/events.jsonl      0600
<state>/rune/observe/<id>/diagnostics.json  0600
<state>/rune/observe/<id>/.lock             while a writer holds it
```

`<state>` is `$XDG_STATE_HOME` when absolute, else `%LocalAppData%` on Windows, else
`~/.local/state`.
