# Specification Quality Checklist: Per-Task Output Tails and Failure Summaries for Agents

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-11-23
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

- The attribute names `[agent-output]` and `[agent-summary]`, the `[exit N]` line, the
  output schema and the structured content are named because they are the surface the issue
  asks for (it says to choose the Runefile syntax during the work and document it), as in
  specs 042, 044 and 050. They are the subject of the feature, not implementation choices.
- No clarification was asked. The issue states the contract; its open choices have defaults
  recorded in the requirements and in Assumptions:
  - Syntax: two independent attributes with named string arguments, as `[agent-allow]` and
    `[plan]` use, so the grammar gains no new form.
  - `tail` is required, `head` optional, both 1 to 10,000.
  - The summary quotes the last 5 lines of each non-empty stream and is at most 4 KiB.
  - The policy of the called task governs the whole result.
  - The output schema grows by two optional properties for every tool, as spec 044 said
    this issue would do. Results of tasks without the attributes do not change.
- Scope was kept small on purpose (KISS): no full-output retrieval, no model, no command-line
  presentation flag, no server-wide policy, no per-stream policy, no content filtering. Each
  is listed under Out of scope.
