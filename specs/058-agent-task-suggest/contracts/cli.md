# Contract: Commands

Streams: a listing and a diff go to standard output. Reports, notes and errors go to
standard error. Exit status: 0 success, 1 failure of the command, 2 usage error, unknown ID
or conflict, 3 a settings file or Runefile that cannot be read or analyzed.

Every command finds the project root as research R3 says, and none runs a recorded command
or a task.

## rune agent observe claude

```text
rune agent observe claude [--remove] [--dry-run] [-f FILE]
```

Turns recording on for this project, or off with `--remove`, by editing
`.claude/settings.local.json` (see [hooks.md](hooks.md)). Works without a Runefile.

On:

```text
.claude/settings.local.json: updated
observing: the shell commands Claude Code runs in this project are recorded on this
  machine, without their output, for up to 30 days. See them with: rune suggest
```

The first line's status is `created`, `updated` or `unchanged`. Off:

```text
.claude/settings.local.json: updated
not observing: what was recorded stays until it expires; delete it with: rune suggest forget
```

Notes, each on a line of its own after the report:

- when `.claude/settings.json` or `.claude/settings.local.json` denies `Bash` or `Bash(*)`:
  `note: Claude Code's settings deny the Bash tool here, so the agent runs no shell command and nothing is recorded. Rune does not change that rule.`
- on: `note: start Claude Code in <root>, the project this file belongs to; commands are recorded for that project.`

`--dry-run` prints the unified diff and the report with `would be` before the status, and
writes nothing. A client other than `claude` is a usage error:
`rune agent observe supports claude only, not "codex"`. A settings file that is not valid JSON is
reported like `rune agent init` reports it, with its line, and exits 3.

## rune agent record

```text
rune agent record
```

Hidden. Reads one hook event from standard input, records it or counts why not, writes
nothing to standard output or standard error, and exits 0 in every case. Arguments are
ignored.

## rune suggest

```text
rune suggest [--min-runs N] [-f FILE]
```

Lists what the agent repeated. `N` is 1 to 1000, default 3; another value is a usage error.

```text
Recorded 212 commands in 4 sessions, 2026-11-02 to 2026-11-23.
1 attempt has no recorded outcome.

[3f9a1c2e] the agent ran this 14 times (12 succeeded, 2 failed) in 3 sessions, last on 2026-11-23:
    go test -run TestX ./pkg/...
  suggested task:
    # Run the parser tests.
    go-test:
        go test -run TestX ./pkg/...

[5be09f12] the agent ran this 9 times with 3 values (9 succeeded) in 2 sessions, last on 2026-11-22, in web:
    go test -run TestA ./...   (5 runs)
    go test -run TestB ./...   (3 runs)
    go test -run TestC ./...   (1 run)
  suggested task:
    # Run go test.
    [working-directory("web")]
    go-test-2 run="TestA":
        go test -run {{quote(run)}} ./...

For review, not turned into a task:
  ran 5 times: go test $(go list ./...)
    reason: it uses a command substitution

Already tasks: vet (10 runs).
Not recorded: 3 events (here-document 2, too-long 1).

Apply with: rune suggest apply ID...
```

Rules:
- Dates are UTC days; a single day reads `on 2026-11-23`. Counts use the singular for 1
  (`1 time`, `1 session`, `1 run`).
- The outcome list names only non-zero outcomes, in this order: succeeded, failed,
  interrupted, started in the background.
- `, in DIR` appears for a directory other than the root, and a review item in another
  directory ends with ` (in DIR)`.
- A family lists at most 5 values with their runs, most first, then `and N more`.
- The task text is indented by 4 spaces under `suggested task:`.
- Review reasons are one of:
  `it spans more than one line`, `a value was removed as a secret; write this task by hand`,
  `it uses a command substitution`, `it uses a process substitution`,
  `it uses arithmetic expansion`, `it runs a subshell`, `it runs a group in braces`,
  `it uses <keyword>` for `if`, `for`, `while`, `until`, `case` and `select`,
  `it defines a function`, `it runs commands in sequence with ;`,
  `it runs a command in the background with &`, `it changes the shell's state with <name>`,
  `it uses a shell feature Rune does not turn into a task`.
- The `Already tasks`, `Not recorded` and attempts lines appear only when non-zero. `Not
  recorded` adds the shared diagnostics to the project's.
- When lines of the store did not decode, after `Not recorded`:
  `Skipped 2 damaged lines in the store.`
- With no records: `Nothing recorded for <root>.` and
  `Turn recording on with: rune agent observe claude`, exit 0.
- With records and nothing listed or already a task: the header, then
  `No command ran N times or more. Lower the bar with --min-runs.`, exit 0.
- A Runefile with a task named `suggest` adds the note
  `note: "suggest" is also a task of this Runefile; run it with: rune -- suggest` on
  standard error, as `agent` and `export` do.
- A Runefile with errors: the errors, exit 3.

## rune suggest apply

```text
rune suggest apply ID... [--dry-run] [--min-runs N] [-f FILE]
```

Adds the tasks of the given suggestions to the Runefile, or creates it.

```text
Runefile: updated, added go-test, go-test-2
note: these tasks have no attributes, so an agent can call them through rune mcp without
  approval. Add [confirm] to a task that changes state.
```

Status is `created` or `updated`. `--dry-run` prints the unified diff on standard output and
`Runefile: would be updated, adding go-test` on standard error, and writes nothing.

Errors (nothing is written):

| Case | Message | Exit |
|---|---|---|
| no ID | `rune suggest apply needs the ID of a suggestion; list them with: rune suggest` | 2 |
| unknown ID | `no suggestion has the ID 1234abcd; list them with: rune suggest (and the same --min-runs)` | 2 |
| Runefile is a symbolic link | `Runefile is a symbolic link to <target>; edit that file, or apply in its directory` | 2 |
| Runefile changed meanwhile | `Runefile changed while the tasks were being added; nothing written. Run the command again` | 2 |
| Runefile has errors | the errors, then `fix the Runefile first` | 3 |
| generated text does not analyze | the errors, then `the added tasks are not valid; this is a bug in rune suggest` | 1 |

## rune suggest forget

```text
rune suggest forget [-f FILE]
```

Deletes this project's records and diagnostics: `deleted 212 records for <root>`
(`1 record` for one), or `nothing recorded for <root>`. Exit 0. It does not touch the settings or the Runefile.

## Reserved name

`suggest` is a built-in command. `rune suggest WORD` for any WORD other than `apply` and
`forget` is a usage error:
`unknown command "WORD" for "rune suggest"; to run a task named suggest, use: rune -- suggest`.
