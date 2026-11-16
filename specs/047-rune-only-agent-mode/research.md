# Research: Rune-Only Agent Mode

The findings themselves are in `docs/design/rune-only-agent-mode.md` and in
[observations/README.md](observations/README.md). This file records how the research was
done and the choices behind it.

Done on 2026-11-16, on Linux x86_64, with Claude Code 2.1.325, Codex CLI 0.167.0 and a
`rune` built from this checkout. Cursor, VS Code and Zed are not installed on the machine.

## R1. Read every client, run the two that are installed

**Decision**: One reading pass per client, over the vendor's documentation and, where the
client is open source, its source at a named commit or tag. Then runs of Claude Code and
Codex CLI against the scenario Runefile of the compatibility research
(`test/integration/testdata/clients/scenario`).

**Rationale**: Issue #53 says of itself that it rests on "research and local CLI help
inspection only". The question here is whether a configuration really leaves the agent
without a shell, and reading cannot answer that. A run can, for the clients that run here.
For the other three, reading is all there is, and the note says so.

**Alternatives considered**: Reading only: leaves the main claim unproven for every
client. Installing Cursor, VS Code and Zed: they need a display the machine does not have,
and installing software for a research note changes the machine.

**What was checked twice**: The reading passes returned summaries. Every statement the
note's decision rests on was then checked at the page or file itself: the Cursor hooks and
CLI permissions pages, the Cursor forum post on custom modes, Zed's
`settings_content/src/project.rs` and `agent_settings/src/agent_profile.rs`, VS Code's
`chatSelectedTools.ts` and `copilotPluginConverters.ts`, the VS Code custom agent
reference and approvals page, and the Codex configuration reference. One summary was wrong
and was corrected: Cursor's CLI permission entries take glob patterns, they are not exact
strings.

## R2. A sixth evidence label: observed

**Decision**: The note uses the compatibility table's labels (recorded, source, docs,
staff, unknown) and adds **observed**: seen in a run on 2026-11-16 whose client version,
configuration, command and output are kept in `observations/`.

**Rationale**: The compatibility table defines "recorded" as seen in a session recorded on
the wire between the client and `rune mcp`. Whether a client has a shell tool is not on
that wire: it is in the tool list the client gives the model. The evidence here is a
different kind, so it gets its own name, and "recorded" keeps one meaning across the
documentation.

**Alternatives considered**: Calling these runs "recorded": two meanings for one label.
Recording them with the wire recorder too: the recorder shows calls that reached Rune, not
the absence of a shell tool, and its fixtures are held to cases by a Go test that would
have to grow for no gain.

## R3. The tool list is taken from the client, not from the model

**Decision**: For Claude Code, the tool list is the `tools` array of the `system/init`
event that `claude -p --output-format stream-json --verbose` prints before the first
request. For Codex, which prints no such list, a stand-in endpoint
(`observations/codex/capture.py`) logs the request Codex would send a model and answers
with an error; `tools.py` lists the tools in it. A second Codex run with the real account
shows what the model did with those tools.

**Rationale**: A model asked "do you have a shell?" can be wrong in both directions. The
client's own list cannot. In Codex 0.167.0 with the default model the list is not flat: the
model gets one `exec` tool that runs JavaScript in an isolate, and the other tools are
nested inside it as functions, so `exec_command` has to be looked for in that tool's
description. The capture shows it there, and shows it gone with `shell_tool` off.

**Limit**: The capture uses a provider that is not the account's, so the model catalogue is
the bundled one. The real runs (`y1` to `y5`) agree with it on every point that was
compared: a shell command ran in `y1`, and the model reported no shell tool and no subagent
tool in `y2`.

**Alternatives considered**: Asking the model only: shown wrong above. Reading the Codex
session log: its format is not documented and it does not list tools.

## R4. Observations run in a temporary project outside the user's configuration

**Decision**: Each run uses its own copy of the scenario in a directory under `/tmp`
(four runs, c12, x6, x7 and y5, use a directory inside this repository, because they need
a project the client already trusts), with
project files (`.mcp.json`, `.claude/settings.json`) or command-line options
(`-c key=value`, `--disable`). Claude Code runs load project settings only
(`--setting-sources project`) and only the given MCP server (`--strict-mcp-config`). Codex
runs ignore the user configuration (`--ignore-user-config`).

**Rationale**: FR-026. It also keeps the user's plugins, hooks and servers out of the tool
lists.

**A consequence worth keeping**: A fresh directory is a project the client does not trust.
That is how the research met two findings it was not looking for: Claude Code ignores a
project's `permissions.allow` there and applies its `permissions.deny`, and Codex ignores
the whole project file there (compatibility research). Both are in the note.

**A mistake worth keeping**: The first runs used a directory under `~/.claude`. Claude Code
refused the file edits there as edits to "a sensitive file", which is a rule about that
directory and not about a Runefile. Those runs were discarded and repeated under `/tmp`.
The README says so.

