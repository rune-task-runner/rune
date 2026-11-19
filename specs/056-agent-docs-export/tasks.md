---

description: "Task list for spec 056: export AGENTS.md guidance and a Claude skill from the Runefile with rune export"
---

# Tasks: Export AGENTS.md Guidance and a Claude Skill with `rune export`

**Input**: Design documents from `/specs/056-agent-docs-export/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and the issue's acceptance criteria ask for
them. In every phase the test tasks come first, and a test is written and seen to fail
before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on
  an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US5)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
  The target is 90% statement coverage of `internal/agentdocs`.
- Keep it simple: no abstraction, option or helper that a task below does not name.
- `internal/agentdocs` reads no file, writes no file, reads no environment and starts
  nothing. Its tests build `Source`, `Catalog` and byte slices in the test.
- Golden cases live in `internal/cli/testdata/export/<case>/`:
  - `Runefile`, and any file it imports or loads as a module.
  - `want.agents.md`: the section, as standard output holds it.
  - `want.SKILL.md`: `SKILL.md` of the skill named `rune-tasks`.
  - `want.tasks.md`: only when the catalog is above the inline limit.
  - The test loads the Runefile with `loadStatic`, builds the catalog with the real reach
    walk, and compares. Golden files are regenerated with `-update` and read, never edited
    by hand. The repository checks files out with LF on every system (`.gitattributes`), so
    the goldens compare byte for byte on Linux, macOS and Windows, which is the check of
    SC-005 across systems.
- "Nothing was written" and "nothing ran" are checked from the outside: the bytes of every
  file, the listing of the directory, a marker file.
- The exact texts are in [contracts/export-text.md](contracts/export-text.md) and
  [contracts/rune-export.md](contracts/rune-export.md). Tests compare whole strings unless
  the task says otherwise.
- The four `\u` escapes of the contract are written in Go source as a backslash, `u` and
  four hexadecimal digits. Check them in the golden files after `-update`.
- New prose and comments avoid the em dash.

---

## Phase 1: Setup

- [X] T001 Create `internal/agentdocs/doc.go` with the package comment: the package builds
  the catalog of a Runefile's public tasks, renders it as an AGENTS.md section and as a
  Claude skill, and plans the file changes from the existing bytes; it reads no file, writes
  no file, evaluates nothing and starts nothing

---

## Phase 2: Foundational (blocks every user story)

- [X] T002 [P] Write tests in `internal/agentdocs/text_test.go` for the three renderers and
  the shell quoting of [export-text.md](contracts/export-text.md):
  - `prose`: runs of white space, line breaks, control characters and other characters that
    are not printable (a zero-width or direction-changing format character) collapse to one
    space and the ends are trimmed; the six entity replacements and the eight backslash
    escapes happen in one pass, so `&` gives `&amp;` and a backtick gives `&#96;` with no
    backslash before its `#`; a first `-`, `+`, `=` and the `.` or `)` after leading digits
    get a backslash; a text over 8192 bytes is cut on a rune boundary and ends with
    ` [truncated]`; the empty text stays empty; a text that is `<!-- rune:end tasks -->`
    holds no `<` afterwards; `@docs/x.md` holds no `@` afterwards
  - `value`: `us east` gives the quoted form inside one pair of backticks; the empty value
    gives two double quotes inside backticks; a quote, a backslash and a line break are
    escaped as `strconv.Quote` does; a backtick, `$`, `<` and `>` each become their `\u`
    escape; for every case the result holds exactly two backticks and no `$`, `<` or `>`
  - `code`: `env("TAG", "latest")` is kept; a backtick, `$`, `<` and `>` inside a string of
    the expression become the four escapes
  - `shQuote`: `staging` and `a/b-c_1.2:x=y,z+w@h%` are unchanged; the empty value is `''`;
    `us east`, `a;b`, `it's` (as `'it'\''s'`), `a"b`, `a\b`, `*`, `#x`, `~`, `a&b`, `a|b`
    and a value with a non-ASCII letter are single-quoted; for every case, parsing
    `rune x <quoted>` with `mvdan.cc/sh/v3/syntax` and reading the word's literal value
    gives the original value
  - `exampleSafe`: false for a value with a backtick, a `$`, `<`, `>`, a line break, a tab
    or another character that is not printable, and for a value that starts with `-` such
    as `--yes`; true for `staging`, `us east`, `it's`, `a;b` and the empty value
- [X] T003 Implement `prose`, `value`, `code`, `shQuote`, `exampleSafe` and the constant
  `MaxText = 8 * 1024` in `internal/agentdocs/text.go` until T002 passes
