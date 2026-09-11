# Feature Specification: Public Container Image Distribution on GHCR

**Feature Branch**: `026-ghcr-docker-image`

**Created**: 2026-09-09

**Status**: Draft

**Input**: User description: "i want to add destredute thought as docker image / su reat image rune in gh regestrti" — distribute Rune as a Docker image, published as the `rune` image in the GitHub registry.

## Context: What Already Exists

Investigation of the current repository and the live registry found that the container
distribution channel is **already built and shipping**, but is **not reachable by anyone
outside the organization**:

| Aspect | Current state |
|--------|---------------|
| Image reference | `ghcr.io/rune-task-runner/rune` — exists, 27 versions |
| Published releases | `0.4.4`, `0.5.0`, `0.6.0`, plus a moving `latest` |
| Architectures | Multi-arch manifest: `linux/amd64` + `linux/arm64` |
| Base image | Minimal non-root static-binary base (no shell, no package manager) |
| Supply chain | Images signed keyless; build provenance attested and pushed to the registry |
| Documentation | A full Docker guide, plus install-guide and README references |
| **Package visibility** | **Private** — anonymous pulls are rejected |

The consequence: **every published `docker run ghcr.io/rune-task-runner/rune ...` command in
the project's own documentation fails for the public**, because an unauthenticated pull is
denied. The distribution channel is complete except for the access setting, and nothing
currently detects or prevents that regression.

This specification therefore covers **making the container channel genuinely public,
keeping it public, and proving it stays public**, rather than rebuilding publishing
machinery that already works.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Run Rune with no install and no account (Priority: P1)

A developer or CI author who has never used Rune, and who has no account on the hosting
platform, copies the one-line command from the Rune documentation and runs a task in a
project directory. It works on the first try — no login, no credentials, no token.

**Why this priority**: This is the entire point of shipping a container image. Until an
anonymous pull succeeds, every other part of the channel is inert and the published
documentation is actively misleading. Delivering only this story already turns a broken
channel into a working one.

**Independent Test**: From a machine with no stored registry credentials (and with any
existing credentials cleared), pull and run the published image against a sample project
and confirm the task list and a task execution both succeed.

**Acceptance Scenarios**:

1. **Given** a machine with no registry credentials configured, **When** the user pulls the
   published image by its rolling stable reference, **Then** the pull succeeds without any
   authentication prompt or error.
2. **Given** a project directory containing a Runefile, **When** the user runs the image
   with that directory mounted at the image working directory and passes `--list`,
   **Then** the project's tasks are printed exactly as a native run would print them.
3. **Given** the same mounted project, **When** the user runs the image with a task name and
   arguments, **Then** the task executes and the process exit code matches a native run.
4. **Given** a machine with no registry credentials, **When** the user pulls a specific
   released version reference, **Then** that version is retrieved and reports the matching
   version string when asked.

---

### User Story 2 - Pull the right architecture automatically (Priority: P1)

A user on an ARM laptop and a colleague on an x86 CI runner use the identical image
reference. Each transparently receives a binary native to their platform; neither has to
know about architecture suffixes or pick a variant by hand.

**Why this priority**: Equal in importance to the first story for the two dominant developer
platforms. A channel that silently serves the wrong architecture, or forces users to choose,
fails the "install-free" promise it exists to keep.

**Independent Test**: Inspect the published multi-architecture manifest from an
unauthenticated client and confirm both supported platforms are present; then pull and
execute the image on each platform and confirm the binary runs natively.

**Acceptance Scenarios**:

1. **Given** the rolling stable reference, **When** an unauthenticated client inspects the
   manifest, **Then** both supported platforms are listed under a single reference.
2. **Given** an ARM host, **When** the user runs the rolling stable reference, **Then** the
   ARM variant is selected automatically and executes without emulation warnings.
3. **Given** an x86 host, **When** the user runs the same reference, **Then** the x86 variant
   is selected automatically.

---

### User Story 3 - Verify authenticity before trusting the image (Priority: P2)

A security-conscious adopter, or an organization with a supply-chain policy, verifies that
the image they pulled was produced by the Rune project's own release pipeline before
allowing it into their environment — using only public material, with no pre-shared secret.

**Why this priority**: The signing and provenance material is already produced on every
release, but it is only useful once the artifacts it describes can be fetched anonymously.
This story converts existing, currently unreachable supply-chain guarantees into a benefit
users can actually exercise. It is P2 because the channel is usable without it, while
verification unblocks regulated and policy-gated adopters.

**Independent Test**: From an unauthenticated client, verify the signature and the build
provenance of a published release reference and confirm both checks report success and name
the Rune project's own release pipeline as the producer.

**Acceptance Scenarios**:

1. **Given** a published release reference, **When** an unauthenticated user verifies its
   signature against the project's public identity, **Then** verification succeeds.
2. **Given** the same reference, **When** the user verifies build provenance, **Then**
   verification succeeds and attributes the build to the Rune project's release pipeline.
3. **Given** an image reference that was not produced by the release pipeline, **When** the
   same verification is attempted, **Then** it fails clearly rather than passing silently.

