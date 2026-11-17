# Specification Quality Checklist: Bind Approval and Apply to the Exact Reviewed Plan

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

- The feature is a Runefile attribute pair and a protocol surface, so the spec names them:
  `[plan]`, `[applies]`, `RUNE2020`, `RUNE_PLAN_FILE`, `rune mcp`, `--allow-destructive`,
  SHA-256, stdio and Streamable HTTP. They are the product's user-facing words, as in specs 049
  and 050. How the store is laid out and wired into the engine is left to the plan.
- No clarification markers. Points resolved with recorded assumptions the maintainer may want
  to revisit:
  - Approval is given inside the apply call and never stored. The plan carries the expiry.
  - The manual workflow for a client that cannot be asked is the command line, where the
    operator is asked about the same stored plan.
  - Plan and apply tasks must have the same parameters. The arguments are the target.
  - Any change to the composed Runefile after a plan refuses the apply.
  - An apply task cannot be a dependency or a hook of another task.
  - The plan task's declared file and summary are moved out of the workspace into the store.
  - The summary is required and is written by the plan body. Rune does not parse the artifact.
  - A started attempt uses up the plan even when a dependency fails before the apply body.
  - Helm and database migration runners are documented as unsupported.
