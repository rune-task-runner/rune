# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities **privately** — do not open a public issue for a
suspected vulnerability.

- Preferred: open a private advisory via GitHub Security Advisories
  ("Report a vulnerability") on the repository.
- Include: a description, affected versions, reproduction steps, and impact.

You can expect an acknowledgement within a few business days and a coordinated disclosure
timeline once the report is validated. Please give us a reasonable window to release a fix
before any public disclosure.

## Supported versions

Security fixes target the latest released version. Rune follows a backward-compatibility
promise: there is no breaking "Rune 2.0", and breaking changes to Runefile semantics are
opt-in per file.

## Security model

Rune executes task bodies and can expose tasks to AI agents over MCP, so it is conservative
by default (see [docs/mcp.md](docs/mcp.md)):

- **Secrets come from the environment only.** API keys and secrets are read from the
  environment (or the agent CLI's own session) — never from a Runefile, and never included
  in any MCP tool description, schema, or log.
- **Destructive tasks are refused by default.** The operator allows them one by one, or the
  person at the client approves one call, where the client can ask. A call that can reach
  an ungranted `[confirm]` task runs nothing before that. Rune trusts the client to show
  the request and report the answer. A task
  that can reach a `[confirm]` task carries the MCP `DestructiveHint`, and only `[readonly]`
  tasks claim to be read-only.
- **A task can limit the values an agent may run it with.** `[agent-allow(env="staging")]`
  is checked for the called task and for everything the call can reach, before anything
  runs, and neither a grant nor an approval lifts it. It covers runs that start through
  Rune. It does not cover an agent's own shell.
- **A task that applies a plan runs only the plan a person approved.** `[applies("plan")]`
  runs a task with the plan that the `[plan]` task `plan` made and Rune stored, identified by
  its SHA-256 digest, only after a person approved that plan and only once, and a grant with
  `--allow-destructive` approves no plan.
- **Agent actions are recorded before they run.** Every MCP server writes the start of a
  tool call to an audit log outside the project before anything of the call runs, refuses
  the call when it cannot, and records refused and invalid calls too. Secrets are masked,
  values of secret-named parameters are never stored, and tokens, headers and task output
  never reach a record. The log is evidence on the operator's machine, readable by the
  owner only. It is not signed: a process that runs as the same user can change it. Ship
  the records elsewhere for retention you control (see
  [the audit log](docs/mcp.md#the-audit-log)).
- **Remote MCP endpoints are opt-in**, bound to loopback unless the operator names another
  address, and token-gated. The HTTP transport has no TLS: on an address that is not loopback
  the token is sent in cleartext, and the server prints a warning when it starts.
- **The container image runs as non-root** and contains only the static binary (no shell or
  package manager), minimizing attack surface.

## Scope

When reporting, please consider in-scope: the `rune` binary and CLI, the Runefile
parser/analyzer/executors, and the MCP server. Out of scope: vulnerabilities in third-party
task bodies you author, and issues requiring a pre-compromised host.