- [X] T004 [P] Export the canonical text of an expression: rename `formatExpr` to `Expr` in
  `internal/formatter/formatter.go` (all callers in that file), with a doc comment, and add
  a test in `internal/formatter/formatter_test.go` for a string literal, a variable, a
  concatenation, a path join, a function call and a conditional. Write the test first
- [X] T005 Write tests in `internal/agentdocs/catalog_test.go` for `Build`, `Entry.Usage`,
  `Entry.Example` and `Catalog.Fingerprint`, on tasks built in the test with a stub `Reach`:
  - visibility: a `[private]` task, a task named `_x` and a `[context]` task have no entry;
    a module task named `ops::_helper` has one, because `Task.IsPrivate` reads the full
    name; a task with `[windows]` has one whatever the host
  - order: groups in first-seen order, declaration order inside a group, ungrouped tasks as
    their own first-seen group (the order of `visibleTasksByGroup` without its host filter)
  - fields per [data-model.md](data-model.md): `Name` with a module prefix, `Doc`, `Group`,
    `OS` in source order with each once, `Returns`, `Confirm` and `Question`, `Plan`,
    `Applies`, `ReadOnly`, `Network`, `Separator` when `Commands[Name]`
  - params: `Kind` for the four parameter kinds; `Type` and `Enum`; `HasDefault` is true
    exactly when `Kind` is `Optional`; `Literal` and `Default` for a string literal;
    `Default` as `formatter.Expr` text and `Literal` false for a function call or a
    variable; `Doc` from `[param-doc]`; `AgentValue` from `[agent-allow]`, nil when not
    named
  - `Reaches`: only reached tasks other than the entry's own that have `[confirm]` or
    `[agent-allow]`, in reach order, with `Allow` in the attribute's order; a reached task
    with both sets both
  - `Usage`: `<p>`, `[p]`, `<p>...`, `[p]...` in declaration order; `rune -- log` for a
    command name; `rune ops::restart` for a module task
  - `Example`, every row of the rule in [export-text.md](contracts/export-text.md): none
    without a parameter that needs a value; the agent value wins over the first enum value;
    the first enum value; `true` for a required boolean; `force:boolean="false"` before an
    optional `env="prod"` with the agent value `staging` gives `false staging`, not `true`;
    an optional `a="x y"` before an optional parameter with an agent value uses its default,
    quoted; an optional parameter
    whose literal default is the agent value is not a reason for an example; one whose
    default is computed is; none when a needed parameter is a free string, a number, a path
    or a raw value; none when an optional parameter in the range has a computed default and
    no enum; none when a value fails `exampleSafe`, with the enum value `--yes` as one
    case; values quoted by `shQuote`; one value for a one-or-more parameter and none for a
    zero-or-more one; `rune -- log staging` for a command name
  - `Fingerprint`: `sha256:` and 64 hexadecimal digits; equal for equal catalogs; different
    after a change to each of name, description, a parameter's type, default and doc, group,
    OS, returns, confirm question, agent value, a reached task's policy; equal when only the
    stub `Reach` returns an extra task with no limit
- [X] T006 Implement `Source`, `Catalog`, `Entry`, `Param`, `ParamKind`, `Reached`,
  `Allowed`, `Build`, `Entry.Usage`, `Entry.Example` and `Catalog.Fingerprint` (SHA-256 of
  the JSON of `struct{ V int; Entries []Entry }` with `V = 1`, R14) in
  `internal/agentdocs/catalog.go` until T005 passes
- [X] T007 [P] Make the reach walk a function. First add a test in
  `internal/cli/mcp_test.go` for `reachTasks(tasks map[string]*ast.Task, t *ast.Task,
  available func(*ast.Task) bool) []*ast.Task`: with an always-true predicate it reaches a
  `[windows]` task and what that task runs, which the host predicate on Linux skips, and it
  still does not follow the `||` hooks of a task reached only as a `||` hook. Then move the
  walk of `mcpAdapter.reachTasks` in `internal/cli/mcp.go` into that function and keep the
  method as one line that passes the host's predicate, so `sandbox.go` and the existing
  tests are unchanged
