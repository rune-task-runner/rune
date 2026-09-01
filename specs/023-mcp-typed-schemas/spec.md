# Feature Specification: Typed Parameter Schemas and Outcome Descriptions for Agents

**Feature Branch**: `023-mcp-typed-schemas`

**Created**: 2026-08-28

**Status**: Draft

**Input**: User description: "Currently, rune exposes tasks to AI agents via the Model Context Protocol (MCP). Deepen this with a rich attribute system (similar to just) that provides the LLM a strictly typed tool schema. Type-Constrained Parameters: attributes like [type: \"path\"] or [type: \"enum\", values: [\"v1\", \"v2\"]] let the MCP server tell the agent exactly what inputs are valid, preventing hallucinated malformed arguments. Outcome Descriptions: a [returns] attribute (e.g., [returns: \"JSON array of IDs\"]) gives the agent a clear success criterion so it can verify its own work against the expected output shape."

## Clarifications

### Session 2026-08-28

- Q: Where should a parameter's type constraint be written in the Runefile? → A: Inline in the parameter list — the constraint is written next to the parameter it governs (illustrative: `deploy env:enum("staging","prod") replicas:number="2":`), not in attribute lines above the task.
- Q: Should per-parameter prose descriptions (surfaced in the agent tool schema) be part of this feature's scope? → A: Yes — authors can attach a short description to each parameter, and agents see it in the tool schema alongside the type constraint.
- Q: What values should the number constraint accept — integers only, or decimals too? → A: Both, as a single `number` kind — integers and decimals are accepted (e.g., `2` and `1.5`); non-numeric text is rejected. No separate integer-only kind in v1.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Agent receives a strictly typed tool schema (Priority: P1)

A task author constrains a task's parameters in the Runefile — for example, `deploy env` accepts only `staging` or `prod`, and `build target` must be a filesystem path. When an AI agent connects over MCP, the tool definition it receives states these constraints explicitly: the enum lists its exact allowed values, the numeric parameter is typed as a number, the boolean as true/false. The agent composes a valid call on the first attempt because the schema — not trial and error — tells it what is acceptable.

**Why this priority**: This is the core value of the feature. Today every parameter is presented to agents as free-form text, so an agent can invent `env=production` when only `prod` exists, waste a round trip on the failure, and possibly run a command with a nonsense value. A constrained schema prevents the malformed call from ever being composed.

**Independent Test**: Can be fully tested by declaring a constrained parameter on a task, connecting an MCP client, and inspecting the advertised tool definition — the constraint (allowed values, value type) must appear verbatim in the tool's input schema.

**Acceptance Scenarios**:

1. **Given** a task with a parameter constrained to the values `v1` and `v2`, **When** an MCP client lists tools, **Then** the tool's input schema enumerates exactly `v1` and `v2` as the allowed values for that parameter.
2. **Given** a task with a parameter declared as a number, **When** an MCP client lists tools, **Then** the schema types that parameter as a number rather than free-form text.
3. **Given** a task with an unconstrained parameter, **When** an MCP client lists tools, **Then** the schema presents it as free-form text exactly as it does today (no behavior change).

---

### User Story 2 - Invalid arguments are rejected before anything runs (Priority: P1)

An agent (or a human on the CLI) supplies an argument that violates a declared constraint — a value outside the enum, text where a number is required. Rune rejects the invocation before executing any command, with an error that names the parameter, shows the offending value, and states what would have been valid. The agent can self-correct from the error alone; no partially-executed side effects exist to clean up.

**Why this priority**: Schema advertisement without enforcement is a suggestion, not a guarantee. Enforcement is what makes the constraint trustworthy, and it must hold on every invocation path (MCP and CLI) so a constraint means the same thing everywhere.

**Independent Test**: Invoke a constrained task with an out-of-range value via MCP and via the CLI; both must fail before execution with an error identifying the parameter, the rejected value, and the allowed values/type.

**Acceptance Scenarios**:

1. **Given** a task whose parameter allows only `staging` and `prod`, **When** an agent calls the tool with `production`, **Then** the call fails before any command executes and the error names the parameter, the value `production`, and the two allowed values.
2. **Given** a task whose parameter is a number, **When** the CLI is invoked with a non-numeric argument, **Then** Rune exits non-zero before running anything, with an error naming the parameter and the expected type.
3. **Given** a Runefile that declares a default value violating its own constraint (e.g., default `dev` for an enum of `staging`/`prod`), **When** any Rune command loads the file, **Then** static analysis reports the contradiction as a source diagnostic (file, line, column, caret span) with zero side effects.
4. **Given** a variadic parameter with a declared constraint, **When** several values are supplied and one violates the constraint, **Then** the invocation is rejected and the error identifies the offending value.

---

### User Story 3 - Agent learns the expected outcome of a task (Priority: P2)

