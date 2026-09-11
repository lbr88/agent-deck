package ui

import (
	"fmt"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func (h *Home) showActionMenu() {
	if h.actionMenu == nil {
		h.actionMenu = NewActionMenu()
	}
	h.actionMenu.SetSize(h.width, h.height)
	h.actionMenu.Show(h.availableActionMenuItems())
}

func (h *Home) handleActionMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if h.actionMenu == nil || !h.actionMenu.IsVisible() {
		return h, nil
	}
	menu, cmd := h.actionMenu.Update(msg)
	h.actionMenu = menu
	if id, ok := h.actionMenu.ConsumeSelection(); ok {
		model, actionCmd := h.dispatchAction(id)
		return model, tea.Batch(cmd, actionCmd)
	}
	return h, cmd
}

// dispatchAction is the common entry point for menu choices and enabled
// shortcuts. handleMainDispatch switches on the stable action ID for action
// commands and on the literal key only for structural navigation controls.
func (h *Home) dispatchAction(id ActionID) (tea.Model, tea.Cmd) {
	if id == "" {
		return h, nil
	}
	if enabled, reason, _ := h.actionAvailability(id); !enabled {
		if reason == "" {
			reason = "action is no longer available"
		}
		h.setError(fmt.Errorf("%s: %s", actionLabel(id), reason))
		return h, nil
	}
	return h.handleMainDispatch(tea.KeyMsg{}, id)
}

func actionLabel(id ActionID) string {
	for _, definition := range actionDefinitions() {
		if definition.ID == id {
			return definition.Label
		}
	}
	return string(id)
}

// actionIDForCanonicalKey converts the already-resolved canonical key into its
// action identity. normalizeMainKey has already rejected disabled shortcuts at
// this point, so raw legacy keys cannot accidentally re-enable an action.
func actionIDForCanonicalKey(key string) ActionID {
	for _, action := range hotkeyActionOrder {
		for _, trigger := range defaultTriggersForAction(action) {
			if key == trigger {
				return ActionID(action)
			}
		}
	}
	return ""
}

func (h *Home) availableActionMenuItems() []ActionMenuItem {
	definitions := actionDefinitions()
	items := make([]ActionMenuItem, 0, len(definitions))
	for _, definition := range definitions {
		enabled, reason, include := h.actionAvailability(definition.ID)
		if !include {
			continue
		}
		items = append(items, ActionMenuItem{
			Action:         definition,
			Enabled:        enabled,
			DisabledReason: reason,
		})
	}
	return items
}

func (h *Home) selectedActionItem() (session.Item, bool) {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) {
		return session.Item{}, false
	}
	item := h.flatItems[h.cursor]
	if item.Type == session.ItemTypeDivider || item.IsCreatingPlaceholder() {
		return item, false
	}
	return item, true
}

