package ui

import (
	"strings"
	"testing"

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
