# OMP session ownership and lifecycle recovery

## Required outcome

One Agent Deck entry owns one active OMP conversation. Provider-created branches,
previous conversations and task artifacts are history, not competing active
conversations. Create, native fork, in-provider switch/new/branch/move, rename,
exit, restart and attach must preserve this invariant. A failure must be visible
in the TUI and remote preview, not just a red X or a vanished pane.

## Incident evidence

- Installed Agent Deck v1.13.4 and OMP v18.1.15.
- A managed entry had two root transcripts: its original conversation and a
  native branch. The intended current work was in the branch; timestamps alone
  were not sufficient evidence of ownership.
- The root-count guard rejected both; the TUI Enter path used Restart, which did
  not arm the asynchronous startup-error watcher. Diagnostic CLI Start printed
  success; that was not what the user saw in the TUI.
- Targeted recovery preserved the original JSONL in
  a per-entry preserved-history directory and started the branch explicitly.
  Both transcript SHA-256 hashes were unchanged by recovery and initial resume.
- Read-only audit after recovery: 21 local OMP entries, each with one distinct
  root UUID; all nine running OMP panes' current terminal breadcrumbs point to
  their own entry directories. This is evidence about current mappings, not a
  claim that every historical conversation was semantically correct.
- systemd-run expands command arguments by default; shell parameter expansions
  in the legacy-migration script were erased before bash. Existing tests ran the
  generated script directly under bash and missed the production launch boundary.

## Design

### Explicit ownership, continuously maintained

Use a per-entry active binding, containing provider UUID, exact transcript path,
materialization state and launch generation. Do not infer the current conversation
from directory mtime. A bundled explicit OMP extension observes the root session
manager on lifecycle events and a lightweight interval (the provider's `/move`
does not emit switch events). Only the interactive root context may write binding
state; task/subagent extension instances must never do so. Generation fencing
prevents superseded processes from overwriting a newer launch's state.

State written by the provider is generation-specific: use
`.agent-deck-active-session.<generation>`,
`.agent-deck-omp-status.<generation>.json`, and
`.agent-deck-fresh-pending.<generation>`. The common binding is read only for
legacy entries with no `.agent-deck-launch-generation`. A check followed by a
rename/unlink of a shared file is not sufficient fencing across processes.
Launch setup snapshots the predecessor binding into
`.agent-deck-source-binding.<new-generation>` before rotating the generation.
That snapshot authorizes resume and reserves ownership during startup, but is
never treated as the new provider's acknowledgment. Missing current ACKs cannot
fall back to old roots. A previously saved file disappearing is an error, not a
new lazy conversation.

Creation and native forks install the same extension; forks get new OMP IDs and
independent bindings. New conversations with lazily created files remain marked
pending, so restarting an unused `/new` cannot resurrect the prior conversation.
Bind by ID and exact path, including a provider-authorized move; never switch an
entry to a conversation owned by another entry. Detect conflicts before a switch
where the provider supports cancellation, and expose a persistent diagnostic if
a provider path bypasses that event.

Pending OMP fork intent must survive an Agent Deck save/load before provider
acknowledgment, not only an in-memory retry. Store that OMP-only intent using
the existing session metadata mechanism. Once the child is acknowledged, normal
save clears the pending recipe; if an older saved recipe is replayed after a
crash, the exact bound child is resumed instead of creating another fork.

Capture manager identity before new/fork/branch transitions as well as afterward:
a `/move` immediately followed by another operation may precede the next timer.
Revalidate ownership when asynchronous title operations finish, so old failures
cannot clear a newer identity error.

Titles explicitly set by Agent Deck remain authoritative. Push them into the
bound OMP conversation using the provider API, including on fork/start and later
rename. Do not rewrite every transcript sharing a directory or an ancestor UUID.

### Upgrade and ongoing health

Audit/reconcile saved entries before a future restart is needed. Bootstrap an
unbound entry from its sole valid root or a uniquely corroborated, entry-scoped
provider breadcrumb. Preserve all history. Conflicting evidence produces visible
candidate information for deliberate recovery, not a silent refusal or an
arbitrary timestamp choice. This change does not introduce a history-selection
menu or silently repair conflicting identities. A bound entry can retain historical root files
without becoming unstartable. Periodic checks are bounded and do not read entire
conversation files or block session-list navigation.

### Launch and errors

All lifecycle entry points share validation and diagnostics. Capture errors
before losing the pane, preserve target-side failure information, and expose it
through existing CLI/TUI/hub preview surfaces. An asynchronous watcher owned by
the short-lived CLI is insufficient. Readiness must not rely on the positive
tmux existence cache. Fix systemd argument transport at the actual launch
boundary, including service, scope and direct fallback, preserving old-systemd
compatibility.

Legacy re-keying retains its recovery marker after archive/artifact-copy work.
Only a later launch that validates the provider's actual acknowledgment may
clear it. A crash before acknowledgment can therefore retry the exact preserved
replacement without generating another fork. Error previews retain bounded
diagnostic tails rather than rendering an entire scrollback in the error bar.

### Constraints

- Do not change or restart the recovered user session during implementation.
- Do not update OMP or Codex to implement this; test installed OMP v18.1.15.
- Do not modify credentials or unrelated settings, histories or worktrees.
- Use the configured disk-backed TMPDIR, GOTMPDIR and Go caches.
- Keep target-side behavior functional for local, hub-owned, SSH and sandbox
  launches; no new jq/node dependency on a target just to parse binding state.
- Commit, review, push, release and install the completed tested fix; no claim of
  general resolution based only on the local data recovery.

## Verification contract

Regression coverage must exercise production consumers, not generated-string
snapshots alone: fresh create; independent fork and rename; normal resume; old
multiple-root recovery; in-OMP new/branch/switch/move; lazy/unmaterialized new;
subagent events; stale generations; duplicate IDs; corrupt/missing bindings;
startup error before the first watcher tick; TUI Enter/Restart error display;
CLI lifetime; hub result/preview propagation; and systemd shell argument fidelity.
Run a real isolated OMP lifecycle smoke test with no model requests, plus the
relevant Go suites, race checks, lint, govulncheck, full CI and required review.
