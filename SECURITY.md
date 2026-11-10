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
- **Destructive tasks are refused by default.** The operator allows them one by one. A call
  that can reach an ungranted `[confirm]` task is refused too, before anything runs. A task
  that can reach a `[confirm]` task carries the MCP `DestructiveHint`, and only `[readonly]`
  tasks claim to be read-only.
- **Remote MCP endpoints are opt-in**, intended to be localhost-bound, and token-gated.
- **The container image runs as non-root** and contains only the static binary (no shell or
  package manager), minimizing attack surface.

## Scope

When reporting, please consider in-scope: the `rune` binary and CLI, the Runefile
parser/analyzer/executors, and the MCP server. Out of scope: vulnerabilities in third-party
task bodies you author, and issues requiring a pre-compromised host.
