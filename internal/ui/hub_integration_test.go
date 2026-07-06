package ui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHubConfiguredPrefixesLocalGroupsWithLocal(t *testing.T) {
	h := newHubProjectionHome(t, []*session.Instance{
		{ID: "s1", Title: "api", GroupPath: "default", Tool: "claude", Status: session.StatusRunning},
	})
	h.hubConfigured = true
	h.hubLocalNodeName = "local"

	h.rebuildFlatItems()

	got := h.View()
	if !strings.Contains(got, "local / default") {
		t.Fatalf("view missing local-prefixed group:\n%s", got)
	}
	if h.instances[0].GroupPath != "default" {
		t.Fatalf("local session GroupPath changed to %q", h.instances[0].GroupPath)
	}
}

func TestHubRemoteSnapshotAppearsAsNodePrefixedGroup(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{{
				ID:        "r1",
				Title:     "deploy",
				Tool:      "claude",
				Status:    "waiting",
				GroupPath: "default",
			}},
		},
	}

	h.rebuildFlatItems()

	got := h.View()
	if !strings.Contains(got, "server1 / default") || !strings.Contains(got, "deploy") {
		t.Fatalf("view missing remote hub session:\n%s", got)
	}
	if strings.Contains(got, "remotes/") {
		t.Fatalf("hub sessions should render inline, not under remotes/:\n%s", got)
	}
}

func TestHubSessionsRespectStatusFilterAndRenderWithoutPanic(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{
				{ID: "waiting-remote", Title: "needs-input", Tool: "claude", Status: "waiting", GroupPath: "ops"},
				{ID: "idle-remote", Title: "quiet", Tool: "claude", Status: "idle", GroupPath: "ops"},
			},
		},
	}
	h.statusFilter = session.StatusWaiting

	h.rebuildFlatItems()

	got := h.View()
	if !strings.Contains(got, "needs-input") {
		t.Fatalf("waiting hub session missing with waiting filter:\n%s", got)
	}
	if strings.Contains(got, "quiet") {
		t.Fatalf("idle hub session should be hidden by waiting filter:\n%s", got)
	}
}

func TestActiveTopPlacesHubWaitingAboveIdleDivider(t *testing.T) {
	local := session.NewInstanceWithTool("local-active", "/tmp/local-active", "claude")
	local.Status = session.StatusRunning
	local.GroupPath = "local"
	idle := session.NewInstanceWithTool("local-idle", "/tmp/local-idle", "claude")
	idle.Status = session.StatusIdle
	idle.GroupPath = "local"

	h := newHubProjectionHome(t, []*session.Instance{local, idle})
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{
				{ID: "hub-waiting", Title: "remote needs input", Tool: "claude", Status: "waiting", GroupPath: "ops"},
				{ID: "hub-idle", Title: "remote quiet", Tool: "claude", Status: "idle", GroupPath: "ops"},
			},
		},
	}

	h.groupViewMode = session.GroupViewActiveTop
	h.rebuildFlatItems()

	div := dividerIndex(h)
	waiting := hubSessionIndexByID(h, "hub-waiting")
	idleIdx := hubSessionIndexByID(h, "hub-idle")
	if div < 0 {
		t.Fatalf("expected active-top divider with local active and idle sessions")
	}
	if waiting < 0 || waiting >= div {
		t.Fatalf("waiting hub session must be above idle divider: waiting=%d divider=%d\nitems=%#v", waiting, div, h.flatItems)
	}
	if idleIdx < 0 || idleIdx <= div {
		t.Fatalf("idle hub session must be below idle divider: idle=%d divider=%d\nitems=%#v", idleIdx, div, h.flatItems)
	}
}