## R5. The note is a page of the documentation; the evidence stays with the spec

**Decision**: `docs/design/rune-only-agent-mode.md`, in a new navigation group "Design
notes". The observations and the follow-up text are in this spec directory.

**Rationale**: An operator needs the note today, to configure a client by hand, and the
MCP guide links to it. A design note that only maintainers can find would not correct the
impression the issue is about. The evidence is a record of one day's research and is not
maintained, which is what a spec directory is for.

**Alternatives considered**: A new section of `docs/mcp.md`: that page is 700 lines and is
a guide to what Rune does, while most of the note is about what clients do and about a
command that does not exist yet. The note in the spec directory only: not on the site, and
the guide would link into `specs/`. Evidence under `test/integration/testdata/clients/`:
`TestClientRecordings` fails for a file there without a case.

## R6. The approach: write project configuration, report one of three outcomes

**Decision**: Rune produces a client's configuration with the command issue #53 proposes,
`rune agent init <client>`, which writes project-scoped files and shows the change first.
Printing the configuration, which issue #46 proposes as `rune agent-config`, is that
command's dry run and not a second command. The command reports one of three outcomes,
defined in [contracts/setup-outcome.md](contracts/setup-outcome.md).

**Rationale**:

- In the two clients that can remove their shell, the control is a setting a project file
  can carry: `permissions.deny` in Claude Code's `.claude/settings.json` (observed),
  `features.shell_tool` in Codex's `.codex/config.toml` (observed).
- A project file covers every session started in the project, with a terminal and without
  one, and it is reviewed like code.
- Merging by hand into a JSON file that already has `deny` rules, or a TOML file with
  comments, is where a printed block goes wrong. A command that merges and shows a diff
  does not ask the user to do that.
- The three outcomes make "registration alone must not be reported as shell restriction"
  something the command's output can be tested for.

**Alternatives considered**:

- *Print only (`rune agent-config`)*: rejected as a separate command. It leaves the merge
  to the user and cannot report an outcome, because it does not know what the file will
  hold. Kept as the dry run.
- *A hook that redirects a shell command to the matching Rune tool*: rejected as the
  restriction. No client's hook can replace a call with a call to another tool: Claude Code
  (docs), Codex (source), Cursor (docs) and VS Code (docs) can refuse a call with a message,
  and some can change its input. A refusal with a message did lead the model to call the
  Rune tool (observed in Claude Code), but where the shell can be removed the hook adds
  nothing, and where it cannot the hook is a second process that must not fail open.
  Matching a command to a task is also unsound beyond the literal `rune <task>`: a task has
  parameters, a working directory and dependencies that a command line does not show.
- *Rune launches the restricted agent*: deferred. Command-line options outrank project
  files in both installed clients, need no trust step, and are what an `(agent)` task
  needs: `agent_cmd` with `claude --restricted` and the executor's endpoint gave an agent
  with Rune's tools and no shell (observed). But it covers only clients that are started
  from a command line, and only sessions started through Rune. The launch lines go in the
  note now; a launcher command is left for a later issue.

## R7. Which clients the first implementation supports

**Decision**: Claude Code and Codex CLI as Rune-only. VS Code as Rune-only within a named
custom agent, and only after someone has observed it. Cursor and Zed not supported.

**Rationale**: Claude Code and Codex remove the tool and were observed doing it. VS Code
has no workspace setting that removes the terminal tools (source, docs); a custom agent's
tool list is an allowlist the user can widen in the tools picker (source), and nothing was
run. Cursor has had no way to remove the terminal tool since custom modes were removed in
2.1 (staff); a hook can refuse shell calls, which is not removal. Zed reads agent profiles
and tool permissions from the user's settings only (source), so a project file can register
Rune and nothing more.

**Effect on the two issues**: #46 names `cursor` and #53 names `vscode`. Neither is
Rune-only for a project. The follow-up text says so and gives each its outcome.

## R8. The follow-up issue is #53, and its text is a file here

**Decision**: `follow-up-issue-53.md` holds what to add to issue #53. The pull request does
not edit the issue.

**Rationale**: Issue #46 names #53 as "the concrete implementation follow-up", so creating
another issue would split it. Keeping the text in the pull request puts it under the same
review as the note, which is the issue's verification target. Posting it is one command
for the maintainer, given in the quickstart.

**Alternatives considered**: Editing #53 from the pull request branch: a change outside the
repository that review cannot see or revert. A new issue: a duplicate of #53.

## R9. What was not done

- No interactive session of any client. An approval dialog, and the trust prompt, were not
  seen.
- No run of Cursor, VS Code or Zed.
- Codex's hooks, its `requirements.toml`, and Claude Code's managed settings were read, not
  run: they need files outside a project.
- Claude Code on Windows, where `PowerShell` is the second shell tool, was not run.
- Whether a Codex subagent inherits `shell_tool = false` was not tested; the note turns
  subagents off instead.
