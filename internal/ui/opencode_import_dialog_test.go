package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestOpenCodeImportDialogSelectsEntry(t *testing.T) {
	d := NewOpenCodeImportDialog()
	entries := []session.OpenCodeImportEntry{{
		ID:        "ses_alpha123",
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

func TestOpenCodeImportDialogMovesCursor(t *testing.T) {
	d := NewOpenCodeImportDialog()
	entries := []session.OpenCodeImportEntry{
		{ID: "ses_alpha123", Title: "alpha", UpdatedAt: time.Now()},
		{ID: "ses_beta123", Title: "beta", UpdatedAt: time.Now()},
	}
	d.Show(entries)

	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got, ok := d.Selected()
	if !ok || got.ID != entries[1].ID {
		t.Fatalf("selected = %#v ok=%v, want second entry", got, ok)
	}
}

func TestImportSourceDialogTracksOpenCodeCount(t *testing.T) {
	d := NewImportSourceDialog()
	d.Show(ImportSourceCounts{OpenCode: 3})
	if got := d.OpenCodeCount(); got != 3 {
		t.Fatalf("OpenCodeCount = %d, want 3", got)
	}
}
