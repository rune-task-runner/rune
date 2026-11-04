# Feature Specification: Safe Quoting for Task Parameters

**Feature Branch**: `030-safe-param-quoting`

**Created**: 2026-11-03

**Status**: Draft

**Input**: GitHub issue [#35](https://github.com/rune-task-runner/rune/issues/35):
"Variadic params interpolate unquoted (shell injection); quote() joins them into one word"

## Overview

A variadic parameter (`*names` or `+names`) collects the remaining arguments of a
call. Rune joins them with spaces. `{{names}}` pastes the joined text into the shell
line unquoted, so an argument that contains shell syntax runs as shell code.
`{{quote(names)}}` quotes the joined text as one word, so the boundaries between
arguments are lost. No form passes a list safely as separate arguments.

Repro on v0.6.0:

```rune
# Print each argument on its own line.
a *names:
    @printf "[%s]\n" {{names}}

# Same, quoted.
b *names:
    @printf "[%s]\n" {{quote(names)}}
```

```console
$ rune a yq 'x; echo pwned' tofu
[yq]
[x]
pwned tofu
$ rune b yq 'x; echo pwned' tofu
[yq x; echo pwned tofu]
```

Scalar parameters have the same exposure: `{{env}}` pastes an argument unquoted, and
only `{{quote(env)}}` is safe. Nothing tells the author which form a line uses.
Arguments come from the command line and from AI agents that call tasks over MCP, so
a value an agent supplies can run shell code.

This feature:

1. adds a `quote_each()` built-in that quotes each element of a variadic parameter
   as its own shell word;
2. keeps each argument as a separate element from every source (command line, MCP
   arrays, dependency calls), so `quote_each()` sees the arguments as they were given;
3. adds an analyzer warning for a string or path parameter interpolated unquoted into
   a shell body;
4. adds a `raw` parameter kind that declares word splitting as intended and turns the
   warning off for that parameter;
5. updates the docs and examples to the safe forms.

`{{names}}` and `{{quote(names)}}` keep producing today's text, so existing Runefiles
run unchanged.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Pass a variadic to a command as separate, quoted arguments (Priority: P1)

A developer writes `fmt *files:` with the body `prettier --write {{quote_each(files)}}`.
Each argument reaches `prettier` as one argument, whatever characters it contains.

**Why this priority**: This is the defect the issue reports. Today no form passes a
list safely, so a task that forwards arguments either allows injection or merges
them.

**Independent Test**: Add the task `c` below to the issue's repro, run it with the
issue's arguments, and check the output.

```rune
c *names:
    @printf "[%s]\n" {{quote_each(names)}}

n *names:
    @set -- {{quote_each(names)}}; echo "$#"
```

**Acceptance Scenarios**:

1. **Given** task `c`, **When** the user runs `rune c yq 'x; echo pwned' tofu`,
   **Then** stdout is exactly `[yq]`, `[x; echo pwned]`, `[tofu]` on three lines and
   no injected command runs.
2. **Given** task `c`, **When** the user runs `rune c 'a b' '' "it's"`, **Then**
   stdout is `[a b]`, `[]`, `[it's]`: a space, an empty argument, and a single quote
   each stay inside one argument.
3. **Given** task `n`, **When** the user runs `rune n` with no arguments, **Then** it
   prints `0`: `{{quote_each(names)}}` expands to nothing, not to an empty argument.
4. **Given** task `n`, **When** the user runs `rune n 'a b' c`, **Then** it prints `2`.
5. **Given** task `c`, **When** an AI agent calls it as an MCP tool with
   `names: ["yq", "x; echo pwned", "tofu"]`, **Then** the output is the same three
   lines as in scenario 1.
6. **Given** task `c` with `+names` instead of `*names`, **When** the user runs
   `rune c yq`, **Then** stdout is `[yq]`.
7. **Given** task `c` under `set shell := ["bash", "-eu"]`, **When** the user runs it
   with the arguments from scenario 1, **Then** the output matches scenario 1.

---

### User Story 2 - Existing Runefiles produce the same commands (Priority: P1)

A developer upgrades Rune. Tasks that use `{{names}}`, `{{quote(names)}}`, or scalar
parameters run the same commands as before.

**Why this priority**: The constitution forbids silent meaning changes. Code that
relies on today's text, such as a commit message built from `+msg` with `quote()`,
must keep working.

**Independent Test**: Run the existing test suite and the compatibility corpus, and
run tasks `a` and `b` from the repro before and after the change.

**Acceptance Scenarios**:

1. **Given** task `a` from the repro, **When** the user runs
   `rune a yq 'x; echo pwned' tofu`, **Then** the output is the same as on v0.6.0.
   This feature warns about the line (Story 3); it does not change what it runs.
2. **Given** task `b` from the repro, **When** the user runs it with the same
   arguments, **Then** the output is `[yq x; echo pwned tofu]`, as on v0.6.0.
3. **Given** a task `commit +msg:` with the body `git commit -m {{quote(msg)}}`,
   **When** the user runs `rune commit fix the build`, **Then** git receives the
   single message `fix the build`.
4. **Given** a Runefile that uses neither `quote_each()` nor the `raw` kind, **When**
   any task runs with the same arguments as before, **Then** its interpolated command
   text is byte-identical to v0.6.0's.

---

### User Story 3 - The analyzer flags unquoted parameters in shell bodies (Priority: P2)

A developer runs `rune analyze` or opens the Runefile in an editor. Each interpolation
that pastes a string or path parameter into a shell body without quoting gets a
warning that names the parameter and the fix.

**Why this priority**: The issue asks for arguments to be safe by default. Quoting
`{{p}}` automatically would change the text of working Runefiles, which the
constitution forbids, so the analyzer points at every unsafe line instead.

**Independent Test**: Analyze a fixture Runefile that contains every form below and
compare the diagnostics with a golden list.

**Acceptance Scenarios**:

1. **Given** `deploy env:` with the body `./deploy.sh {{env}}`, **When** the user runs
   `rune analyze`, **Then** one warning points at `{{env}}`, names `env`, suggests
   `{{quote(env)}}`, and `rune analyze` exits 0.
2. **Given** task `a` from the repro, **When** the user runs `rune analyze`, **Then**
   a warning names `names` and suggests `{{quote_each(names)}}`.
3. **Given** the body `@echo "hello {{name}}"`, **When** the user runs
   `rune analyze`, **Then** it warns, because `$(...)` still runs inside double
   quotes, and the hint says to write `{{quote(name)}}` outside the shell quotes.
4. **Given** parameters of kind `enum(...)`, `number`, or `boolean`, **When** they are
   interpolated unquoted, **Then** there is no warning.
5. **Given** `{{quote(env)}}`, `{{quote_each(names)}}`, `{{quote("v" + version)}}`,
   or `{{"--env=" + quote(env)}}`, **When** the user runs `rune analyze`, **Then**
   there is no warning.
6. **Given** `{{"--env=" + env}}` or `{{trim(env)}}`, **When** the user runs
   `rune analyze`, **Then** it warns, because the parameter's text still reaches the
   line unquoted.
7. **Given** `{{if env == "prod" { "--strict" } else { "" }}}`, **When** the user runs
   `rune analyze`, **Then** there is no warning: `env` appears only in the comparison.
8. **Given** an unquoted parameter in a `(python)`, `(node)`, agent, or custom-executor
   body, or in a `[script("python3")]` body, **When** the user runs `rune analyze`,
   **Then** there is no warning.
9. **Given** an unquoted parameter in a `[script("bash -eu")]` body, **When** the user
   runs `rune analyze`, **Then** it warns.
10. **Given** a Runefile that only has this warning, **When** the user runs a task,
    **Then** the run prints no warning and its exit status is unchanged.

---

### User Story 4 - Declare a parameter that splits into words on purpose (Priority: P2)

A developer has `test flags="-v -race":` with the body `go test {{flags}} ./...` and
wants `flags` split into words. They write `flags:raw="-v -race"`. The warning goes
away for that parameter and the command is unchanged.

**Why this priority**: Without an opt-out, the warning fires on lines that are correct
as written, and the only suggested fix, `quote()`, would break them.

**Independent Test**: Analyze and run a fixture that declares `raw` parameters, and
read the MCP tool listing for it.

**Acceptance Scenarios**:

1. **Given** `test flags:raw="-v -race":` with the body `go test {{flags}} ./...`,
   **When** the user runs `rune analyze`, **Then** there is no warning; **When** the
   user runs `rune test`, **Then** `go test` receives `-v` and `-race` as two
   arguments.
2. **Given** `run *args:raw` with the body `./tool {{args}}`, **When** the user runs
   `rune analyze`, **Then** there is no warning, and `{{args}}` produces the elements
   joined by spaces, as today.
3. **Given** a task with a `raw` parameter, **When** an AI agent lists the MCP tools,
   **Then** the parameter is a string (an array of strings for a variadic), and its
   schema says the value is split into shell words without quoting.
4. **Given** a Runefile with `flags:raw="-v"`, **When** the user runs `rune --fmt`,
   **Then** the annotation is kept in canonical form.
5. **Given** a `raw` parameter in an editor, **When** the user hovers it, **Then**
   the hover shows its annotation and says the value is split into shell words
   without quoting.

---

### User Story 5 - Forwarding a variadic to a dependency keeps its elements (Priority: P3)

A developer writes `ci *pkgs: (test pkgs)` and `test *pkgs:` with the body
`go test {{quote_each(pkgs)}}`. `rune ci ./a './b c'` runs `go test` with `./a` and
`./b c` as two arguments.

**Why this priority**: Tasks compose through dependency calls. If forwarding merged
the elements, `quote_each()` in the dependency would quote them as one word. It is P3
because Stories 1 to 4 work without it.

**Independent Test**: Run fixtures that forward a variadic into a dependency's
variadic and scalar parameters, and check what the dependency receives.

**Acceptance Scenarios**:

1. **Given** the tasks above, **When** the user runs `rune ci ./a './b c'`, **Then**
   `go test` receives `./a` and `./b c` as two arguments.
2. **Given** `release *targets: (build "linux" targets)` and `build os *targets:`,
   **When** the user runs `rune release a b`, **Then** `build` receives `os` =
   `linux` and the targets `a` and `b` as two elements.
3. **Given** `tag *names: (label names)` and `label text:`, **When** the user runs
   `rune tag a b`, **Then** `text` is `a b`, as today.
4. **Given** `ci *pkgs: (test pkgs)` and `test *pkgs:enum("a","b"):`, **When** the
   user runs `rune ci a b`, **Then** each element passes the enum check and `test`
   runs. (Today the joined `a b` fails the check.)
5. **Given** `ci *pkgs: (test pkgs)` and `test +pkgs:`, **When** the user runs
   `rune ci` with no arguments, **Then** `test` runs, as today, with no elements:
   `{{pkgs}}` is empty text and `{{quote_each(pkgs)}}` expands to nothing.
6. **Given** `ci *pkgs: (test pkgs)` and `test +pkgs:path:`, **When** the user runs
   `rune ci ./a ''`, **Then** `test` runs, as today, with the elements `./a` and
   the empty string. The empty element alone fails the `path` check, but v0.6.0
   checked the text `./a `, which passes.

---

### Edge Cases

- **Empty variadic.** `quote_each()` of an empty list expands to nothing.
  `quote("")` still expands to `''`, an empty argument.
- **Empty element.** An empty argument inside a list is kept as `''`.
- **Special characters.** Elements with single quotes, newlines, `$`, backticks, `;`,
  or non-ASCII bytes are quoted the same way `quote()` quotes them.
- **Leading dash.** Quoting stops shell syntax. It does not stop a command from
  reading an argument that starts with `-` as an option. That is out of scope; the
  docs recommend `--` where the command supports it.
- **Non-variadic argument.** `quote_each()` of anything other than a bare reference
  to a variadic parameter treats the value as one element, so `quote_each(name)` and
  `quote_each(names + "x")` give the same text as `quote()`.
- **String for a variadic over MCP.** A plain string supplied for a variadic parameter
  is one element, as the per-element check already treats it.
- **Same text, different elements.** `rune c 'a b'` and `rune c a b` run different
  commands under `quote_each()`. The task cache and the once-per-invocation rule
  treat them as different invocations. Because the cache now records elements, a
  `[cache]` task that references a variadic runs once after the upgrade.
- **Interpolation inside shell quotes.** `"{{p}}"` and `'{{p}}'` are both warned:
  `$(...)` runs inside double quotes, and a `'` in the value ends single quotes.
- **Defaults.** A defaulted string parameter is warned. The default is the author's,
  but the command line and agents can override it.
- **Outside bodies.** Interpolations in dependency arguments and attribute strings,
  such as `[confirm("...")]`, are never warned.
- **Other shells.** `quote()` and `quote_each()` produce POSIX quoting. A `set shell`
  or `[script]` program that is not a known shell (PowerShell, `cmd`) gets no warning,
  because the suggested fix would not be correct there.
- **Imported files.** The warning applies in imported modules, located in the
  imported file.
- **`raw` and tasks named `raw`.** If a task is named `raw`, the existing warning
  about a kind name that shadows a task name applies.
- **Older Runefiles.** `x:raw` is an unknown-kind error today, so no working Runefile
  changes meaning when `raw` becomes a kind.

## Requirements *(mandatory)*

### Functional Requirements

**`quote_each()`**

- **FR-001**: Rune MUST provide a built-in function `quote_each(x)` with one
  argument. When the argument is a bare reference to a variadic parameter, the result
  MUST be each element quoted as `quote()` quotes it, in order, joined by single
  spaces.
- **FR-002**: When that variadic has no elements, `quote_each()` MUST produce empty
  text.
- **FR-003**: When the argument is anything other than a bare reference to a variadic
  parameter, `quote_each()` MUST treat its value as one element and produce the same
  text as `quote()`.
- **FR-004**: A call to `quote_each()` with a number of arguments other than one MUST
  fail the same way a call to `quote()` with the wrong count fails.
- **FR-005**: `quote_each()` MUST appear in the built-in function reference and in
  editor completion and hover, next to `quote()`.

**Element boundaries**

- **FR-006**: Rune MUST keep the elements of a variadic parameter as given: each
  command-line argument, each element of an MCP array, and each dependency or hook
  argument that fills the variadic is one element. A plain string supplied over MCP
  for a variadic is one element; an empty string is no elements, like `[]`.
- **FR-007**: When a dependency, post-hook, or failure-hook call passes a bare
  reference to the calling task's variadic parameter into the target's variadic, the
  target MUST receive the caller's elements as separate elements. When the reference
  fills a scalar parameter, that parameter MUST receive the space-joined text, as
  today. The text `{{p}}` gives for the target's variadic MUST stay as today. An
  empty list that leaves a `+` parameter with no elements MUST still run the
  target, as today, with no elements.
- **FR-008**: Per-element constraint checks MUST apply to the elements defined in
  FR-006 and FR-007. A dependency or hook call whose written arguments pass the
  target's checks, as v0.6.0 checked them, MUST still be accepted when an element
  on its own fails.
- **FR-009**: Invocations whose variadic elements differ MUST count as different
  invocations for the once-per-invocation rule and for the task cache, even when
  their space-joined text is the same.

**Compatibility**

- **FR-010**: Everywhere except as the argument of `quote_each()`, a variadic
  parameter's value MUST stay its elements joined by single spaces, so `{{names}}`,
  `{{quote(names)}}`, concatenation, comparisons, and other functions produce the
  same text as today.
- **FR-011**: For a Runefile that uses neither `quote_each()` nor `raw`, the
  interpolated command text of every task MUST be byte-identical to v0.6.0's for the
  same arguments.

**Analyzer warning**

- **FR-012**: The analyzer MUST warn for each interpolation in a shell body whose
  text can contain a warned parameter's value unquoted, where:
  - a **shell body** is the body of a task run by the built-in shell, by a
    `set shell` list, or by a `[script("...")]` command whose program is a known
    shell (as defined in spec 029 FR-001);
  - a **warned parameter** is a scalar or variadic parameter with no kind, or with
    kind `string` or `path`;
  - an appearance inside a `quote()` or `quote_each()` call, at any depth, is
    **not** warned (so `{{"--env=" + quote(env)}}` is safe), and neither is an
    appearance that is only an operand of a conditional's comparison;
  - every other appearance is warned, including concatenation, path-join, a
    conditional's result, and arguments of other functions such as
    `{{trim(env)}}`.
- **FR-013**: The warning MUST point at the interpolation with `file:line:col` and a
  caret span, name the parameter, and name the fix: `{{quote(p)}}` for a scalar or
  `{{quote_each(p)}}` for a variadic, written outside any shell quotes, or the `raw`
  kind when splitting into words is intended.
- **FR-014**: The warning MUST have its own diagnostic code, listed in the diagnostics
  reference. It MUST appear where existing analyzer warnings appear (`rune analyze`,
  its JSON output, and the editor) and MUST NOT change the exit status of
  `rune analyze` or of any run.
- **FR-015**: Python, node, agent, and custom-executor bodies, `set shell` and
  `[script]` programs that are not known shells, and interpolations outside task
  bodies MUST NOT get this warning.

**`raw` kind**

- **FR-016**: Rune MUST accept `raw` as a parameter kind in every parameter form:
  `name:raw`, `name:raw="default"`, `*name:raw`, and `+name:raw`. It MUST accept any
  value, as a parameter with no kind does, and MUST NOT change interpolated text.
- **FR-017**: The analyzer MUST NOT report the FR-012 warning for a `raw` parameter.
- **FR-018**: The formatter MUST keep the `raw` annotation in canonical form. Editor
  hover on a `raw` parameter MUST show the annotation and explain it. (The editor
  offers no completion for any kind today; adding it is out of scope.)
- **FR-019**: An MCP tool schema MUST present a `raw` parameter as a string, or an
  array of strings for a variadic, and MUST state that the value is split into shell
  words without quoting.

**Documentation and release**

- **FR-020**: The Runefile reference and the parameters guide MUST document
  `quote_each()`, the `raw` kind, the warning, and that `{{p}}` inside shell quotes is
  still unsafe. The grammar reference MUST list `raw` among the parameter kinds.
- **FR-021**: Every documentation example and the repository's own Runefile MUST
  analyze without the new warning.
- **FR-022**: The changelog entries for this change MUST name `quote_each()`, the
  `raw` kind, and the new warning. The changelog is generated from PR titles, so
  these titles carry it.

### Key Entities

- **Variadic parameter**: the last parameter of a task, written `+name` (one or more)
  or `*name` (zero or more). Holds an ordered list of elements.
- **Element**: one argument of a variadic parameter, exactly as given on the command
  line, in an MCP array, or in a dependency call.
- **Warned parameter**: a parameter with no kind, or of kind `string` or `path`.
  Values of these kinds can contain shell syntax.
- **Shell body**: a task body run by a POSIX shell: the built-in shell, a `set shell`
  list, or a `[script]` command naming a known shell.
- **`raw` kind**: a parameter kind that accepts any value and declares that the value
  is split into shell words on purpose.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: `rune c yq 'x; echo pwned' tofu` (Story 1) prints exactly three lines
  and runs no injected command, under the built-in shell on Linux, macOS, and Windows.
- **SC-002**: Across a test matrix of argument lists (no elements, one element,
  elements with spaces, empty strings, single quotes, `;`, `$(...)`, backticks,
  newlines, and a leading `-`) supplied from the command line, over MCP, and by
  dependency forwarding, the command under `quote_each()` receives exactly the given
  elements, in order, in every case.
- **SC-003**: Existing compatibility corpus fixtures need no edits (the `raw` kind
  adds a new one), and every existing test that runs a task passes without
  changes.
- **SC-004**: A fixture that contains every form in Story 3 produces exactly the
  expected warnings: no unsafe form is missed and no safe or exempt form is warned.
- **SC-005**: Every documentation example and the repository's own Runefile analyze
  with zero instances of the new warning.

## Assumptions

- **Decisions made.** On 2026-11-03 the user chose: a new `quote_each()` built-in,
  rather than changing `quote()` or adding spread syntax; an analyzer warning by
  default, rather than a change in output or a per-file setting; and a warning that
  covers scalars as well as variadics, with the `raw` kind as the opt-out.
- **Constitution.** `quote_each()` adds a total, pure built-in function and no new
  syntax, but Principle III freezes the expression sublanguage. The plan amends
  Principle III (v1.1.0 to v1.2.0, MINOR, following spec 023's precedent) so the
  built-in library may grow under stated conditions. The `raw` kind is a parameter
  annotation, which Principle III already allows. Neither changes the meaning of a
  working Runefile.
- **No output change by default.** Quoting `{{p}}` automatically would change the text
  of working Runefiles; for example, `echo "hello {{name}}"` would print the quotes.
  The constitution forbids that silent change, so the safety default is a warning.
- **Forwarding rule.** FR-007 is a default chosen for this spec, so that
  `quote_each()` works across dependency calls. It keeps the text every existing
  form produces and accepts every call v0.6.0 accepted, including an empty `*`
  list forwarded into a `+` parameter; the constitution requires an opt-in for any
  break. `/speckit-clarify` can revisit it.
- **POSIX quoting only.** `quote()` and `quote_each()` use POSIX shell quoting. Quoting
  for PowerShell or `cmd` is out of scope.
- **Argument injection.** A value starting with `-` read as an option is out of scope;
  quoting cannot prevent it.
- **Warnings do not gate.** Like the existing warnings, the new one does not change
  exit codes. A way to treat warnings as errors is out of scope.
