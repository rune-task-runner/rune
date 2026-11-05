# Specification Quality Checklist: Per-Task Opt-In for Destructive Tasks over MCP

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-11-04
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

- Validation passed on the first iteration.
- The spec names CLI flags, exit statuses, stderr lines, and the MCP protocol.
  For a command-line tool these are the user-facing surface, not implementation
  details. No source files, types, or package internals are named.
- FR-016 covers the embeddable server library because that package is a public,
  embeddable surface (constitution Principle IV). It states the behavior
  (authorization only by task name, no approve-all), not the API shape.
- Scope boundaries are explicit: #37, #38, #39, #44, and #47 are listed as out
  of scope. #38 (refusing the whole run before it starts) is the closest
  neighbor. Without it, an ungranted destructive dependency is refused only
  when the run reaches it.
- No clarification markers were needed. The issue's backlog section fixes the
  flag name, the per-task model, and the refusal message. Remaining defaults
  (unknown name fails with exit 2, non-destructive name warns) are recorded in
  Assumptions and can be revisited in `/speckit-clarify`.
