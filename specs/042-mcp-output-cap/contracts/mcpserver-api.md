# Contract: `mcpserver` Library Changes

**Feature**: 042-mcp-output-cap | Covers FR-026, FR-027

For programs that embed the MCP server. `Engine`, `ListEngine`, `Result`, `TaskInfo`, `ParamInfo`,
and the transports are unchanged.

## Constants

```go
const (
	DefaultOutputLimit = 64 << 10 // bytes of each stream a tool result carries
	MinOutputLimit     = 1 << 10
	MaxOutputLimit     = 1 << 30
)
```

## `Options`

```go
// OutputLimit caps the bytes of Result.Stdout and of Result.Stderr that a
// tool result carries, marker included. Zero or less means DefaultOutputLimit.
// Other values are raised to MinOutputLimit or lowered to MaxOutputLimit.
OutputLimit int
```

Every result is capped, whatever the `Engine`. A stream within the limit is passed through
unchanged. This is a behavior change for an embedder whose engine returns more than 64 KiB per
stream: set `OutputLimit` to keep more.

## `OutputBuffer`

```go
// OutputBuffer is an io.Writer that keeps the beginning and the end of what
// is written to it and never holds more than its limit in bytes. It is safe
// for concurrent use.
type OutputBuffer struct{ /* unexported */ }

// NewOutputBuffer returns a buffer with the given limit, adjusted as
// Options.OutputLimit is.
func NewOutputBuffer(limit int) *OutputBuffer

// Write keeps what fits and drops the rest. It always returns len(p), nil.
func (b *OutputBuffer) Write(p []byte) (int, error)

// String returns everything written when it is within the limit. Otherwise
// it returns the beginning, a "[truncated N bytes]" line, and the end, at
// most limit bytes in all.
func (b *OutputBuffer) String() string
```

An engine that captures task output should write into an `OutputBuffer` built with the same limit
it gives `Options.OutputLimit`, so its memory is bounded while the task runs. The server caps
`Result` either way.
