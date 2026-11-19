# Quickstart: validating `rune export`

Runnable checks that the feature works end to end. The contracts are
[rune-export.md](contracts/rune-export.md) and [export-text.md](contracts/export-text.md).

## Prerequisites

- Docker, for the test suite: `docker compose run --rm -e GOFLAGS=-buildvcs=false test ...`
- A built binary for the manual scenarios: `go build -o /tmp/rune ./cmd/rune` inside the
  container, or `rune build`.

## 1. The test suite

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/agentdocs/... ./internal/cli/... ./cmd/rune/... ./test/integration/... ./test/docs/...
```

Expected: all packages pass. The golden test of `internal/cli` covers Runefiles with
module tasks, typed, defaulted and variadic parameters, operating system attributes, private
and `[context]` tasks, `[confirm]`, `[agent-allow]`, `[plan]` and `[applies]`, hostile
descriptions and values, an empty catalog, and catalogs at and above the two skill limits.

## 2. Print the guidance

```sh
mkdir -p /tmp/export-demo && cd /tmp/export-demo
cat > Runefile <<'EOF'
# Build the app.
build:
    @echo build-ok

# Deploy the service.
[confirm("Deploy now?")]
[agent-allow(env="staging")]
deploy env:enum("staging","prod") region="eu-west-1":
    @echo deploy {{env}} {{region}} && touch ran.marker

_helper:
    @touch ran.marker
EOF
rune export agents-md
ls
```

Expected: the section is printed, from `<!-- rune:begin tasks fingerprint=sha256:` to
`<!-- rune:end tasks -->`, with entries for `build` and `deploy` and none for `_helper`. The
`deploy` entry has the usage `rune deploy <env> [region]`, the example `rune deploy staging`,
the confirmation line and the line about the value allowed for agents. `ls` shows only
`Runefile`: nothing was written and nothing ran.

## 3. Write, keep hand-written text, write again

```sh
printf '# Project notes\n\nWritten by hand.\n' > AGENTS.md
rune export agents-md --write        # AGENTS.md: updated: 2 tasks, ... bytes
head -3 AGENTS.md                    # the three hand-written lines, unchanged
rune export agents-md --write        # AGENTS.md: unchanged
rune export agents-md --check; echo "exit $?"   # AGENTS.md: current, exit 0
```

## 4. Staleness

```sh
printf '\n# Run the tests.\ntest:\n    @echo test-ok\n' >> Runefile
rune export agents-md --check; echo "exit $?"
```

Expected: `AGENTS.md: stale: the Runefile changed since the export`, the line
`regenerate with: rune export agents-md --write`, exit 1, and `AGENTS.md` unchanged.

```sh
rune export agents-md --write && rune export agents-md --check; echo "exit $?"   # exit 0
sed -i 's/Build the app\./Build it./' AGENTS.md
rune export agents-md --check; echo "exit $?"
```

Expected: `stale: the text differs from what this rune generates (...)`, exit 1.

## 5. Broken markers are refused

```sh
printf '<!-- rune:begin tasks -->\n' >> AGENTS.md
rune export agents-md --write; echo "exit $?"
```

Expected: a diagnostic for each marker line, "nothing written", exit 2, and the file as it
was.

## 6. The skill

```sh
rm AGENTS.md
rune export claude-skill --write
head -5 .claude/skills/rune-tasks/SKILL.md
rune export claude-skill --check; echo "exit $?"
```

Expected: `SKILL.md` created; its first lines are `---`, `name: rune-tasks`, a
`description:` in double quotes that names `build`, `deploy` and `test`, and `---`. The check
exits 0. A `SKILL.md` written by hand at that path makes the write exit 2.

For the large form, generate a Runefile with 150 tasks and write the skill: `SKILL.md` has
an index of 100 lines and the line `- 50 more tasks are in [tasks.md](tasks.md).`, and
`tasks.md` has 150 entries.

## 7. The examples parse

```sh
rune export agents-md | sed -n 's/^- Example: `rune \(.*\)`$/\1/p' | while IFS= read -r args; do
  sh -c "rune --dry-run $args" >/dev/null || echo "FAILED: $args"
done
```

Expected: no `FAILED` line. The automated form of this is the test that binds every example
of every fixture with the command line's own argument handling.

## 8. Nothing leaks and nothing is offered

```sh
SECRET_TOKEN=do-not-print rune export agents-md | grep -c 'do-not-print'   # 0
rune export agents-md | grep -c -- '--yes'                                   # 0
rune export agents-md | grep -c 'This text is guidance'                      # 1
```

## 9. A task named `export`

```sh
printf '\nexport:\n    @echo exported\n' >> Runefile
rune export agents-md >/dev/null     # note: "export" is also a task of this Runefile; ...
rune export nothing; echo "exit $?"  # usage error naming rune -- export, exit 2
rune -- export                       # exported
rune export agents-md | grep 'rune -- export'   # the usage of that task
```

## 10. Documentation

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...
```

Expected: the new page `docs/how-to/agent-instructions.md` passes the code block and link
checks, and the CLI reference lists `rune export`.
