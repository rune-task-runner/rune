# Contract: `mcpserver` library additions

```go
// Options
// Reloadable says the server's tasks may be replaced with Replace. The server
// then states the tools capability with listChanged from the start, even
// with no tool.
Reloadable bool

// Change counts what a Replace did to the tool list.
type Change struct{ Added, Removed, Changed int }

// Replace makes engine the server's engine and its tasks the server's tools,
// under the Options given to New. outputLimit is as Options.OutputLimit.
// Calls in progress finish on the engine they started with. Connected clients
// are notified when the tool list differs. Safe for concurrent use.
func (s *Server) Replace(engine Engine, outputLimit int) Change

// SetInstructions sets the instructions that sessions started from now on
// receive. It must already be masked, as Options.Instructions.
func (s *Server) SetInstructions(text string)
```

Unchanged: `New`, `Exposure` (now reports the current snapshot), `MCP`, the transports, `Engine`,
`ListEngine`, `TaskInfo`, `Result`. A server that never calls `Replace` or `SetInstructions` and
leaves `Reloadable` false behaves as before.

`Replace` applies `AllowList`, `DenyList` and `AllowDestructive` from the original `Options` to
the new engine's tasks. Watching files is not part of the library.
