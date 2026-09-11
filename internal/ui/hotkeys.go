package ui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const (
	hotkeyQuit                 = "quit"
	hotkeyNewSession           = "new_session"
	hotkeyQuickCreate          = "quick_create"
	hotkeyRename               = "rename"
	hotkeyRestart              = "restart"
	hotkeyRestartFresh         = "restart_fresh"
	hotkeyDelete               = "delete"
	hotkeyCloseSession         = "close_session"
	hotkeyArchiveSession       = "archive_session"
	hotkeyUnarchiveSession     = "unarchive_session"
	hotkeyViewArchived         = "view_archived"
	hotkeyUndoDelete           = "undo_delete"
	hotkeyMoveToGroup          = "move_to_group"
	hotkeyMCPManager           = "mcp_manager"
	hotkeyPluginManager        = "plugin_manager"
	hotkeySkillsManager        = "skills_manager"
	hotkeyTogglePreview        = "toggle_preview"
	hotkeyCycleGroupView       = "cycle_group_view"
	hotkeyMarkUnread           = "mark_unread"
	hotkeyQuickApprove         = "quick_approve"
	hotkeyPromptSession        = "prompt_session" // #1410: prompt the highlighted session without attaching
	hotkeyToggleYolo           = "toggle_yolo"
	hotkeyQuickFork            = "quick_fork"
	hotkeyForkWithOptions      = "fork_with_options"
	hotkeyCopyOutput           = "copy_output"
	hotkeyCopyPane             = "copy_pane"
	hotkeySendOutput           = "send_output"
	hotkeyExecShell            = "exec_shell"
	hotkeyOpenShellHere        = "open_shell_here"
	hotkeyEditNotes            = "edit_notes"
	hotkeyEditPaths            = "edit_paths"
	hotkeyEditSession          = "edit_session"
	hotkeyWorktreeSetup        = "worktree_setup"
	hotkeyWorktreeFinish       = "worktree_finish"
	hotkeyCreateGroup          = "create_group"
	hotkeySearch               = "search"
	hotkeyHelp                 = "help"
	hotkeySettings             = "settings"
	hotkeyImport               = "import"
	hotkeyReload               = "reload"
	hotkeyDetach               = "detach"
	hotkeyWatcherPanel         = "watcher_panel"
	hotkeyNavigateUp           = "navigate_up"
	hotkeyNavigateDown         = "navigate_down"
	hotkeyPageUp               = "page_up"
	hotkeyPageDown             = "page_down"
	hotkeyFullPageUp           = "full_page_up"
	hotkeyFullPageDown         = "full_page_down"
	hotkeyFirstItem            = "first_item"
	hotkeyLastItem             = "last_item"
	hotkeyNextGroupSession     = "next_group_session"
	hotkeyPreviousGroupSession = "previous_group_session"
	hotkeyFirstGroupSession    = "first_group_session"
	hotkeyLastGroupSession     = "last_group_session"
	hotkeySearchGroup          = "search_group"
	hotkeyOpenNewWindow        = "open_new_window"
	hotkeyToggleExpand         = "toggle_expand"
	hotkeyCollapse             = "collapse"
	hotkeyMoveUp               = "move_up"
	hotkeyMoveDown             = "move_down"
	hotkeyPromote              = "promote"
	hotkeyDemote               = "demote"
	hotkeyCyclePin             = "cycle_pin"
	hotkeyPreviewSmaller       = "preview_smaller"
	hotkeyPreviewLarger        = "preview_larger"
	hotkeyPreviewOrientation   = "preview_orientation"
	hotkeyQuickOpen            = "quick_open"
	hotkeyRemoveSession        = "remove_session"
	hotkeyBulkRemoveErrored    = "bulk_remove_errored"
	hotkeyInsertMode           = "insert_mode"
	hotkeyHubAdmin             = "hub_admin"
	hotkeyHandover             = "handover"
	hotkeyCopyInfo             = "copy_info"
	hotkeyCopyCodeBlock        = "copy_code_block"
	hotkeyGeminiModel          = "gemini_model"
	hotkeyFeedback             = "feedback"
	hotkeyClearFilter          = "clear_filter"
	hotkeyFilterRunning        = "filter_running"
	hotkeyFilterWaiting        = "filter_waiting"
	hotkeyFilterIdle           = "filter_idle"
	hotkeyFilterErrorOrCost    = "filter_error_or_cost"
	hotkeyCostDashboard        = "cost_dashboard"
	hotkeyFilterOpen           = "filter_open"
	hotkeyJumpRootGroup1       = "jump_root_group_1"
	hotkeyJumpRootGroup2       = "jump_root_group_2"
	hotkeyJumpRootGroup3       = "jump_root_group_3"
	hotkeyJumpRootGroup4       = "jump_root_group_4"
	hotkeyJumpRootGroup5       = "jump_root_group_5"
	hotkeyJumpRootGroup6       = "jump_root_group_6"
	hotkeyJumpRootGroup7       = "jump_root_group_7"
	hotkeyJumpRootGroup8       = "jump_root_group_8"
	hotkeyJumpRootGroup9       = "jump_root_group_9"
	hotkeyJumpMode             = "jump_mode"
	hotkeyNthGroupSession1     = "nth_group_session_1"
	hotkeyNthGroupSession2     = "nth_group_session_2"
	hotkeyNthGroupSession3     = "nth_group_session_3"
	hotkeyNthGroupSession4     = "nth_group_session_4"
	hotkeyNthGroupSession5     = "nth_group_session_5"
	hotkeyNthGroupSession6     = "nth_group_session_6"
	hotkeyNthGroupSession7     = "nth_group_session_7"
	hotkeyNthGroupSession8     = "nth_group_session_8"
	hotkeyNthGroupSession9     = "nth_group_session_9"
	// Session switcher. While attached it is intercepted in the tmux attach
	// loop (see internal/tmux/pty.go AttachOptions); on the home screen it is
	// dispatched like any other hotkey. Must resolve to a "ctrl+<letter>" chord.
	//
	// Disabled by default (see defaultDisabledHotkeys): intercepting it while
	// attached steals the control byte from the attached program, and the
	// suggested Ctrl+S collides with Claude Code (stash prompt) and XOFF
	// flow-control. Users opt in by binding [hotkeys].switch_session.
	hotkeySwitchSession = "switch_session" // canonical "ctrl+s" (opt-in)
	// Scrollback pager. While attached to a session from the deck (Enter), the
	// deck owns the viewport so tmux's own copy-mode/scrollback is unreachable
	// (#1491). This trigger, intercepted in the attach loop, opens an in-view
	// scrollable pager rendered from the pane's history. Unlike other hotkeys
	// its value is not a home-screen key: it is "pageup" (default), a
	// "ctrl+<letter>" chord, or "" (disabled). Resolved by
	// ResolvedScrollbackTrigger, kept out of the home-screen dispatch maps.
	hotkeyScrollback = "scrollback"
)