---

### User Story 4 - The channel cannot silently go private again (Priority: P2)

A maintainer completes a release. Before the release is considered finished, the pipeline
itself proves that the newly published image is anonymously pullable. If it is not — because
the package defaulted back to private, a new package was created, or the reference changed —
the release surfaces a loud, specific failure instead of shipping a channel nobody can use.

**Why this priority**: This is the difference between fixing the problem once and fixing it
permanently. The current outage went unnoticed across three releases precisely because
nothing checked. It is P2 because the first release after the fix delivers user value
regardless; this story protects that value going forward.

**Independent Test**: Simulate the failure by pointing the check at a reference that is not
publicly readable and confirm the pipeline fails with a message naming the reference and the
visibility problem; then point it at the real published reference and confirm it passes.

**Acceptance Scenarios**:

1. **Given** a completed release whose image is publicly readable, **When** the pipeline's
   post-publish verification runs, **Then** it passes and the release completes.
2. **Given** a completed release whose image is not publicly readable, **When** the same
   verification runs, **Then** the release is reported as failed with a message that names
   the affected reference and identifies visibility as the cause.
3. **Given** a verification failure, **When** a maintainer reads the failure output,
   **Then** it states the specific remediation step needed, not just that a pull failed.

---

### User Story 5 - Discover the container option while choosing how to install (Priority: P3)

A newcomer comparing installation options sees the container as a first-class, explicitly
credential-free choice alongside the native install methods, and understands upfront which
kinds of tasks the minimal image can and cannot run — before hitting a surprise.

**Why this priority**: Documentation exists and is largely accurate today; this story is
about closing the remaining honesty gaps — stating that no authentication is needed, and
making the minimal-image limitations impossible to miss. Valuable, but it changes words
rather than capability, so it ranks below the functional stories.

**Independent Test**: Follow the documentation as a new user with no credentials, from the
installation page through the container guide, and confirm every command shown succeeds and
that the constraints are stated before the commands that hit them.

**Acceptance Scenarios**:

1. **Given** the installation documentation, **When** a reader reaches the container option,
   **Then** it states explicitly that no account or authentication is required.
2. **Given** the container guide, **When** a reader runs each command shown in order,
   **Then** every command succeeds unauthenticated on a supported platform.
3. **Given** the container guide, **When** a reader looks for what will not work,
   **Then** the minimal-image limitations and the recommended alternatives appear before or
   alongside the usage examples, not only at the end.

---

### Edge Cases

- **A user pulls while logged in with credentials that lack access to the package.** The pull
  must still succeed, because the package is public; a stale or unrelated stored credential
  must not turn a public pull into a denial.
- **A prerelease is published.** The rolling stable reference must continue to point at the
  last stable release, never at the prerelease. A user who pulls the rolling reference during
  a prerelease cycle receives the previous stable version.
- **A release fails partway, after images are pushed but before the release completes.** The
  pushed image references must not become the rolling stable reference, and re-running the
  release must not leave two different images claiming the same version.
- **The user mounts a project directory that contains no Runefile.** The container reports the
  same clear, actionable error a native run reports — not a container-specific or truncated
  failure.
- **A task shells out to a program absent from the minimal image.** The failure message names
  the missing executable and is identical in wording to a native run missing that program.
- **Unreferenced intermediate artifacts accumulate in the registry.** Old untagged entries must
  never be reachable as, or mistaken for, a released version, and cleanup of them must never
  remove an entry that a published version reference or its verification material depends on.
- **A user pulls a version that was never released** (e.g. a typo). The registry returns a
  clear not-found response rather than an authentication error that misleads the user into
  thinking they need credentials.

## Requirements *(mandatory)*

### Functional Requirements

**Public access**

- **FR-001**: The published Rune container image MUST be readable by anonymous users, with no
  account, login, token, or other credential of any kind.
- **FR-002**: Every previously published release version that remains listed MUST become
  anonymously readable at the same time as the current one — a user pinning an older released
  version MUST NOT be blocked.
- **FR-003**: The image MUST remain reachable at its existing published reference; this feature
  MUST NOT change, rename, or relocate the reference already printed in released documentation.

**Tags and platforms**

- **FR-004**: A rolling reference MUST always resolve to the most recent **stable** release, and
  MUST NOT be moved by a prerelease.
- **FR-005**: Every released version MUST be retrievable by an immutable, version-specific
  reference for as long as that version is listed.
- **FR-006**: Each user-facing reference MUST resolve, as a single reference, to a
  variant matching the user's platform for both supported platforms, without the user naming
  an architecture.
- **FR-007**: A published version reference MUST NOT be reassigned to different content after
  publication.

**Provenance and trust**

- **FR-008**: Each published release image MUST carry signature and build-provenance material
  that an anonymous user can retrieve and verify using only public information.
- **FR-009**: Verification MUST identify the Rune project's own release pipeline as the
  producer, and MUST fail for images not produced by it.

**Release-time guarantees**

- **FR-010**: The release process MUST verify, after publishing and before reporting success,
  that the newly published image is retrievable **without credentials**.
