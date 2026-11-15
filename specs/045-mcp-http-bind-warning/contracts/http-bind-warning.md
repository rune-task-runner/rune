# Contract: the cleartext warning of the HTTP transport

## `rune serve --http`

Stderr of a start whose bound address is not loopback:

```text
rune MCP server on http://0.0.0.0:7777 (token required)
warning: serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext
```

- The warning line is printed once, after the address is bound and before any request is
  handled. It follows the startup line. Other startup warnings (`--allow-destructive`,
  `--allow`, `--deny`) keep their place before the startup line.
- `warning` carries the warning style of the stderr theme. With colors off the line is exactly
  the text above.
- Nothing is written to stdout. Exit status, the token check and the startup line are
  unchanged.

Stderr of a start whose bound address is loopback, including the default `127.0.0.1:7777`:

```text
rune MCP server on http://127.0.0.1:7777 (token required)
```

A start that fails (no token file, empty token, bind error) prints no cleartext warning.
`rune mcp` on stdio never prints it.

| `--addr` | Bound | Warning |
|---|---|---|
| not given | `127.0.0.1:7777` | no |
| `127.0.0.1:7777`, `[::1]:7777` | loopback | no |
| `localhost:7777` | what `localhost` resolves to, normally loopback | normally no |
| `:7777`, `0.0.0.0:7777`, `[::]:7777` | every interface | yes |
| `192.168.1.5:7777` | that interface | yes |
| `HOST:7777` | what `HOST` resolves to | yes unless it is loopback |

## `mcpserver` (embedders)

```go
type HTTPConfig struct {
	Addr  string
	Token string
	// Warn, when set, is called once with a one-line message when the transport
	// has bound an address that is not loopback.
	Warn func(message string)
}
```

- `ServeHTTP` and `StartHTTP` both call `Warn` at most once per call, after a successful bind
  and before any request is served, on the caller's goroutine.
- The message is
  `serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext`,
  with no prefix and no line break.
- With `Warn` nil the library reports nothing and writes nothing.
- A missing token or a failed bind returns the error it returns today and does not call `Warn`.
- `ServeHTTP` no longer contains a branch that could return
  `binding a non-localhost address requires an explicit --addr`; it never did return it.

## Help

- `rune serve --help`: the HTTP example binds `127.0.0.1:7777`; the `--addr` flag help names the
  default; the long text says that the transport has no TLS and that a bind beyond loopback
  prints the warning.
