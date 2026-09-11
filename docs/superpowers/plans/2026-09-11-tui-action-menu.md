# TUI Action Menu Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Agent Deck's shortcut-first overview with an always-available global action menu and individually configurable optional shortcuts.

**Architecture:** A declarative action catalog supplies stable action IDs, labels, categories, default accelerators, and availability. The global menu, shortcut editor, key dispatcher, help, and footer all consume that catalog; menu invocation calls action IDs directly, so disabling a shortcut never disables the operation itself.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, BurntSushi TOML configuration, and existing `internal/ui` test seams.

**Spec:** `docs/superpowers/specs/2026-09-11-tui-action-menu-design.md`

## Global Constraints

- `Space` opens the global menu anywhere in the overview, including when no row is selected.
- Arrow keys, Enter, Escape, Space, and the attached-session detach chord are structural controls and cannot be disabled.
- Optional shortcuts default to disabled in menu-first mode.
- Explicit non-empty `[hotkeys]` entries stay enabled; explicit empty entries stay disabled.
- Menu actions re-check availability immediately before dispatch.
- Destructive operations retain their existing confirmation dialogs.
- No new dependency is permitted.
- Configuration saves must preserve every unrelated `config.toml` section.

---

### Task 1: Shortcut mode and action metadata

**Files:**
- Create: `internal/ui/action_catalog.go`
- Create: `internal/ui/action_catalog_test.go`
- Modify: `internal/ui/hotkeys.go`
- Modify: `internal/ui/hotkeys_test.go`
- Modify: `internal/session/userconfig.go`
- Test: `internal/session/userconfig_test.go`

**Interfaces:**
- Produces: `type ActionID string`
- Produces: `type ActionCategory string`
- Produces: `type ActionDefinition struct { ID ActionID; Label string; Category ActionCategory; DefaultKey string; Optional bool }`
- Produces: `func actionDefinitions() []ActionDefinition`
- Produces: `func resolveHotkeysForMode(overrides map[string]string, mode string) map[string]string`
- Produces: `func validateHotkeyBindings(bindings map[string]string) error`
- Consumes: existing `defaultHotkeyBindings`, action ordering, and alias normalization.

- [ ] **Step 1: Write the failing catalog and menu-first resolution tests**

```go
func TestResolveHotkeysForMode_MenuFirstOnlyEnablesExplicitBindings(t *testing.T) {
    got := resolveHotkeysForMode(map[string]string{"rename": "ctrl+r", "delete": ""}, "menu")
    if got["rename"] != "ctrl+r" { t.Fatalf("rename = %q", got["rename"]) }
    if _, ok := got["delete"]; ok { t.Fatal("delete must remain disabled") }
    if _, ok := got["restart"]; ok { t.Fatal("implicit restart must be disabled") }
}

func TestActionDefinitionsHaveUniqueStableIDs(t *testing.T) {
    seen := map[ActionID]bool{}
    for _, action := range actionDefinitions() {
        if action.ID == "" || seen[action.ID] { t.Fatalf("invalid action ID %q", action.ID) }
        seen[action.ID] = true
    }
}
```

- [ ] **Step 2: Run tests and verify missing-symbol failures**

Run: `go test ./internal/ui ./internal/session -run 'TestResolveHotkeysForMode|TestActionDefinitions' -count=1`

Expected: build failure naming `resolveHotkeysForMode`, `ActionID`, or `actionDefinitions`.

- [ ] **Step 3: Add the catalog and menu/legacy resolver**

```go
type ActionID string
type ActionCategory string

const (
    ActionCategorySelected ActionCategory = "selected"
    ActionCategorySessions ActionCategory = "sessions"
    ActionCategoryManage   ActionCategory = "manage"
    ActionCategoryView     ActionCategory = "view"
    ActionCategoryApp      ActionCategory = "application"
)

func resolveHotkeysForMode(overrides map[string]string, mode string) map[string]string {
    if strings.EqualFold(strings.TrimSpace(mode), "legacy") {
        return resolveHotkeys(overrides)
    }
    return resolveExplicitHotkeys(overrides)
}
```

Add `ShortcutMode string` with TOML name `shortcut_mode` to `UISettings`; only `menu` and `legacy` resolve, with empty resolving to `menu`.

- [ ] **Step 4: Add collision and unsupported-key tests, then implement validation**

