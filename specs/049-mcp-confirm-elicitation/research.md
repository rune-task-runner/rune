# Research: Approve a Destructive Call Inside the Agent Client

**Feature**: 049-mcp-confirm-elicitation | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

The spec has no open clarifications. Each decision below cites the code it rests on, as read on
`main` at `c33e934`, and the pinned MCP library, `github.com/modelcontextprotocol/go-sdk` v1.6.1.
Statements about the library marked "run" were checked with a throwaway test in the Docker harness:
a tool handler that calls `ServerSession.Elicit`, against an SDK client, over the in-memory and the
Streamable HTTP transports.

## R1. Where the decision is made, and in what order

**Decision**: In the tool handler (`mcpserver/handler.go`), in this order:

1. Find the task in the snapshot. A hidden or private task is not there (unchanged).
2. Work out the ungranted destructive tasks of the call: `ungranted(t)`, split out of `refusal`.
3. If there are none, continue as today.
4. If there are some and the server does not ask, or the engine cannot carry an approval, or the
   session stated no form support: refuse now, before the arguments are read, as today.
5. Otherwise decode and validate the arguments as today, then call
   `ApprovalEngine.CallApproved` with the ungranted names and the server's ask function.

**Rationale**: FR-004 wants the arguments checked before a person is asked. Spec 034 FR-010 wants
the refusal before the argument checks, and existing tests pin that. Both hold when the order
depends on whether the person can be asked: a client that cannot be asked gets exactly today's
order, and a client that can be asked is never refused at step 4. Exposure (`--allow`, `--deny`,
`[private]`, the `[context]` hook, host OS) is decided when tools are registered
(`mcpserver/reload.go`, `internal/cli/mcp.go` `Tasks`), before any of this, so approval cannot
reach a task that is not a tool (FR-005).

**Alternatives considered**:

- Always validating arguments first. It would change the result of a refused, badly formed call on
  every existing client, against spec 034 FR-010, for no gain: that call does not run either way.
- Asking before validating. A person would approve a call that then fails on a misspelled
  argument.

## R2. How Rune knows the client can show a form

**Decision**: `formSupport(session)` reads `session.InitializeParams().Capabilities.Elicitation`.
Nil capabilities or nil `Elicitation`: no. `Form` set: yes. Both `Form` and `URL` nil: yes. Only
`URL` set: no.

**Rationale**: FR-006. The 2025-06-18 revision has `elicitation: {}` and no modes. The 2025-11-25
revision adds `form` and `url` and says an empty object means form only. This is the rule
`ServerSession.Elicit` applies itself (`mcp/server.go:1257` in v1.6.1). Rune checks first so that it
can refuse at the right place with its own text, and never sends a form request to a URL-only
client. The recorded sessions show both tested clients state `{"form":{},"url":{}}`: Claude Code
2.1.325 on revision 2025-11-25 and Codex CLI 0.167.0 on 2025-06-18
(`test/integration/testdata/clients/*/session.jsonl`).

**Alternatives considered**:

- Calling `Elicit` and reading its error. The error is a plain string, and the refusal would come
  after the argument checks.
- Looking at the negotiated revision. A capability is the protocol's way to state support; the
  revision only says which fields can appear.

## R3. What the request says

**Decision**: One request per call. Its message is plain text, built by the library from the names
it worked out itself and from what the engine reports:

```text
Approve this Rune task call?

Task: release
Arguments:
- version: "1.2.3"
Destructive tasks it can run:
- clean: Delete dist?

Approving runs this call once.
```

The `Arguments:` block is left out for a task with no parameters. Each argument value is masked by
the engine and written whole, as a quoted string in which every character that is not printable is
an escape. A variadic parameter shows each of its elements as its own quoted string, or `(none)`.
Each `[confirm]` message is masked by the engine, cut to 500 characters with ` [truncated]`, and
has non-printable characters escaped. Task and parameter names are escaped the same way. A task with no message gets `Run "NAME"?`, the question `confirmQuestion` asks at a
terminal (`internal/cli/confirm.go`). If the whole message is over 8192 bytes, Rune does not ask and
refuses with the fallback text of R9 and the reason "the approval request would be too large to
show".