A task author annotates a task with an outcome description — e.g., "JSON array of build artifact IDs" or "exit 0 with no output when the tree is clean". When an agent inspects the tool, the expected outcome is part of the tool definition. After invoking the task, the agent compares the actual output against the described shape and decides for itself whether the task achieved its goal or needs a retry.

**Why this priority**: Valuable for agent self-verification, but it builds on the schema surface delivered by Stories 1–2 and is advisory (documentation for the agent) rather than enforced, so it lands after enforcement.

**Independent Test**: Declare an outcome description on a task, list tools over MCP, and confirm the description is present in the tool definition; confirm tasks without one are unchanged.

**Acceptance Scenarios**:

1. **Given** a task annotated with an outcome description, **When** an MCP client lists tools, **Then** the tool definition includes that outcome description alongside the task's existing documentation.
2. **Given** a task with no outcome description, **When** an MCP client lists tools, **Then** the tool definition is identical to today's (no placeholder text is invented).
3. **Given** a task with an outcome description, **When** a human lists tasks or dumps the machine-readable project description, **Then** the outcome description is visible there too, so the annotation serves humans and agents alike.

---

### User Story 4 - Author gets immediate feedback on annotation mistakes (Priority: P3)

A task author mistypes a constraint — an unknown type name, an enum with no values, duplicate enum values, or a constraint that contradicts the default. Rune's static analysis reports each mistake with a precise source location before anything runs, in the same style as every other Runefile diagnostic.

**Why this priority**: Authoring ergonomics. The feature works without it only in the sense that broken annotations would surface confusingly later; precise diagnostics are how the project treats every other authoring error.

**Independent Test**: Write a Runefile containing each malformed annotation; every one must produce a compile-time diagnostic with file/line/column and a caret span, and no task may execute.

**Acceptance Scenarios**:

1. **Given** a parameter declaring an unknown type name, **When** the Runefile is loaded, **Then** a diagnostic identifies the unknown name and lists the supported types.
2. **Given** an enum constraint with zero values or duplicate values, **When** the Runefile is loaded, **Then** a diagnostic pinpoints the malformed value list.

---

### Edge Cases

- **Default value vs. constraint**: a default outside its own enum (or non-numeric for a number type) is a static authoring error, caught before execution (Story 2, scenario 3).
- **Enum matching**: values match exactly and case-sensitively; `Prod` is not `prod`. The rejection error shows the allowed values so near-misses are self-explanatory.
- **Path type**: constrains the value's role (a filesystem path, surfaced as such in the schema), not its existence — whether the path must exist is the task's business. No traversal or existence checks are implied.
- **Variadic parameters**: a constraint on a variadic parameter applies to every supplied value individually.
- **Empty string arguments**: an empty string is a valid string but never a valid number/boolean, and is a valid enum value only if the enum explicitly lists it.
- **Boolean forms**: exactly one canonical pair (true/false) is accepted; the schema tells the agent which, so there is no ambiguity about `yes`/`1`/`on`.
- **Number semantics**: the single `number` kind accepts integers and decimals alike (`2`, `1.5`); non-numeric text is rejected. There is no integer-only kind in v1 — tasks needing whole-number semantics enforce that themselves.
- **Outcome description length**: descriptions are author-written prose; unreasonably long text is surfaced as-is to humans but agent-facing surfaces apply the project's existing size caps and secret masking, like every other agent-facing text.
- **Private tasks**: a `[private]` task is never an MCP tool, but its constraints still bind CLI and dependency invocations — a constraint means the same thing on every path.
- **Constraint interplay with existing attributes**: type constraints and outcome descriptions compose with all existing task attributes (`[confirm]`, OS availability, `[group]`, …) without changing their semantics.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Task authors MUST be able to declare a value constraint on any task parameter, choosing from a fixed set of kinds: free-form text (the default), number, boolean, filesystem path, and enumeration (a closed, author-listed set of allowed values). The constraint is written inline in the parameter list, directly on the parameter it governs (not in attribute lines above the task), and composes with the existing default-value and variadic marker syntax.
- **FR-002**: A parameter with no declared constraint MUST behave exactly as it does today; existing Runefiles MUST load and run unchanged, byte-for-byte identical in listings, schemas, and behavior.
- **FR-003**: An enumeration constraint MUST list at least one value, and duplicate values MUST be rejected at load time.
- **FR-004**: The MCP tool definition for a task MUST express each declared constraint in the tool's input schema in machine-readable form — enumerations list their exact allowed values, numbers and booleans are typed as such, and paths are distinguishable from free-form text — so a conforming client can construct only valid calls.
- **FR-005**: Every invocation path (MCP tool call, CLI invocation, and dependency invocation with parenthesized arguments) MUST validate supplied arguments against declared constraints before any command executes; a violation stops the invocation with zero side effects.
- **FR-006**: A constraint-violation error MUST name the parameter, quote the rejected value, and state what would have been accepted (the type, or the full list of allowed values).
- **FR-007**: Static analysis MUST reject, with standard source diagnostics (file, line, column, caret span): unknown constraint kinds, malformed enumerations (per FR-003), and default values that violate their own parameter's constraint.
- **FR-008**: Constraints on variadic parameters MUST be validated against each supplied value individually.
- **FR-009**: Task authors MUST be able to declare an outcome description on a task — a short prose statement of what successful output looks like.
- **FR-010**: A declared outcome description MUST be included in the task's MCP tool definition so agents can verify results against it, and MUST be visible in human-facing task listings and the machine-readable project dump.
- **FR-011**: All new agent-facing text introduced by this feature (schema annotations, outcome descriptions) MUST pass through the same secret-masking and size-cap guarantees as every other agent-facing surface, and MUST never cause environment values or secrets to appear in a tool definition.
- **FR-012**: The canonical formatter MUST preserve and canonically format the new annotations, and the machine-readable project dump MUST report each parameter's declared constraint and each task's outcome description.
- **FR-013**: Declaring constraints MUST remain optional per parameter and per task — authors can mix constrained and unconstrained parameters freely within one task.
- **FR-014**: Task authors MUST be able to attach a short prose description to each parameter; a declared description MUST appear in the MCP tool definition for that parameter (alongside any type constraint) and in the machine-readable project dump. Like constraints, descriptions are optional per parameter, and their absence changes nothing.

