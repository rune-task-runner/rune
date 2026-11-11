# Contract: `rune --self-upgrade`

What a person or a script can rely on. Decisions referenced as R*n* are in
[../research.md](../research.md).

## Synopsis

```text
rune --self-upgrade [--color auto|always|never]
```

Help line: `--self-upgrade   upgrade this rune to the latest stable release; runs no task`

The flag takes no value. It reads no Runefile, so it behaves the same in any directory.

## What may accompany it

| Command line | Result |
|---|---|
| `rune --self-upgrade` | Runs the upgrade |
| `rune --self-upgrade --color never` | Runs the upgrade |
| `rune --self-upgrade --self-upgrade` | Runs the upgrade once |
| `rune --self-upgrade --help` | Prints root help; exit 0; contacts nothing |
| `rune --self-upgrade <anything positional>` | Usage error; exit 2 |
| `rune --self-upgrade` with `--list`, `--version`, `--dry-run`, `--summary`, `--dump`, `--format`, `--fmt`, `--watch`, `--choose`, `--clear-cache`, `--file`, `--set`, `--yes`, `--quiet` or `--ignore-version` | Usage error; exit 2 |
| `rune deploy --self-upgrade` | Runs task `deploy` with that argument, as today |
| `rune deploy -- --self-upgrade` | Passes the text through, as today |
| `rune <subcommand> --self-upgrade` | That subcommand's own unknown-flag error, as today |

Usage error text, on stderr through the normal banner:

```text
rune: --self-upgrade cannot be combined with <what was found>
```

`<what was found>` is the first conflicting flag as typed (for example `--list`) or
`task arguments` when a positional argument is present. A usage error starts nothing and contacts
nothing.

## Output

Everything goes to stderr, like Rune's other messages. stdout stays empty. Labels use the existing
status styles and are plain text when color is off.

Upgraded:

```text
rune: installed 0.7.0
rune: latest 0.8.0
rune: downloading rune_0.8.0_linux_amd64.tar.gz
rune: verified signature and checksum
upgraded: 0.7.0 -> 0.8.0 (/usr/local/bin/rune)
```

Already current (also when the installed version is newer):

```text
rune: installed 0.8.0
rune: latest 0.8.0
current: nothing to upgrade
```

`rune: installed <version>` is the first line of every run that gets past the argument check,
including the refusals and failures below. `rune: latest <version>` follows once the release is
found.

Refused, one per origin, each exits 1 (the `installed` line comes first and is not repeated here).
Paths are printed as the system reports them, so a Windows path has backslashes; documentation under
`docs/` does not reproduce a Windows path example:

```text
rune: self-upgrade: this rune was installed with Homebrew (/opt/homebrew/Caskroom/rune/0.7.0/rune); run: brew upgrade rune
rune: self-upgrade: this rune was installed with Scoop (C:\Users\me\scoop\apps\rune\0.7.0\rune.exe); run: scoop update rune
rune: self-upgrade: this rune was built with go install; run: go install github.com/rune-task-runner/rune/cmd/rune@latest
rune: self-upgrade: this rune runs from the container image; pull a newer one: docker pull ghcr.io/rune-task-runner/rune:latest
rune: self-upgrade: this is a development build (version "dev") and is not upgraded; install a release: https://github.com/rune-task-runner/rune/releases/latest
```

Failed, each exits 1 and leaves the installed binary as it was. Any other error is reported as
`rune: self-upgrade: <error>` and also exits 1.

| Reason | Message after `rune: self-upgrade: ` |
|---|---|
| offline | `cannot reach <host>: <cause>; check the network connection and try again` |
| timeout | `<host> did not answer within <limit>; try again later` |
| rate-limited | `the release service rate limit is used up until <time>; try again then, or download from <releases URL>` |
| release-lookup | `unexpected answer from the release service: <status>` |
| unsupported-platform | `release <version> has no archive for <os>/<arch>; build from source: <installation URL>` |
| unverified | `release <version> could not be verified: <cause>; nothing was installed` |
| download-failed | `could not download <file>: <cause>; nothing was installed` |
| checksum-mismatch | `<archive> does not match the signed checksum; nothing was installed` |
| too-large | `<file> is larger than <limit>; nothing was installed` |
| bad-archive | `<archive> does not contain exactly one rune binary; nothing was installed` |
| not-writable | `cannot write to <directory>: <cause>; rune does not ask for elevated rights. Rerun with the rights needed for that directory, or download from <releases URL>` |
| in-progress | `another self-upgrade of <path> is running; try again when it finishes` |
| replace-failed | `could not replace <path>: <cause>; the installed rune is unchanged` |
| replace-failed, Windows, old file not restored | `could not replace <path>: <cause>; it is now missing. Restore it by renaming <old path> back to <path>` |

There is no option that skips verification, and no prompt.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Upgraded, or already current or newer |
| 1 | Not upgraded: refused or failed |
| 2 | Combined with another argument or flag |
| 130 | Interrupted |

## Guarantees

1. No Runefile is looked for, read, parsed or version-checked.
2. The file replaced is the one the running program was loaded from, after following symbolic
   links. The link is untouched.
3. Nothing is extracted or replaced until `checksums.txt` is proven to be signed by the release
   workflow (issuer and subject in R8) and the archive matches it.
4. Only one file is ever written from an archive, at a name Rune chooses.
5. After any exit other than 0 with `upgraded`, the installed binary is byte-identical to before.
   On Windows see the one exception in R14.
6. The replacement has the permission bits the old file had.
7. Files created beside the binary are removed before exit, except `rune.exe.old` (or `rune.exe.old.N`) on Windows,
   which the next `--self-upgrade` removes.
8. Rune never starts `sudo` or any other program.
9. The run ends within 10 minutes. Limits are in R16 and apply to every host, including the
   trust-root service.
10. It never reads stdin.

## Everything else stays offline

No other invocation of `rune` contacts the release service, the trust-root service or any download
host, and none prints a notice about newer versions.