**Rationale**: FR-007, FR-010, FR-011. The task names come from `ungranted(t)`, not from the
engine, so every task the approval covers is on the page whatever the engine reports. Quoting
argument values keeps an agent-supplied value on its line and makes an empty value visible; `%q`
style escaping also covers bidirectional controls, which `unicode.IsControl` does not. The values
shown are `eval.Args.Values` after `bindNamedParams` (`internal/cli/args.go:163`), which is what the
body interpolates, defaults included, and `eval.Args.Lists` for a variadic, whose elements a
`quote_each` body receives one by one. An argument value is never cut: the run uses all of it, so a
value shown as a harmless beginning and a mark would be approved unseen (FR-013). A `[confirm]`
message is description, not input, so cutting it changes nothing about what runs. Refusing an
oversized request, and not dropping lines from it, means nothing is approved unseen. The limit is the size Rune already uses for agent-facing text
(`contextMaxBytes`, 8 KiB).

**Alternatives considered**:

- One request per destructive task. More prompts and no more control: declining any one refuses the
  call.
- A form with one boolean per task. Task names would become property names, and `tools::clean` is
  not a safe one for every client.
- Hiding arguments that do not fit, or cutting a long value. The person would approve values they
  did not see, and an agent could put the part that matters after the cut.
- Markdown in the message. The protocol does not say clients render it.

## R4. The form and how an answer is read

**Decision**: The requested schema is

```json
{
  "type": "object",
  "properties": {
    "approved": {
      "type": "boolean",
      "title": "Approve this call",
      "description": "Run this task call once.",
      "default": false
    }
  },
  "required": ["approved"]
}
```

`readAnswer` maps the reply to an outcome:

| Reply | Outcome |
|---|---|
| `action: "accept"` and `content.approved` is the boolean `true` | approved |
| `action: "accept"` and `content.approved` is the boolean `false` | not given |
| `action: "decline"` | declined |
| `action: "cancel"` | dismissed |
| any other action; `accept` whose `approved` is missing or not a boolean | no valid answer |
| `Elicit` returned an error and the call's context is done | the call was cancelled |
| `Elicit` returned an error and Rune's wait ended | no answer in time |
| `Elicit` returned any other error | no valid answer |

**Rationale**: FR-008, FR-009, FR-016. A boolean with a false default is one of the primitive
field types every revision with elicitation allows. The library validates an accepted reply against
the schema and returns an error when `approved` is missing or has another type (run). That error is
a formatted string, not a type, and a client error or a broken connection arrive the same way. So
they share one outcome, "no valid answer", and the two cases that can be told apart reliably, the
call's own cancellation and Rune's deadline, are read from the contexts. `readAnswer` still checks
the type of `approved` itself, so the rule does not depend on the library's validation. After an approved
reply, the ask function checks both contexts once more before it returns: the library waits on
the reply and on the context at once, so a reply that arrives together with the deadline or with
the call's cancellation could otherwise win (FR-018).

**Alternatives considered**:

- Accepting `action: "accept"` alone. A client whose only button is "Submit" would approve by
  submitting the default.
- Matching the library's error text to split "invalid" from "failed". It would break on a library
  upgrade, and the agent can do nothing different with the two.
- Passing the client's error text on. FR-020: it is not Rune's text and it reaches the agent.

## R5. How long Rune waits

**Decision**: The ask function wraps the call's context in `context.WithTimeout` with
`Options.ApprovalTimeout`, default `DefaultApprovalTimeout` = 5 minutes. `rune mcp` and
`rune serve` use the default and have no flag for it.

