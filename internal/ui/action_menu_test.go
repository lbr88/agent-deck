package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func actionMenuItem(id ActionID, label string, category ActionCategory, enabled bool) ActionMenuItem {
	item := ActionMenuItem{
		Action:  ActionDefinition{ID: id, Label: label, Category: category},
		Enabled: enabled,
	}
	if !enabled {
		item.DisabledReason = "No session selected"
	}
	return item
}

func TestActionMenuShowsGlobalItemsWithoutContext(t *testing.T) {
	menu := NewActionMenu()
	menu.SetSize(100, 30)
	menu.Show([]ActionMenuItem{
		actionMenuItem("new_session", "New session", ActionCategorySessions, true),
		actionMenuItem("settings", "Settings", ActionCategoryApp, true),
	})

	view := menu.View()
	for _, want := range []string{"Actions", "Sessions", "New session", "Application", "Settings"} {
		if !strings.Contains(view, want) {
			t.Fatalf("menu view missing %q:\n%s", want, view)
		}
	}
	if !menu.HasAction("new_session") {
		t.Fatal("menu does not report its global action")
	}
}

func TestActionMenuCannotSelectDisabledItem(t *testing.T) {
	menu := NewActionMenu()
	menu.Show([]ActionMenuItem{
		actionMenuItem("restart", "Restart", ActionCategorySelected, false),
	})

	menu, cmd := menu.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("disabled action returned a command")
	}
	if _, ok := menu.ConsumeSelection(); ok {
		t.Fatal("disabled action was selected")
	}
	if !strings.Contains(menu.View(), "No session selected") {
		t.Fatalf("disabled reason is not visible:\n%s", menu.View())
	}
}

func TestActionMenuFiltersByTypedQuery(t *testing.T) {
	menu := NewActionMenu()
	menu.Show([]ActionMenuItem{
		actionMenuItem("new_session", "New session", ActionCategorySessions, true),
		actionMenuItem("settings", "Settings", ActionCategoryApp, true),
	})

	menu, _ = menu.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("set")})
	view := menu.View()
	if !strings.Contains(view, "Settings") {
		t.Fatalf("matching action disappeared:\n%s", view)
	}
	if strings.Contains(view, "New session") {
		t.Fatalf("non-matching action remained visible:\n%s", view)
	}
	if got := menu.Query(); got != "set" {
		t.Fatalf("query = %q, want set", got)
	}
}

func TestActionMenuArrowNavigationAndEnterSelect(t *testing.T) {
	menu := NewActionMenu()
	menu.Show([]ActionMenuItem{
		actionMenuItem("new_session", "New session", ActionCategorySessions, true),
		actionMenuItem("settings", "Settings", ActionCategoryApp, true),
	})

	menu, _ = menu.Update(tea.KeyMsg{Type: tea.KeyDown})
	menu, cmd := menu.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("menu selection must be consumed directly by Home, not emitted as a key command")
	}
	if menu.IsVisible() {
		t.Fatal("menu remained visible after selection")
	}
	id, ok := menu.ConsumeSelection()
	if !ok || id != "settings" {
		t.Fatalf("selection = %q, %v; want settings, true", id, ok)
	}
	if _, ok := menu.ConsumeSelection(); ok {
		t.Fatal("selection was not consumed exactly once")
	}
}

func TestActionMenuEscapeClosesWithoutSelection(t *testing.T) {
	menu := NewActionMenu()
	menu.Show([]ActionMenuItem{
		actionMenuItem("new_session", "New session", ActionCategorySessions, true),
	})

	menu, cmd := menu.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("Escape leaked a command from the menu")
	}
	if menu.IsVisible() {
		t.Fatal("Escape did not close the menu")
	}
	if _, ok := menu.ConsumeSelection(); ok {
		t.Fatal("Escape created a selection")
	}
}

func TestActionMenuShowResetsPriorFilterAndSelection(t *testing.T) {
	menu := NewActionMenu()
	items := []ActionMenuItem{
		actionMenuItem("new_session", "New session", ActionCategorySessions, true),
	}
	menu.Show(items)
	menu, _ = menu.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("new")})
	menu, _ = menu.Update(tea.KeyMsg{Type: tea.KeyEnter})

	menu.Show(items)
	if got := menu.Query(); got != "" {
		t.Fatalf("query after Show = %q, want empty", got)
	}
	if _, ok := menu.ConsumeSelection(); ok {
		t.Fatal("Show retained a pending selection")
	}
}
