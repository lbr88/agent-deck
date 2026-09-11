package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func TestShortcutSettingsTogglesOneActionWithoutChangingOthers(t *testing.T) {
	panel := NewShortcutSettings()
	panel.Show(&session.UserConfig{UI: session.UISettings{ShortcutMode: shortcutModeMenu}})
	if !panel.Select(ActionRename) {
		t.Fatal("Rename action is missing")
	}
	panel.Toggle()

	mode, got, err := panel.Bindings()
	if err != nil {
		t.Fatalf("Bindings: %v", err)
	}
	if mode != shortcutModeMenu {
		t.Fatalf("mode = %q, want menu", mode)
	}
	if got[hotkeyRename] != defaultHotkeyBindings[hotkeyRename] {
		t.Fatalf("rename = %q, want %q", got[hotkeyRename], defaultHotkeyBindings[hotkeyRename])
	}
	if got[hotkeyDelete] != "" {
		t.Fatalf("unrelated delete shortcut = %q, want disabled", got[hotkeyDelete])
	}
}

func TestShortcutPreferencesRoundTripPreservesUnrelatedSections(t *testing.T) {
	home := setXDGTestHome(t)
	writeXDGTestConfig(t, home, `
[ui]
preview_pct = 44

[hotkeys]
delete = ""

[mcps.demo]
command = "demo-mcp"

[groups."ops"]
create = true
default_path = "/srv/ops"

[remotes.node]
host = "user@example"

[tmux]
detach_key = "ctrl+]"
`)

	if err := saveShortcutPreferences(shortcutModeMenu, map[string]string{
		hotkeyRename: "ctrl+r",
		hotkeyDelete: "",
	}); err != nil {
		t.Fatalf("saveShortcutPreferences: %v", err)
	}
	session.ClearUserConfigCache()
	cfg, err := session.LoadUserConfig()
	if err != nil {
		t.Fatalf("LoadUserConfig: %v", err)
	}
	if cfg.UI.GetShortcutMode() != shortcutModeMenu || cfg.UI.PreviewPct != 44 {
		t.Fatalf("ui settings changed unexpectedly: %+v", cfg.UI)
	}
	if cfg.Hotkeys[hotkeyRename] != "ctrl+r" || cfg.Hotkeys[hotkeyDelete] != "" {
		t.Fatalf("hotkeys = %#v", cfg.Hotkeys)
	}
	if cfg.MCPs["demo"].Command != "demo-mcp" {
		t.Fatalf("MCP section was lost: %#v", cfg.MCPs)
	}
	if !cfg.Groups["ops"].Create || cfg.Groups["ops"].DefaultPath != "/srv/ops" {
		t.Fatalf("group section was lost: %#v", cfg.Groups)
	}
	if cfg.Remotes["node"].Host != "user@example" {
		t.Fatalf("remote section was lost: %#v", cfg.Remotes)
	}
	if cfg.Tmux.DetachKey != "ctrl+]" {
		t.Fatalf("tmux section was lost: %+v", cfg.Tmux)
	}
}

func TestShortcutSaveFailureDoesNotChangeActiveBindings(t *testing.T) {
	originalSave := saveShortcutUserConfig
	saveShortcutUserConfig = func(*session.UserConfig) error { return errors.New("disk full") }
	t.Cleanup(func() { saveShortcutUserConfig = originalSave })

	h := homeForActionMenu(nil)
	h.shortcutSettings = NewShortcutSettings()
	h.shortcutSettings.Show(&session.UserConfig{UI: session.UISettings{ShortcutMode: shortcutModeMenu}})
	h.shortcutSettings.Select(ActionRename)
	h.shortcutSettings.Toggle()
	h.setHotkeys(map[string]string{hotkeyRename: "x", hotkeyDetach: "ctrl+q"})

	_, _ = h.handleShortcutSettingsKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if got := h.hotkeys[hotkeyRename]; got != "x" {
		t.Fatalf("active rename binding changed after failed save: %q", got)
	}
	if !h.shortcutSettings.IsVisible() {
		t.Fatal("shortcut editor closed after failed save")
	}
	if !strings.Contains(h.shortcutSettings.Error(), "disk full") {
		t.Fatalf("save error not shown in editor: %q", h.shortcutSettings.Error())
	}
}

func TestShortcutSaveSuccessActivatesBindingsImmediately(t *testing.T) {
	setXDGTestHome(t)
	h := homeForActionMenu(nil)
	h.shortcutSettings = NewShortcutSettings()
	h.shortcutSettings.Show(&session.UserConfig{UI: session.UISettings{ShortcutMode: shortcutModeMenu}})
	h.shortcutSettings.Select(ActionRename)
	h.shortcutSettings.Toggle()
	h.setHotkeys(resolveHotkeysForMode(nil, shortcutModeMenu))

	_, _ = h.handleShortcutSettingsKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if got := h.hotkeys[hotkeyRename]; got != defaultHotkeyBindings[hotkeyRename] {
		t.Fatalf("active rename binding = %q, want %q", got, defaultHotkeyBindings[hotkeyRename])
	}
	if h.shortcutMode != shortcutModeMenu {
		t.Fatalf("active shortcut mode = %q, want menu", h.shortcutMode)
	}
	if h.shortcutSettings.IsVisible() {
		t.Fatal("shortcut editor remained open after successful save")
	}
}