func TestHubSnapshotCallbackQueuesUpdateAndProjectsRemote(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"

	h.handleHubSnapshot(hub.NodeSessions{
		Node: hub.Node{ID: "node_server", Name: "server1"},
		Sessions: []hub.SessionInfo{{
			ID:        "r1",
			Title:     "deploy",
			Tool:      "claude",
			Status:    "waiting",
			GroupPath: "ops",
		}},
	})

	var msg hubSnapshotMsg
	select {
	case msg = <-h.hubSnapshotCh:
	default:
		t.Fatal("hub snapshot callback did not enqueue an update message")
	}
	model, _ := h.Update(msg)
	h = model.(*Home)

	got := h.View()
	if !strings.Contains(got, "server1 / ops") || !strings.Contains(got, "deploy") {
		t.Fatalf("view missing callback-projected hub session:\n%s", got)
	}
}

func TestHubTrustRequestShowsConfirmation(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true

	h.handleHubTrustRequest(hub.TrustRequestPayload{NodeID: "node_joining", NodeName: "new laptop"})

	var msg hubTrustRequestMsg
	select {
	case msg = <-h.hubTrustRequestCh:
	default:
		t.Fatal("hub trust callback did not enqueue an update message")
	}
	model, _ := h.Update(msg)
	h = model.(*Home)

	if !h.confirmDialog.IsVisible() {
		t.Fatal("trust request did not show confirmation dialog")
	}
	if got := h.confirmDialog.GetConfirmType(); got != ConfirmHubTrustNode {
		t.Fatalf("confirm type = %v, want ConfirmHubTrustNode", got)
	}
	if got := h.confirmDialog.GetTargetID(); got != "node_joining" {
		t.Fatalf("confirm target = %q, want node_joining", got)
	}
}

func TestHubTrustAllowSendsDecisionThroughHubClient(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.applyHubTrustRequest(hub.TrustRequestPayload{NodeID: "node_joining", NodeName: "new laptop"})

	model, cmd := h.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("allow trust returned no command")
	}
	msg := cmd()
	result, ok := msg.(hubTrustDecisionResultMsg)
	if !ok {
		t.Fatalf("trust decision command returned %T", msg)
	}
	if result.err != nil || !result.allow || result.nodeID != "node_joining" {
		t.Fatalf("trust decision result = %+v", result)
	}
	if len(client.trustDecisions) != 1 {
		t.Fatalf("trust decisions = %d, want 1", len(client.trustDecisions))
	}
	if got := client.trustDecisions[0]; got.nodeID != "node_joining" || !got.allow {
		t.Fatalf("trust decision = %+v", got)
	}
	if h.confirmDialog.IsVisible() {
		t.Fatal("trust dialog still visible after allow")
	}
}

func TestHubStatusRendersWhenConfigured(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.setHubStatus("hub offline")

	got := h.View()
	if !strings.Contains(got, "hub offline") {
		t.Fatalf("view missing hub status:\n%s", got)
	}
}

func TestHubRowsRespectGroupScope(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.groupScope = "ops"
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{
				{ID: "ops-remote", Title: "ops-worker", Tool: "claude", Status: "waiting", GroupPath: "ops"},
				{ID: "personal-remote", Title: "personal-worker", Tool: "claude", Status: "waiting", GroupPath: "personal"},
			},
		},
	}

	h.rebuildFlatItems()

	got := h.View()
	if !strings.Contains(got, "server1 / ops") || !strings.Contains(got, "ops-worker") {
		t.Fatalf("scoped view missing matching hub row:\n%s", got)
	}
	if strings.Contains(got, "personal-worker") || strings.Contains(got, "server1 / personal") {
		t.Fatalf("scoped view included out-of-scope hub row:\n%s", got)
	}
}

func TestHubLocalGroupRenameUsesStoredGroupName(t *testing.T) {
	h := newHubProjectionHome(t, []*session.Instance{
		{ID: "s1", Title: "api", GroupPath: "default", Tool: "claude", Status: session.StatusRunning},
	})
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.rebuildFlatItems()

	h.cursor = 0
	if h.flatItems[h.cursor].Type != session.ItemTypeGroup {
		t.Fatalf("test setup cursor item = %+v, want group", h.flatItems[h.cursor])
	}
	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	if got := h.groupDialog.GetValue(); got != "default" {
		t.Fatalf("rename dialog value = %q, want stored group name without hub prefix", got)
	}
}

