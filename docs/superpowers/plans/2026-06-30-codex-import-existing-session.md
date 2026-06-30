# Codex Import Existing Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add CLI and TUI workflows that import an existing saved Codex session into Agent Deck.

**Architecture:** Put Codex session-index parsing and resolution in `internal/session` so CLI and TUI share behavior. The CLI creates a persisted stopped Agent Deck row, optionally starts it, and the TUI exposes the same import flow through the existing import hotkey.

**Tech Stack:** Go 1.24+, Bubble Tea TUI, existing Agent Deck `session.Storage`, existing `codex resume <uuid>` launcher.

## Global Constraints

- Preserve Agent Deck's identity rule: rollout files may validate that an ID exists, but automatic disk scans must not silently rebind an unrelated Agent Deck instance.
- Do not parse or rewrite Codex transcript contents.
- Do not require a running Codex process to import an existing saved session.
- Use Conventional Commits.
- Write failing tests before production code.

---

## File Structure

- Create `internal/session/codex_index.go`: Codex index read/list/resolve/append helpers.
- Create `internal/session/codex_index_test.go`: unit tests for index behavior.
- Create `cmd/agent-deck/session_import_codex.go`: CLI handler and import-instance builder.
- Create `cmd/agent-deck/session_import_codex_test.go`: CLI import tests.
- Modify `cmd/agent-deck/session_cmd.go`: route/help for `session import-codex`.
- Create `internal/ui/codex_import_dialog.go`: TUI picker for saved Codex sessions.
- Create `internal/ui/codex_import_dialog_test.go`: dialog unit tests.
- Modify `internal/ui/home.go`: wire import hotkey/dialog into session creation.
- Modify `internal/ui/home_test.go`: TUI import regression tests.

## Task 1: Codex Index Helpers

**Files:**
- Create: `internal/session/codex_index.go`
- Test: `internal/session/codex_index_test.go`

**Interfaces:**
- Produces:
  - `type CodexIndexEntry struct { ID string; ThreadName string; UpdatedAt time.Time }`
  - `func ListCodexIndex(codexHome string) ([]CodexIndexEntry, error)`
  - `func ResolveCodexIndexTarget(codexHome, target string) (CodexIndexEntry, error)`
  - `func CodexRolloutExists(codexHome, sessionID string) bool`

- [ ] **Step 1: Write failing tests for latest-record collapse and resolution**

```go
func TestListCodexIndexLatestRecordWins(t *testing.T) {
	home := t.TempDir()
	writeCodexIndex(t, home,
		`{"id":"11111111-1111-1111-1111-111111111111","thread_name":"old","updated_at":"2026-06-30T09:00:00Z"}`,
		`{"id":"11111111-1111-1111-1111-111111111111","thread_name":"new","updated_at":"2026-06-30T10:00:00Z"}`,
	)
	got, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ThreadName != "new" {
		t.Fatalf("latest record not selected: %#v", got)
	}
}

func TestResolveCodexIndexTargetUUIDAndName(t *testing.T) {
	home := t.TempDir()
	id := "22222222-2222-2222-2222-222222222222"
	writeCodexIndex(t, home, `{"id":"`+id+`","thread_name":"work item","updated_at":"2026-06-30T10:00:00Z"}`)
	writeCodexRollout(t, home, id)
	byID, err := ResolveCodexIndexTarget(home, id)
	if err != nil {
		t.Fatal(err)
	}
	byName, err := ResolveCodexIndexTarget(home, "work item")
	if err != nil {
		t.Fatal(err)
	}
	if byID.ID != id || byName.ID != id {
		t.Fatalf("resolution mismatch byID=%#v byName=%#v", byID, byName)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/session -run 'Test(ListCodexIndex|ResolveCodexIndex)'`

Expected: fail to compile because `ListCodexIndex` and `ResolveCodexIndexTarget` do not exist.

- [ ] **Step 3: Implement minimal index parsing**

