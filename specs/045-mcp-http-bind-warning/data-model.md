# Data Model: Warn on Non-Loopback MCP HTTP Binds

No persistent data.

## `mcpserver.HTTPConfig`

| Field | Type | Meaning |
|---|---|---|
| `Addr` | `string` | Listen address. Empty means `127.0.0.1:0`. Unchanged. |
| `Token` | `string` | Required bearer token. Unchanged. |
| `Warn` | `func(message string)` | NEW. Receives the cleartext warning. Nil means none. |

## Bound address

`ln.Addr()` of the listener opened for `Addr`. The only input to the loopback decision.

| Bound address | Loopback | Warning |
|---|---|---|
| `127.0.0.1`, any `127.x.y.z` | yes | no |
| `::1` | yes | no |
| `::ffff:127.0.0.1` | yes | no |
| `0.0.0.0`, `::` (from `:PORT`, `0.0.0.0:PORT`, `[::]:PORT`) | no | yes |
| Any other IP address | no | yes |
| An address that is not TCP, or has no IP | no | yes |

A host name in `Addr` is whatever the bind resolved it to.

## Cleartext warning

Fixed text, no parameters:

```text
serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext
```

The `rune` command prints it as `warning: <text>` on stderr, with `warning` in the warning
style of the stderr theme.

## Sequence of one HTTP start

1. Token missing: error, nothing else happens.
2. `Addr` empty: default applied.
3. Listen. On error: return it. No warning.
4. Bound address not loopback and `Warn` set: `Warn(text)`, once.
5. Serve.
