# Data Model: Reload Tools When the Runefile Changes

No persistent data. These are the in-memory values the feature adds.

## `mcpserver.snapshot` (unexported, immutable)

| Field | Meaning |
|---|---|
| `engine Engine` | The engine calls run against. |
| `tasks map[string]TaskInfo` | Exposed tasks by tool name. A call to a name not here is unknown. |
| `exposed, exposable int` | What `Exposure()` reports. |
| `outputLimit int` | The clamped result cap for calls on this snapshot. |

One snapshot is current at a time (`atomic.Pointer[snapshot]`). A call loads it once.

## `mcpserver.Server` additions

| Field | Meaning |
|---|---|
| `state atomic.Pointer[snapshot]` | Replaces the `engine`, `exposed`, `exposable`, `outputLimit` fields. |
| `registered map[string][]byte` | JSON of each registered tool, by tool name. Guarded by `swapMu`. |
| `swapMu sync.RWMutex` | Write: `Replace`. Read: `tools/list`. |
| `instructions atomic.Pointer[string]` | What the next `initialize` returns. |

## `mcpserver.Change`

`Added`, `Removed`, `Changed int`: tools registered new, removed, and re-registered because
their advertised form differs. All zero means no notification was caused.

## `cli` load result

`loadModule` and `newMCPAdapter` return `paths []string`: absolute, cleaned, root first, every
path the load read or probed. Returned on failure too.

## `cli.reloader`

| Field | Meaning |
|---|---|
| `opts Options`, `runefile string` | What `ServeMCP` was started with. The root path never changes (FR-007). |
| `srv *mcpserver.Server` | The server to replace. |
| `granted, allow, deny []string`, `flagLimit int` | Parsed flags, fixed. |
| `useHTTP bool` | Whether a reload reruns the `[context]` hook. |
| `good []string` | Paths of the last successful load. |
| `files map[string]bool` | Watched set (the spec's "watched graph"): `good`, plus the last failed load's paths. |
| `dirs map[string]bool` | Directories currently added to the watcher. |

**States**: `current` (last load succeeded, `files == good`) and `stale` (last load failed,
`files == good ∪ failed`). Any relevant event leads to a load; success goes to `current`,
failure to `stale`. Neither state stops the server.
