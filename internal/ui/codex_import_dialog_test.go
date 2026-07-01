package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestCodexImportDialogSelectsEntry(t *testing.T) {
	d := NewCodexImportDialog()
	entries := []session.CodexIndexEntry{{
		ID:         "44444444-4444-4444-4444-444444444444",
		ThreadName: "alpha",
		UpdatedAt:  time.Now(),
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

func TestCodexImportDialogMovesCursor(t *testing.T) {
	d := NewCodexImportDialog()
	entries := []session.CodexIndexEntry{
		{ID: "44444444-4444-4444-4444-444444444444", ThreadName: "alpha", UpdatedAt: time.Now()},
		{ID: "55555555-5555-5555-5555-555555555555", ThreadName: "beta", UpdatedAt: time.Now()},
	}
	d.Show(entries)

	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got, ok := d.Selected()
	if !ok || got.ID != entries[1].ID {
		t.Fatalf("selected = %#v ok=%v, want second entry", got, ok)
	}
}

func TestCodexImportDialogShowsPath(t *testing.T) {
	d := NewCodexImportDialog()
	d.SetSize(120, 40)
	d.Show([]session.CodexIndexEntry{{
		ID:         "44444444-4444-4444-4444-444444444444",
		ThreadName: "alpha",
		Path:       "/home/user/project-alpha",
		UpdatedAt:  time.Now(),
	}})

	rendered := d.View()
	if !strings.Contains(rendered, "/home/user/project-alpha") {
		t.Fatalf("rendered dialog should include project path:\n%s", rendered)
	}
}
