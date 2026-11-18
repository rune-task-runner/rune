# Contract: The Stored Record and the `rune log --json` Object

Two shapes with the same field names. A stored line is one **record**. `rune log --json` prints
one **action** per line, which is the start record and the end record of one id merged.

## Files

```text
<base>/<project id>/YYYY-MM-DD.jsonl
```

- `<base>`: `RUNE_AUDIT_DIR` (an absolute path), else `$XDG_STATE_HOME/rune/audit` (when that
  variable is an absolute path; a relative one is ignored), else
  `%LocalAppData%\rune\audit` on Windows, else `~/.local/state/rune/audit`.
- `<project id>`: the first 16 hex digits of the SHA-256 of the resolved Runefile path.
- The day is the UTC day the action **started** on. The end of an action is in the file of its
  start.
- Directories have mode `0700`, files `0600`. `project.json` in the project directory holds
  `{"runefile":"<path>"}`.
- One record per line, UTF-8, ending with a line break. `<`, `>` and `&` are not escaped.
- A line is never changed or removed. A file is removed whole by retention.

## Record fields

Every record:

| Field | Type | Meaning |
|---|---|---|
| `v` | number | Record version, `1` |
| `type` | string | `call`, `agent-run`, `request`, `auth-failure` |
| `phase` | string | `start` or `end`. Only for `call` and `agent-run` |
| `id` | string | 16 hex digits. Joins a start and its end |
| `time` | string | UTC, RFC 3339 with nanoseconds |

`call`, phase `start`:

| Field | Type | Meaning |
|---|---|---|
| `session` | string | Session id |
| `parent` | string | Id of the agent run whose endpoint received the call. Absent otherwise |
| `transport` | string | `stdio`, `http`, `agent-endpoint` |
| `auth` | string | `process` or `bearer-token` |
| `client` | object | `name`, `version`: as the client reported them. Either may be absent |
| `tool` | string | The tool name the call asked for |
| `task` | string | The task that tool names. Absent for an unknown tool |
| `arguments.supplied` | any JSON | The arguments as received, redacted and bounded. Absent when none were sent |
| `project.runefile` | string | Resolved path of the Runefile |
| `project.definitionDigest` | string | `sha256:<hex>` of the composed definition |
| `project.configDigest` | string | `sha256:<hex>` of the configuration |
| `project.git.state` | string | `commit`, `unborn`, `none`, `unavailable` |
| `project.git.commit` | string | Only with state `commit` |
| `project.git.dirty` | boolean | Only when git answered |
| `sandbox.enforced` | boolean | `false`. No sandbox exists yet (issue #51) |

`call`, phase `end`:

| Field | Type | Meaning |
|---|---|---|
| `durationMs` | number | Whole milliseconds on a monotonic clock |
| `outcome` | string | `completed`, `failed`, `refused`, `invalid-arguments`, `unknown-tool`, `cancelled`, `timed-out`, `error` |
| `exitCode` | number | Only when a run produced one |
| `arguments.effective` | object | Name to string, or to an array of strings for a variadic. Only when binding succeeded |
| `error.kind` | string | `protocol`, `validation`, `policy`, `approval`, `internal` |
| `error.message` | string | Redacted, at most 2 KiB |
| `approval.asked` | boolean | Whether a request was sent to the client |
| `approval.decision` | string | `approved`, `declined`, `dismissed`, `not-given`, `timed-out`, `no-answer`, `cancelled`, `runefile-changed`, `cannot-ask` |
| `approval.tasks` | array | The tasks the request covered |
| `grants` | array | Destructive tasks of the call that the operator's grant allowed |
| `plan.role` | string | `stored` or `applied` |
| `plan.id` | string | The plan's id |
| `plan.digest` | string | `sha256:<hex>` of the artifact |

`agent-run`, phase `start`: `task`, `parent` (the inherited `RUNE_INVOCATION_ID`, when set),
`project` as above. Phase `end`: `durationMs`, `outcome` (`completed`, `failed`, `cancelled`),
`exitCode` when the agent gave one, `error` when it failed.

`request`: `session`, `transport`, `auth`, `client`, `method`, `durationMs`, `outcome` (`ok` or
`error`), and `error` with kind `protocol` when it failed.

`auth-failure`: `transport` (`http`), `remote` (the remote address), `suppressed` (number of
failures not recorded since the last record, absent when zero).

## Redaction and bounds

- Every string in `arguments`, `error.message`, `client` and `approval.tasks` passed the masking
  of the load the call ran against. A masked secret reads `***`.
- A value whose name matches the secret-name rule (case-insensitive substring: `TOKEN`,
  `SECRET`, `PASSWORD`, `PASSWD`, `APIKEY`, `API_KEY`, `PRIVATE_KEY`, `ACCESS_KEY`,
  `CREDENTIAL`, `AUTH`) is stored as `***`, whatever it is. This applies at any depth of
  `arguments.supplied` and to `arguments.effective`. The rule errs on the side of hiding: a
  parameter named `author` matches `AUTH` and is hidden too.
- A string longer than 1024 bytes is cut at a character boundary and ends with
  `[N bytes cut]`.
- `arguments.supplied` lists at most 64 names per object and 64 elements per array. The rest
  are counted under the name `[more]` (objects) or as a last element `[N more]` (arrays).
- Nesting deeper than 8 levels is replaced by `[nested]`.
- A record is at most 64 KiB. One that would be larger stores `arguments.supplied` as
  `[N bytes not stored]`.

## Never stored

Bearer tokens, request headers, the protocol session id, environment variables, task output,
fix suggestions, plan and summary bytes, protocol message bodies and responses, a person's
name.

## The action object (`rune log --json`)

One object per line. Fields of the start and the end record merged, with these changes:

| Field | Meaning |
|---|---|
| `schemaVersion` | `1`. Replaces `v` |
| `start` | The start record's `time`. Replaces `time` |
| `end` | The end record's `time`. Absent for an incomplete action |
| `outcome` | As recorded, or `incomplete` when there is no end |
| `phase` | Not printed |
| `client.reported` | `true`, on every action that has `client`: the name is the client's own claim |

For `request` and `auth-failure`, `start` is the record's time and there is no `end`.

Example:

```json
{"schemaVersion":1,"type":"call","id":"b81e04f7a2c9d310","start":"2026-11-18T08:05:02.114Z","end":"2026-11-18T08:05:24.214Z","durationMs":22100,"outcome":"completed","exitCode":0,"session":"5f0e7a11c3d2b694","transport":"stdio","auth":"process","client":{"name":"claude-code","version":"2.1.0","reported":true},"tool":"apply","task":"apply","arguments":{"supplied":{"env":"prod"},"effective":{"env":"prod"}},"approval":{"asked":true,"decision":"approved","tasks":["apply"]},"plan":{"role":"applied","id":"7f3c9a1e52b84d06","digest":"sha256:9b1f...c2e0"},"sandbox":{"enforced":false},"project":{"runefile":"/repo/Runefile","definitionDigest":"sha256:41aa...","configDigest":"sha256:77c0...","git":{"state":"commit","commit":"07afc00d...","dirty":false}}}
```

## Stability

- New fields may be added to version 1. A consumer ignores fields it does not know.
- A field is never removed or given another meaning within version 1.
- New outcome, kind and decision values may be added. A consumer treats an unknown value as
  itself, not as success.
- A change that breaks these rules gets a new `v` and `schemaVersion`.

## Reading rules

- A line that is not JSON, has an unknown `v`, or lacks `type`, `id` or `time` is unreadable.
- An end whose start is not in the same file, and a repeated start or end, are unreadable.
- Unreadable records are skipped, counted per file and reported on standard error:
  `warning: 1 record in <file> could not be read and was skipped`.
- Every other record is shown.