- [X] T008 [P] Extract the static half of the load. First add tests in
  `internal/cli/serve_test.go` for `loadStatic(opts Options, runefile string,
  allowIgnoreVersion bool, rec *pathRecorder) (*ast.File, diag.SourceProvider, error)`: a
  valid Runefile returns the composed file with module tasks; a parse error and an analyzer
  error give a `*ValidationError`; a missing file gives a `*UsageError`; a Runefile with a
  setting whose expression fails when it is evaluated (pick one that
  `config.ResolveSettings` evaluates, with a call to `error("evaluated")`) loads with
  `loadStatic` and is rejected by `loadModule`, which shows that the static load evaluates
  nothing. Then add the function to `internal/cli/serve.go` (read, parse,
  `enforceMinimumVersion`, compose, analyze, with the diagnostics rendered and the usual
  error types) and have `loadModuleFrom` call it

**Checkpoint**: the catalog and its text rules exist and are tested without a disk.

---

## Phase 3: User Story 1 - Print the tasks as guidance for an agent (Priority: P1) MVP

**Goal**: `rune export agents-md` prints the section on standard output.

**Independent Test**: run it against the fixtures; compare with the goldens; bind every
example with the command line's own argument handling.

### Tests for User Story 1

- [X] T009 [P] [US1] Write tests in `internal/agentdocs/render_test.go` for `Body`, the
  entry text and `Section` against [export-text.md](contracts/export-text.md): the general
  text word for word; an entry with every line of the table present, in the contract's
  order; an entry with only a usage; the description paragraph omitted when empty; each
  parameter line form (kind, each type text, literal default, expression default with
  "worked out when the task runs", the doc); the `Runs only on` texts for
  `linux`, `macos`, `windows` and `unix`; the empty catalog line; `Section` wraps the body
  in the begin line with the fingerprint, the comment line for people, and the end line, and
  ends with one line break
- [X] T010 [P] [US1] Write tests in `internal/agentdocs/plan_test.go` for `ParseFormat`:
  `agents-md` and `claude-skill` parse; any other name and the empty name give an error
  whose text lists both formats; `Formats()` returns the two names in that order
- [X] T011 [P] [US1] Add golden cases under `internal/cli/testdata/export/`: `basic` (two
  tasks with descriptions), `params` (typed, defaulted and variadic parameters,
  `[param-doc]`, `[returns]`, an enum with `us east`, `it's` and `a;b`, a default
  `env("REGION", "eu-west-1")`, a default that names a variable, and a task whose example
  needs every kind of quoting), `modules` (a `mod ops` with
  `ops.rune` that holds `restart` and `_helper`), `os` (`[linux]` with `[macos]`,
  `[windows]`, `[unix]`), `hidden` (a `[private]` task, a `_x` task, a `[context]` task, one
  public task), `commands` (tasks named `log` and `export`, two `[group]`s and an ungrouped
  task between them), `empty` (only private tasks), `hostile` (descriptions that hold
  `# Heading`, a code fence, `<!-- x -->`, `<!-- rune:end tasks -->`, `@docs/secret.md`,
  an exclamation mark followed by a command in backticks, `$ARGUMENTS`,
  `${CLAUDE_SKILL_DIR}` and the text `pass --yes`; an enum whose values are one with a
  backtick, one with `$`, one with `<!--` and `--yes`; a group and a confirm question with
  Markdown). Write the `Runefile`s only; the `want.*` files come from `-update` in T017
- [X] T012 [US1] Write the golden test and the binding tests in
  `internal/cli/export_test.go`:
  - `TestExportGolden`: for every case, `loadStatic`, `exportCatalog`, then
    `agentdocs.Section` equals `want.agents.md`
  - `TestExportExamplesBind`: for every case and every entry with an example, parse the
    example with `mvdan.cc/sh/v3/syntax`, drop `rune` and a leading `--`, run the words
    through `splitArgs` and `bindParams` with the task index of the fixture, and require
    the named task and, for each value the example was built from, the bound value
  - `TestExportUsagesNameTheirTask`: every usage starts with `rune NAME`, or with
    `rune -- NAME` exactly when the name is in `taskimport.RuneCommands`, and has one token
    per parameter
  - `TestExportOutputInvariants`: for every case, the section holds no `$` and no
    exclamation mark directly before a backtick; exactly one begin line and one end line;
    the same `### ` headings as the catalog has entries, in its order; no example holds
    `--yes` or a value that starts with `-`; outside the lines that `prose` wrote, no
    `--yes`, no ` | rune` and no `<<<`
  - `TestExportMaxText`: `agentdocs.MaxText` equals `contextMaxBytes`
- [X] T013 [US1] Write command tests in `internal/cli/export_test.go` for the print path of
  `Export(opts, flags, args)`: the section on standard output and nothing on standard error
  for a plain Runefile; no file created (the directory listing is the same); no Runefile
  gives a `*UsageError`; a Runefile with an analyzer error prints the diagnostics and gives
  a `*ValidationError` with empty standard output; no argument and two arguments give the
  `*UsageError` "rune export needs one format: ..." and an unknown format gives the
  "unknown format" one, each with the contract's whole text, which names `rune -- export`;
  a Runefile with a task named `export` prints the note on standard error, also before each
  of those three errors; with `-f` the named Runefile is exported