```go
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CodexIndexEntry struct {
	ID         string
	ThreadName string
	UpdatedAt  time.Time
}

type codexIndexLine struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

func ListCodexIndex(codexHome string) ([]CodexIndexEntry, error) {
	path := filepath.Join(codexHome, "session_index.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	latest := map[string]CodexIndexEntry{}
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw codexIndexLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", path, lineNo, err)
		}
		ts, err := time.Parse(time.RFC3339Nano, raw.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse %s line %d updated_at: %w", path, lineNo, err)
		}
		entry := CodexIndexEntry{ID: strings.TrimSpace(raw.ID), ThreadName: raw.ThreadName, UpdatedAt: ts}
		if entry.ID == "" {
			continue
		}
		if prev, ok := latest[entry.ID]; !ok || entry.UpdatedAt.After(prev.UpdatedAt) {
			latest[entry.ID] = entry
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make([]CodexIndexEntry, 0, len(latest))
	for _, entry := range latest {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}
```

- [ ] **Step 4: Implement target resolution and rollout validation**

```go
var (
	ErrCodexSessionNotFound  = errors.New("codex session not found")
	ErrCodexSessionAmbiguous = errors.New("codex session name is ambiguous")
	ErrCodexRolloutMissing   = errors.New("codex rollout file missing")
)

func ResolveCodexIndexTarget(codexHome, target string) (CodexIndexEntry, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return CodexIndexEntry{}, ErrCodexSessionNotFound
	}
	entries, err := ListCodexIndex(codexHome)
	if err != nil {
		return CodexIndexEntry{}, err
	}
	if codexSessionIDPattern.MatchString(target) {
		for _, entry := range entries {
			if strings.EqualFold(entry.ID, target) {
				if !CodexRolloutExists(codexHome, entry.ID) {
					return CodexIndexEntry{}, ErrCodexRolloutMissing
				}
				return entry, nil
			}
		}
		if CodexRolloutExists(codexHome, target) {
			return CodexIndexEntry{ID: strings.ToLower(target)}, nil
		}
		return CodexIndexEntry{}, ErrCodexSessionNotFound
	}
	var matches []CodexIndexEntry
	for _, entry := range entries {
		if entry.ThreadName == target {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 0 {
		return CodexIndexEntry{}, ErrCodexSessionNotFound
	}
	if len(matches) > 1 {
		return CodexIndexEntry{}, ErrCodexSessionAmbiguous
	}
	if !CodexRolloutExists(codexHome, matches[0].ID) {
		return CodexIndexEntry{}, ErrCodexRolloutMissing
	}
	return matches[0], nil
}

func CodexRolloutExists(codexHome, sessionID string) bool {
	return codexRolloutExistsInHome(sessionID, codexHome)
}
```

- [ ] **Step 5: Run tests to verify green**

Run: `go test ./internal/session -run 'Test(ListCodexIndex|ResolveCodexIndex|Codex)'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/session/codex_index.go internal/session/codex_index_test.go
git commit -m "feat(codex): add session index resolver"
```

## Task 2: CLI Import Command

**Files:**
- Create: `cmd/agent-deck/session_import_codex.go`
- Modify: `cmd/agent-deck/session_cmd.go`
- Test: `cmd/agent-deck/session_import_codex_test.go`

**Interfaces:**
- Consumes: `session.ResolveCodexIndexTarget`, `session.CodexIndexEntry`
- Produces: `func handleSessionImportCodex(profile string, args []string)`

- [ ] **Step 1: Write failing CLI import tests**

```go
func TestHandleSessionImportCodexCreatesStoppedSession(t *testing.T) {
	profile := uniqueTestProfile(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	id := "33333333-3333-3333-3333-333333333333"
	writeCodexIndexForCLI(t, home, id, "existing codex", "2026-06-30T10:00:00Z")
	writeCodexRolloutForCLI(t, home, id)

	handleSessionImportCodex(profile, []string{id, "--title", "imported", "--path", t.TempDir(), "--quiet"})

	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances=%d, want 1", len(instances))
	}
	got := instances[0]
	if got.Tool != "codex" || got.CodexSessionID != id || got.Title != "imported" || got.Status != session.StatusStopped {
		t.Fatalf("bad imported instance: %#v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/agent-deck -run TestHandleSessionImportCodexCreatesStoppedSession`