**Rationale**: FR-018 and Principle VIII. When the context ends, the library sends
`notifications/cancelled` for the elicitation request and retires it (`mcp/transport.go:222`), so a
late reply matches no pending request. `Elicit` returned `context deadline exceeded` at the
deadline on both transports (run); a test in this feature sends an approval after the deadline and
checks that nothing runs. Five minutes is the spec's assumption. Clients with a shorter limit on a
call cancel it first (Cursor's CLI and Zed 60 seconds, per the compatibility table), which ends the
same way. Codex's limit is 300 seconds, the same as Rune's wait, so either side may end that call
first; both ends run nothing. The option exists so tests can use milliseconds and an embedder
can choose.

**Alternatives considered**:

- No limit of Rune's own. Zed does not tell the server when it stops waiting, so the call would
  hold its goroutine until the session ends.
- A `--approval-timeout` flag. Out of scope in the spec.

## R6. How an approval reaches the engine's gate

**Decision**: A new optional interface in `mcpserver`:

```go
type ApprovalEngine interface {
    CallApproved(ctx context.Context, name string, args map[string]string,
        lists map[string][]string, tasks []string,
        approve func(context.Context, ApprovalRequest) error) (Result, error)
}
```

`mcpAdapter.CallApproved` goes through the same `call` as `CallLists`. After `bindNamedParams` and
before `scheduler.Run`, `call` builds the `ApprovalRequest` (arguments from the bound values,
`[confirm]` messages from `a.tasks`, both through `a.maskSet.MaskString`) and calls `approve`. If
`approve` returns an error, `call` returns it unchanged and nothing has run. If it returns nil,
`call` sets `eng.approved` to the set of `tasks` and runs. `confirmFailure`
(`internal/cli/confirm.go:24`) returns nil for a task in `e.approved`, next to the existing check of
`e.grants`.

**Rationale**: FR-012 to FR-014. `call` already builds a new `engine` for every call
(`internal/cli/mcp.go`), so a field on it is scoped to one call with no bookkeeping, and concurrent
calls cannot see each other's. Neither `Options.Yes` nor `grants` is touched, so a `[confirm]` task
the request did not name is still declined by `confirm` (nil stdin). Binding once and asking in the
middle means the arguments the person saw are the ones the run uses, by construction. Binding
before approval is safe: defaults are expressions, and the expression language runs no command
(`internal/eval/builtins.go` only looks up paths and reads the environment). A callback keeps the
elicitation in the library and the binding in the engine, with one method to implement.

**Alternatives considered**:

- A context value carrying the approved names. It hides a security decision in a context key, and
  the engine would still need the request data path.
- Two methods, `Prepare` then `Run`. Two calls an embedder can make in the wrong order, and a
  prepared call object to keep alive.
- Adding the names to `grants` for the call. `a.grants` is shared by every call of the adapter; a
  per-call copy would work but would blur "the operator allowed it" with "the person approved it".
- Building the message in the engine. Every embedder would have to repeat the escaping and limits,
  and the names shown would be the engine's word, not the library's.

## R7. Why a repeated call cannot reuse an approval, and which revisions are supported

**Decision**: The approval is a return value inside one handler invocation and a field of one
engine value. No map, token or cache exists. The request is sent with `ServerSession.Elicit`, the
server-initiated `elicitation/create` of revisions 2025-06-18 and 2025-11-25. The guide states
that range. Argument names are checked before asking (R1), so a field such as `approved` in the
tool arguments is an unknown argument unless the task declares it, and then it is only that task's
parameter.

**Rationale**: FR-012, FR-015, FR-024. v1.6.1 negotiates `2025-11-25`, `2025-06-18`, `2025-03-26`
and `2024-11-05` (`mcp/shared.go:46`). The 2026-07-28 revision, where the server answers
"input required" and the client sends the tool call again with the input, is not among them, so no
session can be in that flow. With nothing stored, a second `tools/call` has nothing to present: the
handler runs again from step 1 and asks again. A test sends the same call twice with the same
content and checks two requests and, when the second is declined, one run.

**Alternatives considered**:

- A one-time token bound to session, arguments and snapshot, to prepare for the retried-call flow.
  It is state that must be expired, bound and tested for a flow the library cannot negotiate. When
  the library adds that revision, the binding the issue lists becomes the design input for it.

## R8. A reload while the person is deciding

**Decision**: The handler loads the snapshot once, as today. The ask function, after an approved
answer, compares `s.state.Load()` with that snapshot by pointer. If they differ it returns the
"Runefile changed" result and nothing runs.

**Rationale**: FR-013. `Replace` stores a new `*snapshot` on every successful reload
(`mcpserver/reload.go`), including a change to a body only, and never mutates an old one. So
pointer identity is exactly "no reload happened since the call started". A reload that fails stores
nothing (`reloader.reload` in `internal/cli/mcpreload.go`), the server goes on serving the old
tasks, and the approved call is still what was shown. A `--no-reload` server never calls `Replace`
after start. A `Replace` between the comparison and the run is the case of any call in progress
during a reload, which finishes on the engine it started with (spec 043).

**Alternatives considered**:

- Hashing the Runefile's files on disk. It disagrees with what the server serves when a save does
  not load, and reads files on every approved call.
- Running against the new snapshot. The person approved the old one.

## R9. The refusal for a client that cannot be asked

**Decision**: `refusal(t)` is unchanged. When the server asks but this call cannot be asked about,
the handler appends `cannotAsk(called, reason)`:

```text
task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server. Or the operator can run "rune release" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.
```

The middle sentence is present when `Options.RunCommand` is set (`rune` on `rune mcp` and
`rune serve`). It starts `Run` and not `Or run` on a server with no `AllowDestructiveFlag`, whose
refusal offers no other way. The reason is one of "the client does not support MCP form elicitation" and "the
approval request would be too large to show" (R3).

**Rationale**: FR-021. Whole sentences after the existing text compose with all of its forms (one
task, several, a destructive called task, a server with no flag) with no new grammar cases. The
command names the called task, since `rune release` at a terminal asks about `clean` itself (spec
027). Tests that pin the text on `rune mcp` add the suffix through one helper.

Every outcome but a cancelled call and a changed Runefile ends with one more sentence, built from
the same names, flag and command. It names the operator, because the agent reads it too:

```text
task "release" was not approved: the request was dismissed. Nothing ran. To run it without a request, the operator can add clean to --allow-destructive and restart the server, or run "rune release" themselves.
```

Claude Code run as `claude -p` and Codex run as `codex exec` both state form support
(`test/integration/testdata/clients/*/session.jsonl`), so a headless run of either takes the ask
path, where nobody can answer. Before this feature such a run got a refusal naming the flag at
once. The added sentence keeps that way out in the result. What each of them answers was recorded
as soon as the ask path existed (R15), and it decided which outcomes carry the sentence.

**Alternatives considered**:

- Rewriting the sentence. Every form of the existing text would need a second variant.
- Putting the full command line with arguments in the text. The agent already has them, and it
  would put agent-supplied values into a second place that needs masking and limits.

## R10. Which servers ask

**Decision**: Three new `Options` fields: `AskApproval bool`, `ApprovalTimeout time.Duration`,
`RunCommand string`. `ServeMCP` (`internal/cli/serve.go`) sets `AskApproval: true` and
`RunCommand: "rune"`, or `"rune -f PATH"` with the Runefile's absolute path when the server was
started with `--file`: such a server does not serve the Runefile of the directory the operator is
in, so a bare `rune clean` there could find none, or run another project's `clean` (found in code
review). `agentMCPOptions` (`internal/cli/agentexec.go`) is not changed. The library
asks only when `AskApproval` is set and the snapshot's engine implements `ApprovalEngine`.

