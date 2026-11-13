# Contract: `--output-limit` and `set mcp_output_limit`

**Feature**: 042-mcp-output-cap | Covers FR-018 to FR-025

## Size

```text
size := DIGITS [ "B" | "KiB" | "MiB" ]
```

- `DIGITS` is one or more of `0` to `9`. No sign, no fraction, no spaces. Leading zeros are
  allowed: `064KiB` is `64KiB`.
- No unit and `B` mean bytes. `KiB` is 1,024 bytes. `MiB` is 1,048,576 bytes.
- The size must be from 1,024 to 1,073,741,824 bytes.
- Units are spelled exactly. `kib`, `KB`, `K`, `MB`, and `GiB` are rejected.

| Text | Bytes |
|---|---|
| `65536`, `65536B`, `64KiB` | 65,536 |
| `256KiB` | 262,144 |
| `1MiB`, `1024KiB` | 1,048,576 |
| `1024MiB` | 1,073,741,824 |

Rejected: `""`, `lots`, `-1`, `0`, `10`, `1023`, `1.5MiB`, `64 KiB`, `64kib`, `64KB`, `1GiB`,
`1025MiB`, `99999999999999999999`.

The reason text is the same in both places:

```text
invalid size "lots": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB
```

## Flag

```console
$ rune mcp --output-limit 256KiB
$ rune serve --http --token-file tok --output-limit 1MiB
```

- Applies to that server, on stdio and HTTP.
- Given more than once, the last value is used.
- An invalid value is a usage error: exit status 2, before the Runefile is read, so no Runefile
  code runs. The message, after the usual error prefix, is:

```text
--output-limit: invalid size "lots": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB
```

- Help line: `--output-limit string   cap each of stdout and stderr in a tool result (default 64KiB; such as 256KiB or 1MiB)`

## Setting

```rune
set mcp_output_limit := "256KiB"
```

- Applies to every agent-facing endpoint of the Runefile: `rune mcp`, `rune serve`, and the
  endpoint an `agent` task opens.
- Has no effect on output of tasks run from the command line.
- An invalid value is a diagnostic whose span is the value, reported when settings are resolved,
  before any task runs. It is rendered like every Runefile diagnostic, with `file:line:col` and a
  caret under the value. Its message is:

```text
invalid mcp_output_limit: invalid size "lots": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB
```

- Given more than once in one file, it is a `duplicate setting` error from the analyzer, as for
  other settings.

## Precedence

`--output-limit`, then `set mcp_output_limit`, then 64 KiB.