- **FR-011**: If that verification fails, the release MUST be reported as failed, and the
  failure message MUST name the affected reference, identify public readability as the cause,
  and state the remediation step.
- **FR-012**: The release process MUST NOT depend on a maintainer manually adjusting access
  settings after each release for the image to be publicly usable.

**Registry hygiene**

- **FR-013**: Intermediate and unreferenced registry entries MUST NOT be presented to users as
  released versions.
- **FR-014**: Unreferenced entries MUST be left in place; no automatic or scheduled pruning is
  in scope. Should a maintainer ever prune them by hand, every listed release version and all
  material required to verify it MUST be preserved.

**Documentation**

- **FR-015**: User-facing documentation MUST state that pulling and running the image requires
  no account and no authentication.
- **FR-016**: Documentation MUST state which classes of tasks the minimal image cannot run, and
  MUST offer at least one concrete alternative for each.
- **FR-017**: Every command shown in user-facing container documentation MUST be executable
  as written by an unauthenticated user on a supported platform.

**Scope of the image itself**

- **FR-018**: The published image MUST continue to run as a non-root user by default.
- **FR-019**: The image MUST continue to accept Rune's normal command-line arguments directly
  after the image reference, with behavior matching a native run for tasks the image can execute.
- **FR-020**: The minimal image MUST remain the only published variant. No tooling-bundled
  variant is in scope for this feature; users needing external programs are directed to a
  native install or to building their own image on top of the released binary.

### Key Entities

- **Container image**: A runnable, versioned packaging of the Rune binary. Attributes: version,
  supported platforms, base characteristics (minimal, non-root), and access visibility.
- **Version reference**: An immutable, human-typed name identifying exactly one released image.
- **Rolling reference**: A mutable name that always points at the newest stable release.
- **Platform manifest**: The grouping that lets one reference serve multiple architectures.
- **Verification material**: The signature and provenance records that let any user confirm an
  image's origin without a shared secret.
- **Registry package**: The container of all versions under one reference, carrying the
  visibility setting that determines whether anonymous users can read any of it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user with no account and no stored credentials can go from the documentation to
  a successfully executed Rune task in a container in **under 2 minutes**, on the first attempt,
  with **zero** authentication steps.
- **SC-002**: **100%** of commands printed in user-facing container documentation succeed
  verbatim for an unauthenticated user on a supported platform.
- **SC-003**: **100%** of listed released versions, including those published before this
  change, are retrievable anonymously.
- **SC-004**: Both supported platforms resolve correctly from a **single** reference, verified
  on each platform — the user names an architecture in **zero** cases.
- **SC-005**: A release that would publish an image the public cannot retrieve is reported as
  failed **100%** of the time, with a message that names the reference and the cause.
- **SC-006**: Maintainer effort per release to keep the channel publicly usable is **zero**
  manual steps.
- **SC-007**: An unauthenticated user can verify the origin of a published release image and
  receive a clear pass or fail result in **under 1 minute**, using only publicly available
  information.
- **SC-008**: The number of releases that can ship a publicly unusable image before detection
  drops from **unbounded** (three have already shipped this way) to **zero**.

## Assumptions

- **The existing publishing pipeline is sound and stays in place.** Images are already built for
  both platforms, grouped under one reference, signed, attested, and pushed on every release.
  This feature changes access, verification, and wording — it does not rebuild publishing.
- **The GitHub-hosted registry is the only distribution target.** The request named the GitHub
  registry specifically; mirroring to any other container registry is out of scope.
- **The existing image reference is kept.** It already appears in shipped documentation and in
  released artifacts, so relocating it would break users who have already copied it.
- **Both currently supported platforms are sufficient.** Additional architectures are out of
  scope for this feature.
- **The rolling stable reference already behaves correctly** with respect to prereleases; the
  requirement here records and protects that behavior rather than introducing it.
- **Verification tooling is the user's to install.** The project publishes verifiable material
  and documents the commands; it does not distribute the verification tools themselves.
- **"Public" means read-only public.** Anonymous users can pull; publishing remains restricted
  to the project's release pipeline.
- **Making previously published private versions public is acceptable.** They contain only the
  released Rune binary and its metadata, all of which is already public in other formats.
- **No change to how contributors run the project's own test suite.** The separate
  Docker-based test harness is unrelated to the published image and stays as it is.
- **One published variant only** (decided 2026-09-10). The minimal image stays the sole
  variant. The consequence is accepted and must stay documented: a container user cannot run
  any task that shells out to `git`, `curl`, a compiler, or a language runtime.
- **No registry-cleanup automation** (decided 2026-09-10). Unreferenced intermediate entries
  are allowed to accumulate; they are cosmetic clutter in the package listing and cost nothing
  functionally. Pruning them automatically would risk breaking signature verification for a
  released version, which is a worse trade than clutter.
- **The minimum viable delivery is the visibility change alone.** User Story 1 standing by
  itself converts a channel that fails for every outside user into one that works. Everything
  after it protects that outcome rather than extending it.
