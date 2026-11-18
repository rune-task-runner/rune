---

description: "Task list for spec 054: import tasks from a justfile, Makefile, Taskfile or package.json with rune import"
---

# Tasks: Import Tasks with `rune import`

**Input**: Design documents from `/specs/054-task-import/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and the issue's acceptance criteria ask for
them. A test is written before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US8)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
  The target is 90% statement coverage of `internal/taskimport`.
- Keep it simple: no abstraction, option or helper that a task below does not name.
- Golden cases live in `internal/taskimport/testdata/<format>/<case>/` as `source`,
  `want.Runefile` and `want.report`. They are regenerated with `-update` and read, never
  edited by hand. A case directory with a file named `wrap` is converted with wrappers.
- "Nothing ran" and "nothing was written" are checked from the outside: a marker file, the
  bytes of the source, the listing of the directory.
- The exact texts are in [contracts/rune-import.md](contracts/rune-import.md) and the
  conversion rules in [contracts/support-matrix.md](contracts/support-matrix.md). Tests
  compare whole strings unless the task says otherwise.
- New prose and comments avoid the em dash.

---

## Phase 1: Setup

- [X] T001 Create `internal/taskimport/doc.go` with the package comment: what the package
  does, that it only reads text and starts nothing, and that it is not the Runefile `import`
  statement

---

## Phase 2: Foundational (blocks every user story)

- [X] T002 [P] Write tests in `internal/taskimport/names_test.go` for `mapNames` and
  `validName`: a valid name is kept; `test:unit` gives `test-unit`; `import`, `set`, `mod`,
  `if`, `else` get the prefix `task-`; `1x` gives `task-1x`; a name of only invalid
  characters gives `task`; two names that map to the same name give `-2`, `-3` in source
  order; a kept name is never taken by a mapped one
- [X] T003 [P] Write tests in `internal/taskimport/shellline_test.go` for `parseLine` (a line
  that parses; a line that does not; `{{name}}` does not break parsing), `changesState`
  (every builtin that is not harmless, a bare assignment, a non-literal command name,
  `command cd`, `for`, a function, `${X:=v}`, `printf -v`; false for `echo a && ls | wc`,
  `(cd x && ls)`, `A=1 cmd`, `printf '%s'`), `shellBody` (prefixes, blank lines, a wrapped
  state change, an unwrapped last line, a line that is not a complete command, a command that
  starts with `@` or `-`, a state change that ends in a comment, a line break), `escapeBraces`
  and `shellQuote`
- [X] T004 [P] Write tests in `internal/taskimport/emit_test.go` for `quote` (escapes `\\`,
  `\"`, newline, tab, carriage return; rejects another control character), `emitRunefile`
  (header comment, variables, a task with description, attributes one per line, parameters,
  dependencies, `&&` hooks, four-space body; a task without a body) and `emitReport` (line
  form of the contract, the `->` mapping, reasons joined with `; `, sorting by line with a
  task before an item, the summary, the partial flag)
- [X] T005 [P] Write tests in `internal/taskimport/finish_test.go` for `finish`: dependency
  names are mapped; a dependency on an undefined task creates a failing task with the line of
  its first referrer and the note `not defined in <file>`; an unsupported task becomes a
  failing task that keeps `private` and takes `*args`, with its message shell-quoted and `{{`
  escaped; an item that affects every task adds `see <file>:<line>` to converted and review
  tasks and leaves unsupported ones; a task named like a rune command is review with the note
  of the matrix; a task with `confirm` and dependencies is review; a dependency cycle is a
  `*ParseError` (`findCycle`)
- [X] T006 [P] Write tests in `internal/taskimport/taskimport_test.go` for `ParseFormat` (the
  four names; an unknown name gives the contract's message), `FormatOf` (every file name of
  the data model, an unknown name), `Candidates` (the names in a fixed order), `Runner`,
  `Status.String` and `ParseError.Error`, and in `internal/taskimport/vars_test.go` for
  `addVars` (forward references, a bad variable, a user of a bad variable, a cycle, an
  unknown name, a name that is not a valid Rune name)