// defaultScrollbackTrigger is the out-of-the-box scrollback trigger: a bare
// PageUp, exactly the key the #1491 reporter pressed expecting to scroll.
const defaultScrollbackTrigger = "pageup"

var hotkeyActionOrder = []string{
	hotkeyQuit,
	hotkeyNewSession,
	hotkeyQuickCreate,
	hotkeyRename,
	hotkeyRestart,
	hotkeyRestartFresh,
	hotkeyDelete,
	hotkeyCloseSession,
	hotkeyArchiveSession,
	hotkeyUnarchiveSession,
	hotkeyViewArchived,
	hotkeyUndoDelete,
	hotkeyMoveToGroup,
	hotkeyMCPManager,
	hotkeyPluginManager,
	hotkeySkillsManager,
	hotkeyTogglePreview,
	hotkeyCycleGroupView,
	hotkeyMarkUnread,
	hotkeyQuickApprove,
	hotkeyPromptSession,
	hotkeyToggleYolo,
	hotkeyQuickFork,
	hotkeyForkWithOptions,
	hotkeyCopyOutput,
	hotkeyCopyPane,
	hotkeySendOutput,
	hotkeyExecShell,
	hotkeyOpenShellHere,
	hotkeyEditNotes,
	hotkeyEditPaths,
	hotkeyEditSession,
	hotkeyWorktreeSetup,
	hotkeyWorktreeFinish,
	hotkeyCreateGroup,
	hotkeySearch,
	hotkeyHelp,
	hotkeySettings,
	hotkeyImport,
	hotkeyReload,
	hotkeyDetach,
	hotkeyWatcherPanel,
	hotkeySwitchSession,
	hotkeyNavigateUp,
	hotkeyNavigateDown,
	hotkeyPageUp,
	hotkeyPageDown,
	hotkeyFullPageUp,
	hotkeyFullPageDown,
	hotkeyFirstItem,
	hotkeyLastItem,
	hotkeyNextGroupSession,
	hotkeyPreviousGroupSession,
	hotkeyFirstGroupSession,
	hotkeyLastGroupSession,
	hotkeySearchGroup,
	hotkeyOpenNewWindow,
	hotkeyToggleExpand,
	hotkeyCollapse,
	hotkeyMoveUp,
	hotkeyMoveDown,
	hotkeyPromote,
	hotkeyDemote,
	hotkeyCyclePin,
	hotkeyPreviewSmaller,
	hotkeyPreviewLarger,
	hotkeyPreviewOrientation,
	hotkeyQuickOpen,
	hotkeyRemoveSession,
	hotkeyBulkRemoveErrored,
	hotkeyInsertMode,
	hotkeyHubAdmin,
	hotkeyHandover,
	hotkeyCopyInfo,
	hotkeyCopyCodeBlock,
	hotkeyGeminiModel,
	hotkeyFeedback,
	hotkeyClearFilter,
	hotkeyFilterRunning,
	hotkeyFilterWaiting,
	hotkeyFilterIdle,
	hotkeyFilterErrorOrCost,
	hotkeyCostDashboard,
	hotkeyFilterOpen,
	hotkeyJumpRootGroup1,
	hotkeyJumpRootGroup2,
	hotkeyJumpRootGroup3,
	hotkeyJumpRootGroup4,
	hotkeyJumpRootGroup5,
	hotkeyJumpRootGroup6,
	hotkeyJumpRootGroup7,
	hotkeyJumpRootGroup8,
	hotkeyJumpRootGroup9,
	hotkeyJumpMode,
	hotkeyNthGroupSession1,
	hotkeyNthGroupSession2,
	hotkeyNthGroupSession3,
	hotkeyNthGroupSession4,
	hotkeyNthGroupSession5,
	hotkeyNthGroupSession6,
	hotkeyNthGroupSession7,
	hotkeyNthGroupSession8,
	hotkeyNthGroupSession9,
}

