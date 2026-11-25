# Learn tasks from your agent's commands

> Record the shell commands a coding agent runs in your project, see which ones it repeats,
> and turn the ones you choose into Runefile tasks.

A project without a Runefile gets nothing from Rune until someone writes the tasks. Your
agent already knows them: it runs the same test command, the same linter and the same build
again and again. `rune suggest` lists those repetitions as tasks you can review and add.

```sh
rune agent observe claude     # start recording, in the project root
# ... work with Claude Code as usual ...
rune suggest                  # what did the agent repeat?
rune suggest apply 3f9a1c2e   # add that task to the Runefile
```

Nothing is recorded until you turn recording on, and recording never changes your
Runefile. Only `rune suggest apply` writes to it, with the suggestions you name. No command
the agent ran is ever run by Rune: commands are read as text, to group, rank, preview and
apply them. Only Claude Code's Bash tool is recorded.

## Turn recording on

Run this in the project root, the directory you start Claude Code in. No Runefile is
needed.

```sh
rune agent observe claude
```

It adds three hook handlers to `.claude/settings.local.json`, your own settings file for the
project: one before each Bash call, one after a call that succeeded and one after a call
that failed. Each runs `rune agent record` in the background:

```json
{
  "hooks": {
    "PreToolUse": [{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }],
    "PostToolUse": [{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }],
    "PostToolUseFailure": [{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }]
  }
}
```

The file is edited in place: your other hooks, settings, comments and layout stay as they
were, and no permission rule is added, removed or changed. A second run changes nothing.
`--dry-run` prints the difference and writes nothing. Do not commit
`.claude/settings.local.json`: it would turn recording on for everyone who clones the
project.

A background hook cannot block or change a tool call, and `rune agent record` prints nothing
and always exits 0. Recording can never change what the agent's command does.

## What is recorded, and where

For each Bash call, Rune keeps:

- the command, with secrets removed;
- the directory it ran in, relative to the project;
- the time, the Claude Code session and the call's id;
- whether it succeeded, failed (with its exit code when Claude Code gives one), was
  interrupted or was started in the background;
- the agent's one-line description of the command.

It never keeps what the command printed. Before anything is written, Rune replaces with
`***`:

- the value of an assignment to a secret name, such as `API_TOKEN=... deploy` or
  `export API_TOKEN=...`, and of an argument that passes one, such as
  `env API_TOKEN=...`, `docker run -e API_TOKEN=...` or `--build-arg=API_TOKEN=...`;
- the value of a flag with a secret name, such as `--token ...`, `--password=...` or
  `--api-key ...`;
- the value of an `Authorization:` or `Proxy-Authorization:` header, or of a header with a
  secret name;
- the password in a URL such as `postgres://user:password@host`, and the token in an
  HTTP URL such as `https://TOKEN@github.com/...`;
- the password of `curl -u user:password`, of `mysql -pPASSWORD` (and `mysqldump`,
  `mysqladmin`, `mariadb`), of `sshpass -p`, and of `docker login -p`;
- every value Rune [masks in task output](secret-masking.md): the values of secret
  variables in the environment and, with a Runefile, the names it lists in `set secrets`.

Secret names are the ones Rune masks in task output: names that hold `TOKEN`, `SECRET`,
`PASSWORD`, `PASSWD`, `APIKEY`, `API_KEY`, `PRIVATE_KEY`, `ACCESS_KEY`, `CREDENTIAL` or
`AUTH`, and the ones your Runefile declares, less the ones it exempts. A flag such as
`--author` holds `AUTH`, so its value is replaced too; such a command is then listed for
review rather than turned into a task.

A command is not kept at all when it holds a here-document, cannot be parsed, is longer
than 4096 bytes or holds a control character. Its text is never stored, and the listing
counts it under `Not recorded`.

The records live outside the project, in your state directory:
`$XDG_STATE_HOME/rune/observe`, else `%LocalAppData%\rune\observe` on Windows, else
`~/.local/state/rune/observe`. The directory is open to your user only. Records older than
30 days are removed, and one project's records never take more than 4 MiB: the oldest go
first. `rune suggest forget` deletes this project's records now.

## A noisy session

After a few days of work the agent has run 212 commands. `rune suggest` lists those that
ran at least three times:

