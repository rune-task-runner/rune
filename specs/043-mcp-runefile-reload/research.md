# Research: Reload Tools When the Runefile Changes

## R1. One snapshot per call

**Decision**: `mcpserver.Server` keeps `{engine, tasks by name, exposed, exposable, outputLimit}`
in an immutable `snapshot` behind an `atomic.Pointer`. The handler registered for a tool name
loads the snapshot once when a call starts and takes the engine, the `TaskInfo` and the output
limit from it. A name the snapshot does not hold is answered with the SDK's unknown-tool error
text. The CLI builds a new `mcpAdapter` for every reload and never mutates one.

**Rationale**: FR-011 says a call must not see a mix of old and new definitions and a reload must
not wait for calls. With the engine and the task table in one pointer, a call that started before
the swap holds the old adapter until it returns, and no lock is held across a task run. Handlers
keyed by name, not by `TaskInfo`, mean a body-only edit needs no `AddTool`, so no notification
(FR-017).

**Alternatives considered**: A mutable adapter with an inner pointer and a `Server.Refresh()`
that reads `Tasks()` again. The handler would validate against one table and run against another
if a swap fell between them. A read-write lock around each call would make a reload wait for a
long task.

## R2. Diff by advertised form

**Decision**: `Replace` derives each exposed task's `mcp.Tool`, marshals it to JSON, and compares
it with the JSON stored for that name at the last registration. Different or new: `AddTool`
(which replaces). Missing: `RemoveTools`. Equal: nothing. `Replace` returns
`Change{Added, Removed, Changed int}`.

**Rationale**: SDK v1.6.1 `Server.AddTool` always schedules a notification, so the server must
decide itself whether the list changed (FR-016, FR-017). JSON is exactly what a client sees, and
`encoding/json` sorts map keys, so equal tools give equal bytes.

**Alternatives considered**: Comparing `TaskInfo` values. `DestructiveReach` and other fields can
differ without changing the advertised tool, which would notify for nothing.

## R3. Notification and list consistency

**Decision**: Rely on the SDK for the notification: `AddTool` and `RemoveTools` call
`changeAndNotify`, which sends one `notifications/tools/list_changed` to every session 10 ms
after the last change. `Replace` holds a write lock while it stores the snapshot and applies the
adds and removes; a receiving middleware holds the read lock for `tools/list` only.
`Options.Reloadable` makes `New` state `Tools{ListChanged: true}` and `Logging` in the
capabilities, as a filtered server does today.

**Rationale**: The SDK's debounce gives one notification per reload (SC-004). The lock keeps a
`tools/list` from landing between a remove and an add (FR-019). Tool calls do not take the lock,
so a reload never waits for a task. The SDK advertises tools only when one is registered or when
capabilities are given, so a server that starts with no tool needs them stated (FR-020). An
option, not an unconditional change, keeps `--no-reload` and existing embedders byte-identical
(`TestEmptyFilteredServerAdvertisesTools` pins the unfiltered case).

**Alternatives considered**: A new `mcp.Server` per reload. Open sessions belong to the old one
and would get no notification.

## R4. Instructions for new sessions

**Decision**: `Server` holds the instructions in an `atomic.Pointer[string]`. A receiving
middleware on the `initialize` method calls the next handler and sets `Instructions` on the
`*mcp.InitializeResult` from the pointer. `SetInstructions(text)` stores a new value.
`Options.Instructions` is the initial value and is no longer passed to the SDK.

**Rationale**: `mcp.ServerOptions.Instructions` is read from an unexported copy on every
`initialize`, and the SDK has no setter. A middleware changes what new sessions get and cannot
reach old ones (FR-026, FR-028). One path for start and reload means one behavior to test.

**Alternatives considered**: `getServer` returning a new `mcp.Server` per HTTP session. Tools and
notifications would have to be mirrored across servers.

## R5. Knowing the watched graph

**Decision**: `loadModule` wraps its source provider in a recorder and returns the cleaned,
absolute paths of the root file and of every path the provider was asked for, found or not.
`newMCPAdapter` returns that list beside the adapter, also when the load fails.

**Rationale**: `config.Compose` reads every import and module through the provider and probes
module candidates (`<name>.rune`, `<name>/Runefile`, `<name>/.runefile`) through it. So the
recorder sees exactly the files the Runefile is made of, the files a failed load reached, and the
names of missing files (FR-001 to FR-003), with no second walk of the AST that could drift from
`Compose`.

