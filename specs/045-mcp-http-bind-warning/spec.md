# Feature Specification: Warn on Non-Loopback MCP HTTP Binds

**Feature Branch**: `045-mcp-http-bind-warning`

**Created**: 2026-11-15

**Status**: Draft

**Input**: GitHub issue [#43](https://github.com/rune-task-runner/rune/issues/43)
(backlog MCP-9): "Warn on non-loopback MCP HTTP binds and remove dead guard"

## Overview

`rune serve --http` serves MCP over plain HTTP. Every request must carry a
bearer token, and the server has no TLS. On a loopback address that is safe:
the token never leaves the machine. On any other address the token crosses
the network unencrypted, and anyone who can read the traffic can replay it
and run the project's tasks.

Today the operator is not told:

```sh
rune serve --http --addr 0.0.0.0:7777 --token-file ./token
```

starts with the same single line as a loopback server. The code has a guard
that was meant to refuse a non-loopback bind unless the address was given
explicitly, but it tests for an empty address after the empty address has
been replaced by the default, so it can never fire.

This feature removes that guard and makes the server say what it is doing:

```text
warning: serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext
```

The server still starts. A non-loopback bind stays an operator's explicit
choice; the warning makes sure it is an informed one.

### Out of scope

- TLS for the HTTP transport (`--tls-cert`, `--tls-key`). The issue leaves it
  to a separate change.
- Refusing a non-loopback bind, or requiring an extra flag to allow one.
- A way to silence the warning.
- Changing the token check, the default address, or the startup line.
- The stdio transport, which has no address and no token.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - An operator who binds beyond loopback is warned (Priority: P1)

An operator wants a networked client to reach the server and starts it with
`--addr 0.0.0.0:7777`. The server starts, and stderr carries one warning that
the bearer token is sent in cleartext. The operator can then decide to keep
the server on a trusted network, put it behind a TLS proxy, or go back to a
loopback address.

**Why this priority**: This is the defect in the issue: the bind exposes the
token and nothing says so.

**Independent Test**: Start the HTTP transport on a non-loopback address with
a token. The warning line appears once on stderr, the server accepts a
request with the token, and it rejects a request without it.

**Acceptance Scenarios**:

1. **Given** a token file, **When** the operator runs
   `rune serve --http --addr 0.0.0.0:7777 --token-file ./token`, **Then**
   stderr holds the line `warning: serving MCP on a non-loopback address
   without TLS; the bearer token is sent in cleartext` exactly once, and the
   server serves requests that carry the token.
2. **Given** the same command with `--addr :7777`, which names no host and so
   binds every interface, **When** the server starts, **Then** the same
   warning is printed.
3. **Given** the same command with `--addr [::]:7777`, or with the address of
   a network interface such as `192.168.1.5:7777`, **When** the server
   starts, **Then** the same warning is printed.
4. **Given** a non-loopback bind, **When** the server runs and then stops
   normally, **Then** its exit status is what it is today: the warning does
   not make the command fail.

---

### User Story 2 - A loopback server stays quiet (Priority: P1)

An operator, or an IDE configuration, starts the server on the default
address or on an explicit loopback address. Nothing is at risk, so nothing is
added to the output. Scripts and tests that read the server's stderr see what
they saw before.

**Why this priority**: A warning that fires on the safe, default setup would
be noise, and operators would learn to ignore it.

**Independent Test**: Start the HTTP transport on `127.0.0.1` with a token.
Stderr holds no cleartext warning.

**Acceptance Scenarios**:

1. **Given** a token file, **When** the operator runs
   `rune serve --http --token-file ./token` with no `--addr`, **Then** the
   server binds its default loopback address and prints no cleartext warning.
2. **Given** `--addr 127.0.0.1:7777` or `--addr [::1]:7777`, or
   `--addr localhost:7777` on a machine where `localhost` is a loopback
   address, **When** the server starts, **Then** no cleartext warning is
   printed.
3. **Given** a Runefile with an `agent` task, **When** the task runs and Rune
   opens its own endpoint for the agent, **Then** no cleartext warning is
   printed: that endpoint is always on loopback.
4. **Given** the stdio transport (`rune mcp`), **When** the server starts,
   **Then** no cleartext warning is printed.

---

### User Story 3 - An embedder gets the same warning where it chooses (Priority: P2)

A Go program embeds the MCP server library and starts its HTTP transport
itself. If it binds a non-loopback address, it can receive the same warning
on an output it chooses. The library does not write to the process's stderr
on its own, because an embedder owns its output.

**Why this priority**: The issue places the check and its tests in the
library. The `rune` command is the first user of it; other embedders get the
same protection.

**Independent Test**: In a unit test of the library, start the HTTP transport
with a destination for warnings, once on a loopback address and once on a
non-loopback address. The destination holds the warning in the second case
and nothing in the first.

**Acceptance Scenarios**:

1. **Given** an embedder that names a destination for warnings and binds
   `0.0.0.0`, **When** the transport starts, **Then** the destination
   receives the warning text once.
2. **Given** the same embedder binding `127.0.0.1`, **When** the transport
   starts, **Then** the destination receives nothing.
3. **Given** an embedder that names no destination, **When** the transport
   starts on any address, **Then** the library writes nothing anywhere and
   the transport starts as it does today.

---

### Edge Cases

- An address with no host, such as `:7777`. It binds every interface, so it
  is not loopback and it warns. The current check treats it as loopback.
- A host name, such as `build-box:7777`. Binding resolves the name, and the
  address that was bound decides: a name that resolves to a network
  interface warns, a name that resolves to loopback does not.
- A host name that only looks like a loopback address, such as
  `127.example.com:7777`. The current check takes it for loopback because of
  how it is spelled. The address it binds decides.
- `localhost` on a machine whose configuration maps it to a network
  interface. The bind is not loopback, so it warns.
- Any address in `127.0.0.0/8`, such as `127.0.0.2:7777`, and the IPv6
  loopback written in any valid form, including an IPv4-mapped loopback
  address (`[::ffff:127.0.0.1]:7777`). These are loopback.
- Port `0`, where the system picks the port. The port plays no part in the
  decision.
- The bind fails, for example because the port is taken. The command fails
  with the bind error as it does today, and no warning is printed: nothing
  is being served.
- No token is given. The command fails with the usage error it gives today,
  before any warning.
- The server reloads its Runefile while running. The listener is not
  reopened, so the warning is not repeated.
- Stderr is not a terminal, or colors are off. The warning is the same text
  without styling, like Rune's other warnings.

## Requirements *(mandatory)*

### Functional Requirements

**The warning**

- **FR-001**: When the HTTP transport is started on an address that is not
  loopback, the `rune` command MUST print this line on stderr:
  `warning: serving MCP on a non-loopback address without TLS; the bearer
  token is sent in cleartext`.
- **FR-002**: The warning MUST be printed once per server start, after the
  address is bound and before the server handles any request. A start whose
  bind fails MUST print no warning.
- **FR-003**: The warning MUST NOT be printed when the address is loopback,
  when no address is given and the default applies, or on the stdio
  transport.
- **FR-004**: The warning MUST NOT change whether the server starts, which
  requests it accepts, or the command's exit status. The token is required
  exactly as before.
- **FR-005**: The warning MUST go to stderr only. It MUST NOT appear on
  stdout or in anything sent to a client.
- **FR-006**: The word `warning` MUST be styled like Rune's other warnings
  when stderr supports styling, and the line MUST be plain text when it does
  not.

**What counts as loopback**

- **FR-007**: A bind is loopback when the address the server bound is an
  IPv4 address in `127.0.0.0/8`, the IPv6 loopback address, or an
  IPv4-mapped form of an IPv4 loopback address.
- **FR-008**: Every other bind is not loopback. That includes an address
  with no host (`:7777`), the unspecified addresses `0.0.0.0` and `::`, and
  any other IP address. A host name counts as the address it was bound to.
- **FR-009**: The decision MUST be made from the address that was bound, not
  from how `--addr` is spelled. It MUST add no name lookup and no network
  access beyond what binding the address already does.

**The dead guard**

- **FR-010**: The check that compares the address with the empty string
  after the default has been applied MUST be removed, together with the
  error message that could never be returned. No command that starts today
  may be refused because of this change.
- **FR-011**: The description of the HTTP transport in the code and in the
  documentation MUST say what happens: a non-loopback bind is allowed and
  warned about, not refused.

**Library, documentation and tests**

- **FR-012**: The embeddable server library MUST make the same loopback
  decision for an embedder and MUST send the warning text to a destination
  the embedder names. With no destination named it MUST write nothing. It
  MUST NOT write to the process's stderr on its own.
- **FR-013**: The endpoint that Rune opens for an `agent` task MUST stay on
  loopback and MUST print no cleartext warning.
- **FR-014**: The documentation of `rune serve --http` MUST state that a
  non-loopback bind sends the token without encryption and prints the
  warning, and MUST say which addresses count as loopback. The command's
  own help MUST NOT show a non-loopback address as its HTTP example without
  saying so.
- **FR-015**: Unit tests of the server library MUST cover a non-loopback
  bind, which produces the warning, and a loopback bind, which does not.
  They MUST also cover the address forms of FR-007 and FR-008.
- **FR-016**: A test of the `rune` command MUST show that the warning
  reaches stderr for a non-loopback `--addr` and is absent for a loopback
  one.

### Key Entities

- **Bind address**: The host and port the operator asks for, from `--addr`
  or the default.
- **Bound address**: The one concrete address the server is listening on
  after binding the bind address. It decides whether the bind is loopback.
- **Loopback address**: An address that only programs on the same machine
  can reach (FR-007).
- **Cleartext warning**: The one fixed line that tells the operator the
  bearer token is sent without encryption.
- **Warning destination**: Where the warning is written. For the `rune`
  command it is stderr; for an embedder it is whatever the embedder names.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every start of the HTTP transport on a non-loopback address
  shows the cleartext warning exactly once.
- **SC-002**: No start on a loopback address, on the default address, or on
  stdio shows the warning.
- **SC-003**: Every command line that started a server before this change
  still starts one, with the same exit status and the same output apart from
  the added warning line.
- **SC-004**: An operator who reads only the server's startup output can
  tell that the token is exposed, without reading the documentation.
- **SC-005**: Each address form listed in FR-007 and FR-008 is classified as
  stated, and the classification takes no network access.
- **SC-006**: The unreachable check and its error message are gone, and no
  description of the transport claims that a non-loopback bind is refused.

## Assumptions

- **Warn, do not refuse.** The issue asks for a warning. Refusing would
  break operators who bind a private interface on purpose, and it would be a
  breaking change the issue does not ask for.
- **The fixed text of the issue is used as given**, with the `warning:`
  prefix that Rune's other startup warnings carry. The address is not
  repeated in it: the startup line already shows it.
- **An address with no host warns.** `:7777` binds every interface. Treating
  it as loopback, as the current check does, would leave the most common way
  to expose the server silent. The command's help uses `:7777` as its HTTP
  example, so the example changes with it (FR-014).
- **The bound address decides, not the spelling.** Binding already turns
  `--addr` into one concrete address. Judging that address is exact for
  every spelling: host names, `localhost`, an empty host, and every way to
  write an IP address. It needs no second name lookup, so the answer cannot
  differ from what was bound.
- **A name that starts with `127.` is a name.** The current check accepts
  any host that begins with `127.` as loopback. Only the address that was
  bound counts.
- **No switch to silence the warning.** It is one line at startup. An
  operator who terminates TLS in a proxy on the same machine binds loopback
  and never sees it.
- **The library stays silent unless told where to write.** It writes nothing
  to the process's output today, and an embedder may use stderr for its own
  protocol. The `rune` command names stderr as the destination.
- **No warning for a failed bind.** If the port is taken, nothing is served
  and no token is exposed. The operator sees the bind error alone.
- **Not a breaking change.** The server starts as before and only stderr
  gains a line. The commit is a fix, and the release notes mention the
  warning.
