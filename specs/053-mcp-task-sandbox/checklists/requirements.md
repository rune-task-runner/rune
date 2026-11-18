# Specification Quality Checklist: Confine the Tasks an Agent Runs with an Operating-System Sandbox

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-11-18
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

- The feature is a security boundary with an operator surface, so the spec names that surface:
  `--sandbox`, `RUNE_SANDBOX`, the modes `enforce` and `off`, `TMPDIR`, `XDG_CACHE_HOME`, the
  protected paths and the hidden locations. It names bubblewrap as the Linux backend because the
  issue asks for a published support table. They are the product's user-facing words, as in
  specs 049 to 052. How a body is moved out of the server process and how the backend is
  invoked is left to the plan.
- No clarification markers. Points resolved with recorded assumptions the maintainer may want
  to revisit:
  - The workspace is the Runefile's directory, not the repository root.
  - macOS and Windows are unsupported: calls there are refused unless the server runs with
    `--sandbox off`. This is a breaking change for those systems.
  - The server starts when the sandbox is unavailable and refuses each call, unlike the audit
    log, which stops the start.
  - There is no automatic mode. The mode comes from `--sandbox` or `RUNE_SANDBOX`.
  - Reading is confined only by a fixed list of hidden locations and credential stores.
  - `.git` and `.rune` are read-only for a confined task.
  - A `[network]` task gets the host's whole network, loopback and abstract sockets included.
  - A pre-existing hard link is detected only for the Runefile, the files it loads and the
    dotenv file. This is narrower than the issue's wording.
  - A process a body leaves in the background ends with the body.
  - Landlock is evaluated in the research and not used in this release.