var defaultHotkeyBindings = map[string]string{
	hotkeyQuit:                 "q",
	hotkeyNewSession:           "n",
	hotkeyQuickCreate:          "N",
	hotkeyRename:               "r",
	hotkeyRestart:              "R",
	hotkeyRestartFresh:         "T",
	hotkeyDelete:               "d",
	hotkeyCloseSession:         "D",
	hotkeyArchiveSession:       "A",
	hotkeyUnarchiveSession:     "shift+u",
	hotkeyViewArchived:         "^",
	hotkeyUndoDelete:           "ctrl+z",
	hotkeyMoveToGroup:          "M",
	hotkeyMCPManager:           "m",
	hotkeyPluginManager:        "L",
	hotkeySkillsManager:        "s",
	hotkeyTogglePreview:        "v",
	hotkeyCycleGroupView:       "t",
	hotkeyMarkUnread:           "u",
	hotkeyQuickApprove:         "a",
	hotkeyPromptSession:        "o",
	hotkeyToggleYolo:           "y",
	hotkeyQuickFork:            "f",
	hotkeyForkWithOptions:      "F",
	hotkeyCopyOutput:           "c",
	hotkeyCopyPane:             "V",
	hotkeySendOutput:           "x",
	hotkeyExecShell:            "E",
	hotkeyOpenShellHere:        "H",
	hotkeyEditNotes:            "e",
	hotkeyEditPaths:            "p",
	hotkeyEditSession:          "P",
	hotkeyWorktreeSetup:        "b",
	hotkeyWorktreeFinish:       "W",
	hotkeyCreateGroup:          "g",
	hotkeySearch:               "/",
	hotkeyHelp:                 "?",
	hotkeySettings:             "S",
	hotkeyImport:               "i",
	hotkeyReload:               "ctrl+r",
	hotkeyDetach:               "ctrl+q",
	hotkeyWatcherPanel:         "w",
	hotkeySwitchSession:        "ctrl+s",
	hotkeyNavigateUp:           "k",
	hotkeyNavigateDown:         "j",
	hotkeyPageUp:               "pgup",
	hotkeyPageDown:             "pgdown",
	hotkeyFullPageUp:           "ctrl+b",
	hotkeyFullPageDown:         "ctrl+f",
	hotkeyFirstItem:            "home",
	hotkeyLastItem:             "end",
	hotkeyNextGroupSession:     "alt+j",
	hotkeyPreviousGroupSession: "alt+k",
	hotkeyFirstGroupSession:    "alt+g",
	hotkeyLastGroupSession:     "alt+G",
	hotkeySearchGroup:          "alt+/",
	hotkeyOpenNewWindow:        "shift+enter",
	hotkeyToggleExpand:         "tab",
	hotkeyCollapse:             "h",
	hotkeyMoveUp:               "shift+up",
	hotkeyMoveDown:             "shift+down",
	hotkeyPromote:              "shift+left",
	hotkeyDemote:               "shift+right",
	hotkeyCyclePin:             ",",
	hotkeyPreviewSmaller:       "<",
	hotkeyPreviewLarger:        ">",
	hotkeyPreviewOrientation:   "O",
	hotkeyQuickOpen:            "z",
	hotkeyRemoveSession:        "X",
	hotkeyBulkRemoveErrored:    "ctrl+x",
	hotkeyInsertMode:           "I",
	hotkeyHubAdmin:             "alt+a",
	hotkeyHandover:             "alt+h",
	hotkeyCopyInfo:             "C",
	hotkeyCopyCodeBlock:        "Y",
	hotkeyGeminiModel:          "ctrl+g",
	hotkeyFeedback:             "ctrl+e",
	hotkeyClearFilter:          "0",
	hotkeyFilterRunning:        "!",
	hotkeyFilterWaiting:        "@",
	hotkeyFilterIdle:           "#",
	hotkeyFilterErrorOrCost:    "$",
	hotkeyCostDashboard:        "alt+$",
	hotkeyFilterOpen:           "%",
	hotkeyJumpRootGroup1:       "1",
	hotkeyJumpRootGroup2:       "2",
	hotkeyJumpRootGroup3:       "3",
	hotkeyJumpRootGroup4:       "4",
	hotkeyJumpRootGroup5:       "5",
	hotkeyJumpRootGroup6:       "6",
	hotkeyJumpRootGroup7:       "7",
	hotkeyJumpRootGroup8:       "8",
	hotkeyJumpRootGroup9:       "9",
	hotkeyJumpMode:             "alt+space",
	hotkeyNthGroupSession1:     "alt+1",
	hotkeyNthGroupSession2:     "alt+2",
	hotkeyNthGroupSession3:     "alt+3",
	hotkeyNthGroupSession4:     "alt+4",
	hotkeyNthGroupSession5:     "alt+5",
	hotkeyNthGroupSession6:     "alt+6",
	hotkeyNthGroupSession7:     "alt+7",
	hotkeyNthGroupSession8:     "alt+8",
	hotkeyNthGroupSession9:     "alt+9",
}

