# Specification Quality Checklist: Approve a Destructive Call Inside the Agent Client

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-11-17
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

- The feature is about a protocol feature and a command surface, so the spec names them:
  MCP elicitation, form and URL modes, `rune mcp`, `rune serve`, `--allow-destructive`,
  `--deny`, `[confirm]`. They are the product's user-facing words, as in specs 031, 034
  and 041. How the request is sent and how an approval reaches the engine is left to the
  plan.
- No clarification markers. Points resolved with recorded assumptions the maintainer may
  want to revisit:
  - The request is on by default on `rune mcp` and `rune serve`, with no operator switch.
    Before this feature an ungranted `[confirm]` task could not run from an agent session
    at all. After it, a client that answers elicitation requests by itself can approve.
    The guide states this and names `--deny` as the way to rule a task out.
  - A task granted with `--allow-destructive` is not asked about. The issue describes
    that flag as a future policy, but spec 031 already shipped it.
  - One request per call covers every destructive task the call can run, each named.
  - Rune waits five minutes for an answer. The issue asks that a timeout runs nothing and
    gives no value.
  - The refusal for a client that cannot ask keeps today's sentence and adds the command
    to run and the reason, so tests that pin today's text on `rune mcp` change.
  - The protocol revision that carries elicitation as a retried tool call (2026-07-28)
    is not built, because the pinned MCP library does not negotiate it. FR-024 fixes the
    property the issue asks of that flow: no approval outlives its call.
  - Constitution Principle VII is read as still holding and is not amended here. Its
    sentence "Destructive tasks are refused on the agent surface by default" stays true
    when nobody approves. The maintainer may prefer a clarifying amendment.
