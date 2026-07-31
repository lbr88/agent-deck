package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/web"
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

func TestWebMenuSnapshotIncludesHubSessionsAndEmptyNodes(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	menuData := web.NewMemoryMenuData(nil)
	h.SetWebMenuData(menuData)
	archivedAt := time.Unix(123, 0).UTC()
	h.hubSessions = map[string]hub.NodeSessions{
		"node_empty": {
			Node: hub.Node{ID: "node_empty", Name: "empty"},
		},
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Groups: []hub.GroupInfo{{
				Name: "empty",
				Path: "empty",
			}},
			Sessions: []hub.SessionInfo{{
				ID:             "r1",
				Title:          "deploy",
				Tool:           "claude",
				Status:         "waiting",
				Substate:       "idle-at-empty-prompt",
				GroupPath:      "ops",
				ProjectPath:    "/srv/app",
				IsConductor:    true,
				Command:        "claude --model sonnet",
				Wrapper:        "direnv exec .",
				TmuxSession:    "tmux-r1",
				TmuxSocketName: "agent-deck",
				Windows: []hub.WindowInfo{{
					Index:    0,
					Name:     "main",
					Activity: 123,
					Tool:     "claude",
				}, {
					Index: 1,
					Name:  "logs",
					Tool:  "shell",
				}},
				Color:            "cyan",
				ClaudeSessionID:  "claude-r1",
				LatestPrompt:     "ship it",
				WorktreePath:     "/srv/app/.worktrees/deploy",
				WorktreeRepoRoot: "/srv/app",
				WorktreeBranch:   "fork/deploy",
				Notes:            "remote runbook",
				LoadedMCPNames:   []string{"github"},
				Plugins:          []string{"review"},
				Channels:         []string{"code"},
				ExtraArgs:        []string{"--verbose"},
				ToolOptionsJSON:  json.RawMessage(`{"model":"sonnet"}`),
				Sandbox:          json.RawMessage(`{"enabled":true,"image":"sandbox:latest"}`),
				SandboxContainer: "container-r1",
				SSHHost:          "server1",
				SSHRemotePath:    "/srv/app",
				TitleLocked:      true,
				CanFork:          true,
			}, {
				ID:              "r1-child",
				Title:           "deploy child",
				Tool:            "claude",
				Status:          "running",
				GroupPath:       "ops",
				ProjectPath:     "/srv/app",
				ParentSessionID: "r1",
			}, {
				ID:         "archived",
				Title:      "old",
				Tool:       "codex",
				Status:     "stopped",
				GroupPath:  "ops",
				ArchivedAt: &archivedAt,
			}},
		},
	}

	h.rebuildFlatItems()

	active, err := menuData.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot: %v", err)
	}
	if !webSnapshotHasHubSession(active, "node_server", "r1") {
		t.Fatalf("active web snapshot missing hub session: %+v", active.Items)
	}
	if !webSnapshotHubSessionCanFork(active, "node_server", "r1") {
		t.Fatalf("active web snapshot did not mark forkable hub session: %+v", active.Items)
	}
	if got := webSnapshotHubSessionNotes(active, "node_server", "r1"); got != "remote runbook" {
		t.Fatalf("active web snapshot hub notes = %q, want remote runbook", got)
	}
	if got := webSnapshotHubSessionWorktree(active, "node_server", "r1"); got != "fork/deploy" {
		t.Fatalf("active web snapshot hub worktree branch = %q, want fork/deploy", got)
	}
	projected := webSnapshotHubSession(active, "node_server", "r1")
	if projected == nil {
		t.Fatal("active web snapshot hub session missing projected metadata")
	}
	if projected.Command != "claude --model sonnet" || projected.Wrapper != "direnv exec ." ||
		projected.TmuxSession != "tmux-r1" || projected.TmuxSocketName != "agent-deck" ||
		projected.Color != "cyan" || projected.ClaudeSessionID != "claude-r1" || projected.LatestPrompt != "ship it" {
		t.Fatalf("active web snapshot hub rich metadata missing: %+v", projected)
	}
	if projected.Substate != "idle-at-empty-prompt" {
		t.Fatalf("active web snapshot hub substate = %q, want idle-at-empty-prompt", projected.Substate)
	}
	if !projected.IsConductor {
		t.Fatalf("active web snapshot hub isConductor = false, want true: %+v", projected)
	}
	if len(projected.Windows) != 2 || projected.Windows[1].Index != 1 || projected.Windows[1].Name != "logs" || projected.Windows[1].Tool != "shell" {
		t.Fatalf("active web snapshot hub windows missing: %+v", projected.Windows)
	}
	child := webSnapshotHubSession(active, "node_server", "r1-child")
	if child == nil {
		t.Fatal("active web snapshot missing hub child session")
	}
	if child.ParentSessionID != web.HubSessionWebID("node_server", "r1") {
		t.Fatalf("active web snapshot hub child parentSessionId = %q, want %q", child.ParentSessionID, web.HubSessionWebID("node_server", "r1"))
	}
	if len(projected.LoadedMCPNames) != 1 || projected.LoadedMCPNames[0] != "github" ||
		len(projected.Plugins) != 1 || projected.Plugins[0] != "review" ||
		len(projected.Channels) != 1 || projected.Channels[0] != "code" ||
		len(projected.ExtraArgs) != 1 || projected.ExtraArgs[0] != "--verbose" {
		t.Fatalf("active web snapshot hub list metadata missing: %+v", projected)
	}
	if string(projected.ToolOptionsJSON) != `{"model":"sonnet"}` || projected.Sandbox == nil || !projected.Sandbox.Enabled ||
		projected.SandboxContainer != "container-r1" || projected.SSHHost != "server1" || projected.SSHRemotePath != "/srv/app" ||
		!projected.TitleLocked {
		t.Fatalf("active web snapshot hub config metadata missing: %+v", projected)
	}
	if webSnapshotHasHubSession(active, "node_server", "archived") {
		t.Fatalf("active web snapshot included archived hub session: %+v", active.Items)
	}
	if !webSnapshotHasHubGroup(active, "node_empty", session.DefaultGroupPath, 0) {
		t.Fatalf("active web snapshot missing empty hub node group: %+v", active.Items)
	}
	if !webSnapshotHasHubGroup(active, "node_server", "empty", 0) {
		t.Fatalf("active web snapshot missing empty remote hub group: %+v", active.Items)
	}
	if !webSnapshotHasHubNode(active, "node_empty", "empty") {
		t.Fatalf("active web snapshot missing empty hub node target: %+v", active.HubNodes)
	}

	archived, err := menuData.LoadArchivedMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadArchivedMenuSnapshot: %v", err)
	}
	if !webSnapshotHasHubSession(archived, "node_server", "archived") {
		t.Fatalf("archived web snapshot missing archived hub session: %+v", archived.Items)
	}
}

func TestHubRemoteEmptyGroupAppearsAsCreateTarget(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubLocalNodeAdmin = true
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Groups: []hub.GroupInfo{{
				Name: "empty",
				Path: "empty",
			}},
		},
	}
	h.rebuildFlatItems()

	got := h.View()
	if !strings.Contains(got, "server1 / empty") {
		t.Fatalf("view missing empty remote hub group:\n%s", got)
	}
	h.cursor = indexHubGroup(t, h, "node_server", "empty")
	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if cmd == nil {
		t.Fatal("N on empty hub group returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("N hub group command error = %v", msg.(hubActionResultMsg).err)
	}
	req, ok := client.commands[0].payload.(hub.CreateSessionRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.CreateSessionRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_server" || client.commands[0].action != "create" || req.GroupPath != "empty" {
		t.Fatalf("hub empty group create = command=%+v req=%+v", client.commands[0], req)
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

func TestHubSnapshotCallbackCoalescesWithoutLosingLatest(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	menuData := web.NewMemoryMenuData(nil)
	h.SetWebMenuData(menuData)

	for i := 0; i < 200; i++ {
		h.handleHubSnapshot(hub.NodeSessions{
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{{
				ID:        "r1",
				Title:     fmt.Sprintf("snapshot-%03d", i),
				Tool:      "claude",
				Status:    "waiting",
				GroupPath: "ops",
			}},
		})
	}

	snapshots := h.hubSessionSnapshots()
	if len(snapshots) != 1 || len(snapshots[0].Sessions) != 1 {
		t.Fatalf("authoritative hub snapshots = %+v, want one session", snapshots)
	}
	if got := snapshots[0].Sessions[0].Title; got != "snapshot-199" {
		t.Fatalf("latest hub title = %q, want snapshot-199", got)
	}
	if got := len(h.hubSnapshotCh); got != 1 {
		t.Fatalf("queued hub notifications = %d, want 1", got)
	}

	webSnapshot, err := menuData.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot: %v", err)
	}
	foundLatest := false
	for _, item := range webSnapshot.Items {
		if item.Session != nil && item.Session.HubNodeID == "node_server" {
			if item.Session.Title != "snapshot-199" {
				t.Fatalf("web hub title = %q, want snapshot-199", item.Session.Title)
			}
			foundLatest = true
		}
	}
	if !foundLatest {
		t.Fatalf("web snapshot did not receive latest hub state: %+v", webSnapshot.Items)
	}
}

func TestHubWelcomeMetadataUpdateRenamesLocalPrefixAndRole(t *testing.T) {
	h := newHubProjectionHome(t, []*session.Instance{{
		ID:        "local-session",
		Title:     "local worker",
		Tool:      "codex",
		Status:    session.StatusWaiting,
		GroupPath: session.DefaultGroupPath,
	}})
	h.hubConfigured = true
	h.hubLocalNodeID = "node_local"
	h.hubLocalNodeName = "old-laptop"
	h.hubLocalNodeAdmin = false

	h.applyHubWelcome(hub.WelcomePayload{
		NodeID:   "node_local",
		NodeName: "private-laptop",
		Admin:    true,
	})
	h.rebuildFlatItems()

	if h.hubLocalNodeName != "private-laptop" || !h.hubLocalNodeAdmin {
		t.Fatalf("local hub metadata = name:%q admin:%v, want private-laptop/admin", h.hubLocalNodeName, h.hubLocalNodeAdmin)
	}
	view := h.View()
	wantPrefix := hubDisplayGroupPath("private-laptop", session.DefaultGroupPath)
	if !strings.Contains(view, wantPrefix) {
		t.Fatalf("renamed local hub prefix missing from session list:\n%s", view)
	}
	if strings.Contains(view, hubDisplayGroupPath("old-laptop", session.DefaultGroupPath)) {
		t.Fatalf("stale local hub prefix remained in session list:\n%s", view)
	}
}

func TestHubSessionRenderUsesRemoteSubstateGlyph(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	item := session.Item{
		Type:        session.ItemTypeHubSession,
		HubNodeID:   "node_server",
		HubNodeName: "server1",
		HubSession: &session.HubSessionInfo{
			ID:       "r1",
			Title:    "deploy",
			Tool:     "claude",
			Status:   string(session.StatusError),
			Substate: string(session.SubstateAuth401),
		},
	}

	var row strings.Builder
	h.renderHubSessionItem(&row, item, false)
	if got := stripANSIForHubTest(row.String()); !strings.Contains(got, "🔒") {
		t.Fatalf("hub row did not render auth substate glyph:\n%s", got)
	}
	preview := stripANSIForHubTest(h.renderHubPreview(item, 80, 24))
	if !strings.Contains(preview, "🔒 error") {
		t.Fatalf("hub preview did not render auth substate glyph:\n%s", preview)
	}
}

func stripANSIForHubTest(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) {
			switch s[i+1] {
			case '[':
				j := i + 2
				for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
					j++
				}
				i = j
				continue
			case ']':
				j := i + 2
				for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				if j < len(s) && s[j] == 0x1b {
					j++
				}
				i = j
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func TestHubStatusCallbackWakesUpdateLoop(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true

	cmd := h.listenForHubStatus()
	if cmd == nil {
		t.Fatal("hub status listener command is nil")
	}
	h.handleHubStatus("hub connected")

	msg, ok := cmd().(hubStatusMsg)
	if !ok {
		t.Fatalf("listener msg type = %T, want hubStatusMsg", msg)
	}
	model, next := h.Update(msg)
	h = model.(*Home)
	if got := h.hubStatusText(); got != "hub connected" {
		t.Fatalf("hub status = %q, want hub connected", got)
	}
	if next == nil {
		t.Fatal("hub status handler did not re-arm the listener")
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

func TestHubAdminDialogLoadsInvitesAndTrustForAdmin(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeAdmin = true
	client := &fakeHubAttachClient{
		invites: []hub.AdminInvite{{
			ID:        "invite_1",
			NodeName:  "gpu",
			ExpiresAt: time.Unix(456, 0).UTC(),
			Status:    "active",
		}},
		trustRequests: []hub.TrustRequestPayload{{
			NodeID:   "node_pending",
			NodeName: "pending gpu",
			Version:  "1.0.0",
			OS:       "linux",
			Arch:     "amd64",
		}},
	}
	h.hubClient = client

	cmd := h.openHubAdminDialog()
	if cmd == nil {
		t.Fatal("admin hub dialog returned no load command")
	}
	msg, ok := cmd().(hubAdminDialogLoadedMsg)
	if !ok {
		t.Fatalf("load command returned %T, want hubAdminDialogLoadedMsg", msg)
	}
	model, _ := h.Update(msg)
	h = model.(*Home)

	if !h.hubAdminDialog.IsVisible() {
		t.Fatal("hub admin dialog is not visible after load")
	}
	view := h.hubAdminDialog.View()
	for _, want := range []string{"Trust pending gpu", "Invite gpu", "active"} {
		if !strings.Contains(view, want) {
			t.Fatalf("hub admin dialog missing %q:\n%s", want, view)
		}
	}
}

func TestHubManagementDialogAllowsUserTrustControlsWithoutAdminInvites(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeAdmin = false
	client := &fakeHubAttachClient{
		invites: []hub.AdminInvite{{ID: "invite_hidden", NodeName: "hidden"}},
		trustRequests: []hub.TrustRequestPayload{{
			NodeID:   "node_pending",
			NodeName: "pending laptop",
		}},
	}
	h.hubClient = client

	cmd := h.openHubAdminDialog()
	if cmd == nil {
		t.Fatal("non-admin hub management dialog returned no load command")
	}
	msg, ok := cmd().(hubAdminDialogLoadedMsg)
	if !ok {
		t.Fatalf("load command returned %T, want hubAdminDialogLoadedMsg", msg)
	}
	model, _ := h.Update(msg)
	h = model.(*Home)

	if !h.hubAdminDialog.IsVisible() {
		t.Fatal("non-admin hub management dialog is not visible after load")
	}
	view := h.hubAdminDialog.View()
	for _, want := range []string{"Hub Management", "role: user", "Trust pending laptop"} {
		if !strings.Contains(view, want) {
			t.Fatalf("user hub management dialog missing %q:\n%s", want, view)
		}
	}
	for _, forbidden := range []string{"create invite", "Invite hidden"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("user hub management dialog exposed admin-only %q:\n%s", forbidden, view)
		}
	}
	if client.listInvitesCalls != 0 {
		t.Fatalf("ListInvites calls = %d, want 0 for non-admin", client.listInvitesCalls)
	}
	if client.listTrustRequestsCalls != 1 {
		t.Fatalf("ListTrustRequests calls = %d, want 1", client.listTrustRequestsCalls)
	}

	_, cmd = h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("user allow trust key returned no command")
	}
	if result := cmd().(hubAdminActionResultMsg); result.err != nil || result.action != "trust_allow" || result.id != "node_pending" {
		t.Fatalf("user allow trust result = %+v", result)
	}

	_, cmd = h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil || h.hubAdminDialog.IsCreatingInvite() {
		t.Fatal("non-admin c key exposed invite creation")
	}
}

func TestHubAdminDialogAllowsDeniesTrustAndRevokesInvite(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeAdmin = true
	client := &fakeHubAttachClient{
		invites: []hub.AdminInvite{{
			ID:        "invite_1",
			NodeName:  "gpu",
			ExpiresAt: time.Unix(456, 0).UTC(),
			Status:    "active",
		}},
		trustRequests: []hub.TrustRequestPayload{{
			NodeID:   "node_pending",
			NodeName: "pending gpu",
		}},
	}
	h.hubClient = client
	model, _ := h.Update(h.openHubAdminDialog()().(hubAdminDialogLoadedMsg))
	h = model.(*Home)

	model, cmd := h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("allow trust key returned no command")
	}
	if result := cmd().(hubAdminActionResultMsg); result.err != nil || result.action != "trust_allow" || result.id != "node_pending" {
		t.Fatalf("allow trust result = %+v", result)
	}

	model, cmd = h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("deny trust key returned no command")
	}
	if result := cmd().(hubAdminActionResultMsg); result.err != nil || result.action != "trust_deny" || result.id != "node_pending" {
		t.Fatalf("deny trust result = %+v", result)
	}

	model, _ = h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyDown})
	h = model.(*Home)
	_, cmd = h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if cmd == nil {
		t.Fatal("revoke invite key returned no command")
	}
	if result := cmd().(hubAdminActionResultMsg); result.err != nil || result.action != "invite_revoke" || result.id != "invite_1" {
		t.Fatalf("revoke invite result = %+v", result)
	}

	if len(client.trustDecisions) != 2 ||
		client.trustDecisions[0] != (hubTrustDecisionCall{nodeID: "node_pending", allow: true}) ||
		client.trustDecisions[1] != (hubTrustDecisionCall{nodeID: "node_pending", allow: false}) {
		t.Fatalf("trust decisions = %+v", client.trustDecisions)
	}
	if len(client.revokedInvites) != 1 || client.revokedInvites[0] != "invite_1" {
		t.Fatalf("revoked invites = %+v", client.revokedInvites)
	}
}

