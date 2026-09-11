package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestOverviewAlwaysRendersMenuAffordance(t *testing.T) {
	for _, width := range []int{45, 55, 80, 120} {
		home := newTestHomeWithItems(width, 30, nil)
		home.shortcutMode = shortcutModeMenu
		home.setHotkeys(resolveHotkeysForMode(nil, shortcutModeMenu))
		home.cursor = -1

		for _, jumpMode := range []bool{false, true} {
			home.jumpMode = jumpMode
			if got := ansi.Strip(home.renderHelpBar()); !strings.Contains(got, menuAffordanceText) {
				t.Fatalf("width=%d jump=%v footer = %q, want persistent %q affordance", width, jumpMode, got, menuAffordanceText)
			}
		}
	}
}

func TestMenuAffordanceClickOpensWithoutSelectedRow(t *testing.T) {
	home := newTestHomeWithItems(100, 30, nil)
	home.shortcutMode = shortcutModeMenu
	home.setHotkeys(resolveHotkeysForMode(nil, shortcutModeMenu))
	home.cursor = -1
	home.actionMenu.Hide()

	x0, _, y := home.menuAffordanceBounds()
	model, _ := home.handleMouse(tea.MouseMsg{
		X: x0, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
	})
	got := model.(*Home)
	if !got.actionMenu.IsVisible() {
		t.Fatal("clicking the persistent Menu affordance did not open the global menu")
	}
}

func TestMenuAffordanceBoundsUseTerminalCellsAfterUnicodeHints(t *testing.T) {
	inst := session.NewInstanceWithGroupAndTool("unicode-prefix", ".", session.DefaultGroupPath, "codex")
	home := newTestHomeWithItems(120, 30, []session.Item{{Type: session.ItemTypeSession, Session: inst}})
	home.footerMode = session.FooterCurated
	home.shortcutMode = shortcutModeLegacy
	home.setHotkeys(resolveHotkeysForMode(nil, shortcutModeLegacy))

	line := strings.Split(ansi.Strip(home.renderHelpBar()), "\n")[1]
	byteIndex := strings.Index(line, menuAffordanceText)
	if byteIndex < 0 {
		t.Fatalf("footer missing %q: %q", menuAffordanceText, line)
	}
	wantX := ansi.StringWidth(line[:byteIndex])
	gotX, _, _ := home.menuAffordanceBounds()
	if gotX != wantX {
		t.Fatalf("menu click starts at terminal cell %d, want %d for footer %q", gotX, wantX, line)
	}
}

func TestHelpDisabledShortcutIsOmitted(t *testing.T) {
	help := NewHelpOverlay()
	help.SetSize(120, 80)
	help.SetHotkeys(map[string]string{hotkeyRestart: "alt+r"})
	help.Show()

	got := ansi.Strip(help.View())
	if strings.Contains(got, "Delete session") {
		t.Fatalf("disabled Delete shortcut was still advertised:\n%s", got)
	}
	if count := strings.Count(got, "Restart"); count != 1 {
		t.Fatalf("enabled Restart action appeared %d times, want once:\n%s", count, got)
	}
	if count := strings.Count(got, "alt+r"); count != 1 {
		t.Fatalf("explicit Restart binding appeared %d times, want once:\n%s", count, got)
	}
}

func TestMenuHintDismissalPersistsInUIState(t *testing.T) {
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	session.ClearUserConfigCache()
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		session.ClearUserConfigCache()
	})

	storage, err := session.NewStorageWithProfile("_menu_hint")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	t.Cleanup(func() { storage.Close() })

	before := NewHome()
	before.storage = storage
	before.menuHintShown = true
	if err := before.saveUIStateErr(); err != nil {
		t.Fatalf("saveUIStateErr: %v", err)
	}

	after := NewHome()
	after.storage = storage
	after.menuHintShown = false
	after.loadUIState()
	if !after.menuHintShown {
		t.Fatal("menu hint dismissal did not survive UI-state round trip")
	}
}