Expected: fail to compile because `handleSessionImportCodex` does not exist.

- [ ] **Step 3: Implement command handler**

```go
func handleSessionImportCodex(profile string, args []string) {
	fs := flag.NewFlagSet("session import-codex", flag.ExitOnError)
	title := fs.String("title", "", "Agent Deck title")
	titleShort := fs.String("t", "", "Agent Deck title")
	group := fs.String("group", session.DefaultGroupPath, "Group path")
	groupShort := fs.String("g", "", "Group path")
	pathFlag := fs.String("path", "", "Project path")
	command := fs.String("command", "codex", "Codex command")
	commandShort := fs.String("c", "", "Codex command")
	start := fs.Bool("start", false, "Start after import")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	quiet := fs.Bool("quiet", false, "Minimal output")
	quietShort := fs.Bool("q", false, "Minimal output")
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		os.Exit(1)
	}
	out := NewCLIOutput(*jsonOutput, *quiet || *quietShort)
	if fs.NArg() != 1 {
		out.Error("usage: agent-deck session import-codex <session-id-or-name>", ErrCodeInvalidOperation)
		os.Exit(1)
	}
	target := fs.Arg(0)
	codexHome := session.GetCodexHomeDir()
	entry, err := session.ResolveCodexIndexTarget(codexHome, target)
	if err != nil {
		out.Error(fmt.Sprintf("failed to resolve codex session: %v", err), ErrCodeInvalidOperation)
		os.Exit(1)
	}
	projectPath := *pathFlag
	if projectPath == "" {
		projectPath, _ = os.Getwd()
	}
	resolvedTitle := mergeFlags(*title, *titleShort)
	if resolvedTitle == "" {
		resolvedTitle = entry.ThreadName
	}
	if resolvedTitle == "" {
		resolvedTitle = entry.ID[:8]
	}
	resolvedGroup := mergeFlags(*group, *groupShort)
	resolvedCommand := mergeFlags(*command, *commandShort)
	inst := session.NewInstanceWithGroupAndTool(resolvedTitle, projectPath, resolvedGroup, "codex")
	inst.Command = resolvedCommand
	inst.CodexSessionID = entry.ID
	inst.CodexDetectedAt = entry.UpdatedAt
	inst.Status = session.StatusStopped
	if *start {
		if err := inst.Start(); err != nil {
			out.Error(fmt.Sprintf("failed to start imported codex session: %v", err), ErrCodeInvalidOperation)
			os.Exit(1)
		}
	}
	storage, instances, groups, err := loadSessionData(profile)
	if err != nil {
		out.Error(err.Error(), ErrCodeNotFound)
		os.Exit(1)
	}
	instances = append(instances, inst)
	if err := saveSessionData(storage, instances, groups); err != nil {
		out.Error(fmt.Sprintf("failed to save imported session: %v", err), ErrCodeInvalidOperation)
		os.Exit(1)
	}
	out.Success("Imported Codex session: "+inst.Title, map[string]interface{}{
		"success": true, "id": inst.ID, "title": inst.Title, "codexSessionId": inst.CodexSessionID,
	})
}
```

- [ ] **Step 4: Wire dispatch and help**

Add case in `handleSession`:

```go
case "import-codex":
	handleSessionImportCodex(profile, args[1:])
```

Add help line:

```go
fmt.Println("  import-codex <id|name> Import an existing saved Codex session")
```

- [ ] **Step 5: Run CLI tests**

Run: `go test ./cmd/agent-deck -run 'TestHandleSessionImportCodex|TestSessionHelp'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/agent-deck/session_cmd.go cmd/agent-deck/session_import_codex.go cmd/agent-deck/session_import_codex_test.go
git commit -m "feat(codex): import existing sessions from cli"
```

## Task 3: TUI Import Dialog

**Files:**
- Create: `internal/ui/codex_import_dialog.go`
- Create: `internal/ui/codex_import_dialog_test.go`
- Modify: `internal/ui/home.go`
- Modify: `internal/ui/home_test.go`