func TestHubRowsCreateSessionsThroughHubCommand(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{{
				ID:          "r1",
				Title:       "deploy",
				Tool:        "claude",
				Status:      "waiting",
				GroupPath:   "ops",
				ProjectPath: "/srv/app",
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	h = model.(*Home)
	if cmd != nil {
		msg := cmd()
		if result, ok := msg.(hubActionResultMsg); !ok || result.err != nil {
			t.Fatalf("n hub command msg = %#v", msg)
		}
	} else {
		t.Fatal("n on hub session returned no command")
	}
	if h.newDialog.IsVisible() {
		t.Fatal("n on hub session opened the local new-session dialog")
	}
	if len(client.commands) != 1 {
		t.Fatalf("hub commands after n = %d, want 1", len(client.commands))
	}
	if got := client.commands[0]; got.nodeID != "node_server" || got.action != "create" {
		t.Fatalf("n command = %+v", got)
	}
	req, ok := client.commands[0].payload.(hub.CreateSessionRequest)
	if !ok {
		t.Fatalf("n payload type = %T, want hub.CreateSessionRequest", client.commands[0].payload)
	}
	if req.GroupPath != "ops" || req.ProjectPath != "/srv/app" || req.Tool != "claude" || strings.TrimSpace(req.Title) == "" {
		t.Fatalf("n create request = %+v", req)
	}

	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("N on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("N hub command error = %v", msg.(hubActionResultMsg).err)
	}
	if len(client.commands) != 2 {
		t.Fatalf("hub commands after N = %d, want 2", len(client.commands))
	}
	if got := client.commands[1]; got.nodeID != "node_server" || got.action != "create" {
		t.Fatalf("N command = %+v", got)
	}
}

func TestHubSessionStopRestartAndPromptUseHubCommand(t *testing.T) {
	h, client := newHubActionHome(t)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("D on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("D hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "stop", map[string]string{
		"session_id": "r1",
	})

	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("R on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("R hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[1], "node_server", "restart", map[string]string{
		"session_id": "r1",
	})

	key := defaultHotkeyBindings[hotkeyPromptSession]
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("prompt hotkey should open the dialog without returning a command")
	}
	if !h.promptInputDialog.IsVisible() {
		t.Fatal("prompt hotkey on hub session did not open prompt dialog")
	}
	if got := h.promptInputDialog.instanceID; got != hubPromptTarget("node_server", "r1") {
		t.Fatalf("prompt target = %q, want hub target", got)
	}
	model, cmd = h.updateInner(promptSubmitMsg{instanceID: h.promptInputDialog.instanceID, text: "run tests"})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub prompt submit returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("prompt hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[2], "node_server", "send", map[string]string{
		"session_id": "r1",
		"message":    "run tests",
	})
}

func TestHubSessionRenameUsesHubCommand(t *testing.T) {
	h, client := newHubActionHome(t)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("rename hotkey should only open the dialog")
	}
	if got := h.groupDialog.GetSessionID(); got != hubPromptTarget("node_server", "r1") {
		t.Fatalf("rename target = %q, want hub target", got)
	}
	h.groupDialog.nameInput.SetValue("deploy renamed")

	model, cmd = h.handleGroupDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub rename submit returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("rename hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "rename", map[string]string{
		"session_id": "r1",
		"title":      "deploy renamed",
	})
	if got := h.hubSessions["node_server"].Sessions[0].Title; got != "deploy renamed" {
		t.Fatalf("cached hub title = %q, want deploy renamed", got)
	}
	if h.groupDialog.IsVisible() {
		t.Fatal("rename dialog should hide after submit")
	}
}

