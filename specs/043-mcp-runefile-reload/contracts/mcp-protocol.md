# Contract: MCP protocol

## Capabilities

Without `--no-reload`, every `initialize` result holds
`"capabilities": {"logging": {}, "tools": {"listChanged": true}}`, including when the server has
no tool. This is what a server with at least one tool already returns. With `--no-reload` the
capabilities are unchanged from the previous release.

## Notification

After a successful reload that adds, removes or changes a tool, each connected session receives
one:

```json
{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}
```

within 2 seconds of the save. No notification is sent for a failed reload, or for a reload after
which every tool's name, description, input schema and annotations are equal to before.

## `tools/list`

Returns the tools of the current loaded Runefile. A request never sees part of a reload.

## `tools/call`

- A call runs against the Runefile that was current when the call started.
- A call to a tool that a reload removed gets the same error as a call to a name no task has.

## `initialize` instructions

On HTTP, a session that starts after a successful reload receives the `[context]` output of the
reloaded Runefile (or none when it has no `[context]` task, or the failure notice when the hook
fails). Open sessions receive nothing new. On stdio the instructions are those computed at start.
