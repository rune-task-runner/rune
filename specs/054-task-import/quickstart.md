# Quickstart: validating `rune import`

Prerequisites: Docker with the compose plugin. Tests never run on the host.

```sh
alias t='docker compose run --rm -e GOFLAGS=-buildvcs=false test'
```

## Automated checks

```sh
t go test ./internal/taskimport/...                 # readers, names, shell lines, emitter, goldens
t go test ./internal/cli/... -run Import            # source and output resolution, exit status
t go test ./cmd/rune/... -run Import                # flags, help, completion
t go test ./test/integration/... -run Import        # built binary: parity, safety, dry-run
t go test ./test/docs/...                           # the new page and the CLI reference
```

Regenerate the golden files after an intended change, then read the diff:

```sh
t go test ./internal/taskimport/... -update
```

Re-check the recorded behavior against a real runner where one is installed (the test
container has `make`):

```sh
t env RUNE_TEST_SOURCE_RUNNERS=1 go test ./test/integration/... -run ImportParity -v
```

## Manual scenarios

Build a binary inside the container and use the fixtures under
`test/integration/testdata/import/`.

```sh
t go build -o /tmp/rune ./cmd/rune
```

| # | Do | Expect |
|---|---|---|
| A | In a copy of `justfile/`, run `rune import` | `Runefile` written; one report line per recipe; exit 0; `rune analyze` reports no error; the justfile is unchanged |
| B | `rune build` and the other tasks in that copy | the output recorded for `just` in the parity test |
| C | Run `rune import` again in the same directory | exit 2; the message names `--output` and `--force`; the Runefile is unchanged |
| D | `rune import --dry-run -o x.rune` | Runefile text on standard output, report on standard error ending with `dry run: nothing written`, no `x.rune` |
| E | `rune import -o imported.rune`, then add `import "imported.rune"` to a Runefile | both sets of tasks are listed |
| F | In a copy of `unsafe/` (adversarial sources), import each file with `--dry-run` | exit 1 or 0 per file, never a `MARKER` file, sources unchanged |
| G | In a copy of `make/`, run `rune import`; then `rune NAME=value <task>` | phony targets converted, the file target and the pattern rule unsupported with lines, exit 1 |
| H | The same with `--wrap` | the unsupported targets are wrappers that call `make -f Makefile <target>`; the pattern rule stays an unsupported item, so the exit status is still 1 |
| I | In a copy of `task/`, run `rune import` and run a task with `deps` | dependencies run concurrently; the `prompt` task asks; the `internal` task is not listed |
| J | In a copy of `npm/`, run `rune import`, then `rune build "a b"` | `prebuild`, `build`, `postbuild` in order; the tool in `node_modules/.bin` is found and receives `a b` as one argument |
| K | In a directory with a justfile and a `package.json`, run `rune import` | exit 2; both names are listed |
| L | `rune import --from nope` | exit 2; the four formats are listed |
| M | Import a source with a syntax error | exit 3; `file:line:col: error:` with the line and a caret; nothing written |
| N | `rune import --help`; complete `rune imp<TAB>` and `--from <TAB>` | the flags and examples of the contract; `import`; the four formats |

Contracts: [contracts/rune-import.md](contracts/rune-import.md),
[contracts/support-matrix.md](contracts/support-matrix.md).