var hotkeyActionDefaultTriggers = map[string][]string{
	hotkeyQuit:            {"q", "ctrl+c"},
	hotkeyForkWithOptions: {"F", "shift+f"},
	hotkeyMoveToGroup:     {"M", "shift+m"},
	hotkeyWorktreeFinish:  {"W", "shift+w"},
	hotkeyEditSession:     {"P", "shift+p"},
	hotkeyNavigateUp:      {"k", "ctrl+p"},
	hotkeyNavigateDown:    {"j", "ctrl+n"},
	hotkeyPageUp:          {"pgup", "ctrl+u"},
	hotkeyPageDown:        {"pgdown", "ctrl+d"},
	hotkeyToggleExpand:    {"tab", "l"},
	hotkeyMoveUp:          {"shift+up", "ctrl+up", "+", "K"},
	hotkeyMoveDown:        {"shift+down", "ctrl+down", "-", "J"},
	hotkeySearch:          {"/", "G"},
}

// renamedHotkeys maps old action names to new names for backward compatibility.
var renamedHotkeys = map[string]string{
	"toggle_gemini_yolo": hotkeyToggleYolo,
}

// defaultDisabledHotkeys are actions that keep a canonical key in
// defaultHotkeyBindings (so the home-screen dispatch case and help/status
// labels resolve) but ship UNBOUND: resolveHotkeys drops them unless the user
// binds them explicitly. switch_session is opt-in because enabling it
// intercepts a control byte in the attach loop before the attached program
// sees it — the suggested Ctrl+S collides with Claude Code's stash-prompt and
// terminal XOFF flow-control, and no control byte is safe to steal from every
// attached tool.
var defaultDisabledHotkeys = map[string]bool{
	hotkeySwitchSession: true,
	// These actions had no unambiguous reachable legacy shortcut: H was already
	// consumed by open_shell_here, while handover lived behind the overloaded
	// P-prefix. They are menu-first and may be enabled explicitly.
	hotkeyHubAdmin: true,
	hotkeyHandover: true,
}

