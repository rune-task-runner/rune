# Data Model: Rune-Only Agent Mode Design Note

The feature stores no data. These are the things the note and the evidence are made of, and
the rules each must meet.

## Client

One of Claude Code, Codex CLI, Cursor, VS Code Copilot agent mode, Zed.

| Field | Rule |
|---|---|
| Version | Named for every client (FR-004). For a client read from source, the commit or tag too. |
| Run or not | "observed" appears only for Claude Code and Codex CLI. The other three are marked not tested where the summary table is introduced and in their own part (FR-009). |
| Mode | The way it was started: `claude -p`, `codex exec`. Interactive behavior is listed as not tested. |

## Command-running tool

A tool of the client through which an agent can run a command of its choosing.

| Field | Rule |
|---|---|
| Name | The name the client uses for it in its configuration. |
| Kind | Direct (runs the command) or indirect (starts something that can: a subagent, a scheduled or remote agent, a skill). |
| How it was found | An evidence label. |
| Removal | The control that removes or denies it, or "none found". |

A tool that only writes files is not one of these. It appears under enforcement boundaries.

## Control

A setting, option, profile or hook of a client.

| Field | Rule |
|---|---|
| Purpose | One of: register the server, remove or deny a tool, allow a tool. |
| Place | The file and key, or the command-line option. |
| Scope | Project, user, organization, a named profile or custom agent, a mode, or one way of starting the client (FR-006). |
| Effect | "removes the tool" (the model is not given it) or "refuses calls" (the model is given it and each call is refused). The note never writes one for the other. |
| Who can undo it | The levels that outrank it, and whether the agent can write the file. |

## Outcome

What a client's setup amounts to. Exactly three values, defined in
[contracts/setup-outcome.md](contracts/setup-outcome.md):

- **Rune-only**
- **Rune-only within a named scope**
- **Registered only**

The summary table gives every client one of them (FR-008). The follow-up text maps them to
supported, partial and not supported (FR-021).

## Approach

One way for Rune to bring a client to a Rune-only session. Four are evaluated (FR-012):
print the configuration, write it, redirect with a hook, launch the agent. Each has one
status: chosen, rejected or deferred, with a reason (FR-015). Exactly one is chosen.

## Enforcement boundary

Something a Rune-only session does not prevent.

| Field | Rule |
|---|---|
| What | What the agent or the user can still do. |
| Why | Why removing the shell does not prevent it. |
| What closes it | A setting, with its owner (Rune or the client), or "nothing in scope". |
| Evidence | A label, where the boundary rests on a client's behavior. |

The note has at least the seven of FR-017. A Rune flag is described as what Rune then
does, never as a restriction of the client (FR-019).

## Evidence label

| Label | Meaning |
|---|---|
| observed | Seen in a run on 2026-11-16 that has a file in `observations/`. |
| recorded | Seen in a session of the compatibility research, recorded under `test/integration/testdata/clients/`. |
| source | Read in the client's source code, not run. |
| docs | Stated in the vendor's documentation, or in the client's own `--help`. |
| staff | Stated by the vendor's staff on the vendor's forum. |
| unknown | No evidence found. |

## Observation

One run of a client, kept as one text file.

| Field | Rule |
|---|---|
| Name | A short id used in the note and the README: `c1` to `c12` for Claude Code, `x1` to `x7` for Codex captures, `y1` to `y5` for Codex runs, `a1` and `a2` for the `(agent)` executor. |
| Content | The tool list the client reported or would have sent, and the client's reply word for word, whichever the run produced. |
| In the README | The client version, the model, the project files, the command, the prompt, and what the run shows (FR-010). |
| Replaced | The project path, the path of the `rune` binary and the home directory. The tools of the account's connected apps are cut from Codex's lists. The README lists each (FR-027). |

Relationships: a statement labelled observed names one or more observation ids. An
observation belongs to one client. The README is the index from id to file.

## Follow-up text

What this research adds to issue #53.

| Part | Rule |
|---|---|
| Clients | Every client named in #46 or #53, with supported, partial or not supported, the scope and the reason (FR-021). |
| Adapter facts | For each supported or partial client: files, keys, tool names, scope, and what a merge must keep (FR-022). |
| Corrections | Each statement of #53 the research contradicts or sharpens, with the finding and its evidence (FR-023). |
| Verification | What a check must show before a setup is reported as Rune-only (FR-024). |
