# Kiro CLI Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `kiro` as a first-class Agent Deck provider with tmux-backed launch/resume, persisted Kiro session IDs, and CLI/TUI saved-session import.

**Architecture:** Follow the existing Codex/OpenCode pattern. Put Kiro saved-session metadata parsing and import-instance construction in `internal/session`, keep CLI and TUI thin, and wire `kiro` into built-in tool registries, command builders, restart paths, and persistence.

**Tech Stack:** Go, Bubble Tea/Lip Gloss TUI, SQLite `tool_data`, tmux sessions, Kiro CLI saved metadata under `~/.kiro/sessions/cli`.

## Global Constraints

- Add `kiro` as a built-in first-class Agent Deck tool.
- Default command is `kiro-cli chat --tui`.
- Resume command is `kiro-cli chat --resume-id <session-id> --tui`.
- Do not implement `kiro-cli acp` integration in this first version.
- Do not implement Kiro headless automation in this first version.
- Do not rewrite Kiro transcript JSONL files.
- TUI parity is mandatory for import and built-in tool selection.
- Saved-session import reads Kiro metadata JSON under `~/.kiro/sessions/cli/*.json`.
- Imported Kiro sessions default path and group from saved session `cwd`.
- Duplicate Kiro imports by `KiroSessionID` must be rejected.
- Follow TDD: write a failing test, verify it fails, implement minimal code, verify it passes.
- Baseline `go test ./...` failed before implementation because inotify could not initialize with `too many open files`; run targeted tests and report the baseline failure.

---

## File Structure

- Create `internal/session/kiro_sessions.go`: Kiro sessions directory resolution, metadata parsing, listing, target resolution, duplicate-safe imported instance construction.
- Create `internal/session/kiro_sessions_test.go`: Kiro saved-session parsing, sorting, resolution, malformed files, and imported instance defaults.
- Modify `internal/session/tooloptions.go` and `internal/session/tooloptions_test.go`: `KiroOptions`, JSON wrapper support, config defaults, CLI flag rendering.
- Modify `internal/session/userconfig.go`: add `[kiro]` config, built-in command lookup, default command helpers.
- Modify `internal/session/builtins.go`: add Kiro to the canonical built-in registry and command detection.
- Modify `internal/session/instance.go`: add Kiro state fields, command builder, start/restart routing, tmux env sync, display ID, launch model support, and fresh-start clearing.
- Modify `internal/session/storage.go`: include Kiro fields in JSON `InstanceData`, `instanceToRow`, and DB load conversion.
- Modify `internal/statedb/migrate.go`: add Kiro fields to `toolDataBlob`, `MarshalToolData`, and `UnmarshalToolData`.
- Modify `internal/statedb/statedb.go`: add targeted `WriteKiroSessionBinding` only if Kiro async detection is added in this branch; otherwise keep Kiro persistence on normal save.
- Modify `internal/tmux/patterns.go`: add conservative raw detection/status patterns for `kiro`.
- Modify `cmd/agent-deck/session_import_kiro.go` and `cmd/agent-deck/session_import_kiro_test.go`: CLI import handler and tests.
- Modify `cmd/agent-deck/session_cmd.go`: route and help for `session import-kiro`.
- Create `internal/ui/kiro_import_dialog.go` and tests: saved Kiro picker with `/` search, path detail, bounds-safe rendering.
- Modify `internal/ui/import_source_dialog.go`: saved Kiro source count and row.
- Modify `internal/ui/home.go` and `internal/ui/home_test.go`: load Kiro entries, open picker, create imported Kiro sessions, reject duplicates.
- Modify `internal/ui/settings_panel.go`, `internal/ui/styles.go`, and new-session tool list files: show Kiro in TUI built-in selectors.
- Modify web/static fixed tool selector files if `rg` finds hard-coded built-in lists under `internal/web`, `site`, or `assets`.

## Task 1: Kiro Saved-Session Helpers

**Files:**
- Create: `internal/session/kiro_sessions.go`
- Create: `internal/session/kiro_sessions_test.go`

**Interfaces:**
- Produces:
  - `type KiroSavedSession struct { ID, Title, CWD, AgentName string; CreatedAt, UpdatedAt time.Time }`
  - `var ErrKiroSessionNotFound error`
  - `var ErrKiroSessionAmbiguous error`
  - `type KiroSessionAmbiguousError struct { Target string; Matches []KiroSavedSession }`
  - `func KiroSessionsDir() string`
  - `func ListKiroSavedSessions(dir string) ([]KiroSavedSession, error)`
  - `func ResolveKiroSavedSession(dir, target string) (KiroSavedSession, error)`
  - `func NewKiroImportedInstance(entry KiroSavedSession, opts KiroImportOptions) (*Instance, error)`