func resolveHotkeys(overrides map[string]string) map[string]string {
	bindings := make(map[string]string, len(defaultHotkeyBindings))
	for action, key := range defaultHotkeyBindings {
		bindings[action] = key
	}

	overrideActions := make([]string, 0, len(overrides))
	for action := range overrides {
		overrideActions = append(overrideActions, action)
	}
	sort.Strings(overrideActions)

	canonicalOverrides := make(map[string]string, len(overrides))
	for _, action := range overrideActions {
		key := overrides[action]
		normalizedAction := strings.TrimSpace(strings.ToLower(action))
		normalizedKey := strings.TrimSpace(key)
		if isReservedOverviewBinding(normalizedKey) {
			continue
		}

		if _, ok := defaultHotkeyBindings[normalizedAction]; ok {
			canonicalOverrides[normalizedAction] = normalizedKey
		}
	}
	for _, action := range overrideActions {
		key := overrides[action]
		normalizedAction := strings.TrimSpace(strings.ToLower(action))
		newName, ok := renamedHotkeys[normalizedAction]
		if !ok {
			continue
		}
		if _, exists := canonicalOverrides[newName]; exists {
			continue
		}
		normalizedKey := strings.TrimSpace(key)
		if isReservedOverviewBinding(normalizedKey) {
			continue
		}
		canonicalOverrides[newName] = normalizedKey
	}

	for action, key := range canonicalOverrides {
		if key == "" {
			delete(bindings, action)
			continue
		}
		bindings[action] = key
	}

	// Opt-in actions ship unbound: drop them unless the user set them
	// explicitly. The canonical default stays in defaultHotkeyBindings so the
	// dispatch case and labels resolve once a user binds it.
	for action := range defaultDisabledHotkeys {
		if _, overridden := canonicalOverrides[action]; !overridden {
			delete(bindings, action)
		}
	}

	return bindings
}

