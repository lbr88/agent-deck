package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestImportSourceDialogSelectsClaudeSource(t *testing.T) {
	d := NewImportSourceDialog()
	d.Show(2)
	if !d.IsVisible() {
		t.Fatal("dialog should be visible")
	}

	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	model, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = model
	if cmd == nil {
		t.Fatal("enter should submit selected import source")
	}
	got, ok := d.Selected()
	if !ok || got != importSourceClaude {
		t.Fatalf("selected = %v ok=%v", got, ok)
	}
}

func TestClaudeImportDialogSelectsEntry(t *testing.T) {
	d := NewClaudeImportDialog()
	entries := []session.ClaudeImportCandidate{{
		SessionID: "44444444-4444-4444-4444-444444444444",
		Name:      "alpha",
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
	if !ok || got.SessionID != entries[0].SessionID {
		t.Fatalf("selected = %#v ok=%v", got, ok)
	}
}

func TestClaudeImportDialogMovesCursor(t *testing.T) {
	d := NewClaudeImportDialog()
	entries := []session.ClaudeImportCandidate{
		{SessionID: "44444444-4444-4444-4444-444444444444", Name: "alpha", UpdatedAt: time.Now()},
		{SessionID: "55555555-5555-5555-5555-555555555555", Name: "beta", UpdatedAt: time.Now()},
	}
	d.Show(entries)

	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got, ok := d.Selected()
	if !ok || got.SessionID != entries[1].SessionID {
		t.Fatalf("selected = %#v ok=%v, want second entry", got, ok)
	}
}
