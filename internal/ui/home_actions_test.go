package ui

import (
	"testing"
	"time"

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

func TestHomeActionMenuOmitsHandlersThatWouldNoOpForSelectedRow(t *testing.T) {
	nonsandbox := session.NewInstanceWithGroupAndTool("local", ".", session.DefaultGroupPath, "codex")
	tests := []struct {
		name   string
		item   session.Item
		action ActionID
	}{
		{name: "hub node cannot open", item: session.Item{Type: session.ItemTypeHubNode, HubNodeID: "node-1"}, action: ActionOpen},
		{name: "hub group cannot open", item: session.Item{Type: session.ItemTypeHubGroup, HubNodeID: "node-1", HubGroupPath: "grp"}, action: ActionOpen},
		{name: "remote session cannot move group", item: session.Item{Type: session.ItemTypeRemoteSession, RemoteName: "node", RemoteSession: &session.RemoteSessionInfo{ID: "remote-1"}}, action: ActionMoveToGroup},
		{name: "ordinary session has no sandbox shell", item: session.Item{Type: session.ItemTypeSession, Session: nonsandbox}, action: ActionExecShell},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := homeForActionMenu([]session.Item{tt.item}).availableActionMenuItems()
			if action, ok := menuItemByID(items, tt.action); ok && action.Enabled {
				t.Fatalf("action %q was enabled even though its handler would do nothing: %#v", tt.action, action)
			}
		})
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

func TestMenuModeEnablesOnlyTheConfiguredKeyNotLegacyAliases(t *testing.T) {
	a := session.NewInstanceWithGroupAndTool("a", ".", session.DefaultGroupPath, "codex")
	b := session.NewInstanceWithGroupAndTool("b", ".", session.DefaultGroupPath, "codex")
	h := homeForActionMenu([]session.Item{
		{Type: session.ItemTypeSession, Session: a},
		{Type: session.ItemTypeSession, Session: b},
	})
	h.shortcutMode = shortcutModeMenu
	h.setHotkeys(resolveHotkeysForMode(map[string]string{hotkeyNavigateDown: "j"}, shortcutModeMenu))

	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyCtrlN})
	if h.cursor != 0 {
		t.Fatalf("legacy ctrl+n alias moved cursor in menu mode: %d", h.cursor)
	}
	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if h.cursor != 1 {
		t.Fatalf("configured j shortcut did not move cursor: %d", h.cursor)
	}
}

func TestLegacyModeRetainsHistoricalShortcutAliases(t *testing.T) {
	a := session.NewInstanceWithGroupAndTool("a", ".", session.DefaultGroupPath, "codex")
	b := session.NewInstanceWithGroupAndTool("b", ".", session.DefaultGroupPath, "codex")
	h := homeForActionMenu([]session.Item{
		{Type: session.ItemTypeSession, Session: a},
		{Type: session.ItemTypeSession, Session: b},
	})
	h.shortcutMode = shortcutModeLegacy
	h.setHotkeys(resolveHotkeysForMode(nil, shortcutModeLegacy))

	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyCtrlN})
	if h.cursor != 1 {
		t.Fatalf("legacy ctrl+n alias did not move cursor: %d", h.cursor)
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

func TestActionMenuSelectionRestoresCapturedIdentityAfterReorder(t *testing.T) {
	a := session.NewInstanceWithGroupAndTool("a", ".", session.DefaultGroupPath, "codex")
	b := session.NewInstanceWithGroupAndTool("b", ".", session.DefaultGroupPath, "codex")
	h := homeForActionMenu([]session.Item{
		{Type: session.ItemTypeSession, Session: a},
		{Type: session.ItemTypeSession, Session: b},
	})
	h.showActionMenu()

	// A background refresh reorders the rows while the menu is open and leaves
	// the cursor at index zero, which now points at a different session.
	h.flatItems[0], h.flatItems[1] = h.flatItems[1], h.flatItems[0]
	h.cursor = 0

	if !h.ensureActionMenuTarget(ActionRename) {
		t.Fatal("captured menu target still exists but was rejected")
	}
	if h.cursor != 1 || h.flatItems[h.cursor].Session.ID != a.ID {
		t.Fatalf("menu target drifted to %q; want captured session %q", h.flatItems[h.cursor].Session.ID, a.ID)
	}
}

func TestActionMenuSelectionRejectsDisappearedTarget(t *testing.T) {
	a := session.NewInstanceWithGroupAndTool("a", ".", session.DefaultGroupPath, "codex")
	h := homeForActionMenu([]session.Item{{Type: session.ItemTypeSession, Session: a}})
	h.showActionMenu()
	h.flatItems = nil
	h.cursor = -1

	if h.ensureActionMenuTarget(ActionRename) {
		t.Fatal("selection-scoped action accepted a target that disappeared")
	}
	if h.err == nil {
		t.Fatal("disappeared menu target did not produce user-visible feedback")
	}
}

func TestActionMenuGlobalActionDoesNotRequireCapturedTarget(t *testing.T) {
	h := homeForActionMenu(nil)
	h.cursor = -1
	h.showActionMenu()
	if !h.ensureActionMenuTarget(ActionNewSession) {
		t.Fatal("global action incorrectly required a selected item")
	}
}

func TestActionMenuContextualGlobalActionRestoresCapturedTarget(t *testing.T) {
	a := session.NewInstanceWithGroupAndTool("a", ".", session.DefaultGroupPath, "codex")
	b := session.NewInstanceWithGroupAndTool("b", ".", session.DefaultGroupPath, "codex")
	h := homeForActionMenu([]session.Item{
		{Type: session.ItemTypeSession, Session: a},
		{Type: session.ItemTypeSession, Session: b},
	})
	h.showActionMenu()
	h.flatItems[0], h.flatItems[1] = h.flatItems[1], h.flatItems[0]
	h.cursor = 0

	if !h.ensureActionMenuTarget(ActionNewSession) {
		t.Fatal("contextual global action rejected its captured target")
	}
	if h.cursor != 1 || h.flatItems[h.cursor].Session.ID != a.ID {
		t.Fatalf("new-session context drifted to %q; want %q", h.flatItems[h.cursor].Session.ID, a.ID)
	}
}

func TestActionMenuContextualGlobalActionWorksWithoutInitialTarget(t *testing.T) {
	h := homeForActionMenu(nil)
	h.cursor = -1
	h.showActionMenu()
	if !h.ensureActionMenuTarget(ActionCreateGroup) {
		t.Fatal("create-group action should work without an initial row")
	}
}

func TestDirectCreateGroupActionDoesNotTriggerLegacyDoubleG(t *testing.T) {
	h := homeForActionMenu(nil)
	h.groupDialog = NewGroupDialog()
	h.lastGTime = time.Now()

	_, _ = h.dispatchAction(ActionCreateGroup)
	if !h.groupDialog.IsVisible() {
		t.Fatal("menu create-group action was mistaken for the second key in legacy gg navigation")
	}
}
