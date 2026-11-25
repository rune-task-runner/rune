# Data Model: Generate GitHub Actions Workflows from the Runefile

## Root

| Field | Meaning |
|---|---|
| Task | the task named on the command line |
| Args | its positional arguments as typed |

## Node

One task with bound arguments on one target OS. The unit of run-once and of jobs.

| Field | Meaning |
|---|---|
| Task | `*ast.Task` |
| Args | bound `eval.Args`, as the scheduler binds them |
| Argv | the words that bind the same Args on a command line: each parameter in order, variadic elements as separate words |
| Key | `name(args.Key())`, the scheduler's memo key (namespace is always empty) |
| Needs | the nodes whose jobs must finish before this one starts (research R2), reduced |
| Done | this node plus its post-hooks' done sets: what a dependent waits for |
| Index | position in generation order (topological) |
| Outputs | true when the task's `[cache]` declares outputs |
| Cached | true when the task has `[cache]` |

Rules: created once per Key per OS; `Needs` only refers to nodes with a lower Index.

## Graph

The target `OS`, its nodes in Index order, and the list of roots. Built by `ci.Build` from
roots, a resolver (the CLI engine with the target OS: `BindRoot`, `ResolveDep`,
`Available`) and the Runefile's tasks and assignments.
Refusals (research R5) are returned as diagnostics with spans.

## Job

| Field | Meaning |
|---|---|
| ID | unique GitHub job ID (research R11) |
| Label | `TASK ARG...` with shell-word arguments, plus ` (OS)` with several OSes |
| RunsOn | `ubuntu-latest` or `macos-latest` |
| Needs | job IDs of the node's reduced needs |
| Downloads | job IDs of all transitive needs whose node has outputs, in Index order |
| Steps | per contracts/workflow.md |

## Workflow options

| Field | Source |
|---|---|
| OSes | `--os`, default `[linux]`, in the order given, duplicates removed |
| Version | `--rune-version` or this rune's release version, without a leading `v` |
| Branch | `--branch`, default `main` |
| WorkDir | Runefile directory relative to the repository root, `.` when equal |
| Command | the regeneration line of the header (research R12) |

## Bundle

A gzip-compressed tar at `DIR/bundle.tar`.

| Entry | Content |
|---|---|
| directories | relative, clean, inside the project |
| regular files | relative, clean, inside the project, mode bits kept |

Holds the files matched by the task's evaluated `[cache]` outputs and the task's cache
record `.rune/cache/<sanitized name>.json`. Links, devices, absolute paths and `..` are
refused on unpack.

## Cache key

`rune-<slug>-<hex>`; `<hex>` = SHA-256 of the lines `fingerprint`, `node`, `rune`, `goos`,
`goarch`, `image`, each `name\x00value\n`. At most 512 characters (GitHub's limit).

## Owned workflow file

| State | When |
|---|---|
| missing | no file at the target |
| not generated | the first line is not the marker |
| current | bytes equal the plan |
| stale | marker present, bytes differ |
