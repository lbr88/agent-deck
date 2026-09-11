package ui

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func homeForActionMenu(items []session.Item) *Home {
	return &Home{
		width:      120,
		height:     40,
		cursor:     0,
		flatItems:  items,
		actionMenu: NewActionMenu(),
		hotkeys:    make(map[string]string),
	}
}

func menuItemByID(items []ActionMenuItem, id ActionID) (ActionMenuItem, bool) {
	for _, item := range items {
		if item.Action.ID == id {
			return item, true
		}
	}
	return ActionMenuItem{}, false
}

func TestHomeSpaceOpensGlobalMenuWithNoRows(t *testing.T) {
	h := homeForActionMenu(nil)
	h.cursor = -1

	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeySpace})

	if !h.actionMenu.IsVisible() {
		t.Fatal("global menu did not open")
	}
	if !h.actionMenu.HasAction(ActionNewSession) {
		t.Fatal("global New session action is missing without a selected row")
	}
}

func TestHomeActionMenuOpensForEveryRowFamily(t *testing.T) {
	local := session.NewInstanceWithGroupAndTool("local", ".", session.DefaultGroupPath, "codex")
	tests := []struct {
		name string
		item session.Item
	}{
		{name: "local session", item: session.Item{Type: session.ItemTypeSession, Session: local}},
		{name: "local group", item: session.Item{Type: session.ItemTypeGroup, Path: session.DefaultGroupPath, Group: &session.Group{Name: session.DefaultGroupName, Path: session.DefaultGroupPath}}},
		{name: "remote session", item: session.Item{Type: session.ItemTypeRemoteSession, RemoteName: "node", RemoteSession: &session.RemoteSessionInfo{ID: "remote-1", Title: "remote"}}},
		{name: "remote group", item: session.Item{Type: session.ItemTypeRemoteGroup, RemoteName: "node", Path: "remotes/node"}},
		{name: "hub session", item: session.Item{Type: session.ItemTypeHubSession, HubNodeID: "node-1", HubSession: &session.HubSessionInfo{ID: "hub-1", Title: "hub", Tool: "codex"}}},
		{name: "hub group", item: session.Item{Type: session.ItemTypeHubGroup, HubNodeID: "node-1", HubGroupPath: session.DefaultGroupPath}},
		{name: "hub node", item: session.Item{Type: session.ItemTypeHubNode, HubNodeID: "node-1", HubNodeName: "node"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := homeForActionMenu([]session.Item{tt.item})
			_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeySpace})
			if !h.actionMenu.IsVisible() {
				t.Fatal("global menu did not open")
			}
			if !h.actionMenu.HasAction(ActionNewSession) {
				t.Fatal("global actions disappeared for this row family")
			}
		})
	}
}

func TestHomeActionMenuContextEnablesOnlySupportedRestart(t *testing.T) {
	local := session.NewInstanceWithGroupAndTool("local", ".", session.DefaultGroupPath, "codex")
	localItems := homeForActionMenu([]session.Item{{Type: session.ItemTypeSession, Session: local}}).availableActionMenuItems()
	restart, ok := menuItemByID(localItems, ActionRestart)
	if !ok || !restart.Enabled {
		t.Fatalf("local session restart = %#v, %v; want enabled", restart, ok)
	}

	groupItems := homeForActionMenu([]session.Item{{Type: session.ItemTypeGroup, Path: session.DefaultGroupPath}}).availableActionMenuItems()
	restart, ok = menuItemByID(groupItems, ActionRestart)
	if ok && (restart.Enabled || restart.DisabledReason == "") {
		t.Fatalf("group restart = %#v; want absent or disabled with reason", restart)
	}
}

func TestDisabledShortcutStillDispatchesDirectlyFromMenuAction(t *testing.T) {
	h := homeForActionMenu(nil)
	h.setHotkeys(resolveHotkeysForMode(nil, shortcutModeMenu))

	before := h.previewMode
	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if h.previewMode != before {
		t.Fatal("disabled legacy v shortcut still changed preview mode")
	}

	_, _ = h.dispatchAction(ActionTogglePreview)
	if h.previewMode == before {
		t.Fatal("direct menu action did not change preview mode")
	}
}

func TestActionMenuConsumesKeysBeforeHomeShortcuts(t *testing.T) {
	h := homeForActionMenu(nil)
	h.setHotkeys(resolveHotkeysForMode(map[string]string{hotkeyQuit: "q"}, shortcutModeMenu))
	h.showActionMenu()

	model, cmd := h.handleActionMenuKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if model != h || cmd != nil {
		t.Fatal("menu filtering leaked a command or replaced Home")
	}
	if got := h.actionMenu.Query(); got != "q" {
		t.Fatalf("menu query = %q, want q", got)
	}
	if h.isQuitting {
		t.Fatal("menu filter key leaked into the quit action")
	}
}