// actionAvailability is evaluated both when the menu opens and immediately
// before dispatch, so a background reload cannot make a stale menu selection
// operate on a different or disappeared row.
func (h *Home) actionAvailability(id ActionID) (enabled bool, reason string, include bool) {
	switch id {
	case ActionNewSession, ActionQuickCreate, ActionSearch, ActionImport, ActionReload,
		ActionTogglePreview, ActionCycleGroupView, ActionViewArchived,
		ActionCreateGroup, ActionWatcherPanel, ActionSettings, ActionHelp, ActionQuit,
		ActionNavigateUp, ActionNavigateDown, ActionPageUp, ActionPageDown,
		ActionFullPageUp, ActionFullPageDown, ActionFirstItem, ActionLastItem,
		ActionPreviewSmaller, ActionPreviewLarger, ActionPreviewOrientation,
		ActionQuickOpen, ActionClearFilter, ActionFilterRunning, ActionFilterWaiting,
		ActionFilterIdle, ActionFilterErrorOrCost, ActionFilterOpen,
		ActionJumpRootGroup1, ActionJumpRootGroup2, ActionJumpRootGroup3,
		ActionJumpRootGroup4, ActionJumpRootGroup5, ActionJumpRootGroup6,
		ActionJumpRootGroup7, ActionJumpRootGroup8, ActionJumpRootGroup9,
		ActionJumpMode, ActionHubAdmin, ActionFeedback:
		return true, "", true
	case ActionBulkRemoveErrored:
		for _, inst := range h.instances {
			if inst != nil && inst.Status == session.StatusError {
				return true, "", true
			}
		}
		return false, "No errored local sessions", true
	case ActionCostDashboard:
		if h.costStore == nil {
			return false, "Cost tracking is not configured", true
		}
		return true, "", true
	case ActionUndoDelete:
		if len(h.undoStack) == 0 {
			return false, "Nothing to undo", true
		}
		return true, "", true
	case ActionSwitchSession:
		if len(h.instances) == 0 {
			return false, "No local sessions available", true
		}
		return true, "", true
	case ActionDetach:
		// Detach is structural inside an attached session, not an overview
		// command. Keeping it out avoids a permanently disabled menu row.
		return false, "Already in the session list", false
	}

	item, selected := h.selectedActionItem()
	if !selected {
		return false, "No applicable item selected", false
	}

	isLocalSession := item.Type == session.ItemTypeSession && item.Session != nil
	isLocalGroup := item.Type == session.ItemTypeGroup && item.Group != nil
	isWindow := item.Type == session.ItemTypeWindow
	isRemoteSession := item.Type == session.ItemTypeRemoteSession && item.RemoteSession != nil
	isHubSession := item.Type == session.ItemTypeHubSession && item.HubSession != nil
	isHubGroup := item.Type == session.ItemTypeHubGroup
	tool := ""
	if isLocalSession {
		tool = item.Session.Tool
	} else if isHubSession {
		tool = item.HubSession.Tool
	} else if isWindow {
		tool = item.WindowTool
	}

	supported := false
	switch id {
	case ActionOpen:
		supported = item.Type != session.ItemTypeDivider
	case ActionRename:
		supported = isLocalSession || isLocalGroup || isRemoteSession || isHubSession || isHubGroup || item.Type == session.ItemTypeHubNode
	case ActionRestart:
		supported = isLocalSession || isRemoteSession || isHubSession
	case ActionRestartFresh:
		supported = isLocalSession || isHubSession
	case ActionDelete:
		supported = isLocalSession || isLocalGroup || isRemoteSession || isHubSession || isHubGroup || item.Type == session.ItemTypeHubNode
	case ActionCloseSession:
		supported = isLocalSession || isRemoteSession || isHubSession
	case ActionArchiveSession:
		supported = (isLocalSession && !item.Session.IsArchived()) || (isHubSession && !hubSessionArchived(*item.HubSession))
	case ActionUnarchiveSession:
		supported = h.statusFilter == FilterModeArchived && ((isLocalSession && item.Session.IsArchived()) || (isHubSession && hubSessionArchived(*item.HubSession)))
	case ActionMoveToGroup:
		supported = isLocalSession || isLocalGroup || isHubSession || isHubGroup
	case ActionMCPManager:
		supported = (isLocalSession || isHubSession) && session.ToolSupportsMCPManager(tool)
	case ActionPluginManager:
		supported = (isLocalSession || isHubSession) && session.IsClaudeCompatible(tool)
	case ActionSkillsManager:
		supported = (isLocalSession || isHubSession) && session.SupportsProjectSkills(tool)
	case ActionMarkUnread:
		supported = isLocalSession || isHubSession
	case ActionQuickApprove:
		supported = (isLocalSession || isHubSession || isWindow) && session.IsClaudeCompatible(tool)
	case ActionPromptSession:
		supported = isLocalSession || isHubSession || isWindow
	case ActionToggleYolo:
		supported = (isLocalSession || isHubSession) && (tool == "gemini" || tool == "codex" || tool == "hermes")
	case ActionQuickFork, ActionForkWithOptions:
		supported = (isLocalSession && item.Session.CanFork()) || (isHubSession && item.HubSession.CanFork)
	case ActionCopyOutput, ActionSendOutput:
		supported = isLocalSession || isHubSession
	case ActionCopyPane:
		supported = isLocalSession
	case ActionExecShell:
		supported = isLocalSession || isHubSession
	case ActionOpenShellHere:
		supported = isLocalSession
	case ActionEditNotes:
		supported = isLocalSession || isHubSession
	case ActionEditPaths:
		supported = isLocalSession && item.Session.IsMultiRepo()
	case ActionEditSession:
		supported = isLocalSession || isHubSession
	case ActionHandover:
		supported = isLocalSession
	case ActionOpenNewWindow:
		supported = isLocalSession || isRemoteSession
	case ActionToggleExpand, ActionCollapse:
		supported = item.Type == session.ItemTypeGroup || item.Type == session.ItemTypeRemoteGroup ||
			isLocalSession || isHubSession || isWindow
	case ActionMoveUp, ActionMoveDown:
		supported = isLocalSession || isLocalGroup || isRemoteSession || item.Type == session.ItemTypeRemoteGroup || isHubGroup
	case ActionPromote, ActionDemote, ActionCyclePin:
		supported = isLocalSession
	case ActionRemoveSession:
		supported = isLocalSession || isHubSession
	case ActionInsertMode:
		supported = isLocalSession
	case ActionCopyInfo, ActionCopyCodeBlock:
		supported = isLocalSession || isHubSession
	case ActionGeminiModel:
		supported = isLocalSession && tool == "gemini"
	case ActionNextGroupSession, ActionPreviousGroupSession, ActionFirstGroupSession,
		ActionLastGroupSession, ActionSearchGroup,
		ActionNthGroupSession1, ActionNthGroupSession2, ActionNthGroupSession3,
		ActionNthGroupSession4, ActionNthGroupSession5, ActionNthGroupSession6,
		ActionNthGroupSession7, ActionNthGroupSession8, ActionNthGroupSession9:
		supported = true
	case ActionWorktreeSetup, ActionWorktreeFinish:
		supported = (isLocalSession && item.Session.IsWorktree()) ||
			(isHubSession && strings.TrimSpace(item.HubSession.WorktreePath) != "" && strings.TrimSpace(item.HubSession.WorktreeRepoRoot) != "")
	}

	if supported {
		return true, "", true
	}
	return false, "Not available for the selected item", false
}
