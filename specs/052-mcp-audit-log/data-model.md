# Data Model: The Audit Log

**Feature**: 052-mcp-audit-log | **Spec**: [spec.md](spec.md) | **Research**: [research.md](research.md)

The stored line and the `rune log --json` object are specified field by field in
[contracts/audit-record.md](contracts/audit-record.md). This file gives the entities, their
relations and their rules.

## Entities

### Record

One stored line. Written once, never changed.

| Field | Rule |
|---|---|
| version | `1`. A reader skips, and counts as unreadable, a line with a version it does not know |
| type | `call`, `agent-run`, `request` or `auth-failure` |
| phase | `start` or `end` for `call` and `agent-run`. Absent for the single-record types |
| id | 16 hex digits, random. The same in the start and the end of one action |
| time | UTC, RFC 3339 with nanoseconds. The start time in a start, the end time in an end |

The other fields depend on type and phase (contract).

### Action

What a reader builds from records, and what `rune log` shows.

- A `call` or `agent-run` action is one start, plus the end with the same id when there is one.
- A `request` or `auth-failure` action is one record.
- An end with no start in the same file is counted as unreadable and not shown.
- A second start or end with an id already seen is counted as unreadable.

### Outcome

| Outcome | Applies to | Exit code |
|---|---|---|
| `completed` | call, agent-run | 0 |
| `failed` | call, agent-run | non-zero, or absent when the run gave none |
| `refused` | call | never |
| `invalid-arguments` | call | never |
| `unknown-tool` | call | never |
| `cancelled` | call, agent-run | only when a run produced one |
| `timed-out` | call | only when a run produced one |
| `error` | call, request | never |
| `ok` | request | never |
| `incomplete` | call, agent-run. Derived by the reader: a start with no end | never |

`auth-failure` has no outcome.

### Error

Present on an action that ran no task, or whose completion says why it stopped.

| Field | Rule |
|---|---|
| kind | `protocol`, `validation`, `policy`, `approval` or `internal` |
| message | Redacted, at most 2 KiB |

### Session

| Field | Rule |
|---|---|
| id | 16 hex digits derived from the server instance and the protocol session (R12) |
| transport | `stdio`, `http` or `agent-endpoint` |
| auth | `process` for stdio, `bearer-token` for `http` and `agent-endpoint` |
| client name, version | As the client reported them in `initialize`. Redacted. May be empty |

### Project identity

| Field | Rule |
|---|---|
| runefile | Resolved absolute path of the Runefile |
| definition digest | SHA-256 of the composed definition of the load the call ran against |
| config digest | SHA-256 of that load's settings, dotenv entries and overrides |
| git state | `commit`, `unborn`, `none` or `unavailable` |
| git commit | Only with state `commit` |
| git dirty | Only when git answered. Absent is "not known", never "clean" |

### Arguments

| Field | Rule |
|---|---|
| supplied | The call's arguments as received: a JSON value, redacted and bounded (R4). Absent when the call sent none |
| effective | Name to value, or name to list for a variadic, as the task was bound. Only when binding succeeded |

### Approval

| Field | Rule |
|---|---|
| asked | Whether a request was sent to the client |
| decision | `approved`, `declined`, `dismissed`, `not-given`, `timed-out`, `no-answer`, `cancelled`, `runefile-changed` or `cannot-ask` |
| tasks | The tasks the request covered |

### Grants

The tasks of the call (itself and what it reaches) that are destructive and named in the
operator's `--allow-destructive`. Absent when there is none.

### Plan

| Field | Rule |
|---|---|
| role | `stored` for a plan task's call, `applied` for an apply call that claimed a plan |
| id | The plan's id |
| digest | Hex SHA-256 of the artifact |

### Parent

The id of the action this one happened inside: an `agent-run` for a call made through that
run's endpoint; a `call` for an `agent-run` started under `RUNE_INVOCATION_ID`. Absent
otherwise.

## Relations

```text
Session 1 ── n Action(call | request)
Action(agent-run) 1 ── n Action(call)          via parent
Action(call) 1 ── 0..1 Action(agent-run)       via parent, across a process boundary
Action(call) 1 ── n plan journal lines         via Event.Invocation (spec 051 journal)
Project directory 1 ── n day file 1 ── n Record
```

## Lifecycle of a tool call

```text
request arrives
  └─ middleware: snapshot, id, identity, redacted supplied arguments
       └─ write START ── fails ─▶ result: audit error; nothing runs; no record
            └─ SDK lookup ── unknown ─▶ mark unknown-tool
                 └─ handler: authorization, decode, validation ── stop ─▶ mark kind
                      └─ engine: bind, policy, plan checks, approval ── stop ─▶ mark kind
                           └─ run ─▶ exit code
       └─ write END (outcome, exit code, effective arguments, approval, plan, error)
            └─ fails ─▶ result unchanged + one text block; warning on standard error
```

A server that dies anywhere after START leaves START alone: the action reads `incomplete`.

## Validation rules

- An exit code is stored only from a `Result` the engine returned. No other path sets it.
- A record is at most 64 KiB. Supplied arguments are the only unbounded input, and R4 bounds
  them before encoding. When a record would still be larger, the supplied arguments are
  replaced by a count of the bytes left out.
- Every stored string passed the snapshot engine's `Redact`, or is produced by Rune from fixed
  words (outcome, kind, decision, transport), or is a digest or an id.
- The log directory is never inside the Runefile's directory or the working directory.
- Day file names match `YYYY-MM-DD.jsonl` exactly. Retention touches nothing else.