func TestHubAdminDialogCreatesUserAndAdminInvites(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeAdmin = true
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubAdminDialog.SetSize(100, 40)
	h.hubAdminDialog.Show(true, nil, nil)

	model, cmd := h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("c should enter invite create mode without a command")
	}
	if !h.hubAdminDialog.IsCreatingInvite() {
		t.Fatal("c did not enter invite create mode")
	}
	h.hubAdminDialog.createNameInput.SetValue("gpu-worker")
	h.hubAdminDialog.createTTLInput.SetValue("12")
	_, cmd = h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("create invite enter returned no command")
	}
	result, ok := cmd().(hubAdminActionResultMsg)
	if !ok {
		t.Fatalf("create invite command returned %T", result)
	}
	if result.err != nil || result.inviteResp == nil || result.action != "invite_create" || result.id != "gpu-worker" {
		t.Fatalf("create invite result = %+v", result)
	}
	if len(client.createdInvites) != 1 {
		t.Fatalf("created invites = %d, want 1", len(client.createdInvites))
	}
	if got := client.createdInvites[0]; got.NodeName != "gpu-worker" || got.TTLSeconds != 12*3600 || got.Admin {
		t.Fatalf("created user invite = %+v", got)
	}
	model, reload := h.Update(result)
	h = model.(*Home)
	if reload == nil {
		t.Fatal("create invite result did not schedule admin dialog reload")
	}
	if !strings.Contains(h.hubAdminDialog.View(), "agent-deck hub join") {
		t.Fatalf("created invite join command missing from dialog:\n%s", h.hubAdminDialog.View())
	}

	model, _ = h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	h = model.(*Home)
	h.hubAdminDialog.createNameInput.SetValue("admin-gpu")
	h.hubAdminDialog.createTTLInput.SetValue("1.5")
	_, cmd = h.handleHubAdminDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("create admin invite enter returned no command")
	}
	if result := cmd().(hubAdminActionResultMsg); result.err != nil {
		t.Fatalf("create admin invite command error = %v", result.err)
	}
	if len(client.createdInvites) != 2 {
		t.Fatalf("created invites = %d, want 2", len(client.createdInvites))
	}
	if got := client.createdInvites[1]; got.NodeName != "admin-gpu" || got.TTLSeconds != 5400 || !got.Admin {
		t.Fatalf("created admin invite = %+v", got)
	}
}

func TestHubManagementDialogClearsInviteTokenAfterDemotion(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeID = "node_local"
	h.hubLocalNodeAdmin = true
	h.hubAdminDialog.Show(true, nil, nil)
	h.hubAdminDialog.FinishCreateInvite(hub.CreateAdminInviteResponse{
		URL:         "wss://hub.example",
		InviteToken: "one-time-secret",
	})
	if !strings.Contains(h.hubAdminDialog.View(), "one-time-secret") {
		t.Fatal("admin dialog setup did not retain the newly created invite command")
	}

	h.applyHubWelcome(hub.WelcomePayload{NodeID: "node_local", NodeName: "local", Admin: false})
	if h.hubAdminDialog.IsAdmin() {
		t.Fatal("live demotion left hub management dialog in admin mode")
	}
	if strings.Contains(h.hubAdminDialog.View(), "one-time-secret") {
		t.Fatalf("demoted user dialog retained an admin invite token:\n%s", h.hubAdminDialog.View())
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

func TestHubSessionNOpensNewSessionDialog(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubLocalNodeAdmin = true
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Groups: []hub.GroupInfo{{
				Path:        "ops",
				Name:        "ops",
				DefaultPath: "/srv/ops",
			}},
			Sessions: []hub.SessionInfo{{
				ID:              "r1",
				Title:           "deploy",
				Tool:            "claude",
				Status:          "waiting",
				GroupPath:       "ops",
				ProjectPath:     "/srv/app",
				AdditionalPaths: []string{"/srv/lib"},
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("n on hub session should open the dialog, not quick-create")
	}
	if !h.newDialog.IsVisible() {
		t.Fatal("n on hub session should open the new-session dialog")
	}
	if len(client.commands) != 0 {
		t.Fatalf("hub commands after n = %d, want 0", len(client.commands))
	}
	if got := h.newDialog.GetSelectedGroup(); got != "ops" {
		t.Fatalf("selected group = %q, want ops", got)
	}
	_, path, command := h.newDialog.GetRemoteValues()
	if path != "/srv/app" {
		t.Fatalf("dialog path = %q, want /srv/app", path)
	}
	if want := []string{"/srv/app", "/srv/lib", "/srv/ops"}; !slices.Equal(h.newDialog.allPathSuggestions, want) {
		t.Fatalf("hub path suggestions = %#v, want %#v", h.newDialog.allPathSuggestions, want)
	}
	if !h.newDialog.remotePathSuggestions {
		t.Fatal("hub dialog should use remote path suggestion mode")
	}
	if command != "claude" {
		t.Fatalf("dialog command = %q, want claude", command)
	}
}

func TestHubEmptyNodeNOpensNewSessionDialog(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node:     hub.Node{ID: "node_server", Name: "server1"},
			Sessions: nil,
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubNode(t, h, "node_server")

	view := h.View()
	if !strings.Contains(view, "server1") {
		t.Fatalf("view missing empty hub node short name:\n%s", view)
	}

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("n on empty hub node should open dialog without command")
	}
	if !h.newDialog.IsVisible() {
		t.Fatal("n on empty hub node should open the new-session dialog")
	}
	if h.pendingHubNodeID != "node_server" || h.pendingHubNodeName != "server1" {
		t.Fatalf("pending hub target = %q/%q, want node_server/server1", h.pendingHubNodeID, h.pendingHubNodeName)
	}
	if got := h.newDialog.GetSelectedGroup(); got != session.DefaultGroupPath {
		t.Fatalf("selected group = %q, want %q", got, session.DefaultGroupPath)
	}

	h.newDialog.nameInput.SetValue("worker")
	_, cmd = h.handleNewDialogKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("submitting hub node dialog returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("hub node create command error = %v", msg.(hubActionResultMsg).err)
	}
	if len(client.commands) != 1 {
		t.Fatalf("hub commands after submit = %d, want 1", len(client.commands))
	}
	if got := client.commands[0]; got.nodeID != "node_server" || got.action != "create" {
		t.Fatalf("hub node create command = %+v", got)
	}
	req, ok := client.commands[0].payload.(hub.CreateSessionRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.CreateSessionRequest", client.commands[0].payload)
	}
	if req.Title != "worker" || req.GroupPath != session.DefaultGroupPath || req.ProjectPath != "." || strings.TrimSpace(req.Tool) == "" {
		t.Fatalf("hub node create request = %+v", req)
	}
}

func TestHubGroupNewSessionDialogUsesRemotePathSuggestions(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubClient = &fakeHubAttachClient{}
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Groups: []hub.GroupInfo{
				{Path: "ops", Name: "ops", DefaultPath: "/srv/ops"},
				{Path: "ml", Name: "ml", DefaultPath: "/srv/ml"},
			},
			Sessions: []hub.SessionInfo{
				{ID: "r1", Title: "deploy", Tool: "claude", Status: "waiting", GroupPath: "ops", ProjectPath: "/srv/app", AdditionalPaths: []string{"/srv/lib", "/srv/ops"}},
				{ID: "r2", Title: "worker", Tool: "codex", Status: "idle", GroupPath: "ml", ProjectPath: "/srv/ml/worker"},
			},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubGroup(t, h, "node_server", "ops")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("n on hub node should open dialog without command")
	}
	if !h.newDialog.IsVisible() {
		t.Fatal("hub node new-session dialog not visible")
	}
	want := []string{"/srv/app", "/srv/lib", "/srv/ml", "/srv/ml/worker", "/srv/ops"}
	if !slices.Equal(h.newDialog.allPathSuggestions, want) {
		t.Fatalf("hub node path suggestions = %#v, want %#v", h.newDialog.allPathSuggestions, want)
	}
	if _, path, _ := h.newDialog.GetRemoteValues(); path != "/srv/ops" {
		t.Fatalf("hub group default path = %q, want /srv/ops", path)
	}
}

func TestHubGroupNewSessionDialogUsesGroupDefaultPath(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubClient = &fakeHubAttachClient{}
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Groups: []hub.GroupInfo{{
				Path:        "ops",
				Name:        "ops",
				DefaultPath: "/srv/ops",
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubGroup(t, h, "node_server", "ops")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("n on hub group should open dialog without command")
	}
	if got := h.newDialog.GetSelectedGroup(); got != "ops" {
		t.Fatalf("selected group = %q, want ops", got)
	}
	if _, path, _ := h.newDialog.GetRemoteValues(); path != "/srv/ops" {
		t.Fatalf("hub group default path = %q, want /srv/ops", path)
	}
}

func TestHubEmptyNodeShiftNQuickCreatesThroughHubCommand(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubNode(t, h, "node_server")

	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if cmd == nil {
		t.Fatal("N on empty hub node returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("N hub node command error = %v", msg.(hubActionResultMsg).err)
	}
	if h.newDialog.IsVisible() {
		t.Fatal("N on empty hub node should not open dialog")
	}
	if len(client.commands) != 1 {
		t.Fatalf("hub commands after N = %d, want 1", len(client.commands))
	}
	req, ok := client.commands[0].payload.(hub.CreateSessionRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.CreateSessionRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_server" || client.commands[0].action != "create" ||
		req.GroupPath != session.DefaultGroupPath || req.ProjectPath != "." || strings.TrimSpace(req.Title) == "" {
		t.Fatalf("quick hub node create = command=%+v req=%+v", client.commands[0], req)
	}
}

func TestHubNodeRenameUsesAdminClient(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubLocalNodeAdmin = true
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubNode(t, h, "node_server")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("r on hub node should open rename dialog without command")
	}
	if !h.groupDialog.IsVisible() || h.groupDialog.Mode() != GroupDialogRenameNode {
		t.Fatalf("hub node rename dialog mode=%v visible=%v", h.groupDialog.Mode(), h.groupDialog.IsVisible())
	}
	h.groupDialog.nameInput.SetValue("desktop")

	model, cmd = h.handleGroupDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub node rename submit returned no command")
	}
	msg := cmd()
	result, ok := msg.(hubActionResultMsg)
	if !ok {
		t.Fatalf("rename msg type = %T, want hubActionResultMsg", msg)
	}
	if result.err != nil {
		t.Fatalf("rename hub node command error = %v", result.err)
	}
	if len(client.renamedNodes) != 1 || client.renamedNodes[0].nodeID != "node_server" || client.renamedNodes[0].name != "desktop" {
		t.Fatalf("renamed nodes = %+v, want node_server/desktop", client.renamedNodes)
	}
	if got := h.hubSessions["node_server"].Node.Name; got != "desktop" {
		t.Fatalf("cached node name = %q, want desktop", got)
	}
	if h.groupDialog.IsVisible() {
		t.Fatal("rename dialog should hide after submit")
	}
}

func TestHubNodeRenameRequiresAdmin(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubLocalNodeAdmin = false
	h.hubClient = &fakeHubAttachClient{}
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubNode(t, h, "node_server")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("r on non-admin hub node should not return a command")
	}
	if h.groupDialog.IsVisible() {
		t.Fatal("non-admin hub node rename should not open dialog")
	}
	if h.err == nil || !strings.Contains(h.err.Error(), "admin") {
		t.Fatalf("non-admin rename error = %v, want admin guidance", h.err)
	}
}

func TestHubNodePromoteDemoteUsesAdminClientAndUpdatesCache(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubLocalNodeAdmin = true
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1", Admin: false},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubNode(t, h, "node_server")

	cmd := h.setHubNodeAdmin(h.flatItems[h.cursor], true)
	if cmd == nil {
		t.Fatal("promote hub node returned no command")
	}
	result, ok := cmd().(hubActionResultMsg)
	if !ok {
		t.Fatalf("promote command msg = %T, want hubActionResultMsg", result)
	}
	if result.err != nil {
		t.Fatalf("promote command error = %v", result.err)
	}
	model, _ := h.Update(result)
	h = model.(*Home)
	if len(client.promotedNodes) != 1 || client.promotedNodes[0] != "node_server" {
		t.Fatalf("promoted nodes = %+v", client.promotedNodes)
	}
	if !h.hubNodeIsAdmin("node_server") {
		t.Fatal("promoted hub node cache admin=false, want true")
	}

	h.cursor = indexHubNode(t, h, "node_server")
	cmd = h.setHubNodeAdmin(h.flatItems[h.cursor], false)
	if cmd == nil {
		t.Fatal("demote hub node returned no command")
	}
	result, ok = cmd().(hubActionResultMsg)
	if !ok {
		t.Fatalf("demote command msg = %T, want hubActionResultMsg", result)
	}
	if result.err != nil {
		t.Fatalf("demote command error = %v", result.err)
	}
	model, _ = h.Update(result)
	h = model.(*Home)
	if len(client.demotedNodes) != 1 || client.demotedNodes[0] != "node_server" {
		t.Fatalf("demoted nodes = %+v", client.demotedNodes)
	}
	if h.hubNodeIsAdmin("node_server") {
		t.Fatal("demoted hub node cache admin=true, want false")
	}
}

func TestHubNodeAdminActionsRequireAdmin(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubLocalNodeAdmin = false
	h.hubClient = &fakeHubAttachClient{}
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubNode(t, h, "node_server")

	if cmd := h.setHubNodeAdmin(h.flatItems[h.cursor], true); cmd != nil {
		t.Fatal("non-admin promote returned command")
	}
	if h.err == nil || !strings.Contains(h.err.Error(), "admin") {
		t.Fatalf("non-admin promote error = %v, want admin guidance", h.err)
	}

	h.clearError()
	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("non-admin revoke returned command")
	}
	if h.confirmDialog.IsVisible() {
		t.Fatal("non-admin revoke opened confirmation dialog")
	}
	if h.err == nil || !strings.Contains(h.err.Error(), "admin") {
		t.Fatalf("non-admin revoke error = %v, want admin guidance", h.err)
	}
}