func TestHubSessionImportUsesHubCommand(t *testing.T) {
	h, client := newHubActionHome(t)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("i on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("import hub command error = %v", msg.(hubActionResultMsg).err)
	}
	if h.importSourceDialog != nil && h.importSourceDialog.IsVisible() {
		t.Fatal("i on hub session opened the local import source dialog")
	}
	if len(client.commands) != 1 {
		t.Fatalf("hub commands after i = %d, want 1", len(client.commands))
	}
	if got := client.commands[0]; got.nodeID != "node_server" || got.action != "import_tmux" {
		t.Fatalf("import command = %+v, want node_server import_tmux", got)
	}
}

func TestSelectedHubPreviewTarget(t *testing.T) {
	h, _ := newHubActionHome(t)

	nodeID, sessionID, previewKey, ok := h.selectedHubPreviewTarget()

	if !ok {
		t.Fatal("selectedHubPreviewTarget should resolve hub session selection")
	}
	if nodeID != "node_server" {
		t.Fatalf("nodeID = %q, want node_server", nodeID)
	}
	if sessionID != "r1" {
		t.Fatalf("sessionID = %q, want r1", sessionID)
	}
	if previewKey != "hub:node_server:r1" {
		t.Fatalf("previewKey = %q, want hub:node_server:r1", previewKey)
	}
}

func TestFetchSelectedPreviewSchedulesHubPreview(t *testing.T) {
	h, _ := newHubActionHome(t)

	cmd := h.fetchSelectedPreview()
	if cmd == nil {
		t.Fatal("fetchSelectedPreview returned nil for selected hub session")
	}
	msg := cmd()
	debounce, ok := msg.(previewDebounceMsg)
	if !ok {
		t.Fatalf("fetchSelectedPreview returned %T, want previewDebounceMsg", msg)
	}
	if debounce.hubNodeID != "node_server" || debounce.sessionID != "r1" || debounce.previewKey != "hub:node_server:r1" {
		t.Fatalf("hub preview debounce = %+v", debounce)
	}
}

func TestFetchHubPreviewUsesHubCommand(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.PreviewSessionResponse{Content: "Hub answer"})
	key := hubPreviewCacheKey("node_server", "r1")

	cmd := h.fetchHubPreview("node_server", "r1", key)
	if cmd == nil {
		t.Fatal("fetchHubPreview returned nil command")
	}
	msg := cmd()
	fetched, ok := msg.(previewFetchedMsg)
	if !ok {
		t.Fatalf("fetchHubPreview returned %T, want previewFetchedMsg", msg)
	}
	if fetched.previewKey != key {
		t.Fatalf("preview key = %q, want %q", fetched.previewKey, key)
	}
	if fetched.err != nil {
		t.Fatalf("preview fetch error = %v", fetched.err)
	}
	if fetched.content != "Hub answer" {
		t.Fatalf("preview content = %q, want Hub answer", fetched.content)
	}
	assertHubCommand(t, client.commands[0], "node_server", "preview", map[string]string{
		"session_id": "r1",
	})
}

func TestRenderHubPreviewIncludesCachedResponse(t *testing.T) {
	h, _ := newHubActionHome(t)
	key := hubPreviewCacheKey("node_server", "r1")
	h.previewCache[key] = "Hub answer"

	rendered := h.renderHubPreview(h.flatItems[h.cursor], 80, 20)

	if !strings.Contains(rendered, "Last response") {
		t.Fatalf("rendered preview should include last response header, got: %q", rendered)
	}
	if !strings.Contains(rendered, "Hub answer") {
		t.Fatalf("rendered preview should include cached hub response, got: %q", rendered)
	}
}