- [ ] **Step 1: Write failing metadata tests**

Add tests that create temporary `*.json` files shaped like Kiro metadata:

```go
func TestListKiroSavedSessionsSortsByUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	writeKiroSessionFile(t, dir, "old.json", `{"session_id":"37a7454c-f9d3-434a-bd7e-03318ef6b72a","cwd":"/repo/old","title":"old","created_at":"2026-05-08T05:27:02.624242907Z","updated_at":"2026-05-08T06:31:51.570257120Z","session_state":{"agent_name":"kiro_default"}}`)
	writeKiroSessionFile(t, dir, "new.json", `{"session_id":"75e59a16-9f76-433d-baa3-3cb8e5ef4c5d","cwd":"/repo/new","title":"new","created_at":"2026-05-09T05:27:02Z","updated_at":"2026-05-09T06:31:51Z","session_state":{"agent_name":"planner"}}`)

	entries, err := ListKiroSavedSessions(dir)
	if err != nil {
		t.Fatalf("ListKiroSavedSessions: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Title != "new" || entries[0].AgentName != "planner" || entries[0].CWD != "/repo/new" {
		t.Fatalf("newest entry not first or malformed: %+v", entries[0])
	}
}
```

- [ ] **Step 2: Verify red**

Run: `go test ./internal/session -run 'TestListKiroSavedSessions|TestResolveKiro|TestNewKiroImportedInstance'`

Expected: fail to compile because Kiro helper symbols do not exist.

- [ ] **Step 3: Implement helpers**

Create `internal/session/kiro_sessions.go` using `os.UserHomeDir`, `filepath.Glob`, `encoding/json`, `time.Parse(time.RFC3339Nano)`, `sort.Slice`, and the existing `firstNonEmpty` helper. `ListKiroSavedSessions` returns nil on missing directory, skips non-UUID session IDs, and includes file path in parse errors.

- [ ] **Step 4: Add resolution/import tests**

Cover UUID first, exact title, unambiguous case-insensitive title, ambiguous title error, missing directory, malformed selected JSON, and imported defaults:

```go
func TestNewKiroImportedInstanceUsesCWDPathAndGroup(t *testing.T) {
	updated := time.Date(2026, 7, 3, 10, 30, 0, 0, time.UTC)
	inst, err := NewKiroImportedInstance(KiroSavedSession{
		ID: "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d", Title: "github import", CWD: "/home/lrasmussen/git/domutech/domutech-github", UpdatedAt: updated,
	}, KiroImportOptions{Command: "kiro-cli chat --tui"})
	if err != nil {
		t.Fatalf("NewKiroImportedInstance: %v", err)
	}
	if inst.Tool != "kiro" || inst.Command != "kiro-cli chat --tui" || inst.ProjectPath != "/home/lrasmussen/git/domutech/domutech-github" {
		t.Fatalf("unexpected imported instance: %+v", inst)
	}
	if inst.KiroSessionID != "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d" || !inst.KiroDetectedAt.Equal(updated) {
		t.Fatalf("kiro binding not copied: %q %v", inst.KiroSessionID, inst.KiroDetectedAt)
	}
}
```

- [ ] **Step 5: Verify green**

Run: `go test ./internal/session -run 'TestListKiroSavedSessions|TestResolveKiro|TestNewKiroImportedInstance'`

