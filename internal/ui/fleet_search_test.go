package ui

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func TestFleetSearchResultsContainLocalRemoteAndHubSessions(t *testing.T) {
	local := &session.Instance{
		ID:          "local-1",
		Title:       "local session",
		ProjectPath: "/work/local",
		GroupPath:   "dev",
		Tool:        "codex",
		Status:      session.StatusWaiting,
	}
	h := newHubProjectionHome(t, []*session.Instance{local})
	h.hubConfigured = true
	h.hubLocalNodeID = "node-local"
	h.hubLocalNodeName = "work-laptop"
	h.remoteSessions = map[string][]session.RemoteSessionInfo{
		"legacy-ssh": {{
			ID:     "remote-1",
			Title:  "ssh session",
			Path:   "/work/ssh",
			Group:  "ops",
			Tool:   "claude",
			Status: string(session.StatusRunning),
		}},
	}
	h.hubSessions = map[string]hub.NodeSessions{
		"node-local": {
			Node: hub.Node{ID: "node-local", Name: "work-laptop"},
			Sessions: []hub.SessionInfo{{
				ID: "local-1", Title: "local session duplicate", Tool: "codex", Status: "waiting",
			}},
		},
		"node-aws": {
			Node: hub.Node{ID: "node-aws", Name: "aws-workstation"},
			Sessions: []hub.SessionInfo{{
				ID: "hub-1", Title: "hub session", ProjectPath: "/work/hub", GroupPath: "ops", Tool: "codex", Status: "stopped",
			}},
		},
	}

	results := h.fleetSearchResults()
	if got := len(results); got != 3 {
		t.Fatalf("fleet results = %d, want 3 (local + SSH remote + non-local hub): %+v", got, results)
	}

	byID := make(map[string]*SessionSearchResult, len(results))
	for _, result := range results {
		byID[result.SessionID] = result
	}
	if got := byID["local-1"]; got == nil || got.Source != SearchSourceLocal || got.Host != "work-laptop" {
		t.Fatalf("local result = %+v", got)
	}
	if got := byID["remote-1"]; got == nil || got.Source != SearchSourceRemote || got.RemoteName != "legacy-ssh" || got.Host != "legacy-ssh" {
		t.Fatalf("SSH remote result = %+v", got)
	}
	if got := byID["hub-1"]; got == nil || got.Source != SearchSourceHub || got.HubNodeID != "node-aws" || got.Host != "aws-workstation" || got.Status != session.StatusStopped {
		t.Fatalf("hub result = %+v", got)
	}
}

func TestSlashOpensGlobalFleetSearchAndTabShowsOnlyLocal(t *testing.T) {
	local := &session.Instance{ID: "local-1", Title: "local session", Tool: "codex"}
	h := newHubProjectionHome(t, []*session.Instance{local})
	h.remoteSessions = map[string][]session.RemoteSessionInfo{
		"aws": {{ID: "remote-1", Title: "remote session", Tool: "codex"}},
	}

	model, _ := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	h = model.(*Home)
	if !h.search.IsVisible() {
		t.Fatal("/ did not open session search")
	}
	if got := h.search.Scope(); got != SearchScopeGlobal {
		t.Fatalf("/ scope = %q, want %q", got, SearchScopeGlobal)
	}
	if got := len(h.search.results); got != 2 {
		t.Fatalf("global results = %d, want 2", got)
	}

	model, _ = h.Update(tea.KeyMsg{Type: tea.KeyTab})
	h = model.(*Home)
	if got := h.search.Scope(); got != SearchScopeLocal {
		t.Fatalf("scope after Tab = %q, want %q", got, SearchScopeLocal)
	}
	if got := len(h.search.results); got != 1 {
		t.Fatalf("local results after Tab = %d, want 1", got)
	}
	if got := h.search.results[0].SessionID; got != local.ID {
		t.Fatalf("local result ID = %q, want %q", got, local.ID)
	}
}

func TestSearchEnterActivatesSelectedHubSession(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubConfigured = true
	h.hubClient = &fakeHubAttachClient{}
	h.hubSessions = map[string]hub.NodeSessions{
		"node-aws": {
			Node: hub.Node{ID: "node-aws", Name: "aws-workstation"},
			Sessions: []hub.SessionInfo{{
				ID: "hub-1", Title: "hub session", Tool: "codex", Status: "waiting",
			}},
		},
	}
	h.openFleetSearch()
	h.search.input.SetValue("hub session")
	h.search.updateResults()

	model, cmd := h.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("Enter on hub search result returned no activation command")
	}
	if !h.isAttaching.Load() {
		t.Fatal("Enter on hub search result did not enter attaching state")
	}
	if h.search.IsVisible() {
		t.Fatal("search remained visible after activation")
	}
}

func TestSearchEnterActivatesSelectedSSHRemoteSession(t *testing.T) {
	withTempAgentDeckHome(t, `
[remotes.aws]
host = "ubuntu@aws.example"
`)
	h := NewHome()
	t.Cleanup(h.cancel)
	h.initialLoading = false
	h.search.SetFleetItems([]*SessionSearchResult{{
		Source: SearchSourceRemote, SessionID: "remote-1", Title: "remote session", RemoteName: "aws", Host: "aws",
	}})
	h.search.ShowGlobal()

	model, cmd := h.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("Enter on SSH-remote search result returned no activation command")
	}
	if !h.isAttaching.Load() {
		t.Fatal("Enter on SSH-remote search result did not enter attaching state")
	}
	if h.search.IsVisible() {
		t.Fatal("search remained visible after SSH-remote activation")
	}
}

func TestActivateSearchResultResolvesCurrentLocalInstance(t *testing.T) {
	stale := &session.Instance{ID: "local-1", Title: "stale", Tool: "codex", Status: session.StatusStopped}
	current := &session.Instance{ID: "local-1", Title: "current", Tool: "codex", Status: session.StatusStopped}
	h := newHubProjectionHome(t, []*session.Instance{current})
	var checked *session.Instance
	h.resumeSessionExists = func(inst *session.Instance) bool {
		checked = inst
		return false
	}

	cmd := h.activateSearchResult(localSessionSearchResult(stale, "work-laptop"))
	if cmd == nil {
		t.Fatal("stopped current session returned no resume command")
	}
	if checked != current {
		t.Fatalf("activation used stale search pointer %p (%q), want current instance %p (%q)", checked, checked.Title, current, current.Title)
	}
}

func TestHubSearchActivationUsesCurrentSnapshotStatus(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.hubSessions = map[string]hub.NodeSessions{
		"node-aws": {
			Node: hub.Node{ID: "node-aws", Name: "aws-workstation"},
			Sessions: []hub.SessionInfo{{
				ID: "hub-1", Title: "hub session", Tool: "codex", Status: "stopped",
			}},
		},
	}
	staleResult := &SessionSearchResult{
		Source: SearchSourceHub, SessionID: "hub-1", HubNodeID: "node-aws", Status: session.StatusRunning,
	}

	if !h.hubSearchResultNeedsRestart(staleResult) {
		t.Fatal("activation trusted stale running status instead of the current stopped hub snapshot")
	}
}

func TestSearchEnterWithNoResultsKeepsOverlayOpen(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	h.openFleetSearch()
	h.search.input.SetValue("missing")
	h.search.updateResults()

	model, cmd := h.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	h = model.(*Home)
	if cmd != nil {
		t.Fatal("Enter with no result returned an activation command")
	}
	if !h.search.IsVisible() {
		t.Fatal("Enter with no result closed search")
	}
}