// resolveHotkeysForMode applies either the historical implicit bindings or the
// menu-first policy where only explicit bindings are enabled. Detach remains
// structural in both modes so an attached session always has an escape route.
func resolveHotkeysForMode(overrides map[string]string, mode string) map[string]string {
	if strings.EqualFold(strings.TrimSpace(mode), shortcutModeLegacy) {
		return resolveHotkeys(overrides)
	}

	bindings := make(map[string]string)
	for rawAction, rawKey := range overrides {
		action := strings.TrimSpace(strings.ToLower(rawAction))
		if renamed, ok := renamedHotkeys[action]; ok {
			action = renamed
		}
		if _, ok := defaultHotkeyBindings[action]; !ok {
			continue
		}
		if key := strings.TrimSpace(rawKey); key != "" && !isReservedOverviewBinding(key) {
			bindings[action] = key
		}
	}
	if strings.TrimSpace(bindings[hotkeyDetach]) == "" {
		bindings[hotkeyDetach] = defaultHotkeyBindings[hotkeyDetach]
	}
	return bindings
}

func validateHotkeyBindings(bindings map[string]string) error {
	seen := make(map[string]string)
	for action, rawKey := range bindings {
		if _, ok := defaultHotkeyBindings[action]; !ok {
			return fmt.Errorf("unknown shortcut action %q", action)
		}
		key := strings.TrimSpace(rawKey)
		if key == "" {
			continue
		}
		if isReservedOverviewBinding(key) {
			return fmt.Errorf("shortcut %q cannot use reserved overview key %q", action, key)
		}
		if !supportedHotkeyBinding(key) {
			return fmt.Errorf("shortcut %q uses unsupported key %q", action, rawKey)
		}
		for _, alias := range hotkeyAliases(normalizeHotkeyBinding(key)) {
			if previous, ok := seen[alias]; ok && previous != action {
				return fmt.Errorf("shortcut %q conflicts with %q on %q", action, previous, key)
			}
			seen[alias] = action
		}
	}
	return nil
}

func isReservedOverviewBinding(key string) bool {
	switch strings.ToLower(normalizeHotkeyBinding(key)) {
	case "space", "up", "down", "left", "right", "enter", "esc":
		return true
	default:
		return false
	}
}

func normalizeHotkeyBinding(key string) string {
	key = strings.TrimSpace(key)
	if len([]rune(key)) == 1 {
		return key
	}
	parts := strings.Split(key, "+")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if i < len(parts)-1 || len([]rune(parts[i])) != 1 {
			parts[i] = strings.ToLower(parts[i])
		}
	}
	return strings.Join(parts, "+")
}

func supportedHotkeyBinding(key string) bool {
	key = normalizeHotkeyBinding(key)
	if runes := []rune(key); len(runes) == 1 {
		return !unicode.IsControl(runes[0]) && !unicode.IsSpace(runes[0])
	}
	parts := strings.Split(key, "+")
	if len(parts) < 2 || len(parts) > 4 {
		return supportedNamedHotkey(key)
	}
	modifiers := make(map[string]bool)
	for _, modifier := range parts[:len(parts)-1] {
		if modifier != "ctrl" && modifier != "alt" && modifier != "shift" || modifiers[modifier] {
			return false
		}
		modifiers[modifier] = true
	}
	base := parts[len(parts)-1]
	return len([]rune(base)) == 1 || supportedNamedHotkey(base)
}

func supportedNamedHotkey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "backspace", "delete", "insert", "home", "end", "pgup", "pgdown", "pageup", "pagedown",
		"tab", "enter", "esc", "space", "up", "down", "left", "right":
		return true
	default:
		return false
	}
}

func buildHotkeyLookup(bindings map[string]string) (map[string]string, map[string]bool) {
	keyToCanonical := make(map[string]string, len(bindings))
	blockedCanonical := make(map[string]bool)

	for _, action := range hotkeyActionOrder {
		canonical := defaultHotkeyBindings[action]
		bound := strings.TrimSpace(bindings[action])
		defaultTriggers := defaultTriggersForAction(action)
		if bound == "" {
			for _, trigger := range defaultTriggers {
				blockedCanonical[trigger] = true
			}
			continue
		}
		if bound != canonical {
			for _, trigger := range defaultTriggers {
				blockedCanonical[trigger] = true
			}
		}
		for _, alias := range hotkeyAliases(bound) {
			if _, exists := keyToCanonical[alias]; !exists {
				keyToCanonical[alias] = canonical
			}
		}
	}

	return keyToCanonical, blockedCanonical
}

