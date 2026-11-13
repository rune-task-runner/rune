# Research: Cap Task Output Before It Reaches the Agent

**Feature**: 042-mcp-output-cap | **Date**: 2026-11-13 | **Spec**: [spec.md](spec.md)

The Technical Context has no open unknowns. These are the design decisions, each checked against
the code on `main` at `f016c3a`.

## R1. One buffer type, in the public `mcpserver` package

**Decision**: Add `mcpserver.OutputBuffer` in `mcpserver/output.go`. The CLI adapter uses it for
both streams and the handler uses it to cap any engine's result.

**Rationale**: Two places need the same rule. The adapter must bound memory while the task writes
(FR-015), and the handler must cap results from engines an embedder supplies (FR-027).
`internal/cli` already imports `mcpserver`, so one type there serves both with no new package
(Principle IV). An embedder writing an `Engine` gets the same bounded writer. Issue #55 can build a
buffer with a different limit per task (FR-026).

**Alternatives considered**:

- A new `internal/capture` package. Rejected: a new package against the locked layout, and
  embedders could not use it.
- Extend `cappedBuffer` in `internal/cli/failhook.go`. Rejected: it keeps a head only and the
  handler in `mcpserver` could not reach it. It stays as it is for the fix suggestion (FR-011).
- Cap only in the handler, after the run. Rejected: memory would stay unbounded (FR-015).

## R2. Storage: one region of at most `limit` bytes, head fixed, tail a ring

**Decision**: The buffer holds one byte slice that never grows past `limit`, plus a count of all
bytes written.

- While the total is at most `limit`, bytes are appended. Growth doubles the capacity but never
  past `limit`, so a small output costs a small buffer and a 1 GiB limit allocates nothing up
  front.
- On the first write that passes `limit`, the slice is compacted in place: the first `headMax`
  bytes stay, and the last `tailMax` of the first `limit` bytes are moved to sit right after them.
  That region of `tailMax` bytes becomes a ring that later writes overwrite, oldest first.
- `headMax + tailMax = limit - markerReserve`, where `markerReserve` is 40 bytes: the longest
  marker, `"\n[truncated N bytes]\n"` with a 20-digit N. `headMax` is half, rounded down; `tailMax`
  is the rest.

**Rationale**: The stream must be returned whole when it fits, so the first `limit` bytes cannot be
thrown away early. Reusing the same slice for the ring means the capture never holds more than
`limit` bytes, with no second allocation at the moment of overflow (FR-015, SC-004). A write larger
than the ring copies only its last `tailMax` bytes.

Fixing the reserve at 40 bytes makes the head and tail sizes independent of N, so the same output
gives the same cut however it was chunked. The cost is that a truncated stream is up to 21 bytes
under the limit.

**Alternatives considered**:

- Keep the first `limit` bytes and a separate tail ring. Rejected: 1.5 times the limit.
- Reserve exactly the digits of N. Rejected: N depends on the kept sizes, which then depend on N.

## R3. Rendering

**Decision**: `String()` returns the stream whole when the total is at most `limit`. Otherwise it
returns `head + "\n[truncated N bytes]\n" + tail` with `N = total - len(head) - len(tail)`.

The head is trimmed back if it ends in an incomplete UTF-8 sequence (at most 3 bytes), and the
tail skips leading continuation bytes (at most 3). The trimmed bytes are counted in N. Bytes that
were never valid UTF-8 are left alone apart from that fixed trim, so binary output is capped too.

**Rationale**: FR-004 to FR-008. The marker always has its own line, whatever the output's line
structure, because both line breaks belong to the marker. The head check needs only the head's own
last bytes, which is all the buffer still has after compaction.

`String()` does not change the buffer, so it can be called more than once.

## R4. The handler caps every result; the total is twice the limit plus 32 bytes

**Decision**: `formatResult` takes the server's limit and passes `Result.Stdout` and
`Result.Stderr` through the cap before joining them. A string within the limit is returned as it
is, with no copy. The exit line and joining line breaks are added after, as today.

**Rationale**: An embedder's `Engine` returns plain strings and may not bound them (FR-027). For
the CLI adapter the strings are already within the limit, so the cap does nothing and the result
is the adapter's own rendering. The framing is `"[exit N]"` plus at most two line breaks: at most
29 bytes for any `int`, which the spec rounds to 32 (FR-009). Taking the framing out of a stream's
budget would truncate an output that is exactly at the limit, against FR-003.

`FixSuggestion` is not touched (FR-011).

## R5. Masking stays upstream

**Decision**: In `CallLists` the two `OutputBuffer` values replace the two `bytes.Buffer` values
as the sinks under `maskOptions`. Nothing else in the masking path changes.

