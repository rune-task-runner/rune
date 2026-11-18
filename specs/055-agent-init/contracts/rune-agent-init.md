# Contract: the `rune agent init` command

## Synopsis

```text
rune agent init <client> [--file <Runefile>] [--unpinned] [--force] [--dry-run | --check]
```

`<client>` is `claude`, `codex` or `vscode`.

| Flag | Meaning |
|---|---|
| `-f`, `--file <path>` | Set up the project of this Runefile instead of the one found from the working directory |
| `--unpinned` | Register `rune mcp` without the Runefile's path, for a file that is committed |
| `--force` | Replace a value the command owns that is already there with another value |
| `--dry-run` | Print the difference for each file and the report; write nothing |
| `--check` | Say whether the setup is in the project's files; write nothing, start nothing |

`--check` with `--force` or with `--dry-run` is a usage error. `--unpinned` with a Runefile
whose name discovery does not find is a usage error.

## `rune agent`

`rune agent` with no argument prints its help and exits 0. With an argument that is not a
subcommand it is a usage error:

```text
unknown command "deploy" for "rune agent"; to run a task named agent, use: rune -- agent
```

When the Runefile found from the working directory has a task named `agent`, `rune agent` and
`rune agent init` print on standard error:

```text
note: "agent" is also a task of this Runefile; run it with: rune -- agent
```

## What runs

Nothing of the Runefile. The Runefile is found and analyzed. A real run and a dry run start
the client program once with `--version` and stop it after five seconds, and only after the
plan was made: a run that stops on a conflict or on a file it cannot read starts no program.
`--check` starts no program. No network is used.

## Streams

- Standard error: the report, diagnostics, and the final error line.
- Standard output: with `--dry-run`, the unified difference of each file that would change.
  Nothing otherwise.

## The report

Lines of the form `kind: text`, in this order. Paths are relative to the project directory,
with forward slashes.

```text
note: "agent" is also a task of this Runefile; run it with: rune -- agent     (only when true)
<path>: created: <what was set>
<path>: updated: <what was set>
<path>: unchanged
client: <Name> <version> (checked: <versions>)
outcome: <outcome>
reason: <why>                 (Registered only)
remains: <tools> (<why>)      (Registered only)
step: <text>                  (one per pending step)
note: <text>                  (one or more)
check: <text>                 (one or more)
dry run: nothing written      (--dry-run only)
```

When the run stops on a conflict, a file that cannot be parsed, or a file that needs a setting
added by hand, none of these lines is printed: the output is the diagnostics and the final
error line. No outcome is named for a setup that was not written.

### File lines

`<what was set>` is a list joined with `; ` of:

- `server "rune" runs: rune mcp --file <path> --no-reload` when the server was added.
  `<path>` is quoted for a shell when it needs it.
- `added <key> to server "rune"` when the entry was there without that key.
- `replaced <key> of server "rune"` when `--force` replaced a value.
- `deny <names>` and `allow <names>`, with only the rules that were added.
- `features.shell_tool = false`, `agents.enabled = false`, and `replaced <key>` for Codex.
- `custom agent "rune" with tools rune/*`, or `replaced the custom agent "rune"`.

### The client line

```text
client: Claude Code 2.1.326 (checked: 2.1.325, 2.1.326)
client: Codex CLI 0.170.0 (checked: 0.167.0, 0.169.0)
client: Claude Code, no version found (checked: 2.1.325, 2.1.326)
client: VS Code 1.146.0 (checked: none)
```

The checked versions are a list of exact versions, joined with `, `. A version keeps its
pre-release part, so `0.169.0-alpha.2` is not the checked `0.169.0`.

### The outcome line

```text
outcome: Rune-only, pending 2 steps
outcome: Rune-only, pending 1 step
outcome: Rune-only within the custom agent "rune", pending 2 steps
outcome: Registered only, pending 2 steps
```

No other wording. Every adapter has one or more steps, so every outcome is pending.
`Registered only` is followed by:

```text
reason: this version was not checked
reason: no version was found
reason: no VS Code version has been checked with these files
remains: Bash, PowerShell, Monitor, Workflow, CronCreate, RemoteTrigger (not checked for this version)
remains: exec_command, write_stdin, and the subagent tools spawn_agent, wait_agent, send_input, resume_agent, close_agent, followup_task, interrupt_agent, list_agents, send_message (not checked for this version)
remains: runInTerminal, runTask, createAndRunTask, runNotebookCell, runSubagent, vscode/runCommand (kept by every agent but "rune", and not checked for it)
```

An adapter with no checked version gives its own reason whatever version was found.

### Steps

Claude Code:

```text
step: trust the project: start claude in <dir> and accept the trust question. Until then the deny rules apply and the allow rule does not: the session has no shell and Rune's tools are refused.
step: approve the server "rune" when Claude Code asks, and start a new session. Until then the session has no Rune tools.
```

Codex CLI:

```text
step: trust the project: start codex in <dir> and accept the trust question, then start a new session. Until then Codex ignores .codex/config.toml: the session has its shell and no Rune tools.
```

VS Code:

```text
step: open <dir> in VS Code, trust the workspace and start the server "rune" when VS Code asks. Until then the session has no Rune tools.
step: choose the agent "rune" in the Chat view. Every other agent keeps the terminal.
```

`<dir>` is the project directory, quoted for a shell when it needs it.

### Notes