- [X] T014 [P] [US1] Write `cmd/rune/export_test.go`: the flags exist with their short
  forms; the format completes to the two names; the command accepts any number of arguments
  and hands them to `cli.Export`, so no argument, two arguments and an unknown format each
  end in a `*cli.UsageError`; an empty `--file` is the `*cli.UsageError` "--file needs a
  path"; the help text holds both formats, `rune -- export` and "guidance", and none of
  "restricted", "locked down", "safe", "secure", "sandboxed". Update
  `TestImporterKnowsTheCommands` in `cmd/rune/import_test.go` only if it needs it: it reads
  the commands from the binary
- [X] T015 [P] [US1] Write `test/integration/export_test.go` for the print path with the
  built binary: `rune export agents-md` in a project prints the section, exits 0 and leaves
  the directory listing unchanged; a Runefile whose every body and `[context]` task touches
  a marker file, with a default and a variable that call `error("evaluated")`, exits 0 and
  leaves no marker; with `SECRET_TOKEN=do-not-print` in the environment and in a dotenv
  file the Runefile loads, the output does not hold `do-not-print`; two runs from two
  working directories give the same bytes; no Runefile exits 2; an invalid Runefile exits 3
  with empty standard output; a Runefile with a task named `export` prints the note,
  `rune export nothing` and `rune export a b` exit 2 naming `rune -- export`, and
  `rune -- export` runs the task

### Implementation for User Story 1

- [X] T016 [US1] Implement `Body`, the entry renderer and `Section` in
  `internal/agentdocs/render.go`, and `Format`, `ParseFormat` and `Formats` in
  `internal/agentdocs/plan.go`, until T009 and T010 pass
- [X] T017 [US1] Add the command and the print path together, so that the catalog knows
  `export` as a command name before any golden is written:
  - `"export": true` in `RuneCommands` in `internal/taskimport/finish.go`
  - `cmd/rune/export.go` with `newExportCmd(opts)`: `Use: "export <format>"`, the `Short`,
    `Long` and `Example` of the contract's Help section, `Args: cobra.ArbitraryArgs`,
    completion of the two formats, the flags `-f/--file`, `--write`, `-o/--output` and
    `--check`, and "--file needs a path" for an empty `--file`; registered in `addCommands`
    in `cmd/rune/main.go`
  - `internal/cli/export.go`: `ExportFlags{Write, Output, Check}`,
    `exportCatalog(file *ast.File) agentdocs.Catalog` (the task list, `reachTasks` with an
    always-true predicate, `taskimport.RuneCommands`), and `Export(opts, flags, args)` for
    the print path: resolve the Runefile, print the note for a task named `export`, check
    the arguments and parse the format, `loadStatic` with no version override, print
    `Section` on standard output
  - run the golden test with `-update`, read every `want.agents.md` against the contract
    (the four `\u` escapes in `hostile`, `rune -- export` in `commands`, `ops::_helper` in
    `modules`), and get T012 to T015 green

**Checkpoint**: `rune export agents-md` prints a correct section. This is the MVP.

---

## Phase 4: User Story 2 - Be told the limits of a task, and of the file (Priority: P1)

**Goal**: entries state `[confirm]`, `[agent-allow]`, `[plan]`, `[applies]`, `[readonly]`,
`[network]` and what a run can reach, and no output offers a way around them.

**Independent Test**: export the `limits` fixture, read the entries, and search every
output for the forbidden texts.

### Tests for User Story 2

- [X] T018 [P] [US2] Extend `internal/agentdocs/render_test.go` with the limit lines of the
  contract: confirm with a question and without one; one "A run can reach" line per reached
  `[confirm]` task in reach order; "The author allows agents only" with one predicate and
  with two joined by ` and `; the reach line for a reached `[agent-allow]` task; a reached
  task with both gets both lines; the `[plan]` line; the `[applies]` line with the plan
  task's name; the two declaration lines; the order of all of them in one entry
