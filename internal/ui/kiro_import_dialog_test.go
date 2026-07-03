package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestKiroImportDialogSelectsEntry(t *testing.T) {
	d := NewKiroImportDialog()
	entries := []session.KiroSavedSession{{
		ID:        "44444444-4444-4444-4444-444444444444",
		Title:     "alpha",
		UpdatedAt: time.Now(),
	}}
	d.Show(entries)
	if !d.Visible() {
		t.Fatal("dialog should be visible")
	}

	model, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = model
	if cmd == nil {
		t.Fatal("enter should submit selected import")
	}
	got, ok := d.Selected()
	if !ok || got.ID != entries[0].ID {
		t.Fatalf("selected = %#v ok=%v", got, ok)
	}
}

func TestKiroImportDialogMovesCursor(t *testing.T) {
	d := NewKiroImportDialog()
	entries := []session.KiroSavedSession{
		{ID: "44444444-4444-4444-4444-444444444444", Title: "alpha", UpdatedAt: time.Now()},
		{ID: "55555555-5555-5555-5555-555555555555", Title: "beta", UpdatedAt: time.Now()},
	}
	d.Show(entries)

	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got, ok := d.Selected()
	if !ok || got.ID != entries[1].ID {
		t.Fatalf("selected = %#v ok=%v, want second entry", got, ok)
	}
}

func TestKiroImportDialogShowsPath(t *testing.T) {
	d := NewKiroImportDialog()
	d.SetSize(120, 40)
	d.Show([]session.KiroSavedSession{{
		ID:        "44444444-4444-4444-4444-444444444444",
		Title:     "alpha",
		CWD:       "/home/user/project-alpha",
		UpdatedAt: time.Now(),
	}})

	rendered := d.View()
	if !strings.Contains(rendered, "/home/user/project-alpha") {
		t.Fatalf("rendered dialog should include project path:\n%s", rendered)
	}
}

func TestImportSourceDialogTracksKiroCount(t *testing.T) {
	d := NewImportSourceDialog()
	d.Show(ImportSourceCounts{Kiro: 3})
	if got := d.KiroCount(); got != 3 {
		t.Fatalf("KiroCount = %d, want 3", got)
	}
}
