# Contract: Setup Outcome

This feature adds no command and no interface. The one thing it hands to the
implementation, issue #53, is a vocabulary: the three outcomes a client's setup may be
given, and what has to be true before each is said. The design note's summary table and the
follow-up text both use it.

## Definitions

A **command-running tool** is a tool of the client, not of Rune, through which an agent can
run a command of its choosing. It is **removed** when the client does not give it to the
model. It is **refused** when the model has it and the client refuses each call.

## The three outcomes

| Outcome | Says | True only when |
|---|---|---|
| **Rune-only** | In every session the client starts in this project, the agent has Rune's tools and no command-running tool. | The configuration is in project-scoped files. Every command-running tool the adapter knows for the client's version is removed. Rune's tools are registered and allowed. No step is left for the user, or the output names each one that is. |
| **Rune-only within SCOPE** | The same, but only inside a named profile, custom agent, mode or way of starting the client. | As above within the scope. The output names the scope and says that sessions outside it keep their command-running tools. |
| **Registered only** | Rune's tools are available. The client's command-running tools are too. | Rune is registered. One or more command-running tools are neither removed nor covered by a scope. The output names each. |

## Rules

1. **No fourth word.** A setup is never called "restricted", "locked down", "safe" or
   "Rune-only" on any other ground. A tool that is refused and not removed does not make a
   setup Rune-only: the output says "refused, not removed" and gives the outcome that the
   rest of the setup earns.
2. **Registration is not restriction.** An outcome that leaves a command-running tool
   available names that tool in the same message that reports success.
3. **A step left to the user is part of the outcome.** If the project must be trusted, the
   server approved, or the client restarted before the configuration applies, the outcome
   is reported as pending that step, and the step is named. Until then the session may have
   a shell and no Rune tools at all.
4. **Unknown version, weaker outcome.** For a client version the adapter does not know, the
   strongest outcome it may report is "Registered only".
5. **The outcome is about the agent, not the user.** No outcome claims that the person at
   the keyboard cannot undo the configuration. Only an organization's managed settings bind
   the user, and those are outside a project.
6. **The outcome is not a sandbox.** No outcome says anything about what a task can reach
   once Rune runs it.

## What a check has to show

Before a client and version is listed as able to reach "Rune-only" or "Rune-only within
SCOPE", someone has to have seen, in a session started the way the outcome claims:

1. The client's tool list holds Rune's tools.
2. The client's tool list holds no command-running tool, from the client's own listing
   where it has one, and not from what the model says.
3. A call to a Rune tool runs without a question the session cannot answer.
4. A subagent, where the client has them, has no command-running tool either, or cannot be
   started.

The observation is kept with the client version, the configuration, the command and the
output, as the files in [observations/](../observations/README.md) are.

## Mapping to the follow-up text

| Outcome | In the follow-up text for issue #53 |
|---|---|
| Rune-only | supported |
| Rune-only within SCOPE | partial |
| Registered only | not supported as Rune-only; registration may still be offered, and is reported as such |
