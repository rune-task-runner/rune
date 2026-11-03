# Research: Honor the `-c` Flag in `set shell`

**Feature**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)

All decisions below were checked against the code on `main` at `b763a12`. Shell
behavior was checked with bash 5.2 in the `golang:1.25-bookworm` image that
`Dockerfile.test` builds on.

## Current behavior (baseline)

- `runtime.Select` (`internal/runtime/executor.go`) returns `KindInterp` with
  `Command: settings.Shell` when a default-executor task runs under `set shell`,
  and `Command: strings.Fields(attr.Str)` for `[script("...")]`.
- `engine.runBodyTo` (`internal/cli/run.go`) joins the interpolated body lines with
  `joinBody` and calls `interp.Run`. Only `Line.Text` is used, so the `@` (no echo)
  and `-` (continue on error) prefixes have no effect, and nothing is echoed.
- `interp.Run` writes the script to a `rune-script-*` temp file (mode 0700 outside
  Windows) and runs `command... <tmpfile>`.
- With `-c` in the list, the shell treats the temp-file path as its command string.
  bash runs the file as a separate child script, which does not inherit `-e`, `-u`,
  or `pipefail`.

Measured in the test image, with a body of `echo one`, `false`, `echo two`:

| Invocation | Output | Status |
|---|---|---|
| `bash -euo pipefail -c <path>` (today) | `one`, `two` | 0 |
| `bash -euo pipefail <path>` | `one` | 1 |
| `bash -euo pipefail -c "<body>"` | `one` | 1 |
| `bash -eu <path>` (`-euc` with `c` removed) | `one` | 1 |
| `bash -c -e <path>` (flag before others, today) | `one`, `two` | 0 |

A body of `echo $0` prints the temp-file path when run from a file and `bash` when
passed as a command string.

## R1. How the body reaches the shell when the list has `-c`

**Decision**: Remove the command-string flag (R3: a known shell's `-c` with no code
after it) from the list and keep running the body from the temp file. A lone `-c`
element is dropped. In a cluster such as `-cu` or `-euc`, the letter `c` is removed
and the rest is kept (`-u`, `-eu`). `interp.Run` does not change.

**Rationale**:

- FR-003 says a list with `-c` must behave like the same list without it. Removing
  the flag makes that true by construction, including `$0` and the script name in
  the shell's own error messages (rows 2 and 4 above).
- The body stays in a temp file readable only by the user (mode 0700). Bodies can
  hold interpolated values, including `{{ env("...") }}` reads of secrets. On a
  command line, those values would be visible to other local users through the
  process list (`ps`, `/proc/<pid>/cmdline`) unless the system restricts it.
- There is no new size limit, so FR-007 adds no failure mode. Linux caps a single
  argument at 128 KiB (`MAX_ARG_STRLEN`), and Windows caps a whole command line at
  32,767 characters.
- There is no extra command-line quoting of multi-line bodies on Windows.
- It needs no shell-specific syntax, so it works the same for `sh`, `bash`, `zsh`,
  `dash`, and `fish`.

**Alternatives considered**:

- *Pass the body as the argument after `-c`* (the `just` model). It works for any
  program that takes code after `-c`, and it would also make wrapper lists such as
  `["docker", "exec", "ctr", "sh", "-c"]` work. Rejected because it puts the body,
  including interpolated secrets, on a command line other users can read. It also
  adds the size limits above and breaks FR-003 for programs that run a code string
  but not a file. A wrapper list never worked with the temp-file model, and it is
  out of scope here.
- *Keep `-c` and pass a command that sources the file* (`-c '. "<path>"'`). This
  needs shell-specific syntax (`.` is POSIX, fish uses `source`) and path quoting,
  and it fails for non-POSIX programs. Rejected.
- *Feed the body on standard input* (`bash -s`). The body would lose standard
  input, which breaks spec Story 1 scenario 7. Rejected.

## R2. Where the flag is removed

**Decision**: In `runtime.Select`, in the `set shell` branch and the
`[script("...")]` branch only, through one unexported helper:

```go
// commandWithoutStringFlag returns cmd without the shell's command-string flag.
func commandWithoutStringFlag(cmd []string) []string
```

The helper returns a new slice and never changes its input. `settings.Shell` is
shared by every task in the module, so changing it in place would affect other
tasks. The python, node, custom, and agent branches do not call it (FR-008).
`Selection.Display` keeps showing `shell:<program>`.

**Rationale**: `Select` is the one place that turns `set shell` and `[script]`
into a command, and both `runBodyTo` and `cacheSpec` read the result from there.
`interp.Run` also runs python and node bodies, so filtering there would need a
flag to tell the callers apart. Doing it in `config` (at `set shell` evaluation)
would miss `[script]` and would change the value that hover and other readers
of the setting see.

**Alternatives considered**: filtering in `interp.Run` (rejected: applies to python
and node unless told otherwise); filtering in `config.Settings` (rejected: misses
`[script]`).

## R3. Which elements count as the command-string flag (FR-001)