**Rationale**: The mask writer rewrites bytes before they reach the sink and holds back a possible
partial match until it is resolved or flushed. So the buffer only ever sees masked text, and a cut
can at worst split the `***` placeholder, never a secret (FR-013, FR-014). `flushMask()` already
runs before the buffers are read.

`OutputBuffer` locks its own mutex, so the `syncWriter` wrappers around the two capture buffers go
away. The one around the fix-suggestion writer stays.

## R6. Size syntax and range live in `internal/config`

**Decision**: `config.ParseOutputLimit(s string) (int, error)` accepts decimal digits followed by
nothing, `B`, `KiB`, or `MiB`, and requires the result to be from 1,024 to 1,073,741,824. The error
text names the accepted forms and the range and is shared by the flag and the setting:

```text
invalid size "lots": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB
```

The bounds are `config.MinOutputLimit` and `config.MaxOutputLimit`, which repeat the `mcpserver`
constants so that `internal/config` does not import the MCP server. A test in `internal/cli` holds
the two pairs equal.

**Rationale**: The setting is resolved in `internal/config` and the flag in `internal/cli`, which
imports `config`. One parser gives one syntax (FR-021, FR-022). The maximum fits a 32-bit `int`.
The analyzer and the language server load `internal/config`, so it must not depend on `mcpserver`
and the MCP SDK.

## R7. Precedence and where each value is checked

**Decision**:

- `cli.Options.MCPOutputLimit *string` is nil when the flag was not given. `ServeMCP` parses it
  first, beside the task-list flags, before the Runefile is resolved (FR-023). A given empty value
  is invalid.
- `config.Settings.MCPOutputLimit int` is 0 when the setting is absent. `ResolveSettings` adds a
  diagnostic on the value's span for an invalid one (FR-024). The setting is resolved on every run,
  so `rune build` reports it too.
- `mcpAdapter.outputLimit int` is the limit in force: the flag's value, else the setting's, else
  `mcpserver.DefaultOutputLimit`. `ServeMCP` sets it and passes the same number in
  `mcpserver.Options.OutputLimit`. The `agent` task endpoint has no flag, so it uses the setting or
  the default (spec edge case).

**Rationale**: FR-020. The adapter and the server must agree, so one number is computed once.

## R8. Library surface

**Decision**: `Options.OutputLimit int`. Zero or negative means `DefaultOutputLimit`. A positive
value below `MinOutputLimit` is raised to it and one above `MaxOutputLimit` is lowered to it.
`NewOutputBuffer(limit int)` applies the same rule.

**Rationale**: The library does not validate names in `AllowList` either; the CLI validates and
the library is lenient. A buffer below the minimum could not hold the marker.

## R9. Tests

- `mcpserver/output_test.go`: whole output; exactly at the limit; one byte over; 3 MB; marker
  arithmetic (shown plus N equals written); the same bytes in one write, byte by byte, and in
  random chunks give the same text; a multi-byte character on each cut; binary bytes; the slice
  capacity never passes the limit during a 200 MB write loop; concurrent writers under `-race`;
  limit clamping.
- `mcpserver/transport_test.go`: a capped result over HTTP.
- `mcpserver/handler_test.go`: a plain `Engine` returning 3 MB on one and on both streams; total
  bound; exit line last; fix suggestion after it; `IsError` from the exit code only; an output
  within the limit unchanged.
- `internal/config`: the parser table; the setting's value, diagnostic text, and span.
- `internal/cli`: adapter results for large stdout, stderr, both, `[parallel]` writers, a 64 MiB run, and a cancelled run; a
  secret at each cut; flag precedence over the setting; each bad flag value returns a usage error
  and runs no `[context]` hook; the agent endpoint options carry the setting.
- `test/integration/output_cap_mcp_test.go`: the real binary over stdio. This is the equivalent of
  the issue's `TestMCPE2E_Pending_OutputCapped`, which is not in this repository.
- `test/docs`: the docs harness covers the new `set` line in documented examples.

## R10. Documentation and release note

**Decision**: `docs/mcp.md` gets an "Output limit" section; `docs/cli.md` the flag;
`docs/runefile.md` and `docs/GRAMMAR.md` the setting; `cmd/rune/serve.go` the flag help and an
example; `internal/language/builtin.go` the setting entry. The commit is
`feat(mcp)!: breaking: ...` so git-cliff puts the change in the release notes (FR-028).

**Rationale**: Surface changes carry their docs. An agent that received more than 64 KiB per
stream now receives less by default, which is a visible behavior change before 1.0.