- [X] T019 [P] [US2] Add the golden case `internal/cli/testdata/export/limits/Runefile`: a
  `[confirm("Deploy now?")]` task `deploy` with `[agent-allow(env="staging")]` and an enum;
  `ship` that depends on `test` and `(deploy "prod")`; `nightly` with a `||` hook that
  reaches a private `[confirm]` task `_wipe`; `winship` that depends on a `[windows]` task
  `winprep`, which depends on a `[confirm]` task, so that the reach is stated on every host;
  `publish env:enum("staging","prod")="prod"` with the agent value `staging`; `rollout
  force:boolean="false" env="prod"` with the agent value `staging` for `env`; a
  `[plan(file="tfplan")]` task and its `[applies("plan")]` task; a `[readonly]` task and a
  `[network]` task
- [X] T020 [US2] Extend `internal/cli/export_test.go`: `TestExportOutputInvariants` also
  requires, for every case, the sentences "This text is guidance. It does not limit what a
  shell can run." and "`name=value` does not pass a parameter.", and, outside the lines
  that `prose` wrote, none of `--allow-destructive`, `--sandbox`, "restricted", "locked
  down", "safe", "secure", "sandboxed". Add `TestExportLimitsExamples`: the examples of the
  `limits` case are `rune deploy staging`, `rune publish staging` and
  `rune rollout false staging`, and the entry of `winship` names the `[confirm]` task

### Implementation for User Story 2

- [X] T021 [US2] Complete the limit lines in `internal/agentdocs/render.go` until T018
  passes, regenerate the goldens with `-update`, read `limits/want.agents.md` against the
  contract and against scenarios 1 to 6 of the story, and get T020 green

**Checkpoint**: the two P1 stories about content are complete.

---

## Phase 5: User Story 3 - Put the guidance into AGENTS.md and keep what is there (Priority: P1)

**Goal**: `--write` and `--output` create the file, add the section or replace it, and
change nothing else.

**Independent Test**: write against the merge fixtures, compare every byte outside the
section, run again and see that nothing is written.

### Tests for User Story 3

- [X] T022 [P] [US3] Write tests in `internal/agentdocs/merge_test.go` for `scanMarkers` and
  `mergeSection` (R11, R12):
  - a begin line with a fingerprint, with none, and with trailing white space or CR; an end
    line; a line that only holds the marker text inside a sentence is not a marker; a marker
    inside a code fence counts; a begin line directly after a byte order mark is found
  - no existing file and an empty one: the section alone; a byte order mark alone: the mark,
    then the section, and merging the result again says unchanged
  - text with and without a final line break: the text, one empty line, the section
  - one begin line and one end line after it: the lines between are replaced and every byte
    before the begin line and after the end line is the same
  - CR LF in the file: the section's lines end with CR LF, in the append and in the replace
  - an equal section, also when the file has CR LF and the new text LF: the same bytes come
    back and the result says unchanged
  - two begin lines, a begin line with no end line, an end line with no begin line, an end
    line before its begin line: a `*Refusal` with the line of each marker
  - the old fingerprint and whether the file held a section are returned
- [X] T023 [P] [US3] Write tests in `internal/agentdocs/plan_test.go` for `PlanSection`
  and for `File.Status`: created, updated, unchanged; `Owned` and
  `OldFingerprint` for a file with a section and for one without; `Tasks` is the number of
  entries; `Size` is the length of the section in the new bytes; a refusal comes back as the
  error and no file
- [X] T024 [US3] Write command tests in `internal/cli/export_test.go` for the write path:
  `--write` creates `AGENTS.md` next to the Runefile, also when the working directory is a
  subdirectory; the report lines `created: 2 tasks, N bytes`, `updated: ...` and `unchanged`
  on standard error and nothing on standard output; a second run does not change the
  file's modification time; a hand-written file keeps every byte before the section;
  `--output docs/agents/tasks.md` creates the directories and leaves `AGENTS.md` alone;
  `--write` with `--output` writes the `--output` path; `--output` naming a directory is a
  `*UsageError`; broken markers print one diagnostic per marker line with a caret and give
  a `*UsageError`, and the file is unchanged; a write that fails (the injected writer
  returns an error) gives a `*CommandFailure` that names the file; a target that cannot be
  read (with `--write`, an `AGENTS.md` that is a directory) gives a `*CommandFailure` that
  names it; with `--write`, an `AGENTS.md` that is a symbolic link out of the project is
  refused with the text "rune export --write writes only inside the project; name the path
  with --output" and the link's target is unchanged, and the same link named with
  `--output` is written through; the `hostile` fixture written and then updated after a
  task was added has exactly one begin line and one end line and the hand-written text
  around it unchanged
- [X] T025 [P] [US3] Write tests in `internal/cli/agentinit_test.go` for the two changes of
  T027: `writePlanned` removes a file marked for removal and puts it back, with its bytes,
  when a later file fails; `insideProject(dir, target, command)` names the command it was
  given, and `rune agent init` still gets its own text. The existing tests of
  `writeClientFiles` and `insideProject` stay green
