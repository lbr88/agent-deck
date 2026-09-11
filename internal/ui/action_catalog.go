package ui

// ActionID is the stable identity shared by menus, shortcuts, help, and
// dispatch. Hotkey-backed actions deliberately retain their existing config
// names so [hotkeys] remains backward compatible.
type ActionID string

type ActionCategory string

const (
	ActionCategorySelected ActionCategory = "selected"
	ActionCategorySessions ActionCategory = "sessions"
	ActionCategoryManage   ActionCategory = "manage"
	ActionCategoryView     ActionCategory = "view"
	ActionCategoryApp      ActionCategory = "application"
)

const (
	shortcutModeMenu   = "menu"
	shortcutModeLegacy = "legacy"
)

// ActionDefinition contains presentation and accelerator metadata. Runtime
// availability and invocation live on Home because they depend on current
// session state.
type ActionDefinition struct {
	ID           ActionID
	Label        string
	Category     ActionCategory
	DefaultKey   string
	HotkeyAction string
	Optional     bool
}

func hotkeyAction(id, label string, category ActionCategory) ActionDefinition {
	return ActionDefinition{
		ID:           ActionID(id),
		Label:        label,
		Category:     category,
		DefaultKey:   defaultHotkeyBindings[id],
		HotkeyAction: id,
		Optional:     id != hotkeyDetach,
	}
}

// actionDefinitions is the single ordered catalog for every existing
// user-configurable TUI action.
func actionDefinitions() []ActionDefinition {
	return []ActionDefinition{
		hotkeyAction(hotkeyRename, "Rename", ActionCategorySelected),
		hotkeyAction(hotkeyRestart, "Restart", ActionCategorySelected),
		hotkeyAction(hotkeyRestartFresh, "Restart with new session ID", ActionCategorySelected),
		hotkeyAction(hotkeyDelete, "Delete", ActionCategorySelected),
		hotkeyAction(hotkeyCloseSession, "Close process", ActionCategorySelected),
		hotkeyAction(hotkeyArchiveSession, "Archive", ActionCategorySelected),
		hotkeyAction(hotkeyUnarchiveSession, "Unarchive", ActionCategorySelected),
		hotkeyAction(hotkeyMoveToGroup, "Move to group", ActionCategorySelected),
		hotkeyAction(hotkeyMarkUnread, "Mark unread", ActionCategorySelected),
		hotkeyAction(hotkeyQuickApprove, "Quick approve", ActionCategorySelected),
		hotkeyAction(hotkeyPromptSession, "Prompt session", ActionCategorySelected),
		hotkeyAction(hotkeyToggleYolo, "Toggle approval mode", ActionCategorySelected),
		hotkeyAction(hotkeyQuickFork, "Quick fork", ActionCategorySelected),
		hotkeyAction(hotkeyForkWithOptions, "Fork with options", ActionCategorySelected),
		hotkeyAction(hotkeyCopyOutput, "Copy output", ActionCategorySelected),
		hotkeyAction(hotkeyCopyPane, "Copy pane", ActionCategorySelected),
		hotkeyAction(hotkeySendOutput, "Send output", ActionCategorySelected),
		hotkeyAction(hotkeyExecShell, "Execute shell command", ActionCategorySelected),
		hotkeyAction(hotkeyOpenShellHere, "Open shell here", ActionCategorySelected),
		hotkeyAction(hotkeyEditNotes, "Edit notes", ActionCategorySelected),
		hotkeyAction(hotkeyEditPaths, "Edit paths", ActionCategorySelected),
		hotkeyAction(hotkeyEditSession, "Edit session", ActionCategorySelected),
		hotkeyAction(hotkeyWorktreeSetup, "Set up worktree", ActionCategoryManage),
		hotkeyAction(hotkeyWorktreeFinish, "Finish worktree", ActionCategoryManage),
		hotkeyAction(hotkeyMCPManager, "MCP manager", ActionCategoryManage),
		hotkeyAction(hotkeyPluginManager, "Plugin manager", ActionCategoryManage),
		hotkeyAction(hotkeySkillsManager, "Skills manager", ActionCategoryManage),
		hotkeyAction(hotkeyCreateGroup, "Create group", ActionCategoryManage),
		hotkeyAction(hotkeyWatcherPanel, "Watchers", ActionCategoryManage),
		hotkeyAction(hotkeyNewSession, "New session", ActionCategorySessions),
		hotkeyAction(hotkeyQuickCreate, "Quick create session", ActionCategorySessions),
		hotkeyAction(hotkeySearch, "Search", ActionCategorySessions),
		hotkeyAction(hotkeyImport, "Import session", ActionCategorySessions),
		hotkeyAction(hotkeyReload, "Reload sessions", ActionCategorySessions),
		hotkeyAction(hotkeyTogglePreview, "Toggle preview", ActionCategoryView),
		hotkeyAction(hotkeyCycleGroupView, "Cycle group view", ActionCategoryView),
		hotkeyAction(hotkeyViewArchived, "View archived", ActionCategoryView),
		hotkeyAction(hotkeyUndoDelete, "Undo delete", ActionCategoryView),
		hotkeyAction(hotkeySettings, "Settings", ActionCategoryApp),
		hotkeyAction(hotkeyHelp, "Help", ActionCategoryApp),
		hotkeyAction(hotkeySwitchSession, "Switch session while attached", ActionCategoryApp),
		hotkeyAction(hotkeyDetach, "Return to session list", ActionCategoryApp),
		hotkeyAction(hotkeyQuit, "Quit Agent Deck", ActionCategoryApp),
	}
}
