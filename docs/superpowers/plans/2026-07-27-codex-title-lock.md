# Codex Title Lock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent native Codex thread titles from overwriting an explicitly renamed Agent Deck session.

**Architecture:** Keep `Instance.reconcileTitleFromCodexLocked` as the single inbound Codex-title reconciliation boundary and short-circuit it when the persisted `TitleLocked` flag is set. Preserve the existing unlocked reconciliation path and the independent Agent Deck-to-Codex rename path.

**Tech Stack:** Go 1.25.12 module, SQLite-backed session metadata, standard `testing` package.

## Global Constraints

- Explicit Agent Deck renames set `TitleLocked=true` and remain authoritative.
- Unlocked Codex sessions continue to adopt a different native Codex title.
- Agent Deck-to-Codex rename synchronization remains unchanged.
- `sync_title=false` continues to disable inbound provider-title synchronization globally.
- Do not add title-length heuristics.
- Gemini, OpenCode, and Kiro receive no behavior change because they have no inbound title reconciler.
- Use Conventional Commits.
- Write and verify the failing regression test before production changes.

---

## File Structure

- Modify `internal/session/codex_index_test.go`: reverse the regression test that currently encodes the incorrect locked-title overwrite behavior.
- Modify `internal/session/codex_title_sync_test.go`: keep existing detached and owning-storage reconciliation coverage explicitly unlocked.
- Modify `internal/session/codex_title_sync.go`: enforce the title lock at the shared inbound Codex reconciliation boundary.
- Modify `internal/session/instance.go`: describe `TitleLocked` as provider-neutral inbound-title protection.
- Modify `internal/session/storage.go`: make the persisted field comment provider-neutral.

### Task 1: Enforce Codex Title-Lock Precedence

**Files:**

- Modify: `internal/session/codex_index_test.go:345`
- Modify: `internal/session/codex_title_sync_test.go:13`
- Modify: `internal/session/codex_title_sync.go:28`
- Modify: `internal/session/instance.go:118`
- Modify: `internal/session/storage.go:48`

**Interfaces:**

- Consumes: `Instance.TitleLocked bool`
- Produces: `(*Instance).reconcileTitleFromCodexLocked() (string, bool, error)` returns `("", false, nil)` for locked instances without changing in-memory or persisted titles.

- [ ] **Step 1: Write the failing locked-title regression test**

Rename `TestReconcileTitleFromCodexUpdatesLockedTitleAndPersists` to
`TestReconcileTitleFromCodexNoopWhenTitleLocked` and replace its assertions
after `ReconcileTitleFromCodex` with:

```go
name, changed, err := inst.ReconcileTitleFromCodex()
if err != nil {
	t.Fatalf("ReconcileTitleFromCodex: %v", err)
}
if changed || name != "" {
	t.Fatalf("reconcile = (%q, %v), want locked no-op", name, changed)
}
if inst.Title != "agent deck title" {
	t.Fatalf("instance title = %q, want explicit Agent Deck title", inst.Title)
}
rows, err := db.LoadInstances()
if err != nil {
	t.Fatalf("LoadInstances: %v", err)
}
if len(rows) != 1 || rows[0].Title != "agent deck title" {
	t.Fatalf("persisted rows = %#v, want explicit Agent Deck title", rows)
}
if !inst.TitleLocked {
	t.Fatal("locked reconciliation must preserve the title-lock setting")
}
```

In `internal/session/codex_title_sync_test.go`, remove `TitleLocked: true` from
`TestUpdateStatusReconcilesCodexTitleWithoutLiveTmux` and change
`TitleLocked: true` to `TitleLocked: false` in
`TestReconcileTitleFromCodexPersistsThroughOwningStorageWithoutGlobal`. These
tests continue to prove that unlocked reconciliation and owning-storage
persistence work.

- [ ] **Step 2: Run the regression test and verify the expected failure**

Run:

```bash
go test ./internal/session -run '^TestReconcileTitleFromCodexNoopWhenTitleLocked$' -count=1
```

Expected: FAIL because the current Codex reconciler returns the native title,
reports `changed=true`, and overwrites the persisted Agent Deck title.

- [ ] **Step 3: Implement the minimal title-lock guard**

Change the reconciler comment and its first guard in
`internal/session/codex_title_sync.go` to:

```go
// ReconcileTitleFromCodex pulls a native Codex title into Agent Deck when
// inbound title sync is enabled and the session title is not explicitly locked.
func (i *Instance) ReconcileTitleFromCodex() (string, bool, error) {
	if i == nil {
		return "", false, nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.reconcileTitleFromCodexLocked()
}

func (i *Instance) reconcileTitleFromCodexLocked() (string, bool, error) {
	if i.TitleLocked || !IsCodexCompatible(i.Tool) || strings.TrimSpace(i.CodexSessionID) == "" {
		return "", false, nil
	}
```

Update the comments in `internal/session/instance.go` and
`internal/session/storage.go` to describe `TitleLocked` as blocking native
provider session names from syncing into Agent Deck, rather than mentioning
Claude only. Do not change serialization or database behavior.

- [ ] **Step 4: Run focused tests and verify green**

Run:

```bash
go test ./internal/session -run 'Test(ReconcileTitleFromCodex|UpdateStatusReconcilesCodexTitle|SetField.*Title|SyncCodexSessionName)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run package and full-suite verification**

Run:

```bash
go test ./internal/session -count=1
go test ./... -count=1
git diff --check
```

Expected: all tests pass and `git diff --check` prints no output. If an
unrelated pre-existing failure occurs, record its exact package and error
without changing unrelated code.

- [ ] **Step 6: Commit the implementation**

Stage only the five implementation/test files and commit:

```bash
git add internal/session/codex_index_test.go \
  internal/session/codex_title_sync_test.go \
  internal/session/codex_title_sync.go \
  internal/session/instance.go \
  internal/session/storage.go
git commit -m "fix(codex): preserve locked session titles"
```