func TestHubSessionIndentMatchesLocalGroupedSession(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.width = 100
	h.height = 30

	local := session.NewInstanceWithTool("local-session", "/tmp", "claude")
	local.Status = session.StatusWaiting
	local.GroupPath = "ops"
	localItem := session.Item{
		Type:          session.ItemTypeSession,
		Session:       local,
		Level:         1,
		IsLastInGroup: false,
	}

	hubItem := session.Item{
		Type: session.ItemTypeHubSession,
		HubSession: &session.HubSessionInfo{
			ID:     "hub-session",
			Title:  "hub-session",
			Tool:   "claude",
			Status: string(session.StatusWaiting),
		},
		Level:         1,
		IsLastInGroup: false,
	}

	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "unselected", true: "selected"}[selected], func(t *testing.T) {
			var localRow strings.Builder
			h.renderSessionItem(&localRow, localItem, selected, map[string]sessionRenderState{
				local.ID: {status: session.StatusWaiting, tool: "claude"},
			}, h.width)

			var hubRow strings.Builder
			h.renderHubSessionItem(&hubRow, hubItem, selected)

			localCol := renderedConnectorColumn(localRow.String())
			hubCol := renderedConnectorColumn(hubRow.String())
			if localCol < 0 || hubCol < 0 {
				t.Fatalf("missing tree connector: local=%q hub=%q", stripAnsi(localRow.String()), stripAnsi(hubRow.String()))
			}
			if hubCol != localCol {
				t.Fatalf("hub session connector column = %d, want local grouped session column %d\nlocal: %q\nhub:   %q",
					hubCol, localCol, stripAnsi(localRow.String()), stripAnsi(hubRow.String()))
			}
		})
	}
}

func renderedConnectorColumn(row string) int {
	clean := stripAnsi(row)
	for _, connector := range []string{treeBranch, treeLast} {
		if idx := strings.Index(clean, connector); idx >= 0 {
			return len([]rune(clean[:idx]))
		}
	}
	return -1
}

