# OMP Session Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Preserve one active OMP conversation per Agent Deck entry throughout its lifecycle and make failures actionable.

**Architecture:** Record the provider's actual root identity continuously through an explicitly bundled OMP extension. Shared Go/shell consumers use that binding for restart/fork/title updates; proactive reconciliation handles legacy rows. Correct systemd transport independently and use durable lifecycle diagnostics across CLI/TUI/hub.

**Tech Stack:** Go, POSIX/bash launch scripts, OMP extension JavaScript, Node built-in test runner, isolated tmux/systemd integration tests.

**Spec:** `docs/superpowers/specs/2026-09-09-omp-session-lifecycle.md`

## Global Constraints

- Do not change or restart the recovered user session during implementation.
- Do not update OMP or Codex to implement this; test installed OMP v18.1.15.
- Do not modify credentials or unrelated settings, histories or worktrees.
- Use the configured disk-backed TMPDIR, GOTMPDIR and Go caches.
- No new jq/node dependency on a target just to parse binding state.
- Preserve historical transcripts; current means a bound identity, not maximum mtime.
- Root interactive extension contexts only; subagents cannot update owner state.
- Conventional Commits; main controller owns commits, reviews, publishing and installation.

## Current verification note

The checked implementation items have focused verification. Consolidated
verification/review/delivery is still in progress;
this document does not assert that a new release has shipped.

## Task 1: Preserve shell arguments across systemd launch

**Files:** `internal/tmux` launch boundary and focused tests.

- [x] Reproduce with production `startCommandSpec` and `newSpawnCommand` on a dedicated socket/unit.
- [x] Test service/scope/fallback-scope with fresh/existing servers and direct fallback.
- [x] Preserve braced shell expansion, plain variables, literal dollar signs and PID expansion; handle pre-v254 systemd without corrupting direct fallback.
- [x] Run focused unit/race tests and real isolated systemd tests; review complete task diff.

## Task 2: Track root OMP identity throughout the live lifecycle

**Files:** create `internal/session/omp/identity.mjs` and `identity.test.mjs`.

**Interfaces:** use these exact host-independent target-side files under
`AGENTDECK_OMP_DIR` (always absolute, supplied by launch code):

```text
.agent-deck-active-session.<generation> # atomic five-line binding, trailing newline
1                           # schema line
/absolute/root.jsonl        # exact provider path, no newline/NUL
provider-session-uuid        # actual getSessionId(), no newline/NUL
pending                     # pending or saved; saved iff real file exists
launch-generation           # AGENTDECK_OMP_LAUNCH_ID
```

The preceding representation shows file name as a comment; actual file begins
with `1`. `.agent-deck-launch-generation` contains the currently authorized launch
ID. `.agent-deck-omp-status.<generation>.json` carries `instance_id`, `launch_id`, `pid`,
`session_id`, `session_file`, `identity_ready`, `error`, `updated_at` (ISO string).
`.agent-deck-title.json` carries `{ "title": "explicit title" }`.

The common binding is legacy-only when there is no generation file. Setup takes
a per-launch `.agent-deck-source-binding.<new-generation>` snapshot before
rotation. Only the new provider writes the current ACK. Pending-fresh markers
are also generation-specific, so stale callbacks cannot delete newer intent.

Launch supplies `AGENTDECK_INSTANCE_ID`, `AGENTDECK_OMP_DIR`,
`AGENTDECK_OMP_LAUNCH_ID`; extension needs no global configuration mutation.