```text
note: the server keeps the tasks it read at start (--no-reload). After an edit to the Runefile, start a new session.
note: [confirm] tasks are not approved by this setup. Rune refuses them, or asks you for each call.
note: the server is registered with this checkout's path. For a file you commit, use --unpinned.
note: the server serves the Runefile found from the directory the client starts it in.      (--unpinned, in place of the line above)
note: a task an agent calls runs in Rune's sandbox, which needs bubblewrap on Linux. Where there is none, each call is refused until the server runs with --sandbox off.
note: .claude/settings.json keeps the deny rule mcp__rune__deploy: that tool stays denied.
note: .claude/settings.json keeps the ask rule mcp__rune: Claude Code asks before those calls.
```

### The manual check

Claude Code:

```text
check: claude -p hi --output-format stream-json --verbose
check: the "tools" list in the first line of its output has names that start with mcp__rune__ and none of: Bash, PowerShell, Monitor, Workflow, CronCreate, RemoteTrigger
```

Codex CLI:

```text
check: codex exec "Call the rune tool of one task. Then say whether you have a tool that runs a shell command, and whether you have a tool that starts a subagent."
check: Codex prints no tool list, so the answer is the model's: a Rune task ran, and it has neither tool
```

VS Code:

```text
check: in the Chat view choose the agent "rune" and open its tools: only the rune tools are selected, and no terminal tool is
```

## `--check`

One line per owned entry, then one summary line, then the manual check:

```text
.mcp.json: mcpServers.rune.type: present
.mcp.json: mcpServers.rune.command: present
.mcp.json: mcpServers.rune.args: differs
.claude/settings.json: permissions.deny: Bash: missing
...
incomplete: 2 of 10 entries are missing or different. Run: rune agent init claude
check: ...
```

or, when every entry is present:

```text
complete: all 10 entries are present
check: ...
```

No `client:` line and no `outcome:` line: the client's version was not read. The entries are
compared with what a run with the same options would write, so a setup made with
`--unpinned` is checked with `--unpinned`, and the run the `incomplete:` line names then
carries `--unpinned` too. A conflict at an owned key is the state `differs`.
A conflict that is not about an owned entry (the deny rule `mcp__rune`, a value of the wrong
type) is printed as a diagnostic, as in a real run, and the summary line is then

```text
incomplete: the setup has a conflict
```

## Diagnostics

A conflict, a file that cannot be parsed and a file that needs a setting added by hand are
printed as Rune prints a Runefile error: the message, `file:line:col`, the line and a caret.

| Case | Message | Exit |
|---|---|---|
| Unknown or missing client | `unknown client "cursor": the clients are claude, codex and vscode`; with none: `rune agent init needs a client: claude, codex or vscode` | 2 |
| `--check` with `--force` or `--dry-run` | `--check cannot be combined with --force or --dry-run` | 2 |
| `--file` with an empty value | `--file needs a path` | 2 |
| A Runefile path that is not valid UTF-8, without `--unpinned` | `the path of the Runefile is not valid UTF-8, so it cannot be written into a client file; register the server without it: --unpinned` | 2 |
| `--unpinned` with a Runefile discovery does not find | `--unpinned needs a Runefile that rune finds by its name; <name> is not one` | 2 |
| No Runefile | the existing discovery error | 2 |
| Runefile with errors | its diagnostics, then `N error(s)` | 3 |
| Client file not valid | `<file>:<line>:<col>`: the parser's message | 3 |
| Owned key with another value | `<key> is <found>; rune agent init writes <wanted>. Edit the file, or run again with --force` | 2 |
| Wrong type on the way | `<key> is not an object` or `<key> is not a list`, and `rune agent init cannot add to it` | 2 |
| Deny rule `mcp__rune` or `mcp__rune__*` | `permissions.deny holds <rule>, which denies every Rune tool. rune agent init never removes a deny rule` | 2 |
| Agent file with other content | `.github/agents/rune.agent.md has other content. Run again with --force to replace it` | 2 |
| A file that cannot be extended in place | At the `file:line:col` of the construct that defines the table another way (line 1 when it cannot be named): `rune agent init cannot add to this file in place. Set this yourself: <key> = <value>`, for the first setting that cannot be added | 2 |
| A path that leaves the project | `<path> leads outside the project, to <target>. rune agent init writes only inside the project` | 2 |
| A file that cannot be read | the system's message with the path | 1 |
| A file that cannot be written | the system's message with the path; the files already written in the run are put back | 1 |
| A planned file that fails its own verification | `<file>: the planned change did not verify; this is a bug in rune agent init` | 1 |

The final error line:

| Situation | Line | Exit |
|---|---|---|
| Conflicts | `agent init: N conflict(s), nothing written` | 2 |
| Outcome "Registered only" | `agent init: the outcome is "Registered only"` | 1 |
| `--check` found the setup not complete | `agent init: the setup is not complete` | 1 |

## Exit status

| Status | Meaning |
|---|---|
| 0 | The outcome is "Rune-only" or "Rune-only within SCOPE". With `--check`: every owned entry is present |
| 1 | The outcome is "Registered only"; or `--check` found the setup not complete; or a file could not be read or written |
| 2 | Usage error, no Runefile, a conflict, a file that needs lines added by hand, or a path that leaves the project |
| 3 | The Runefile has errors, or a client file is not valid JSON or TOML |

Only with status 0, and with status 1 for "Registered only", were files written.

## Guarantees

1. No file outside the project directory is created or changed.
2. No task, hook or `[context]` task runs.
3. A run that would change nothing writes nothing.
4. All files of a client are planned and verified before the first is written, and a failed
   write puts back what the run already wrote.
5. `--dry-run` and `--check` create no file and no directory. `--check` starts no program.
6. Output is the same for the same inputs.
