# Specification Quality Checklist: Restrict the Argument Values an Agent May Pass to a Task

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

- The feature is a Runefile attribute and a protocol surface, so the spec names them:
  `[agent-allow]`, `RUNE2019`, `rune mcp`, `rune serve`, `--allow-destructive`, `[confirm]`,
  stdio and Streamable HTTP. They are the product's user-facing words, as in specs 033, 041
  and 049. How the check is wired into the engine and the server is left to the plan.
- No clarification markers. Points resolved with recorded assumptions the maintainer may
  want to revisit:
  - One code, `RUNE2019`, covers every invalid declaration. The issue says "documented
    diagnostic codes" and does not fix a number.
  - A default or a dependency argument that the policy forbids is valid Runefile text and is
    refused at call time, because a person may still run it.
  - A call from a client that cannot be asked about a destructive task keeps the destructive
    refusal, which comes before the arguments are read. The value check follows it.
  - The `[context]` hook counts as an agent run.
  - A restricted parameter whose value can change during a run (time, random values, file
    state) makes the call refused before it starts. The issue asks for a refusal where a
    graph "cannot be safely preflighted" and does not say which graphs those are.
  - The refusal reports the first breach in run order, not every breach.
  - The embeddable server library narrows the tool. The engine refuses the call.
