## Findings from the Rune-only agent mode research (#46)

The design note is `docs/design/rune-only-agent-mode.md`. The runs behind every "observed"
are in `specs/047-rune-only-agent-mode/observations/README.md`. Researched on 2026-11-16
with Claude Code 2.1.325 and Codex CLI 0.167.0. Cursor, VS Code and Zed were not run.

### Clients

| Client | As Rune-only | Scope | Reason |
|---|---|---|---|
| Claude Code | Supported | The project | `permissions.deny` with bare tool names removes the tools, and `permissions.allow` lets Rune's run, from the project files alone in a trusted workspace (observed: c12) |
| Codex CLI | Supported | A project the user's configuration trusts | `[features] shell_tool = false` and `[agents] enabled = false` in the project file remove the shell and subagent tools, and Rune's tools run, from the project file alone (observed: x7, y5) |
| VS Code | Partial, not tested | One custom agent | No workspace setting removes the terminal tools (source, docs). A custom agent's `tools` list is an allowlist the user can widen (source) |
| Cursor (named in #46) | Not supported | None | No way to remove the terminal tool since custom modes left in 2.1 (staff). A hook can refuse shell calls, which is not removal (docs) |
| Zed | Not supported | None in a project | Profiles and tool permissions are user settings only (source) |

### Adapter facts

**Claude Code**
- Files: `.mcp.json` (`mcpServers.rune`), `.claude/settings.json` (`permissions.deny`,
  `permissions.allow`).
- Deny by bare name: `Bash`, `PowerShell`, `Monitor`, `Workflow`, `CronCreate`,
  `RemoteTrigger` (observed: c11). These are the tools `claude --restricted` removes
  (observed: c4), without `WebFetch`, which runs nothing.
- Allow: `mcp__rune`. It covers `[confirm]` tasks too, because Claude Code ignores hints
  (recorded). To keep a question in front of them, add an `ask` rule per task named in
  `--allow-destructive`: rules are evaluated deny, then ask, then allow (docs). Not tested.
- A project's `allow` rules are ignored until the workspace is trusted; its `deny` rules
  apply at once (observed: c2). Report the outcome as pending trust.
- Merge: `deny` and `allow` are lists that every level adds to. Append, never replace. A
  tool denied at any level cannot be allowed at another (docs), so an existing deny is safe.
- `.mcp.json` documents no `cwd`. The client sets `CLAUDE_PROJECT_DIR` in the server's
  environment (docs).

**Codex CLI**
- File: `.codex/config.toml`: `[features] shell_tool = false`, `[agents] enabled = false`,
  `[mcp_servers.rune]` with `command`, `args`, and `cwd` where needed (source, docs).
- `unified_exec = false` and `multi_agent = false` do nothing (observed: x5, x4). Do not
  write them.
- Leave approval at its default. Plain and `[readonly]` tasks run without a question
  (recorded). `default_tools_approval_mode = "approve"` would also cover `[confirm]` tasks.
- The whole file is ignored in a project that is not trusted (recorded): the shell is
  there and Rune is not. Report that, and the trust step.
- Command-line options outrank the project file (source). A user's `--enable shell_tool`
  undoes the setup; `requirements.toml` is the only pin.
- Merge: TOML with comments. Keep unknown keys and other servers.

**VS Code**
- Files: `.vscode/mcp.json` (Local harness) or `.mcp.json` (Copilot harness), and
  `.github/agents/rune.agent.md` with `tools: ['rune/*']` and `agents: []`.
- Never write an empty `tools` list: in the Copilot harness it means every tool (source).
- Report "Rune-only within the custom agent `rune`", and that other agents keep the
  terminal.

### What this changes in the issue

- "disable the client's built-in shell/terminal execution paths using documented client
  controls": possible for a project in two of the three clients named. In VS Code it is
  one agent's setting.
- "Verify their effective interaction" (Codex `shell_tool` and `enabled_tools`):
  `shell_tool = false` removes `exec_command` and `write_stdin` (observed: x2). The shell
  is nested inside an `exec` tool, so a check must look there (observed: x1).
  `enabled_tools` filters Rune's own tools and was not tested.
- "Do not auto-approve destructive Rune tasks": see the allow rules above. The obvious
  setting in each client approves them.
- "ordinary sandbox approval mode is not proof": agreed, and more. Both clients run an MCP
  server outside their sandbox (docs, source), so a Rune task has the user's full access
  (#51).
- Add: register `rune mcp --no-reload` in a Rune-only setup when the agent keeps a
  file-writing tool. With reload on, an agent without a shell rewrote a task and ran it
  (observed: c7, c9, y3); with `--no-reload` it could not (observed: c8, c10, y4).
- Add: report one of three outcomes, defined in
  `specs/047-rune-only-agent-mode/contracts/setup-outcome.md`: Rune-only, Rune-only within
  a scope, Registered only.
- The `(agent)` executor's agent has a shell and no Rune tools with `["claude", "-p"]`
  (observed: a1). An `agent_cmd` with `claude --restricted` and the executor's endpoint
  fixes that (observed: a2). Document it, or generate it.

### Verification

Before a client and version is listed as Rune-only, a run started with the project files
alone has to show: Rune's tools in the client's own tool list; no command-running tool in
it; a Rune call that runs; and a subagent without a command-running tool. For Claude Code
the list is the `tools` array of the `system/init` event of
`claude -p --output-format stream-json --verbose`. For Codex the request captured as in
`observations/codex/capture.py` shows the command-running tools but no MCP tool, so Rune's
tools and the call need a `codex exec --json` run with the model.

Both installed clients have had that run, in a trusted project and with nothing on the
command line but the prompt: c12 for Claude Code 2.1.325 and y5 for Codex CLI 0.167.0.
Repeat it for each version an adapter claims.