- [X] T026 [P] [US3] Extend `test/integration/export_test.go`: write into a project with a
  hand-written `AGENTS.md`, compare the bytes before the section, run again and compare the
  whole file; change the Runefile and write again, and compare the bytes outside the
  section; broken markers exit 2 with the file unchanged; `--write` through a symbolic link
  out of the project exits 2 (skipped where the system has no symbolic links)

### Implementation for User Story 3

- [X] T027 [US3] In `internal/cli/agentinit.go`: introduce `plannedFile{target string; old,
  new []byte; existed, remove bool}` and `writePlanned(files []plannedFile, write
  func(string, []byte) error) error` holding the directory creation, the atomic write, the
  removal and the rollback, and have `writeClientFiles` build its list and call it; give
  `insideProject` a third parameter, the command its message names. Get T025 green
- [X] T028 [US3] Implement `scanMarkers`, `mergeSection` and `Refusal` in
  `internal/agentdocs/merge.go`, and `File`, `File.Status` and `PlanSection` in
  `internal/agentdocs/plan.go`, until T022 and T023 pass
- [X] T029 [US3] Implement the write path in `internal/cli/export.go`: the target (the path
  of `--output` against the working directory, else `AGENTS.md` in the Runefile's directory,
  which is checked with `insideProject`), reading the existing file, `agentdocs.PlanSection`,
  rendering a refusal as diagnostics with `renderDiags`, `writePlanned`, and the report of
  [rune-export.md](contracts/rune-export.md). Get T024 and T026 green

**Checkpoint**: all three P1 stories work. `rune export agents-md --write` is safe to run
twice.

---

## Phase 6: User Story 4 - Generate a Claude skill (Priority: P2)

**Goal**: `rune export claude-skill` prints `SKILL.md`; `--write` and `--output` write the
skill directory, with `tasks.md` above the inline limit.

**Independent Test**: export a small and a large Runefile; parse the frontmatter as YAML;
follow the links; count the index lines.

### Tests for User Story 4

- [X] T030 [P] [US4] Extend `internal/agentdocs/render_test.go` for the skill
  ([export-text.md](contracts/export-text.md)):
  - `SkillName`: `rune-tasks`, `a`, `a1-b2`, `true` and `123` are valid; the empty name,
    `-a`, `a-`, `a--b`, `A`, `a_b`, `a.b` and a name of 65 characters are not, and the error
    states the rule
  - `skillDescription`: the fixed sentences around the names; names joined by `, `; with
    names that would pass 900 characters the list stops and ends with ` and K more`, and
    the whole is within 900; one name longer than 900 characters gives `N tasks`; `none
    yet` for an empty catalog; never over 1024 characters for 1000 long names; one line
  - `SkillFiles` with 30 entries: one file, `SKILL.md`, with the frontmatter (`name`, then
    `description`, both in double quotes), the generated line with the fingerprint, the
    comment line, and the same body `Section` holds
  - with 31 entries: `SKILL.md` has the index text with `N`, 31 index lines and no "more"
    line, and `tasks.md` has the generated line, the heading, the pointer to `SKILL.md` and
    every entry
  - with 100, 101 and 1000 entries: 100 index lines; the "more" line is absent, `- 1 more
    task is in [tasks.md](tasks.md).` and `- 900 more tasks are in [tasks.md](tasks.md).`;
    `SKILL.md` for 1000 entries has exactly one line more than for 100
  - an index line is the usage in backticks, a colon and the summary; a description of 200
    characters whose 100th is a backtick is cut first and escaped after, so the line ends
    with `&#96;...`; a task with no description has the usage alone
  - the frontmatter of every case parses with `go.yaml.in/yaml/v3` into exactly `name` and
    `description`, both strings without a line break, also for the names `true` and `123`
- [X] T031 [P] [US4] Extend `internal/agentdocs/merge_test.go` and
  `internal/agentdocs/plan_test.go` for skill files: `generatedFingerprint` finds the
  generated line and its fingerprint, and reports a file without one; `PlanSkill` gives, per the table "Whose file it is": `SKILL.md` created, updated and
  unchanged; a `SKILL.md` without the generated line is a `*Refusal` that names it;
  `tasks.md` created with a large catalog; a generated `tasks.md` with a small catalog is a
  file with `Remove` and the status `removed`; a `tasks.md` without the generated line is a
  `*Refusal` with a large catalog and is not in the plan with a small one; a file that
  differs only by CR LF is unchanged
