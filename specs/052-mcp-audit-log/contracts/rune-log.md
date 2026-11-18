# Contract: `rune log`

```text
rune log [-f <Runefile>] [--date <day> | --since <when> --until <when>] [--tz <zone>]
         [--task <name>[,...]] [--outcome <outcome>[,...]] [--client <name>[,...]]
         [--session <id>[,...]] [--all] [--json] [--dir]
```

Reads the audit log of one project. Runs no task and writes nothing to the log.

## Project

The Runefile found from the working directory, or the one `-f` names, as for `rune mcp`. With
no Runefile the command fails as other commands do (exit 2).

## Flags

| Flag | Value | Meaning |
|---|---|---|
| `-f`, `--file` | path | Read the log of this Runefile |
| `--date` | `YYYY-MM-DD` | Actions that started on that day in the display zone |
| `--since` | day, RFC 3339 time, or duration | Actions that started at or after it. A day is its first instant. A duration (`90m`, `2h`, `7d`) is counted back from now |
| `--until` | day, RFC 3339 time, or duration | Actions that started before it. A day includes the whole day |
| `--tz` | IANA name, `UTC` or `local` | The display zone. Default `local`: the zone of this machine, which `TZ` changes |
| `--task` | names | The task name or the tool name equals one of them |
| `--outcome` | outcomes | One of `completed`, `failed`, `refused`, `invalid-arguments`, `unknown-tool`, `cancelled`, `timed-out`, `error`, `incomplete`, `ok` |
| `--client` | names | The client name equals one of them, ignoring case |
| `--session` | ids | The session id equals one of them or starts with one of them |
| `--all` | | Also list requests that are not tool calls |
| `--json` | | Print one JSON object per action |
| `--dir` | | Print the project's log directory and exit |

- With no range flag the range is today in the display zone.
- `--date` cannot be combined with `--since` or `--until`.
- `--since` without `--until` runs to now. `--until` without `--since` starts at the oldest
  file.
- List flags take comma-separated values and may be repeated. Values of one flag are
  alternatives. Different flags must all match.
- `--dir` cannot be combined with another flag except `-f`.
- An action that lacks the field a flag filters on does not match that flag: an
  authentication failure has no task, outcome, client or session, and an agent run has no
  client or session.

## Text output

Standard output:

```text
Agent actions on 2026-11-18 (local, UTC+02:00) for /repo/Runefile

10:04:11  completed          plan env="prod"       exit 0   12.4s  claude-code 2.1.0  3fa9c2d1e07b5a44
10:05:02  completed          apply env="prod"      exit 0   22.1s  claude-code 2.1.0  b81e04f7a2c9d310
          approved at the client; plan 7f3c9a1e52b84d06 sha256:9b1f8c2e0d41
10:07:40  refused            clean                 -        0.0s   claude-code 2.1.0  5d20e1b9c4a7f863
          policy: task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server
10:09:00  incomplete         deploy env="staging"  -        -      codex 0.9.2        e4b6a8d0127c95f3
          no completion was recorded: the call is still running or the server stopped
```

- Header: `Agent actions on <day> (<zone>) for <Runefile>` for one day, or
  `Agent actions from <start> to <end> (<zone>) for <Runefile>` for a range, then an empty line.
  `<zone>` is the `--tz` value as given, `local` when the flag is absent, followed by a comma
  and the UTC offset at the start of the range, such as `(local, UTC+02:00)` or
  `(Europe/Kyiv, UTC+02:00)`. For `UTC` it is `(UTC)`. `<start>` and `<end>` are written
  `2006-01-02 15:04` in the display zone. A range with no `--since` starts at
  `the start of the log`, and one with no `--until` ends now.
- One line per action, oldest first: the start time in the display zone (`15:04:05` when the
  range is one day, `2006-01-02 15:04:05` otherwise), the outcome, the task with its effective
  arguments (supplied ones when there are no effective ones), `exit N` or `-`, the duration or
  `-`, the client name and version or `-`, and the invocation id.
- Arguments are written `name="value"`, a variadic as `name=["a","b"]`. The argument text of
  one line is cut at 80 characters with `...`.