func TestHubAttachCmdCallsClient(t *testing.T) {
	client := &fakeHubAttachClient{}
	cmd := hubAttachCmd{
		client:    client,
		nodeID:    "node_server",
		sessionID: "remote_session",
		size:      hub.TerminalSize{Cols: 120, Rows: 40},
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.nodeID != "node_server" || client.sessionID != "remote_session" {
		t.Fatalf("attach call = node %q session %q", client.nodeID, client.sessionID)
	}
	if client.size.Cols != 120 || client.size.Rows != 40 {
		t.Fatalf("attach size = %+v, want 120x40", client.size)
	}
}

func TestHubAttachCmdRestartsBeforeAttachWhenRequested(t *testing.T) {
	client := &fakeHubAttachClient{}
	cmd := hubAttachCmd{
		client:              client,
		nodeID:              "node_server",
		sessionID:           "remote_session",
		size:                hub.TerminalSize{Cols: 120, Rows: 40},
		restartBeforeAttach: true,
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(client.commands) != 1 {
		t.Fatalf("hub commands = %d, want restart before attach", len(client.commands))
	}
	assertHubCommand(t, client.commands[0], "node_server", "restart", map[string]string{
		"session_id": "remote_session",
	})
	if client.nodeID != "node_server" || client.sessionID != "remote_session" {
		t.Fatalf("attach call = node %q session %q", client.nodeID, client.sessionID)
	}
}

func TestHubSessionNeedsRestartBeforeAttachForStoppedOrError(t *testing.T) {
	for _, status := range []string{"stopped", "error"} {
		t.Run(status, func(t *testing.T) {
			hs := &session.HubSessionInfo{Status: status}
			if !hubSessionNeedsRestartBeforeAttach(hs) {
				t.Fatalf("hubSessionNeedsRestartBeforeAttach(%q) = false, want true", status)
			}
		})
	}
	for _, status := range []string{"running", "waiting", ""} {
		t.Run("no_restart_"+status, func(t *testing.T) {
			hs := &session.HubSessionInfo{Status: status}
			if hubSessionNeedsRestartBeforeAttach(hs) {
				t.Fatalf("hubSessionNeedsRestartBeforeAttach(%q) = true, want false", status)
			}
		})
	}
}

func TestHubEnterOnSessionStartsAttachCommand(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubClient = &fakeHubAttachClient{}
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{{
				ID:        "r1",
				Title:     "deploy",
				Tool:      "claude",
				Status:    "waiting",
				GroupPath: "ops",
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("Enter on hub session returned no command")
	}
	if h.err != nil {
		t.Fatalf("Enter on hub session error = %v", h.err)
	}
	if !h.isAttaching.Load() {
		t.Fatal("Enter on hub session did not mark Home as attaching")
	}
}

func TestHubAttachResultMsgRecordsErrorThroughUpdate(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.isAttaching.Store(true)

	model, cmd := h.Update(hubAttachResultMsg{err: errors.New("relay closed")})
	h = model.(*Home)

	if h.isAttaching.Load() {
		t.Fatal("hub attach result did not clear attach flag")
	}
	if h.err == nil || !strings.Contains(h.err.Error(), "hub attach: relay closed") {
		t.Fatalf("hub attach result error = %v", h.err)
	}
	if got := h.hubStatusText(); got != "hub attach failed" {
		t.Fatalf("hub status = %q, want hub attach failed", got)
	}
	if cmd == nil {
		t.Fatal("hub attach result did not return post-attach refresh command")
	}
}

func newHubProjectionHome(t *testing.T, instances []*session.Instance) *Home {
	t.Helper()
	setXDGTestHome(t)
	h := NewHome()
	h.width = 120
	h.height = 40
	h.initialLoading = false
	h.instances = instances
	h.instanceByID = make(map[string]*session.Instance, len(instances))
	for _, inst := range instances {
		h.instanceByID[inst.ID] = inst
	}
	h.groupTree = session.NewGroupTree(instances)
	return h
}

func newHubActionHome(t *testing.T) (*Home, *fakeHubAttachClient) {
	t.Helper()
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{{
				ID:          "r1",
				Title:       "deploy",
				Tool:        "claude",
				Status:      "waiting",
				GroupPath:   "ops",
				ProjectPath: "/srv/app",
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")
	return h, client
}

type fakeHubAttachClient struct {
	nodeID         string
	sessionID      string
	size           hub.TerminalSize
	commands       []hubCommandCall
	trustDecisions []hubTrustDecisionCall
	commandErr     error
	commandResult  json.RawMessage
}

func (c *fakeHubAttachClient) Attach(ctx context.Context, nodeID, sessionID string, size hub.TerminalSize) error {
	c.nodeID = nodeID
	c.sessionID = sessionID
	c.size = size
	return nil
}

func (c *fakeHubAttachClient) Command(ctx context.Context, nodeID, action string, payload any) (json.RawMessage, error) {
	c.commands = append(c.commands, hubCommandCall{nodeID: nodeID, action: action, payload: payload})
	if c.commandErr != nil {
		return nil, c.commandErr
	}
	return c.commandResult, nil
}

func (c *fakeHubAttachClient) TrustDecision(ctx context.Context, nodeID string, allow bool) error {
	c.trustDecisions = append(c.trustDecisions, hubTrustDecisionCall{nodeID: nodeID, allow: allow})
	return nil
}

func (c *fakeHubAttachClient) Close() error {
	return nil
}

type hubCommandCall struct {
	nodeID  string
	action  string
	payload any
}

type hubTrustDecisionCall struct {
	nodeID string
	allow  bool
}

func assertHubCommand(t *testing.T, got hubCommandCall, nodeID, action string, wantPayload map[string]string) {
	t.Helper()
	if got.nodeID != nodeID || got.action != action {
		t.Fatalf("hub command = %+v, want node=%q action=%q", got, nodeID, action)
	}
	payload, ok := got.payload.(map[string]string)
	if !ok {
		t.Fatalf("hub command payload type = %T, want map[string]string", got.payload)
	}
	for k, v := range wantPayload {
		if payload[k] != v {
			t.Fatalf("hub command payload[%q] = %q, want %q (payload=%v)", k, payload[k], v, payload)
		}
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return raw
}

func indexHubSession(t *testing.T, h *Home, id string) int {
	t.Helper()
	for i, item := range h.flatItems {
		if item.Type == session.ItemTypeHubSession && item.HubSession != nil && item.HubSession.ID == id {
			return i
		}
	}
	t.Fatalf("hub session %q not found in flatItems: %+v", id, h.flatItems)
	return -1
}