- [X] T032 [US4] Extend `internal/cli/export_test.go`:
  - `TestExportGolden` also compares `want.SKILL.md` for every case and `want.tasks.md` for
    a new case `large` (a Runefile of 31 tasks)
  - for `SKILL.md` and `tasks.md` of every case: no `$`, no exclamation mark directly
    before a backtick, exactly one generated line and no begin or end line, and the
    sentences of T020 in `SKILL.md`; for a case at or under the inline limit the text of
    `SKILL.md` after its comment lines equals the text of `want.agents.md` between its
    comment line and its end line; for `large`, the `### ` headings of `tasks.md` are the
    catalog's entries and `SKILL.md` has `### Index` and no entry heading; every `](...)`
    link in the two files names a file of the skill
  - command tests: `rune export claude-skill` prints `SKILL.md` on standard output and
    writes nothing; for `large` the note about `tasks.md` is on standard error; `--write`
    creates `.claude/skills/rune-tasks/SKILL.md`; with `large` both files and two report
    lines; after the Runefile shrinks, `SKILL.md` is updated and `tasks.md` is reported as
    removed and is gone; a hand-written `SKILL.md` gives a `*UsageError` and stays; with
    `large`, a generated `SKILL.md` of an older catalog and a hand-written `tasks.md` give
    a `*UsageError` and both files stay byte for byte (every target is checked before the
    first is written); `--output .agents/skills/project-tasks` writes there with the name
    `project-tasks`; `--output Bad_Name` and `--output` naming an existing file are
    `*UsageError`s; with `--write`, a `.claude` that is a symbolic link out of the project
    is refused, and the same directory named with `--output` is written
- [X] T033 [P] [US4] Extend `test/integration/export_test.go`: `rune export claude-skill
  --write` creates the skill and a second run reports it unchanged; a Runefile whose
  description holds an exclamation mark and a command in backticks gives a `SKILL.md` in
  which no backtick comes from the description; a hand-written `SKILL.md` exits 2 and stays

### Implementation for User Story 4

- [X] T034 [US4] Implement `SkillName`, `skillDescription`, the YAML string writer and
  `SkillFiles`, with the constants `inlineLimit = 30` and `indexLimit = 100`, in
  `internal/agentdocs/render.go`, and `generatedFingerprint` in
  `internal/agentdocs/merge.go` and `PlanSkill` in
  `internal/agentdocs/plan.go`, until T030 and T031 pass
