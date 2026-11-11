# Feature Specification: Go 1.27 Toolchain and `rune --self-upgrade`

**Feature Branch**: `035-go127-self-upgrade`

**Created**: 2026-11-10

**Status**: Draft

**Input**: GitHub issue [#84](https://github.com/rune-task-runner/rune/issues/84):
"Upgrade to Go 1.27 and add --self-upgrade"

## Overview

The issue asks for two things that ship together.

- **Toolchain.** Rune builds with Go 1.25 today. The module file, the CI, release and docs-site
  workflows, and both Dockerfiles select 1.25, while the installation and contribution docs mention
  both 1.24 and 1.25. The project moves to Go 1.27 everywhere, and the docs state one requirement.
- **Self-upgrade.** A person who installed Rune from a release archive or the install script has no
  command that brings it up to date. They repeat the install by hand. `rune --self-upgrade` replaces
  the running binary with the latest stable release, after proving the download is a genuine Rune
  release.

What `rune --self-upgrade` does depends on how Rune was installed:

| Installation | Outcome |
|---|---|
| Release archive or install script (standalone) | Upgraded in place |
| Homebrew | Not changed; prints the Homebrew upgrade command |
| Scoop | Not changed; prints the Scoop upgrade command |
| `go install` | Not changed; prints the `go install` command to rerun |
| Official container image | Not changed; says to pull a newer image |
| Built from a source checkout (reports a development version) | Not changed; says why |
| Origin cannot be determined | Treated as standalone |

The upgrade happens only when asked for. Running tasks, listing them, printing help and printing the
version never contact the network to look for a new release.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Upgrade a standalone install with one command (Priority: P1)

A person installed Rune from a release archive or the install script. A newer stable release exists.
They run `rune --self-upgrade`. Rune reports the installed and the target version, downloads the
release for their operating system and processor, proves it is genuine, replaces the binary, and
reports that the upgrade happened. `rune --version` then reports the new release.

**Why this priority**: This is the capability the issue asks for. Without it nothing else in the
self-upgrade half has a purpose.

**Independent Test**: Install an older standalone binary in a scratch directory, publish a newer
release fixture, run `rune --self-upgrade`, then check that `rune --version` reports the fixture's
version.

**Acceptance Scenarios**:

1. **Given** a standalone install older than the latest stable release, **When** the person runs
   `rune --self-upgrade`, **Then** the binary is replaced, the output names the old and new versions
   and says the upgrade happened, the exit status is success, and `rune --version` reports the new
   version.
2. **Given** an install at the latest stable release, **When** the person runs `rune --self-upgrade`,
   **Then** nothing is downloaded or replaced, the output says the install is already current, and
   the exit status is success.
3. **Given** an install newer than the latest stable release (for example a release candidate),
   **When** the person runs `rune --self-upgrade`, **Then** nothing is replaced and the exit status
   is success. Rune never downgrades.
4. **Given** the newest published release is a draft or a prerelease, **When** the person runs
   `rune --self-upgrade`, **Then** that release is ignored and the newest stable one is the target.
5. **Given** a directory with no Runefile, or with a Runefile that does not parse, or one that
   requires a newer Rune than is installed, **When** the person runs `rune --self-upgrade`, **Then**
   the upgrade proceeds exactly as it does anywhere else and no Runefile diagnostic is printed.
6. **Given** the binary is reached through a symbolic link, **When** the upgrade succeeds, **Then**
   the file the link points to is replaced and the link itself is unchanged.
7. **Given** a successful upgrade, **When** the person inspects the new binary, **Then** it is
   executable with the same permissions the old one had.

---

### User Story 2 - A download that cannot be proven genuine is never installed (Priority: P1)

The person trusts `rune --self-upgrade` to install only what the Rune project released. Before
anything is extracted or replaced, Rune confirms that the release's checksum list was signed by the
Rune release process, and that the downloaded archive matches that list. If either check fails, the
upgrade stops and the installed binary is untouched. There is no flag, prompt or fallback that
installs an unverified download.

**Why this priority**: A self-upgrade that can be fed a forged binary is worse than no self-upgrade.
This story is what makes story 1 safe to ship, so it carries the same priority.

**Independent Test**: Serve release fixtures that are each wrong in one way (altered archive, altered
checksum list, missing signature, signature from a different signer) and confirm that every run
fails, names the failed check, and leaves the installed binary byte-identical.

**Acceptance Scenarios**:

1. **Given** an archive whose contents differ from the published checksum, **When** the upgrade runs,
   **Then** it fails before extraction, says the archive did not match, and the installed binary is
   unchanged.
2. **Given** a checksum list that was altered after signing, or whose signature is missing, **When**
   the upgrade runs, **Then** it fails, says the release could not be verified, and the installed
   binary is unchanged.
3. **Given** a checksum list validly signed by any identity other than the Rune release process,
   **When** the upgrade runs, **Then** it fails for the same reason and the installed binary is
   unchanged.
4. **Given** an archive that holds entries besides the Rune binary, or an entry whose path points
   outside the extraction location, **When** the upgrade runs, **Then** only the expected binary is
   ever written, or the upgrade fails. No other file is created anywhere.
5. **Given** a machine with nothing installed except Rune, **When** the upgrade runs, **Then**
   verification completes without the person installing any other tool.

---

### User Story 3 - A failed or interrupted upgrade leaves a working Rune (Priority: P1)

Upgrades fail for ordinary reasons: the network drops, the person presses Ctrl-C, the binary's
directory is not writable, the disk fills, or two upgrades start at once. In every case the person
still has a Rune that runs, and it is either the complete old version or the complete new one.

**Why this priority**: A command that can break the tool it upgrades cannot be recommended to
anyone. This also carries the same priority as story 1.

**Independent Test**: Force each failure in turn against a scratch install and confirm afterwards
that `rune --version` runs and reports either the old or the new version, never an error.

**Acceptance Scenarios**:

1. **Given** the download is cut off partway, **When** the upgrade fails, **Then** the installed
   binary is unchanged and the output says the download did not complete.
2. **Given** the person cancels during download, verification or replacement, **When** Rune exits,
   **Then** the installed binary is complete and runnable, and no temporary files remain beside it.
3. **Given** the binary or its directory is not writable by the person, **When** the upgrade runs,
   **Then** it fails with a message that names the path and how to upgrade manually. Rune does not
   request elevated privileges.
4. **Given** two upgrades are started at the same moment against the same install, **When** both
   finish, **Then** the binary is one complete version, at most one of them performed the
   replacement, and the other either reports that an upgrade is already running or finds the install
   current.
5. **Given** no network, an unreachable release service, or a rate-limit response, **When** the
   upgrade runs, **Then** it ends within a bounded time with a message that names which of those
   happened and what to try next.
6. **Given** a release that is larger than any plausible Rune archive, **When** the download exceeds
   the size bound, **Then** it is abandoned and the installed binary is unchanged.
7. **Given** the upgrade is running on Windows, where a running program cannot overwrite its own
   file, **When** it succeeds, **Then** the next `rune` invocation is the new version, and when it
   fails the old one still runs.

---

### User Story 4 - Installs owned by something else are pointed at the right upgrade path (Priority: P2)

Rune that came from Homebrew, Scoop, `go install` or the container image is tracked by that tool.
Overwriting it from inside would leave the tool's records wrong. `rune --self-upgrade` recognises
these installs, changes nothing, and prints the command that upgrades Rune the way it was installed.

**Why this priority**: It prevents damage to a working setup, but it only matters once the standalone
path exists.

**Independent Test**: Place the binary in a location laid out as each tool lays it out, run
`rune --self-upgrade`, and check that the binary is unchanged and the output contains that tool's
upgrade command.

**Acceptance Scenarios**:

1. **Given** a Homebrew install, **When** the person runs `rune --self-upgrade`, **Then** nothing is
   downloaded or replaced and the output gives the Homebrew upgrade command.
2. **Given** a Scoop install, **When** the person runs `rune --self-upgrade`, **Then** nothing is
   downloaded or replaced and the output gives the Scoop upgrade command.
3. **Given** a binary built with `go install`, **When** the person runs `rune --self-upgrade`,
   **Then** nothing is replaced and the output gives the `go install` command to rerun.
4. **Given** Rune running from the official container image, **When** the person runs
   `rune --self-upgrade`, **Then** nothing is replaced and the output says to pull a newer image.
5. **Given** a binary built from a source checkout, which reports a development version, **When**
   the person runs `rune --self-upgrade`, **Then** nothing is replaced and the output says that a
   development build is not upgraded and how to install a release.
6. **Given** a symbolic link in a personal directory that points into a Homebrew or Scoop location,
   **When** the person runs `rune --self-upgrade`, **Then** the install is treated as owned by that
   package manager.
7. **Given** any of the installs above, **When** the command ends, **Then** the exit status is
   failure, because the requested upgrade did not take place.

---

### User Story 5 - The flag cannot be confused with running a task (Priority: P2)

`--self-upgrade` is a global flag on the root command. It stands alone. Combining it with a task or
with another action is a usage error that changes nothing. Existing argument rules stay as they are:
a flag written after a task name belongs to that task, and everything after `--` is passed through.

**Why this priority**: The flag must not alter how any existing command line is read, and a typo
must not start a download.

**Independent Test**: Run each combination and confirm that only the bare flag (optionally with
display options such as `--color`) reaches the network.

**Acceptance Scenarios**:

1. **Given** `rune --self-upgrade build`, **When** it runs, **Then** it fails with a usage error,
   no task runs and nothing is downloaded.
2. **Given** `rune --self-upgrade` combined with another action such as `--list` or `--version`,
   **When** it runs, **Then** it fails with a usage error that names the conflict.
3. **Given** a Runefile with a task `deploy` that accepts its own flags, **When** the person runs
   `rune deploy --self-upgrade`, **Then** the flag is handed to the task as before and no upgrade is
   attempted.
4. **Given** `rune deploy -- --self-upgrade`, **When** it runs, **Then** the text after `--` is
   passed through unchanged and no upgrade is attempted.
5. **Given** `rune --help`, **When** it runs, **Then** the flag is listed with a one-line
   description.

---

### User Story 6 - Rune builds, tests and releases on Go 1.27 (Priority: P2)

A contributor clones the repository and builds it. The module, CI, the release pipeline, the
docs-site pipeline and both container builds all use Go 1.27 at its latest stable patch, and the docs
state one source-build requirement that matches. People who install a release see no difference: the
same platforms, archives, checksums, signatures, provenance, images and package-manager entries are
published, and `rune --version` is stamped as before.

**Why this priority**: The migration is independent of the self-upgrade and has no user-visible
behaviour of its own. It can land before or after the other stories.

**Independent Test**: Run the full gate set (build, tests, race tests, lint, golden check, fuzz
smoke, docs check, release dry run) and search the scoped files for any remaining 1.24 or 1.25
reference.

**Acceptance Scenarios**:

1. **Given** the migrated repository, **When** the full gate set runs, **Then** every gate passes on
   Go 1.27.
2. **Given** the module file, the three workflows and the two Dockerfiles, **When** they are
   searched, **Then** none selects Go 1.25 or earlier.
3. **Given** the installation and contribution docs, **When** a reader looks for the Go version
   needed to build from source, **Then** they find one answer and it matches the module file.
4. **Given** a release dry run before and after the migration, **When** the published file lists are
   compared, **Then** the platforms, archive names, checksum list, signature bundle, software bills
   of materials, container images and package-manager entries are the same.
5. **Given** a dependency or a development tool that does not work with Go 1.27, **When** it is
   updated, **Then** the update is the smallest one that restores compatibility and no unrelated
   dependency changes.

---

### Edge Cases

- The release service lists a newer stable release that has no archive for the running operating
  system and processor. The upgrade fails with a message naming the platform, and nothing changes.
- Rune is running on a platform for which no release is published at all. The same failure applies,
  with instructions for building from source.
- The latest stable release exists but its checksum list or signature bundle is not published yet
  (release still in progress). This counts as "could not be verified": the upgrade fails and nothing
  changes.
- The installed version string cannot be read as a version. It is handled as a development build.
- A previous upgrade was killed and left a staged file or a stale marker behind. The next upgrade
  is not blocked forever by it and cleans it up.
- On Windows a previous successful upgrade left the old binary behind because it was still running.
  It is removed on a later run and never mistaken for the current binary.
- The binary sits on a different filesystem from the system temporary directory. Replacement is
  still all-or-nothing.
- The directory is writable but the binary is in use by other running `rune` processes. Those
  processes keep running their old version and are not disturbed.
- Output is not a terminal (piped or in CI). The same messages are printed without prompts, and the
  command never waits for input.
- The person passes `--self-upgrade` twice or with `--color`. It behaves as the bare flag.

## Requirements *(mandatory)*

### Functional Requirements

**Invocation**

- **FR-001**: Rune MUST accept `--self-upgrade` as a global flag on the root command and list it in
  `rune --help`.
- **FR-002**: `--self-upgrade` MUST be handled before Rune looks for, reads, parses or version-checks
  a Runefile, so it behaves identically inside a project, outside one, and beside a Runefile that is
  missing, invalid or requires a newer Rune.
- **FR-003**: Rune MUST reject `--self-upgrade` combined with a task name, task arguments, or any
  other flag. The rejection is a usage error that names the conflict, runs nothing and contacts
  nothing. Two flags are exempt: `--color` only changes how output looks, and `--help` prints help
  as it does in every other combination and starts no upgrade.
- **FR-004**: Existing argument rules MUST NOT change: a flag written after a task name belongs to
  that task, and arguments after `--` are passed through untouched, including the text
  `--self-upgrade`.
- **FR-005**: Running tasks, listing tasks, printing help, printing the version and every other
  existing command MUST NOT check for, announce or download a new release.

**Choosing the target**

- **FR-006**: The target MUST be the newest stable release published by the Rune project. Drafts and
  prereleases are never the target.
- **FR-007**: Installed and target versions MUST be compared as semantic versions. When the installed
  version is equal to or newer than the target, the command is a successful no-op that downloads and
  replaces nothing.
- **FR-008**: The download MUST be the release archive published for the running operating system
  and processor. When no such archive exists the command MUST fail with a message that names the
  platform.

**Proving the download**

- **FR-009**: Before extracting or replacing anything, Rune MUST confirm that the release's checksum
  list carries a valid signature made by the Rune project's release process, identified by the same
  signer identity and issuer that the project documents for manual release verification. A signature
  from any other identity MUST be rejected.
- **FR-010**: Rune MUST then confirm that the downloaded archive matches its entry in the verified
  checksum list.
- **FR-011**: When either check cannot be completed or fails, the command MUST stop with a message
  that names the failed check. There MUST be no option, prompt or fallback that installs an
  unverified download.
- **FR-012**: Verification MUST work on a machine that has only Rune installed.
- **FR-013**: Rune MUST take only the expected binary from the archive. Other entries are ignored or
  cause failure, and no file is ever written outside the staging location.

**Replacing the binary**

- **FR-014**: Rune MUST upgrade the executable that is actually running. When it was started through
  a symbolic link, the file the link resolves to is replaced and the link is left as it is.
- **FR-015**: The replacement MUST be all-or-nothing. At every moment, including after a crash or a
  cancel, the install location holds either the complete old binary or the complete new one. One
  exception is accepted on Windows, which cannot swap the file of a running program in one step: if
  the process is killed in the instant between moving the old file aside and moving the new one in,
  the complete old binary remains beside the install path under a documented name, and the
  installation guide says how to put it back.
- **FR-016**: The new binary MUST keep the executable permissions of the one it replaces.
- **FR-017**: Only one upgrade of a given install may replace the binary at a time. A second,
  simultaneous attempt MUST end without damaging the install, either by reporting that an upgrade is
  in progress or by finding the install current. Leftovers of a killed attempt MUST NOT block later
  attempts.
- **FR-018**: On any failure (download, verification, extraction, permission, cancellation,
  replacement) the installed binary MUST be the one that was there before the attempt, and every
  temporary file the attempt created MUST be removed.
- **FR-019**: Rune MUST NOT request elevated privileges. When the install location is not writable
  the command fails with the path and manual upgrade instructions.
- **FR-020**: Self-upgrade MUST work on every operating system and processor for which Rune publishes
  release archives, including Windows, where the running program's file cannot be overwritten
  directly. Any old binary left behind on Windows MUST be removed by the next `--self-upgrade` run.

**Installs owned by something else**

- **FR-021**: When the running binary is identified as a Homebrew or a Scoop install, including
  through a symbolic link into such a location, Rune MUST NOT replace it and MUST print that package
  manager's upgrade command.
- **FR-022**: When the running binary was built with `go install`, Rune MUST NOT replace it and MUST
  print the `go install` command that fetches the latest release.
- **FR-023**: When Rune runs from the official container image, it MUST NOT replace the binary and
  MUST say to pull a newer image.
- **FR-024**: When the running binary reports a development or unreadable version, Rune MUST NOT
  replace it and MUST say that development builds are not upgraded and how to install a release.
- **FR-025**: When none of FR-021 to FR-024 applies, the install MUST be treated as standalone and
  upgraded in place.

**Reporting and limits**

- **FR-026**: The command MUST report the installed version, the target version once it is known,
  and exactly one outcome: upgraded, already current, or not upgraded with the reason. A run that
  ends before the latest release is found (a refusal, a conflict in progress, an unwritable
  location, no network) reports the installed version and the reason only.
- **FR-027**: The exit status MUST be success when the binary was upgraded or was already current,
  and failure in every other case, including the refusals in FR-021 to FR-024.
- **FR-028**: Every network wait MUST be bounded, and downloads MUST be abandoned beyond a fixed size
  limit. A stalled or oversized transfer ends the command with a failure message.
- **FR-029**: The command MUST stop promptly when cancelled, subject to FR-018.
- **FR-030**: Being offline, being rate-limited by the release service, and running on an unsupported
  platform MUST each produce a distinct message that says what happened and what to do next.
- **FR-031**: The command MUST never wait for input, so it behaves the same in a terminal, in a pipe
  and in CI.

**Toolchain migration**

- **FR-032**: The module file, every Go selection in the CI, release and docs-site workflows, and the
  default Go version in the production and test Dockerfiles MUST select Go 1.27. Where a patch
  version is pinned it MUST be the latest stable 1.27 patch at the time of the change.
- **FR-033**: The installation and contribution docs MUST state one minimum Go version for building
  from source, equal to the one in the module file, with no remaining mention of 1.24 or 1.25 as a
  requirement.
- **FR-034**: Every quality gate named in the project constitution MUST pass on Go 1.27.
- **FR-035**: Dependencies and development tools MUST be changed only where Go 1.27 compatibility
  requires it.
- **FR-036**: The release output MUST be unchanged by the migration: the same operating system and
  processor pairs, archive formats and names, checksum list, signature bundle, provenance, software
  bills of materials, container images and package-manager entries, with version and commit stamped
  into the binary as before. The self-upgrade half adds one environment variable to the container
  images so Rune can tell it runs in one; nothing else about the images changes.

**Documentation**

- **FR-037**: The CLI reference and the installation guide MUST describe the flag, what is verified
  before a download is installed, the supported platforms, the outcome for each installation method
  in the Overview table, and how to recover from a failed upgrade, including the manual upgrade path.
- **FR-038**: The docs MUST state that Rune never checks for updates on its own.

### Key Entities

- **Installed binary**: The executable that is running. It has a version, a real location (after
  following links), permissions, and an origin.
- **Install origin**: How the binary got there: standalone, Homebrew, Scoop, `go install`, container
  image, development build, or undetermined. It decides whether Rune replaces the binary or prints
  guidance.
- **Stable release**: A published Rune release that is neither a draft nor a prerelease. It has a
  version, one archive per supported platform, a checksum list and a signature over that list.
- **Release archive**: The downloadable package for one operating system and processor. It contains
  the Rune binary plus licence and readme files.
- **Checksum list and signature**: The list of archive checksums for a release and the proof that
  the Rune release process produced it. Together they tie an archive to the project.
- **Upgrade outcome**: Upgraded, already current, or not upgraded with a reason. It carries the
  installed and target versions and decides the exit status.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A person with a standalone install reaches the latest stable release with one command
  and no other tool, in under one minute on a connection of 10 megabits per second or faster.
- **SC-002**: After a successful upgrade, `rune --version` reports the target release in 100% of
  runs on all six published platform pairs.
- **SC-003**: In 100% of tampering cases tested (altered archive, altered checksum list, missing
  signature, wrong signer, unsafe archive paths) the upgrade is refused and the installed binary is
  byte-identical afterwards.
- **SC-004**: In 100% of failure cases tested (cut-off download, cancellation at each stage,
  unwritable location, simultaneous upgrades) the installed binary still runs afterwards and reports
  either the old or the new version.
- **SC-005**: With no network, the command ends with an explanatory message within 60 seconds.
- **SC-006**: For each of Homebrew, Scoop, `go install`, the container image and development builds,
  the binary is unchanged and the output contains the correct next step.
- **SC-007**: Across the existing integration suite, the number of network requests made by commands
  other than `--self-upgrade` is zero.
- **SC-008**: Every command line that worked before this feature gives the same result after it;
  the existing test suite passes without changes to its expectations.
- **SC-009**: All seven quality gates pass on Go 1.27, and a search of the scoped configuration and
  docs finds zero references to Go 1.24 or 1.25 as a selected or required version.
- **SC-010**: A release dry run produces the same set of published files before and after the
  toolchain migration.

## Assumptions

- **Go 1.27 patch.** The issue records 1.27.3 as the latest stable patch when it was filed. The exact
  patch is rechecked against the official Go download feed when the work is done.
- **Minimum for source builds.** The single documented requirement becomes Go 1.27, the same as the
  module file. Building from source with an older Go is no longer supported.
- **Windows is supported, not refused.** The issue allows either. Windows is a first-class platform
  for Rune, so the upgrade completes there. Scoop installs on Windows are redirected like any other
  package-manager install.
- **`go install` builds are redirected, not replaced.** The tool that installed a binary is the one
  that upgrades it. Rerunning `go install` is one command and keeps the Go toolchain's own records
  consistent.
- **Development builds are refused with a failure exit status.** A checkout build has no release to
  compare against, and silently swapping a contributor's local build for a release would be
  surprising.
- **Undetermined origin means standalone.** A binary that matches no known package-manager layout,
  is not a `go install` or development build, and is not in the container image is upgraded in place,
  subject to the same write-permission rule as any other.
- **No downgrade and no version choice.** The flag takes no value. It targets the latest stable
  release only. Installing a specific version, a prerelease channel or an older release stays a
  manual install.
- **No confirmation prompt.** Typing the flag is the consent. The command reports what it will do
  and does it.
- **Anonymous access to the release service is enough.** The command needs no account or token. A
  rate-limit response is reported as such.
- **Exact time and size bounds are set during planning.** The spec requires that they exist and that
  the offline case ends within 60 seconds.
- **Trust anchor.** The trusted signer is the one already documented in `docs/releasing.md` under
  "Verifying a release". Releases published before signing was introduced are not valid targets, and
  the latest stable release is always signed.
- **The install script is unchanged.** Adding signature verification to `scripts/install.sh` is not
  part of this feature.
- **Out of scope**: background or scheduled update checks, update notices in normal output, new
  release channels, rollback to a previous version, upgrading other machines or other users'
  installs, and refactoring unrelated to these two changes.
- **Dependencies**: the published release layout (archive names, checksum list, signature bundle)
  stays as the release pipeline produces it today, and the constitution's Docker-only testing rule
  applies to all new tests.
