# Stable Fork Title Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep every Agent Deck fork's generated or custom title independent from its parent across all supported providers and fork surfaces.

**Architecture:** Establish `TitleLocked=true` at the shared `CreateForkedInstanceForTool` dispatcher after provider-specific creation succeeds. Existing title reconciliation and explicit unlock behavior then enforce the desired policy without provider-specific heuristics or duplicated caller logic.

**Tech Stack:** Go, Agent Deck session model, Go test, SQLite-backed title reconciliation fixtures.

## Global Constraints

- Every shared-dispatch fork is title-locked regardless of whether its title was generated or supplied explicitly.
- Cover Claude-compatible, Codex-compatible, OpenCode, and Pi forks.
- Preserve independent provider conversation IDs and existing explicit unlock behavior.
- Do not mutate live session state or infer bulk repairs for historical rows.

---

### Task 1: Lock fork titles at the shared provider dispatcher

**Files:**
- Modify: `internal/session/instance.go`
- Test: `internal/session/instance_fork_dispatch_test.go`
- Modify: `internal/ui/fork_title_lock_test.go`
- Modify: `internal/ui/home.go`
- Modify: `cmd/agent-deck/rename_title_lock_test.go`
- Modify: `cmd/agent-deck/session_cmd.go`
- Modify: `internal/hub/actions.go`

**Interfaces:**
- Consumes: `(*session.Instance).CreateForkedInstanceForTool(newTitle, newGroupPath string, opts *ClaudeOptions) (*Instance, string, error)`.
- Produces: the same API, with the added invariant that every non-nil successful child has `TitleLocked == true`.

- [ ] **Step 1: Write the failing cross-provider regression**

  Add table cases for Claude, Codex, OpenCode, and Pi to
  `internal/session/instance_fork_dispatch_test.go`. Use each provider's real
  fork constructor prerequisites and assert the successful child retains the
  literal requested title and has `TitleLocked=true`.

- [ ] **Step 2: Run the focused test and verify RED**

  Run:

  ```bash
  go test ./internal/session -run TestCreateForkedInstanceForTool_LocksForkTitleAcrossProviders -count=1
  ```

  Expected: FAIL because each current provider-specific constructor returns an
  unlocked child.

- [ ] **Step 3: Implement the shared invariant**

  Refactor `CreateForkedInstanceForTool` to capture the provider-specific
  result, return provider errors unchanged, and set `forked.TitleLocked = true`
  exactly once for every successful non-nil child.

- [ ] **Step 4: Verify GREEN and provider reconciliation behavior**

  Re-run the focused table test. Add a Codex reconciliation regression using a
  real temporary Codex state database: after native metadata exposes the
  parent's name, `ReconcileTitleFromCodex` must report no change and the child
  must retain the literal generated fork title.

- [ ] **Step 5: Update stale surface contracts**

  Change the TUI test that currently expects quick forks to remain unlocked so
  it requires a locked fork. Remove the CLI's `explicitTitle` split and require
  the resulting fork to be locked for default and custom titles. Remove the
  hub options path assignment that can overwrite the shared invariant with
  `false`. Update comments describing generated fork names as sync-enabled.

- [ ] **Step 6: Run focused and affected-package verification**

  Run:

  ```bash
  go test ./internal/session -run 'TestCreateForkedInstanceForTool|TestReconcileTitleFromCodex.*Fork' -count=1
  go test ./internal/ui -run 'TestCompleteFork|TestQuickFork|TestFork' -count=1
  go test ./cmd/agent-deck -run 'TestHandleSessionFork|TestSessionFork' -count=1
  go test ./internal/hub -run 'Test.*Fork' -count=1
  go test -race ./internal/session -run 'TestCreateForkedInstanceForTool|TestReconcileTitleFromCodex.*Fork' -count=1
  go vet ./internal/session ./internal/ui ./cmd/agent-deck ./internal/hub
  go build ./cmd/agent-deck
  ```

  Expected: every command exits zero with no new warning or failure.

- [ ] **Step 7: Commit the implementation**

  ```bash
  git add internal/session/instance.go internal/session/instance_fork_dispatch_test.go internal/ui/fork_title_lock_test.go internal/ui/home.go cmd/agent-deck/rename_title_lock_test.go cmd/agent-deck/session_cmd.go internal/hub/actions.go
  git commit -m "fix(session): preserve independent fork titles"
  ```