```go
func TestValidateHotkeyBindingsRejectsAliasesThatCollide(t *testing.T) {
    err := validateHotkeyBindings(map[string]string{"rename": "R", "restart": "shift+r"})
    if err == nil { t.Fatal("expected alias collision") }
}
```

Validation expands `hotkeyAliases`, rejects duplicate aliases, rejects structural `Space`, and accepts the key strings already supported by the current dispatcher.

- [ ] **Step 5: Run focused tests and commit**

Run: `go test ./internal/ui ./internal/session -run 'TestResolveHotkeysForMode|TestActionDefinitions|TestValidateHotkeyBindings|ShortcutMode' -count=1`

Commit: `feat(tui): add action catalog and shortcut modes`

---

### Task 2: Pure global action-menu model

**Files:**
- Create: `internal/ui/action_menu.go`
- Create: `internal/ui/action_menu_test.go`

**Interfaces:**
- Consumes: `ActionID`, `ActionCategory`, and `ActionDefinition` from Task 1.
- Produces: `type ActionMenuItem struct { Action ActionDefinition; Enabled bool; DisabledReason string }`
- Produces: `type ActionMenuSelection struct { ID ActionID }`
- Produces: `func NewActionMenu() *ActionMenu`
- Produces: `func (m *ActionMenu) Show(items []ActionMenuItem)`
- Produces: `func (m *ActionMenu) Update(tea.Msg) (*ActionMenu, tea.Cmd)`
- Produces: `func (m *ActionMenu) ConsumeSelection() (ActionID, bool)`

- [ ] **Step 1: Write failing navigation, filtering, disabled-item, and empty-context tests**

```go
func TestActionMenuShowsGlobalItemsWithoutContext(t *testing.T) {
    menu := NewActionMenu()
    menu.Show([]ActionMenuItem{{Action: ActionDefinition{ID: "new_session", Label: "New session", Category: ActionCategorySessions}, Enabled: true}})
    if !strings.Contains(menu.View(), "New session") { t.Fatal(menu.View()) }
}

func TestActionMenuCannotSelectDisabledItem(t *testing.T) {
    menu := NewActionMenu()
    menu.Show([]ActionMenuItem{{Action: ActionDefinition{ID: "restart", Label: "Restart"}, DisabledReason: "No session selected"}})
    menu, _ = menu.Update(tea.KeyMsg{Type: tea.KeyEnter})
    if _, ok := menu.ConsumeSelection(); ok { t.Fatal("disabled action selected") }
}
```

- [ ] **Step 2: Run tests and verify missing-type failures**

Run: `go test ./internal/ui -run TestActionMenu -count=1`

- [ ] **Step 3: Implement the focused Bubble Tea model**

The model owns only visibility, query, filtered indexes, cursor, scroll offset, and pending selection. It renders category headings and an explicit reason for disabled entries. It performs no session mutation.

- [ ] **Step 4: Add overlay key-isolation tests and make them pass**

Test that letters update filtering, arrows move, Enter selects, Escape closes, and none of those keys are returned for home dispatch.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/ui -run TestActionMenu -count=1`

Commit: `feat(tui): add global action menu model`

---

### Task 3: Home action dispatch and global-menu integration

**Files:**
- Create: `internal/ui/home_actions.go`
- Create: `internal/ui/home_actions_test.go`
- Modify: `internal/ui/home.go`
- Test: `internal/ui/home_test.go`
- Test: `internal/ui/hub_integration_test.go`

**Interfaces:**
- Consumes: `ActionMenu`, `ActionMenuItem`, and the action catalog.
- Produces: `func (h *Home) availableActionMenuItems() []ActionMenuItem`
- Produces: `func (h *Home) dispatchAction(id ActionID) (tea.Model, tea.Cmd)`
- Produces: `func (h *Home) showActionMenu()`

- [ ] **Step 1: Write failing Space tests with no selection and each row family**

```go
func TestHomeSpaceOpensGlobalMenuWithNoRows(t *testing.T) {
    h := newTestHome()
    h.flatItems = nil
    h.cursor = -1
    _, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeySpace})
    if !h.actionMenu.IsVisible() { t.Fatal("global menu did not open") }
    if !h.actionMenu.HasAction(ActionNewSession) { t.Fatal("global actions missing") }
}
```

Add table cases for local session, local group, hub session, hub node, remote session, and remote group. Assert unsupported operations are absent or disabled with a reason.

- [ ] **Step 2: Run tests and verify current Space jump-mode behavior fails them**

Run: `go test ./internal/ui -run 'TestHomeSpaceOpensGlobalMenu|TestHomeActionMenuContext' -count=1`

- [ ] **Step 3: Add the menu to `Home` and route it before main-key dispatch**

```go
type Home struct {
    // existing fields
    actionMenu *ActionMenu
}