- [X] T007 [P] Implement `internal/taskimport/names.go` (`validName`, `mapNames`) per the name
  mapping of the data model
- [X] T008 [P] Implement `internal/taskimport/shellline.go` (`parseLine`, `changesState`,
  `shellBody`, `escapeBraces`, `shellQuote`) with `mvdan.cc/sh/v3/syntax` only
- [X] T009 [P] Implement `internal/taskimport/emit.go` (`quote`, `emitRunefile`,
  `emitReport`) and `internal/taskimport/vars.go` (`addVars`)
- [X] T010 Implement `internal/taskimport/finish.go` (`finish`, `findCycle`, the failing task
  of research R6)
- [X] T011 Implement `internal/taskimport/taskimport.go`: `Format`, `Status`, `Task`, `Call`,
  `Var`, `Item`, `Result`, `Options` as in [data-model.md](data-model.md); `ParseFormat`,
  `FormatOf`, `Candidates`; and `Convert(data []byte, opts Options) (*Result, error)`, which
  calls the reader of the format, then `finish`, then the emitter. A reader error is a
  `*ParseError{Line, Msg}`. A reader that does not exist yet returns an error until its story
- [X] T012 Write `internal/taskimport/golden_test.go`: `TestGolden` walks
  `testdata/<format>/<case>/`, calls `Convert`, checks that the Runefile parses and analyzes,
  and compares `want.Runefile` and `want.report` (`-update` regenerates);
  `TestConvertTruncated` converts every prefix of every golden source: no call panics, and
  each returned Runefile analyzes; `TestStaticImports` fails when a non-test file of the
  package imports `os`, `os/exec`, `net`, `net/http` or `mvdan.cc/sh/v3/interp`
- [X] T013 Add `./internal/taskimport` to the `Regenerate goldens` step of the `golden` job
  in `.github/workflows/ci.yml`

**Checkpoint**: the shared pipeline works on hand-built task records.

---

## Phase 3: User Story 1 - Import a simple justfile (Priority: P1) MVP

**Goal**: `rune import` in a directory with a justfile writes a Runefile whose tasks behave
as the recipes did.

**Independent Test**: import `test/integration/testdata/import/justfile/`, analyze the
result, run each task and compare with the recorded behavior of just.

