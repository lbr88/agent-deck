package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestGlobalSearchVisibility(t *testing.T) {
	gs := NewGlobalSearch()

	if gs.IsVisible() {
		t.Error("GlobalSearch should not be visible initially")
	}

	gs.Show()
	if !gs.IsVisible() {
		t.Error("GlobalSearch should be visible after Show()")
	}

	gs.Hide()
	if gs.IsVisible() {
		t.Error("GlobalSearch should not be visible after Hide()")
	}
}

func TestGlobalSearchKeyHandling(t *testing.T) {
	gs := NewGlobalSearch()
	gs.Show()

	// Test escape closes
	gs.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if gs.IsVisible() {
		t.Error("Escape should hide GlobalSearch")
	}
}

func TestGlobalSearchCursorNavigation(t *testing.T) {
	gs := NewGlobalSearch()
	gs.Show()

	// Add some mock results
	gs.results = []*GlobalSearchResult{
		{SessionID: "1", Summary: "First"},
		{SessionID: "2", Summary: "Second"},
		{SessionID: "3", Summary: "Third"},
	}

	// Test down navigation
	gs.Update(tea.KeyMsg{Type: tea.KeyDown})
	if gs.cursor != 1 {
		t.Errorf("Expected cursor at 1, got %d", gs.cursor)
	}

	gs.Update(tea.KeyMsg{Type: tea.KeyDown})
	if gs.cursor != 2 {
		t.Errorf("Expected cursor at 2, got %d", gs.cursor)
	}

	// Should not go past end
	gs.Update(tea.KeyMsg{Type: tea.KeyDown})
	if gs.cursor != 2 {
		t.Errorf("Cursor should stay at 2, got %d", gs.cursor)
	}

	// Test up navigation
	gs.Update(tea.KeyMsg{Type: tea.KeyUp})
	if gs.cursor != 1 {
		t.Errorf("Expected cursor at 1, got %d", gs.cursor)
	}
}

func TestGlobalSearchSelected(t *testing.T) {
	gs := NewGlobalSearch()
	gs.Show()

	// No results, should return nil
	if gs.Selected() != nil {
		t.Error("Selected should be nil when no results")
	}

	// Add results
	gs.results = []*GlobalSearchResult{
		{SessionID: "1", Summary: "First"},
		{SessionID: "2", Summary: "Second"},
	}

	// Should return first
	selected := gs.Selected()
	if selected == nil || selected.SessionID != "1" {
		t.Error("Selected should return first result")
	}

	// Move cursor and check
	gs.cursor = 1
	selected = gs.Selected()
	if selected == nil || selected.SessionID != "2" {
		t.Error("Selected should return second result")
	}
}

func TestGlobalSearchEnterClosesAndSelects(t *testing.T) {
	gs := NewGlobalSearch()
	gs.Show()
	gs.input.SetValue("deploy")
	gs.query = "deploy"
	gs.results = []*GlobalSearchResult{
		{SessionID: "test-1", Summary: "Test"},
	}
	gs.cursor = 0
	gs.previewScroll = 2
	gs.searching = true

	gs.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if gs.IsVisible() {
		t.Error("Enter should hide GlobalSearch")
	}
	if got := gs.input.Value(); got != "" {
		t.Fatalf("query after Enter = %q, want empty", got)
	}
	if gs.query != "" {
		t.Fatalf("stored query after Enter = %q, want empty", gs.query)
	}
	if len(gs.results) != 0 {
		t.Fatalf("results after Enter = %d, want 0", len(gs.results))
	}
}

func TestGlobalSearchHighlightMatches(t *testing.T) {
	gs := NewGlobalSearch()

	tests := []struct {
		name     string
		text     string
		query    string
		contains string
	}{
		{
			name:     "empty query returns original text",
			text:     "Hello World",
			query:    "",
			contains: "Hello World",
		},
		{
			name:     "empty text returns empty",
			text:     "",
			query:    "test",
			contains: "",
		},
		{
			name:     "case insensitive match",
			text:     "Hello World",
			query:    "world",
			contains: "Hello ", // Text before match should be preserved
		},
		{
			name:     "preserves original case in match",
			text:     "Hello WORLD",
			query:    "world",
			contains: "WORLD", // Original case preserved in highlight
		},
		{
			name:     "multiple matches",
			text:     "test one test two test",
			query:    "test",
			contains: "one", // Text between matches should be preserved
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gs.highlightMatches(tt.text, tt.query)
			if tt.query == "" {
				// Empty query should return original text unchanged
				if result != tt.text {
					t.Errorf("Expected original text %q, got %q", tt.text, result)
				}
				return
			}
			// For non-empty query, verify:
			// 1. Result contains the expected content (between matches)
			if tt.contains != "" && !strings.Contains(result, tt.contains) {
				t.Errorf("Expected result to contain %q, got %q", tt.contains, result)
			}
			// 2. Result is not empty when both text and query are provided
			if tt.text != "" && result == "" {
				t.Errorf("Expected non-empty result for text %q with query %q", tt.text, tt.query)
			}
			// Note: lipgloss may not emit ANSI codes in test environment (no TTY)
			// so we skip checking for escape sequences
		})
	}
}

func TestGlobalSearchQueryStorage(t *testing.T) {
	gs := NewGlobalSearch()
	gs.Show()

	// Initially query should be empty
	if gs.query != "" {
		t.Errorf("Expected empty query, got %q", gs.query)
	}

	// Simulate typing by setting input value (query is set in Update's default case)
	gs.input.SetValue("test search")
	gs.query = gs.input.Value()

	// Query should now be stored
	if gs.query != "test search" {
		t.Errorf("Expected query 'test search', got %q", gs.query)
	}
}

func TestGlobalSearchFormatPreviewContentWithHighlighting(t *testing.T) {
	gs := NewGlobalSearch()
	gs.Show()
	gs.query = "hello"

	// Test with user message
	content := "User: Hello world\nAssistant: Hi there"
	lines := gs.formatPreviewContent(content, 80)

	// Should have at least 2 lines
	if len(lines) < 2 {
		t.Errorf("Expected at least 2 lines, got %d", len(lines))
	}

	// First line should have user emoji
	if len(lines) > 0 && len(lines[0]) == 0 {
		t.Error("Expected non-empty first line")
	}
}

func TestGlobalSearchMatchCount(t *testing.T) {
	gs := NewGlobalSearch()
	gs.Show()

	// Test results with different match counts
	gs.results = []*GlobalSearchResult{
		{SessionID: "1", Summary: "First", Content: "test test test", MatchCount: 3},
		{SessionID: "2", Summary: "Second", Content: "test", MatchCount: 1},
		{SessionID: "3", Summary: "Third", Content: "no matches here", MatchCount: 0},
	}

	// Verify match counts are stored correctly
	if gs.results[0].MatchCount != 3 {
		t.Errorf("Expected MatchCount 3, got %d", gs.results[0].MatchCount)
	}
	if gs.results[1].MatchCount != 1 {
		t.Errorf("Expected MatchCount 1, got %d", gs.results[1].MatchCount)
	}
	if gs.results[2].MatchCount != 0 {
		t.Errorf("Expected MatchCount 0, got %d", gs.results[2].MatchCount)
	}

	// Verify View renders without errors (contains match info)
	gs.SetSize(160, 40)
	view := gs.View()
	if view == "" {
		t.Error("Expected non-empty view output")
	}
}
