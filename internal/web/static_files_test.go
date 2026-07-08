package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndexCacheControl(t *testing.T) {
	s := NewServer(Config{Token: "test-token"})
	req := httptest.NewRequest(http.MethodGet, "/?token=test-token", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control missing no-cache: %q", cc)
	}
}

func TestIndexImportMap(t *testing.T) {
	s := NewServer(Config{Token: "test-token"})
	req := httptest.NewRequest(http.MethodGet, "/?token=test-token", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`"preact"`,
		`"preact/hooks"`,
		`"htm/preact"`,
		`"@preact/signals"`,
		`"@xterm/xterm"`,
		`"@xterm/addon-fit"`,
		`"@xterm/addon-webgl"`,
		`<script type="importmap">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
}

func TestIndexThemeInit(t *testing.T) {
	s := NewServer(Config{Token: "test-token"})
	req := httptest.NewRequest(http.MethodGet, "/?token=test-token", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "localStorage.getItem('theme')") {
		t.Error("index.html missing theme init script")
	}
	// theme init must appear before importmap
	themeIdx := strings.Index(body, "localStorage.getItem('theme')")
	importIdx := strings.Index(body, `<script type="importmap">`)
	if themeIdx > importIdx {
		t.Error("theme init script must appear before importmap")
	}
}

func TestIndexNoCDN(t *testing.T) {
	s := NewServer(Config{Token: "test-token"})
	req := httptest.NewRequest(http.MethodGet, "/?token=test-token", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)
	body := w.Body.String()
	if strings.Contains(body, "cdn.jsdelivr.net") {
		t.Error("index.html must not reference CDN URLs")
	}
}

func TestWebUIAllowsForkButtonForForkableHubSessions(t *testing.T) {
	for _, file := range []string{
		"static/app/AppShell.js",
		"static/app/Sidebar.js",
	} {
		data, err := embeddedStaticFiles.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", file, err)
		}
		body := string(data)
		if strings.Contains(body, "!session.isHub && session.canFork") || strings.Contains(body, "!s.isHub && s.canFork") {
			t.Fatalf("%s still hides fork actions for hub sessions", file)
		}
		if !strings.Contains(body, "session.canFork") && !strings.Contains(body, "s.canFork") {
			t.Fatalf("%s does not render fork action from canFork", file)
		}
	}
}

func TestFleetPaneSortsGroupTilesByStatus(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/app/panes/FleetPane.js")
	if err != nil {
		t.Fatalf("ReadFile(FleetPane.js): %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"FLEET_STATUS_PRIORITY",
		"error: 0",
		"waiting: 1",
		"running: 2",
		"sortedFleetItems(items)",
		"sortedItems.slice(0, 6)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("FleetPane.js missing %q; fleet group cards must sort visible tiles by status before slicing", want)
		}
	}
}

func TestWebDashboardSwitcherUsesOnlyWebAvailableHubNodes(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/app/Topbar.js")
	if err != nil {
		t.Fatalf("ReadFile(Topbar.js): %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"hubNodesSignal",
		"dashboardHubNodes(hubNodesSignal.value)",
		"for (const n of hubNodes || [])",
		"n.webAvailable === true",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Topbar.js missing %q; dashboard switch targets must be filtered to nodes with remote web available", want)
		}
	}
	for _, forbidden := range []string{
		"for (const g of groups)",
		"for (const s of sessions)",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Topbar.js still infers dashboard targets from sessions/groups via %q", forbidden)
		}
	}
}

func TestWebSidebarSessionActionsUseMoreMenu(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/app/Sidebar.js")
	if err != nil {
		t.Fatalf("ReadFile(Sidebar.js): %v", err)
	}
	body := string(data)
	for _, want := range []string{
		`data-testid="session-more-btn"`,
		`data-testid="session-more-menu"`,
		`Prompt without attaching`,
		`Sandbox shell`,
		`Restart fresh`,
		`Move group`,
		`Delete`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Sidebar.js missing %q; session rows must keep uncommon actions behind More", want)
		}
	}
	css, err := embeddedStaticFiles.ReadFile("static/app/app.css")
	if err != nil {
		t.Fatalf("ReadFile(app.css): %v", err)
	}
	style := string(css)
	for _, want := range []string{
		".sess .actions .more-menu",
		`aria-haspopup="menu"`,
		".sess .actions .more-menu:hover",
	} {
		if !strings.Contains(style+body, want) {
			t.Fatalf("sidebar action menu CSS/markup missing %q", want)
		}
	}
}

func TestWebHubAdminManagementSurface(t *testing.T) {
	topbar, err := embeddedStaticFiles.ReadFile("static/app/Topbar.js")
	if err != nil {
		t.Fatalf("ReadFile(Topbar.js): %v", err)
	}
	for _, want := range []string{
		"hubNodesDialogSignal",
		"hubAdminSignal",
		"hubAdmin &&",
		"data-testid=\"hub-nodes-btn\"",
		"Manage hub nodes",
	} {
		if !strings.Contains(string(topbar), want) {
			t.Fatalf("Topbar.js missing %q; hub admins need an entry point to rename nodes", want)
		}
	}

	appShell, err := embeddedStaticFiles.ReadFile("static/app/AppShell.js")
	if err != nil {
		t.Fatalf("ReadFile(AppShell.js): %v", err)
	}
	for _, want := range []string{
		"HubNodesDialog",
		"hubNodesDialogSignal",
		"hubAdminSignal.value === true && hubNodesDialogSignal.value",
		"data.hubAdmin !== true",
		"hubNodesDialogSignal.value = false",
	} {
		if !strings.Contains(string(appShell), want) {
			t.Fatalf("AppShell.js missing %q; hub node dialog must be mounted only for hub admins", want)
		}
	}

	dialog, err := embeddedStaticFiles.ReadFile("static/app/HubNodesDialog.js")
	if err != nil {
		t.Fatalf("ReadFile(HubNodesDialog.js): %v", err)
	}
	for _, want := range []string{
		"/api/hub/nodes",
		"PATCH",
		"DELETE",
		"/promote",
		"/demote",
		"hub-node-rename-btn",
		"hub-node-promote-btn",
		"hub-node-demote-btn",
		"hub-node-revoke-btn",
		"/api/hub/invites",
		"/api/hub/trust/pending",
		"/api/hub/trust/",
		"hub-invite-create-btn",
		"hub-invite-revoke-btn",
		"hub-trust-allow-btn",
		"hub-trust-deny-btn",
	} {
		if !strings.Contains(string(dialog), want) {
			t.Fatalf("HubNodesDialog.js missing %q; hub admin controls must call the web API", want)
		}
	}
}

func TestWebRenameShortcutOpensEditDialog(t *testing.T) {
	appShell, err := embeddedStaticFiles.ReadFile("static/app/AppShell.js")
	if err != nil {
		t.Fatalf("ReadFile(AppShell.js): %v", err)
	}
	body := string(appShell)
	if strings.Contains(body, "web rename API not implemented") ||
		strings.Contains(body, "use the TUI (web rename API not implemented yet)") {
		t.Fatalf("AppShell.js still treats web rename as unavailable:\n%s", body)
	}
	for _, want := range []string{
		"editSessionDialogSignal",
		"editSessionDialogSignal.value = { sessionId: s.id }",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("AppShell.js missing %q; r shortcut must open EditSessionDialog", want)
		}
	}

	shortcuts, err := embeddedStaticFiles.ReadFile("static/app/KeyboardShortcuts.js")
	if err != nil {
		t.Fatalf("ReadFile(KeyboardShortcuts.js): %v", err)
	}
	if strings.Contains(string(shortcuts), "TUI-only") {
		t.Fatalf("KeyboardShortcuts.js still documents rename as TUI-only:\n%s", shortcuts)
	}
	if !strings.Contains(string(shortcuts), "Rename focused session") {
		t.Fatal("KeyboardShortcuts.js missing rename shortcut label")
	}
}

func TestWebUIExposesNativeSessionActions(t *testing.T) {
	sidebar, err := embeddedStaticFiles.ReadFile("static/app/Sidebar.js")
	if err != nil {
		t.Fatalf("ReadFile(Sidebar.js): %v", err)
	}
	sidebarBody := string(sidebar)
	for _, want := range []string{
		"/restart-fresh",
		"/toggle-yolo",
		"/unread",
		"/approve",
		"/remove",
		"session-restart-fresh-btn",
		"session-toggle-yolo-btn",
		"session-remove-btn",
		"session-close-btn",
		"session-unread-btn",
		"session-approve-btn",
		"session-notes-btn",
		"session-move-btn",
		"session-prompt-btn",
		"session-send-output-btn",
		"session-sandbox-shell-btn",
		"session-paths-btn",
	} {
		if !strings.Contains(sidebarBody, want) {
			t.Fatalf("Sidebar.js missing %q; web session rows must expose native action parity", want)
		}
	}
	for file, want := range map[string]string{
		"static/app/MoveSessionDialog.js":   "/group",
		"static/app/NotesSessionDialog.js":  "/notes",
		"static/app/PromptSessionDialog.js": "/send",
		"static/app/SendOutputDialog.js":    "/send-output",
		"static/app/EditPathsDialog.js":     "/paths",
		"static/app/ForkSessionDialog.js":   "/fork",
		"static/app/panes/PluginsPane.js":   "/plugins",
	} {
		data, err := embeddedStaticFiles.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", file, err)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s missing %q; web dialogs must call native action parity endpoints", file, want)
		}
	}
	terminalPanel, err := embeddedStaticFiles.ReadFile("static/app/TerminalPanel.js")
	if err != nil {
		t.Fatalf("ReadFile(TerminalPanel.js): %v", err)
	}
	if strings.Contains(string(terminalPanel), "terminal.focus()") {
		t.Fatal("TerminalPanel.js must not auto-focus xterm on mount; it swallows global parity shortcuts like o")
	}

	appShell, err := embeddedStaticFiles.ReadFile("static/app/AppShell.js")
	if err != nil {
		t.Fatalf("ReadFile(AppShell.js): %v", err)
	}
	appShellBody := string(appShell)
	for _, want := range []string{"restart-fresh", "toggle-yolo", "unread", "NotesSessionDialog", "MoveSessionDialog", "PromptSessionDialog", "SendOutputDialog", "sendOutputDialogSignal", "EditPathsDialog", "pathsSessionDialogSignal", "ForkSessionDialog", "forkSessionDialogSignal", "PluginsPane", "cycleAndApplyPreviewMode", "previewModeSignal"} {
		if !strings.Contains(appShellBody, want) {
			t.Fatalf("AppShell.js missing %q; focused session header must expose native action parity", want)
		}
	}
	uiState, err := embeddedStaticFiles.ReadFile("static/app/uiState.js")
	if err != nil {
		t.Fatalf("ReadFile(uiState.js): %v", err)
	}
	for _, want := range []string{"PREVIEW_MODES", "cyclePreviewMode", "agentdeck.previewMode"} {
		if !strings.Contains(string(uiState), want) {
			t.Fatalf("uiState.js missing %q; web must persist and cycle TUI preview-mode parity", want)
		}
	}

	shortcuts, err := embeddedStaticFiles.ReadFile("static/app/KeyboardShortcuts.js")
	if err != nil {
		t.Fatalf("ReadFile(KeyboardShortcuts.js): %v", err)
	}
	for _, want := range []string{"Jump to a session by hint", "Cycle preview mode", "Edit focused session notes", "Edit focused multi-repo paths", "Mark focused session unread", "Quick approve focused Claude session", "Copy focused terminal output", "Copy focused session info", "Prompt focused session", "Send focused output to another session", "Move focused session to group"} {
		if !strings.Contains(string(shortcuts), want) {
			t.Fatalf("KeyboardShortcuts.js missing %q; shortcut overlay must expose native action parity", want)
		}
	}
	for _, want := range []string{"jumpModeSignal", "jumpSessionHints", "jump-overlay", "data-testid=\"jump-hint\""} {
		if !strings.Contains(appShellBody, want) {
			t.Fatalf("AppShell.js missing %q; web must expose jump-mode parity", want)
		}
	}
	searchPane, err := embeddedStaticFiles.ReadFile("static/app/panes/SearchPane.js")
	if err != nil {
		t.Fatalf("ReadFile(SearchPane.js): %v", err)
	}
	for _, want := range []string{"globalSearchModeSignal", "/api/search/global", "GLOBAL CONVERSATION SEARCH", "global-search-result"} {
		if !strings.Contains(string(searchPane), want) {
			t.Fatalf("SearchPane.js missing %q; web must expose global-search parity", want)
		}
	}
	for _, want := range []string{"globalSearchModeSignal", "activeTabSignal.value = 'search'"} {
		if !strings.Contains(appShellBody, want) {
			t.Fatalf("AppShell.js missing %q; G shortcut must open global search", want)
		}
	}
	for file, want := range map[string]string{
		"static/app/AppShell.js":           "copyTextToClipboard",
		"static/app/AppShell.js\x00a":      "agentdeck:copy-terminal-output",
		"static/app/AppShell.js\x00b":      "sessionInfoText",
		"static/app/AppShell.js\x00c":      "/output",
		"static/app/AppShell.js\x00d":      "Copied session output",
		"static/app/TerminalPanel.js":      "terminalBufferText",
		"static/app/TerminalPanel.js\x00a": "agentdeck:copy-terminal-output",
	} {
		path := strings.Split(file, "\x00")[0]
		data, err := embeddedStaticFiles.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s missing %q; web must expose copy output/session-info parity", path, want)
		}
	}
	for _, want := range []string{"/approve", "Approve"} {
		if !strings.Contains(appShellBody, want) {
			t.Fatalf("AppShell.js missing %q; focused session header must expose quick approve parity", want)
		}
	}
	for file, want := range map[string]string{
		"static/app/menuRefresh.js":       "refreshMenuSnapshot",
		"static/app/AppShell.js":          "refreshMenuSnapshot",
		"static/app/KeyboardShortcuts.js": "Refresh session list",
		"static/app/main.js":              "refreshMenuSnapshot",
	} {
		data, err := embeddedStaticFiles.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", file, err)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s missing %q; web must expose manual refresh parity", file, want)
		}
	}
}

func TestWebTerminalPanelSupportsSandboxShellMode(t *testing.T) {
	terminalPanel, err := embeddedStaticFiles.ReadFile("static/app/TerminalPanel.js")
	if err != nil {
		t.Fatalf("ReadFile(TerminalPanel.js): %v", err)
	}
	terminalBody := string(terminalPanel)
	for _, want := range []string{
		"terminalModeSignal",
		"url.searchParams.set('shell', 'sandbox')",
		"terminalMode === 'sandbox'",
		"sandbox shell",
	} {
		if !strings.Contains(terminalBody, want) {
			t.Fatalf("TerminalPanel.js missing %q; web terminal must support sandbox shell attach mode", want)
		}
	}

	appShell, err := embeddedStaticFiles.ReadFile("static/app/AppShell.js")
	if err != nil {
		t.Fatalf("ReadFile(AppShell.js): %v", err)
	}
	appShellBody := string(appShell)
	for _, want := range []string{
		"e.key === 'E'",
		"terminalModeSignal.value = 'sandbox'",
		"activeTabSignal.value = 'terminal'",
	} {
		if !strings.Contains(appShellBody, want) {
			t.Fatalf("AppShell.js missing %q; Shift+E must open the focused sandbox shell", want)
		}
	}

	shortcuts, err := embeddedStaticFiles.ReadFile("static/app/KeyboardShortcuts.js")
	if err != nil {
		t.Fatalf("ReadFile(KeyboardShortcuts.js): %v", err)
	}
	if !strings.Contains(string(shortcuts), "Open focused sandbox shell") {
		t.Fatal("KeyboardShortcuts.js missing sandbox shell shortcut label")
	}
}

func TestMcpPaneUsesSessionAwareCatalog(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/app/panes/McpPane.js")
	if err != nil {
		t.Fatalf("ReadFile(McpPane.js): %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "/api/sessions/") || !strings.Contains(body, "/mcps") || !strings.Contains(body, "attachedResp.catalog") {
		t.Fatalf("McpPane.js must load MCP attachments and catalog from the session-aware endpoint:\n%s", body)
	}
	for _, want := range []string{"isHubSession", "remote node catalog"} {
		if !strings.Contains(body, want) {
			t.Fatalf("McpPane.js missing %q; empty catalog help must distinguish remote hub nodes", want)
		}
	}
	if strings.Contains(body, "jsonFetch('/api/mcps')") || strings.Contains(body, `jsonFetch("/api/mcps")`) {
		t.Fatal("McpPane.js must not fetch local /api/mcps before session state; hub sessions need the remote node catalog")
	}
}

func TestWebUIExposesGroupManagementControls(t *testing.T) {
	sidebar, err := embeddedStaticFiles.ReadFile("static/app/Sidebar.js")
	if err != nil {
		t.Fatalf("ReadFile(Sidebar.js): %v", err)
	}
	sidebarBody := string(sidebar)
	for _, want := range []string{
		"group-create-btn",
		"group-rename-btn",
		"group-move-btn",
		"group-reorder-up-btn",
		"group-reorder-down-btn",
		"group-delete-btn",
		"/reorder",
		"groupMoveDialogSignal",
		"groupNameDialogSignal.value = { mode: 'create', parentPath: g.path }",
	} {
		if !strings.Contains(sidebarBody, want) {
			t.Fatalf("Sidebar.js missing %q; web group headers must expose native group management parity", want)
		}
	}

	groupName, err := embeddedStaticFiles.ReadFile("static/app/GroupNameDialog.js")
	if err != nil {
		t.Fatalf("ReadFile(GroupNameDialog.js): %v", err)
	}
	for _, want := range []string{"parentPath", "New subgroup", "parentPath: parentPath || ''"} {
		if !strings.Contains(string(groupName), want) {
			t.Fatalf("GroupNameDialog.js missing %q; web group creation must support subgroups", want)
		}
	}

	groupMove, err := embeddedStaticFiles.ReadFile("static/app/GroupMoveDialog.js")
	if err != nil {
		t.Fatalf("ReadFile(GroupMoveDialog.js): %v", err)
	}
	for _, want := range []string{"/change", "destParentPath", "sameGroupSpace", "isInvalidTarget", "Root"} {
		if !strings.Contains(string(groupMove), want) {
			t.Fatalf("GroupMoveDialog.js missing %q; web group move must route to group reparent API", want)
		}
	}

	appShell, err := embeddedStaticFiles.ReadFile("static/app/AppShell.js")
	if err != nil {
		t.Fatalf("ReadFile(AppShell.js): %v", err)
	}
	for _, want := range []string{"GroupMoveDialog", "groupMoveDialogSignal"} {
		if !strings.Contains(string(appShell), want) {
			t.Fatalf("AppShell.js missing %q; group move dialog must be mounted", want)
		}
	}
}

func TestVendorFilesServed(t *testing.T) {
	s := NewServer(Config{})
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", s.staticFileServer()))
	for _, path := range []string{
		"/static/vendor/preact.mjs",
		// vendor/tailwind.js was deleted in Phase 1 / Plan 03 (PERF-01).
		// The Tailwind Play CDN runtime is replaced by build-time compiled
		// /static/styles.css (see internal/web/static_files.go //go:generate).
		"/static/vendor/xterm.mjs",
		"/static/vendor/xterm.css",
		"/static/vendor/addon-fit.mjs",
		"/static/vendor/addon-webgl.mjs",
		// vendor/addon-canvas.js was deleted in Phase 8 / Plan 03 (PERF-C).
		// xterm v6 does not reference the canvas renderer anywhere in the
		// first-party code. The TerminalPanel.js fallback path still has a
		// `typeof window.CanvasAddon !== 'undefined'` guard which is now
		// always false, making the canvas fallback inert. See the 404 gate
		// in TestAddonCanvasDeleted below.
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("GET %s: expected 200, got %d", path, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Errorf("GET %s: empty body", path)
		}
	}
}

// TestAddonCanvasDeleted is the regression gate for Phase 8 / Plan 03
// (PERF-C). xterm v6 never references the canvas renderer, so the 94 KB
// vendor file was dead weight. This test ensures the file stays deleted:
//  1. /static/vendor/addon-canvas.js returns 404 via the static file server
//  2. index.html does not <script src> the file
func TestAddonCanvasDeleted(t *testing.T) {
	s := NewServer(Config{Token: "test-token"})

	// Index must not reference addon-canvas.js
	req := httptest.NewRequest(http.MethodGet, "/?token=test-token", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)
	body := w.Body.String()
	if strings.Contains(body, "/static/vendor/addon-canvas.js") {
		t.Error("index.html must NOT reference /static/vendor/addon-canvas.js (deleted in plan 08-03)")
	}

	// Static file server must 404 on the deleted path
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", s.staticFileServer()))
	req2 := httptest.NewRequest(http.MethodGet, "/static/vendor/addon-canvas.js", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != 404 {
		t.Errorf("GET /static/vendor/addon-canvas.js: expected 404, got %d", w2.Code)
	}
}

func TestIndexXtermCSS(t *testing.T) {
	s := NewServer(Config{Token: "test-token"})
	req := httptest.NewRequest(http.MethodGet, "/?token=test-token", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `href="/static/vendor/xterm.css"`) {
		t.Error("index.html missing xterm.css stylesheet link")
	}
}

func TestIndexAppRoot(t *testing.T) {
	s := NewServer(Config{Token: "test-token"})
	req := httptest.NewRequest(http.MethodGet, "/?token=test-token", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `id="app-root"`) {
		t.Error("index.html missing app-root mount point")
	}
}

func TestCreateSessionDialogUsesModelIDCatalog(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/app/CreateSessionDialog.js")
	if err != nil {
		t.Fatalf("read CreateSessionDialog.js: %v", err)
	}
	body := string(data)

	for _, want := range []string{
		"MODEL_ID_CATALOG",
		"<label>MODEL ID</label>",
		`<option value="">Tool default</option>`,
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex",
		"o3-pro",
		"claude-sonnet-4-6",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-haiku-4-5-20251001",
		"gemini-3.1-pro-preview",
		"gemini-3-flash-preview",
		"gemini-2.5-flash-lite",
		"openai/gpt-5.5",
		"anthropic/claude-sonnet-4-6",
		"anthropic/claude-opus-4-8",
		"Custom model ID",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("CreateSessionDialog.js missing %q", want)
		}
	}
	if strings.Contains(body, "<label>VERSION</label>") {
		t.Fatal("web session creation should use model IDs directly, not a separate version selector")
	}
}

func TestCreateSessionDialogUsesTargetPathSuggestions(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/app/CreateSessionDialog.js")
	if err != nil {
		t.Fatalf("read CreateSessionDialog.js: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"pathSuggestionsForTarget",
		"g.defaultPath",
		"s.additionalPaths",
		"s.raw?.additionalPaths",
		"multiRepo",
		"additionalPathValues",
		"payload.additionalPaths",
		"create-session-path-suggestions",
		"Multi-repo session",
		"create-session-hub-paths",
		"create-session-local-paths",
		"<datalist id=${pathListID}>",
		"Suggestions come from the selected hub node.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("CreateSessionDialog.js missing %q; web create path selector must autocomplete local and selected hub-node paths", want)
		}
	}
}

func TestWebToolPickerLabelsCodexAsCodex(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/app/pickerTools.js")
	if err != nil {
		t.Fatalf("ReadFile(pickerTools.js): %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"DEFAULT_PICKER_TOOLS = ['claude', 'codex', 'gemini', 'opencode', 'shell']",
		"codex: 'Codex'",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pickerTools.js missing %q; web create/edit tool picker must expose Codex by name", want)
		}
	}
	if strings.Contains(body, "codex: 'ChatGPT'") {
		t.Fatal("pickerTools.js still labels codex as ChatGPT, making Codex appear missing in the web modal")
	}

	createData, err := embeddedStaticFiles.ReadFile("static/app/CreateSessionDialog.js")
	if err != nil {
		t.Fatalf("ReadFile(CreateSessionDialog.js): %v", err)
	}
	editData, err := embeddedStaticFiles.ReadFile("static/app/EditSessionDialog.js")
	if err != nil {
		t.Fatalf("ReadFile(EditSessionDialog.js): %v", err)
	}
	cssData, err := embeddedStaticFiles.ReadFile("static/app/app.css")
	if err != nil {
		t.Fatalf("ReadFile(app.css): %v", err)
	}
	for name, source := range map[string]string{
		"CreateSessionDialog.js": string(createData),
		"EditSessionDialog.js":   string(editData),
		"app.css":                string(cssData),
	} {
		if !strings.Contains(source, "tool-picker-row") {
			t.Fatalf("%s missing tool-picker-row; all tool buttons must wrap instead of pushing Codex out of view", name)
		}
	}
	if !strings.Contains(string(cssData), "flex-wrap: wrap") {
		t.Fatal("app.css must wrap the web tool picker row so Codex remains visible with many tools")
	}
}

// TestNoTailwindPlayCDN is the regression gate for Phase 1 / Plan 03 (PERF-01).
// The Tailwind Play CDN runtime (vendor/tailwind.js, 397 KB) was deleted in
// favor of a build-time compiled /static/styles.css file (~8 KB gzipped).
// This test ensures:
//  1. internal/web/static/index.html does NOT load /static/vendor/tailwind.js
//  2. internal/web/static/index.html does NOT carry an inline tailwind.config block
//  3. The static file server does NOT serve /static/vendor/tailwind.js (404 expected)
//  4. The compiled /static/styles.css IS linked from index.html
//
// If any of these fail, someone has either re-introduced the Play CDN or
// regressed the cascade swap. See .planning/research/PITFALLS.md Pitfall #2.
func TestNoTailwindPlayCDN(t *testing.T) {
	s := NewServer(Config{Token: "test-token"})
	req := httptest.NewRequest(http.MethodGet, "/?token=test-token", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)
	body := w.Body.String()
	if strings.Contains(body, `/static/vendor/tailwind.js`) {
		t.Error("index.html must NOT reference /static/vendor/tailwind.js (Play CDN was deleted in plan 03)")
	}
	if strings.Contains(body, `tailwind.config = {`) {
		t.Error("index.html must NOT contain inline tailwind.config (palette is now in styles.src.css @theme)")
	}
	if !strings.Contains(body, `href="/static/styles.css"`) {
		t.Error("index.html missing compiled /static/styles.css link")
	}

	// The static file server should now 404 on /static/vendor/tailwind.js.
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", s.staticFileServer()))
	req2 := httptest.NewRequest(http.MethodGet, "/static/vendor/tailwind.js", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != 404 {
		t.Errorf("GET /static/vendor/tailwind.js: expected 404, got %d", w2.Code)
	}
}