- [x] Write Node behavior tests which invoke real extension callbacks against temporary files and a narrow fake provider context; watch failures before implementing.
- [x] Default-export a provider extension that obtains exact identity from `ctx.sessionManager`, writes atomic binding/status, and fences stale launches.
- [x] On `session_start`, `session_switch`, `session_branch`, `agent_end`, `session_shutdown`, and a root-owned `ctx.setInterval` at 1000ms, reconcile current identity/materialization. Guard every callback with `ctx.mode === 'tui' && ctx.hasUI`; never register child timers.
- [x] Preserve pending initial conversations; detect file materialization rather than assuming `message_end` has persisted it.
- [x] On `session_before_switch`, reject another Agent Deck entry's root, nested subagent files, and conflicting owned IDs explicitly with `{cancel:true}` plus visible diagnostic. Catch errors; OMP otherwise swallows exceptions and fails open.
- [x] Track provider `/move` by actual session-manager path: permit relocation of this same UUID outside the managed directory only when not inside another entry's directory and not claimed by another entry. Never rewrite another owner's mapping or fall back to an older local transcript.
- [x] Synchronize explicit title intent via `await pi.setSessionName(title)` on start/switch and when the title file changes; never copy inherited provider names into the entry.
- [x] Verify start/new/fork/branch/move/switch, titles, pending-to-saved, children, stale generations, malformed state and write failures. Missing extension acknowledgment must remain distinguishable from identity-ready.

## Task 3: Consume identity consistently and reconcile existing entries

**Files:** `internal/session/omp_binding.go`, `omp_binding_test.go`, command builders and lifecycle dispatch in `instance.go`, `mutators.go`, plus appropriate CLI/UI tests.

- [x] Write `TestOmpRecordedBindingSurvivesHistoricalRoots`; verify old resume and CanFork reject valid recorded binding with historical roots.
- [x] Parse/validate the fixed-line binding in Go and equivalent target-side shell logic. Use exact bound file for both resume and fork; retain every historical file.
- [x] Bootstrap unbound unique roots, or an unambiguous entry-scoped provider breadcrumb. Read bounded headers, not full histories. Reject conflicting evidence with candidate information for deliberate recovery.
- [x] Install the embedded extension on the execution host via portable target-side setup, then supply launch generation and title intent. Cover local, SSH, hub-owned and sandbox command construction.
- [x] Route normal Start, StartWithMessage, Restart and native fork through the same validation; do not repeat import flags or fork a second time after first start.
- [x] Persist pending OMP-only fork intent across actual storage save/load, clear after acknowledged completion, and safely replay a stale pending recipe as exact child resume. Explicit RestartFresh supersedes pending fork intent.
- [x] Checkpoint finalized child rows before provider start in all production fork callers; retain recoverable children/worktrees on errors and avoid duplicate registry entries after watcher reload. Replay an acknowledged child independently of its parent's continued availability.
- [x] Preserve existing identity state across repeated recognized non-TUI launches; reject unsupported identity-changing operations and interactive initial-message delivery before launch, without affecting interactive ephemeral sessions.
- [x] Reconcile/audit existing entries before restart and on bounded metadata refresh; surface identity health while idle/running, not only after process death.
- [x] SetField title writes only this entry's title intent; provider updates only its actual bound conversation. Test independent fork titles and original history unchanged.
- [x] Test corrupt/missing bindings, ID mismatch, copied legacy IDs, filename/path quoting, multiple histories with explicit binding, pending new, stale generation, moved history and remote path isolation.

## Task 4: Durable feedback, end-to-end verification and delivery

**Files:** shared launch/failure code, TUI restart/preview tests, CLI and hub tests; release metadata after implementation is verified.

- [x] Shared preflight errors call the existing durable prepare-failure path before spawning; TUI Enter must show the reason.
- [x] Target-side failures retain a diagnostic even if process exits before the watcher or the CLI exits. Missing/current-generation identity acknowledgment is visible, never silently treated as an established binding.
- [x] Test the real TUI Restart path, immediate exit, CLI lifetime, hub response and preview. Do not use cached Exists as proof of a live/ready provider.
- [x] Run real isolated OMP v18.1.15 no-model-request smoke: create, new/branch, fork into distinct row, rename, exit/resume and failed startup. Never touch user sessions for tests.
- [x] Re-run metadata-only audit of existing sessions; report any unresolved item, no blind history rewrites.
- [x] Reconcile queued title and group edits together before reload persistence; verify the adjacent shared ordering bug exposed by fork-checkpoint tests without changing provider behavior.
- [ ] Run focused tests, race coverage, lint, govulncheck and full CI. Complete local review and required PR reviews, resolve findings, merge and publish release, install verified release artifact; preserve dirty unrelated worktrees.
