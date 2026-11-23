# Research: Learn Rune Tasks from an Agent's Shell Commands

Decisions for spec 058. Each has the decision, why, and what was rejected. Facts about
Claude Code come from its hooks reference (https://code.claude.com/docs/en/hooks, read
2026-11-23); facts about Rune come from the code at commit `4b07941`.

## R1. Which hook events, and what they carry

**Decision**: install handlers on `PreToolUse`, `PostToolUse` and `PostToolUseFailure`, each
with matcher `Bash`. The recorder reads these input fields and ignores every other one:

| Field | Events | Use |
|---|---|---|
| `hook_event_name` | all | which of the three; anything else is ignored |
| `tool_name` | all | must be `Bash`; anything else is ignored |
| `tool_use_id` | all | joins the events of one call (R8) |
| `session_id` | all | counts sessions |
| `cwd` | all | the working directory the command started in |
| `tool_input.command` | all | the command |
| `tool_input.description` | all | the agent's short description |
| `tool_input.run_in_background` | all | a background command's success says only that it started |
| `error` | failure | its first line `Exit code N` gives the exit code; the rest is never read |
| `is_interrupt` | failure | the call was interrupted |

`tool_response` (the command's stdout and stderr on success) is never read past the JSON
decoder, and nothing of it is kept.

**Why**: the reference documents these three events and fields for the Bash tool. The
failure event says "Exit code N" on its first line when the command ran, and has no
exit-code line when the shell could not start; the success event documents no exit code at
all, so a success has none. `PreToolUse` runs "before every tool call, whether or not it
needs permission", and "permission denials fire `PreToolUse` but not" the failure event, so
a pre-execution event without an outcome is exactly an attempt (FR-023).

The reference says a `Bash` matcher also fires `PreToolUse` for shell commands the Monitor
tool starts. Those events have another `tool_name` or no outcome event with matcher `Bash`;
either way they are not counted as runs. Only `tool_name` equal to `Bash` is recorded.

**Rejected**:
- Only the two outcome events: simpler, but an attempt that was denied would be invisible,
  and the issue asks to tell attempts from runs.
- `PermissionDenied`: it fires only for the auto-mode classifier, not for deny rules.
- The transcript or shell history: the issue rules them out.
- The `PowerShell` tool: its commands are not POSIX shell, so Rune cannot parse them as
  data with the same parser that runs task bodies.

## R2. The handlers, where they go, and how they are removed

**Decision**: `rune agent observe claude` edits `.claude/settings.local.json` in the project
root. For each of the three events it makes sure one handler exists whose `command` is
exactly `rune agent record`. When none exists, it appends one matcher group to the event's
list:

```json
{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }
```

`--remove` deletes every handler whose `command` is exactly `rune agent record` under those
three events. Then it deletes any group whose `hooks` list became empty because of that, and
any event key whose list became empty. The `hooks` object itself stays. Each removal is one
splice found on a fresh parse, repeated until no handler is left, so every step is the
simplest case.

The file is edited as text with `internal/agentinit`'s JSON editor (`parseJSON`,
`insertItems`, `applySplices`), which keeps comments, trailing commas, key order and layout.
The editor gains one operation, `removeItem(doc, parent, i)`: it deletes member or element
`i` of an object or list with the comma and white space that belong to it. The plan is
verified like `verifyJSON` verifies `agent init`: the decoded new document must equal the
decoded old one with exactly Rune's handlers added or removed.

The run reads `.claude/settings.json` too, without changing it. When either file's
`permissions.deny` holds `Bash` or `Bash(*)`, the report notes that the agent cannot run
shell commands, so nothing will be recorded while that rule stands (FR-006).

**Why**:
- `settings.local.json` is the person's own file for one project; the reference calls it
  "gitignored when Claude Code saves a setting to it". Writing to `.claude/settings.json` would turn
  recording on for everyone who clones the project, and the user-wide file would record
  every project.
- The command is a fixed string with no path or quoting, so it is the same on every shell
  and still right after the project moves (R3). The `args` form of a handler would avoid a
  shell, but the reference does not say since when clients support it, and a client that
  ignored `args` would run `rune` with no arguments.
- `async: true` runs the handler in the background. The reference says async hooks "can't
  block or control Claude's behavior", so the recorder cannot change a command's outcome
  even when it is slow or broken (FR-011). No `timeout` is set: none applies to async hooks.
- A group of its own per event makes removal touch nothing of the person's groups.

**Rejected**:
- `rune agent init claude --observe`: `agent init` needs a Runefile, which a cold-start
  project does not have, and it denies the Bash tool, so the hook would record nothing.
