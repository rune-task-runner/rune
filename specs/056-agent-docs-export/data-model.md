# Data Model: `rune export`

All types are in `internal/agentdocs`. Nothing is stored: the model lives for one run.

## Source

What `Build` is given.

| Field | Type | Meaning |
|---|---|---|
| `Tasks` | `[]*ast.Task` | the tasks of the composed, analyzed Runefile, in file order |
| `Reach` | `func(*ast.Task) []*ast.Task` | the tasks a run of a task can reach, the task itself first (R5) |
| `Commands` | `map[string]bool` | the names Rune takes as commands (R6) |

## Catalog

| Field | Type | Meaning |
|---|---|---|
| `Entries` | `[]Entry` | one per public task, in listing order (R4) |

`Catalog.Fingerprint()` returns `sha256:<hex>` (R14).

## Entry

One public task. Every field is a fact the text states; there is no formatting in it.

| Field | Type | From | Meaning |
|---|---|---|---|
| `Name` | `string` | `Task.Name` | full name, with the module prefix |
| `Doc` | `string` | `Task.Doc` | the description, with its white space collapsed as the text shows it, so that a comment wrapped at another column is the same fact. `Returns` and a parameter's `Doc` are held the same way |
| `Separator` | `bool` | `Commands[Name]` | the usage needs `rune --` |
| `Params` | `[]Param` | `Task.Params` | in declaration order |
| `Group` | `string` | `[group]` | empty when none |
| `OS` | `[]string` | `Task.OSFilters()` | `linux`, `macos`, `windows`, `unix`, in source order, each once |
| `Returns` | `string` | `[returns]` | empty when none |
| `Confirm` | `bool` | `[confirm]` | the task asks a person |
| `Question` | `string` | `[confirm("...")]` | the author's question, empty when none |
| `Reaches` | `[]Reached` | `Reach` | tasks a run can reach that carry a limit, in reach order |
| `Plan` | `bool` | `[plan]` | the task makes a plan Rune stores |
| `Applies` | `string` | `[applies]` | the plan task's name, empty when none |
| `ReadOnly` | `bool` | `[readonly]` | |
| `Network` | `bool` | `[network]` | |

`Entry.Usage()` and `Entry.Example()` are computed from these fields (R6, R7). An entry has
no example when `Example()` returns `""`.

## Param

| Field | Type | From | Meaning |
|---|---|---|---|
| `Name` | `string` | `Param.Name` | |
| `Kind` | `ParamKind` | `Param.Kind` | `Required`, `Optional`, `OneOrMore`, `ZeroOrMore` |
| `Type` | `string` | `Constraint.KindName` | `""`, `string`, `number`, `boolean`, `path`, `raw`, `enum` |
| `Enum` | `[]string` | `Constraint.Values` | enum values in source order |
| `HasDefault` | `bool` | `Param.Default != nil` | |
| `Literal` | `bool` | default is `*ast.StringLit` | |
| `Default` | `string` | the literal's value, or the canonical text of the expression | never evaluated |
| `Doc` | `string` | `[param-doc]` | empty when none |
| `AgentValue` | `*string` | `[agent-allow]` | the one value the author allows agents, nil when not named |

Rules:

- `HasDefault` is true exactly when `Kind` is `Optional`.
- `AgentValue` is nil for a variadic parameter (the analyzer's `RUNE2019` already refuses a
  policy that names one).
- A parameter **needs a value** when `Kind` is `Required` or `OneOrMore`, or when
  `AgentValue` is set and the default is not the literal equal to it.
- The analyzer orders parameters: required ones, then optional ones, then at most one
  variadic. So an optional parameter stands before one that needs a value only when that one
  is a later optional parameter with an `AgentValue`. A one-or-more parameter cannot follow
  an optional one: `a="x" +f` reads as the default `"x" + f`.

## Reached

A task other than the entry's own that a run can reach and that carries a limit.

| Field | Type | Meaning |
|---|---|---|
| `Name` | `string` | the reached task, public or private |
| `Confirm` | `bool` | it asks a person to confirm |
| `Allow` | `[]Allowed` | its `[agent-allow]` predicates, in the attribute's order |

`Allowed` is `{Param, Value string}`. An entry's own predicates are on its `Params`.

A reached task with neither a confirmation nor a policy is not listed. A private task that a
public task reaches is named here and has no entry of its own, so a change to its limits
changes the fingerprint, and a change to anything else of it does not.

## Format

`AgentsMD` (`agents-md`) or `ClaudeSkill` (`claude-skill`). `ParseFormat(name)` returns the
format or an error that lists the two.

## File

One file of a plan.

| Field | Type | Meaning |
|---|---|---|
| `Path` | `string` | relative to the target: `""` for the one Markdown file of `agents-md`; `SKILL.md` or `tasks.md` for a skill |
| `Old` | `[]byte` | the bytes read, nil when the file did not exist |
| `New` | `[]byte` | the bytes to have, nil when the file is to be removed |
| `Existed` | `bool` | |
| `Owned` | `bool` | the old bytes held Rune's content: a section, or a generated line |
| `OldFingerprint` | `string` | the fingerprint read from the old bytes, empty when none |
| `Remove` | `bool` | a generated `tasks.md` that is no longer part of the skill |
| `Changed` | `bool` | the new bytes differ from the old ones by more than line endings |
| `Tasks` | `int` | the number of entries the file holds |
| `Size` | `int` | the bytes Rune owns in `New`: the section, or the whole skill file |

`File.Status()` is `created`, `updated`, `removed` or `unchanged`. `unchanged` means that
the owned bytes equal the new ones when CR LF is read as LF (R12).

`File.CheckState(fingerprint)` is, by the first rule that holds (R15):

| Rule | State |
|---|---|
| `Remove` | `stale: no longer part of the skill` |
| not `Owned` | `missing` |
| the status is `unchanged` | `current` |
| `OldFingerprint` is not `fingerprint` | `stale: the Runefile changed since the export` |
| otherwise | `stale: the text differs from what this rune generates (edited by hand, or written by another rune version)` |

## Refusal

Why nothing can be written, as an error.

| Field | Type | Meaning |
|---|---|---|
| `Path` | `string` | the file, as `File.Path`: empty for the Markdown file of `agents-md` |
| `Markers` | `[]Marker` | the marker lines involved, each with its line and whether it is a begin line; empty for a skill file |

Two kinds: marker lines that are not one begin line followed by one end line, and a skill
file at the target that has no generated line.

## Planning

`PlanSection(catalog, old, existed)` plans the `agents-md` export into one Markdown file and
returns one `File`. `PlanSkill(catalog, name, existing)` plans the `claude-skill` export
into a skill directory and returns its files, `SKILL.md` first. Both return a `*Refusal`
before any file is planned.

## State transitions of a target

```text
agents-md file     missing ──write──> created ──(Runefile changes)──> stale ──write──> updated
                   no section ──write──> updated (section added at the end)
                   broken markers ──write or check──> refused

skill directory    empty ──write──> SKILL.md created [+ tasks.md created above the inline limit]
                   catalog grows past the limit ──write──> SKILL.md updated, tasks.md created
                   catalog shrinks to the limit ──write──> SKILL.md updated, tasks.md removed
                   SKILL.md not Rune's ──write or check──> refused
```
