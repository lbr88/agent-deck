package ui

import (
	"context"
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

func TestHubRowsDoNotCreateLocalSessions(t *testing.T) {
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
			}},
		},
	}
	h.rebuildFlatItems()
	h.cursor = indexHubSession(t, h, "r1")

	model, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("n on hub session returned a command")
	}
	if h.newDialog.IsVisible() {
		t.Fatal("n on hub session opened the local new-session dialog")
	}
	if h.err == nil || !strings.Contains(h.err.Error(), "hub session creation is not available yet") {
		t.Fatalf("n on hub session error = %v", h.err)
	}

	h.err = nil
	model, cmd = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("N on hub session returned a local creation command")
	}
	if h.err == nil || !strings.Contains(h.err.Error(), "hub session creation is not available yet") {
		t.Fatalf("N on hub session error = %v", h.err)
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

type fakeHubAttachClient struct {
	nodeID    string
	sessionID string
	size      hub.TerminalSize
}

func (c *fakeHubAttachClient) Attach(ctx context.Context, nodeID, sessionID string, size hub.TerminalSize) error {
	c.nodeID = nodeID
	c.sessionID = sessionID
	c.size = size
	return nil
}

func (c *fakeHubAttachClient) Close() error {
	return nil
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