- Adding Rune's handler into an existing `Bash` group of the person's: removal would then
  edit the person's group.

## R3. The project root

**Decision**: the root is found as every `rune` command finds the project: the directory of
the Runefile that `config.Discover` finds from a starting directory, or the starting
directory itself when there is none (`config.ErrNotFound`). The starting directory is:

- for `rune suggest` and `rune agent observe`: the current directory, or the directory of
  the file named with `--file`;
- for `rune agent record`: `CLAUDE_PROJECT_DIR`, which the reference documents as "the
  project root where the session started". When it is empty or not absolute, the event is
  not recorded and the shared diagnostics count it.

A directory holding two Runefile names (`*config.AmbiguousError`) is treated as a Runefile
that cannot be read (FR-018).

Both sides resolve the root with `filepath.EvalSymlinks` and clean it before using it, and
the recorder resolves the event's `cwd` the same way before relating it to the root. The
same directory can be named in several ways: macOS gives `/var/...` to one program and
`/private/var/...` to another, a project can be reached through a symbolic link, and Windows
paths differ in the drive letter's case. Without resolving, the two sides would hash
different strings and use different stores. A `cwd` that cannot be resolved is used cleaned
as given.

**Why**: one rule on both sides means the recorder and `rune suggest` agree whenever the
client is started where the person runs `rune`. The event's `cwd` follows the agent's `cd`,
so it cannot name the project.

**Rejected**: writing the absolute root into the handler: it needs quoting for each shell,
and a moved project would keep recording under the old path.

## R4. The store

**Decision**: records live in `<state>/rune/observe/<id>/events.jsonl`. `<state>` is the
state directory the audit log uses (`$XDG_STATE_HOME` when absolute, `%LocalAppData%` on
Windows, `~/.local/state` otherwise). `<id>` is `audit.ProjectID` of the root's absolute
path: 16 hex digits of its SHA-256. Per-project diagnostics are in `<id>/diagnostics.json`,
and the shared ones in `<state>/rune/observe/diagnostics.json`.

Directories are created 0700 and checked like the audit log's: not a symbolic link, owned by
the current user. Files are 0600. Every write holds the directory's lock file, taken as the
audit log takes it, with a 2-second wait (FR-019). A record is one line appended with one
`Write` to a file opened `O_APPEND`.

Bounds (FR-021):
- A record line is at most 8 KiB. With a command of at most 4096 bytes and a description of
  at most 200 bytes, every valid record fits; a longer line is a bug and is not written.
- Before an append, under the lock, the recorder compacts the file when the new line would
  take it past 4 MiB or its first record is older than 30 days. Compaction keeps the newest
  records that are at most 30 days old and fit in 3 MiB, writes them to a temporary file and
  renames it over the old one. The file is therefore never larger than 4 MiB. The slack
  between 3 and 4 MiB keeps a full store from being rewritten on every event.
- A store directory that resolves inside the project root is refused, as the audit log
  refuses a log directory inside the project; the event is counted as `store-error`.
- `rune suggest` ignores records older than 30 days and runs the same compaction when it
  finds the file over a bound.

The code reuses the audit package rather than copying it. `internal/audit` exports three
helpers taken out of existing code without changing its behavior:
- `StateDir`, which returns `<state>/rune`, the directory `DefaultBase` adds `audit` to;
- `PrivateDir`, the directory checks of `ensureDir`;
- `LockDir`, its lock.

**Why**: one file per project is the simplest store that the 4 MiB bound and "oldest first"
can be applied to exactly. The audit log's day files remove whole days, so one busy day
could not be bounded without dropping new records. Reusing the audit helpers keeps one copy
of the directory, ownership and lock rules.

**Rejected**:
- Reusing `audit.Log` itself: its records are audit records, its project hint names a
  Runefile, and it has no size bound.
- A store inside the project (`.rune/`): an agent with the project's files could read or
  change it, and it could be committed.

## R5. The record

**Decision**: one JSON object per line:

| Key | Type | Notes |
|---|---|---|
| `v` | number | 1 |
| `time` | string | RFC 3339 UTC, when the recorder ran |
| `client` | string | `claude` |
| `session` | string | `session_id`, at most 128 bytes |
| `toolUse` | string | `tool_use_id`, at most 128 bytes |
| `event` | string | `pre`, `success` or `failure` |
| `exit` | number | only for `failure` with an `Exit code N` line |
| `interrupted` | bool | only when true |
| `background` | bool | only when true |
| `cwd` | string | relative to the root with forward slashes; empty for the root |
| `command` | string | redacted (R6) |
| `description` | string | redacted, one line, at most 200 bytes; omitted when empty |

