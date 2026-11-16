# Data Model: A File Flag for the MCP Server

The feature stores nothing. It has one input and three things that follow from it.

## The flag's value

| State | How it arises | What the server does |
|---|---|---|
| Not given | No `-f` or `--file` on the command line | Looks for a Runefile in the starting directory and each parent, as before (FR-005) |
| Given, empty | `-f ""`, `--file=`, or a client variable that expanded to nothing | Stops with `--file needs a path`, exit code 2. Reads no Runefile (FR-016, FR-017) |
| Given, a path | `-f PATH` or `--file PATH`, after or before the command name | Resolves the path and serves that file (FR-001 to FR-004) |

Given twice, the last value is the value.

## Resolving a path

| Path | Result |
|---|---|
| Absolute, a file | Served as given |
| Relative, a file | Joined to the starting directory, then served (FR-004) |
| Names nothing | Usage error with the path in the message, exit code 2 (FR-014) |
| Names a directory | Usage error `--file points at a directory, not a Runefile`, exit code 2 (FR-015) |

## What follows from the named Runefile

| Thing | Comes from |
|---|---|
| The tools | The tasks of the named Runefile, its imports and its modules (FR-002, FR-007) |
| Where a task runs | The directory of the named Runefile (FR-006) |
| The `set dotenv` file | Found from the directory of the named Runefile (FR-007) |
| The server's instructions | The `[context]` task of the named Runefile, run in its directory (FR-008) |
| What is watched for reload | The named Runefile and the files it imports or loads (FR-009) |

The starting directory decides none of these. With the flag it is used only to read a
relative path.

## What the server's flag set holds

| Flags | Before | After |
|---|---|---|
| `--http`, `--addr`, `--token-file`, `--mcp`, `--ignore-version`, `--allow-destructive`, `--allow`, `--deny`, `--no-reload`, `--output-limit` | yes | yes |
| `--color` (inherited from the root) | yes | yes |
| `-f`, `--file` | no | yes |
| `--yes`, `--quiet`, `--list`, `--dry-run`, `--summary`, `--dump`, `--format`, `--set`, `--watch`, `--choose`, `--fmt`, `--clear-cache`, `--self-upgrade` | no | no (FR-010, FR-011) |

`--ignore-version` exists on both the root and the server already, each with its own
meaning, and is the one name the two had in common before this feature.
