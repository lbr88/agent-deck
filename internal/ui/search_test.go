package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSearchGlobalScopeDefaultsAndTabTogglesLocal(t *testing.T) {
	s := NewSearch()
	local := &session.Instance{ID: "local-1", Title: "local session", ProjectPath: "/work/local", Tool: "codex", Status: session.StatusWaiting}
	s.SetFleetItems([]*SessionSearchResult{
		{
			Source:      SearchSourceLocal,
			SessionID:   local.ID,
			Title:       local.Title,
			ProjectPath: local.ProjectPath,
			Tool:        local.Tool,
			Status:      local.Status,
			Host:        "work-laptop",
		},
		{
			Source:      SearchSourceRemote,
			SessionID:   "remote-1",
			Title:       "remote session",
			ProjectPath: "/work/remote",
			Tool:        "claude",
			Status:      session.StatusRunning,
			Host:        "aws-workstation",
			RemoteName:  "aws",
		},
	})

	s.ShowGlobal()
	if got := s.Scope(); got != SearchScopeGlobal {
		t.Fatalf("scope after ShowGlobal = %q, want %q", got, SearchScopeGlobal)
	}
	if got := len(s.results); got != 2 {
		t.Fatalf("global results = %d, want 2", got)
	}

	_, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := s.Scope(); got != SearchScopeLocal {
		t.Fatalf("scope after Tab = %q, want %q", got, SearchScopeLocal)
	}
	if got := len(s.results); got != 1 {
		t.Fatalf("local results = %d, want 1", got)
	}
	if got := s.results[0].SessionID; got != local.ID {
		t.Fatalf("local result ID = %q, want %q", got, local.ID)
	}

	_, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := s.Scope(); got != SearchScopeGlobal {
		t.Fatalf("scope after second Tab = %q, want %q", got, SearchScopeGlobal)
	}
}

func TestSearchGlobalMatchesAndRendersOwningHost(t *testing.T) {
	s := NewSearch()
	s.SetFleetItems([]*SessionSearchResult{{
		Source:      SearchSourceHub,
		SessionID:   "hub-1",
		Title:       "duplicate title",
		ProjectPath: "/srv/app",
		GroupPath:   "ops",
		Tool:        "codex",
		Status:      session.StatusWaiting,
		Host:        "aws-workstation",
		HubNodeID:   "node-aws",
	}})
	s.SetSize(120, 40)
	s.ShowGlobal()
	s.input.SetValue("aws-workstation")
	s.updateResults()

	if got := len(s.results); got != 1 {
		t.Fatalf("host query results = %d, want 1", got)
	}
	selected := s.Selected()
	if selected == nil || selected.HubNodeID != "node-aws" {
		t.Fatalf("selected result lost hub identity: %+v", selected)
	}
	if view := s.View(); !strings.Contains(view, "aws-workstation") {
		t.Fatalf("global search view missing owning host:\n%s", view)
	}
}

func TestNewSearch(t *testing.T) {
	s := NewSearch()

	if s == nil {
		t.Fatal("NewSearch returned nil")
	}
	if s.IsVisible() {
		t.Error("Search should not be visible by default")
	}
	if s.cursor != 0 {
		t.Error("Cursor should start at 0")
	}
}

func TestSearchSetItems(t *testing.T) {
	s := NewSearch()
	items := []*session.Instance{
		{Title: "session-1", ProjectPath: "/tmp/1", Tool: "claude"},
		{Title: "session-2", ProjectPath: "/tmp/2", Tool: "shell"},
	}

	s.SetItems(items)

	if len(s.allItems) != 2 {
		t.Errorf("Expected 2 items, got %d", len(s.allItems))
	}
}

func TestSearchVisibility(t *testing.T) {
	s := NewSearch()

	s.Show()
	if !s.IsVisible() {
		t.Error("Search should be visible after Show()")
	}

	s.Hide()
	if s.IsVisible() {
		t.Error("Search should not be visible after Hide()")
	}
}

func TestSearchShowClearsPreviousQuery(t *testing.T) {
	s := NewSearch()
	items := []*session.Instance{
		{Title: "api"},
		{Title: "worker"},
	}
	s.SetItems(items)
	s.Show()
	s.input.SetValue("api")
	s.updateResults()
	if len(s.results) != 1 || s.results[0].Title != "api" {
		t.Fatalf("precondition results = %+v", s.results)
	}

	s.Hide()

	if got := s.input.Value(); got != "" {
		t.Fatalf("search query after hide = %q, want empty", got)
	}
	if len(s.results) != 2 {
		t.Fatalf("search results after hide = %d, want all items", len(s.results))
	}

	s.Show()

	if got := s.input.Value(); got != "" {
		t.Fatalf("search query after reopen = %q, want empty", got)
	}
	if len(s.results) != 2 {
		t.Fatalf("search results after reopen = %d, want all items", len(s.results))
	}
}

func TestSearchSelected(t *testing.T) {
	s := NewSearch()
	items := []*session.Instance{
		{Title: "session-1"},
		{Title: "session-2"},
	}
	s.SetItems(items)
	s.Show()

	selected := s.Selected()
	if selected == nil {
		t.Fatal("Selected should not be nil when items exist")
	}
	if selected.Title != "session-1" {
		t.Errorf("Expected session-1, got %s", selected.Title)
	}
}

func TestSearchSetSize(t *testing.T) {
	s := NewSearch()
	s.SetSize(100, 50)

	if s.width != 100 {
		t.Errorf("Width = %d, want 100", s.width)
	}
	if s.height != 50 {
		t.Errorf("Height = %d, want 50", s.height)
	}
}

func TestSearchView(t *testing.T) {
	s := NewSearch()

	// Not visible - should return empty
	view := s.View()
	if view != "" {
		t.Error("View should be empty when not visible")
	}

	// Visible - should return content
	s.SetSize(80, 24)
	s.Show()
	view = s.View()
	if view == "" {
		t.Error("View should not be empty when visible")
	}
}