func TestHubNodeRevokeConfirmsAndUsesAdminClient(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubLocalNodeAdmin = true
	client := &fakeHubAttachClient{}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubNode(t, h, "node_server")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("d on hub node returned command before confirmation")
	}
	if !h.confirmDialog.IsVisible() || h.confirmDialog.GetConfirmType() != ConfirmRevokeHubNode {
		t.Fatalf("revoke confirmation visible/type = %v/%v", h.confirmDialog.IsVisible(), h.confirmDialog.GetConfirmType())
	}

	_, cmd = h.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirm revoke returned no command")
	}
	result, ok := cmd().(hubActionResultMsg)
	if !ok {
		t.Fatalf("revoke command msg = %T, want hubActionResultMsg", result)
	}
	if result.err != nil {
		t.Fatalf("revoke command error = %v", result.err)
	}
	model, _ = h.Update(result)
	h = model.(*Home)
	if len(client.revokedNodes) != 1 || client.revokedNodes[0] != "node_server" {
		t.Fatalf("revoked nodes = %+v", client.revokedNodes)
	}
	if hasHubNode(h, "node_server") {
		t.Fatalf("revoked hub node still present: %+v", h.flatItems)
	}
}

func TestHubCreateResultImmediatelyAddsSessionToCache(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	client := &fakeHubAttachClient{commandResult: mustJSON(t, map[string]string{"session_id": "remote_new"})}
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
		},
	}

	cmd := h.hubCreateCommand("node_server", "server1", hub.CreateSessionRequest{
		Title:       "worker",
		Tool:        "codex",
		ProjectPath: ".",
		GroupPath:   "ops",
	})
	if cmd == nil {
		t.Fatal("hub create command is nil")
	}
	msg := cmd()
	result, ok := msg.(hubActionResultMsg)
	if !ok {
		t.Fatalf("create msg type = %T, want hubActionResultMsg", msg)
	}
	if result.err != nil {
		t.Fatalf("create result error = %v", result.err)
	}
	model, _ := h.Update(result)
	h = model.(*Home)

	info, ok := h.findHubSessionInfo("node_server", "remote_new")
	if !ok {
		t.Fatal("created hub session was not inserted into cache")
	}
	if info.Title != "worker" || info.Tool != "codex" || info.GroupPath != "ops" || info.ProjectPath != "." {
		t.Fatalf("created hub session info = %+v", info)
	}
}

func TestHubSessionShiftNQuickCreatesThroughHubCommand(t *testing.T) {
	h, client := newHubActionHome(t)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("N on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("N hub command error = %v", msg.(hubActionResultMsg).err)
	}
	if h.newDialog.IsVisible() {
		t.Fatal("N on hub session should not open the new-session dialog")
	}
	if len(client.commands) != 1 {
		t.Fatalf("hub commands after N = %d, want 1", len(client.commands))
	}
	if got := client.commands[0]; got.nodeID != "node_server" || got.action != "create" {
		t.Fatalf("N command = %+v", got)
	}
	req, ok := client.commands[0].payload.(hub.CreateSessionRequest)
	if !ok {
		t.Fatalf("N payload type = %T, want hub.CreateSessionRequest", client.commands[0].payload)
	}
	if req.GroupPath != "ops" || req.ProjectPath != "/srv/app" || req.Tool != "claude" || strings.TrimSpace(req.Title) == "" {
		t.Fatalf("N create request = %+v", req)
	}
}

func TestHubSessionQuickForkUsesHubCommand(t *testing.T) {
	h, client := newHubActionHome(t)
	h.hubSessions["node_server"].Sessions[0].CanFork = true
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("f on forkable hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("fork hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "fork", map[string]string{
		"session_id": "r1",
	})
}

func TestHubSessionForkWithOptionsUsesHubForkRequest(t *testing.T) {
	h, client := newHubActionHome(t)
	h.hubSessions["node_server"].Sessions[0].CanFork = true
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("Shift+F should open fork dialog without returning a command")
	}
	if !h.forkDialog.IsVisible() {
		t.Fatal("Shift+F on hub session did not open fork dialog")
	}

	model, cmd = h.handleForkDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("submitting hub fork dialog returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("hub fork options command error = %v", msg.(hubActionResultMsg).err)
	}
	if h.forkDialog.IsVisible() {
		t.Fatal("fork dialog still visible after submit")
	}
	if len(client.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(client.commands))
	}
	if client.commands[0].nodeID != "node_server" || client.commands[0].action != "fork" {
		t.Fatalf("hub command = %+v", client.commands[0])
	}
	req, ok := client.commands[0].payload.(hub.ForkSessionRequest)
	if !ok {
		t.Fatalf("hub fork payload type = %T, want hub.ForkSessionRequest", client.commands[0].payload)
	}
	if req.SessionID != "r1" || req.Title == "" || req.GroupPath != "ops" {
		t.Fatalf("hub fork request = %+v", req)
	}
}

func TestHubSessionDeleteAndCloseUseConfirmations(t *testing.T) {
	h, client := newHubActionHome(t)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("d on hub session should open confirmation without command")
	}
	if h.confirmDialog.GetConfirmType() != ConfirmDeleteHubSession {
		t.Fatalf("d confirm type = %v, want ConfirmDeleteHubSession", h.confirmDialog.GetConfirmType())
	}
	_, cmd = h.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirm hub delete returned no command")
	}
	msg := cmd()
	result, ok := msg.(hubActionResultMsg)
	if !ok {
		t.Fatalf("delete hub command msg type = %T, want hubActionResultMsg", msg)
	}
	if result.err != nil {
		t.Fatalf("delete hub command error = %v", result.err)
	}
	model, _ = h.Update(msg)
	h = model.(*Home)
	assertHubCommand(t, client.commands[0], "node_server", "delete", map[string]string{
		"session_id": "r1",
	})
	if _, ok := h.findHubSessionInfo("node_server", "r1"); ok {
		t.Fatal("confirmed hub delete did not optimistically remove the session from cache")
	}
	if len(h.undoStack) != 1 || h.undoStack[0].hubNodeID != "node_server" || h.undoStack[0].hubSessionID != "r1" {
		t.Fatalf("hub undo stack after delete = %+v", h.undoStack)
	}
	_, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if cmd == nil {
		t.Fatal("ctrl+z after hub delete returned no command")
	}
	msg = cmd()
	result, ok = msg.(hubActionResultMsg)
	if !ok {
		t.Fatalf("undo hub command msg type = %T, want hubActionResultMsg", msg)
	}
	if result.err != nil {
		t.Fatalf("undo hub delete command error = %v", result.err)
	}
	if len(client.commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(client.commands))
	}
	if client.commands[1].nodeID != "node_server" || client.commands[1].action != "undo_delete" {
		t.Fatalf("undo hub command = %+v", client.commands[1])
	}

	h, client = newHubActionHome(t)
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("D on hub session should open confirmation without command")
	}
	if h.confirmDialog.GetConfirmType() != ConfirmCloseHubSession {
		t.Fatalf("D confirm type = %v, want ConfirmCloseHubSession", h.confirmDialog.GetConfirmType())
	}
	_, cmd = h.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirm hub close returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("close hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "stop", map[string]string{
		"session_id": "r1",
	})
}