- [X] T035 [US4] Extend `internal/cli/export.go` for `claude-skill`: the print path with the
  note; the target directory (the path of `--output`, else `.claude/skills/rune-tasks` in
  the Runefile's directory, which is checked with `insideProject`) with the name rule and
  the usage errors; reading `SKILL.md` and `tasks.md`; the write with removal; the refusal
  text of the contract. Add the `large` case, regenerate the goldens with `-update`, read
  `want.SKILL.md` of `basic`, `hostile` and `large` against the contract, and get T032 and
  T033 green

**Checkpoint**: both formats work in print and write mode.

---

## Phase 7: User Story 5 - Find stale guidance in CI (Priority: P2)

**Goal**: `--check` says whether the target is what Rune would write now, with the reason,
and never writes.

**Independent Test**: write, check (0), change a parameter, check (1 with the reason),
compare the directory before and after.

### Tests for User Story 5

- [X] T036 [P] [US5] Extend `internal/agentdocs/plan_test.go` for `File.CheckState`, one
  case per row of the table in [data-model.md](data-model.md): `stale: no longer part of
  the skill` for a removal; `missing` for a file that did not exist and for a Markdown file
  with no section; `current` for an unchanged file; `stale: the Runefile changed since the
  export` when the old fingerprint differs; `stale: the text differs from what this rune
  generates (edited by hand, or written by another rune version)` when the fingerprints are
  equal
- [X] T037 [US5] Extend `internal/cli/export_test.go` for the check path: a written export
  is current and the run returns nil; the report line per file; after a parameter was
  added: the reason "the Runefile changed", the line `regenerate with: rune export
  agents-md --write`, a `*CommandFailure`, and every file byte for byte as before; after a
  hand edit of the section: the reason "the text differs"; no file and no section:
  `missing`; a change to a task body only, to a private task that nothing reaches, and to a
  comment that is no description: current, with the same fingerprint; a file converted to
  CR LF: current; a skill with a leftover `tasks.md` and one that lacks a needed
  `tasks.md`: stale; broken markers, a hand-written `SKILL.md`, and a documented location
  that a symbolic link takes out of the project: the refusal of a write, as a
  `*UsageError`; `--check --write` is a `*UsageError`; `--check --output p` checks `p` and
  the regenerate line holds `--output p`; with `-f`, the regenerate line holds `-f` and the
  path as one shell word
- [X] T038 [P] [US5] Extend `test/integration/export_test.go`: write, `--check` exits 0; add
  a task, `--check` exits 1 and the directory is byte for byte the same; write, `--check`
  exits 0; the same three steps for `claude-skill`

### Implementation for User Story 5

- [X] T039 [US5] Implement `File.CheckState` in `internal/agentdocs/plan.go` and the check
  path in `internal/cli/export.go` (the same target and the same plan as a write, the
  report and the regenerate line of the contract, no write) until T036 to T038 pass

**Checkpoint**: all five stories work.

---

## Phase 8: Polish and cross-cutting

- [X] T040 [P] Write `docs/how-to/agent-instructions.md` (FR-046 to FR-049): what the
  command is for; the two formats with a short real example of each; print, `--write`,
  `--output` and `--check`; the marker lines and what Rune owns; regenerating; a CI step;
  how each output reaches an agent, stating only the facts of research R17 with links to
  the three sources; the size note for `AGENTS.md` and the pointer to the skill for large
  catalogs; a section "Guidance, not enforcement" that links to `../mcp.md` and
  `agent-setup.md`; what the text never contains and how text from the Runefile is escaped;
  that examples are for a POSIX shell; regenerating after a Rune upgrade; removing an
  export; a task named `export`. Every `rune` code block must validate in the docs harness
- [X] T041 [P] Update the other pages: the `rune export` row and `export` among the command
  names in `docs/cli.md`; the page in `docs/how-to/README.md` and `docs/nav.yaml`; one
  pointer each in `docs/mcp.md`, `docs/use-cases/mcp-agents.md` and
  `docs/how-to/agent-setup.md`; `export` in the lists of command names in
  `docs/how-to/import-from-other-runners.md` and `docs/troubleshooting.md`
- [X] T042 Run the docs harness and the site check and fix what they report:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...` and
  `go run ./cmd/docsite -check`
- [X] T043 Run the whole suite, the race run and the linter, and fix what they report:
  `go test ./...`, `go test -race ./...` with `CGO_ENABLED=1`, `golangci-lint run` in the
  `golangci/golangci-lint:v2.14` image, and the golden gate
  (`go test ./internal/cli -update` leaves `git diff` empty)
- [X] T044 Measure coverage of `internal/agentdocs` (target 90%) and of the new functions in
  `internal/cli/export.go`, and add a test for each branch that has none
- [X] T045 Walk through [quickstart.md](quickstart.md) sections 2 to 9 with a built binary
  and record any difference from the expected output as a defect to fix
- [X] T046 Carry the breaking change into the release notes (FR-049): the commit and the
  pull request title are `feat(cli)!: breaking: ...` and say that `rune export` is now a
  command and that a task named `export` is run with `rune -- export`, as the titles of
  specs 052 and 055 did, so that `cliff.toml` lists it under the breaking changes

---

## Dependencies

- Phase 2 blocks every story. T003 follows T002; T006 follows T005 and needs T003 and T004.
- US1 (T009 to T017) needs Phase 2. T016 follows T009 and T010; T017 follows T011 to T016
  and needs T007 and T008.
- US2 (T018 to T021) needs US1.
- US3 (T022 to T029) needs US1. T027 follows T025; T028 follows T022 and T023; T029 follows
  T024, T026, T027 and T028.
- US4 (T030 to T035) needs US3 (the write path and the planned files).
- US5 (T036 to T039) needs US3, and US4 for the skill cases.
- Phase 8 needs all stories.
- Tasks that extend `internal/cli/export_test.go` (T012, T013, T020, T024, T032, T037) and
  `test/integration/export_test.go` (T015, T026, T033, T038) touch one file each and run in
  that order.

## Parallel examples

- Phase 2: T002, T004, T007 and T008 touch four different files.
- US1: T009, T010, T011, T014 and T015 are in five different places.
- US3: T022, T023, T025 and T026.
- Phase 8: T040 and T041.

## Implementation strategy

1. Phases 1 to 3 give the MVP: `rune export agents-md` on standard output.
2. Phase 4 completes what an entry says. Phases 3 and 4 together are a correct export.
3. Phase 5 makes it safe to write into a hand-written file.
4. Phases 6 and 7 add the skill and the check.
5. Phase 8 ships the documentation and passes the gates.
