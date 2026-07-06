package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestSavedSessionImportDialogsDefaultToStartAfterImport(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		d := NewCodexImportDialog()
		d.Show([]session.CodexIndexEntry{{ID: "44444444-4444-4444-4444-444444444444", ThreadName: "alpha"}})
		if !d.StartAfterImport() {
			t.Fatal("Codex import should default to start after import")
		}
		if rendered := d.View(); !containsAll(rendered, "[x]", "Start after import") {
			t.Fatalf("Codex import dialog missing checked start toggle:\n%s", rendered)
		}
		d.Update(tea.KeyMsg{Type: tea.KeySpace})
		if d.StartAfterImport() {
			t.Fatal("space should disable Codex start after import")
		}
	})

	t.Run("claude", func(t *testing.T) {
		d := NewClaudeImportDialog()
		d.Show([]session.ClaudeImportCandidate{{SessionID: "44444444-4444-4444-4444-444444444444", Name: "alpha"}})
		if !d.StartAfterImport() {
			t.Fatal("Claude import should default to start after import")
		}
		if rendered := d.View(); !containsAll(rendered, "[x]", "Start after import") {
			t.Fatalf("Claude import dialog missing checked start toggle:\n%s", rendered)
		}
		d.Update(tea.KeyMsg{Type: tea.KeySpace})
		if d.StartAfterImport() {
			t.Fatal("space should disable Claude start after import")
		}
	})

	t.Run("opencode", func(t *testing.T) {
		d := NewOpenCodeImportDialog()
		d.Show([]session.OpenCodeImportEntry{{ID: "ses_alpha123", Title: "alpha"}})
		if !d.StartAfterImport() {
			t.Fatal("OpenCode import should default to start after import")
		}
		if rendered := d.View(); !containsAll(rendered, "[x]", "Start after import") {
			t.Fatalf("OpenCode import dialog missing checked start toggle:\n%s", rendered)
		}
		d.Update(tea.KeyMsg{Type: tea.KeySpace})
		if d.StartAfterImport() {
			t.Fatal("space should disable OpenCode start after import")
		}
	})

	t.Run("kiro", func(t *testing.T) {
		d := NewKiroImportDialog()
		d.Show([]session.KiroSavedSession{{ID: "44444444-4444-4444-4444-444444444444", Title: "alpha"}})
		if !d.StartAfterImport() {
			t.Fatal("Kiro import should default to start after import")
		}
		if rendered := d.View(); !containsAll(rendered, "[x]", "Start after import") {
			t.Fatalf("Kiro import dialog missing checked start toggle:\n%s", rendered)
		}
		d.Update(tea.KeyMsg{Type: tea.KeySpace})
		if d.StartAfterImport() {
			t.Fatal("space should disable Kiro start after import")
		}
	})
}

func TestClaudeImportStartsImportedSessionWhenEnabled(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	started := false
	oldStart := startImportedSession
	startImportedSession = func(inst *session.Instance) error {
		started = true
		inst.Status = session.StatusRunning
		return nil
	}
	t.Cleanup(func() { startImportedSession = oldStart })

	cmd := h.createSessionFromClaudeImport(session.ClaudeImportCandidate{
		SessionID: "44444444-4444-4444-4444-444444444444",
		Name:      "Imported Claude",
		CWD:       t.TempDir(),
		UpdatedAt: time.Now(),
	}, true)
	msg := cmd().(sessionCreatedMsg)
	if msg.err != nil {
		t.Fatalf("createSessionFromClaudeImport returned error: %v", msg.err)
	}
	if !started {
		t.Fatal("startImportedSession was not called")
	}
	if msg.instance == nil || msg.instance.Status != session.StatusRunning {
		t.Fatalf("imported instance = %#v, want running", msg.instance)
	}
}

func TestClaudeImportCanRemainStoppedWhenStartDisabled(t *testing.T) {
	h := newHubProjectionHome(t, nil)
	started := false
	oldStart := startImportedSession
	startImportedSession = func(inst *session.Instance) error {
		started = true
		return nil
	}
	t.Cleanup(func() { startImportedSession = oldStart })

	cmd := h.createSessionFromClaudeImport(session.ClaudeImportCandidate{
		SessionID: "44444444-4444-4444-4444-444444444444",
		Name:      "Imported Claude",
		CWD:       t.TempDir(),
		UpdatedAt: time.Now(),
	}, false)
	msg := cmd().(sessionCreatedMsg)
	if msg.err != nil {
		t.Fatalf("createSessionFromClaudeImport returned error: %v", msg.err)
	}
	if started {
		t.Fatal("startImportedSession should not be called when start is disabled")
	}
	if msg.instance == nil || msg.instance.Status != session.StatusStopped {
		t.Fatalf("imported instance = %#v, want stopped", msg.instance)
	}
}

func containsAll(s string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(s, needle) {
			return false
		}
	}
	return true
}
