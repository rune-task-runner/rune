# Specification Quality Checklist: Export AGENTS.md Guidance and a Claude Skill with `rune export`

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-11-19
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

- The command name, its flags, its exit statuses, the file names `AGENTS.md`, `SKILL.md` and
  `tasks.md`, and the Markdown and YAML formats are named in the spec because they are the
  user-facing surface the issue asks for, as in specs 052 to 055. They are the subject of
  the feature, not implementation choices.
- No clarification was asked. The issue states the contract. The open choices have defaults
  recorded in the requirements and in Assumptions:
  - The command is `rune export`, as the issue writes it, which takes the name `export` on
    the command line. A task of that name is run with `rune -- export`.
  - Standard output is the default. `--write` writes to the documented location, `--output`
    to another. There is no `--force`.
  - A file with no marker line gains the section at its end. Broken or repeated markers are
    refused.
  - A skill file is Rune's as a whole. A file at the target that Rune did not generate is
    never replaced.
  - The fingerprint is computed from the catalog, not from the source bytes, so that a
    change to a task body does not make a committed export stale.
  - Tasks limited to another operating system are listed with the limit stated, because the
    export must not depend on the host.
- Scope was kept small on purpose (KISS): two formats, no other instruction files, no
  machine-readable catalog, no evaluation of anything, no enforcement. Each is listed under
  Out of scope or in Assumptions.
