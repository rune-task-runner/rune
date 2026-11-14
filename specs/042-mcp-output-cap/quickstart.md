# Quickstart: Validating the Output Limit

**Feature**: 042-mcp-output-cap | **Contracts**: [contracts/](contracts/)

Each scenario is automated in `test/integration/output_cap_mcp_test.go` unless noted. Run them in
Docker:

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/integration/ -run 'OutputCap' -v
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./mcpserver/ ./internal/cli/ ./internal/config/
```

## Scratch Runefile

Bodies use only shell builtins, so they run the same on Linux, macOS, and Windows. `fill` doubles a
100-byte string 15 times: 3,276,800 bytes. Each flood prints `BEGIN`, the filler, and `END`, so the
first and last lines are recognizable. A flooded stream is 3,276,811 bytes.

```rune
fill := "s=0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789; i=0; while [ $i -lt 15 ]; do s=$s$s; i=$((i+1)); done"

# About 3 MB on stdout.
flood:
    @{{fill}}; printf 'BEGIN\n%s\nEND\n' "$s"

# About 3 MB on each stream.
both:
    @{{fill}}; printf 'BEGIN\n%s\nEND\n' "$s"; printf 'EBEGIN\n%s\nEEND\n' "$s" >&2

# About 3 MB, then exit 3.
fail:
    @{{fill}}; printf 'BEGIN\n%s\nEND\n' "$s"; exit 3

# 60,000 bytes: under the limit.
small:
    @s=0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789; i=0; while [ $i -lt 600 ]; do printf '%s' "$s"; i=$((i+1)); done
```

## Scenarios

| # | Start | Call | Expect |
|---|---|---|---|
| A | `rune mcp` | `flood` | result at most 65,568 bytes; starts with `BEGIN`; one `[truncated N bytes]` line; shown stdout bytes plus N is 3,276,811; the line before `[exit 0]` is `END` |
| B | `rune mcp` | `both` | result at most 131,104 bytes; two marker lines; ends `[exit 0]` |
| C | `rune mcp` | `fail` | `isError` true; ends `[exit 3]`; one marker |
| D | `rune mcp` | `small` | result is 60,000 bytes of digits, a line break, `[exit 0]`; no marker |
| E | `rune mcp --output-limit 256KiB` | `flood` | result more than 65,536 and at most 262,176 bytes |
| F | `set mcp_output_limit := "1KiB"`, `rune mcp` | `flood` | result at most 1,056 bytes |
| G | same Runefile, `rune mcp --output-limit 4KiB` | `flood` | result more than 1,056 and at most 4,128 bytes |
| H | `rune mcp --output-limit lots` (also `""`, `-1`, `0`, `10`) | none | exit 2; stderr names `--output-limit`, the value, and the forms; a `[context]` hook marker file is absent |
| I | `set mcp_output_limit := "lots"`, `rune small` | none | non-zero exit; diagnostic with `file:line:col` at the value; no task output |
| J | `set mcp_output_limit := "1KiB"`, `rune flood` from the command line | none | 3,276,811 bytes on stdout |
| K | secret `API_TOKEN` in the environment; a task that prints filler, the token, filler, sized so the token straddles the end of the head; again for the start of the tail | the task | the token and every 4-byte part of it are absent from the result |

Memory (SC-004) is checked in `mcpserver/output_test.go`: after each write of a 200 MB loop the
buffer's capacity is at most the limit.