func TestHubSessionRestartAndPromptUseHubCommand(t *testing.T) {
	h, client := newHubActionHome(t)

	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if cmd == nil {
		t.Fatal("R on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("R hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "restart", map[string]string{
		"session_id": "r1",
	})

	key := defaultHotkeyBindings[hotkeyPromptSession]
	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
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
	_, cmd = h.updateInner(promptSubmitMsg{instanceID: h.promptInputDialog.instanceID, text: "run tests"})
	if cmd == nil {
		t.Fatal("hub prompt submit returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("prompt hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[1], "node_server", "send", map[string]string{
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

func TestHubSessionParityActionsUseHubCommands(t *testing.T) {
	h, client := newHubActionHome(t)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'T'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("T on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("restart fresh hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "restart_fresh", map[string]string{"session_id": "r1"})

	key := defaultHotkeyBindings[hotkeyQuickApprove]
	_, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	if cmd == nil {
		t.Fatal("quick approve on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("quick approve hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[1], "node_server", "send", map[string]string{
		"session_id": "r1",
		"message":    "1",
	})

	_, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil {
		t.Fatal("u on hub session returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("mark unread hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[2], "node_server", "mark_unread", map[string]string{
		"session_id": "r1",
	})
	if got := h.hubSessions["node_server"].Sessions[0].Status; got != string(session.StatusWaiting) {
		t.Fatalf("cached hub status after u = %q, want waiting", got)
	}
}

func TestHubSessionArchiveUnarchiveAndRemoveUseConfirmations(t *testing.T) {
	h, client := newHubActionHome(t)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("A on hub session should open confirmation without command")
	}
	if h.confirmDialog.GetConfirmType() != ConfirmArchiveHubSession {
		t.Fatalf("archive confirm type = %v, want ConfirmArchiveHubSession", h.confirmDialog.GetConfirmType())
	}
	_, cmd = h.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirm hub archive returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("archive hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "archive", map[string]string{"session_id": "r1"})
	if h.hubSessions["node_server"].Sessions[0].ArchivedAt == nil {
		t.Fatal("hub session was not marked archived optimistically")
	}

	h.statusFilter = FilterModeArchived
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'U'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("Shift+U on archived hub session should open confirmation without command")
	}
	if h.confirmDialog.GetConfirmType() != ConfirmUnarchiveHubSession {
		t.Fatalf("unarchive confirm type = %v, want ConfirmUnarchiveHubSession", h.confirmDialog.GetConfirmType())
	}
	_, cmd = h.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirm hub unarchive returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("unarchive hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[1], "node_server", "unarchive", map[string]string{"session_id": "r1"})

	h.statusFilter = ""
	h.hubSessions["node_server"] = hub.NodeSessions{
		Node: hub.Node{ID: "node_server", Name: "server1"},
		Sessions: []hub.SessionInfo{{
			ID: "r1", Title: "deploy", Tool: "claude", Status: "stopped", GroupPath: "ops",
		}},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("X on stopped hub session should open confirmation without command")
	}
	if h.confirmDialog.GetConfirmType() != ConfirmRemoveHubSession {
		t.Fatalf("remove confirm type = %v, want ConfirmRemoveHubSession", h.confirmDialog.GetConfirmType())
	}
	_, cmd = h.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirm hub remove returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("remove hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[2], "node_server", "remove", map[string]string{"session_id": "r1"})
	if len(h.hubSessions["node_server"].Sessions) != 0 {
		t.Fatalf("hub sessions after remove = %d, want 0", len(h.hubSessions["node_server"].Sessions))
	}
}

func TestArchivedHubSessionsOnlyRenderInArchiveView(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	archivedAt := time.Unix(123, 0).UTC()
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{
				{ID: "active", Title: "active remote", Tool: "claude", Status: "waiting", GroupPath: "ops"},
				{ID: "archived", Title: "archived remote", Tool: "claude", Status: "stopped", GroupPath: "ops", ArchivedAt: &archivedAt},
			},
		},
	}

	h.rebuildFlatItems()
	got := h.View()
	if strings.Contains(got, "archived remote") {
		t.Fatalf("active view included archived hub session:\n%s", got)
	}
	if !strings.Contains(got, "active remote") {
		t.Fatalf("active view missing active hub session:\n%s", got)
	}

	h.statusFilter = FilterModeArchived
	h.rebuildFlatItems()
	got = h.View()
	if !strings.Contains(got, "archived remote") {
		t.Fatalf("archive view missing archived hub session:\n%s", got)
	}
	if strings.Contains(got, "active remote") {
		t.Fatalf("archive view included active hub session:\n%s", got)
	}
}

func TestHubGroupReorderKeyUsesHubCommandAndRemoteOrder(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	client := &fakeHubAttachClient{}
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Groups: []hub.GroupInfo{
				{Name: "alpha", Path: "alpha", Order: 0},
				{Name: "beta", Path: "beta", Order: 1},
			},
		},
	}
	h.rebuildFlatItems()
	alphaBefore := indexHubGroup(t, h, "node_server", "alpha")
	betaBefore := indexHubGroup(t, h, "node_server", "beta")
	if alphaBefore >= betaBefore {
		t.Fatalf("initial hub group order alpha=%d beta=%d, want alpha before beta", alphaBefore, betaBefore)
	}
	h.cursor = betaBefore

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("K on hub group returned no command")
	}
	betaAfter := indexHubGroup(t, h, "node_server", "beta")
	alphaAfter := indexHubGroup(t, h, "node_server", "alpha")
	if betaAfter >= alphaAfter {
		t.Fatalf("hub group order after K beta=%d alpha=%d, want beta before alpha", betaAfter, alphaAfter)
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("hub group reorder command error = %v", msg.(hubActionResultMsg).err)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_server" || client.commands[0].action != "group_reorder" {
		t.Fatalf("hub commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(hub.GroupReorderRequest)
	if !ok {
		t.Fatalf("hub reorder payload type = %T, want hub.GroupReorderRequest", client.commands[0].payload)
	}
	if payload.GroupPath != "beta" || payload.Direction != "up" {
		t.Fatalf("hub reorder payload = %+v", payload)
	}
}

func TestHubGroupMoveDialogUsesGroupReparentCommand(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	client := &fakeHubAttachClient{}
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubClient = client
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Groups: []hub.GroupInfo{
				{Name: "ops", Path: "ops", Order: 0},
				{Name: "backend", Path: "ops/backend", Order: 1},
				{Name: "platform", Path: "platform", Order: 2},
			},
			Sessions: []hub.SessionInfo{{
				ID: "r1", Title: "worker", Tool: "claude", Status: "waiting", GroupPath: "ops/backend",
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubGroup(t, h, "node_server", "ops/backend")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("M on hub group returned command, want dialog only")
	}
	if !h.groupDialog.IsVisible() || h.groupDialog.Mode() != GroupDialogMove {
		t.Fatal("M on hub group did not open move dialog")
	}
	targetIdx := -1
	for i, path := range h.groupDialog.groupPaths {
		if path == "platform" {
			targetIdx = i
		}
		if path == "ops/backend" || strings.HasPrefix(path, "ops/backend/") {
			t.Fatalf("hub move targets include invalid source/descendant path %q: %v", path, h.groupDialog.groupPaths)
		}
	}
	if targetIdx < 0 {
		t.Fatalf("hub move targets missing platform: %v", h.groupDialog.groupPaths)
	}
	h.groupDialog.selected = targetIdx

	model, cmd = h.handleGroupDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("confirm hub group move returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("hub group reparent command error = %v", msg.(hubActionResultMsg).err)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_server" || client.commands[0].action != "group_reparent" {
		t.Fatalf("hub commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(hub.GroupReparentRequest)
	if !ok {
		t.Fatalf("hub reparent payload type = %T, want hub.GroupReparentRequest", client.commands[0].payload)
	}
	if payload.GroupPath != "ops/backend" || payload.DestParentPath != "platform" {
		t.Fatalf("hub reparent payload = %+v", payload)
	}
	if _, ok := h.findHubSessionInfo("node_server", "r1"); !ok {
		t.Fatal("hub session disappeared from cache after group reparent")
	}
	info, _ := h.findHubSessionInfo("node_server", "r1")
	if info.GroupPath != "platform/backend" {
		t.Fatalf("hub session group path = %q, want platform/backend", info.GroupPath)
	}
}

func TestHubSessionMoveAndEditUseHubCommands(t *testing.T) {
	h, client := newHubActionHome(t)
	h.hubSessions["node_server"] = hub.NodeSessions{
		Node: hub.Node{ID: "node_server", Name: "server1"},
		Sessions: []hub.SessionInfo{
			{ID: "r1", Title: "deploy", Tool: "claude", Status: "waiting", GroupPath: "ops", ProjectPath: "/srv/app"},
			{ID: "r2", Title: "peer", Tool: "claude", Status: "idle", GroupPath: "infra", ProjectPath: "/srv/app"},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("M on hub session should open move dialog without command")
	}
	for i, path := range h.groupDialog.groupPaths {
		if path == "infra" {
			h.groupDialog.selected = i
			break
		}
	}
	model, cmd = h.handleGroupDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub move submit returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("move hub command error = %v", msg.(hubActionResultMsg).err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "move", map[string]string{
		"session_id": "r1",
		"group_path": "infra",
	})
	if got := h.hubSessions["node_server"].Sessions[0].GroupPath; got != "infra" {
		t.Fatalf("cached hub group = %q, want infra", got)
	}

	h.cursor = indexHubSession(t, h, "r1")
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("P should enter prefix mode without command")
	}
	model, cmd = h.handleSessionActionPrefixKey("e")
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("P e should open edit dialog without command")
	}
	if !h.editSessionDialog.IsVisible() {
		t.Fatal("hub edit dialog not visible")
	}
	h.editSessionDialog.fields[0].input.SetValue("deploy edited")
	model, cmd = h.handleEditSessionDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub edit submit returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("edit hub command error = %v", msg.(hubActionResultMsg).err)
	}
	if got := client.commands[1]; got.nodeID != "node_server" || got.action != "update" {
		t.Fatalf("edit hub command = %+v", got)
	}
	req, ok := client.commands[1].payload.(hub.UpdateSessionRequest)
	if !ok {
		t.Fatalf("edit payload type = %T, want hub.UpdateSessionRequest", client.commands[1].payload)
	}
	if req.SessionID != "r1" || len(req.Changes) != 1 || req.Changes[0].Field != session.FieldTitle || req.Changes[0].Value != "deploy edited" {
		t.Fatalf("edit request = %+v", req)
	}
	if got := h.hubSessions["node_server"].Sessions[0].Title; got != "deploy edited" {
		t.Fatalf("cached hub title = %q, want deploy edited", got)
	}

	cfg, err := session.LoadUserConfig()
	if err != nil {
		t.Fatalf("LoadUserConfig: %v", err)
	}
	showNotes := true
	cfg.Preview.ShowNotes = &showNotes
	if err := session.SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	h.cursor = indexHubSession(t, h, "r1")
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("e should enter hub notes edit mode without command")
	}
	if !h.notesEditing || h.notesEditingHubNodeID != "node_server" || h.notesEditingSessionID != "r1" {
		t.Fatalf("hub notes edit state = editing:%v node:%q session:%q", h.notesEditing, h.notesEditingHubNodeID, h.notesEditingSessionID)
	}
	h.notesEditor.SetValue("remember remote deploy")
	model, cmd = h.handleNotesEditorKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub notes save returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("notes hub command error = %v", msg.(hubActionResultMsg).err)
	}
	if got := client.commands[2]; got.nodeID != "node_server" || got.action != "update" {
		t.Fatalf("notes hub command = %+v", got)
	} else {
		req, ok := got.payload.(hub.UpdateSessionRequest)
		if !ok {
			t.Fatalf("notes payload type = %T, want hub.UpdateSessionRequest", got.payload)
		}
		if req.SessionID != "r1" || len(req.Changes) != 1 || req.Changes[0].Field != session.FieldNotes || req.Changes[0].Value != "remember remote deploy" {
			t.Fatalf("notes request = %+v", req)
		}
	}
	if got := h.hubSessions["node_server"].Sessions[0].Notes; got != "remember remote deploy" {
		t.Fatalf("cached hub notes = %q, want remember remote deploy", got)
	}
}

func TestWebMutatorRoutesHubSessionActionsThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	mutator := NewWebMutator(h)
	webID := web.HubSessionWebID("node_server", "r1")

	if err := mutator.StopSession(webID); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "stop", map[string]string{"session_id": "r1"})

	changed, restartRequired, warnings, err := mutator.UpdateSession(webID, map[string]string{
		session.FieldTitle: "deploy-api",
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if restartRequired || len(warnings) != 0 || len(changed) != 1 || changed[0] != session.FieldTitle {
		t.Fatalf("UpdateSession result changed=%v restart=%v warnings=%v", changed, restartRequired, warnings)
	}
	if got := client.commands[1]; got.nodeID != "node_server" || got.action != "update" {
		t.Fatalf("update command = %+v", got)
	} else {
		req, ok := got.payload.(hub.UpdateSessionRequest)
		if !ok {
			t.Fatalf("update payload type = %T, want hub.UpdateSessionRequest", got.payload)
		}
		if req.SessionID != "r1" || len(req.Changes) != 1 || req.Changes[0].Field != session.FieldTitle || req.Changes[0].Value != "deploy-api" {
			t.Fatalf("update request = %+v", req)
		}
	}

	if err := mutator.RestartFreshSession(webID); err != nil {
		t.Fatalf("RestartFreshSession: %v", err)
	}
	assertHubCommand(t, client.commands[2], "node_server", "restart_fresh", map[string]string{"session_id": "r1"})

	if err := mutator.ToggleYoloSession(webID); err != nil {
		t.Fatalf("ToggleYoloSession: %v", err)
	}
	assertHubCommand(t, client.commands[3], "node_server", "toggle_yolo", map[string]string{"session_id": "r1"})

	if err := mutator.MoveSessionToGroup(webID, "infra"); err != nil {
		t.Fatalf("MoveSessionToGroup: %v", err)
	}
	assertHubCommand(t, client.commands[4], "node_server", "move", map[string]string{"session_id": "r1", "group_path": "infra"})
	if got := h.hubSessions["node_server"].Sessions[0].GroupPath; got != "infra" {
		t.Fatalf("hub group after MoveSessionToGroup = %q, want infra", got)
	}

	if err := mutator.SendSessionPrompt(webID, "run tests"); err != nil {
		t.Fatalf("SendSessionPrompt: %v", err)
	}
	assertHubCommand(t, client.commands[5], "node_server", "send", map[string]string{"session_id": "r1", "message": "run tests"})

	if err := mutator.QuickApproveSession(webID); err != nil {
		t.Fatalf("QuickApproveSession: %v", err)
	}
	assertHubCommand(t, client.commands[6], "node_server", "send", map[string]string{"session_id": "r1", "message": "1"})

	if err := mutator.UpdateSessionNotes(webID, "web notes"); err != nil {
		t.Fatalf("UpdateSessionNotes: %v", err)
	}
	if got := client.commands[7]; got.nodeID != "node_server" || got.action != "update" {
		t.Fatalf("notes command = %+v", got)
	} else {
		req, ok := got.payload.(hub.UpdateSessionRequest)
		if !ok {
			t.Fatalf("notes payload type = %T, want hub.UpdateSessionRequest", got.payload)
		}
		if req.SessionID != "r1" || len(req.Changes) != 1 || req.Changes[0].Field != session.FieldNotes || req.Changes[0].Value != "web notes" {
			t.Fatalf("notes request = %+v", req)
		}
	}
	if got := h.hubSessions["node_server"].Sessions[0].Notes; got != "web notes" {
		t.Fatalf("hub notes after UpdateSessionNotes = %q, want web notes", got)
	}

	if err := mutator.MarkSessionUnread(webID); err != nil {
		t.Fatalf("MarkSessionUnread: %v", err)
	}
	assertHubCommand(t, client.commands[8], "node_server", "mark_unread", map[string]string{"session_id": "r1"})
	if got := h.hubSessions["node_server"].Sessions[0].Status; got != string(session.StatusWaiting) {
		t.Fatalf("hub status after MarkSessionUnread = %q, want waiting", got)
	}

	if err := mutator.DeleteSession(webID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	assertHubCommand(t, client.commands[9], "node_server", "delete", map[string]string{"session_id": "r1"})
	if _, ok := h.findHubSessionInfo("node_server", "r1"); ok {
		t.Fatal("DeleteSession did not remove hub session from cache")
	}
	client.commandResult = json.RawMessage(`{"session_id":"r1"}`)
	restoredID, err := mutator.UndoDelete()
	if err != nil {
		t.Fatalf("UndoDelete: %v", err)
	}
	if restoredID != webID {
		t.Fatalf("UndoDelete restored id = %q, want %q", restoredID, webID)
	}
	if len(client.commands) != 11 {
		t.Fatalf("commands = %d, want 11", len(client.commands))
	}
	if client.commands[10].nodeID != "node_server" || client.commands[10].action != "undo_delete" {
		t.Fatalf("undo command = %+v", client.commands[10])
	}
}

func TestWebMutatorRemovesHubSessionThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	mutator := NewWebMutator(h)
	webID := web.HubSessionWebID("node_server", "r1")

	if err := mutator.RemoveSession(webID); err != nil {
		t.Fatalf("RemoveSession: %v", err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "remove", map[string]string{"session_id": "r1"})
	if _, ok := h.findHubSessionInfo("node_server", "r1"); ok {
		t.Fatal("RemoveSession did not remove hub session from cache")
	}
}

func TestWebMutatorFinishesHubWorktreeThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.WorktreeFinishResponse{
		SessionID:     "r1",
		Branch:        "fork/deploy",
		MergedInto:    "main",
		Merged:        true,
		BranchDeleted: true,
	})
	mutator := NewWebMutator(h)
	webID := web.HubSessionWebID("node_server", "r1")

	result, err := mutator.FinishWorktree(webID, web.WorktreeFinishOptions{Into: "main", KeepBranch: true, Force: true})
	if err != nil {
		t.Fatalf("FinishWorktree: %v", err)
	}
	if result.SessionID != webID || result.Branch != "fork/deploy" || result.MergedInto != "main" || !result.Merged || !result.BranchDeleted {
		t.Fatalf("FinishWorktree result = %+v", result)
	}
	if len(client.commands) != 1 {
		t.Fatalf("commands length = %d, want 1", len(client.commands))
	}
	got := client.commands[0]
	if got.nodeID != "node_server" || got.action != "worktree_finish" {
		t.Fatalf("worktree finish command = %+v", got)
	}
	req, ok := got.payload.(hub.WorktreeFinishRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.WorktreeFinishRequest", got.payload)
	}
	if req.SessionID != "r1" || req.Into != "main" || !req.KeepBranch || !req.Force {
		t.Fatalf("worktree finish request = %+v", req)
	}
	if _, ok := h.findHubSessionInfo("node_server", "r1"); ok {
		t.Fatal("FinishWorktree did not remove hub session from cache")
	}
}

func TestWebMutatorRoutesHubGroupActionsThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.GroupCreateResponse{Path: "ops/api", Name: "api", MaxConcurrent: 1})
	mutator := NewWebMutator(h)

	path, err := mutator.CreateGroup("api", hubWebGroupPath("node_server", "ops"))
	if err != nil {
		t.Fatalf("CreateGroup hub: %v", err)
	}
	if path != hubWebGroupPath("node_server", "ops/api") {
		t.Fatalf("hub CreateGroup path = %q, want hub group path", path)
	}
	createReq, ok := client.commands[0].payload.(hub.GroupCreateRequest)
	if !ok {
		t.Fatalf("create group payload type = %T, want hub.GroupCreateRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_server" || client.commands[0].action != "group_create" || createReq.Name != "api" || createReq.ParentPath != "ops" {
		t.Fatalf("create group command = %+v req=%+v", client.commands[0], createReq)
	}

	client.commandResult = mustJSON(t, hub.GroupRenameResponse{OldPath: "ops/api", Path: "ops/backend", Name: "backend"})
	if err := mutator.RenameGroup(hubWebGroupPath("node_server", "ops/api"), "backend"); err != nil {
		t.Fatalf("RenameGroup hub: %v", err)
	}
	renameReq, ok := client.commands[1].payload.(hub.GroupRenameRequest)
	if !ok {
		t.Fatalf("rename group payload type = %T, want hub.GroupRenameRequest", client.commands[1].payload)
	}
	if client.commands[1].nodeID != "node_server" || client.commands[1].action != "group_rename" || renameReq.GroupPath != "ops/api" || renameReq.Name != "backend" {
		t.Fatalf("rename group command = %+v req=%+v", client.commands[1], renameReq)
	}

	client.commandResult = mustJSON(t, hub.GroupReparentResponse{OldPath: "ops/backend", Path: "platform/backend", DestParentPath: "platform"})
	reparented, err := mutator.ReparentGroup(hubWebGroupPath("node_server", "ops/backend"), hubWebGroupPath("node_server", "platform"))
	if err != nil {
		t.Fatalf("ReparentGroup hub: %v", err)
	}
	if reparented != hubWebGroupPath("node_server", "platform/backend") {
		t.Fatalf("hub ReparentGroup path = %q", reparented)
	}
	reparentReq, ok := client.commands[2].payload.(hub.GroupReparentRequest)
	if !ok {
		t.Fatalf("reparent group payload type = %T, want hub.GroupReparentRequest", client.commands[2].payload)
	}
	if client.commands[2].nodeID != "node_server" || client.commands[2].action != "group_reparent" || reparentReq.GroupPath != "ops/backend" || reparentReq.DestParentPath != "platform" {
		t.Fatalf("reparent group command = %+v req=%+v", client.commands[2], reparentReq)
	}

	client.commandResult = mustJSON(t, hub.GroupReorderResponse{Path: "platform/backend", FromPosition: 2, ToPosition: 1})
	from, to, err := mutator.ReorderGroup(hubWebGroupPath("node_server", "platform/backend"), "up", nil)
	if err != nil {
		t.Fatalf("ReorderGroup hub: %v", err)
	}
	if from != 2 || to != 1 {
		t.Fatalf("hub ReorderGroup positions = %d -> %d", from, to)
	}
	reorderReq, ok := client.commands[3].payload.(hub.GroupReorderRequest)
	if !ok {
		t.Fatalf("reorder group payload type = %T, want hub.GroupReorderRequest", client.commands[3].payload)
	}
	if client.commands[3].nodeID != "node_server" || client.commands[3].action != "group_reorder" || reorderReq.GroupPath != "platform/backend" || reorderReq.Direction != "up" {
		t.Fatalf("reorder group command = %+v req=%+v", client.commands[3], reorderReq)
	}

	client.commandResult = mustJSON(t, hub.GroupDeleteResponse{Path: "platform/backend", SessionsMoved: 0, MovedTo: session.DefaultGroupPath})
	if err := mutator.DeleteGroup(hubWebGroupPath("node_server", "platform/backend")); err != nil {
		t.Fatalf("DeleteGroup hub: %v", err)
	}
	deleteReq, ok := client.commands[4].payload.(hub.GroupDeleteRequest)
	if !ok {
		t.Fatalf("delete group payload type = %T, want hub.GroupDeleteRequest", client.commands[4].payload)
	}
	if client.commands[4].nodeID != "node_server" || client.commands[4].action != "group_delete" || deleteReq.GroupPath != "platform/backend" || !deleteReq.Force {
		t.Fatalf("delete group command = %+v req=%+v", client.commands[4], deleteReq)
	}
}

func TestWebMutatorRoutesHubMCPActionsThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	mutator := NewWebMutator(h)
	hubSessionID := web.HubSessionWebID("node_server", "r1")

	client.commandResult = mustJSON(t, hub.MCPListResponse{
		SessionID: "r1",
		Local:     []string{"exa"},
		Global:    []string{"memory"},
		User:      []string{"github"},
		Catalog:   []hub.MCPCatalogEntry{{Name: "remote-search", Description: "remote catalog", Transport: "stdio", Command: "npx"}},
	})
	attached, err := mutator.ListAttached(hubSessionID, "/srv/app")
	if err != nil {
		t.Fatalf("ListAttached hub: %v", err)
	}
	if attached["local"][0] != "exa" || attached["global"][0] != "memory" || attached["user"][0] != "github" {
		t.Fatalf("attached MCPs = %+v", attached)
	}
	listReq, ok := client.commands[0].payload.(hub.MCPListRequest)
	if !ok {
		t.Fatalf("mcp list payload type = %T, want hub.MCPListRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_server" || client.commands[0].action != "mcp_list" || listReq.SessionID != "r1" {
		t.Fatalf("mcp list command = %+v req=%+v", client.commands[0], listReq)
	}
	client.commands = nil
	state, err := mutator.ListSessionMCPs(hubSessionID, "/srv/app")
	if err != nil {
		t.Fatalf("ListSessionMCPs hub: %v", err)
	}
	if len(state.Catalog) != 1 || state.Catalog[0].Name != "remote-search" || state.Catalog[0].Command != "npx" {
		t.Fatalf("hub mcp catalog = %+v", state.Catalog)
	}
	client.commands = nil

	client.commandResult = mustJSON(t, hub.MCPMutateResponse{SessionID: "r1", Name: "exa", Scope: "local"})
	if err := mutator.Attach(hubSessionID, "/srv/app", "exa", "local"); err != nil {
		t.Fatalf("Attach hub: %v", err)
	}
	attachReq, ok := client.commands[0].payload.(hub.MCPMutateRequest)
	if !ok {
		t.Fatalf("mcp attach payload type = %T, want hub.MCPMutateRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_server" || client.commands[0].action != "mcp_attach" || attachReq.SessionID != "r1" || attachReq.Name != "exa" || attachReq.Scope != "local" {
		t.Fatalf("mcp attach command = %+v req=%+v", client.commands[0], attachReq)
	}

	client.commandResult = mustJSON(t, hub.MCPMutateResponse{SessionID: "r1", Name: "exa", Scope: "local"})
	if err := mutator.Detach(hubSessionID, "/srv/app", "exa", "local"); err != nil {
		t.Fatalf("Detach hub: %v", err)
	}
	detachReq, ok := client.commands[1].payload.(hub.MCPMutateRequest)
	if !ok {
		t.Fatalf("mcp detach payload type = %T, want hub.MCPMutateRequest", client.commands[1].payload)
	}
	if client.commands[1].action != "mcp_detach" || detachReq.SessionID != "r1" || detachReq.Name != "exa" || detachReq.Scope != "local" {
		t.Fatalf("mcp detach command = %+v req=%+v", client.commands[1], detachReq)
	}

	client.commandResult = mustJSON(t, hub.MCPMoveResponse{SessionID: "r1", Name: "exa", FromScope: "local", ToScope: "global"})
	if err := mutator.Move(hubSessionID, "/srv/app", "exa", "local", "global"); err != nil {
		t.Fatalf("Move hub: %v", err)
	}
	moveReq, ok := client.commands[2].payload.(hub.MCPMoveRequest)
	if !ok {
		t.Fatalf("mcp move payload type = %T, want hub.MCPMoveRequest", client.commands[2].payload)
	}
	if client.commands[2].action != "mcp_move" || moveReq.SessionID != "r1" || moveReq.Name != "exa" || moveReq.FromScope != "local" || moveReq.ToScope != "global" {
		t.Fatalf("mcp move command = %+v req=%+v", client.commands[2], moveReq)
	}
}

func TestWebMutatorRoutesHubSkillActionsThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	mutator := NewWebMutator(h)
	hubSessionID := web.HubSessionWebID("node_server", "r1")

	client.commandResult = mustJSON(t, hub.SkillListResponse{
		SessionID: "r1",
		Catalog:   []session.SkillCandidate{{ID: "pool/alpha", Name: "alpha", Source: "pool", Kind: "dir"}},
		Attached:  []session.ProjectSkillAttachment{{ID: "pool/beta", Name: "beta", Source: "pool"}},
	})
	state, err := mutator.ListSessionSkills(hubSessionID, "/srv/app")
	if err != nil {
		t.Fatalf("ListSessionSkills hub: %v", err)
	}
	if state.Catalog[0].Name != "alpha" || state.Attached[0].Name != "beta" {
		t.Fatalf("skill state = %+v", state)
	}
	listReq, ok := client.commands[0].payload.(hub.SkillListRequest)
	if !ok {
		t.Fatalf("skill list payload type = %T, want hub.SkillListRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_server" || client.commands[0].action != "skill_list" || listReq.SessionID != "r1" {
		t.Fatalf("skill list command = %+v req=%+v", client.commands[0], listReq)
	}

	client.commandResult = mustJSON(t, hub.SkillMutateResponse{SessionID: "r1", Skill: &session.ProjectSkillAttachment{ID: "pool/alpha", Name: "alpha", Source: "pool"}})
	if _, err := mutator.AttachSkill(hubSessionID, "/srv/app", "claude", "alpha", "pool"); err != nil {
		t.Fatalf("AttachSkill hub: %v", err)
	}
	attachReq, ok := client.commands[1].payload.(hub.SkillMutateRequest)
	if !ok {
		t.Fatalf("skill attach payload type = %T, want hub.SkillMutateRequest", client.commands[1].payload)
	}
	if client.commands[1].nodeID != "node_server" || client.commands[1].action != "skill_attach" || attachReq.SessionID != "r1" || attachReq.Name != "alpha" || attachReq.Source != "pool" {
		t.Fatalf("skill attach command = %+v req=%+v", client.commands[1], attachReq)
	}

	client.commandResult = mustJSON(t, hub.SkillMutateResponse{SessionID: "r1", Skill: &session.ProjectSkillAttachment{ID: "pool/beta", Name: "beta", Source: "pool"}})
	if _, err := mutator.DetachSkill(hubSessionID, "/srv/app", "beta", "pool"); err != nil {
		t.Fatalf("DetachSkill hub: %v", err)
	}
	detachReq, ok := client.commands[2].payload.(hub.SkillMutateRequest)
	if !ok {
		t.Fatalf("skill detach payload type = %T, want hub.SkillMutateRequest", client.commands[2].payload)
	}
	if client.commands[2].action != "skill_detach" || detachReq.SessionID != "r1" || detachReq.Name != "beta" || detachReq.Source != "pool" {
		t.Fatalf("skill detach command = %+v req=%+v", client.commands[2], detachReq)
	}
}

func TestHubMCPKeyLoadsRemoteAttachedState(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.MCPListResponse{
		SessionID: "r1",
		Local:     []string{"exa"},
		Global:    []string{"memory"},
		User:      []string{"github"},
	})

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("m on hub session returned no command")
	}
	msg := cmd()
	loaded, ok := msg.(hubMCPDialogLoadedMsg)
	if !ok {
		t.Fatalf("command msg type = %T, want hubMCPDialogLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatalf("load hub mcp dialog err = %v", loaded.err)
	}
	model, _ = h.Update(loaded)
	h = model.(*Home)

	if len(client.commands) != 1 || client.commands[0].nodeID != "node_server" || client.commands[0].action != "mcp_list" {
		t.Fatalf("commands = %+v", client.commands)
	}
	req, ok := client.commands[0].payload.(hub.MCPListRequest)
	if !ok || req.SessionID != "r1" {
		t.Fatalf("mcp list payload = %T %+v", client.commands[0].payload, client.commands[0].payload)
	}
	if !h.mcpDialog.IsVisible() {
		t.Fatal("hub mcp dialog did not open")
	}
	if h.hubMCPDialogNodeID != "node_server" || h.hubMCPDialogSessionID != "r1" {
		t.Fatalf("hub mcp state node=%q session=%q", h.hubMCPDialogNodeID, h.hubMCPDialogSessionID)
	}
	names := h.mcpDialog.AttachedNamesByScope()
	if names["local"][0] != "exa" || names["global"][0] != "memory" || names["user"][0] != "github" {
		t.Fatalf("dialog attached names = %+v", names)
	}
}

func TestHubMCPDialogApplyUsesHubCommandsAndRestarts(t *testing.T) {
	h, client := newHubActionHome(t)
	h.mcpDialog.visible = true
	h.mcpDialog.sessionID = "r1"
	h.mcpDialog.tool = "claude"
	h.mcpDialog.localAttached = nil
	h.mcpDialog.globalAttached = []MCPItem{{Name: "exa"}, {Name: "slack"}}
	h.mcpDialog.localChanged = true
	h.mcpDialog.globalChanged = true
	h.hubMCPDialogNodeID = "node_server"
	h.hubMCPDialogNodeName = "server1"
	h.hubMCPDialogSessionID = "r1"
	h.hubMCPDialogInitial = map[string][]string{
		"local":  {"exa"},
		"global": nil,
		"user":   nil,
	}

	model, cmd := h.handleMCPDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub mcp apply returned no command")
	}
	msg := cmd()
	applied, ok := msg.(hubMCPApplyResultMsg)
	if !ok {
		t.Fatalf("command msg type = %T, want hubMCPApplyResultMsg", msg)
	}
	if applied.err != nil {
		t.Fatalf("apply hub mcp err = %v", applied.err)
	}

	if got := len(client.commands); got != 4 {
		t.Fatalf("command count = %d, want 4: %+v", got, client.commands)
	}
	detachReq, ok := client.commands[0].payload.(hub.MCPMutateRequest)
	if !ok || client.commands[0].action != "mcp_detach" || detachReq.SessionID != "r1" || detachReq.Name != "exa" || detachReq.Scope != "local" {
		t.Fatalf("detach command = %+v payload=%+v", client.commands[0], client.commands[0].payload)
	}
	attachReq, ok := client.commands[1].payload.(hub.MCPMutateRequest)
	if !ok || client.commands[1].action != "mcp_attach" || attachReq.Name != "exa" || attachReq.Scope != "global" {
		t.Fatalf("first attach command = %+v payload=%+v", client.commands[1], client.commands[1].payload)
	}
	attachReq, ok = client.commands[2].payload.(hub.MCPMutateRequest)
	if !ok || client.commands[2].action != "mcp_attach" || attachReq.Name != "slack" || attachReq.Scope != "global" {
		t.Fatalf("second attach command = %+v payload=%+v", client.commands[2], client.commands[2].payload)
	}
	restartReq, ok := client.commands[3].payload.(map[string]string)
	if !ok || client.commands[3].action != "restart" || restartReq["session_id"] != "r1" {
		t.Fatalf("restart command = %+v payload=%+v", client.commands[3], client.commands[3].payload)
	}

	model, _ = h.Update(applied)
	h = model.(*Home)
	if h.hubMCPDialogNodeID != "" || h.hubMCPDialogInitial != nil {
		t.Fatalf("hub mcp dialog state was not cleared: node=%q initial=%+v", h.hubMCPDialogNodeID, h.hubMCPDialogInitial)
	}
}

func TestHubPluginKeyLoadsRemoteAttachedState(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.PluginListResponse{
		SessionID: "r1",
		Catalog: []hub.PluginCatalogEntry{
			{Name: "octopus", ID: "octopus@local", Description: "octo"},
			{Name: "discord", ID: "discord@local", Description: "chat"},
		},
		Plugins: []string{"octopus"},
	})

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("L")})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("L on hub session returned no command")
	}
	msg := cmd()
	loaded, ok := msg.(hubPluginDialogLoadedMsg)
	if !ok {
		t.Fatalf("command msg type = %T, want hubPluginDialogLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatalf("load hub plugin dialog err = %v", loaded.err)
	}
	model, _ = h.Update(loaded)
	h = model.(*Home)

	if len(client.commands) != 1 || client.commands[0].nodeID != "node_server" || client.commands[0].action != "plugin_list" {
		t.Fatalf("commands = %+v", client.commands)
	}
	req, ok := client.commands[0].payload.(hub.PluginListRequest)
	if !ok || req.SessionID != "r1" {
		t.Fatalf("plugin list payload = %T %+v", client.commands[0].payload, client.commands[0].payload)
	}
	if !h.pluginDialog.IsVisible() {
		t.Fatal("hub plugin dialog did not open")
	}
	if h.hubPluginDialogNodeID != "node_server" || h.hubPluginDialogSessionID != "r1" {
		t.Fatalf("hub plugin state node=%q session=%q", h.hubPluginDialogNodeID, h.hubPluginDialogSessionID)
	}
	if got := h.pluginDialog.SelectedPluginNames(); len(got) != 1 || got[0] != "octopus" {
		t.Fatalf("attached plugins = %+v", got)
	}
}

func TestHubPluginDialogApplyUsesHubCommandsAndRestarts(t *testing.T) {
	h, client := newHubActionHome(t)
	h.pluginDialog.visible = true
	h.pluginDialog.sessionID = "r1"
	h.pluginDialog.tool = "claude"
	h.pluginDialog.items = []pluginDialogItem{
		{name: "octopus", enabled: false},
		{name: "discord", enabled: true},
	}
	h.pluginDialog.initialEnabled = map[string]bool{"octopus": true, "discord": false}
	h.hubPluginDialogNodeID = "node_server"
	h.hubPluginDialogNodeName = "server1"
	h.hubPluginDialogSessionID = "r1"
	h.hubPluginDialogInitial = []string{"octopus"}

	model, cmd := h.handlePluginDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub plugin apply returned no command")
	}
	msg := cmd()
	applied, ok := msg.(hubPluginApplyResultMsg)
	if !ok {
		t.Fatalf("command msg type = %T, want hubPluginApplyResultMsg", msg)
	}
	if applied.err != nil {
		t.Fatalf("apply hub plugin err = %v", applied.err)
	}

	if got := len(client.commands); got != 3 {
		t.Fatalf("command count = %d, want 3: %+v", got, client.commands)
	}
	detachReq, ok := client.commands[0].payload.(hub.PluginMutateRequest)
	if !ok || client.commands[0].action != "plugin_detach" || detachReq.SessionID != "r1" || detachReq.Name != "octopus" {
		t.Fatalf("detach command = %+v payload=%+v", client.commands[0], client.commands[0].payload)
	}
	attachReq, ok := client.commands[1].payload.(hub.PluginMutateRequest)
	if !ok || client.commands[1].action != "plugin_attach" || attachReq.SessionID != "r1" || attachReq.Name != "discord" {
		t.Fatalf("attach command = %+v payload=%+v", client.commands[1], client.commands[1].payload)
	}
	restartReq, ok := client.commands[2].payload.(map[string]string)
	if !ok || client.commands[2].action != "restart" || restartReq["session_id"] != "r1" {
		t.Fatalf("restart command = %+v payload=%+v", client.commands[2], client.commands[2].payload)
	}

	model, _ = h.Update(applied)
	h = model.(*Home)
	if h.hubPluginDialogNodeID != "" || h.hubPluginDialogInitial != nil {
		t.Fatalf("hub plugin dialog state was not cleared: node=%q initial=%+v", h.hubPluginDialogNodeID, h.hubPluginDialogInitial)
	}
}

func TestWebMutatorRoutesHubPluginActionsThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.PluginListResponse{
		Catalog:  []hub.PluginCatalogEntry{{Name: "octopus", PluginName: "octopus", Source: "nyldn/claude-octopus"}},
		Plugins:  []string{"discord"},
		Channels: []string{"plugin:discord"},
	})
	mutator := NewWebMutator(h)
	webID := web.HubSessionWebID("node_server", "r1")

	state, err := mutator.ListSessionPlugins(webID, &web.MenuSession{ID: webID, Tool: "claude"})
	if err != nil {
		t.Fatalf("ListSessionPlugins: %v", err)
	}
	if len(state.Catalog) != 1 || state.Catalog[0].Name != "octopus" || len(state.Plugins) != 1 || state.Plugins[0] != "discord" {
		t.Fatalf("plugin state = %+v", state)
	}
	if len(client.commands) != 1 || client.commands[0].action != "plugin_list" {
		t.Fatalf("list commands = %+v", client.commands)
	}

	client.commands = nil
	client.commandResult = mustJSON(t, hub.PluginMutateResponse{SessionID: "r1", Plugins: []string{"discord", "octopus"}, Channels: []string{"plugin:discord"}})
	if _, err := mutator.AttachPlugin(webID, &web.MenuSession{ID: webID, Tool: "claude"}, "octopus", true); err != nil {
		t.Fatalf("AttachPlugin: %v", err)
	}
	if len(client.commands) != 1 || client.commands[0].action != "plugin_attach" {
		t.Fatalf("attach commands = %+v", client.commands)
	}
	attachReq, ok := client.commands[0].payload.(hub.PluginMutateRequest)
	if !ok || attachReq.SessionID != "r1" || attachReq.Name != "octopus" || !attachReq.NoChannelLink {
		t.Fatalf("attach payload = %#v", client.commands[0].payload)
	}

	client.commands = nil
	client.commandResult = mustJSON(t, hub.PluginMutateResponse{SessionID: "r1", Plugins: []string{"discord"}, Channels: []string{"plugin:discord"}})
	if _, err := mutator.DetachPlugin(webID, &web.MenuSession{ID: webID, Tool: "claude"}, "octopus"); err != nil {
		t.Fatalf("DetachPlugin: %v", err)
	}
	if len(client.commands) != 1 || client.commands[0].action != "plugin_detach" {
		t.Fatalf("detach commands = %+v", client.commands)
	}
}