func TestShortcutSettingsCaptureCanBeCanceled(t *testing.T) {
	panel := NewShortcutSettings()
	panel.Show(&session.UserConfig{
		UI:      session.UISettings{ShortcutMode: shortcutModeMenu},
		Hotkeys: map[string]string{hotkeyRename: "ctrl+r"},
	})
	panel.Select(ActionRename)
	panel, _ = panel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !panel.IsCapturing() {
		t.Fatal("Enter did not begin key capture")
	}
	panel, _ = panel.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if panel.IsCapturing() {
		t.Fatal("Escape did not cancel key capture")
	}
	_, bindings, err := panel.Bindings()
	if err != nil || bindings[hotkeyRename] != "ctrl+r" {
		t.Fatalf("binding changed after canceled capture: %q, %v", bindings[hotkeyRename], err)
	}
}

func TestShortcutSettingsConflictNamesBothActions(t *testing.T) {
	panel := NewShortcutSettings()
	panel.Show(&session.UserConfig{
		UI:      session.UISettings{ShortcutMode: shortcutModeMenu},
		Hotkeys: map[string]string{hotkeyRestart: "x"},
	})
	panel.Select(ActionRename)
	panel, _ = panel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	panel, _ = panel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	errText := panel.Error()
	if !strings.Contains(errText, "Rename") || !strings.Contains(errText, "Restart") {
		t.Fatalf("conflict error does not name both actions: %q", errText)
	}
	if !panel.IsCapturing() {
		t.Fatal("invalid capture should remain active for correction or cancellation")
	}
}

func TestShortcutSettingsMenuFirstPresetDisablesEveryOptionalAction(t *testing.T) {
	panel := NewShortcutSettings()
	panel.Show(&session.UserConfig{UI: session.UISettings{ShortcutMode: shortcutModeLegacy}})
	panel.RequestPreset(shortcutModeMenu)
	if !panel.ConfirmPreset() {
		t.Fatal("menu-first preset was not confirmed")
	}

	mode, bindings, err := panel.Bindings()
	if err != nil {
		t.Fatalf("Bindings: %v", err)
	}
	if mode != shortcutModeMenu {
		t.Fatalf("mode = %q, want menu", mode)
	}
	for _, definition := range actionDefinitions() {
		if definition.HotkeyAction == "" || !definition.Optional {
			continue
		}
		if bindings[definition.HotkeyAction] != "" {
			t.Fatalf("optional action %q remained enabled as %q", definition.ID, bindings[definition.HotkeyAction])
		}
	}
}

func TestShortcutSettingsLegacyPresetRestoresHistoricalBindings(t *testing.T) {
	panel := NewShortcutSettings()
	panel.Show(&session.UserConfig{UI: session.UISettings{ShortcutMode: shortcutModeMenu}})
	panel.RequestPreset(shortcutModeLegacy)
	if !panel.ConfirmPreset() {
		t.Fatal("legacy preset was not confirmed")
	}

	mode, bindings, err := panel.Bindings()
	if err != nil {
		t.Fatalf("Bindings: %v", err)
	}
	if mode != shortcutModeLegacy {
		t.Fatalf("mode = %q, want legacy", mode)
	}
	if bindings[hotkeyRename] != defaultHotkeyBindings[hotkeyRename] {
		t.Fatalf("rename = %q, want legacy default %q", bindings[hotkeyRename], defaultHotkeyBindings[hotkeyRename])
	}
	if bindings[hotkeyJumpMode] != "alt+space" {
		t.Fatalf("jump mode = %q, want alt+space", bindings[hotkeyJumpMode])
	}
	if bindings[hotkeyHandover] != "" {
		t.Fatalf("new opt-in handover shortcut = %q, want disabled", bindings[hotkeyHandover])
	}
}

func TestShortcutSettingsFiltersAndSignalsSave(t *testing.T) {
	panel := NewShortcutSettings()
	panel.SetSize(100, 35)
	panel.Show(&session.UserConfig{UI: session.UISettings{ShortcutMode: shortcutModeMenu}})
	panel, _ = panel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("handover")})
	view := panel.View()
	if !strings.Contains(view, "Hand over to another agent") || strings.Contains(view, "Restart with new session ID") {
		t.Fatalf("filtered shortcut view is wrong:\n%s", view)
	}

	panel, _ = panel.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !panel.ConsumeSave() {
		t.Fatal("Ctrl+S did not signal save")
	}
	if panel.ConsumeSave() {
		t.Fatal("save signal was not consumed exactly once")
	}
}

func TestShortcutOverlayOpensFromGlobalMenuWithoutShortcut(t *testing.T) {
	h := homeForActionMenu(nil)
	h.shortcutSettings = NewShortcutSettings()
	h.showActionMenu()

	_, _ = h.handleActionMenuKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("keyboard")})
	_, _ = h.handleActionMenuKey(tea.KeyMsg{Type: tea.KeyEnter})

	if h.actionMenu.IsVisible() {
		t.Fatal("action menu remained visible after choosing Keyboard shortcuts")
	}
	if !h.shortcutSettings.IsVisible() {
		t.Fatal("Keyboard shortcuts did not open from the global menu")
	}
}

func TestShortcutOverlayConsumesKeysBeforeHomeActions(t *testing.T) {
	h := homeForActionMenu(nil)
	h.shortcutSettings = NewShortcutSettings()
	h.setHotkeys(resolveHotkeysForMode(map[string]string{hotkeyQuit: "q"}, shortcutModeMenu))
	_, _ = h.dispatchAction(ActionKeyboardShortcuts)

	model, cmd := h.handleShortcutSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if model != h || cmd != nil {
		t.Fatal("shortcut overlay leaked a command or replaced Home")
	}
	if h.isQuitting {
		t.Fatal("shortcut-overlay filter key leaked into quit")
	}
}