**Rationale**: FR-001, FR-022, FR-023. A zero `Options` keeps an embedder exactly where it is. The
agent task endpoint serves an agent Rune started itself, with no person at that client, so it must
not ask whatever that agent states. `mcpAdapter` implements `ApprovalEngine` for both servers; the
option is what separates them.

**Alternatives considered**:

- Asking whenever the engine implements the interface. The agent endpoint uses the same adapter
  type and would start asking.
- A `rune mcp --no-ask` flag. Out of scope in the spec; `--deny` already rules a task out.

## R11. Documents and compatibility evidence

**Decision**: Change, in the same PR:

- `docs/mcp.md`: the security model bullets, a new section "Approving a destructive call in the
  client" (request, coverage, outcomes, wait, fallback, trust in the client, revisions 2025-06-18
  and 2025-11-25), the note under "Structured results" that lists calls with no structured content,
  new rows in the compatibility table, and a paragraph in each client section.
- `docs/runefile.md`, `docs/use-cases/mcp-agents.md`, `docs/examples/agent-driven/README.md`,
  `docs/examples/agent-driven/Runefile` (its header comment), `docs/user-guide/README.md`,
  `docs/design/rune-only-agent-mode.md`, `SECURITY.md`, `README.md` and the help text in
  `cmd/rune/serve.go`: only the sentences that say an ungranted `[confirm]` task is always refused
  over MCP.