func TestHubSkillKeyLoadsRemoteAttachedState(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.SkillListResponse{
		SessionID: "r1",
		Catalog: []session.SkillCandidate{
			{ID: "pool/alpha", Name: "alpha", Source: "pool", Kind: "dir"},
			{ID: "pool/beta", Name: "beta", Source: "pool", Kind: "dir"},
		},
		Attached: []session.ProjectSkillAttachment{{ID: "pool/alpha", Name: "alpha", Source: "pool"}},
	})

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("s on hub session returned no command")
	}
	msg := cmd()
	loaded, ok := msg.(hubSkillDialogLoadedMsg)
	if !ok {
		t.Fatalf("command msg type = %T, want hubSkillDialogLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatalf("load hub skill dialog err = %v", loaded.err)
	}
	model, _ = h.Update(loaded)
	h = model.(*Home)

	if len(client.commands) != 1 || client.commands[0].nodeID != "node_server" || client.commands[0].action != "skill_list" {
		t.Fatalf("commands = %+v", client.commands)
	}
	req, ok := client.commands[0].payload.(hub.SkillListRequest)
	if !ok || req.SessionID != "r1" {
		t.Fatalf("skill list payload = %T %+v", client.commands[0].payload, client.commands[0].payload)
	}
	if !h.skillDialog.IsVisible() {
		t.Fatal("hub skill dialog did not open")
	}
	if h.hubSkillDialogNodeID != "node_server" || h.hubSkillDialogSessionID != "r1" {
		t.Fatalf("hub skill state node=%q session=%q", h.hubSkillDialogNodeID, h.hubSkillDialogSessionID)
	}
	if got := h.skillDialog.AttachedCandidates(); len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("attached skills = %+v", got)
	}
}

