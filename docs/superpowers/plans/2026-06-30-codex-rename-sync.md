# Codex Rename Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep Codex's saved session name in sync when a Codex-compatible Agent Deck session is renamed.

**Architecture:** Reuse or create a narrow Codex index append helper in `internal/session`. Wire `session.SetField(inst, session.FieldTitle, newTitle, nil)` to return a post-commit hook that appends a new Codex `session_index.jsonl` record after Agent Deck state is saved.

**Tech Stack:** Go 1.24+, Agent Deck session mutators, JSONL append semantics, existing CLI/TUI/web rename paths.

## Global Constraints

- Do not parse or rewrite Codex transcript contents.
- Do not block an Agent Deck rename if Codex index sync fails.
- Existing non-Codex rename behavior must remain unchanged.
- Missing `session_index.jsonl` is valid for rename; append creates it under `CODEX_HOME`.
- Use Conventional Commits.
- Write failing tests before production code.

---

## File Structure

- Create or modify `internal/session/codex_index.go`: append-only Codex name sync helper.
- Create or modify `internal/session/codex_index_test.go`: append helper tests.
- Modify `internal/session/mutators.go`: title mutator post-commit wiring.
- Modify `internal/session/mutators_test.go`: title mutator tests.
- Modify `cmd/agent-deck/session_cmd.go`: save-before-postCommit ordering and warning.
- Modify `internal/ui/home.go`: run rename post-commit after save and warn on failure.
- Modify `internal/ui/web_mutator.go`: run post-commit after save and return warning-capable errors only where supported.

## Task 1: Append-Only Codex Index Helper

**Files:**
- Create/Modify: `internal/session/codex_index.go`
- Test: `internal/session/codex_index_test.go`

**Interfaces:**
- Produces: `func AppendCodexSessionIndexName(codexHome, sessionID, title string, now time.Time) error`

- [ ] **Step 1: Write failing append tests**

