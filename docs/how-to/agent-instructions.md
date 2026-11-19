# Export instructions for agents

> Write the tasks of your Runefile into `AGENTS.md` or a Claude skill, for an agent that has
> no MCP integration, and keep that text current with a check in CI.

An agent that reaches Rune [through MCP](../mcp.md) is given your tasks as tools. An agent
that has only a shell learns your tasks from the instruction files it reads, and somebody
has to write them down there. `rune export` writes that text from the Runefile, so the
Runefile stays the one place where a task's name, description and parameters are defined.

```sh
rune export agents-md             # print the section for AGENTS.md
rune export agents-md --write     # create or update AGENTS.md in the project
rune export agents-md --check     # is AGENTS.md current? exit 1 when it is not
rune export claude-skill --write  # write the skill .claude/skills/rune-tasks/
```

The command reads the Runefile and the files it imports. It runs no task and no hook, and
it evaluates nothing: no default, no variable, no setting. It does not read the environment
or a dotenv file, so no value from either can reach the text.

## What is exported

For this Runefile:

```rune
# Run the tests.
test:
    @./scripts/test.sh

# Deploy the service.
[confirm("Deploy now?")]
[agent-allow(env="staging")]
deploy env:enum("staging","prod") region="eu-west-1":
    @./scripts/deploy.sh {{quote(env)}} {{quote(region)}}
```

`rune export agents-md` prints a general text on how tasks are called, and one entry per
task:

```markdown
### `test`

Run the tests.

- Usage: `rune test`

### `deploy`

Deploy the service.

- Usage: `rune deploy <env> [region]`
- Example: `rune deploy staging`
- `env`: required, one of `"staging"`, `"prod"`.
- `region`: optional, default `"eu-west-1"`.
- Asks a person to confirm: `"Deploy now?"`. Ask the person to run it.
- The author allows agents only `env` = `"staging"`.
```

An entry holds what a caller needs:

| Line | From |
|---|---|
| The description | the comment above the task, or `[doc]` |
| Usage | the task's name and parameters: `<name>` required, `[name]` optional, `...` several values |
| Example | a command with values Rune knows: the value allowed for agents, a literal default, the first value of an enum, `true` for a boolean |
| One line per parameter | its type, the values of an enum, its default, and its `[param-doc]` text |
| Group, Runs only on, Returns | `[group]`, the operating system attributes, `[returns]` |
| Asks a person to confirm | `[confirm]`, with the question |
| The author allows agents only | `[agent-allow]` |
| A run can reach | a task this one runs through dependencies and hooks that has `[confirm]` or `[agent-allow]` |
| Makes a plan, Applies the plan | `[plan]` and `[applies]` |
| Declared read-only, Declared to use the network | `[readonly]` and `[network]` |

Which tasks have an entry:

- Every task that `rune --list` can show: not `[private]`, and no name that starts with `_`.
- Not the `[context]` task, which is never something to call.
- Tasks of modules, under their full name: `ops::restart` is run with `rune ops::restart`.
- Tasks limited to another operating system, with a "Runs only on" line. The file is read on
  machines other than the one that generated it, so the export does not depend on the host.

A task whose name is also a command of Rune, such as `log`, has the usage `rune -- log`.

A default that is not a literal is shown as its expression, such as
`` `env("TAG", "latest")` ``, with the words "worked out when the task runs". An entry has
an example only when Rune knows a value for every argument the example needs. No value is
invented. The examples are written for a POSIX shell (`sh`, `bash`, `zsh`), with a value in
single quotes where a shell would split or change it.

## Guidance, not enforcement

The export states what you declared, and it tells an agent what to do about it. It enforces
nothing:

- An agent with a shell can run any command, whatever the file says.
- `[confirm]` stops a task at the command line when no person answers. The export tells the
  agent to ask the person, and never names a way to skip the question.
- `[agent-allow]` is checked for calls through the MCP server. At the command line
  `rune deploy prod` runs for anyone. The export tells the agent which value you allow.

