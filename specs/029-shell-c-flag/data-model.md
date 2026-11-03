# Data Model: Honor the `-c` Flag in `set shell`

**Feature**: [spec.md](spec.md) | **Research**: [research.md](research.md)

The feature adds no stored data and no new types. It changes how one value, the
interpreter command for a body, is derived.

## Shell list

The program and its arguments used to run a task body under the interpreter
executor.

| Source | Field today | Example |
|---|---|---|
| `set shell := [...]` | `config.Settings.Shell []string` | `["bash", "-euo", "pipefail", "-c"]` |
| `[script("...")]` | `ast.Attribute.Str`, split on whitespace | `"bash -euc"` |

- Element 0 is the program. It, any wrapper arguments, and the known shell's own
  element are never changed.
- `settings.Shell` is shared by every task in the module and must not be changed
  in place.

## Known shell

The first element of a shell list whose base name (the text after the last `/` or
`\`, lowercased, with any `.exe` suffix removed) is one of `sh`, `bash`, `dash`,
`zsh`, `ksh`, `mksh`, `ash`, or `fish`. It may be element 0 or follow a wrapper
(`/usr/bin/env bash`, `busybox sh`, `nix develop -c bash`).

## Command-string flag

An element of a shell list that matches all of:

1. comes after the known shell;
2. starts with exactly one `-` (not `--`);
3. has at least one character after the `-`;
4. every character after the `-` is an ASCII letter;
5. contains the letter `c`.

## Operand

An element after a command-string flag that does not start with `-` or `+` and is
not an option value. An element is an option value when the element just before it
is a cluster (one `-` or `+`, then letters only) that contains `o` or `O`, as in
`-o pipefail`, `-euo pipefail`, or `-O extglob`. An operand means the list gives
the shell its own code, so the shell does not read the script path as its code.

## Effective command

`runtime.Selection.Command`, the list `interp.Run` executes with the temp-file path
appended.

Derivation, for the `set shell` and `[script]` branches of `runtime.Select` only:

```text
s = index of the known shell; if there is none, return the list unchanged
f = index of the first command-string flag after s; if none, return unchanged
if any element after s is a "-" cluster of letters with "s", return unchanged
if any element after f is an operand, return the list unchanged
for each element e:
    if e is after s and is a command-string flag:
        e' = e with every "c" removed
        if e' == "-": drop the element
        else:         keep e'
    else:
        keep e unchanged
```

The result is always a new slice. The input is never changed.

| Executor branch | Derivation applied |
|---|---|
| default body under `set shell` | yes |
| `[script("...")]` | yes |
| default body without `set shell` | no (`KindShell`, no command) |
| `(python)`, `(node)` and their `set` overrides | no |
| custom executor `(name)` | no |
| `(agent)` | no |

The worked examples are in [research.md](research.md#r3-which-elements-count-as-the-command-string-flag-fr-001).

## Values derived from the effective command

| Consumer | Effect |
|---|---|
| `interp.Run` (body run) | Runs `effective... <tmpfile>`. |
| `cacheSpec` (`[cache]` fingerprint) | Executor ID changes for `-c` lists, so the next run is a cache miss (research R5). |
| `Selection.Display` (banner, diagnostics) | Unchanged: `shell:<program>` or the `[script]` string. |