func TestHubSkillDialogApplyUsesHubCommandsAndRestarts(t *testing.T) {
	h, client := newHubActionHome(t)
	h.skillDialog.visible = true
	h.skillDialog.sessionID = "r1"
	h.skillDialog.tool = "claude"
	h.skillDialog.attached = []SkillDialogItem{{Candidate: session.SkillCandidate{ID: "pool/beta", Name: "beta", Source: "pool", Kind: "dir"}}}
	h.skillDialog.available = []SkillDialogItem{{Candidate: session.SkillCandidate{ID: "pool/alpha", Name: "alpha", Source: "pool", Kind: "dir"}}}
	h.skillDialog.hasChanges = true
	h.hubSkillDialogNodeID = "node_server"
	h.hubSkillDialogNodeName = "server1"
	h.hubSkillDialogSessionID = "r1"
	h.hubSkillDialogInitial = map[string]session.SkillCandidate{
		"pool/alpha": {ID: "pool/alpha", Name: "alpha", Source: "pool", Kind: "dir"},
	}

	model, cmd := h.handleSkillDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub skill apply returned no command")
	}
	msg := cmd()
	applied, ok := msg.(hubSkillApplyResultMsg)
	if !ok {
		t.Fatalf("command msg type = %T, want hubSkillApplyResultMsg", msg)
	}
	if applied.err != nil {
		t.Fatalf("apply hub skill err = %v", applied.err)
	}

	if got := len(client.commands); got != 3 {
		t.Fatalf("command count = %d, want 3: %+v", got, client.commands)
	}
	detachReq, ok := client.commands[0].payload.(hub.SkillMutateRequest)
	if !ok || client.commands[0].action != "skill_detach" || detachReq.SessionID != "r1" || detachReq.Name != "alpha" || detachReq.Source != "pool" {
		t.Fatalf("detach command = %+v payload=%+v", client.commands[0], client.commands[0].payload)
	}
	attachReq, ok := client.commands[1].payload.(hub.SkillMutateRequest)
	if !ok || client.commands[1].action != "skill_attach" || attachReq.SessionID != "r1" || attachReq.Name != "beta" || attachReq.Source != "pool" {
		t.Fatalf("attach command = %+v payload=%+v", client.commands[1], client.commands[1].payload)
	}
	restartReq, ok := client.commands[2].payload.(map[string]string)
	if !ok || client.commands[2].action != "restart" || restartReq["session_id"] != "r1" {
		t.Fatalf("restart command = %+v payload=%+v", client.commands[2], client.commands[2].payload)
	}

	model, _ = h.Update(applied)
	h = model.(*Home)
	if h.hubSkillDialogNodeID != "" || h.hubSkillDialogInitial != nil {
		t.Fatalf("hub skill dialog state was not cleared: node=%q initial=%+v", h.hubSkillDialogNodeID, h.hubSkillDialogInitial)
	}
}

func TestHubWorktreeFinishKeyUsesHubCommandAndRemovesOnSuccess(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.WorktreeFinishResponse{SessionID: "r1", Branch: "fork/deploy", MergedInto: "main", Merged: true})

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("W")})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("W on hub worktree returned no command")
	}
	msg := cmd()
	result, ok := msg.(hubActionResultMsg)
	if !ok {
		t.Fatalf("command msg type = %T, want hubActionResultMsg", msg)
	}
	if result.err != nil {
		t.Fatalf("hub worktree finish command error = %v", result.err)
	}
	model, _ = h.Update(result)
	h = model.(*Home)

	if len(client.commands) != 1 {
		t.Fatalf("commands length = %d, want 1", len(client.commands))
	}
	got := client.commands[0]
	if got.nodeID != "node_server" || got.action != "worktree_finish" {
		t.Fatalf("worktree finish command = %+v", got)
	}
	req, ok := got.payload.(hub.WorktreeFinishRequest)
	if !ok || req.SessionID != "r1" {
		t.Fatalf("worktree finish payload = %#v", got.payload)
	}
	if _, ok := h.findHubSessionInfo("node_server", "r1"); ok {
		t.Fatal("hub worktree finish success did not remove session from cache")
	}
}

func TestHubWorktreeSetupKeyUsesHubCommand(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.WorktreeSetupResponse{SessionID: "r1"})

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("b on hub worktree returned no command")
	}
	setupKey := hubPromptTarget("node_server", "r1")
	if _, running := h.setupRunningSessions[setupKey]; !running {
		t.Fatalf("setup running key %q was not recorded", setupKey)
	}
	msg := cmd()
	result, ok := msg.(worktreeSetupResultMsg)
	if !ok {
		t.Fatalf("command msg type = %T, want worktreeSetupResultMsg", msg)
	}
	if result.err != nil {
		t.Fatalf("hub worktree setup command error = %v", result.err)
	}
	assertHubCommandPayload(t, client.commands[0], "node_server", "worktree_setup", hub.WorktreeSetupRequest{SessionID: "r1"})

	model, _ = h.Update(result)
	h = model.(*Home)
	if _, running := h.setupRunningSessions[setupKey]; running {
		t.Fatalf("setup running key %q was not cleared", setupKey)
	}
}