func (h *Home) showActionMenu() {
    h.actionMenu.Show(h.availableActionMenuItems())
}
```

Route visible-menu keys before `handleMainKey`. Make overview Space structural and move jump mode to action ID `jump_mode`; its legacy accelerator is `alt+space`.

- [ ] **Step 4: Extract direct action dispatch from key normalization**

`dispatchAction` invokes existing operation helpers directly where they exist. For still-inline cases, extract a focused helper from the old switch. Do not invoke an action by synthesizing its shortcut key. Keep Enter's row activation logic structural.

- [ ] **Step 5: Write and pass disabled-shortcut/menu-available regression tests**

```go
func TestDisabledRestartShortcutStillAvailableThroughMenu(t *testing.T) {
    h := homeWithSelectedSession()
    h.setHotkeys(resolveHotkeysForMode(map[string]string{"restart": ""}, "menu"))
    assertKeyDoesNotRestart(t, h, "R")
    assertMenuDispatchesAction(t, h, ActionRestart)
}
```

- [ ] **Step 6: Run focused tests and commit**

Run: `go test ./internal/ui -run 'TestHomeSpaceOpensGlobalMenu|TestHomeActionMenuContext|TestDisabled.*Shortcut' -count=1`

Commit: `feat(tui): route overview actions through global menu`

---

### Task 4: Shortcut editor

**Files:**
- Create: `internal/ui/shortcut_settings.go`
- Create: `internal/ui/shortcut_settings_test.go`
- Modify: `internal/ui/action_menu.go`
- Modify: `internal/ui/home.go`

**Interfaces:**
- Consumes: the action catalog and `validateHotkeyBindings`.
- Produces: `func NewShortcutSettings() *ShortcutSettings`
- Produces: `func (s *ShortcutSettings) Show(cfg *session.UserConfig)`
- Produces: `func (s *ShortcutSettings) Bindings() (mode string, bindings map[string]string, err error)`
- Produces: `func (s *ShortcutSettings) ConsumeSave() bool`

- [ ] **Step 1: Write failing individual-toggle, key-capture, conflict, and preset tests**

```go
func TestShortcutSettingsTogglesOneActionWithoutChangingOthers(t *testing.T) {
    panel := NewShortcutSettings()
    panel.Show(&session.UserConfig{UI: session.UISettings{ShortcutMode: "menu"}})
    panel.Select(ActionRename)
    panel.Toggle()
    _, got, err := panel.Bindings()
    if err != nil || got["rename"] != defaultHotkeyBindings[hotkeyRename] {
        t.Fatalf("got=%v err=%v", got, err)
    }
    if _, ok := got["delete"]; ok { t.Fatal("unrelated shortcut enabled") }
}
```

Add tests proving alias conflicts show both action labels, key capture can be canceled, menu-first disables all optional actions, and legacy restores all non-conflicting defaults.

- [ ] **Step 2: Run tests and verify missing-type failures**

Run: `go test ./internal/ui -run TestShortcutSettings -count=1`

- [ ] **Step 3: Implement the searchable grouped editor**

Use arrows for navigation, Space for one toggle, Enter for capture, and Escape for cancel/back. Provide explicit Menu-first and Legacy preset rows. Preset application opens the existing confirmation dialog before changing the draft.

- [ ] **Step 4: Integrate `Application > Keyboard shortcuts` and overlay priority**

Opening the editor from the global menu must work when the settings shortcut is disabled. Its key handler consumes all input while visible.

- [ ] **Step 5: Run focused tests and commit**

Run: `go test ./internal/ui -run 'TestShortcutSettings|TestShortcutOverlay' -count=1`

Commit: `feat(tui): add per-action shortcut settings`

---

### Task 5: Safe persistence and immediate activation

**Files:**
- Modify: `internal/ui/home.go`
- Modify: `internal/session/userconfig.go`
- Test: `internal/ui/shortcut_settings_test.go`
- Test: `internal/ui/settings_panel_eval_test.go`
- Test: `internal/session/userconfig_section_guard_test.go`

**Interfaces:**
- Consumes: `ShortcutSettings.Bindings`.
- Produces: `func saveShortcutPreferences(mode string, bindings map[string]string) error`

- [ ] **Step 1: Write failing isolated-home round-trip and failure-atomicity tests**

Seed a config containing `[mcps]`, `[groups]`, `[remotes]`, `[tmux]`, and `[hotkeys]`; save one shortcut change; reload and assert every unrelated section is equivalent. Inject a save failure and assert active bindings did not change.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/ui ./internal/session -run 'Shortcut.*RoundTrip|Shortcut.*SaveFailure' -count=1`