**Alternatives considered**: Walking `file.Imports` and `file.Mods` after the load. After
composition the root file no longer says which file a task came from, and a failed parse of an
import leaves no AST to walk.

## R6. Watching

**Decision**: One `fsnotify.Watcher`. The reloader watches the directory of every path in the
watched set and ignores events whose cleaned path is not in the set. After each reload it adds
directories that are new and removes ones no longer needed. After a success the set is the paths
of that load; after a failure it is the last good set plus the paths of the failed load. A
directory that does not exist is waited for in the nearest directory above it that does: the
missing directories join the watched set, so one being made starts a reload, which then watches
it. Events are debounced for 100 ms, the value `watch.go` uses. Write, Create, Rename and Remove
count; Chmod does not.

**Rationale**: Watching a file directly loses it when an editor renames a new file over it
(FR-005). A directory watch also sees a missing file being created (FR-003). The reloader is one
goroutine and a reload runs inside its loop, so events that arrive during a reload wait in the
watcher and start the next debounce (FR-006).

**Alternatives considered**: Reusing `watch()`. It reruns on any file in one directory, which is
wrong for FR-004 and misses imports in other directories.

## R7. Reload sequence and output

**Decision**: On a debounce the reloader runs, in order: (1) `newMCPAdapter`; on error print
`reload failed: <reason>; keeping the previous tools`, extend the watched set, stop. (2) Copy the
grants and the flag's output limit into the adapter. (3) Print the warnings: flag names with no
task, grants without `[confirm]`, filters naming a never-exposed task. (4) `srv.Replace(adapter,
adapter.limit())`. (5) Set the watched set. (6) On HTTP only, run `gatherContext` and
`SetInstructions`. (7) Print `reloaded Runefile: N tools added, N removed, N changed`.

Diagnostics come from `loadModule`'s existing `renderDiags` calls, so they match a start. Every
line goes to `opts.Stderr`. A `UsageError` from an unreadable root file prints its text as the
reason.

**Rationale**: The load is the only step that can fail, and it happens before anything is
replaced, so a failure changes nothing and runs nothing (FR-021 to FR-023). The hook runs after the swap, so
it cannot delay the notification (spec assumption), and before the success line, so the line means
that new sessions get the new instructions.

**Alternatives considered**: Failing the reload on an unknown flag name. That would freeze the
tools after a rename, which the spec rejects (FR-013).

## R8. `--no-reload`

**Decision**: A boolean flag on `rune serve`/`rune mcp`, stored in `Options.MCPNoReload`. When
set, `ServeMCP` passes `Reloadable: false` and starts no reloader.

**Rationale**: FR-030. It is the complete earlier behavior, including capabilities.

## R9. Goroutine ownership

**Decision**: `ServeMCP` derives a cancellable context, starts the reloader with `go`, and after
the transport returns cancels the context and waits on the reloader's done channel. If the
watcher cannot be created, the reloader prints one warning and is not started. A watcher error
during the run prints one warning (the first only) and the loop continues.

**Rationale**: Principle VIII and FR-008. `goleak` in `mcpserver` and the leak test in
`internal/cli` stay green.

## R10. Tests and docs

**Decision**:

- `mcpserver/reload_test.go`: add, remove, change, body-only (no notification), filters and
  grants after replace, removed tool answers as unknown, in-flight call keeps its engine, output
  limit follows `Replace`, `SetInstructions` for new sessions only, capabilities with
  `Reloadable` and zero tools, concurrent `tools/list` during `Replace` under `-race`.
- `internal/cli/mcpreload_test.go`: recorded paths (import, nested import, module, missing
  optional import, failed parse), reloader success and failure with a real temp directory,
  warnings for stale flag names, debounce coalescing, stdio skips the hook, HTTP reruns it.
- `test/integration/reload_mcp_test.go`: the real binary over stdio with a long-lived pipe
  client: add task then notification within 2 s and listed (stands for
  `TestMCPE2E_Pending_RunefileReload`), static error keeps tools and prints diagnostics, import
  and module changes, rename-over save, `--no-reload`; over HTTP, new-session instructions.
- Docs: `docs/mcp.md` gets a "Reloading" section, `docs/cli.md` the flag. The commit is
  `feat(mcp):` so the changelog carries it; its body states the default and the flag.

**Rationale**: Principle VI layers, and the maintainer rule that every new function has a test.