func TestWebMutatorUpdatesHubSessionPathsThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	mutator := NewWebMutator(h)
	webID := web.HubSessionWebID("node_server", "r1")

	if err := mutator.UpdateSessionPaths(webID, []string{"/srv/app", "/srv/shared"}); err != nil {
		t.Fatalf("UpdateSessionPaths: %v", err)
	}
	if len(client.commands) != 1 {
		t.Fatalf("commands length = %d, want 1", len(client.commands))
	}
	got := client.commands[0]
	if got.nodeID != "node_server" || got.action != "update_paths" {
		t.Fatalf("update paths command = %+v", got)
	}
	req, ok := got.payload.(hub.UpdateSessionPathsRequest)
	if !ok {
		t.Fatalf("update paths payload type = %T, want hub.UpdateSessionPathsRequest", got.payload)
	}
	if req.SessionID != "r1" || len(req.Paths) != 2 || req.Paths[1] != "/srv/shared" {
		t.Fatalf("update paths request = %+v", req)
	}
}

func TestWebMutatorSendsHubSessionOutputThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.PreviewSessionResponse{Content: "Hub answer"})
	mutator := NewWebMutator(h)
	sourceID := web.HubSessionWebID("node_server", "r1")
	targetID := web.HubSessionWebID("node_server", "r2")

	if err := mutator.SendSessionOutput(sourceID, targetID); err != nil {
		t.Fatalf("SendSessionOutput: %v", err)
	}
	if len(client.commands) != 2 {
		t.Fatalf("commands length = %d, want 2", len(client.commands))
	}
	assertHubCommand(t, client.commands[0], "node_server", "preview", map[string]string{"session_id": "r1"})
	assertHubCommand(t, client.commands[1], "node_server", "send", map[string]string{
		"session_id": "r2",
		"message":    "--- Output from [deploy] ---\nHub answer\n--- End output from [deploy] ---",
	})
}

func TestWebMutatorSessionOutputForHubUsesPreviewCommand(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.PreviewSessionResponse{Content: "Hub answer"})
	mutator := NewWebMutator(h)
	sourceID := web.HubSessionWebID("node_server", "r1")

	got, err := mutator.SessionOutput(sourceID)
	if err != nil {
		t.Fatalf("SessionOutput: %v", err)
	}
	if got.SessionID != sourceID || got.Title != "deploy" || got.Content != "Hub answer" {
		t.Fatalf("SessionOutput response = %+v", got)
	}
	if len(client.commands) != 1 {
		t.Fatalf("commands length = %d, want 1", len(client.commands))
	}
	assertHubCommand(t, client.commands[0], "node_server", "preview", map[string]string{"session_id": "r1"})
}

func TestHubSessionSendOutputUsesHubPreviewAndSendCommand(t *testing.T) {
	h, client := newHubActionHome(t)
	snapshot := h.hubSessions["node_server"]
	snapshot.Sessions = append(snapshot.Sessions, hub.SessionInfo{
		ID:        "r2",
		Title:     "reviewer",
		Tool:      "codex",
		Status:    "waiting",
		GroupPath: "ops",
	})
	h.hubSessions["node_server"] = snapshot
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")
	client.commandResult = mustJSON(t, hub.PreviewSessionResponse{Content: "Hub answer"})

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatalf("x should open picker synchronously, got command %T", cmd)
	}
	if !h.sessionPickerDialog.IsVisible() {
		t.Fatal("x on hub session did not open send-output picker")
	}
	if got := h.sessionPickerDialog.GetSourceTarget(); got.hubNodeID != "node_server" || got.hubSessionID != "r1" {
		t.Fatalf("source target = %+v, want node_server/r1", got)
	}
	selected := h.sessionPickerDialog.GetSelectedTarget()
	if selected.hubNodeID != "node_server" || selected.hubSessionID != "r2" {
		t.Fatalf("selected target = %+v, want node_server/r2", selected)
	}

	_, cmd = h.handleSessionPickerDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter in hub send-output picker returned no command")
	}
	msg := cmd()
	result, ok := msg.(sendOutputResultMsg)
	if !ok {
		t.Fatalf("send command returned %T, want sendOutputResultMsg", msg)
	}
	if result.err != nil {
		t.Fatalf("hub send output error = %v", result.err)
	}
	assertHubCommand(t, client.commands[0], "node_server", "preview", map[string]string{"session_id": "r1"})
	assertHubCommand(t, client.commands[1], "node_server", "send", map[string]string{
		"session_id": "r2",
		"message":    "--- Output from [deploy] ---\nHub answer\n--- End output from [deploy] ---\n",
	})
}

func TestLocalSessionSendOutputPickerIncludesHubTargets(t *testing.T) {
	local := &session.Instance{ID: "local_1", Title: "local worker", Tool: "claude", Status: session.StatusWaiting}
	h := newHubProjectionHome(t, []*session.Instance{local})
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
	for i, item := range h.flatItems {
		if item.Type == session.ItemTypeSession && item.Session != nil && item.Session.ID == "local_1" {
			h.cursor = i
			break
		}
	}

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatalf("x should open picker synchronously, got command %T", cmd)
	}
	if !h.sessionPickerDialog.IsVisible() {
		t.Fatal("x on local session did not open picker with hub target")
	}
	selected := h.sessionPickerDialog.GetSelectedTarget()
	if selected.kind != sendOutputTargetHub || selected.hubNodeID != "node_server" || selected.hubSessionID != "r1" {
		t.Fatalf("selected target = %+v, want hub node_server/r1", selected)
	}
}

func TestWebMutatorForksHubSessionThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, map[string]string{"session_id": "forked_remote"})
	mutator := NewWebMutator(h)
	webID := web.HubSessionWebID("node_server", "r1")

	gotID, err := mutator.ForkSession(webID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if gotID != web.HubSessionWebID("node_server", "forked_remote") {
		t.Fatalf("ForkSession id = %q, want hub web id", gotID)
	}
	assertHubCommand(t, client.commands[0], "node_server", "fork", map[string]string{"session_id": "r1"})
}

func TestWebMutatorCreatesHubSessionThroughHubClient(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, map[string]string{"session_id": "remote_new"})
	mutator := NewWebMutator(h)

	gotID, err := mutator.CreateHubSession("worker", "codex", ".", "ops", "gpt-5", "node_server")
	if err != nil {
		t.Fatalf("CreateHubSession: %v", err)
	}
	if gotID != web.HubSessionWebID("node_server", "remote_new") {
		t.Fatalf("CreateHubSession id = %q, want hub web id", gotID)
	}
	if got := client.commands[0]; got.nodeID != "node_server" || got.action != "create" {
		t.Fatalf("create command = %+v", got)
	} else {
		req, ok := got.payload.(hub.CreateSessionRequest)
		if !ok {
			t.Fatalf("create payload type = %T, want hub.CreateSessionRequest", got.payload)
		}
		if req.Title != "worker" || req.Tool != "codex" || req.ProjectPath != "." || req.GroupPath != "ops" || req.ModelID != "gpt-5" {
			t.Fatalf("create request = %+v", req)
		}
	}
}

func TestHubSessionImportHotkeyOpensLocalImportDialog(t *testing.T) {
	h, client := newHubActionHome(t)
	codexHome := filepath.Join(t.TempDir(), ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	id := "88888888-8888-8888-8888-888888888888"
	writeCodexIndexForHomeImport(t, codexHome, id, "saved codex", "2026-06-30T10:00:00Z")
	writeCodexRolloutForHomeImport(t, codexHome, id)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("i on hub session should load local import options")
	}
	model, _ = h.updateInner(cmd())
	h = model.(*Home)
	if h.importSourceDialog == nil || !h.importSourceDialog.IsVisible() {
		t.Fatal("i on hub session should open the local import source dialog")
	}
	if got := h.importSourceDialog.CodexCount(); got != 1 {
		t.Fatalf("codex count = %d, want 1", got)
	}
	if len(client.commands) != 0 {
		t.Fatalf("hub commands after i = %d, want 0", len(client.commands))
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

func TestHubSessionCopyOutputUsesHubPreviewCommand(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.PreviewSessionResponse{Content: "   "})

	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("c on hub session returned no command")
	}
	msg := cmd()
	result, ok := msg.(copyResultMsg)
	if !ok {
		t.Fatalf("copy command returned %T, want copyResultMsg", msg)
	}
	if result.err == nil {
		t.Fatal("empty hub preview copy unexpectedly succeeded")
	}
	assertHubCommand(t, client.commands[0], "node_server", "preview", map[string]string{
		"session_id": "r1",
	})
}

func TestHubSessionShiftYOpensCodeBlockPickerFromHubPreview(t *testing.T) {
	h, client := newHubActionHome(t)
	client.commandResult = mustJSON(t, hub.PreviewSessionResponse{Content: "first\n```sh\necho one\n```\nthen\n```go\nfmt.Println(\"two\")\n```"})

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("Y on hub session returned no command")
	}
	msg := cmd()
	blocks, ok := msg.(hubCodeBlockBlocksMsg)
	if !ok {
		t.Fatalf("code block command returned %T, want hubCodeBlockBlocksMsg", msg)
	}
	if blocks.err != nil {
		t.Fatalf("hub code block extraction error = %v", blocks.err)
	}
	if len(blocks.blocks) != 2 {
		t.Fatalf("hub code blocks = %d, want 2", len(blocks.blocks))
	}
	assertHubCommand(t, client.commands[0], "node_server", "preview", map[string]string{
		"session_id": "r1",
	})

	model, _ = h.Update(blocks)
	h = model.(*Home)
	if !h.codeBlockDialog.IsVisible() {
		t.Fatal("hub code block result did not open picker")
	}
	if got := h.codeBlockDialog.sessionTitle; got != "server1/deploy" {
		t.Fatalf("code block dialog title = %q, want server1/deploy", got)
	}
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

func TestHubGroupCreateRenameDeleteUseHubCommands(t *testing.T) {
	h, client := newHubActionHome(t)
	h.hubSessions["node_server"] = hub.NodeSessions{
		Node: hub.Node{ID: "node_server", Name: "server1"},
		Groups: []hub.GroupInfo{
			{Name: "ops", Path: "ops", Expanded: true},
			{Name: "empty", Path: "empty", Expanded: true},
		},
		Sessions: []hub.SessionInfo{{ID: "r1", Title: "deploy", Tool: "claude", Status: "waiting", GroupPath: "ops"}},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubGroup(t, h, "node_server", "ops")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("g on hub group should open create dialog without command")
	}
	if !h.groupDialog.IsVisible() || h.groupDialog.GetParentPath() != "ops" {
		t.Fatalf("hub group create dialog parent = %q visible=%v", h.groupDialog.GetParentPath(), h.groupDialog.IsVisible())
	}
	h.groupDialog.nameInput.SetValue("api")
	model, cmd = h.handleGroupDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub group create submit returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("hub group create command error = %v", msg.(hubActionResultMsg).err)
	}
	createReq, ok := client.commands[0].payload.(hub.GroupCreateRequest)
	if !ok {
		t.Fatalf("group create payload type = %T, want hub.GroupCreateRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_server" || client.commands[0].action != "group_create" || createReq.Name != "api" || createReq.ParentPath != "ops" {
		t.Fatalf("group create command = %+v req=%+v", client.commands[0], createReq)
	}

	h.cursor = indexHubGroup(t, h, "node_server", "empty")
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("r on hub group should open rename dialog without command")
	}
	if !h.groupDialog.IsVisible() || h.groupDialog.GetGroupPath() != "empty" {
		t.Fatalf("hub group rename dialog path = %q visible=%v", h.groupDialog.GetGroupPath(), h.groupDialog.IsVisible())
	}
	h.groupDialog.nameInput.SetValue("renamed")
	model, cmd = h.handleGroupDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("hub group rename submit returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("hub group rename command error = %v", msg.(hubActionResultMsg).err)
	}
	renameReq, ok := client.commands[1].payload.(hub.GroupRenameRequest)
	if !ok {
		t.Fatalf("group rename payload type = %T, want hub.GroupRenameRequest", client.commands[1].payload)
	}
	if client.commands[1].nodeID != "node_server" || client.commands[1].action != "group_rename" || renameReq.GroupPath != "empty" || renameReq.Name != "renamed" {
		t.Fatalf("group rename command = %+v req=%+v", client.commands[1], renameReq)
	}

	h.cursor = indexHubGroup(t, h, "node_server", "renamed")
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("d on hub group should open confirmation without command")
	}
	if h.confirmDialog.GetConfirmType() != ConfirmDeleteHubGroup {
		t.Fatalf("delete hub group confirm type = %v, want ConfirmDeleteHubGroup", h.confirmDialog.GetConfirmType())
	}
	_, cmd = h.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirm hub group delete returned no command")
	}
	if msg := cmd(); msg.(hubActionResultMsg).err != nil {
		t.Fatalf("hub group delete command error = %v", msg.(hubActionResultMsg).err)
	}
	deleteReq, ok := client.commands[2].payload.(hub.GroupDeleteRequest)
	if !ok {
		t.Fatalf("group delete payload type = %T, want hub.GroupDeleteRequest", client.commands[2].payload)
	}
	if client.commands[2].nodeID != "node_server" || client.commands[2].action != "group_delete" || deleteReq.GroupPath != "renamed" || !deleteReq.Force {
		t.Fatalf("group delete command = %+v req=%+v", client.commands[2], deleteReq)
	}
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

