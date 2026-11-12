# Feature Specification: Build Rune's Container Image with ko

**Feature Branch**: `036-ko-container-image`

**Created**: 2026-11-11

**Status**: Draft

**Input**: GitHub issue [#73](https://github.com/rune-task-runner/rune/issues/73):
"Use ko to build Rune's Go container image"

## Overview

Rune publishes a small container image at `ghcr.io/rune-task-runner/rune`: one static binary on a
minimal non-root base. Today that image is produced two ways. A contributor builds it locally from
a Dockerfile that compiles Rune, and a release packages the already-built release binary through a
second Dockerfile. Both need a running Docker daemon, and the release also needs emulation and a
multi-platform builder set up first.

This feature moves both paths to [ko](https://github.com/ko-build/ko), a builder made for Go
programs that assembles and publishes an image without a Docker daemon. The image people pull must
not change in any way they can observe: same name, same tags, same way to start Rune, same working
directory, same user, same labels, same signatures and provenance. Everything else a release
produces (archives, checksums, signatures, package manager entries, the editor extension) stays as
it is.

What changes for each group:

| Who | Today | After |
|---|---|---|
| Person pulling the image | `docker run ... ghcr.io/rune-task-runner/rune` | The same commands, the same behavior |
| Contributor building locally | Needs a Docker daemon to build | Builds the image with Go and ko; a daemon is needed only to run it |
| Maintainer cutting a release | One release run builds and publishes everything | The same single run; the image step no longer needs a daemon or emulation |
| Person verifying a release | Verifies image signature and provenance by digest | The same checks, plus a documented way to fetch the image's bill of materials |

Out of scope: running user tasks inside containers (issue #65), and the Docker-based test harness,
which stays.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The published image behaves exactly as before (Priority: P1)

A person who uses Rune from its container image keeps using the commands in the documentation.
They mount a project at `/work`, pass Rune arguments after the image name, and get the same output
and exit codes as before the change. Scripts and CI jobs that call the image need no edits.

**Why this priority**: The image is a published interface. A build tool change that breaks one
existing `docker run` line is a regression for every user of the image, and it would arrive
silently with a routine release.

**Independent Test**: Build the image the new way, then run the documented commands against a
mounted sample project and compare results with the image from the last release.

**Acceptance Scenarios**:

1. **Given** the new image and a project mounted at `/work`, **When** the person runs it with
   `--version`, **Then** it prints the release's version and commit, the same text the release
   binary prints.
2. **Given** the same setup, **When** the person runs it with `--list`, **Then** the project's
   tasks are listed.
3. **Given** a project with a task that uses only Rune's built-in shell, **When** the person runs
   that task through the image with extra arguments, **Then** the task runs, receives the
   arguments, and the container exits 0.
4. **Given** a task that fails, **When** it runs through the image, **Then** the container exits
   with the task's exit code.
5. **Given** the new image, **When** the person overrides the entry point and calls `/rune`
   directly, **Then** Rune starts.
6. **Given** the new image, **When** its configuration is inspected, **Then** the entry point
   starts Rune with no added arguments, the working directory is `/work`, the user is the same non-root user as before, the
   descriptive labels are present with the release's version and commit, and the variable that
   tells Rune it runs from the container image is set.
7. **Given** the new image, **When** the person runs it with `--self-upgrade`, **Then** Rune
   declines and says to pull a newer image, as it does today.
8. **Given** a task that fetches from an HTTPS address using only what the image contains,
   **When** it runs, **Then** the certificate check succeeds as it does today.

---

### User Story 2 - A release publishes the image under the same names, from one release run (Priority: P1)

A maintainer cuts a release the way they do today. The one release run produces the archives,
checksums, signatures, package manager entries and the image. The image appears in the same
repository under the version tag, the two per-architecture tags, and, for a stable release only,
`latest`.

**Why this priority**: Tags are how people and automation select an image. A changed repository
path, a missing architecture tag, or a prerelease that moves `latest` each break consumers without
any error on the maintainer's side.

**Independent Test**: Exercise a stable release, a prerelease and a snapshot run in a setting
that cannot touch production tags, and inspect which names each one produces.

**Acceptance Scenarios**:

1. **Given** a stable release, **When** the run finishes, **Then** the image is available as
   `ghcr.io/rune-task-runner/rune:<version>`, `:<version>-amd64`, `:<version>-arm64` and
   `:latest`, and the repository path has no added suffix.
2. **Given** the version tag, **When** it is inspected, **Then** it is a multi-platform image
   with exactly two entries: Linux on amd64 and Linux on arm64.
3. **Given** a prerelease, **When** the run finishes, **Then** the version and architecture tags
   exist and `latest` still points at the previous stable release.
4. **Given** a snapshot or dry run, **When** it finishes, **Then** nothing was pushed to the
   production repository.
5. **Given** any release, **When** the run finishes, **Then** every non-container artifact a
   release produces today is present and unchanged in kind: archives for all six platforms,
   `checksums.txt` and its signature, one bill of materials per archive, binary provenance, the
   Homebrew and Scoop entries, and the editor extension.
6. **Given** the release configuration, **When** build settings (flags, version stamping, target
   platforms) are changed, **Then** there is one place to change them, and the archives and the
   image both follow.

---

### User Story 3 - The image can be verified exactly as before (Priority: P1)

A person who checks what they run verifies the image by its digest: the signature made by the
release workflow, the provenance statement, and the bill of materials. The documented commands
work against the digest that was actually published.

**Why this priority**: Rune's releases are signed end to end, and the image is the artifact most
often consumed without a human looking at it. Losing a signature, or signing something other than
the published digest, removes a guarantee people already rely on.

**Independent Test**: After a release in a test setting, run the documented verification commands
against the published digest and confirm each succeeds, then confirm each fails for a digest that
was not published by the release.

**Acceptance Scenarios**:

1. **Given** a published release, **When** the documented signature check is run against the
   multi-platform digest, **Then** it succeeds with the release workflow as the signer.
2. **Given** the same release, **When** the signature check is run against each per-architecture
   digest, **Then** it succeeds.
3. **Given** the same release, **When** the documented provenance check is run against the
   published digest, **Then** it succeeds.
4. **Given** the same release, **When** the person follows the documented steps to fetch the
   image's bill of materials, **Then** they obtain it for the published digest.
5. **Given** an image that was not produced by the release workflow, **When** the same checks are
   run, **Then** they fail.

---

### User Story 4 - A contributor builds and tries the image locally without publishing (Priority: P2)

A contributor wants to try a change in the container image. They run the documented local command
(today `rune docker`) and get an image they can load into their container runtime or export as a
file. Nothing is pushed anywhere, and no registry login is needed.

**Why this priority**: Local builds are the contributor's feedback loop and the place a mistaken
publish would do the most harm. This matters less than the published image only because fewer
people are affected.

**Independent Test**: On a machine with no registry credentials, run the documented local
commands and confirm an image is produced and no network push is attempted.

**Acceptance Scenarios**:

1. **Given** a checkout and the documented prerequisites, **When** the contributor runs the local
   build command, **Then** an image tagged for local use is available to their container runtime,
   or written as a file, as documented.
2. **Given** a machine with no Docker daemon running, **When** the contributor builds and exports
   the image to a file, **Then** the build succeeds.
3. **Given** any documented local command, **When** it runs, **Then** it does not push to the
   production repository, with or without credentials present.
4. **Given** a contributor who wants to publish to a registry of their own, **When** they follow
   the documented explicit publishing step, **Then** the image goes only where they named.
5. **Given** the locally built image, **When** the scenarios of User Story 1 are run against it,
   **Then** they pass.

---

### User Story 5 - The image binary is the release binary, or the difference is on record (Priority: P2)

Today the image contains the exact binary that is in the release archive for the same platform.
A maintainer needs to know whether that still holds after the change, because the new builder
compiles the binary itself. Before the switch, the two are compared byte for byte for both
architectures and the result is written down.

**Why this priority**: "The image runs the same program as the archive" is a claim people may
rely on when they audit one and deploy the other. It ranks below the first three stories because
a difference, if any, changes no behavior; what must not happen is an unnoticed difference.

**Independent Test**: Extract the binary from each architecture's image and compare its checksum
with the binary in the matching release archive.

**Acceptance Scenarios**:

1. **Given** a release built the new way, **When** the binary in each architecture's image is
   compared with the binary in the matching archive, **Then** the result (identical or not) is
   recorded for both architectures.
2. **Given** the binaries are identical, **When** the documentation is read, **Then** it keeps
   stating that the image contains the release binary.
3. **Given** the binaries differ, **When** the change is reviewed, **Then** the difference, its
   cause and the maintainer's decision are recorded, and the documentation stops claiming they
   are identical. A matching `--version` output alone is not accepted as proof either way.

---

### User Story 6 - Documentation and old build paths are consistent (Priority: P3)

A contributor or user reading the docs finds one current way to build, publish and verify the
image, the prerequisites for it, and what to do when something is missing. Files that only served
the old build path are gone once the replacement has passed its checks.

**Why this priority**: Stale instructions cost time but break nothing that is published.

**Independent Test**: Follow every container build command in the documentation on a clean
machine and confirm each works as written; search the repository for instructions that describe
the old path.

**Acceptance Scenarios**:

1. **Given** the container guide, the release guide and the contributor guide, **When** they are
   read, **Then** every container build command in them is one that works after this change.
2. **Given** a machine without ko or without Go, **When** the contributor runs the local build
   command, **Then** the message says what is missing and how to install it.
3. **Given** wrong or missing registry credentials, **When** an explicit publish is attempted,
   **Then** the documentation explains the failure and how to authenticate.
4. **Given** a reader who wants an image with system packages or a program that needs a C
   toolchain, **When** they read the container guide, **Then** it says this build does not cover
   that and what to use instead.
5. **Given** the base image is referenced by an exact digest, **When** a maintainer needs to move
   to a newer base, **Then** the documented steps tell them how.
6. **Given** the replacement has passed the checks in this specification, **When** the repository
   is inspected, **Then** build files that only the old image path used are removed, and the test
   harness files remain.

---

### Edge Cases

- The builder's default image layout differs from Rune's: it places the program under a different
  path and sets no `/work` working directory. The published image must still have a working
  `/rune` and the `/work` working directory.
- The base image has no shell. Tasks that need an external shell or other tools behave as they do
  in today's image (documented limitation), not better and not worse.
- A project is mounted read-only, or mounted with files owned by another user. Behavior matches
  today's image.
- A release is rerun for the same version after a partial failure. Tags end up pointing at one
  consistent build, and signatures and provenance refer to the digest that is finally published.
- The registry is unreachable or rejects the push during a release. The run fails visibly; it does
  not report success with a missing image.
- The pinned base image digest is no longer available from its registry. The build fails with a
  message that names the base image; it does not fall back to an unpinned one.
- A fork or a pull request runs the release dry run. It cannot publish to the production
  repository.
- Someone runs the local build while logged in to the production registry. Still nothing is
  pushed.
- A prerelease is published after a newer stable release exists. `latest` does not move.
- The release tooling version in use does not support an option this design needs. This is found
  before the switch, not during a release.

## Requirements *(mandatory)*

### Functional Requirements

**Image contract (what people pull)**

- **FR-001**: The image MUST be published to `ghcr.io/rune-task-runner/rune`, with no suffix
  added to the repository path.
- **FR-002**: The image's entry point MUST start Rune with no added arguments, and `/rune` MUST
  remain directly invocable (for example as an overridden entry point). The entry point path
  recorded in the image configuration MAY be the builder's own path instead of `/rune`; this is
  the one accepted change to the image configuration, recorded in the plan (research R3).
- **FR-003**: The image's working directory MUST be `/work`, so a project mounted there is found
  without extra arguments.
- **FR-004**: The image MUST run as the same non-root user as the current image.
- **FR-005**: The image MUST carry the same descriptive labels as today (title, description,
  source, license, version, revision), with version and revision taken from the release.
- **FR-006**: The image MUST include the certificate authorities needed for HTTPS, as today.
- **FR-007**: The image MUST set the variable that marks it as Rune's container image, so
  `rune --self-upgrade` keeps declining inside it.
- **FR-008**: The binary in the image MUST be built without C dependencies, with reproducible
  path trimming, and stamped with the release version and commit, so `--version` output matches
  the release binary's.
- **FR-009**: The image MUST be offered for Linux on amd64 and Linux on arm64, and no other
  platform.
- **FR-010**: The base image MUST be referenced by an exact digest, and a build MUST fail rather
  than use an unpinned base.

**Tags and release behavior**

- **FR-011**: A release MUST publish the tags `<version>`, `<version>-amd64` and
  `<version>-arm64`. The `<version>` tag MUST resolve to a multi-platform image covering both
  architectures.
- **FR-012**: A stable release MUST also move `latest`. A prerelease MUST NOT move `latest`.
- **FR-013**: Snapshot and dry runs MUST NOT push any tag to the production repository.
- **FR-014**: The image MUST be produced by the same single release run that produces the other
  artifacts.
- **FR-015**: Build settings that affect the binary (flags, version stamping, target platforms)
  MUST be defined once and used for both the archives and the image, so they cannot drift. A
  second copy for local builds is allowed only if an automated check fails when it differs.
- **FR-016**: Before the switch, the release tooling version the repository uses MUST be
  confirmed to support every option this design relies on.
- **FR-017**: Building and publishing the image in a release MUST NOT require a Docker daemon.
- **FR-018**: A release MUST fail visibly if the image cannot be built or pushed.

**Supply chain**

- **FR-019**: The published multi-platform digest and each per-architecture digest MUST be
  signed by the release workflow, without long-lived keys, as today.
- **FR-020**: A provenance statement MUST be attached to the digest that was actually published.
- **FR-021**: A bill of materials for the image MUST be produced, and the documentation MUST say
  how to retrieve it for a given digest.
- **FR-022**: The documented verification commands for the image MUST succeed against a release
  built the new way.
- **FR-023**: All non-container release outputs MUST remain: archives for all six platforms,
  `checksums.txt`, its signature, per-archive bills of materials, binary provenance, Homebrew and
  Scoop publication, and editor extension publishing.

**Binary identity**

- **FR-024**: Before the switch, the binary inside each architecture's image MUST be compared
  byte for byte with the binary in the matching release archive, and the result recorded.
- **FR-025**: If the binaries are identical, the documentation MUST keep saying so. If they are
  not, the difference, its cause and the maintainer's decision MUST be recorded in the change's
  review, and the documentation MUST be corrected. Equal `--version` output MUST NOT be treated
  as proof of identity.

**Local workflow**

- **FR-026**: There MUST be a documented local command that builds the image and makes it
  available to the contributor's container runtime. `rune docker` MUST remain that entry point,
  or the documentation MUST name its replacement.
- **FR-027**: There MUST be a documented way to build the image and export it as a file with no
  Docker daemon running.
- **FR-028**: No documented local build command may push to any registry. Publishing MUST be a
  separate, explicit step that names its destination.
- **FR-029**: When ko or Go is missing, the local build MUST say which one and how to get it.
- **FR-030**: The locally built image MUST satisfy FR-002 to FR-007, and its binary MUST be built
  without C dependencies and with path trimming. It need not be stamped with a release version: a
  local build reports itself as a development build, and its version and revision labels say so.

**Documentation and cleanup**

- **FR-031**: The task file, the release configuration, the release workflow, the container
  guide, the release guide and the contributor guide MUST be updated together, in one change.
- **FR-032**: The documentation MUST cover: prerequisites, local build and load, export without a
  daemon, explicit publishing and credentials, authentication failures, how to update the pinned
  base image, and the limits of this build (no system packages, no C toolchain).
- **FR-033**: Build files used only by the old image path MUST be removed, and only after the
  checks in this specification pass. The test harness and its build file MUST stay.
- **FR-034**: Every container build command that appears in the documentation MUST work as
  written after the change.

### Key Entities

- **Image**: What people pull. Has a repository name, a configuration (entry point, working
  directory, user, labels, environment) and one binary.
- **Multi-platform image**: The entry a version tag points to. Lists one image per architecture.
- **Tag**: A name for a digest: `<version>`, `<version>-amd64`, `<version>-arm64`, `latest`.
- **Digest**: The content address of an image or multi-platform image. Signatures, provenance
  and the bill of materials attach to a digest, not to a tag.
- **Base image**: The minimal non-root image Rune's binary is placed on, referenced by digest.
- **Release kind**: Stable, prerelease, or snapshot. Decides which tags move and whether
  anything is pushed.
- **Release binary**: The binary inside a release archive for one platform; the reference for
  the identity comparison.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every `docker run` example in the container guide produces the same output and exit
  code with the new image as with the image of the last release (0 differences).
- **SC-002**: Image configuration matches the current image on the checked fields: working
  directory, user, the six labels, and the container marker variable (8 of 8); the entry point
  starts Rune and `/rune` is invocable.
- **SC-003**: A stable release produces exactly 4 tags, a prerelease exactly 3, and a snapshot or
  dry run 0 in the production repository.
- **SC-004**: The version tag lists exactly 2 platforms.
- **SC-005**: Signature and provenance checks succeed for 3 of 3 published digests (the
  multi-platform one and both architectures) and fail for an image not built by the release
  workflow.
- **SC-006**: A contributor with Go and ko installed and no Docker daemon running can produce an
  image file from a clean checkout with one documented command.
- **SC-007**: Local build commands cause 0 pushes to any registry.
- **SC-008**: The binary comparison is recorded for 2 of 2 architectures before the switch.
- **SC-009**: The list of non-container release artifacts is the same before and after (0 removed,
  0 added).
- **SC-010**: The release run needs no Docker daemon, emulation or multi-platform builder setup
  for the image step.
- **SC-011**: A search of the repository finds 0 instructions that describe the old image build
  path.

## Assumptions

- The image's observable contract is the one documented in the container guide today plus the
  container marker variable added with `--self-upgrade` (issue #84). Anything not listed there is
  not part of the contract.
- "Same non-root user" means the same numeric user and group as the current base image provides.
- The base image stays the same family as today (minimal, non-root, no shell); this feature pins
  it by digest and does not choose a different one.
- Per-architecture tags are kept because existing consumers may reference them, even though the
  version tag already covers both architectures.
- Byte-for-byte identity between image binary and archive binary is the goal. If the builder
  cannot deliver it, that is acceptable only with the recorded decision of FR-025; the feature is
  not blocked on identity alone.
- An image bill of materials counts as "retained" if a person can obtain one for a published
  digest by following the documentation; the exact storage place is a design decision.
- A container runtime is still needed to run or load an image locally. Only building, exporting
  and publishing become daemon-free.
- Release verification in a test setting may use a personal fork or a throwaway repository path.
  Nothing in this feature requires pushing test tags to the production repository.
- The Docker Compose test harness and its build file are unrelated to the distribution image and
  do not change.
- Running user tasks inside toolchain containers is issue #65 and is not affected.
