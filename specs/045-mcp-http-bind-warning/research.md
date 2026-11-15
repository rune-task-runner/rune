# Research: Warn on Non-Loopback MCP HTTP Binds

## R1. The bound address decides

**Decision**: Replace `isLoopback(addr string) bool` with `isLoopback(a net.Addr) bool`, called
with `ln.Addr()` of the listener the transport just opened:

```go
func isLoopback(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}
```

**Rationale**: The listener's address is the one concrete address the kernel bound. `net.IP.IsLoopback`
is true for `127.0.0.0/8`, `::1` and IPv4-mapped loopback, and false for the unspecified addresses
and a nil IP (FR-007, FR-008). It is exact for every spelling of `--addr` with no parsing of our
own and no second name lookup (FR-009). An address that is not TCP cannot come from this
listener; it is reported as not loopback so an unknown case warns.

The string check it replaces is wrong three ways: an empty host (`:7777`, every interface) is
loopback; any host that starts with `127.` is loopback, including a name such as
`127.example.com`; `localhost` is trusted even where the hosts file maps it elsewhere.

**Alternatives considered**: Fixing the string check (empty host, `netip.ParseAddr`, `localhost`
case-insensitive). It still guesses about host names and needs its own table of spellings.
Resolving the name before binding: a second lookup that can disagree with the bind.

## R2. The library reports through a callback

**Decision**: Add one field to `HTTPConfig`:

```go
// Warn, when set, is called once with a one-line message when the transport
// has bound an address that is not loopback: without TLS the bearer token
// then crosses the network in cleartext. It is called before any request is
// served, on the goroutine that started the transport.
Warn func(message string)
```

The message is the unexported constant `cleartextWarning`:
`serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext`.
A nil `Warn` means no warning (FR-012).

**Rationale**: `mcpserver` writes nothing to the process's output today and has no writer in
`Options` or `HTTPConfig`. A callback keeps the decision and the text in the library, where the
issue wants them tested, and leaves the destination, the `warning:` prefix and the styling to
the caller (FR-006). The `rune` command prints its other startup warnings the same way.

**Alternatives considered**: An `io.Writer` field: the library would have to write the
`warning:` prefix itself, unstyled, or the CLI would have to wrap the writer to style it.
Writing to `os.Stderr`: an embedder may own stderr. Exporting `IsLoopback` and letting the CLI
check: `ServeHTTP` itself would stay silent, and the CLI would judge the `--addr` text, not the
bound address.

## R3. Where the callback fires

**Decision**: A small method does it for both entry points, right after `Listen` succeeds:

```go
func (cfg HTTPConfig) warnCleartext(bound net.Addr) {
	if cfg.Warn != nil && !isLoopback(bound) {
		cfg.Warn(cleartextWarning)
	}
}
```

`ServeHTTP` calls it between `lc.Listen` and `srv.Serve`. `StartHTTP` calls it between
`lc.Listen` and the goroutine that serves.

**Rationale**: After `Listen` the bound address exists (R1) and a failed bind has already
returned its error, so it prints nothing (FR-002). Before `Serve` no request has been handled.
One listener per start gives one warning (SC-001); a Runefile reload replaces tools, not the
listener. `StartHTTP` is public like `ServeHTTP`, so an embedder gets the same behavior from
either. The `agent` task endpoint (`internal/cli/agentexec.go`) calls `StartHTTP` with
`127.0.0.1:0`, so it never warns (FR-013). It passes the same callback as `rune serve` (R5):
its silence then shows that it bound loopback, and a later change of its address could not
expose the token unannounced.

**Alternatives considered**: Warning before `Listen`: needs the string check of R1 and warns
for a bind that then fails.

## R4. The dead guard

**Decision**: Delete the `if !isLoopback(cfg.Addr) { if cfg.Addr == "" { ... } }` block from
`ServeHTTP` with its error text, and rewrite the function's doc comment, which says the
transport "refuses a non-localhost bind unless the address is explicit". `transport.go` no
longer imports `strings`.