- [ ] **Step 3: Implement merge-first save and reload active mappings only after success**

```go
func saveShortcutPreferences(mode string, bindings map[string]string) error {
    cfg, err := session.LoadUserConfig()
    if err != nil { return err }
    if cfg == nil { cfg = &session.UserConfig{} }
    merged := *cfg
    merged.UI.ShortcutMode = mode
    merged.Hotkeys = cloneBindings(bindings)
    return session.SaveUserConfig(&merged)
}
```

- [ ] **Step 4: Run complete affected-package tests and commit**

Run: `go test ./internal/ui ./internal/session -count=1`

Commit: `feat(tui): persist shortcut preferences safely`

---

### Task 6: Visible Menu affordance, help, footer, and migration hint

**Files:**
- Modify: `internal/ui/home.go`
- Modify: `internal/ui/help.go`
- Modify: `internal/ui/help_test.go`
- Modify: `internal/ui/home_test.go`
- Modify: `docs/terminal-shortcuts.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the catalog and resolved enabled bindings.
- Produces: a header/footer `Menu` hit target and one-time menu-first hint state.

- [ ] **Step 1: Write failing rendering and mouse tests**

Assert the overview always renders `Space Menu`, the Menu hit target opens the menu without a selected row, help omits disabled shortcuts, and enabled explicit bindings appear once with the catalog label.

- [ ] **Step 2: Run tests and verify current footer/help output fails**

Run: `go test ./internal/ui -run 'Test.*MenuAffordance|TestHelp.*EnabledShortcut|TestHelp.*DisabledShortcut' -count=1`

- [ ] **Step 3: Render catalog-driven help/footer and clickable Menu**

Remove duplicated action labels where the catalog supplies them. Keep structural controls in a short navigation section. Add a stable mouse hit target independent of list bounds.

- [ ] **Step 4: Add and test the one-time migration hint**

Persist dismissal through the existing UI-state mechanism, not `config.toml`. Use the text `Space: Menu · Enable legacy accelerators in Keyboard shortcuts`.

- [ ] **Step 5: Update documentation and commit**

Document menu-first behavior, `Space`, shortcut editing, presets, and `[ui].shortcut_mode`.

Commit: `docs(tui): document menu-first controls`

---

### Task 7: Integration verification and delivery preparation

**Files:**
- Modify only files required by failures discovered below.
- Create the PR body under `/var/tmp/lrasmussen`, not in the repository.

**Interfaces:**
- Consumes: all previous tasks.
- Produces: a verified branch and complete PR evidence.

- [ ] **Step 1: Run formatting, diff checks, and vet**

Run `gofmt` over changed Go files, followed by `git diff --check` and `go vet ./...`.

- [ ] **Step 2: Run the isolated full suite**

Create a task directory with `mktemp -d /var/tmp/lrasmussen/agent-deck-menu-tests.XXXXXX`, place `HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, and `XDG_CACHE_HOME` beneath it, then run `go test ./...`. Never use `/tmp`.

- [ ] **Step 3: Prove regression tests fail without production changes**

In a disposable disk-backed copy or temporary worktree, revert only non-test hunks and run the new menu/shortcut tests. Record the expected failures, restore the implementation, and rerun green.

- [ ] **Step 4: Run contributor self-check**

Create the PR body from `.github/PULL_REQUEST_TEMPLATE.md`, include the user's verbatim ask, evidence, AI disclosure, and final gate marker. Run `.github/skills/agent-deck-contributor/scripts/self-check.sh <pr-body>`.

- [ ] **Step 5: Review the complete diff**

Use the required `open-code-review-delegate` skill. Address every valid finding and rerun affected tests.

- [ ] **Step 6: Push and open the PR**

Push `feat/tui-action-menu`, open the PR on `lbr88/agent-deck`, and do not request Copilot. Wait for all checks and other required reviewers, resolve every conversation, merge, tag a new patch release, install it locally, verify the running TUI switched to it, and deploy the matching hub image.