An id longer than 128 bytes or holding a control character makes the event invalid
(counted), never cut: two cut ids could merge two calls.

**Why**: everything `rune suggest` needs, and nothing else (FR-013).

## R6. Redaction and omission

**Decision**: the recorder parses the command with `mvdan.cc/sh/v3/syntax` (R7) and never
runs anything. In this order:

1. **Omit** when: the parse fails; the tree holds a here-document (`syntax.Redirect` with
   `<<` or `<<-`); the command is longer than 4096 bytes; or it holds a control character
   other than tab and line feed. Nothing of the text is kept and the diagnostics count the
   reason.
2. **Replace by position**, walking the tree, each value with `***`:
   - the value of an assignment, as a prefix (`NAME=value cmd`) or after `export`,
     `declare`, `local`, `readonly` or `typeset`, whose name is a secret name;
   - the value of a flag whose name, with leading dashes removed and `-` turned into `_`,
     is a secret name: the rest of the word after `=`, or else the next word;
   - in a word whose literal text starts, ignoring case, with `Authorization:`,
     `Proxy-Authorization:`, or `NAME:` where NAME is a secret name, everything after the
     colon and its spaces;
   - in a word holding `://`, the password of `user:password@`, and the user part of an
     http(s) URL with no password, where a token goes (`https://ghp_x@github.com`);
   - the value of a `NAME=value` argument, alone or after `--flag=`, whose NAME is a secret
     name (`docker run -e API_TOKEN=x`, `--build-arg=API_TOKEN=x`);
   - the password given to a well-known program by a flag whose name is not a secret name:
     after the colon of `curl -u user:password` (and `--user`, `-U`, `--proxy-user`), after
     an attached `-p` of `mysql`, `mysqldump`, `mysqladmin`, `mariadb` and `mariadb-dump`,
     the value of `sshpass -p`, and of `-p` after `docker login` or `podman login`.
   A secret name is decided exactly as `mask.NewSet` decides it for an environment variable:
   declared in `set secrets`, or matching a built-in pattern, and neither listed in
   `set unmasked` nor one of the built-in exemptions (`GIT_AUTHOR_NAME` and the like).
   `internal/mask` exports that rule as `SecretName(name string, declared, exempt []string)
   bool`, the function `NewSet` itself uses. Flag names go through the same rule, so a flag
   such as `--author` (it holds `AUTH`) has its value replaced too: the command becomes a
   review item, which is the safe side of the trade.
   Replacements are applied to the original text by the byte offsets the parser gives, so
   the rest stays exactly as written.