The general text of every export says so in these words: "This text is guidance. It does not
limit what a shell can run. Rune enforces confirmations and the values allowed for agents
only for calls through its MCP server, `rune mcp`."

To enforce the limits, give the agent your tasks [through MCP](../mcp.md), where a
`[confirm]` task is refused or put to you and the value policy is checked. To take the
client's shell away, see [Set up an agent client](agent-setup.md). The two go together with
the export: the setup decides what the agent can do, and the export tells an agent without
the setup what it should do.

## AGENTS.md

`rune export agents-md --write` writes `AGENTS.md` next to the Runefile:

```text
$ rune export agents-md --write
AGENTS.md: created: 2 tasks, 1830 bytes
```

The section stands between two marker lines:

```markdown
<!-- rune:begin tasks fingerprint=sha256:a99f...5884 -->
<!-- Generated by `rune export agents-md` from the Runefile. ... -->

## Rune tasks
...
<!-- rune:end tasks -->
```

Only the lines from `rune:begin tasks` to `rune:end tasks` are Rune's. Everything else in
the file is yours and is kept byte for byte:

| The file | What `--write` does |
|---|---|
| does not exist | creates it with the section |
| has text and no marker line | adds the section at the end |
| has one begin line and, after it, one end line | replaces the lines from the one to the other |
| has any other set of marker lines | stops with the file and the line of each marker, and writes nothing (exit 2) |

Move the section by moving the two marker lines and what is between them. A second run with
an unchanged Runefile writes nothing and reports `AGENTS.md: unchanged`. The section uses
the line endings of the file.

`--output <path>` writes the section to another Markdown file by the same rules:

```sh
rune export agents-md --output docs/agents/tasks.md
rune export agents-md --output CLAUDE.md
```

`--write` writes only inside the project: an `AGENTS.md` that is a symbolic link to a file
outside it is refused. A path you name with `--output` is written as you named it.

### How an agent finds it