- Detail lines, indented to the outcome column (10 spaces for a one-day listing), in this
  order when present: the error as
  `<kind>: <message>` (first line of the message, cut at 200 characters); the approval
  (`approved at the client`, `granted by the operator: <tasks>`, or
  `approval <decision>`), joined on one line with the plan by `; `; the plan (`plan <id> sha256:<first 12 hex>`, followed by ` stored` for a plan
  task); `parent <id>` for a call made through an agent task's endpoint; the incomplete line.
- An agent run is shown as `agent <task>`; an authentication failure as
  `auth-failure  <remote>` with `(+N not recorded)` when it carries a count; a request (only
  with `--all`) as `request  <method>`.
- With no action: the header, then `No agent actions.`
- Columns are padded with spaces to the widest value of the listing. No color.

Standard error:

- `note: "log" is also a task of this Runefile; run it with: rune -- log` when the Runefile has
  a task named `log`.
- `warning: 1 record in <file> could not be read and was skipped`, or
  `warning: N records in <file> could not be read and were skipped`, per damaged file.

## JSON output

`--json` prints the action objects of [audit-record.md](audit-record.md), one per line, oldest
first, and nothing else on standard output. With no action it prints nothing. Notes and
warnings still go to standard error.

## Exit codes

| Code | When |
|---|---|
| 0 | Actions were listed, or there were none |
| 1 | The log directory or a day file could not be read |
| 2 | A usage error, or no Runefile |

## Usage errors

| Input | Message |
|---|---|
| `--outcome nope` | `--outcome: unknown outcome "nope" (one of completed, failed, refused, invalid-arguments, unknown-tool, cancelled, timed-out, error, incomplete, ok)` |
| `--tz Mars/Olympus` | `--tz: unknown time zone "Mars/Olympus" (an IANA name such as Europe/Kyiv, UTC, or local)` |
| `--date 2026-13-01` | `--date: "2026-13-01" is not a day (YYYY-MM-DD)` |
| `--date` with `--since` or `--until` | `--date cannot be combined with --since or --until` |
| `--since soon` | `--since: "soon" is not a day (YYYY-MM-DD), a time (RFC 3339) or a duration (such as 2h or 7d)` |
| `--since` after `--until` | `--since is after --until` |
| `--dir` with another flag | `--dir cannot be combined with other flags` |
| an empty list item | `--task: empty value` |

## Environment

| Variable | Read by | Meaning |
|---|---|---|
| `RUNE_AUDIT_DIR` | servers and `rune log` | The base directory of the log, in place of the state directory. An absolute path: a relative one is refused, by a server and by `rune log` (exit 2) |
| `RUNE_AUDIT_RETENTION_DAYS` | servers | Days a day file is kept. Default `90`. `0` keeps every file |
| `XDG_STATE_HOME` | servers and `rune log` | The state directory, when `RUNE_AUDIT_DIR` is not set |
| `RUNE_INVOCATION_ID` | set by a server for the tasks of a call | The invocation id of the call that started the process |

## Server-side messages

Startup failures (exit 2, nothing served):

```text
audit log: RUNE_AUDIT_RETENTION_DAYS: "soon" is not a whole number of days, zero or more
audit log: RUNE_AUDIT_DIR must be an absolute path, not "../audit"
audit log: <dir> is inside the project (<project dir>); set RUNE_AUDIT_DIR to a directory outside it
audit log: <dir> is a symbolic link
audit log: <dir> belongs to another user
audit log: cannot create <dir>: <reason>; set RUNE_AUDIT_DIR to a writable directory
```

A call whose start could not be written (the tool result, also on standard error):

```text
audit log: this call was not run: the record of its start could not be written (<reason>). The log directory is <dir>. The operator must make it writable or set RUNE_AUDIT_DIR.
```

A call whose completion could not be written (a second text block of the result):

```text
[audit log] The completion of this call was not recorded (<reason>). The result above is the task's own. Do not retry the call because of this message.
```

Standard error, for the same failure and for any other record:

```text
warning: audit log: a record could not be written (<reason>); the log directory is <dir>
warning: audit log: an old day file could not be removed (<reason>)
```

An agent task whose run could not be recorded:

```text
warning: audit log: the agent run could not be recorded (<reason>); the agent runs without task tools
```
