# Feature Specification: Import Tasks from a justfile, Makefile, Taskfile or package.json with `rune import`

**Feature Branch**: `054-task-import`

**Created**: 2026-11-18

**Status**: Draft

**Input**: GitHub issue [#52](https://github.com/rune-task-runner/rune/issues/52)
(backlog ADOPT-1): "Import justfile, Makefile, Taskfile.yml and package.json tasks into a
Runefile"

## Overview

Trying Rune on an existing project means rewriting its task catalog by hand. This feature adds
`rune import`, which reads one task file of another runner and writes a Runefile with the same
tasks, so a team can adopt Rune one project at a time.

```text
$ rune import
justfile:2: unsupported: variable tag: its value runs a command (backtick), which import never runs
justfile:4: converted: build
justfile:9: converted: test
justfile:14: review: serve: serve is also a rune command: run the task with rune -- serve
justfile:20: unsupported: version: it uses the variable "tag" of line 2, which was not converted
summary: 2 converted, 1 review, 2 unsupported, 0 wrappers
wrote Runefile
```

| Question | Today | After |
|---|---|---|
| How do I try Rune on a project that uses just, make, Task or npm scripts? | Rewrite every task by hand | `rune import` writes the Runefile |
| Did the conversion change what a task does? | Unknown until it breaks | Each task is reported as converted, to review, or unsupported, with the source line and the reason |
| What happens to a task Rune cannot express? | It is easy to drop by mistake | It stays in the Runefile as a task that fails with the reason, or as a wrapper that calls the original runner when `--wrap` is given |
| Does importing run my project's code? | Not applicable | Never: the source is only read |
| Can it damage my files? | Not applicable | The source is never changed, and an existing Runefile is never replaced unless `--force` is given |

The four sources are a justfile, a Makefile, a Taskfile (Task version 3) and the `scripts` of a
`package.json`. Each has a documented subset that converts. Similar syntax is not treated as
the same behavior: a construct converts only when the converted task does what the source task
did.

### The three statuses

- **converted**: the task is in the documented subset and behaves as the source task did.
- **review**: the task was converted, and it differs from the source in a way the report
  names. The person decides whether the difference matters.
- **unsupported**: the task was not converted. The report names the construct and its line.

With `--wrap`, an unsupported task becomes a **wrapper**: a task that runs the original runner.
A wrapper is reported as a wrapper, never as converted.

### Out of scope

- Merging into an existing Runefile. The documented way is to write the import to another file
  and name it in the existing Runefile with an `import` statement.
- Importing several sources in one run, and following the includes of a source.
- Converting a Runefile back to another format.
- A machine-readable report. The report is text with one line per item.
- Formats other than the four named, and Taskfile versions before 3.
- Running any source runner to compare behavior during an import.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Import a simple justfile and run the same tasks with Rune (Priority: P1)

A developer whose project has a justfile runs `rune import` in the project directory. Rune
finds the justfile, writes a `Runefile` next to it, and prints one line per recipe. The
developer runs `rune build` and gets what `just build` did.

**Why this priority**: just is the closest match to a Runefile, so it is where adoption starts.
This story alone is a usable feature.

**Independent Test**: Import a justfile with recipes that have documentation comments,
parameters with defaults, dependencies, variables and `@` and `-` line prefixes. The Runefile
passes `rune analyze`, and each task prints the same output and succeeds or fails as the
recipe did.

**Acceptance Scenarios**:

1. **Given** a directory with a justfile and no Runefile, **When** the person runs
   `rune import`, **Then** a `Runefile` is written next to the justfile, every recipe is listed
   with its status and source line, and the justfile is unchanged.
2. **Given** a recipe with a documentation comment, a parameter with a default and a
   dependency, **When** it is imported, **Then** the task has the same name, description,
   parameter, default and dependency, in the source order.
3. **Given** a recipe whose first line is `cd sub` and whose second line runs a command,
   **When** the imported task runs, **Then** the second command runs in the directory it ran in
   under just, because just starts every line in a new shell.
4. **Given** a recipe with a line that fails and a line with the `-` prefix that fails,
   **When** the imported task runs, **Then** it stops and continues at the same lines as under
   just and exits with the failing command's status.
5. **Given** a recipe that passes an argument with spaces to a command, **When** the imported
   task is run with the same argument, **Then** the command receives the same arguments.

---

### User Story 2 - Know exactly what was not converted (Priority: P1)

A developer imports a task file that uses constructs Rune cannot reproduce. The report says
which tasks converted, which need review and which are unsupported, each with the source file
and line and a reason the developer can act on. The exit status tells a script whether the
import was complete.

**Why this priority**: an import that drops a prerequisite or changes behavior without saying
so is worse than no import. The report is what makes the result trustworthy.

**Independent Test**: Import a source that mixes supported and unsupported constructs. Every
source task appears exactly once in the report, the unsupported ones have a line and a reason,
the command exits with the partial status, and running an unsupported task in the Runefile
fails with the same reason.

**Acceptance Scenarios**:

1. **Given** a source with one unsupported task, **When** it is imported, **Then** the report
   has one `unsupported` line with the file, the line and the construct, the other tasks are
   converted, and the command exits 1.
2. **Given** a converted task that depends on an unsupported task, **When** the converted task
   is run, **Then** the run fails at the unsupported task with a message naming its source
   line. The dependency is never skipped.
3. **Given** a source in which every task converts, **When** it is imported, **Then** the
   command exits 0.
4. **Given** a source the format's reader cannot parse, **When** it is imported, **Then**
   nothing is written, the error names the file and line, and the command exits 3.
5. **Given** the same source imported twice, **When** the two reports and Runefiles are
   compared, **Then** they are identical byte for byte.
6. **Given** two source tasks whose names map to the same Rune name, **When** they are
   imported, **Then** the second gets a numbered name, and the report shows both mappings.

---

### User Story 3 - Import never runs code and never damages files (Priority: P1)

A developer imports a task file from a project they do not fully trust, or one whose variables
run commands. The import reads the file and nothing else happens: no command runs, no include
is fetched, the source is unchanged, and an existing Runefile is kept.

**Why this priority**: the command is run on unfamiliar projects. It must be safe to run
anywhere.

**Independent Test**: Import fixtures whose variables, includes and bodies would create a
marker file if anything were executed. No marker file exists afterwards, the sources are
unchanged, and an existing Runefile is unchanged.

**Acceptance Scenarios**:

1. **Given** a justfile with a backtick variable, a Makefile with `$(shell ...)`, a Taskfile
   with a `sh:` variable and a remote include, and a `package.json` with an install script,
   **When** each is imported, **Then** none of those commands runs and nothing is downloaded.
2. **Given** `--dry-run`, **When** a source is imported, **Then** the Runefile text is printed,
   the report is printed, and no file is created or changed.
3. **Given** a directory that already has a Runefile, **When** the person runs `rune import`
   with no output option, **Then** nothing is written and the error names `--output` and
   `--force`, and the command exits 2.
4. **Given** an existing Runefile and `--output imported.rune`, **When** the source is
   imported, **Then** `imported.rune` is written and the existing Runefile is unchanged.
5. **Given** an existing file at the output path and `--force`, **When** the source is
   imported, **Then** the file is replaced.

---

### User Story 4 - Import phony targets from a Makefile (Priority: P2)

A developer whose project uses make as a command runner imports its Makefile. Targets declared
`.PHONY` with ordinary commands become tasks. File targets, pattern rules, automatic variables
and make functions are reported, because Rune does not rebuild by timestamp.

**Why this priority**: make is the most common command runner, and the one with the largest
part that cannot be converted. The distinction between a command target and a build rule is
the value here.

**Independent Test**: Import a Makefile with phony targets, variables, a file target and a
pattern rule. The phony targets run as under make, and the file target and the pattern rule
are reported as unsupported with the reason.

**Acceptance Scenarios**:

1. **Given** a `.PHONY` target with prerequisites that are phony targets, **When** it is
   imported, **Then** the task has those prerequisites as dependencies in the same order.
2. **Given** a recipe of two lines where the first is `cd sub`, **When** the imported task
   runs, **Then** the second line runs in the Makefile's directory, as under make.
3. **Given** variables with literal values and a recipe that uses `$(NAME)` and `$$HOME`,
   **When** the imported task runs, **Then** the command line is the one make would run.
4. **Given** a target that is not declared `.PHONY` and has a plain name, **When** it is
   imported, **Then** it is converted with status review, and the note says make skips it when
   a file of that name is up to date and Rune always runs it.
5. **Given** a pattern rule, a rule that uses `$@` or `$<`, or a variable set with
   `$(shell ...)` or `$(wildcard ...)`, **When** the Makefile is imported, **Then** each is
   reported as unsupported with its line.

---

### User Story 5 - Import a Taskfile (Priority: P2)

A developer imports a `Taskfile.yml`. Tasks with descriptions, commands, variables, parallel
dependencies, calls to other tasks, a prompt and the internal marker become tasks with the
same behavior.

**Why this priority**: Task is widely used and maps well, but several of its features have no
equivalent and must be reported.

**Independent Test**: Import a Taskfile that uses the supported keys. Each task runs as under
Task, and tasks that use unsupported keys are reported.

**Acceptance Scenarios**:

1. **Given** a task with `deps`, **When** it is imported, **Then** its dependencies run
   concurrently, as under Task.
2. **Given** a task whose commands call other tasks before and after its shell commands,
   **When** the imported task runs, **Then** the called tasks run in the same order, one at a
   time.
3. **Given** a task with `internal: true` and a task with a `prompt`, **When** they are
   imported, **Then** the first is private and the second asks for confirmation with the same
   text.
4. **Given** a task that uses `{{.CLI_ARGS}}`, **When** the imported task is run with extra
   arguments, **Then** the command receives them.
5. **Given** a task with `sources` and `generates`, **When** it is imported, **Then** it uses
   Rune's caching with those files, with status review.
6. **Given** a variable with `sh:`, an `includes` entry, a `preconditions` list or a command
   that spans several lines, **When** the Taskfile is imported, **Then** each is reported with
   its line and nothing is fetched or run.

---

### User Story 6 - Import npm scripts (Priority: P2)

A developer imports the `scripts` of a `package.json`. Each script becomes a task that runs
from the package directory with the package's local programs on the path, runs its `pre` and
`post` scripts around it, and passes extra arguments on.

**Why this priority**: a script string alone is not what `npm run` does. The parts npm adds
are what this story preserves.

**Independent Test**: Import a `package.json` with a script that has `pre` and `post` scripts
and uses a program from `node_modules/.bin`. The task runs all three in order, finds the
program, and passes extra arguments to the main script only.

**Acceptance Scenarios**:

1. **Given** scripts `prebuild`, `build` and `postbuild`, **When** the imported `build` task
   runs, **Then** the three run in that order, and `postbuild` does not run when `build` fails.
2. **Given** a script that calls a program installed in `node_modules/.bin`, **When** the
   imported task runs, **Then** the program is found.
3. **Given** a script run with extra arguments, **When** the imported task is run with the
   same arguments, **Then** the script's command receives each argument as one argument.
4. **Given** a script that reads `npm_lifecycle_event`, `npm_package_name` or
   `npm_package_version`, **When** the imported task runs, **Then** it sees the values npm
   would give.
5. **Given** a script named `test:unit`, **When** it is imported, **Then** the task is named
   `test-unit` and the report shows the mapping.
6. **Given** a script that uses another `npm_` variable, **When** it is imported, **Then** it
   is reported as unsupported.

---

### User Story 7 - Keep an unsupported task working through its original runner (Priority: P3)

A developer wants every task callable from Rune today and plans to convert the difficult ones
later. With `--wrap`, an unsupported task becomes a wrapper that runs the original runner.

**Why this priority**: it lets a team switch the entry point before the conversion is
complete, but it is optional and adds a dependency on the old runner.

**Independent Test**: Import a source with an unsupported task using `--wrap`. The report
calls the task a wrapper and names the runner, the Runefile says so in the task's description,
and the command exits 0.

**Acceptance Scenarios**:

1. **Given** an unsupported recipe and `--wrap`, **When** the justfile is imported, **Then**
   the task runs `just` with the recipe's name and passes extra arguments on.
2. **Given** a wrapper, **When** the person lists the tasks, **Then** its description says it
   is a wrapper and names the runner it needs.
3. **Given** no `--wrap`, **When** the same source is imported, **Then** no task calls the
   original runner.

---

### User Story 8 - Find the command and its limits in the documentation (Priority: P3)

A developer reads what converts for each format, what a wrapper is, and a short walk through
moving a project from just to Rune. The CLI help, the CLI reference and shell completion all
describe the command the same way.

**Why this priority**: the support matrix is the contract of the feature. It ships with the
command, after the command works.

**Independent Test**: The documentation checks pass, the help of `rune import` lists every
flag, and completion offers the command and the format names.

**Acceptance Scenarios**:

1. **Given** the documentation, **When** a developer looks up a construct of one of the four
   formats, **Then** the support matrix says converted, review or unsupported.
2. **Given** `rune import --help`, **When** it is read, **Then** every flag, the exit statuses
   and the difference from the `import` statement of a Runefile are described.
3. **Given** shell completion, **When** the person completes `rune imp`, **Then** `import` is
   offered, and `--from` completes to the four format names.

### Edge Cases

- No source file is found in the directory: the command exits 2 and names the file names it
  looked for.
- More than one source is found, for example a justfile and a `package.json`: the command exits
  2, lists them, and asks for `--from` or a path. It never picks one.
- The path's name does not tell the format and `--from` is absent: the command exits 2.
- The source has no tasks, for example a `package.json` without `scripts`: the command exits 2
  and says so.
- A source name is not a valid Rune task name (`test:unit`, `docker:build`), is a word Rune
  reserves (`import`, `set`, `mod`, `if`, `else`), or collides with another mapped name: the
  mapping is deterministic and shown in the report.
- A source name starts with `_` in a justfile: the task stays private.
- A task depends on a task the source does not define, for example one from an include that is
  not followed: the missing task is created as a task that fails with the reason.
- A command line continues over several lines with `\`: it becomes one line.
- A command contains `{{` that is not a variable reference in the source format: it is written
  so that Rune prints it unchanged.
- The output is in another directory than the source: the report says the tasks now run from
  the Runefile's directory.
- A source task has the name of a rune command, such as `help` or `serve`: it keeps its name
  with the status review, and the note says to run it with `rune -- NAME`.
- A task asks for confirmation and has dependencies: Rune asks after the dependencies ran, so
  the task is review.
- The tasks of the source depend on each other in a cycle: the source cannot be imported
  (exit 3).
- `--wrap` with an output in another directory than the source: refused, because a wrapper
  runs the original runner next to the source.
- The file system does not tell `justfile` from `Justfile`: the file counts once.
- A file-wide setting that changes how every task runs and is not converted, such as a custom
  shell: every task of that source is at most "review", and the report names the setting.
- The output directory already has a Runefile under another accepted name: the default output
  is refused, as for an existing Runefile.

## Requirements *(mandatory)*

### Functional Requirements

**The command**

- **FR-001**: Rune MUST provide the command `rune import`, which reads one source file and
  produces one Runefile. The command takes an optional source path, `--from <format>`,
  `--output <path>`, `--dry-run`, `--wrap` and `--force`.
- **FR-002**: The formats are `justfile`, `make`, `task` and `npm`. Any other `--from` value
  is a usage error that lists the four.
- **FR-003**: Without `--from`, the format MUST be taken from the source's file name. Without
  a source path, the command MUST look in the working directory for the usual file names of
  the four formats. When the path is a directory, it MUST look there.
- **FR-004**: When no source is found, when more than one is found, or when a file name does
  not tell the format, the command MUST write nothing and exit 2 with a message that names what
  it found and the option that resolves it.
- **FR-005**: The command MUST be distinct from the `import` statement of a Runefile: the help
  and the documentation of each say what the other is. No Runefile task can be named `import`,
  so the command takes no name from a task.

**Safety**

- **FR-006**: Import MUST be static. It MUST NOT run a task body, a command of a variable, a
  make function, an npm script, or the source's runner, and MUST NOT read or download an
  include.
- **FR-007**: The source file MUST NOT be changed.
- **FR-008**: The default output is the Runefile the source's directory already has, else
  `Runefile` in that directory. When the output path names an existing file, the command MUST
  write nothing and exit 2 with a message that names `--output` and `--force`. `--force`
  replaces the file at the output path. The command MUST NOT leave a directory with two
  Runefiles.
- **FR-009**: With `--dry-run` the command MUST write the Runefile text to standard output,
  write the report to standard error, and create or change no file.
- **FR-010**: The command MUST write a Runefile only when that Runefile passes Rune's own
  static analysis without errors. A Runefile that would not is a failure of the command, not
  output.

**The report**

- **FR-011**: Every task of the source MUST appear exactly once in the report with one of the
  statuses converted, review, unsupported or wrapper, its source file and line, and, for every
  status but converted, the reason. A task that the source refers to and does not define has a
  line too.
- **FR-012**: A construct outside a task that is not converted, such as an include or a
  setting, MUST be reported with its file and line.
- **FR-013**: When the Rune name of a task differs from its source name, the report MUST show
  both.
- **FR-014**: The report ends with the counts per status. The report and the Runefile MUST be
  the same for the same source and options on every run and every operating system, and tasks
  keep the order of the source.
- **FR-015**: The exit status MUST be 0 when nothing is unsupported; 1 when at least one task
  or item is unsupported (the Runefile is still produced), or when the Runefile could not be
  written; 2 for a usage error; and 3 when the source cannot be parsed as its format or its
  tasks depend on each other in a cycle (nothing is produced). A source that cannot be parsed
  MUST be reported with the file, the line, the column and the offending line.

**What is never dropped**

- **FR-016**: An unsupported task MUST stay in the Runefile under its mapped name as a task
  that fails with a message naming the source file, the line and the reason, so that a task
  depending on it cannot succeed without it.
- **FR-017**: A prerequisite, a prompt, a private or internal marker, and a failing command of
  a source task MUST each be converted or reported. None is removed without a report line.
- **FR-018**: A task that the source refers to and does not define MUST be created as a
  failing task, as in FR-016.
- **FR-019**: When a file-wide construct that changes how every task runs is not converted,
  every task of the source MUST have at most the status review, with a reference to that
  construct.
- **FR-045**: A task whose Rune name is also the name of a rune command MUST keep its name and
  have at most the status review, with a note that says how to run it.
- **FR-046**: A task that asks for confirmation and has dependencies MUST have at most the
  status review, because Rune asks after the dependencies ran and the source runners ask
  before.

**Names**

- **FR-020**: A source name that is a valid Rune task name is kept, and no other task takes
  it. In any other name, each run of characters that a Rune name cannot hold becomes one `-`;
  a name that then does not start with a letter or `_`, or is a reserved word, gets the prefix
  `task-`. When a mapped name is already taken, it gets the suffix `-2`, then `-3`, in source
  order.

**Behavior that converted tasks keep**

- **FR-021**: A converted task MUST keep the source task's name (as mapped), description,
  commands in order, parameters and defaults, and dependencies in order.
- **FR-022**: A converted task MUST run from the directory the source task ran from, when the
  Runefile is in the source's directory.
- **FR-023**: Where the source runner starts a new shell for every command line (just, make,
  Task), a line that changes the shell's state, such as `cd` or `export`, MUST NOT affect the
  later lines of the converted task.
- **FR-024**: A converted task MUST stop at the same failing command as the source task, and
  a line the source marks to ignore failure MUST still be ignored. The task's exit status is
  the failing command's status. make and Task replace that status with their own (2 and 201),
  which the documentation states.
- **FR-025**: An argument passed to a converted task MUST reach the command with the same
  boundaries as under the source runner.
- **FR-026**: Text that the source format passes to the command unchanged MUST reach the
  command unchanged, including quotes and braces.

**justfile**

- **FR-027**: The supported subset is: recipes with documentation comments; required,
  defaulted and variadic parameters; dependencies before and after the recipe, with literal
  arguments; variables whose value is built from string literals, other variables, `+` and
  `/`; `{{name}}` references to variables and parameters; the `@` and `-` line prefixes; quiet
  recipes (`@name:`); line continuation; and the attributes that mean the same in Rune (private, confirm, group, doc,
  no-cd, the operating system attributes, no-exit-message, working-directory).
- **FR-028**: Everything else in a justfile is reported: backtick commands, function calls and
  conditionals in expressions, shebang recipes, `import` and `mod`, aliases, settings, other
  attributes and positional arguments. A recipe that uses one of them is unsupported.
- **FR-029**: A difference that holds for a whole format is stated once in the documentation
  and is not a report line: how the runner prints the command before it runs, the default
  task of a bare invocation, the exit status of make and Task, that just runs each line with
  `sh -u` (an unset shell variable is an error under just and is empty under Rune), and that
  Task runs a task every time another task calls it while Rune runs each task once per
  invocation.
- **FR-047**: A recipe name defined more than once, which just allows per operating system,
  is one unsupported task.

**Makefile**

- **FR-030**: The supported subset is: targets declared `.PHONY` whose recipe is ordinary
  command lines; prerequisites that are targets of the Makefile; a description from a `##`
  comment on the rule line or the comment line above the rule; the `@` and `-` line prefixes;
  line continuation; `$$`; and variables set once with `=`, `:=` or `?=` to text made of
  literals and references to other such variables, referenced as `$(NAME)` or `${NAME}`.
- **FR-031**: A target with a plain name that is not declared `.PHONY` is converted with the
  status review, and the note says that make skips it when a file of that name is up to date
  and Rune always runs it. A prerequisite that is not a target of the Makefile is a failing
  task under FR-018: it can be a file, or something a rule of an include makes.
- **FR-032**: These are reported as unsupported with their line: file targets with a path or a
  suffix in the name, pattern and suffix rules, rules with several targets, double-colon
  rules, a target with more than one rule, automatic variables, make functions, variables
  appended with `+=` or set more than once, references to variables the Makefile does not define, `define`, conditionals, includes
  and target-specific variables.
- **FR-033**: `.ONESHELL`, `SHELL`, `.SHELLFLAGS`, `export` and the other special targets and
  directives that change every recipe are file-wide constructs under FR-019.

**Taskfile**

- **FR-034**: The supported subset is: `desc`; `cmds` and `cmd` with single-line commands;
  the command keys `silent` and `ignore_error`; `deps` without variables, run concurrently;
  calls to other tasks without variables at the start and at the end of `cmds`, run one at a
  time in order, each task called at most once; top-level `vars` with literal values and references to other such variables, referenced as
  `{{.NAME}}`;
  `{{.CLI_ARGS}}`; `internal`; one `prompt`; `silent` and `ignore_error` on the task; and
  top-level `silent`.
- **FR-035**: These convert with the status review, each with its note: `env` with literal
  values (Task keeps a value the environment already has, Rune replaces it); `dir` (Task
  creates a missing directory, Rune does not); `sources`, with or without `generates` (Rune caches by
  content of the inputs, the task body and the variables); and `summary`, `aliases` and
  `label`, which are not carried over.
- **FR-036**: Everything else is reported, and a task that uses it is unsupported: variables
  with `sh:` or with values that are not text, numbers or booleans; task-level `vars`;
  references to variables the Taskfile does not define; other template expressions;
  multi-line commands; `for`, `defer`, `platforms`, `preconditions`, `requires`, `status`,
  `method`, `run`, `watch`, `set`, `shopt`, `dotenv`, wildcard task names, and a call to
  another task between shell commands, or at the start of `cmds` together with `deps`. A task
  that uses `{{.CLI_ARGS}}` and is called by another task is review, because Task gives the
  arguments to every task. `includes` and the top-level
  `set`, `shopt`, `dotenv`, `method`, `run` and `output` are reported and fall under FR-019
  where they change every task.

**npm**

- **FR-037**: Each entry of `scripts` becomes a task, in the order of the file, whose body is
  the script text as one command.
- **FR-038**: A script named `pre<name>` runs before the task `<name>` and `post<name>` after
  it succeeds, when both names are scripts of the file.
- **FR-039**: Extra arguments given to a task MUST be added to the end of its own script,
  each as one argument, and not to its `pre` and `post` scripts.
- **FR-040**: Every task MUST have the package's `node_modules/.bin` directory first on the
  program search path, and MUST see `npm_lifecycle_event`, `npm_package_name` and
  `npm_package_version` with the values npm gives.
- **FR-041**: A script that mentions any other `npm_` variable or `INIT_CWD` is unsupported.
  A script that calls `npm`, `npx`, `yarn` or `pnpm` converts with the status review, because
  it still needs that program. A script that npm also runs by itself during install, publish
  or version converts with the status review, because Rune runs it only by name.

**Wrappers**

- **FR-042**: With `--wrap`, which needs the Runefile next to the source, an unsupported task
  that the original runner can run by name MUST become a task that runs that runner with the task's source name and passes extra
  arguments on. Its description and its report line MUST say that it is a wrapper and name the
  runner. A wrapper never counts as converted, and does not cause exit status 1.
- **FR-043**: Without `--wrap`, no produced task calls the source's runner on Rune's
  initiative.

**Documentation**

- **FR-044**: The documentation MUST include the support matrix of the four formats, the
  statuses and exit statuses, wrappers, and a short walk through moving a project from just to
  Rune. The CLI reference, the help of the command and shell completion MUST describe the same
  command and flags.

### Key Entities

- **Source**: the one file being imported and its format.
- **Source task**: a recipe, target, task or script of the source, with its name, line,
  description, parameters, dependencies and commands.
- **Report line**: one line of the report: a source location, a status, the source name, the
  Rune name when it differs, and a reason.
- **File-level item**: a construct outside a task that was not converted, such as a setting or
  an include. It has a report line of its own.
- **Name mapping**: the rule that gives every source task one Rune name.
- **Wrapper**: a task that runs the original runner for one unsupported task.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For one representative simple project per format, every task is imported with
  the status converted or review, the command exits 0, and the produced Runefile passes
  static analysis with no error.
- **SC-002**: Every supported construct has a fixture with its expected Runefile. For each
  format, a fixture project's imported tasks match the recorded behavior of the source runner
  in standard output, success or failure, working directory, argument boundaries, environment
  and dependency effects.
- **SC-003**: For every unsupported construct in the support matrix there is a fixture, and
  its report line names the file, the line and the construct.
- **SC-004**: Importing the adversarial fixtures of the four formats creates no file other
  than the requested output, starts no process and opens no network connection.
- **SC-005**: Importing the same source twice gives identical output and report, byte for
  byte.
- **SC-006**: A person with a simple justfile gets a working Runefile with one command and no
  manual edit.
- **SC-007**: In 100% of imports, every task of the source has exactly one line in the report.
- **SC-008**: The documentation checks pass with the new pages, and the help, the CLI
  reference and completion name the same flags.

## Assumptions

- The person runs the import in a project they can read. No network access is needed.
- The recorded behavior of the source runners in the fixtures comes from the documented
  behavior of just, GNU make, Task version 3 and npm. The tests do not need a source runner.
  Where one is installed, an opt-in check runs it against the records. The import itself
  never needs one.
- A converted task runs in Rune's built-in shell on every operating system. Where a source
  runner uses the system shell, a command that depends on that shell can behave differently;
  the documentation says so.
- Merging is done with the `import` statement of a Runefile, so the command needs no merge
  mode.
- A text report is enough for the first version. A machine-readable report can be added later
  without changing the text.
- The usual file names are: `justfile`, `Justfile`, `.justfile`; `GNUmakefile`, `makefile`,
  `Makefile`; `Taskfile.yml`, `Taskfile.yaml`, `taskfile.yml`, `taskfile.yaml`;
  `package.json`. A file with another name needs `--from`.
- npm on Windows runs scripts with `cmd.exe`. Imported scripts run in Rune's built-in shell,
  so a script written for `cmd.exe` is outside the supported subset.
