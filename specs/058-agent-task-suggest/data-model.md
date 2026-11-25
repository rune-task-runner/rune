# Data Model: Learn Rune Tasks from an Agent's Shell Commands

Types are named as they will be in Go. Packages: `internal/observe` (events, records, the
store, diagnostics), `internal/tasksuggest` (executions, groups, suggestions, text), and
additions to `internal/agentinit`, `internal/audit`, `internal/taskimport` and
`internal/cli`.

## observe.Event

One decoded hook input. Only the fields of research R1 are decoded.

| Field | Type | From |
|---|---|---|
| `Name` | string | `hook_event_name` |
| `Tool` | string | `tool_name` |
| `ToolUse` | string | `tool_use_id` |
| `Session` | string | `session_id` |
| `Cwd` | string | `cwd` |
| `Command` | string | `tool_input.command` |
| `Description` | string | `tool_input.description` |
| `Background` | bool | `tool_input.run_in_background` |
| `Error` | string | `error` (failure only; only its first line is used) |
| `Interrupt` | bool | `is_interrupt` |

`DecodeEvent(r io.Reader) (Event, error)` reads at most `MaxEventBytes` (4 MiB) and decodes.
It returns a `*DropError{Reason}` for input over the limit or that is not JSON.

## observe.Record

What is stored, one JSON line (research R5).

| Field | JSON | Rule |
|---|---|---|
| `V` | `v` | always 1 |
| `Time` | `time` | UTC |
| `Client` | `client` | `"claude"` |
| `Session` | `session` | 1 to 128 bytes, no control characters |
| `ToolUse` | `toolUse` | 1 to 128 bytes, no control characters |
| `Event` | `event` | `pre`, `success` or `failure` |
| `Exit` | `exit,omitempty` | `*int`; only for `failure` with `Exit code N` |
| `Interrupted` | `interrupted,omitempty` | |
| `Background` | `background,omitempty` | |
| `Cwd` | `cwd,omitempty` | relative to the root, forward slashes, `""` for the root |
| `Command` | `command` | redacted, 1 to 4096 bytes |
| `Description` | `description,omitempty` | redacted, one line, at most 200 bytes |

`MaxRecordBytes = 8 << 10`. `Record.Encode()` fails for a line over it.

### Building a record

`observe.NewRecord(ev Event, root string, secrets Secrets, now time.Time) (Record, error)`:

1. `Tool != "Bash"` or `Name` not one of the three: `ErrIgnored` (not a drop, not counted).
2. `ToolUse` or `Session` empty, too long or with a control character, or `Command` empty:
   drop, reason `invalid-event`.
3. `Cwd` not inside `root`: drop, reason `outside-project`.
4. Omission (research R6 step 1): reasons `unparsable`, `here-document`, `too-long`,
   `control-character`.
5. Redaction (R6 steps 2 and 3) and the description cut.

### observe.Secrets

What redaction needs from the project.

| Field | Type | Notes |
|---|---|---|
| `Names` | `func(string) bool` | true for a secret name, by `mask.SecretName` with the project's declared and exempt names |
| `Values` | `*mask.Set` | the values to mask, derived as for task output |

Built in `internal/cli` (`recordSecrets`) from the project's loaded module when there is a
Runefile, and from the recorder's environment alone otherwise.

## observe.Store

One project's records.

| Field | Type | Notes |
|---|---|---|
| `dir` | string | `<state>/rune/observe/<audit.ProjectID(root)>` |
| `base` | string | `<state>/rune/observe` |
| `now` | `func() time.Time` | for tests |
| `lockWait` | `time.Duration` | 2 s |

Operations:
- `OpenStore(base, root string) *Store` makes no directory; the first write does. `root` is
  already resolved (research R3).
- `Append(rec Record) error`: refuse a directory inside the root, `audit.PrivateDir`, lock,
  `compact` when the line would take the file past 4 MiB or the first record is older than
  30 days, append one line, unlock.
- `Read() ([]Record, Stats, error)`: every record not older than 30 days, oldest first;
  `Stats.Damaged` counts lines that do not decode. No lock: a compaction replaces the file by
  rename, so a reader sees the old file or the new one.
- `Prune() error`: the same compaction as `Append`'s, under the lock.
- `Forget() (int, error)`: under the lock, counts the records, removes `events.jsonl` and
  `diagnostics.json`, then the lock and the directory.

Constants: `MaxStoreBytes = 4 << 20`, `CompactTarget = 3 << 20`, `Retention = 30 * 24h`.

## observe.Diagnostics

Counts of events that were not recorded.