func defaultTriggersForAction(action string) []string {
	if triggers, ok := hotkeyActionDefaultTriggers[action]; ok {
		return triggers
	}
	return hotkeyAliases(defaultHotkeyBindings[action])
}

func hotkeyAliases(key string) []string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return nil
	}

	aliases := []string{trimmed}
	seen := map[string]bool{trimmed: true}
	add := func(alias string) {
		alias = strings.TrimSpace(alias)
		if alias == "" || seen[alias] {
			return
		}
		seen[alias] = true
		aliases = append(aliases, alias)
	}

	if shiftAlias := shiftedAliasFor(trimmed); shiftAlias != "" {
		add(shiftAlias)
	}
	if unshiftedAlias := unshiftedAliasFor(trimmed); unshiftedAlias != "" {
		add(unshiftedAlias)
	}

	return aliases
}

func shiftedAliasFor(key string) string {
	runes := []rune(key)
	if len(runes) != 1 {
		return ""
	}

	r := runes[0]
	if unicode.IsUpper(r) {
		return "shift+" + strings.ToLower(string(r))
	}

	switch r {
	case '!':
		return "shift+1"
	case '@':
		return "shift+2"
	case '#':
		return "shift+3"
	case '$':
		return "shift+4"
	case '%':
		return "shift+5"
	case '^':
		return "shift+6"
	case '&':
		return "shift+7"
	case '*':
		return "shift+8"
	case '(':
		return "shift+9"
	case ')':
		return "shift+0"
	}

	return ""
}

func unshiftedAliasFor(key string) string {
	lower := strings.ToLower(strings.TrimSpace(key))
	if !strings.HasPrefix(lower, "shift+") {
		return ""
	}

	base := strings.TrimSpace(lower[len("shift+"):])
	runes := []rune(base)
	if len(runes) != 1 {
		return ""
	}

	r := runes[0]
	if unicode.IsLetter(r) {
		return strings.ToUpper(string(r))
	}

	switch r {
	case '1':
		return "!"
	case '2':
		return "@"
	case '3':
		return "#"
	case '4':
		return "$"
	case '5':
		return "%"
	case '6':
		return "^"
	case '7':
		return "&"
	case '8':
		return "*"
	case '9':
		return "("
	case '0':
		return ")"
	}

	return ""
}

func actionHotkey(bindings map[string]string, action string) string {
	if bindings == nil {
		return ""
	}
	return strings.TrimSpace(bindings[action])
}

func joinHotkeyLabels(keys ...string) string {
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return strings.Join(filtered, "/")
}

// DetachByteFromBinding converts a hotkey binding string (e.g. "ctrl+q") to the
// corresponding ASCII byte used by the PTY attach loop. Returns 0x11 (Ctrl+Q) as
// the default when the binding cannot be mapped.
func DetachByteFromBinding(binding string) byte {
	binding = strings.ToLower(strings.TrimSpace(binding))
	if !strings.HasPrefix(binding, "ctrl+") {
		return 17 // default Ctrl+Q
	}
	ch := binding[len("ctrl+"):]
	if len(ch) == 1 && ch[0] >= 'a' && ch[0] <= 'z' {
		return ch[0] - 'a' + 1
	}
	switch ch {
	case "\\":
		return 0x1C
	case "]":
		return 0x1D
	case "^":
		return 0x1E
	case "_":
		return 0x1F
	}
	return 17 // default Ctrl+Q
}

// DetachByteLabel returns a human-readable label for a detach byte (e.g. "Ctrl+Q").
func DetachByteLabel(b byte) string {
	if b >= 1 && b <= 26 {
		return fmt.Sprintf("Ctrl+%c", 'A'+b-1)
	}
	switch b {
	case 0x1C:
		return "Ctrl+\\"
	case 0x1D:
		return "Ctrl+]"
	case 0x1E:
		return "Ctrl+^"
	case 0x1F:
		return "Ctrl+_"
	}
	return "Ctrl+Q"
}

