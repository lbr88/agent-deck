package ui

import "testing"

func TestActionDefinitionsHaveUniqueStableIDsAndBindings(t *testing.T) {
	seenIDs := make(map[ActionID]bool)
	seenHotkeys := make(map[string]bool)
	for _, action := range actionDefinitions() {
		if action.ID == "" {
			t.Fatal("action catalog contains an empty ID")
		}
		if seenIDs[action.ID] {
			t.Fatalf("action catalog contains duplicate ID %q", action.ID)
		}
		seenIDs[action.ID] = true
		if action.Label == "" {
			t.Fatalf("action %q has no label", action.ID)
		}
		if action.HotkeyAction != "" {
			if seenHotkeys[action.HotkeyAction] {
				t.Fatalf("hotkey action %q appears more than once", action.HotkeyAction)
			}
			seenHotkeys[action.HotkeyAction] = true
		}
	}

	for _, action := range hotkeyActionOrder {
		if !seenHotkeys[action] {
			t.Errorf("configurable hotkey action %q is missing from the action catalog", action)
		}
	}
}

func TestResolveHotkeysForModeMenuFirstOnlyEnablesExplicitBindings(t *testing.T) {
	got := resolveHotkeysForMode(map[string]string{
		hotkeyRename: "ctrl+r",
		hotkeyDelete: "",
	}, shortcutModeMenu)

	if got[hotkeyRename] != "ctrl+r" {
		t.Fatalf("rename binding = %q, want ctrl+r", got[hotkeyRename])
	}
	if _, ok := got[hotkeyDelete]; ok {
		t.Fatal("explicitly disabled delete shortcut was enabled")
	}
	if _, ok := got[hotkeyRestart]; ok {
		t.Fatal("implicit legacy restart shortcut was enabled in menu-first mode")
	}
	if got[hotkeyDetach] != defaultHotkeyBindings[hotkeyDetach] {
		t.Fatalf("structural detach binding = %q, want %q", got[hotkeyDetach], defaultHotkeyBindings[hotkeyDetach])
	}
}

func TestResolveHotkeysForModeLegacyRestoresDefaults(t *testing.T) {
	got := resolveHotkeysForMode(nil, shortcutModeLegacy)
	if got[hotkeyRename] != defaultHotkeyBindings[hotkeyRename] {
		t.Fatalf("legacy rename binding = %q, want %q", got[hotkeyRename], defaultHotkeyBindings[hotkeyRename])
	}
	if got[hotkeyRestart] != defaultHotkeyBindings[hotkeyRestart] {
		t.Fatalf("legacy restart binding = %q, want %q", got[hotkeyRestart], defaultHotkeyBindings[hotkeyRestart])
	}
}

func TestValidateHotkeyBindingsRejectsAliasCollision(t *testing.T) {
	err := validateHotkeyBindings(map[string]string{
		hotkeyRename:  "R",
		hotkeyRestart: "shift+r",
	})
	if err == nil {
		t.Fatal("expected shifted alias collision to be rejected")
	}
}

func TestValidateHotkeyBindingsRejectsStructuralMenuKey(t *testing.T) {
	if err := validateHotkeyBindings(map[string]string{hotkeyRename: "space"}); err == nil {
		t.Fatal("expected structural Space key to be rejected")
	}
}

func TestValidateHotkeyBindingsRejectsUnsupportedKey(t *testing.T) {
	if err := validateHotkeyBindings(map[string]string{hotkeyRename: "ctrl+shift+definitely-not-a-key"}); err == nil {
		t.Fatal("expected unsupported key to be rejected")
	}
}