- `.specify/memory/constitution.md`: Principle VII (R14).
- `specs/031-mcp-destructive-optin/contracts/mcp-tools.md`: a section on the request and the
  fallback.
- `test/integration/testdata/clients/README.md`: the new recordings.

Compatibility rows: "Elicitation modes stated", "A `[confirm]` call with no grant", "When nobody
answers". Evidence rules stay as spec 046 set them. "Recorded" cells come from the
existing recordings (the stated modes) and from new recordings of Claude Code and Codex CLI run
without a display against a build of this branch, calling `clean` with no grant. Cursor, VS Code and
Zed cells come from vendor documentation or source read during implementation, or say unknown.

**Rationale**: FR-025 to FR-027, SC-007. `TestClientRecordings` holds each recording to the claim
the table makes about it, so new "recorded" cells get cases there. A headless run cannot show a
form, so what it records is what that client does with the request when nobody can answer. That is
itself a row a reader needs. The interactive UI of each client can only be read about, and is marked
so.

**Alternatives considered**:

- Claiming the interactive behavior from the stated capability. A stated capability is not an
  observed prompt.
- Leaving the table for a follow-up. The issue's acceptance criteria name it.

## R12. Tests

**Decision**: Written first, failing, at each layer:

- `mcpserver/approval_test.go`: `formSupport` (nil, empty, form, URL only, both); `approvalMessage`
  (layout, default question, quoting, escapes, cuts, masked values passed through, oversize);
  `approvalSchema`; `readAnswer` (every row of R4); `cannotAsk`; handler cases over the in-memory
  transport with a fake `ApprovalEngine` and a client `ElicitationHandler`: ask before run,
  arguments first, each outcome runs nothing, timeout, cancelled call, reload during the wait,
  concurrent calls, no ask without `AskApproval`, no ask when the engine lacks the interface.
- `mcpserver/authz_test.go`: `ungranted` order and deduplication.
- `mcpserver/transport_test.go`: the ask path over Streamable HTTP, and two sessions each getting
  only their own request.
- `internal/cli/mcp_approval_test.go`: `CallApproved` runs a `[confirm]` body with nil stdin;
  a `[confirm]` dependency not named stays declined; an `approve` error runs nothing and comes back
  unchanged; defaults in the request; a secret argument masked.
- `internal/cli/mcp_test.go`: the agent endpoint's options and its refusal.
- `internal/cli/confirm_test.go`: `confirmFailure` with `approved`.
- `test/integration/approval_mcp_test.go`: the binary on stdio and on `--http`, an SDK client with
  an elicitation handler, marker files. Covers every row of the quickstart.