3. **Mask values**: the whole command and the description go through the project's
   `mask.Set`, the same set `rune` derives for task output (`deriveMaskSet` over the
   environment, the tasks' `[env]` values and the declared names), loaded with the
   Runefile's settings and dotenv files when there is a Runefile, from the recorder's
   environment alone otherwise.

The description is cut to its first line and at most 200 bytes, at a character boundary,
after masking.

**Why**: the rules of step 2 catch secrets that are not in the environment, written straight
into a command. Step 3 catches the values Rune already knows are secret. Masking by value
after the positional rules means a value the rules missed but the environment knows is still
caught. A command that cannot be parsed cannot be redacted with confidence, so it is not
kept at all; here-document bodies are often whole files (`.env`, keys) and are never kept.

A value that is only a reference such as `$TOKEN` is replaced too. The task that comes out
of such a command would be a review item (FR-030), which is the conservative result; the
person writes that task by hand.

**Rejected**: recognizing secrets by the shape of a value (`ghp_…`, `AKIA…`): Rune does not
do that for task output either, and a partial list would suggest a guarantee it cannot give.

## R7. Parsing shell as data

**Decision**: `syntax.NewParser()` with its default variant (Bash), as
`internal/runtime/shell` uses to run task bodies and `internal/taskimport` uses to read
imported commands. Canonical text comes from `syntax.NewPrinter()`: it keeps every word's
quoting and normalizes only the spacing between words, operators and redirections.

**Why**: the same parser that will run the task body decides what the command is, so a
command that parses here is one Rune's shell can read. Nothing is expanded: the tree is
only walked and printed.

## R8. Executions and outcomes

**Decision**: `rune suggest` reads the records (oldest first), keeps the first record per
(`toolUse`, `event`) pair, and folds the records of one `toolUse` into an execution:

- the command, description and `cwd` come from the outcome record when there is one, else
  from the `pre` record;
- outcome: `failed` when there is a `failure` record without `interrupted` (with its exit
  code, if any); `interrupted` when the `failure` record has it; `started` for a `success`
  record with `background`; `succeeded` for a `success` record otherwise; `attempt` with
  only a `pre` record. Failed wins over interrupted, both over the two success outcomes.

A run is an execution whose outcome is not `attempt` (FR-024).

**Why**: tool-use ids are unique per call, so the id alone joins the events whatever order
the background handlers finished in. Keeping the first record of each kind makes a repeated
delivery count once.

## R9. Groups and what is listed

**Decision**:
1. **Normalize**: parse the stored command; when the leftmost command of a chain of `&&` is
   `cd DIR` with DIR one word whose literal value is a relative path that, joined with the
   run's `cwd` and cleaned, stays inside the root, drop that `cd DIR &&` and use the joined
   directory. The rest is printed canonically (R7).
2. **Group** runs by (directory, canonical text).
3. **Leave out** a group when any simple command in it has a first word whose base name is
   `rune` or `rune.exe`. These are not counted.
4. **Match tasks**: a group whose command matches an existing task is left out and counted
   (FR-029). A task matches when its body is one line (after an `@` or `-` prefix), its
   directory is the root or its `[working-directory]`, and its body line, with each `{{…}}`
   interpolation replaced by a placeholder word, parses into the same simple command as the
   group's, where a placeholder word matches any plain word (R10) and `--flag=<placeholder>`
   matches `--flag=` and a plain value. A body with any other shape matches only by equal
   canonical text. Tasks come from the analyzed Runefile with its imports.
5. **Threshold**: a group with at least `--min-runs` runs (default 3) is listed.
6. **Classify** (FR-030): a group is a review item when its command spans lines, holds the
   redaction mark `***`, or has a node of a kind outside the supported set: `syntax.CallExpr`
   (with assignments, words and redirections), `syntax.BinaryCmd` with `&&`, `||`, `|` or
   `|&`, and words made of literals, quotes and plain parameter expansions (`$NAME`,
   `${NAME}`). It is also a review item when the first word of any call is a state-changing
   built-in (the list in FR-030). Its reason is the first such finding, worded for a person.

**Why**: every step reads the tree only. The `cd DIR &&` rule covers the agent's most common
compound command while keeping its meaning: the task runs in DIR through
`[working-directory]`, and a missing DIR fails the task as it failed the `cd`.

## R10. One varying word becomes a parameter

**Decision**: among the groups that are suggestions (not review items) and are one simple
command, take each word position `i` from the second word on; the program never varies. Groups with the same directory, the same number of
words, the same assignments and redirections, and the same words everywhere except `i` form
a family when they hold at least two different values at `i`, every value is plain, and their
runs together reach the threshold. A plain value matches `^[A-Za-z0-9_./:@%+,=-]{1,256}$`
and does not start with `-`. When every value starts with the same `--flag=` text, only the
part after it is the value.

Families are picked by most runs, then lowest position, then first canonical text, and a
group joins at most one family. The parameter's name is the flag before position `i` (or in
`--flag=`), with leading dashes removed, `-` turned into `_`, lowercased; it is `value` when
there is no flag, when the result is not a valid name, or when it is a keyword. Its default
is the value with most runs, the latest on a tie. The body is the canonical text with the
word at `i` replaced by `{{quote(NAME)}}` (or `--flag={{quote(NAME)}}`).

**Why**: one varying plain word is the case where quoting, argument boundaries, environment
and directory provably stay the same: `quote()` hands the shell exactly one word, as the
plain value was one word. Anything wider is left to exact groups.

## R11. IDs, names, descriptions, order

**Decision**:
- **ID**: the first 8 hex digits of SHA-256 over `v1`, the directory and the canonical text
  (for a family, the body text with the placeholder). When two listed suggestions share 8
  digits, both use 16.
- **Name** (FR-033): the leading words of the first simple command that match
  `^[a-z][a-z0-9_-]*$`, up to three, joined with `-`; `task` when there is none. A name that
  is a keyword, a rune command (`taskimport.RuneCommands`), an existing task's name or an
  earlier suggestion's gets `-2`, `-3`, and so on, in listing order. Keywords are checked
  with `token.Lookup`, as `rune import` checks them.
- **Description** (FR-034): the description with most runs in the group, the latest on a
  tie; otherwise `Run` and the name's words, with a final period.
- **Order** (FR-037): runs descending, latest run descending, ID ascending.

**Why**: an ID from the content stays the same while new records arrive or the Runefile
changes, so a person applies what they reviewed. Names are recomputed against the current
Runefile on every run, so they never clash.

## R12. Task text and the appended block

**Decision**: a suggestion renders as:

```rune
# <description>
[working-directory("<dir>")]
<name> <param>="<default>":
    <body>
```

