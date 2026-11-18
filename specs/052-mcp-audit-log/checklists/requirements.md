# Specification Quality Checklist: Record Agent Actions in a Durable Log and Show Them with `rune log`

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

- The feature is a command and a file contract, so the spec names them: `rune log` and its
  flags, `RUNE_AUDIT_DIR`, `RUNE_AUDIT_RETENTION_DAYS`, `RUNE_INVOCATION_ID`, the outcome
  names, line-delimited JSON, stdio and Streamable HTTP. They are the product's user-facing
  words, as in specs 049 to 051. How the recorder is wired into the server and the engine is
  left to the plan.
- No clarification markers. Points resolved with recorded assumptions the maintainer may want
  to revisit:
  - The log lives in the user's state directory, not under `.rune/`, and a location inside the
    project is refused.
  - It is configured by the server's environment, not by the Runefile or a flag.
  - There is no switch that turns the log off for `rune mcp`.
  - Only tool calls are refused when the log cannot be written. Other requests are served.
  - A call with a start and no completion reads as `incomplete`, running or crashed.
  - Retention is 90 days, one file per UTC day.
  - Protocol requests are recorded and hidden from `rune log` unless `--all` is given.
  - The run of an `(agent)` task that opens an endpoint is recorded as the parent of the calls
    made through it.
  - `log` becomes a built-in command name. A task named `log` is run with `rune -- log`.
