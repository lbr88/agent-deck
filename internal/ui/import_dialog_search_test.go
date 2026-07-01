package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestSavedSessionImportDialogsAdvertiseSlashSearch(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		d := NewCodexImportDialog()
		d.SetSize(120, 40)
		d.Show([]session.CodexIndexEntry{{ID: "44444444-4444-4444-4444-444444444444", ThreadName: "alpha", UpdatedAt: time.Now()}})
		if rendered := d.View(); !strings.Contains(rendered, "/ search") {
			t.Fatalf("import picker should advertise slash search:\n%s", rendered)
		}
	})

	t.Run("claude", func(t *testing.T) {
		d := NewClaudeImportDialog()
		d.SetSize(120, 40)
		d.Show([]session.ClaudeImportCandidate{{SessionID: "44444444-4444-4444-4444-444444444444", Name: "alpha", UpdatedAt: time.Now()}})
		if rendered := d.View(); !strings.Contains(rendered, "/ search") {
			t.Fatalf("import picker should advertise slash search:\n%s", rendered)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		d := NewOpenCodeImportDialog()
		d.SetSize(120, 40)
		d.Show([]session.OpenCodeImportEntry{{ID: "ses_alpha123", Title: "alpha", UpdatedAt: time.Now()}})
		if rendered := d.View(); !strings.Contains(rendered, "/ search") {
			t.Fatalf("import picker should advertise slash search:\n%s", rendered)
		}
	})
}

func TestSavedSessionImportDialogsSlashSearchFiltersAndSelects(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		d := NewCodexImportDialog()
		d.SetSize(120, 40)
		entries := []session.CodexIndexEntry{
			{ID: "44444444-4444-4444-4444-444444444444", ThreadName: "alpha", Path: "/tmp/alpha", UpdatedAt: time.Now()},
			{ID: "55555555-5555-5555-5555-555555555555", ThreadName: "fresh session", Path: "/tmp/fresh", UpdatedAt: time.Now()},
		}
		d.Show(entries)
		typeSearch(d, "frsh")
		rendered := d.View()
		if strings.Contains(rendered, "alpha") {
			t.Fatalf("search should hide non-matching rows:\n%s", rendered)
		}
		if !strings.Contains(rendered, "fresh session") || !strings.Contains(rendered, "Search: frsh") {
			t.Fatalf("search should show matching row and query:\n%s", rendered)
		}
		_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("enter should submit the filtered selection")
		}
		got, ok := d.Selected()
		if !ok || got.ID != entries[1].ID {
			t.Fatalf("selected = %#v ok=%v, want filtered entry", got, ok)
		}
	})

	t.Run("claude", func(t *testing.T) {
		d := NewClaudeImportDialog()
		d.SetSize(120, 40)
		entries := []session.ClaudeImportCandidate{
			{SessionID: "44444444-4444-4444-4444-444444444444", Name: "alpha", CWD: "/tmp/alpha", UpdatedAt: time.Now()},
			{SessionID: "55555555-5555-5555-5555-555555555555", Name: "fresh session", CWD: "/tmp/fresh", UpdatedAt: time.Now()},
		}
		d.Show(entries)
		typeSearch(d, "frsh")
		rendered := d.View()
		if strings.Contains(rendered, "alpha") {
			t.Fatalf("search should hide non-matching rows:\n%s", rendered)
		}
		if !strings.Contains(rendered, "fresh session") || !strings.Contains(rendered, "Search: frsh") {
			t.Fatalf("search should show matching row and query:\n%s", rendered)
		}
		_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("enter should submit the filtered selection")
		}
		got, ok := d.Selected()
		if !ok || got.SessionID != entries[1].SessionID {
			t.Fatalf("selected = %#v ok=%v, want filtered entry", got, ok)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		d := NewOpenCodeImportDialog()
		d.SetSize(120, 40)
		entries := []session.OpenCodeImportEntry{
			{ID: "ses_alpha123", Title: "alpha", Directory: "/tmp/alpha", UpdatedAt: time.Now()},
			{ID: "ses_fresh123", Title: "fresh session", Directory: "/tmp/fresh", UpdatedAt: time.Now()},
		}
		d.Show(entries)
		typeSearch(d, "frsh")
		rendered := d.View()
		if strings.Contains(rendered, "alpha") {
			t.Fatalf("search should hide non-matching rows:\n%s", rendered)
		}
		if !strings.Contains(rendered, "fresh session") || !strings.Contains(rendered, "Search: frsh") {
			t.Fatalf("search should show matching row and query:\n%s", rendered)
		}
		_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("enter should submit the filtered selection")
		}
		got, ok := d.Selected()
		if !ok || got.ID != entries[1].ID {
			t.Fatalf("selected = %#v ok=%v, want filtered entry", got, ok)
		}
	})
}

func TestCodexImportDialogSearchFindsPathTailMatch(t *testing.T) {
	d := NewCodexImportDialog()
	d.SetSize(88, 40)
	now := time.Now()
	entries := []session.CodexIndexEntry{
		{
			ID:         "11111111-1111-1111-1111-111111111111",
			ThreadName: "granted-registry headless",
			Path:       "/home/lrasmussen/git/domutech/granted-registry",
			UpdatedAt:  now,
		},
		{
			ID:         "22222222-2222-2222-2222-222222222222",
			ThreadName: "current work",
			Path:       "/home/lrasmussen/git/private/agent-deck",
			UpdatedAt:  now.Add(-time.Minute),
		},
	}
	d.Show(entries)
	typeSearch(d, "agent")

	rendered := d.View()
	if !strings.Contains(rendered, "agent-deck") {
		t.Fatalf("path-tail search should show the matching Agent Deck cwd:\n%s", rendered)
	}

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should submit the path-matched selection")
	}
	got, ok := d.Selected()
	if !ok || got.ID != entries[1].ID {
		t.Fatalf("selected = %#v ok=%v, want path-matched Agent Deck entry", got, ok)
	}
}

func TestCodexImportDialogPathLinePreservesTailWhenNarrow(t *testing.T) {
	d := NewCodexImportDialog()
	d.SetSize(72, 40)
	d.Show([]session.CodexIndexEntry{{
		ID:         "22222222-2222-2222-2222-222222222222",
		ThreadName: "current work",
		Path:       "/home/lrasmussen/git/private/agent-deck",
		UpdatedAt:  time.Now(),
	}})

	rendered := d.View()
	if !strings.Contains(rendered, "Path:") || !strings.Contains(rendered, "agent-deck") {
		t.Fatalf("path should render on its own tail-preserving line:\n%s", rendered)
	}
}

func typeSearch[D interface {
	Update(tea.Msg) (D, tea.Cmd)
}](d D, query string) {
	_, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range query {
		_, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}