The `[working-directory]` line appears only for a directory other than the root, and the
parameter only for a family. The body is the canonical text with `{{` and `}}` escaped as
`rune import` escapes them (`escapeBraces`), so the task runs the recorded text. String
literals (the directory, the default) are written as `rune import` writes them (`quote`).
Those two helpers move from `internal/taskimport` to exported functions of that package,
used by both.

Appended to an existing Runefile: when the file does not end with a line feed, one is added;
then one blank line, then the tasks separated by blank lines. An existing file that is empty
or only white space gets the tasks with no separator in front. The line ending is `\r\n` when
the file holds one, `\n` otherwise.

## R13. Applying

**Decision**: `rune suggest apply ID...`:
1. finds the root and reads the Runefile's bytes (none is fine);
2. refuses a Runefile that is a symbolic link (`os.Lstat`);
3. analyzes the current Runefile and refuses on errors, printing them (exit 3);
4. computes the suggestions as `rune suggest` would and resolves every ID; an unknown ID or
   one of a review item stops the run before anything is written (exit 2);
5. builds the new text and analyzes it with `analysis.AnalyzeRequest{URI, Content}`, as
   `rune import`'s `checkGenerated` does; an error is a bug and stops the run (exit 1);
6. with `--dry-run`, prints the unified diff and stops;
7. reads the file again; when the bytes differ from step 1, or a file appeared where there
   was none, stops (exit 2);
8. writes: over an existing file with `writeFileAtomic` (temporary file, same permissions,
   rename); a new file as a temporary file linked to `Runefile` with `os.Link`, so a file
   that appeared in between is never replaced, then the temporary name is removed.

The unified diff is `agentinit`'s, exported as `agentinit.UnifiedDiff`.

**Why**: the only window left is between steps 7 and 8, a few microseconds. Editors do not
take Rune's locks, so no lock could close it.

## R14. Commands

**Decision**:

| Command | Flags | Notes |
|---|---|---|
| `rune agent observe claude` | `--remove`, `--dry-run`, `-f/--file` | under `rune agent` |
| `rune agent record` | none | hidden; reads standard input; always exits 0 |
| `rune suggest` | `--min-runs N`, `-f/--file` | top-level |
| `rune suggest apply ID...` | `--dry-run`, `--min-runs N`, `-f/--file` | |
| `rune suggest forget` | `-f/--file` | |

`suggest` is added to `taskimport.RuneCommands`, with a Long-help sentence and a note when the
Runefile has a task named `suggest`, as `agent` and `export` have. `rune suggest WORD` with an
unknown WORD is a usage error that names `rune -- suggest`.

`rune agent record` discards anything cobra would print, reads at most 4 MiB plus one byte
from standard input, and returns nil whatever happens. Every failure is a diagnostics count.
The read happens in a goroutine the command waits on for at most 5 seconds; past that the
event is counted as `slow-input` and the command returns, which ends the process and the
goroutine with it. A hook has no timeout when it runs in the background, so without this a
client that never closed the input would leave one process per shell call behind.

**Why**: the hook entry belongs with the client setup it serves. The person's commands sit
under one name, `suggest`, as the issue names it.

## R15. Testing

**Decision**:
- Hook fixtures under `internal/observe/testdata/hooks/`, built from the reference's
  documented examples: the three events for one call, a failure with `Exit code 1`, a
  failure without an exit-code line, an interrupt, a background command, an event for
  another tool, one without `tool_use_id`, malformed JSON.
- Unit tests in `internal/observe` (event decoding, redaction table, omission, store
  append, compaction, retention, the lock with concurrent goroutines, diagnostics) and
  `internal/tasksuggest` (executions, normalization, grouping, exclusions, task matching,
  classification, families, names, IDs, rendering, order).
- `internal/agentinit` tests for install, idempotence, removal, comments and other hooks kept,
  the deny-rule note, and `removeItem` on every list and object shape.
- `internal/cli` tests for the three `suggest` paths and both `agent` subcommands, with apply
  edge cases: no Runefile, CRLF, no final line feed, symbolic link, a change between read and
  write, analysis errors, an unknown ID.
- `test/integration`: the real binary, in an empty directory, with `XDG_STATE_HOME` and
  `CLAUDE_PROJECT_DIR` set: `rune agent observe claude`, fixtures piped into
  `rune agent record` (the 14-run set, a sentinel command, every secret fixture),
  `rune suggest`, `apply --dry-run`, `apply`, `rune analyze`, `forget`, `--remove`. It
  checks that the sentinel never exists and that no secret value is in any output or file
  under the state directory.

**Why**: the issue's acceptance criteria, one test layer each, without running a recorded
command anywhere.