**Rationale**: `cfg.Addr` is set to `127.0.0.1:0` three lines earlier, so the inner condition
is never true (FR-010). The comment describes behavior that never existed (FR-011).

## R5. The `rune` command

**Decision**: In `internal/cli/serve.go`, pass `Warn: warnTransport(opts.Stderr, opts.themeStderr())`
in the `HTTPConfig` given to `ServeHTTP`:

```go
func warnTransport(w io.Writer, theme style.Theme) func(string) {
	return func(message string) {
		fmt.Fprintf(w, "%s: %s\n", theme.Warning.Render("warning"), message)
	}
}
```

The address `ServeMCP` applies when `--addr` is empty becomes the constant `defaultHTTPAddr`
(`127.0.0.1:7777`), so a test can hold it to loopback without binding a fixed port.
`internal/cli/agentexec.go` builds its transport in `agentHTTPConfig(token)`, beside
`agentMCPOptions`, with `Warn: warnTransport(e.opts.Stderr, e.theme)` (R3). A unit test can
then hold the address to loopback.

**Rationale**: Same shape as `warnNonDestructive`, `warnNeverExposed` and `warnNoTasksExposed`
(FR-001, FR-006). The warning comes after the startup line, because the startup line is printed
before `ServeHTTP` binds. The reloader may write to the same stderr from its goroutine at that
time; each side makes one `Write` per line, which is how the reloader and the server already
share stderr.

**Alternatives considered**: Printing in `ServeMCP` before the startup line from the `--addr`
text: R1 rules out judging the text.

## R6. Help and documentation

**Decision**:

- `cmd/rune/serve.go`: the example `rune serve --http --addr :7777 --token-file ./tok` becomes
  `--addr 127.0.0.1:7777`; the `--addr` flag help names the default,
  `HTTP listen address (with --http; default 127.0.0.1:7777)`; `Long` gains a paragraph on the
  missing TLS and the warning.
- `docs/mcp.md`: the HTTP part of "Starting the server" states that the transport has no TLS,
  which binds are loopback, that any other bind prints the warning, and that `:PORT` binds every
  interface.
- `docs/cli.md`: one sentence in the `rune serve` row.

**Rationale**: FR-014. The help example showed a bind that now warns. Release notes come from
the commit subject through git-cliff, so no changelog file is edited by hand.

## R7. Testing a non-loopback bind

**Decision**:

- Non-loopback binds in tests use `0.0.0.0:0` and `:0`. Both work in the Docker harness and on
  the CI runners, and need no interface address.
- `isLoopback` is table-tested on constructed `net.Addr` values, which covers `::`, `::1`,
  IPv4-mapped loopback, `127.0.0.2`, a LAN address, a nil IP and a non-TCP address without
  needing IPv6 on the host.
- `StartHTTP` returns after the bind, so its tests assert presence and absence directly,
  including the default address.
- `ServeHTTP` blocks. A test that expects the warning waits for it. A test that expects none
  binds a port it found free on `127.0.0.1`, polls until the server answers `401` to a request
  with no token, then stops the server and asserts that nothing was reported. The CLI and
  binary tests of a quiet loopback server do the same.
- A failed bind uses an address that cannot be bound on any host (`0.0.0.0:99999`) and asserts
  an error and no warning.

**Rationale**: Asserting absence needs proof that the bind happened; an answered request is
that proof and needs no sleep. The existing binary-level HTTP test
(`TestReloadMCP_HTTPInstructions`) finds its port the same way.

## R8. Commit type

**Decision**: `fix(mcp): ...`, not breaking.

**Rationale**: Every command line that started a server still starts one with the same exit
status; stderr gains one line for non-loopback binds. `HTTPConfig` gains a field, which does not
break keyed literals, and every use in this repository is keyed.