- `test/integration/clients_fixture_test.go`: the new recordings.
- `test/docs`: the changed pages.

**Rationale**: FR-028 and Principle VI. The issue asks for the engine gate to be covered as well as
the handler: an approval that the handler accepts and the engine still declines for lack of a
terminal is the bug it names. The `internal/cli` and binary-level tests run real `[confirm]` bodies.

## R13. Release note

**Decision**: One commit and PR title of type `feat(mcp)!`, with the change in the subject.

**Rationale**: Behavior on `rune mcp` and `rune serve` changes for an existing configuration: with
a client that states form support, a call that was always refused can now run after the person
approves, and the refusal text for other clients is longer. The changelog is generated from commit
subjects (`cliff.toml`), as specs 034, 041 and 042 relied on.

## R14. Constitution Principle VII

**Decision**: Amend Principle VII in the same PR, version 1.3.0 to 1.4.0. The sentence "Destructive
tasks are refused on the agent surface by default, and a task makes no read-only claim unless its
author declares one." becomes: "Destructive tasks are refused on the agent surface by default: one
runs only when the operator granted that task when the server started, or when the person at the
client approves that one call. A call is put to the person only through a client that states it
can ask, and anything but an explicit yes is a refusal. A task makes no read-only claim unless its
author declares one." The Sync Impact Report says the amendment is proposed with spec 049 and that
merging the PR is the maintainer's approval.

**Rationale**: The constitution's Governance section says a justified deviation is recorded or the
file is amended. With default flags and a client that states form support, a destructive call is
no longer refused outright: it is put to the person. Leaving the sentence as it is would make the
constitution say something the product does not do, which is what spec 033's amendment (1.2.0 to
1.3.0) corrected for the read-only clause. The change adds a way a destructive task can run, so it
is a MINOR bump, as that one was. The PR description lists the amendment as its own item, so the
maintainer can accept the feature and reword the principle differently.

**Alternatives considered**:

- Recording a "reading" in the plan and leaving the file. An independent review of this plan found
  that reading too generous: the sentence, read as written, is no longer true.
- Recording a deviation in Complexity Tracking. A permanent behavior is not a one-feature deviation.

## R15. What the recorded clients answer

Recorded on 2026-11-17 against a build of this branch, with the server run as `rune mcp` (no
grant), in a copy of the scenario of spec 046. The prompt asked for one call to `clean`. The
recordings are `test/integration/testdata/clients/*/elicitation.jsonl`.

| | Claude Code 2.1.326, `claude -p` | Codex CLI 0.167.0, `codex exec` |
|---|---|---|
| Stated at `initialize` | `elicitation: {form: {}, url: {}}`, revision 2025-11-25 | `elicitation: {form: {}, url: {}}`, revision 2025-06-18 |
| Rune sent `elicitation/create` | yes, 1 ms after the call | yes, 1 ms after the call |
| The client answered | `{"action": "cancel"}` after 9 ms | `{"action": "decline"}` after 1 ms |
| `clean` ran | no | no |
| The model was given | the dismissed text | the declined text |

**Decision**: The declined and not-given outcomes carry the remedy sentence too, not only the
three in which nobody answered.

**Rationale**: Codex's answer is a decline that no person gave. Rune cannot tell it from a
person's, so the declined text is what every headless Codex run gets. Without the sentence that
run would lose what it had before this feature: a result that names the flag. After a person's
own decline the sentence is harmless: it says what the operator can do, and the person is the
operator. Both clients accepted `mode: "form"` on their revision, including Codex on 2025-06-18,
which has no such field.

**Alternatives considered**:

- Treating `decline` within a few milliseconds as "nobody answered". Timing is a guess, and a
  hook or a fast person would be misread.
- Asking only clients known to show a form. Rune would need a list of client names and versions,
  which the capability exists to replace.