```go
func TestAppendCodexSessionIndexNameCreatesJSONLRecord(t *testing.T) {
	home := t.TempDir()
	id := "55555555-5555-5555-5555-555555555555"
	now := time.Date(2026, 6, 30, 10, 0, 0, 123, time.UTC)
	if err := AppendCodexSessionIndexName(home, id, "renamed", now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID         string `json:"id"`
		ThreadName string `json:"thread_name"`
		UpdatedAt  string `json:"updated_at"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.ThreadName != "renamed" || got.UpdatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("bad record: %#v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session -run TestAppendCodexSessionIndexNameCreatesJSONLRecord`

Expected: fail to compile because `AppendCodexSessionIndexName` does not exist.

- [ ] **Step 3: Implement append helper**

```go
func AppendCodexSessionIndexName(codexHome, sessionID, title string, now time.Time) error {
	sessionID = strings.TrimSpace(sessionID)
	title = strings.TrimSpace(title)
	if sessionID == "" || title == "" {
		return nil
	}
	if !codexSessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("invalid codex session id %q", sessionID)
	}
	if codexHome == "" {
		return fmt.Errorf("codex home is empty")
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	path := filepath.Join(codexHome, "session_index.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	rec := codexIndexLine{
		ID:         strings.ToLower(sessionID),
		ThreadName: title,
		UpdatedAt:  now.UTC().Format(time.RFC3339Nano),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/session -run 'TestAppendCodexSessionIndexName|TestSetFieldTitle'`

Expected: append helper tests pass; title mutator tests unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/session/codex_index.go internal/session/codex_index_test.go
git commit -m "feat(codex): append session index names"
```

## Task 2: SetField Title Post-Commit Wiring

**Files:**
- Modify: `internal/session/mutators.go`
- Modify: `internal/session/mutators_test.go`

**Interfaces:**
- Consumes: `AppendCodexSessionIndexName`
- Produces: `SetField(inst, FieldTitle, value, nil)` returns a non-nil `postCommit` for Codex-compatible sessions with a session ID.

- [ ] **Step 1: Write failing mutator test**

```go
func TestSetFieldTitleCodexReturnsPostCommitIndexSync(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	inst := NewInstanceWithTool("old", t.TempDir(), "codex")
	inst.CodexSessionID = "66666666-6666-6666-6666-666666666666"
	old, postCommit, err := SetField(inst, FieldTitle, "new title", nil)
	if err != nil {
		t.Fatal(err)
	}
	if old != "old" || inst.Title != "new title" {
		t.Fatalf("rename failed old=%q title=%q", old, inst.Title)
	}
	if postCommit == nil {
		t.Fatal("codex title rename should return postCommit")
	}
	postCommit()
	entries, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ThreadName != "new title" {
		t.Fatalf("codex index not synced: %#v", entries)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session -run TestSetFieldTitleCodexReturnsPostCommitIndexSync`

Expected: FAIL because `postCommit` is nil or no index record is appended.

- [ ] **Step 3: Implement post-commit creation**

In `SetField`, inside `case FieldTitle`, after `inst.SyncTmuxDisplayName()`:

```go
if IsCodexCompatible(inst.Tool) && inst.CodexSessionID != "" && strings.TrimSpace(value) != "" {
	codexHome := inst.getCodexHomeDir()
	sessionID := inst.CodexSessionID
	title := inst.Title
	postCommit = func() {
		if err := AppendCodexSessionIndexName(codexHome, sessionID, title, time.Now()); err != nil {
			sessionLog.Warn("codex_session_name_sync_failed",
				slog.String("session_id", sessionID),
				slog.String("title", title),
				slog.String("error", err.Error()))
		}
	}
}
```

If `mutators.go` does not currently import `log/slog`, either add it or use the existing package logger without structured fields. Keep the post-commit non-panicking.

- [ ] **Step 4: Run mutator tests**

Run: `go test ./internal/session -run 'TestSetFieldTitle|TestSetField_CodexSessionID|TestAppendCodex'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/mutators.go internal/session/mutators_test.go
git commit -m "fix(codex): sync saved session name on rename"
```

## Task 3: Save-Before-PostCommit Ordering

**Files:**
- Modify: `cmd/agent-deck/session_cmd.go`
- Modify: `internal/ui/home.go`
- Modify: `internal/ui/web_mutator.go`

**Interfaces:**
- Consumes: `postCommit func()` from `session.SetField`
- Produces: rename paths that persist Agent Deck state before appending Codex index records.

- [ ] **Step 1: Write structural tests for ordering**

Add tests that inspect source or exercise a temp storage path. Structural test is acceptable here because the current handlers are large.

```go
func TestHandleSessionSetSavesBeforePostCommit(t *testing.T) {
	body := readFunctionBody(t, "session_cmd.go", "handleSessionSet")
	saveIdx := strings.Index(body, "storage.SaveWithGroups")
	postIdx := strings.Index(body, "postCommit()")
	if saveIdx < 0 || postIdx < 0 || saveIdx > postIdx {
		t.Fatalf("handleSessionSet must save before postCommit; save=%d post=%d", saveIdx, postIdx)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail on current order**

Run: `go test ./cmd/agent-deck -run TestHandleSessionSetSavesBeforePostCommit`

Expected: FAIL because `postCommit()` currently appears before save.

- [ ] **Step 3: Move CLI postCommit after save and warn on failure shape**

Change the CLI flow to collect `postCommit`, save first, then call it:

```go
oldValue, postCommit, setErr := session.SetField(inst, field, value, extraArgTokens)
// account migration remains before save when field == account
groupTree := session.NewGroupTreeWithGroups(instances, groupsData)
if err := storage.SaveWithGroups(instances, groupTree); err != nil {
	out.Error(fmt.Sprintf("failed to save: %v", err), ErrCodeInvalidOperation)
	os.Exit(1)
}
if postCommit != nil {
	postCommit()
}
```

Because `postCommit` currently has no error return, warnings are logged inside the Codex helper. Do not change the signature in this branch unless all call sites are updated.

- [ ] **Step 4: Move TUI/web postCommits after save**

For edit dialog and group rename paths, preserve the existing "drop `instancesMu` before slow side effects" invariant:

```go
h.instancesMu.Unlock()
h.saveInstances()
for _, fn := range postCommits {
	fn()
}
```

For web mutator, save with storage first, then run `postCommits`.

- [ ] **Step 5: Run focused tests**

Run: `go test ./cmd/agent-deck ./internal/ui ./internal/session -run 'PostCommit|Rename|SetFieldTitleCodex'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/agent-deck/session_cmd.go internal/ui/home.go internal/ui/web_mutator.go
git commit -m "fix(session): run rename side effects after save"
```

## Task 4: Final Verification

- [ ] **Step 1: Run package tests**

Run: `go test ./internal/session ./cmd/agent-deck ./internal/ui ./internal/web`

Expected: PASS.

- [ ] **Step 2: Run full lightweight suite**

Run: `go test ./...`

Expected: PASS, or document any pre-existing unrelated failure with exact package and error.

- [ ] **Step 3: Inspect diff**

Run: `git diff --stat HEAD~3..HEAD`

Expected: only Codex index, mutator, and rename side-effect ordering files changed.