| Field | JSON | Notes |
|---|---|---|
| `Reasons` | `reasons` | `map[string]Count` |

`Count{N int64; Last time.Time}`, JSON `{"n":…,"last":…}`.

Reasons: `invalid-event`, `not-json`, `too-large`, `slow-input`, `no-project`, `outside-project`,
`runefile-unreadable`, `unparsable`, `here-document`, `too-long`, `control-character`,
`store-busy`, `store-error`.

`CountDrop(dir, reason string, now time.Time) error` reads, increments and replaces
`diagnostics.json` under the directory's lock. A failure to count is ignored: the recorder
has nowhere else to say it, and it never fails the hook.

`ReadDiagnostics(dir string) (Diagnostics, error)` is used by `rune suggest`, for the project
and for the shared file.

## tasksuggest.Execution

| Field | Type | Notes |
|---|---|---|
| `ToolUse` | string | |
| `Session` | string | |
| `Cwd` | string | |
| `Command` | string | |
| `Description` | string | |
| `Outcome` | `Outcome` | `Attempt`, `Succeeded`, `Started`, `Failed`, `Interrupted` |
| `Exit` | `*int` | for `Failed` |
| `Last` | `time.Time` | the latest record's time |

`Executions(recs []observe.Record) []Execution` follows research R8.

## tasksuggest.Group

| Field | Type | Notes |
|---|---|---|
| `Dir` | string | after the `cd DIR &&` rule |
| `Text` | string | canonical command |
| `Runs` | int | executions with an outcome |
| `Outcomes` | `map[Outcome]int` | |
| `Attempts` | int | |
| `Sessions` | int | distinct sessions among runs |
| `Last` | `time.Time` | the latest run |
| `Descriptions` | `map[string]int` | runs per description |
| `ReviewReason` | string | empty for a supported command |

## tasksuggest.Suggestion

| Field | Type | Notes |
|---|---|---|
| `ID` | string | 8 hex digits, 16 on a clash (R11) |
| `Name` | string | valid, distinct (R11) |
| `Description` | string | |
| `Dir` | string | |
| `Body` | string | canonical text, or the family's body with `{{quote(NAME)}}` |
| `Param` | `*Param` | for a family |
| `Groups` | `[]*Group` | one, or the family's |
| `Runs`, `Sessions`, `Last`, `Outcomes` | | over the groups |

`Param{Name, Default string; Values []ValueCount}`: the values with their runs, most first.

`(*Suggestion).Task() string` renders the task text (R12).

## tasksuggest.Review

A listed group that is not a suggestion: the group and its reason. No ID, no task text.

## tasksuggest.Report

What `rune suggest` lists.

| Field | Type | Notes |
|---|---|---|
| `Commands` | int | runs recorded |
| `Attempts` | int | attempts without an outcome |
| `Sessions` | int | |
| `First`, `Last` | `time.Time` | |
| `Suggestions` | `[]*Suggestion` | ordered (R11) |
| `Reviews` | `[]Review` | ordered |
| `Covered` | `[]Covered` | groups left out because a task matches: task name and runs |
| `Damaged` | int | lines of the store that did not decode |

`tasksuggest.Build(recs []observe.Record, tasks []TaskInfo, opts Options) Report`, where
`TaskInfo{Name, Dir, Body string; OneLine bool}` comes from the analyzed Runefile, and
`Options{MinRuns int; Taken func(string) bool}`.

`tasksuggest.Append(old []byte, picked []*Suggestion) []byte` returns the new Runefile text
(R12).

## agentinit observation plan

| Name | Type | Notes |
|---|---|---|
| `ObserveCommand` | const | `"rune agent record"` |
| `ObserveEvents` | var | `PreToolUse`, `PostToolUse`, `PostToolUseFailure` |
| `ObservePath` | const | `.claude/settings.local.json` |
| `PlanObserve(in ObserveInputs) (File, []string, error)` | func | the file and the notes |

`ObserveInputs{Local, Shared []byte; LocalExisted, Remove bool}`: the two settings files'
bytes. A `*ParseError` comes back for a file that is not valid JSON or not an object.

`removeItem(doc []byte, parent *jsonNode, i int) []byte` is the new splice of the JSON editor.

## State changes

```text
hook event --decode--> Event --NewRecord--> Record --Append--> events.jsonl
                              \--drop----> diagnostics.json (count per reason)

events.jsonl --Read--> []Record --Executions--> []Execution --Build--> Report
Report + ID list --Append--> new Runefile text --analyze--> written, or nothing
```

A record never changes after it is written. Compaction and `forget` only remove records.