**Decision**: The rule targets exactly the defect: a shell told to read its code
from the next operand, with no operand left but rune's script path. In order:

1. *Known shell.* Find the first element whose base name (after the last `/` or
   `\`, lowercased, any `.exe` removed) is `sh`, `bash`, `dash`, `zsh`, `ksh`,
   `mksh`, `ash`, or `fish`. If there is none, the list is unchanged.
2. *Flag.* After that element, a short-option cluster starts with exactly one `-`,
   has at least one character after it, and has only ASCII letters after the `-`.
   A cluster that contains `c` is a command-string flag.
3. *Own code.* If any element after the first flag is an operand, the list gives
   the shell its own code and the script path becomes `$0`, which works today. The
   list is unchanged. An operand does not start with `-` or `+` and is not an
   option value. An element is an option value when the element just before it is
   a `-` or `+` cluster of letters that contains `o` or `O` (`-o pipefail`,
   `-euo pipefail`, `-O extglob`).
4. *Stdin.* If a cluster after the shell contains `s`, the list is unchanged.
   Without `-c`, `-s` makes the shell read its commands from stdin and take the
   script path as `$1`, so the body would not run.
5. *Removal.* Otherwise, all `c` letters are removed from every flag after the
   shell. If only `-` remains, the element is dropped.

| Input | Effective command |
|---|---|
| `["bash", "-cu"]` | `["bash", "-u"]` |
| `["bash", "-euo", "pipefail", "-c"]` | `["bash", "-euo", "pipefail"]` |
| `["bash", "-euc"]` | `["bash", "-eu"]` |
| `["bash", "-c", "-e"]` | `["bash", "-e"]` |
| `["bash", "-c", "-o", "pipefail"]` | `["bash", "-o", "pipefail"]` |
| `["bash", "-c", "-euo", "pipefail"]` | `["bash", "-euo", "pipefail"]` |
| `["bash", "-c", "-c"]` | `["bash"]` |
| `["zsh", "-fc"]` | `["zsh", "-f"]` |
| `["sh", "-c"]` | `["sh"]` |
| `["bash", "--norc", "-c"]` | `["bash", "--norc"]` |
| `["/usr/bin/env", "bash", "-euc"]` | `["/usr/bin/env", "bash", "-eu"]` |
| `["busybox", "sh", "-c"]` | `["busybox", "sh"]` |
| `["BASH.EXE", "-cu"]` | `["BASH.EXE", "-u"]` |
| `["bash", "-o", "pipefail"]` | unchanged |
| `["bash", "-O", "extglob"]` | unchanged |
| `["bash", "-eu"]` | unchanged |
| `["bash", "-O2"]` | unchanged (not letters only) |
| `["bash", "--rcfile=c"]` | unchanged (starts with `--`) |
| `["bash", "-c", "exec bash -eu $0"]` | unchanged (own code) |
| `["bash", "-sc"]`, `["bash", "-s", "-c"]`, `["bash", "-c", "-es"]` | unchanged (`-s`) |
| `["nix", "develop", "-c", "bash", "-eu"]` | unchanged (the wrapper's `-c`) |
| `["php", "-c", "/etc/php.ini"]` | unchanged (not a known shell) |
| `["jq", "-c", "-f"]` | unchanged |
| `["node", "-c"]` | unchanged |
| `["pwsh", "-c"]` | unchanged |
| `["-c"]` | unchanged |

**Rationale**: The defect only occurs when the script path becomes a shell's code.
Lists where it does not, such as a wrapper's `-c`, a list that gives the shell its
own code, or a program whose `-c` means something else (`php -c <ini>` reads a
config file, `jq -c` compacts output, `node -c` only checks syntax), work today and
must keep working (constitution, Backward compatibility). A text-only rule with no
shell lookup would change those lists, some of them silently: `php /etc/php.ini
<script>` prints the ini file and exits 0. Requiring letters only keeps values such
as `-O2` or `-1` out of scope. Removing every `c` handles repeated flags without a
special case.

**Alternatives considered**: a text-only rule on every element after index 0
(rejected for the reasons above); dropping `[script]` from scope (rejected: it
leaves `[script("bash -euc")]` with the issue's defect and does not protect wrapper
lists in `set shell`).

**Known limits**:

- The shell names are a fixed list. A shell missing from it keeps today's behavior
  with `-c`. Adding a name is a one-line change.
- Only `o` and `O` are known to take a value. A shell option that takes a value
  after `-c` (no such shape is a useful configuration) makes the next element look
  like an operand, so the list is left unchanged, as it is today.

## R4. `$0`, error messages, and FR-007

**Decision**: No extra handling.

**Rationale**: The effective command is the list without `-c`, so `$0` and the
shell's own error prefix are what they are for that list today, as FR-003
requires. FR-007 adds no failure mode under R1: the body
still goes through the temp file, and today's temp-file errors (create, write,
close) already fail the task with an `interp.ExecError`.

## R5. Effect on `[cache]` fingerprints

**Decision**: Accept a one-time cache miss for `[cache]` tasks that run under a
`-c` shell list.

**Rationale**: `cacheSpec` builds the executor part of the fingerprint from
`Selection.Command`. Removing the flag changes it, for example from
`shell:bash:bash -cu` to `shell:bash:bash -u`. Each such task runs once after
upgrade and caches again. That is the safe direction (Principle I: tasks run by
default), and it is correct, because the body now runs with different flags.
Hashing the raw list instead would keep a stale hit for a body that now behaves
differently.

## R6. Backward compatibility and release notes

**Decision**: Ship as a bug fix with a breaking marker in the squash-merge PR
title:
`fix(runtime)!: breaking: shell lists with -c now apply their flags, so a failing line can fail the task`.
`cliff.toml` builds the changelog and the GitHub Release notes from that title
alone (`{{ commit.message }}`), and keeps breaking commits
(`protect_breaking_commits = true`), so the title must state both points of FR-010
itself. The PR body names the lists that change: those where a known shell's `-c`
has no code after it (R3).

**Rationale**: The grammar does not change, so the constitution's per-file opt-in
rule for breaking DSL changes does not apply. The Runefile already declares the
flags. A newly failing task fails loudly, with the banner and a non-zero exit, so
there is no silent meaning change. Spec 028 set the same precedent
(`fix(cli)!:` in `9b06193`).

**Corpus check**: `testdata/corpus/full.rune` contains
`set shell := ["bash", "-cu"]`. The corpus test (`test/corpus`) re-parses the file
and compares the result with its golden. It does not run tasks, so it needs no
edit (SC-003).

## R7. Tests and where bash is required

**Decision**:

- *Unit* (`internal/runtime/executor_test.go`, all OSes): a table test of
  `commandWithoutStringFlag` with every row from R3, a check that the input slice is
  unchanged, `Select` for `set shell` and `[script]` with `-c`, and `Select` for
  `set python := ["python3", "-c"]`, `[script("node -c")]`, and
  `[script("php -c /etc/php.ini")]` unchanged (FR-008).
- *Binary level* (`test/integration/shell_override_test.go`, new): the issue's
  repro, the unset variable under `-cu`, `(exit 3)` under `-euc`, pipefail,
  `[script("bash -euc")]`, standard input, a `||` failure hook that sees
  `RUNE_FAILED_EXIT_CODE`, state across lines, a list that gives the shell its own
  code, and the MCP `[exit 1]` marker (using the existing `mcpCall` helper). An
  equivalence test
  covers SC-002. It runs bodies (all succeed, middle line fails, unset variable,
  failing pipeline) under lists (`-c`, `-cu`, `-euc`, `-euo pipefail -c`,
  `-c -eu`) and the same lists without the flag, and compares stdout and the exit
  code.
- *Docs* (`test/docs/shell_override_test.go`, new): finds every
  `set shell := [...]` line in `docs/`, builds a Runefile from it with a body whose
  middle line fails, runs it, and checks that the line after the failure never runs
  and the exit code is non-zero (SC-004). It also checks each list's text for a
  `-e` cluster, so a list whose program is not on `PATH` is still checked, though
  not run. It also checks the new heading and the links to it, because the link
  checker skips anchors (`test/docs/links_test.go`).
- The binary-level and docs tests skip on Windows and when `bash` is not on
  `PATH`.

**Rationale**: On Windows CI runners, `bash` on `PATH` can be the WSL launcher or
Git Bash, and Principle V rules out Git-Bash workarounds. The rule itself is pure
string handling, and its unit test runs on all three OSes. The behavior tests run
on Linux and macOS. The test image is Debian-based and has bash. Spec SC-001 is
narrowed to Linux and macOS to match.

## R8. Documentation

**Decision**:

- `docs/runefile.md`, settings example: `set shell := ["bash", "-euo", "pipefail"]`
  with the comment kept. A new "Shell override" subsection in the Executors
  section states the FR-009 points: the body runs as one script in one shell
  process; a failing line stops the task only if the shell's exit-on-error flag
  (`-e`) is set; lines are not echoed; the `@` and `-` prefixes have no effect, so
  a `-` line that fails under `-e` stops the task; and a shell's `-c` with no code
  after it is accepted, has no further effect, and the other flags apply to the
  body.
- `docs/how-to/settings-and-dotenv.md`: the same example, plus a pitfall bullet
  that links to the new subsection.
- `docs/how-to/executors.md`: the existing `set shell` pitfall links to the new
  subsection.
- `internal/language/builtin.go`: the `shell` setting's hover text becomes
  "Command that runs shell task bodies as one script. Include -e to stop at a
  failing line." No test pins the old string.
- `website/src/content/docs/` is generated from `docs/` and ignored by git, so it
  is not edited.

**Rationale**: The examples drop `-c` because rune ignores it. Showing it would
suggest that it matters. The subsection says that `-c` is accepted, for readers
coming from `just`. The hover text is where an author sees the setting while
writing it.

**Alternatives considered**: keeping `-c` in the examples to match `just` habits
(rejected: the docs would show a flag that does nothing); adding the explanation
only to the how-to (rejected: FR-009 names the Runefile reference).
