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

// Stable action IDs. The hotkey-backed IDs intentionally match their existing
// config keys so menu dispatch and [hotkeys] cannot drift apart.
const (
	ActionQuit                 ActionID = hotkeyQuit
	ActionNewSession           ActionID = hotkeyNewSession
	ActionQuickCreate          ActionID = hotkeyQuickCreate
	ActionRename               ActionID = hotkeyRename
	ActionRestart              ActionID = hotkeyRestart
	ActionRestartFresh         ActionID = hotkeyRestartFresh
	ActionDelete               ActionID = hotkeyDelete
	ActionCloseSession         ActionID = hotkeyCloseSession
	ActionArchiveSession       ActionID = hotkeyArchiveSession
	ActionUnarchiveSession     ActionID = hotkeyUnarchiveSession
	ActionViewArchived         ActionID = hotkeyViewArchived
	ActionUndoDelete           ActionID = hotkeyUndoDelete
	ActionMoveToGroup          ActionID = hotkeyMoveToGroup
	ActionMCPManager           ActionID = hotkeyMCPManager
	ActionPluginManager        ActionID = hotkeyPluginManager
	ActionSkillsManager        ActionID = hotkeySkillsManager
	ActionTogglePreview        ActionID = hotkeyTogglePreview
	ActionCycleGroupView       ActionID = hotkeyCycleGroupView
	ActionMarkUnread           ActionID = hotkeyMarkUnread
	ActionQuickApprove         ActionID = hotkeyQuickApprove
	ActionPromptSession        ActionID = hotkeyPromptSession
	ActionToggleYolo           ActionID = hotkeyToggleYolo
	ActionQuickFork            ActionID = hotkeyQuickFork
	ActionForkWithOptions      ActionID = hotkeyForkWithOptions
	ActionCopyOutput           ActionID = hotkeyCopyOutput
	ActionCopyPane             ActionID = hotkeyCopyPane
	ActionSendOutput           ActionID = hotkeySendOutput
	ActionExecShell            ActionID = hotkeyExecShell
	ActionOpenShellHere        ActionID = hotkeyOpenShellHere
	ActionEditNotes            ActionID = hotkeyEditNotes
	ActionEditPaths            ActionID = hotkeyEditPaths
	ActionEditSession          ActionID = hotkeyEditSession
	ActionHandover             ActionID = "handover"
	ActionWorktreeSetup        ActionID = hotkeyWorktreeSetup
	ActionWorktreeFinish       ActionID = hotkeyWorktreeFinish
	ActionCreateGroup          ActionID = hotkeyCreateGroup
	ActionSearch               ActionID = hotkeySearch
	ActionHelp                 ActionID = hotkeyHelp
	ActionSettings             ActionID = hotkeySettings
	ActionImport               ActionID = hotkeyImport
	ActionReload               ActionID = hotkeyReload
	ActionDetach               ActionID = hotkeyDetach
	ActionWatcherPanel         ActionID = hotkeyWatcherPanel
	ActionSwitchSession        ActionID = hotkeySwitchSession
	ActionNavigateUp           ActionID = hotkeyNavigateUp
	ActionNavigateDown         ActionID = hotkeyNavigateDown
	ActionPageUp               ActionID = hotkeyPageUp
	ActionPageDown             ActionID = hotkeyPageDown
	ActionFullPageUp           ActionID = hotkeyFullPageUp
	ActionFullPageDown         ActionID = hotkeyFullPageDown
	ActionFirstItem            ActionID = hotkeyFirstItem
	ActionLastItem             ActionID = hotkeyLastItem
	ActionNextGroupSession     ActionID = hotkeyNextGroupSession
	ActionPreviousGroupSession ActionID = hotkeyPreviousGroupSession
	ActionFirstGroupSession    ActionID = hotkeyFirstGroupSession
	ActionLastGroupSession     ActionID = hotkeyLastGroupSession
	ActionSearchGroup          ActionID = hotkeySearchGroup
	ActionOpenNewWindow        ActionID = hotkeyOpenNewWindow
	ActionToggleExpand         ActionID = hotkeyToggleExpand
	ActionCollapse             ActionID = hotkeyCollapse
	ActionMoveUp               ActionID = hotkeyMoveUp
	ActionMoveDown             ActionID = hotkeyMoveDown
	ActionPromote              ActionID = hotkeyPromote
	ActionDemote               ActionID = hotkeyDemote
	ActionCyclePin             ActionID = hotkeyCyclePin
	ActionPreviewSmaller       ActionID = hotkeyPreviewSmaller
	ActionPreviewLarger        ActionID = hotkeyPreviewLarger
	ActionPreviewOrientation   ActionID = hotkeyPreviewOrientation
	ActionQuickOpen            ActionID = hotkeyQuickOpen
	ActionRemoveSession        ActionID = hotkeyRemoveSession
	ActionBulkRemoveErrored    ActionID = hotkeyBulkRemoveErrored
	ActionInsertMode           ActionID = hotkeyInsertMode
	ActionHubAdmin             ActionID = hotkeyHubAdmin
	ActionCopyInfo             ActionID = hotkeyCopyInfo
	ActionCopyCodeBlock        ActionID = hotkeyCopyCodeBlock
	ActionGeminiModel          ActionID = hotkeyGeminiModel
	ActionFeedback             ActionID = hotkeyFeedback
	ActionClearFilter          ActionID = hotkeyClearFilter
	ActionFilterRunning        ActionID = hotkeyFilterRunning
	ActionFilterWaiting        ActionID = hotkeyFilterWaiting
	ActionFilterIdle           ActionID = hotkeyFilterIdle
	ActionFilterErrorOrCost    ActionID = hotkeyFilterErrorOrCost
	ActionCostDashboard        ActionID = hotkeyCostDashboard
	ActionFilterOpen           ActionID = hotkeyFilterOpen
	ActionJumpRootGroup1       ActionID = hotkeyJumpRootGroup1
	ActionJumpRootGroup2       ActionID = hotkeyJumpRootGroup2
	ActionJumpRootGroup3       ActionID = hotkeyJumpRootGroup3
	ActionJumpRootGroup4       ActionID = hotkeyJumpRootGroup4
	ActionJumpRootGroup5       ActionID = hotkeyJumpRootGroup5
	ActionJumpRootGroup6       ActionID = hotkeyJumpRootGroup6
	ActionJumpRootGroup7       ActionID = hotkeyJumpRootGroup7
	ActionJumpRootGroup8       ActionID = hotkeyJumpRootGroup8
	ActionJumpRootGroup9       ActionID = hotkeyJumpRootGroup9
	ActionJumpMode             ActionID = hotkeyJumpMode
	ActionNthGroupSession1     ActionID = hotkeyNthGroupSession1
	ActionNthGroupSession2     ActionID = hotkeyNthGroupSession2
	ActionNthGroupSession3     ActionID = hotkeyNthGroupSession3
	ActionNthGroupSession4     ActionID = hotkeyNthGroupSession4
	ActionNthGroupSession5     ActionID = hotkeyNthGroupSession5
	ActionNthGroupSession6     ActionID = hotkeyNthGroupSession6
	ActionNthGroupSession7     ActionID = hotkeyNthGroupSession7
	ActionNthGroupSession8     ActionID = hotkeyNthGroupSession8
	ActionNthGroupSession9     ActionID = hotkeyNthGroupSession9
	ActionOpen                 ActionID = "open"
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
		{ID: ActionOpen, Label: "Open", Category: ActionCategorySelected, DefaultKey: "enter"},
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
		hotkeyAction(hotkeyHandover, "Hand over to another agent", ActionCategorySelected),
		hotkeyAction(hotkeyOpenNewWindow, "Open in new terminal", ActionCategorySelected),
		hotkeyAction(hotkeyToggleExpand, "Expand or collapse", ActionCategorySelected),
		hotkeyAction(hotkeyCollapse, "Collapse or go to parent", ActionCategorySelected),
		hotkeyAction(hotkeyMoveUp, "Move up", ActionCategorySelected),
		hotkeyAction(hotkeyMoveDown, "Move down", ActionCategorySelected),
		hotkeyAction(hotkeyPromote, "Move out one level", ActionCategorySelected),
		hotkeyAction(hotkeyDemote, "Nest under previous session", ActionCategorySelected),
		hotkeyAction(hotkeyCyclePin, "Change pin position", ActionCategorySelected),
		hotkeyAction(hotkeyRemoveSession, "Remove from registry", ActionCategorySelected),
		hotkeyAction(hotkeyInsertMode, "Type into session", ActionCategorySelected),
		hotkeyAction(hotkeyCopyInfo, "Copy session info", ActionCategorySelected),
		hotkeyAction(hotkeyCopyCodeBlock, "Copy code block", ActionCategorySelected),
		hotkeyAction(hotkeyGeminiModel, "Change Gemini model", ActionCategorySelected),
		hotkeyAction(hotkeyWorktreeSetup, "Set up worktree", ActionCategoryManage),
		hotkeyAction(hotkeyWorktreeFinish, "Finish worktree", ActionCategoryManage),
		hotkeyAction(hotkeyMCPManager, "MCP manager", ActionCategoryManage),
		hotkeyAction(hotkeyPluginManager, "Plugin manager", ActionCategoryManage),
		hotkeyAction(hotkeySkillsManager, "Skills manager", ActionCategoryManage),
		hotkeyAction(hotkeyCreateGroup, "Create group", ActionCategoryManage),
		hotkeyAction(hotkeyWatcherPanel, "Watchers", ActionCategoryManage),
		hotkeyAction(hotkeyHubAdmin, "Hub administration", ActionCategoryManage),
		hotkeyAction(hotkeyBulkRemoveErrored, "Remove all errored sessions", ActionCategoryManage),
		hotkeyAction(hotkeyNewSession, "New session", ActionCategorySessions),
		hotkeyAction(hotkeyQuickCreate, "Quick create session", ActionCategorySessions),
		hotkeyAction(hotkeySearch, "Search", ActionCategorySessions),
		hotkeyAction(hotkeyImport, "Import session", ActionCategorySessions),
		hotkeyAction(hotkeyReload, "Reload sessions", ActionCategorySessions),
		hotkeyAction(hotkeyTogglePreview, "Toggle preview", ActionCategoryView),
		hotkeyAction(hotkeyCycleGroupView, "Cycle group view", ActionCategoryView),
		hotkeyAction(hotkeyViewArchived, "View archived", ActionCategoryView),
		hotkeyAction(hotkeyUndoDelete, "Undo delete", ActionCategoryView),
		hotkeyAction(hotkeyNavigateUp, "Previous row", ActionCategoryView),
		hotkeyAction(hotkeyNavigateDown, "Next row", ActionCategoryView),
		hotkeyAction(hotkeyPageUp, "Half page up", ActionCategoryView),
		hotkeyAction(hotkeyPageDown, "Half page down", ActionCategoryView),
		hotkeyAction(hotkeyFullPageUp, "Full page up", ActionCategoryView),
		hotkeyAction(hotkeyFullPageDown, "Full page down", ActionCategoryView),
		hotkeyAction(hotkeyFirstItem, "First row", ActionCategoryView),
		hotkeyAction(hotkeyLastItem, "Last row", ActionCategoryView),
		hotkeyAction(hotkeyNextGroupSession, "Next session in group", ActionCategoryView),
		hotkeyAction(hotkeyPreviousGroupSession, "Previous session in group", ActionCategoryView),
		hotkeyAction(hotkeyFirstGroupSession, "First session in group", ActionCategoryView),
		hotkeyAction(hotkeyLastGroupSession, "Last session in group", ActionCategoryView),
		hotkeyAction(hotkeySearchGroup, "Search current group", ActionCategoryView),
		hotkeyAction(hotkeyPreviewSmaller, "Make preview smaller", ActionCategoryView),
		hotkeyAction(hotkeyPreviewLarger, "Make preview larger", ActionCategoryView),
		hotkeyAction(hotkeyPreviewOrientation, "Change preview orientation", ActionCategoryView),
		hotkeyAction(hotkeyQuickOpen, "Quick open", ActionCategoryView),
		hotkeyAction(hotkeyClearFilter, "Show all statuses", ActionCategoryView),
		hotkeyAction(hotkeyFilterRunning, "Filter running", ActionCategoryView),
		hotkeyAction(hotkeyFilterWaiting, "Filter waiting", ActionCategoryView),
		hotkeyAction(hotkeyFilterIdle, "Filter idle", ActionCategoryView),
		hotkeyAction(hotkeyFilterErrorOrCost, "Filter errors", ActionCategoryView),
		hotkeyAction(hotkeyCostDashboard, "Cost dashboard", ActionCategoryView),
		hotkeyAction(hotkeyFilterOpen, "Filter open", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup1, "Jump to group 1", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup2, "Jump to group 2", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup3, "Jump to group 3", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup4, "Jump to group 4", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup5, "Jump to group 5", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup6, "Jump to group 6", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup7, "Jump to group 7", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup8, "Jump to group 8", ActionCategoryView),
		hotkeyAction(hotkeyJumpRootGroup9, "Jump to group 9", ActionCategoryView),
		hotkeyAction(hotkeyJumpMode, "Jump by row hint", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession1, "Jump to session 1 in group", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession2, "Jump to session 2 in group", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession3, "Jump to session 3 in group", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession4, "Jump to session 4 in group", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession5, "Jump to session 5 in group", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession6, "Jump to session 6 in group", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession7, "Jump to session 7 in group", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession8, "Jump to session 8 in group", ActionCategoryView),
		hotkeyAction(hotkeyNthGroupSession9, "Jump to session 9 in group", ActionCategoryView),
		hotkeyAction(hotkeySettings, "Settings", ActionCategoryApp),
		hotkeyAction(hotkeyHelp, "Help", ActionCategoryApp),
		hotkeyAction(hotkeyFeedback, "Send feedback", ActionCategoryApp),
		hotkeyAction(hotkeySwitchSession, "Switch session while attached", ActionCategoryApp),
		hotkeyAction(hotkeyDetach, "Return to session list", ActionCategoryApp),
		hotkeyAction(hotkeyQuit, "Quit Agent Deck", ActionCategoryApp),
	}
}