func TestHubAttachCmdCallsClientWindow(t *testing.T) {
	client := &fakeHubAttachClient{}
	windowIndex := 1
	cmd := hubAttachCmd{
		client:      client,
		nodeID:      "node_server",
		sessionID:   "remote_session",
		windowIndex: &windowIndex,
		size:        hub.TerminalSize{Cols: 120, Rows: 40},
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !client.attachWindowCalled || client.windowIndex != 1 {
		t.Fatalf("window attach call = called %v index %d", client.attachWindowCalled, client.windowIndex)
	}
	if client.nodeID != "node_server" || client.sessionID != "remote_session" {
		t.Fatalf("attach window call = node %q session %q", client.nodeID, client.sessionID)
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

func TestHubSandboxShellAttachCmdOpensShellThenAttachesToken(t *testing.T) {
	client := &fakeHubAttachClient{
		commandResult: mustJSON(t, hub.SandboxShellResponse{SessionID: "r1", AttachSessionID: "tmuxattach-token"}),
	}
	cmd := hubSandboxShellAttachCmd{
		client:    client,
		nodeID:    "node_server",
		sessionID: "r1",
		size:      hub.TerminalSize{Cols: 120, Rows: 40},
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(client.commands) != 1 {
		t.Fatalf("hub commands = %d, want sandbox shell command", len(client.commands))
	}
	assertHubCommandPayload(t, client.commands[0], "node_server", "sandbox_shell", hub.SandboxShellRequest{SessionID: "r1"})
	if client.nodeID != "node_server" || client.sessionID != "tmuxattach-token" {
		t.Fatalf("attach call = node %q session %q", client.nodeID, client.sessionID)
	}
	if client.size.Cols != 120 || client.size.Rows != 40 {
		t.Fatalf("attach size = %+v, want 120x40", client.size)
	}
}

func TestHubEnterOnSandboxSessionStartsSandboxShellAttach(t *testing.T) {
	h, _ := newHubActionHome(t)
	snapshot := h.hubSessions["node_server"]
	snapshot.Sessions[0].Sandbox = json.RawMessage(`{"enabled":true,"image":"sandbox:latest"}`)
	snapshot.Sessions[0].SandboxContainer = "agent-deck-sbx-r1"
	h.hubSessions["node_server"] = snapshot
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("E on hub sandbox session returned no command")
	}
	if h.err != nil {
		t.Fatalf("E on hub sandbox session error = %v", h.err)
	}
	if !h.isAttaching.Load() {
		t.Fatal("E on hub sandbox session did not mark Home as attaching")
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

func TestHubSessionProjectsRemoteWindows(t *testing.T) {
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
				GroupPath: "ops",
				Windows: []hub.WindowInfo{{
					Index: 0,
					Name:  "main",
					Tool:  "claude",
				}, {
					Index: 1,
					Name:  "logs",
					Tool:  "shell",
				}},
			}},
		},
	}

	h.rebuildFlatItems()

	sessionIdx := indexHubSession(t, h, "r1")
	if sessionIdx+2 >= len(h.flatItems) {
		t.Fatalf("flatItems missing projected window rows after session: %+v", h.flatItems)
	}
	first := h.flatItems[sessionIdx+1]
	second := h.flatItems[sessionIdx+2]
	if first.Type != session.ItemTypeWindow || second.Type != session.ItemTypeWindow {
		t.Fatalf("projected items after hub session = %v/%v, want windows", first.Type, second.Type)
	}
	wantParent := web.HubSessionWebID("node_server", "r1")
	if first.WindowSessionID != wantParent || second.WindowSessionID != wantParent || second.WindowIndex != 1 || second.WindowName != "logs" || second.WindowTool != "shell" {
		t.Fatalf("projected hub windows = %+v / %+v", first, second)
	}
	if first.HubNodeID != "node_server" || first.HubSession == nil || first.HubSession.ID != "r1" {
		t.Fatalf("projected hub window lost hub identity: %+v", first)
	}
}

func TestHubEnterOnWindowStartsWindowAttachCommand(t *testing.T) {
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
				Windows: []hub.WindowInfo{{
					Index: 0,
					Name:  "main",
				}, {
					Index: 2,
					Name:  "logs",
				}},
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubWindow(t, h, "node_server", "r1", 2)

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("Enter on hub window returned no command")
	}
	if h.err != nil {
		t.Fatalf("Enter on hub window error = %v", h.err)
	}
	if !h.isAttaching.Load() {
		t.Fatal("Enter on hub window did not mark Home as attaching")
	}
}

func TestHubSessionShiftCCopiesProjectedSessionInfo(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			Node: hub.Node{ID: "node_server", Name: "server1"},
			Sessions: []hub.SessionInfo{{
				ID:               "r1",
				Title:            "deploy",
				Tool:             "codex",
				Status:           "waiting",
				GroupPath:        "ops",
				ProjectPath:      "/srv/app",
				CodexSessionID:   "codex-remote",
				DisplaySessionID: "codex-remote",
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("Shift+C on hub session returned no copy command")
	}
	if h.err != nil {
		t.Fatalf("Shift+C on hub session error = %v", h.err)
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
	t.Cleanup(h.cancel)
	h.width = 120
	h.height = 40
	h.initialLoading = false
	h.instancesMu.Lock()
	h.instances = instances
	h.instanceByID = make(map[string]*session.Instance, len(instances))
	for _, inst := range instances {
		h.instanceByID[inst.ID] = inst
	}
	h.instancesMu.Unlock()
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
				ID:               "r1",
				Title:            "deploy",
				Tool:             "claude",
				Status:           "waiting",
				GroupPath:        "ops",
				ProjectPath:      "/srv/app",
				WorktreePath:     "/srv/app/.worktrees/deploy",
				WorktreeRepoRoot: "/srv/app",
				WorktreeBranch:   "fork/deploy",
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")
	return h, client
}

type fakeHubAttachClient struct {
	nodeID                 string
	sessionID              string
	windowIndex            int
	attachWindowCalled     bool
	size                   hub.TerminalSize
	commands               []hubCommandCall
	renamedNodes           []hubNodeRenameCall
	promotedNodes          []string
	demotedNodes           []string
	revokedNodes           []string
	trustDecisions         []hubTrustDecisionCall
	invites                []hub.AdminInvite
	createdInvites         []hub.CreateAdminInviteRequest
	revokedInvites         []string
	trustRequests          []hub.TrustRequestPayload
	listInvitesCalls       int
	listTrustRequestsCalls int
	commandErr             error
	commandResult          json.RawMessage
}

func (c *fakeHubAttachClient) Attach(ctx context.Context, nodeID, sessionID string, size hub.TerminalSize) error {
	c.nodeID = nodeID
	c.sessionID = sessionID
	c.size = size
	return nil
}

func (c *fakeHubAttachClient) AttachWindow(ctx context.Context, nodeID, sessionID string, windowIndex int, size hub.TerminalSize) error {
	c.nodeID = nodeID
	c.sessionID = sessionID
	c.windowIndex = windowIndex
	c.attachWindowCalled = true
	c.size = size
	return nil
}

func (c *fakeHubAttachClient) OpenAttach(ctx context.Context, nodeID, sessionID string, size hub.TerminalSize) (hub.AttachStream, error) {
	c.nodeID = nodeID
	c.sessionID = sessionID
	c.size = size
	return nil, nil
}

func (c *fakeHubAttachClient) OpenAttachWindow(ctx context.Context, nodeID, sessionID string, windowIndex int, size hub.TerminalSize) (hub.AttachStream, error) {
	c.nodeID = nodeID
	c.sessionID = sessionID
	c.windowIndex = windowIndex
	c.attachWindowCalled = true
	c.size = size
	return nil, nil
}

func (c *fakeHubAttachClient) Command(ctx context.Context, nodeID, action string, payload any) (json.RawMessage, error) {
	c.commands = append(c.commands, hubCommandCall{nodeID: nodeID, action: action, payload: payload})
	if c.commandErr != nil {
		return nil, c.commandErr
	}
	return c.commandResult, nil
}

func (c *fakeHubAttachClient) RenameNode(ctx context.Context, nodeID, name string) (hub.Node, error) {
	c.renamedNodes = append(c.renamedNodes, hubNodeRenameCall{nodeID: nodeID, name: name})
	return hub.Node{ID: nodeID, Name: name}, nil
}

func (c *fakeHubAttachClient) PromoteNode(ctx context.Context, nodeID string) (hub.Node, error) {
	c.promotedNodes = append(c.promotedNodes, nodeID)
	return hub.Node{ID: nodeID, Admin: true}, nil
}

func (c *fakeHubAttachClient) DemoteNode(ctx context.Context, nodeID string) (hub.Node, error) {
	c.demotedNodes = append(c.demotedNodes, nodeID)
	return hub.Node{ID: nodeID, Admin: false}, nil
}

func (c *fakeHubAttachClient) RevokeNode(ctx context.Context, nodeID string) error {
	c.revokedNodes = append(c.revokedNodes, nodeID)
	return nil
}

func (c *fakeHubAttachClient) TrustDecision(ctx context.Context, nodeID string, allow bool) error {
	c.trustDecisions = append(c.trustDecisions, hubTrustDecisionCall{nodeID: nodeID, allow: allow})
	return nil
}

func (c *fakeHubAttachClient) ListInvites(ctx context.Context) ([]hub.AdminInvite, error) {
	c.listInvitesCalls++
	return append([]hub.AdminInvite(nil), c.invites...), nil
}

func (c *fakeHubAttachClient) CreateInvite(ctx context.Context, req hub.CreateAdminInviteRequest) (hub.CreateAdminInviteResponse, error) {
	c.createdInvites = append(c.createdInvites, req)
	return hub.CreateAdminInviteResponse{
		URL:         "wss://hub.example",
		InviteToken: "invite-token",
		ExpiresAt:   time.Unix(789, 0).UTC(),
	}, nil
}

func (c *fakeHubAttachClient) RevokeInvite(ctx context.Context, inviteID string) error {
	c.revokedInvites = append(c.revokedInvites, inviteID)
	return nil
}

func (c *fakeHubAttachClient) ListTrustRequests(ctx context.Context) ([]hub.TrustRequestPayload, error) {
	c.listTrustRequestsCalls++
	return append([]hub.TrustRequestPayload(nil), c.trustRequests...), nil
}

func (c *fakeHubAttachClient) Close() error {
	return nil
}

type hubCommandCall struct {
	nodeID  string
	action  string
	payload any
}

type hubNodeRenameCall struct {
	nodeID string
	name   string
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

func assertHubCommandPayload[T comparable](t *testing.T, got hubCommandCall, nodeID, action string, want T) {
	t.Helper()
	if got.nodeID != nodeID || got.action != action {
		t.Fatalf("hub command = %+v, want node=%q action=%q", got, nodeID, action)
	}
	payload, ok := got.payload.(T)
	if !ok {
		t.Fatalf("hub command payload type = %T, want %T", got.payload, want)
	}
	if payload != want {
		t.Fatalf("hub command payload = %+v, want %+v", payload, want)
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

func webSnapshotHasHubSession(snapshot *web.MenuSnapshot, nodeID, sessionID string) bool {
	return webSnapshotHubSession(snapshot, nodeID, sessionID) != nil
}

func webSnapshotHubSession(snapshot *web.MenuSnapshot, nodeID, sessionID string) *web.MenuSession {
	if snapshot == nil {
		return nil
	}
	wantID := web.HubSessionWebID(nodeID, sessionID)
	for _, item := range snapshot.Items {
		if item.Type == web.MenuItemTypeSession && item.Session != nil && item.Session.ID == wantID {
			if item.Session.Source == "hub" && item.Session.HubNodeID == nodeID && item.Session.HubSessionID == sessionID {
				return item.Session
			}
			return nil
		}
	}
	return nil
}

func webSnapshotHubSessionCanFork(snapshot *web.MenuSnapshot, nodeID, sessionID string) bool {
	if snapshot == nil {
		return false
	}
	wantID := web.HubSessionWebID(nodeID, sessionID)
	for _, item := range snapshot.Items {
		if item.Type == web.MenuItemTypeSession && item.Session != nil && item.Session.ID == wantID {
			return item.Session.CanFork
		}
	}
	return false
}

func webSnapshotHubSessionNotes(snapshot *web.MenuSnapshot, nodeID, sessionID string) string {
	if snapshot == nil {
		return ""
	}
	wantID := web.HubSessionWebID(nodeID, sessionID)
	for _, item := range snapshot.Items {
		if item.Type == web.MenuItemTypeSession && item.Session != nil && item.Session.ID == wantID {
			return item.Session.Notes
		}
	}
	return ""
}

func webSnapshotHubSessionWorktree(snapshot *web.MenuSnapshot, nodeID, sessionID string) string {
	if snapshot == nil {
		return ""
	}
	wantID := web.HubSessionWebID(nodeID, sessionID)
	for _, item := range snapshot.Items {
		if item.Type == web.MenuItemTypeSession && item.Session != nil && item.Session.ID == wantID {
			if item.Session.WorktreePath == "" || item.Session.WorktreeRepoRoot == "" {
				return ""
			}
			return item.Session.WorktreeBranch
		}
	}
	return ""
}

func webSnapshotHasHubGroup(snapshot *web.MenuSnapshot, nodeID, groupPath string, sessionCount int) bool {
	if snapshot == nil {
		return false
	}
	for _, item := range snapshot.Items {
		if item.Type != web.MenuItemTypeGroup || item.Group == nil {
			continue
		}
		if item.Group.Source == "hub" && item.Group.HubNodeID == nodeID && item.Group.HubGroupPath == groupPath && item.Group.SessionCount == sessionCount {
			return true
		}
	}
	return false
}

func webSnapshotHasHubNode(snapshot *web.MenuSnapshot, nodeID, nodeName string) bool {
	if snapshot == nil {
		return false
	}
	for _, node := range snapshot.HubNodes {
		if node.ID == nodeID && node.Name == nodeName {
			return true
		}
	}
	return false
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

func indexHubWindow(t *testing.T, h *Home, nodeID, sessionID string, windowIndex int) int {
	t.Helper()
	wantParent := web.HubSessionWebID(nodeID, sessionID)
	for i, item := range h.flatItems {
		if item.Type == session.ItemTypeWindow && item.HubNodeID == nodeID && item.WindowSessionID == wantParent && item.WindowIndex == windowIndex {
			return i
		}
	}
	t.Fatalf("hub window %q/%q:%d not found in flatItems: %+v", nodeID, sessionID, windowIndex, h.flatItems)
	return -1
}

func indexHubNode(t *testing.T, h *Home, nodeID string) int {
	t.Helper()
	for i, item := range h.flatItems {
		if item.Type == session.ItemTypeHubNode && item.HubNodeID == nodeID {
			return i
		}
	}
	t.Fatalf("hub node %q not found in flatItems: %+v", nodeID, h.flatItems)
	return -1
}

func hasHubNode(h *Home, nodeID string) bool {
	for _, item := range h.flatItems {
		if item.Type == session.ItemTypeHubNode && item.HubNodeID == nodeID {
			return true
		}
	}
	return false
}

func indexHubGroup(t *testing.T, h *Home, nodeID, groupPath string) int {
	t.Helper()
	for i, item := range h.flatItems {
		if item.Type == session.ItemTypeHubGroup && item.HubNodeID == nodeID && item.HubGroupPath == groupPath {
			return i
		}
	}
	t.Fatalf("hub group %q/%q not found in flatItems: %+v", nodeID, groupPath, h.flatItems)
	return -1
}