- **Codex** reads `AGENTS.md` from the project root down to the working directory, one file
  per directory, and stops adding files when their combined size reaches
  `project_doc_max_bytes`, 32 KiB by default
  ([Codex documentation](https://learn.chatgpt.com/docs/agent-configuration/agents-md)).
- **Claude Code** reads `AGENTS.md` when the directory and its parents have no `CLAUDE.md`
  (version 2.1.277 or later), or when a `CLAUDE.md` imports it with `@AGENTS.md`. Its
  documentation advises keeping an instruction file under 200 lines
  ([Claude Code documentation](https://code.claude.com/docs/en/memory)).

The report of a write gives the size of the section. An instruction file is read in every
session, so a large Runefile costs every session its whole catalog. For a large Runefile
prefer the skill, which a client loads only when it needs it.

## A Claude skill

`rune export claude-skill --write` writes the skill `rune-tasks`:

```text
$ rune export claude-skill --write
.claude/skills/rune-tasks/SKILL.md: created: 2 tasks, 1980 bytes
```

`SKILL.md` starts with the frontmatter a skill needs, and then holds the same text as the
`AGENTS.md` section:

```markdown
---
name: "rune-tasks"
description: "Tasks of this project's Runefile and the rune command line for each: test, deploy. Read it before you run a build, a test, a deploy or another project command."
---
```

The description names the tasks, as many as fit, because a client decides from the
description whether to load the skill.

Claude Code reads a project skill from `.claude/skills/<name>/SKILL.md`, keeps its
description in context, and loads the rest when the skill is used
([Claude Code documentation](https://code.claude.com/docs/en/skills)).

**Large Runefiles.** Up to 30 tasks, every entry is in `SKILL.md`. Above that, `SKILL.md`
holds the general text and an index with one line per task, for at most 100 tasks and the
number of the rest, and the entries are in `tasks.md` beside it. `SKILL.md` therefore has a
bound however many tasks the Runefile has. When the Runefile shrinks again, the next write
removes the `tasks.md` it generated.

**The files are Rune's as a whole.** Each carries a line that says Rune generated it. A
`SKILL.md` at the target without that line is yours: it is never replaced, and the command
stops with exit 2. Write the skill to another directory, or move your file away.

`--output <directory>` writes the skill elsewhere, for a client that reads skills from
another place. The skill takes the name of the directory, which has to be a valid skill
name: lowercase letters, digits and single hyphens.

```sh
rune export claude-skill --output ~/.claude/skills/shop-tasks
```

Without `--write` and `--output`, `SKILL.md` is printed on standard output. When the skill
has a `tasks.md`, a note on standard error says that it is not printed.

## Keep it current: `--check`

`--check` compares the file with what the command would write now. It writes nothing.

```text
$ rune export agents-md --check
AGENTS.md: current

$ rune export agents-md --check
AGENTS.md: stale: the Runefile changed since the export
regenerate with: rune export agents-md --write
rune: export: AGENTS.md is not current
```

| Report | Meaning | Exit |
|---|---|---|
| `current` | the file holds what Rune would write | 0 |
| `missing` | no file, or no section in it | 1 |
| `stale: the Runefile changed since the export` | a task's name, description, parameters or limits changed | 1 |
| `stale: the text differs from what this rune generates` | the section was edited by hand, or written by a Rune version that words it differently | 1 |
| `stale: no longer part of the skill` | a generated `tasks.md` that the Runefile no longer needs | 1 |

As a CI step:

```yaml
- name: Agent instructions are current
  run: rune export agents-md --check
```

Each export carries a fingerprint of what it states. A change to a task body, to a private
task or to a comment that is no description does not change it, so such a commit does not
make the export stale. A difference between LF and CR LF line endings is not a difference.

After you upgrade Rune, run the export again: a new version may word an entry differently,
and `--check` then reports that the text differs. Pin the Rune version in CI to the one you
generate with.

`--check` uses the same place as `--write`, or the path of `--output`.

## Text from your Runefile is shown as text

Descriptions and values are yours, and they end up in a file that an agent client reads and
partly acts on. The export writes them so that they stay text:

- A description is one paragraph. Markdown in it is escaped, so it cannot add a heading, a
  list, a code block, a comment or one of Rune's marker lines.
- A value is shown as a quoted string, such as `"us east"`.
- No backtick and no `$` from the Runefile is in the output. Claude Code runs a command
  written between backticks after `!` when it loads a skill, and replaces `$ARGUMENTS` and
  similar placeholders. A backtick or `$` in a description is written as an HTML entity, and
  in a value as an escape of the quoted string.
- An `@` in a description is written as an entity, because Claude Code imports a file named
  as `@path` in an instruction file.
- A value with one of those characters, or one that starts with `-`, is never used in an
  example.

## What the text never contains

- `--yes`, an answer piped into `rune`, or any flag or setting that approves or skips a
  question.
- A command that does a task's work without Rune: no task body is copied.
- A value of the environment or of a dotenv file.

## Flags

| Flag | Meaning |
|---|---|
| `-f`, `--file <path>` | Export this Runefile instead of the one found from the working directory |
| `--write` | Write the export to its place in the project |
| `-o`, `--output <path>` | Write it to this file (`agents-md`) or skill directory (`claude-skill`) |
| `--check` | Say whether the export is what Rune would write now; write nothing |

`--check` cannot be combined with `--write`.

## Exit status

| Status | When |
|---|---|
| `0` | the export was printed, written, or found current |
| `1` | a check found it missing or stale, or a file could not be read or written |
| `2` | a usage error, no Runefile found, or a file Rune will not change |
| `3` | the Runefile is not valid; nothing is produced |

## Removing an export

Delete the lines from `<!-- rune:begin tasks` to `<!-- rune:end tasks -->` in the Markdown
file, or delete the skill directory. Nothing else was written.

## A task named `export`

`rune export` is the command, so a Runefile task named `export` is run with
`rune -- export`. The command prints a note when your Runefile has one, and its entry in
the export shows that usage. The task is still an MCP tool named `export`.

---

**See also:** [AI agents (MCP)](../mcp.md) · [Set up an agent client](agent-setup.md) ·
[Parameters](parameters.md) · [CLI reference](../cli.md)
