# Stable Fork Title Design

## Problem

Agent Deck gives a new fork a distinct display title such as
`GEN-8525 centralized-docs (fork)`, but quick and default forks are created with
`TitleLocked=false`. When the provider later publishes its inherited session
name, normal inbound title reconciliation overwrites the fork title with the
parent title. The parent and fork then become indistinguishable even though
their Agent Deck IDs, tmux sessions, and provider conversation IDs are distinct.

The live `centralized-docs` failure demonstrates the complete path:

- parent Agent Deck ID `b5ffb392-1785479318` owns Codex thread
  `019fb6dc-505e-7bb2-a972-02122bc57901` and is title-locked;
- fork Agent Deck ID `a4711258-1786513172` owns the distinct Codex thread
  `019ff47b-b0f5-7680-870e-4e62be977f66` whose rollout records the parent in
  `forked_from_id`;
- the fork's tmux session retains `centralized-docs-fork` in its identity, but
  its persisted Agent Deck row has `title_locked=0`;
- after the new Codex binding appeared, Codex's inherited parent name was
  accepted by Agent Deck's normal unlocked-title reconciliation.

## Required Behavior

1. Every session created through Agent Deck's shared fork dispatch starts with
   a locked title, whether the title was generated or typed explicitly.
2. The invariant applies to Claude-compatible tools, Codex-compatible tools,
   OpenCode, and Pi, independent of whether the fork originated in the TUI,
   CLI, Web UI, or hub.
3. A later provider title/name event must not replace the fork's independent
   Agent Deck title.
4. Existing explicit rename and explicit unlock operations remain available.
   Unlocking a fork deliberately restores normal provider-to-Agent-Deck title
   synchronization.
5. Fork conversation identity remains unchanged: a child continues to bind to
   its own provider thread and must never share or overwrite the parent's
   provider binding.

## Design

Make title locking an invariant of `Instance.CreateForkedInstanceForTool`, the
single cross-provider fork dispatcher used by the production TUI, CLI, Web, and
hub paths. The dispatcher will first create the provider-specific child and then
set `TitleLocked=true` on the successfully created child before returning it.

This boundary is preferable to setting the flag independently in each caller:
new fork surfaces automatically inherit the invariant, and provider-specific
constructors remain responsible only for creating their native child command
and identity. It is also preferable to detecting inherited provider names in
each reconciliation implementation, because a stable fork title is an Agent
Deck identity rule rather than a provider parsing heuristic.

For Codex, the existing locked-creation reconciliation will subsequently seed
the fork title into the child's explicit native `name` once its persisted
binding and thread row are available. Claude keeps the Agent Deck title stable
through its existing title-lock check. Providers without inbound title sync are
unaffected except that the persisted fork record expresses the same invariant.

No automatic migration will rewrite existing unlocked rows. A bulk migration
cannot reliably distinguish an intentionally unlocked historical fork from a
normal session using only the current schema. Known damaged rows can be repaired
with an explicit rename, which also locks and synchronizes the title through the
existing supported path.

## Tests

- Add a table-driven session-layer regression covering Claude, Codex, OpenCode,
  and Pi through `CreateForkedInstanceForTool`; each returned child must be
  title-locked and preserve the requested fork title.
- Mutate the returned Codex fork with a provider name reconciliation attempt and
  verify the generated fork title remains unchanged.
- Update TUI and CLI contract tests that currently require generated fork titles
  to remain unlocked.
- Run focused session, TUI, and CLI fork/title tests, then the affected package
  suites, race coverage where practical, static analysis, and a production
  binary build before commit.