- [X] T014 [P] [US1] Add golden cases under `internal/taskimport/testdata/justfile/`:
  `basic` (description, parameters with defaults, variadics, dependencies with arguments,
  `&&`, variables with `+` and `/`, `@` and `-` prefixes, a quiet recipe, `\` continuation,
  every converted attribute, a `_private` recipe, a literal `{{{{`) and `state` (a `cd` line
  followed by a command, an `export` line, a last line that changes state)
- [X] T015 [P] [US1] Write tests in `internal/taskimport/just_test.go` for the helpers of the
  reader: `justString` (single and double quotes, each accepted escape, a rejected escape),
  `justExpr` (literals, names, `+`, `/`; a call, a backtick and a conditional are rejected),
  `justHeader`, `justParams`, `justDeps`, `justAttrs`, `justFields`, `justArity`, and for the
  errors of `readJust` (a line that is no justfile construct, an indented line outside a
  recipe, a line indented less than its recipe, a call with too few or too many arguments)
- [X] T016 [US1] Implement `internal/taskimport/just.go`: `readJust(text)` and the helpers of
  T015, per the justfile table of the support matrix
- [X] T017 [P] [US1] Write tests in `internal/cli/import_test.go` for `Import`: the source is
  found in the working directory, in a directory argument and from a file argument; the
  default output is `Runefile` next to the source; the report goes to standard error and ends
  with `wrote <path>`; the source bytes are unchanged; the written text passes the analysis
  service
- [X] T018 [US1] Implement `internal/cli/import.go`: `ImportFlags{From, Output, DryRun, Wrap,
  Force}` and `Import(opts Options, flags ImportFlags, source string) error`: resolve the
  source and format per the contract, read it, call `taskimport.Convert`, check the text with
  `checkGenerated` (parse and analyze through the analysis service with the text as content,
  research R10), write it, print the report. Export `IsRunefileName` from
  `internal/config/discover.go`, used by `runefileIn`, with a test
- [X] T019 [P] [US1] Write tests in `cmd/rune/import_test.go`: the command is registered,
  takes at most one argument, passes each flag to `cli.Import`, and its help has the flags
  and examples of the contract and says that the command is not the `import` statement of a
  Runefile; and a test that the rune command names `internal/taskimport` knows are the
  commands of the root
- [X] T020 [US1] Implement `cmd/rune/import.go` (`newImportCmd`) and register it in
  `addCommands` in `cmd/rune/main.go`
- [X] T021 [US1] Add the fixture project `test/integration/testdata/import/justfile/` and
  write `test/integration/import_test.go` with `TestImportParity`: a table of cases
  (fixture, task and arguments, expected standard output, expected failure and exit status)
  covering standard output, a failing line, a `-` line, the working directory after `cd`,
  an argument with spaces, a shell variable set on one line and read on the next, and the
  order of dependencies. The test copies the fixture to a temporary directory, runs
  `rune import` (exit 0, every line converted or review: SC-001), `rune analyze`, then each
  case with `--quiet`. With `RUNE_TEST_SOURCE_RUNNERS=1` and the runner on the path it also
  runs the runner and compares standard output and success with the record

**Checkpoint**: US1 is usable and demonstrable by itself.

---

## Phase 4: User Story 2 - Know exactly what was not converted (Priority: P1)

**Goal**: every source task appears once in the report with a status, a line and a reason,
and the exit status says whether the import was complete.

**Independent Test**: import a justfile that mixes supported and unsupported constructs;
check the report, the exit status and that running an unsupported task fails with the reason.

- [X] T022 [P] [US2] Add golden cases under `internal/taskimport/testdata/justfile/`:
  `unsupported` (one recipe for every unsupported row of the justfile table: a backtick
  variable and a recipe that uses it, a call in an interpolation, an undefined name in an
  interpolation, a shebang recipe, an unknown attribute, a parameter with an expression
  default, a recipe defined once per operating system, `alias`, `import`, `mod`, a dependency
  on an undefined recipe), `settings` (a `set shell` line: every recipe is `review` with
  `see justfile:<line>`), `names` (recipes named `set` and `if`, and one that collides with a
  mapped name) and `commands` (recipes named `help` and `serve`, and a `[confirm]` recipe
  with a dependency)
- [X] T023 [US2] Make the cases of T022 pass in `internal/taskimport/just.go` and
  `internal/taskimport/finish.go`
- [X] T024 [P] [US2] Extend `internal/cli/import_test.go`: exit status 0 for a complete
  import, 1 with `import: <n> of <m> items were not converted` for a partial one (the file is
  still written), 3 with the rendered diagnostic of the contract (`file:line:col`, the line,
  a caret) and no file for a source that does not parse, 2 for a source with no tasks and for
  one that cannot be read; `checkGenerated` returns an error for a text that does not analyze
- [X] T025 [US2] Implement the exit statuses of research R13 in `internal/cli/import.go`
  with `CommandFailure`, `UsageError` and `ValidationError`, and render a `*ParseError`
  through `internal/diag`
- [X] T026 [US2] Extend `test/integration/import_test.go` with `TestImportReport`: the
  report of a mixed justfile is identical on two runs, has exactly one line per recipe, and
  running an unsupported task, and a task that depends on it, fails with the reason and
  exit status 1

---

## Phase 5: User Story 3 - Import never runs code and never damages files (Priority: P1)

**Goal**: nothing is executed or fetched, the source is never changed, and an existing
Runefile is kept unless `--force` is given.

**Independent Test**: import the adversarial fixtures; no marker file exists, sources and an
existing Runefile are unchanged.

- [X] T027 [P] [US3] Extend `internal/cli/import_test.go`: an existing `Runefile` or
  `.runefile` in the source's directory refuses the default output with the contract's
  message and exit 2, and names that file; `--output` to a new path writes it and leaves the
  Runefile unchanged; `--output` to an existing path is refused; `--force` replaces;
  `--output other/Runefile` beside `other/.runefile` is refused even with `--force`;
  `--dry-run` prints the text to standard output, ends the report with
  `dry run: nothing written` and creates no file; an output in another directory adds the
  `note:` line, and is refused with `--wrap`; no source, several sources, an unknown `--from`
  and a file name without a known format each give the contract's message and exit 2; the
  candidates come from the directory listing, so one entry is one candidate
- [X] T028 [US3] Implement the output rules of research R12 and the source rules of the
  contract in `internal/cli/import.go`
- [X] T029 [US3] Add `test/integration/testdata/import/unsafe/` with one adversarial source
  per format, each of which would create `MARKER` if a variable command, an include, a
  script or a body ran (a just backtick, `$(shell touch MARKER)`, a Taskfile `sh:` variable
  and a remote include, an npm `preinstall` script), and write `TestImportIsStatic` in
  `test/integration/import_test.go`: import each with `--from` and `-o` to its own output,
  assert no `MARKER`, unchanged source bytes and no file other than the outputs

---

## Phase 6: User Story 4 - Import phony targets from a Makefile (Priority: P2)

**Goal**: `.PHONY` command targets become tasks; build rules are reported.

**Independent Test**: import a Makefile with phony targets, variables, a file target and a
pattern rule; the phony targets run as under make.

- [X] T030 [P] [US4] Add golden cases under `internal/taskimport/testdata/make/`: `basic`
  (phony targets with prerequisites, `##` and comment descriptions, `@`, `-` and `+`
  prefixes, `\` continuation, `$$`, variables with `=`, `:=`, `?=` and references, a `cd`
  line, a literal `{{`, a phony target with `/` in its name, `.DEFAULT_GOAL`), `review` (a
  plain target not in `.PHONY`, a prerequisite that is not a target), `unsupported` (one
  target or item for every unsupported row of the Makefile table) and `filewide` (`SHELL`,
  `.ONESHELL`, `export`)
- [X] T031 [P] [US4] Write tests in `internal/taskimport/make_test.go` for the helpers:
  `makeLines` (joins continuations, keeps line numbers), `makeComment`, `makeValue`,
  `makeExpand` (`$(NAME)`, `${NAME}`, `$$`, and each rejected `$` form), `makePrefixes`, and
  for the errors of `readMake`
- [X] T032 [US4] Implement `internal/taskimport/make.go`: `readMake(text)` and the helpers of
  T031, per the Makefile table of the support matrix
- [X] T033 [US4] Add `test/integration/testdata/import/make/` and parity cases for make in
  `test/integration/import_test.go`: standard output, a failing line, a `-` line, the
  directory after `cd`, a variable override (`rune NAME=value task`), the order of
  prerequisites. Runner command for the opt-in check: `make -s -f Makefile <target>`

---

## Phase 7: User Story 5 - Import a Taskfile (Priority: P2)

**Goal**: tasks with the supported keys become tasks with the same behavior.

**Independent Test**: import a Taskfile that uses the supported keys and run its tasks.

- [X] T034 [P] [US5] Add golden cases under `internal/taskimport/testdata/task/`: `basic`
  (string, list and mapping tasks, `desc`, `cmds`, `cmd`, command mappings, `deps`, task
  calls at the start and at the end, variables and references, `{{.CLI_ARGS}}`, `internal`,
  `prompt`, task-level and top-level `silent`, `ignore_error`, a name with `:`), `review`
  (`env`, `dir`, `sources` and `generates`, `summary`, `aliases`, `label`, a task that uses
  `{{.CLI_ARGS}}` and is called by another) and `unsupported` (one task or item for every
  unsupported row of the Taskfile tables, including a task that calls the same task twice)
- [X] T035 [P] [US5] Write tests in `internal/taskimport/taskfile_test.go` for the helpers:
  `taskTemplate` (`{{.NAME}}`, `{{ .NAME }}`, `{{.CLI_ARGS}}`, a rejected action, a literal
  without actions), `taskValue`, `taskScalar` (text, number, boolean; a map is rejected),
  `taskVersion`, and for the errors of `readTaskfile`
- [X] T036 [US5] Implement `internal/taskimport/taskfile.go`: `readTaskfile(text)` and the
  helpers of T035 with `yaml.Node`, per the Taskfile tables of the support matrix
- [X] T037 [US5] Add `test/integration/testdata/import/task/` and parity cases for Task in
  `test/integration/import_test.go`: standard output, a failing command, `ignore_error`,
  the directory after `cd`, an extra argument with a space received as one argument, the
  order of task calls, a `prompt` task run with `--yes`, an `internal` task absent from
  `rune --list`. Runner command for the opt-in check:
  `task --silent --taskfile Taskfile.yml <task>`

---

## Phase 8: User Story 6 - Import npm scripts (Priority: P2)

**Goal**: each script becomes a task with npm's hooks, path, environment and argument
forwarding.

**Independent Test**: import a `package.json` with `pre` and `post` scripts and a program in
`node_modules/.bin`; the task runs all three in order and forwards arguments.

- [X] T038 [P] [US6] Add golden cases under `internal/taskimport/testdata/npm/`: `basic`
  (scripts in a non-alphabetical order, a `pre` and `post` pair, a name with `:`, a script
  with `{{`, `name` and `version` present), `review` (a script that calls `npm run`, a script
  named `prepare`, a hook that has a hook of its own), `unsupported` (a script that reads
  `npm_config_registry`, one that reads `INIT_CWD`, a script with a line break, a script the
  shell cannot parse, a value that is not text) and `noscripts`
- [X] T039 [P] [US6] Write tests in `internal/taskimport/npm_test.go` for the helpers:
  `npmScripts` (order and line of each key, a missing or empty `scripts`, invalid JSON with
  its line), `npmEnvUse` (the three allowed names, another `npm_` name, `INIT_CWD`) and
  `npmCalls` (`npm`, `npx`, `yarn`, `pnpm` as words; not `npmx`)
- [X] T040 [US6] Implement `internal/taskimport/npm.go`: `readNPM(text)` and the helpers of
  T039, per the npm table of the support matrix
- [X] T041 [US6] Add `test/integration/testdata/import/npm/` and parity cases for npm in
  `test/integration/import_test.go`: the order of `pre`, main and `post`; `post` skipped when
  the main script fails; the program found on the path; an argument with spaces received as
  one argument by the main script only; `npm_lifecycle_event`, `npm_package_name` and
  `npm_package_version`. The test writes the program into `node_modules/.bin/` of the
  temporary copy with mode 0755, because the repository ignores `node_modules/`. The cases
  that start it are skipped on Windows. Runner command for the opt-in check:
  `npm run --silent <script> -- <args>`

---

## Phase 9: User Story 7 - Wrappers (Priority: P3)

**Goal**: with `--wrap`, an unsupported task runs through its original runner and says so.

**Independent Test**: import a source with an unsupported task using `--wrap`; the report
says `wrapper`, the description names the runner, and the exit status is 0.

- [X] T042 [P] [US7] Extend `internal/taskimport/finish_test.go` and add the golden case
  `internal/taskimport/testdata/<format>/wrap/` for each format (with a `wrap` file): the
  body of research R7 for the format with the name and the file shell-quoted, the description
  `Runs "<runner> <name>" (wrapper, needs <runner>).`, the status `wrapper`, the partial flag
  false when only wrappers remain, and a failing task kept for a task the runner cannot run
  by name and for an undefined task
- [X] T043 [US7] Implement wrappers in `internal/taskimport/finish.go`
- [X] T044 [US7] Extend `test/integration/import_test.go` with `TestImportWrap`: `--wrap` on
  a Makefile whose only unsupported parts are targets gives exit 0; the wrapper is listed
  with its description; without `--wrap` no body starts with the runner's name. Where `make`
  is on the path, running the wrapper prints what the target prints

---

## Phase 10: User Story 8 - Documentation (Priority: P3)

**Goal**: the support matrix, the statuses, wrappers and a just walkthrough are documented,
and the help, the CLI reference and completion agree.

**Independent Test**: the docs harness passes and completion offers the command and formats.

- [X] T045 [P] [US8] Write `docs/how-to/import-from-other-runners.md`: what the command does
  and that it runs nothing; the statuses and exit statuses; the differences that hold for a
  whole format; the support matrix of the four formats; wrappers; merging with the `import`
  statement; a short walkthrough from a justfile to a Runefile. Add the page to
  `docs/nav.yaml` and `docs/how-to/README.md`, and regenerate the site tree with
  `go run ./cmd/docsite` if it is committed
- [X] T046 [P] [US8] Update `docs/cli.md` (the command row with its flags and exit statuses),
  `docs/how-to/imports-and-modules.md` and `docs/runefile.md` (one sentence each: the
  statement is not the command) and `docs/getting-started.md` (one pointer for people coming
  from another runner). Add a test in `test/docs/cli_test.go` that the row of `rune import`
  in `docs/cli.md` names every flag of `rune import --help` (SC-008)
- [X] T047 [US8] Add `--from` completion (`RegisterFlagCompletionFunc`) in
  `cmd/rune/import.go`, with a test in `cmd/rune/import_test.go` and a case in
  `test/integration/import_test.go` that `rune __complete imp` offers `import` and
  `rune __complete import --from ""` offers the four formats

---

## Phase 11: Polish

- [X] T048 Run the whole suite, the race run and the linter in Docker and fix what they find:
  `go test ./...`, `go test -race ./...`, `golangci-lint run` (the version CI pins), gofumpt
- [X] T049 Measure the coverage of `internal/taskimport` (`go test -cover`) and add cases
  until it is at least 90%
- [X] T050 Walk through [quickstart.md](quickstart.md) scenarios A to N with a built binary.
  Run the make parity cases against `make` in the test container with
  `RUNE_TEST_SOURCE_RUNNERS=1`, and check the npm records by hand with `npm run` on the
  fixture

---

## Dependencies

- Phase 2 blocks every story. Within it, T002 to T006 (tests) come before T007 to T011.
- US1 (T014 to T021) gives the command and the first reader; US2 and US3 extend the same
  files and come after it.
- US4, US5 and US6 each need only Phase 2 and the command of US1; they do not depend on each
  other.
- US7 needs the readers of US1 and US4 to US6 for its golden cases. US8 needs the command
  and its final messages.

## Parallel examples

- Phase 2: T002, T003, T004, T005 and T006 are in different test files; T007, T008 and T009
  are in different source files.
- After US1: the readers of US4 (T030 to T032), US5 (T034 to T036) and US6 (T038 to T040)
  are in different files and can be written side by side. Their parity cases (T033, T037,
  T041) share `test/integration/import_test.go` and are done one after another.

## Implementation strategy

1. MVP: Phases 1 to 3. A justfile imports and runs.
2. Trust: Phases 4 and 5. The report, the exit statuses and the safety rules.
3. Breadth: Phases 6 to 8, one format at a time.
4. Phases 9 to 11: wrappers, documentation, polish.