Expected: pass.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/session/kiro_sessions.go internal/session/kiro_sessions_test.go
git commit -m "feat(kiro): read saved cli sessions"
```

## Task 2: Kiro Options, Built-In Registry, And Command Builder

**Files:**
- Modify: `internal/session/tooloptions.go`
- Modify: `internal/session/tooloptions_test.go`
- Modify: `internal/session/userconfig.go`
- Modify: `internal/session/builtins.go`
- Modify: `internal/session/instance.go`
- Modify: `internal/tmux/patterns.go`

**Interfaces:**
- Consumes: `KiroSavedSession` and `KiroImportOptions` from Task 1.
- Produces:
  - `type KiroSettings struct { Command, DefaultAgent, DefaultModel string; TrustAllTools bool; TrustTools []string }`
  - `type KiroOptions struct { Agent, Model string; TrustAllTools bool; TrustTools []string }`
  - `func NewKiroOptions(config *UserConfig) *KiroOptions`
  - `func UnmarshalKiroOptions(data json.RawMessage) (*KiroOptions, error)`
  - `func (i *Instance) GetKiroOptions() *KiroOptions`
  - `func (i *Instance) SetKiroOptions(opts *KiroOptions) error`
  - `func (i *Instance) buildKiroCommand(baseCommand string) string`

- [ ] **Step 1: Write failing option and command tests**

Add tests:

```go
func TestKiroOptionsToArgs(t *testing.T) {
	opts := &KiroOptions{Agent: "kiro_default", Model: "claude-sonnet-4", TrustAllTools: true, TrustTools: []string{"shell", "git"}}
	got := strings.Join(opts.ToArgs(), " ")
	want := "--agent kiro_default --model claude-sonnet-4 --trust-all-tools --trust-tools shell --trust-tools git"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestBuildKiroCommandResume(t *testing.T) {
	inst := &Instance{Tool: "kiro", Command: "kiro-cli chat --tui", KiroSessionID: "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d"}
	if err := inst.SetKiroOptions(&KiroOptions{Agent: "planner", Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	got := inst.buildKiroCommand(inst.Command)
	for _, want := range []string{"kiro-cli chat", "--resume-id 75e59a16-9f76-433d-baa3-3cb8e5ef4c5d", "--tui", "--agent planner", "--model sonnet"} {
		if !strings.Contains(got, want) {
			t.Fatalf("buildKiroCommand missing %q in %q", want, got)
		}
	}
}
```

- [ ] **Step 2: Verify red**

Run: `go test ./internal/session -run 'TestKiroOptions|TestBuildKiroCommand|TestBuiltin.*Kiro|TestDefaultRawPatterns.*Kiro'`

Expected: fail to compile because `KiroOptions` and `buildKiroCommand` do not exist.

- [ ] **Step 3: Implement registry/config/options**

Add `Kiro KiroSettings` to `UserConfig`, implement `GetKiroCommand()` and `GetToolCommand("kiro")`, add `{Name: "kiro", Icon: "K", detectSubstrings: []string{"kiro-cli"}}` to `builtinTools()`, and add Kiro option marshal/unmarshal wrappers matching Codex/OpenCode style.

- [ ] **Step 4: Implement command builder and raw patterns**

In `Instance.buildKiroCommand`, normalize supported commands to `kiro-cli chat`, emit `--resume-id <id>` before `--tui`, append option args, and return custom non-Kiro commands unchanged. Add `DefaultRawPatterns("kiro")` with conservative prompt patterns such as `">"`, `"How can I help"`, and `"kiro"`.

- [ ] **Step 5: Wire Start/Restart routing minimally**

In `Start()` command selection, call `buildKiroCommand` for `Tool=="kiro"`. In `Restart()`, add a Kiro respawn branch parallel to OpenCode: recover `KIRO_SESSION_ID` from tmux, build Kiro command, respawn pane, sync env, anchor session ID when present, set waiting. Add Kiro to `CanRestart`, `CanRestartFresh`, `clearSessionBindingForFreshStart`, `DisplaySessionID`, `SyncSessionIDsToTmux`, and `SyncSessionIDsFromTmux`.

- [ ] **Step 6: Verify green**

Run: `go test ./internal/session ./internal/tmux -run 'TestKiroOptions|TestBuildKiroCommand|TestBuiltin.*Kiro|TestDefaultRawPatterns.*Kiro|TestDisplaySessionID|TestCanRestart'`

Expected: pass.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/session/tooloptions.go internal/session/tooloptions_test.go internal/session/userconfig.go internal/session/builtins.go internal/session/instance.go internal/tmux/patterns.go
git commit -m "feat(kiro): add built-in launch support"
```

## Task 3: Persist Kiro Session State

**Files:**
- Modify: `internal/session/storage.go`
- Modify: `internal/statedb/migrate.go`
- Modify: `internal/session/session_persistence_test.go`
- Modify: `internal/statedb/statedb_test.go`

**Interfaces:**
- Consumes: `Instance.KiroSessionID` and `Instance.KiroDetectedAt`.
- Produces: JSON and SQLite round trip for `kiro_session_id` and `kiro_detected_at`.

`Instance.KiroStartedAt` is runtime-only future matching state (`json:"-"`) and is not persisted.

- [ ] **Step 1: Write failing persistence tests**

Add one `internal/session` persistence test that saves an instance with `Tool: "kiro"`, `KiroSessionID`, and `KiroDetectedAt`, reloads it, and asserts both fields. Add one `internal/statedb` test that `MarshalToolData` emits `kiro_session_id` and `UnmarshalToolData` reads it back.

- [ ] **Step 2: Verify red**

Run: `go test ./internal/session ./internal/statedb -run 'Test.*Kiro.*Persist|Test.*Kiro.*ToolData'`

Expected: fail because Kiro fields are not in storage/tool-data conversion.

- [ ] **Step 3: Implement storage fields**

Add Kiro fields to `InstanceData`, `Instance`, `instanceToRow`, `convertToInstances`, and `LoadWithGroups` conversion paths. Update `statedb.toolDataBlob`, `MarshalToolData`, and `UnmarshalToolData` signatures and call sites to include Kiro immediately after Codex fields.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/session ./internal/statedb -run 'Test.*Kiro.*Persist|Test.*Kiro.*ToolData|TestSessionPersistence|TestToolData'`

Expected: pass.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/session/storage.go internal/session/session_persistence_test.go internal/statedb/migrate.go internal/statedb/statedb_test.go
git commit -m "feat(kiro): persist session bindings"
```

## Task 4: CLI Import

**Files:**
- Create: `cmd/agent-deck/session_import_kiro.go`
- Create: `cmd/agent-deck/session_import_kiro_test.go`
- Modify: `cmd/agent-deck/session_cmd.go`

**Interfaces:**
- Consumes: `ResolveKiroSavedSession`, `NewKiroImportedInstance`, `KiroImportOptions`.
- Produces: `agent-deck session import-kiro <session-id-or-title>`.

- [ ] **Step 1: Write failing CLI tests**

Mirror OpenCode import tests for:

- successful stopped import by title
- JSON output includes `kiro_session_id`
- duplicate `KiroSessionID` rejected
- session help includes `import-kiro`

- [ ] **Step 2: Verify red**

Run: `go test ./cmd/agent-deck -run 'TestHandleSessionImportKiro|TestSessionHelpIncludesKiro'`

Expected: fail because handler and route do not exist.

- [ ] **Step 3: Implement handler**

Implement `handleSessionImportKiro(profile string, args []string)` with flags matching the design. Use `loadSessionData`, `session.ResolveKiroSavedSession`, `session.NewKiroImportedInstance`, `isDuplicateSession`, and `findKiroImportSessionIDConflict`. Save via `storage.InsertSessionAndVerify`; if `--start`, call `Start()` and `PostStartSync`.

- [ ] **Step 4: Wire command**

Add `case "import-kiro": handleSessionImportKiro(profile, args[1:])` and update help/examples in `session_cmd.go`.

- [ ] **Step 5: Verify green**

Run: `go test ./cmd/agent-deck -run 'TestHandleSessionImportKiro|TestSessionHelp'`

Expected: pass.

- [ ] **Step 6: Commit**

Run:

```bash
git add cmd/agent-deck/session_import_kiro.go cmd/agent-deck/session_import_kiro_test.go cmd/agent-deck/session_cmd.go
git commit -m "feat(kiro): import saved sessions from cli"
```

## Task 5: TUI Import Picker

**Files:**
- Create: `internal/ui/kiro_import_dialog.go`
- Create: `internal/ui/kiro_import_dialog_test.go`
- Modify: `internal/ui/import_source_dialog.go`
- Modify: `internal/ui/home.go`
- Modify: `internal/ui/home_test.go`
- Modify: `internal/ui/import_dialog_scroll_test.go`

**Interfaces:**
- Consumes: `session.KiroSavedSession`.
- Produces:
  - `type KiroImportDialog`
  - `func NewKiroImportDialog() *KiroImportDialog`
  - `func (d *KiroImportDialog) Show(entries []session.KiroSavedSession)`
  - `func (d *KiroImportDialog) Selected() (session.KiroSavedSession, bool)`

- [ ] **Step 1: Write failing dialog tests**

Test that `/` search stays open, searches path/title/agent/id, renders selected path detail, and scrolls within bounds with 12 entries, mirroring `import_dialog_scroll_test.go`.

- [ ] **Step 2: Verify red**

Run: `go test ./internal/ui -run 'TestKiroImportDialog|TestImportSourceDialog.*Kiro|TestHome.*KiroImport|TestImportDialogScroll.*Kiro'`

Expected: fail because Kiro dialog/source fields do not exist.

- [ ] **Step 3: Implement dialog**

Use `codex_import_dialog.go` and `opencode_import_dialog.go` as templates. Row format:

```text
> <title>  <short-id>  <updated-at>  <agent>  <cwd>
```

Search fields: title, full ID, short ID, agent name, CWD. Append a selected-row detail line with full CWD, truncated only by dialog width.

- [ ] **Step 4: Wire import source and home flow**

Add `Kiro int` to `ImportSourceCounts`, `importSourceKiro` enum, source row, `KiroCount()`, `kiroImportDialog`, entries/error fields, load entries in `openImportDialog`, show Kiro picker on source selection, handle Kiro picker key events, and create imported instances through `session.NewKiroImportedInstance`. Reject duplicate `KiroSessionID` before returning `sessionCreatedMsg`.

- [ ] **Step 5: Ensure modal routing and resizing**

Add Kiro dialog checks wherever Codex/OpenCode dialogs are checked in `Update`, key dispatch, and modal `View()`. Add `SetSize` calls in resize handling.

- [ ] **Step 6: Verify green**

Run: `go test ./internal/ui -run 'TestKiroImportDialog|TestImportSourceDialog|TestHome.*Import|TestImportDialogScroll'`

Expected: pass, except unrelated inotify global-search tests are not part of this run.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/ui/kiro_import_dialog.go internal/ui/kiro_import_dialog_test.go internal/ui/import_source_dialog.go internal/ui/home.go internal/ui/home_test.go internal/ui/import_dialog_scroll_test.go
git commit -m "feat(kiro): add tui import picker"
```

## Task 6: Built-In Tool Selector Surfaces

**Files:**
- Modify: `internal/ui/settings_panel.go`
- Modify: `internal/ui/styles.go`
- Modify: TUI new-session picker files found by `rg -n '"codex"|"opencode"|builtin' internal/ui`
- Modify: web/static selector files found by `rg -n '"codex"|"opencode"|tool' internal/web site assets`

**Interfaces:**
- Consumes: built-in `kiro` registry entry.
- Produces: Kiro visible as a built-in in settings, new-session dialogs, row styles, and web/static create-session selectors when lists are hard-coded.

- [ ] **Step 1: Write failing selector tests**

Add focused tests for settings/new-session fixed lists that assert `kiro` is present. If a surface is render-only without test hooks, add the smallest helper returning the built-in list and test that helper.

- [ ] **Step 2: Verify red**

Run: `go test ./internal/ui ./internal/web -run 'Test.*Kiro.*Tool|Test.*BuiltIn.*Kiro|Test.*Settings.*Kiro'`

Expected: fail because fixed UI lists omit Kiro.

- [ ] **Step 3: Implement selector/style changes**

Add Kiro to fixed lists, icon/color switches, and any command placeholders. Use icon `K` and a distinct color that does not collapse into the existing Codex/OpenCode styles.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/ui ./internal/web -run 'Test.*Kiro.*Tool|Test.*BuiltIn.*Kiro|Test.*Settings.*Kiro'`

Expected: pass.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/ui/settings_panel.go internal/ui/styles.go internal/ui internal/web site assets
git commit -m "feat(kiro): expose built-in tool in ui"
```

## Task 7: Final Verification And Integration

**Files:**
- All modified files.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: verified Kiro first-class support branch.

- [ ] **Step 1: Run targeted package tests**

Run:

```bash
go test ./internal/session ./internal/statedb ./internal/tmux ./cmd/agent-deck ./internal/ui ./internal/web
```

Expected: pass, unless the pre-existing inotify watcher limit recurs. If it recurs, rerun narrower Kiro-related tests and report the environmental full-package failure.

- [ ] **Step 2: Build binary**

Run:

```bash
go build ./cmd/agent-deck
```

Expected: pass.

- [ ] **Step 3: Manual smoke commands**

Run:

```bash
./agent-deck session import-kiro --help
```

Expected: shows import usage and exits 0.

Run:

```bash
./agent-deck --help
```

Expected: exits 0.

- [ ] **Step 4: Inspect diff**

Run:

```bash
git status --short
git diff --stat HEAD
```

Expected: only Kiro support files changed.

- [ ] **Step 5: Final commit if needed**

If verification fixes were made, commit:

```bash
git add <changed-files>
git commit -m "test(kiro): verify first-class support"
```

## Self-Review

- Spec coverage: first-class provider, command/resume, saved-session import, TUI parity, duplicate rejection, cwd grouping, and no ACP/headless are covered by Tasks 1-6.
- Placeholder scan completed: no task contains unfinished-marker wording.
- Type consistency: `KiroSavedSession`, `KiroImportOptions`, `KiroOptions`, `KiroSessionID`, and `KiroDetectedAt` are used consistently across tasks.