// ResolvedDetachByte returns the detach byte for the current hotkey configuration.
func ResolvedDetachByte(overrides map[string]string) byte {
	bindings := resolveHotkeys(overrides)
	key := actionHotkey(bindings, hotkeyDetach)
	if key == "" {
		return 17 // default Ctrl+Q
	}
	return DetachByteFromBinding(key)
}

// ctrlByteFromBinding converts a "ctrl+<letter>" binding to its control byte, or
// returns 0 when the binding is not a single-control-key chord. Unlike
// DetachByteFromBinding it does not fall back to Ctrl+Q, so callers can treat 0
// as "no portable byte for this key" (e.g. "ctrl+tab" / "ctrl+shift+tab", which
// have no legacy control byte).
func ctrlByteFromBinding(binding string) byte {
	binding = strings.ToLower(strings.TrimSpace(binding))
	if !strings.HasPrefix(binding, "ctrl+") {
		return 0
	}
	ch := binding[len("ctrl+"):]
	if len(ch) == 1 && ch[0] >= 'a' && ch[0] <= 'z' {
		return ch[0] - 'a' + 1
	}
	switch ch {
	case "\\":
		return 0x1C
	case "]":
		return 0x1D
	case "^":
		return 0x1E
	case "_":
		return 0x1F
	}
	return 0
}

// ResolvedSwitchByte returns the control byte that opens the in-attach session
// switcher for the current hotkey overrides, or 0 when it is unbound or not a
// ctrl+<letter> chord. The switcher's forward/backward cycling and commit are
// handled in the TUI, so only this single opener byte reaches the attach loop.
func ResolvedSwitchByte(overrides map[string]string) byte {
	bindings := resolveHotkeys(overrides)
	return ctrlByteFromBinding(actionHotkey(bindings, hotkeySwitchSession))
}

// ScrollbackTrigger describes how the in-attach scrollback pager is opened.
// The zero value means scrollback is disabled.
type ScrollbackTrigger struct {
	// KeyByte is a ctrl+<letter> control byte that opens the pager, or 0.
	KeyByte byte
	// OnPageUp reports whether a bare PageUp opens the pager.
	OnPageUp bool
}

// Enabled reports whether any trigger is configured.
func (t ScrollbackTrigger) Enabled() bool {
	return t.KeyByte != 0 || t.OnPageUp
}

// Label returns a short human-readable label for the configured trigger
// (e.g. "PageUp", "Ctrl+G"), or "" when disabled.
func (t ScrollbackTrigger) Label() string {
	switch {
	case t.OnPageUp:
		return "PageUp"
	case t.KeyByte != 0:
		return DetachByteLabel(t.KeyByte)
	default:
		return ""
	}
}

// ResolvedScrollbackTrigger resolves the in-attach scrollback trigger from the
// hotkey overrides. The [hotkeys].scrollback value is one of:
//   - "pageup"       — bare PageUp opens the pager (the default),
//   - "ctrl+<letter>" — that chord opens the pager,
//   - ""             — scrollback disabled.
//
// An unrecognized value falls back to the PageUp default rather than silently
// disabling the feature. It is resolved directly from overrides (not through
// resolveHotkeys) because its value is not a home-screen key and must stay out
// of the home-screen dispatch lookup.
func ResolvedScrollbackTrigger(overrides map[string]string) ScrollbackTrigger {
	val := defaultScrollbackTrigger
	for action, key := range overrides {
		if strings.TrimSpace(strings.ToLower(action)) == hotkeyScrollback {
			val = strings.TrimSpace(strings.ToLower(key))
			break
		}
	}
	switch val {
	case "":
		return ScrollbackTrigger{}
	case "pageup":
		return ScrollbackTrigger{OnPageUp: true}
	default:
		if b := ctrlByteFromBinding(val); b != 0 {
			return ScrollbackTrigger{KeyByte: b}
		}
		return ScrollbackTrigger{OnPageUp: true}
	}
}