**Interfaces:**
- Consumes: `session.ListCodexIndex`, `session.CodexIndexEntry`
- Produces:
  - `type CodexImportDialog`
  - `func NewCodexImportDialog() *CodexImportDialog`
  - `func (d *CodexImportDialog) Show(entries []session.CodexIndexEntry)`
  - `func (d *CodexImportDialog) Selected() (session.CodexIndexEntry, bool)`

- [ ] **Step 1: Write failing dialog tests**

```go
func TestCodexImportDialogSelectsEntry(t *testing.T) {
	d := NewCodexImportDialog()
	entries := []session.CodexIndexEntry{{ID: "44444444-4444-4444-4444-444444444444", ThreadName: "alpha", UpdatedAt: time.Now()}}
	d.Show(entries)
	if !d.Visible() {
		t.Fatal("dialog should be visible")
	}
	model, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = model
	if cmd == nil {
		t.Fatal("enter should submit selected import")
	}
	got, ok := d.Selected()
	if !ok || got.ID != entries[0].ID {
		t.Fatalf("selected = %#v ok=%v", got, ok)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui -run TestCodexImportDialogSelectsEntry`

Expected: fail to compile because `CodexImportDialog` does not exist.

- [ ] **Step 3: Implement dialog**

Use the existing list-style patterns from `McpDialog`/`SkillDialog`: visible flag, cursor, filtered rows, `up/down/k/j`, `enter`, and `esc`.

```go
type CodexImportDialog struct {
	visible  bool
	entries  []session.CodexIndexEntry
	cursor   int
	selected *session.CodexIndexEntry
	width    int
	height   int
}
```

The `View` rows should render: title, short ID, and `UpdatedAt.Local().Format("2006-01-02 15:04")`.

- [ ] **Step 4: Wire the existing import key to an import chooser**

Change `case "i": return h, h.importSessions` into an import dialog path:

```go
case "i":
	return h, h.openImportDialog
```

`openImportDialog` should discover tmux sessions as the legacy behavior and list Codex index entries. It can show two source rows first:

- `Existing tmux sessions`
- `Saved Codex sessions`

Selecting tmux runs the existing `importSessions` logic. Selecting Codex opens `CodexImportDialog`.

- [ ] **Step 5: Create imported session from selected Codex entry**

Add a helper:

```go
func (h *Home) createSessionFromCodexImport(entry session.CodexIndexEntry) tea.Cmd {
	return func() tea.Msg {
		title := strings.TrimSpace(entry.ThreadName)
		if title == "" {
			title = entry.ID[:8]
		}
		projectPath := "."
		if cwd, err := os.Getwd(); err == nil {
			projectPath = cwd
		}
		inst := session.NewInstanceWithGroupAndTool(title, projectPath, h.resolveNewSessionGroup(), "codex")
		inst.Command = "codex"
		inst.CodexSessionID = entry.ID
		inst.CodexDetectedAt = entry.UpdatedAt
		inst.Status = session.StatusStopped
		return sessionCreatedMsg{instance: inst}
	}
}
```

- [ ] **Step 6: Run focused TUI tests**

Run: `go test ./internal/ui -run 'TestCodexImportDialog|TestHome.*CodexImport|TestRenderSessionListEmptyUsesConfiguredKeys'`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/codex_import_dialog.go internal/ui/codex_import_dialog_test.go internal/ui/home.go internal/ui/home_test.go
git commit -m "feat(codex): add tui import for saved sessions"
```

## Task 4: Final Verification

**Files:**
- Modify: `README.md` if CLI/TUI shortcut docs need a small update.

- [ ] **Step 1: Run package tests**

Run: `go test ./internal/session ./cmd/agent-deck ./internal/ui`

Expected: PASS.

- [ ] **Step 2: Run full lightweight suite**

Run: `go test ./...`

Expected: PASS, or document any pre-existing unrelated failure with exact package and error.

- [ ] **Step 3: Commit docs if changed**

```bash
git add README.md
git commit -m "docs(codex): document session import"
```

Skip this commit if README did not change.