```text
$ rune suggest
Recorded 212 commands in 4 sessions, 2026-11-02 to 2026-11-23.
1 attempt has no recorded outcome.

[3f9a1c2e] the agent ran this 14 times (12 succeeded, 2 failed) in 3 sessions, last on 2026-11-23:
    go test -run TestX ./pkg/...
  suggested task:
    # Run the parser tests
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

Applying both suggestions adds them at the end of the Runefile, or creates it:

```sh
rune suggest apply 3f9a1c2e 5be09f12 --dry-run   # print the difference
rune suggest apply 3f9a1c2e 5be09f12
```

```rune
# Run the parser tests
go-test:
    go test -run TestX ./pkg/...

# Run go test.
[working-directory("web")]
go-test-2 run="TestA":
    go test -run {{quote(run)}} ./...
```

Rename the tasks and edit their descriptions as you like. The next `rune suggest` lists
both commands under `Already tasks`.

## How commands become tasks

**Counting.** A call is counted once, however many events Claude Code sent for it, and in
whatever order they arrived. A call with only its first event (it was denied, blocked or is
still running) is an attempt, not a run, and does not count toward a suggestion.

**Grouping.** Two runs are the same command when they have the same words, quoting and
operators, differing only in spacing, and ran in the same directory. `cd web && npm test`
counts as `npm test` run in `web`, and its task gets `[working-directory("web")]`.

**One varying word.** Commands that differ in one plain word, other than the program, become
one task with a parameter. The parameter is named after the flag before the word (`-run`
gives `run`), or `value`. Its default is the value run most. The body passes it as
`{{quote(run)}}`, so the shell receives one word whatever the value holds.

**Left out.** A command that runs `rune` itself is never suggested. A command that is
already the one-line body of a task in the same directory is counted under `Already tasks`.

**For review.** A command Rune cannot write as a task safely is listed with its reason and
no ID: a command substitution, a loop or another compound command, a subshell, commands in
sequence with `;`, a command in the background, a built-in that changes the shell's state
such as `export` or a `cd` that is not the leading `cd DIR &&`, a command over several lines,
or a command that held a secret. Write those tasks by hand.

**The task.** A suggestion is a doc comment, an optional `[working-directory]`, the name,
an optional parameter and one body line. It carries no other attribute: no `[readonly]`
claim, no `[confirm]`, no grant. An agent can call such a task through `rune mcp` without
approval, so add `[confirm]` to a task that changes state.

## Applying

`rune suggest apply ID...` takes the IDs `rune suggest` printed. An ID comes from the
command and its directory, so it stays the same while more records arrive. With the
`--min-runs` you listed with, the same suggestions are found.

- Every byte already in the Runefile stays; the tasks go at the end, after a blank line.
- A name a task already has gets a suffix: `go-test-2`.
- The new Runefile must pass analysis before it is written, and it is written in one step.
- When an ID is unknown, or the Runefile changed while the tasks were being added, nothing
  is written.
- A Runefile with errors is refused until you fix it.

## Turn recording off

```sh
rune agent observe claude --remove   # take the handlers out
rune suggest forget                  # delete what was recorded
```

`--remove` takes out exactly the handlers that run `rune agent record`, and the groups and
event lists they leave empty. Your own hooks stay.

## The Rune-only profile

[`rune agent init claude`](agent-setup.md) denies the Bash tool, so the agent can run tasks
only through Rune. An agent with no shell runs no shell command, and there is nothing to
record. `rune agent observe claude` says so in a note, and it never changes that deny rule
to collect more.

Observation is for the time before that profile: record while the agent still has its
shell, add the tasks it needs, then set the project up with `rune agent init claude`.

## When nothing is recorded

- Start Claude Code in the project root. The handlers are in that directory's
  `.claude/settings.local.json`, and the project is the directory Claude Code names as its
  project.
- `rune` must be on the `PATH` of Claude Code's hooks.
- A setting such as `disableAllHooks` turns every hook off.
- `rune suggest` counts the events that reached the recorder and were not kept:

| Reason | Meaning |
|---|---|
| `no-project` | Claude Code named no project directory |
| `outside-project` | the command ran outside the project |
| `runefile-unreadable` | the Runefile could not be loaded, so its secrets were not known |
| `here-document`, `unparsable`, `too-long`, `control-character` | the command could not be kept safely |
| `invalid-event`, `not-json`, `too-large`, `slow-input` | the event was not one Rune can read |
| `store-busy`, `store-error` | the records could not be written |

## A task named `suggest`

`rune suggest` is the command, so a Runefile task named `suggest` is run with
`rune -- suggest`. The command prints a note when your Runefile has one.

---

**Next:** [Set up an agent client](agent-setup.md) · [Secret masking](secret-masking.md) · [CLI reference](../cli.md)