### Key Entities

- **Parameter constraint**: an author-declared rule attached to one task parameter; has a kind (text, number, boolean, path, or enumeration) and, for enumerations, an ordered list of unique allowed values. Governs both what agents are told and what invocations are accepted.
- **Parameter description**: an optional author-written prose note attached to one task parameter explaining what the parameter means; advisory, surfaced in the agent tool schema and the project dump alongside any constraint.
- **Outcome description**: an author-written prose statement attached to one task describing the shape or meaning of successful output; advisory (never enforced), surfaced to agents and humans alike.
- **Tool definition**: the per-task contract advertised to agents, today consisting of name, description, and untyped parameters; this feature enriches it with typed parameter constraints and the outcome description.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of declared parameter constraints are visible in the corresponding tool definition an agent receives — every enumeration's full value list, every type declaration.
- **SC-002**: 100% of constraint-violating invocations are stopped before any command executes, on every invocation path (agent and human), with an error naming the parameter, the rejected value, and the accepted values or type.
- **SC-003**: Zero behavior change for existing projects: a Runefile written before this feature produces identical listings, identical tool definitions, and identical run behavior after it.
- **SC-004**: An agent given a task with an enumeration-constrained parameter composes a valid call on the first attempt without any prior failed invocation, in an end-to-end agent scenario.
- **SC-005**: Every authoring mistake in the new annotations (unknown kind, empty/duplicate enum, contradicting default) is reported at load time with a precise source location, before anything runs.
- **SC-006**: A task author can add a constraint or an outcome description by editing only the lines belonging to that task — no project-wide configuration is required.

## Assumptions

- **Constraint kinds**: the initial fixed set is text (default), number, boolean, filesystem path, and enumeration. This covers the malformed-argument cases the feature targets; further kinds (e.g., regex-constrained strings) are out of scope for v1.
- **Syntax placement decided, spelling refined at planning**: constraints are written inline in the parameter list, on the parameter they govern (clarified 2026-08-28; the `[type: "enum", ...]` attribute examples in the original feature description were illustrative and are not the chosen surface). The exact token spelling within that inline form is finalized during planning to fit Rune's grammar, and — per the constitution — the DSL surface change ships with updated grammar documentation and fixtures in the same change.
- **Path constraints are advisory about role, strict about nothing else**: a path constraint tells the agent the value is a filesystem path; Rune does not check existence, readability, or containment — that remains the task's responsibility.
- **Outcome descriptions are advisory**: Rune never compares actual task output against the described outcome; verification is the agent's job. Enforced output schemas are explicitly out of scope.
- **Enforcement is universal, not MCP-only**: constraints bind the CLI and dependency invocations too, so a constraint is a property of the task, not of the transport. This is treated as a requirement (FR-005), not an open question, because divergent behavior between agent and human paths would make constraints untrustworthy.
- **Backward compatibility**: all new annotations are opt-in additions; no existing Runefile construct changes meaning (per the project's backward-compatibility constraint).
- **Constitution note**: this feature grows the attribute/annotation surface of the DSL, consistent with prior additions (`[context]`, OS attributes). The planning phase must pass the constitution check for Principle III (minimal, total DSL) — the expression language itself is untouched; only declarative annotations are added.
