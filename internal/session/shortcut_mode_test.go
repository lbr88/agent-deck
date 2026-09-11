package session

import "testing"

func TestUISettingsGetShortcutModeDefaultsToMenu(t *testing.T) {
	for _, raw := range []string{"", "unknown"} {
		if got := (UISettings{ShortcutMode: raw}).GetShortcutMode(); got != "menu" {
			t.Fatalf("GetShortcutMode(%q) = %q, want menu", raw, got)
		}
	}
}

func TestUISettingsGetShortcutModeAcceptsLegacy(t *testing.T) {
	if got := (UISettings{ShortcutMode: "LEGACY"}).GetShortcutMode(); got != "legacy" {
		t.Fatalf("GetShortcutMode(LEGACY) = %q, want legacy", got)
	}
}
