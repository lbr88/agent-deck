package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestSavedSessionImportDialogsBoundVisibleRows(t *testing.T) {
	const (
		width  = 100
		height = 14
	)

	t.Run("codex", func(t *testing.T) {
		d := NewCodexImportDialog()
		d.SetSize(width, height)
		d.Show(codexImportEntriesForScrollTest(12))
		for i := 0; i < 8; i++ {
			_, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
		}

		rendered := d.View()
		assertImportDialogBounded(t, rendered, height)
		if strings.Contains(rendered, "codex-session-00") {
			t.Fatalf("first row should scroll out of the import picker:\n%s", rendered)
		}
		if !strings.Contains(rendered, "codex-session-08") {
			t.Fatalf("selected row should stay visible after scrolling:\n%s", rendered)
		}
	})

	t.Run("claude", func(t *testing.T) {
		d := NewClaudeImportDialog()
		d.SetSize(width, height)
		d.Show(claudeImportEntriesForScrollTest(12))
		for i := 0; i < 8; i++ {
			_, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
		}

		rendered := d.View()
		assertImportDialogBounded(t, rendered, height)
		if strings.Contains(rendered, "claude-session-00") {
			t.Fatalf("first row should scroll out of the import picker:\n%s", rendered)
		}
		if !strings.Contains(rendered, "claude-session-08") {
			t.Fatalf("selected row should stay visible after scrolling:\n%s", rendered)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		d := NewOpenCodeImportDialog()
		d.SetSize(width, height)
		d.Show(openCodeImportEntriesForScrollTest(12))
		for i := 0; i < 8; i++ {
			_, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
		}

		rendered := d.View()
		assertImportDialogBounded(t, rendered, height)
		if strings.Contains(rendered, "opencode-session-00") {
			t.Fatalf("first row should scroll out of the import picker:\n%s", rendered)
		}
		if !strings.Contains(rendered, "opencode-session-08") {
			t.Fatalf("selected row should stay visible after scrolling:\n%s", rendered)
		}
	})

	t.Run("kiro", func(t *testing.T) {
		d := NewKiroImportDialog()
		d.SetSize(width, height)
		d.Show(kiroImportEntriesForScrollTest(12))
		for i := 0; i < 8; i++ {
			_, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
		}

		rendered := d.View()
		assertImportDialogBounded(t, rendered, height)
		if strings.Contains(rendered, "kiro-session-00") {
			t.Fatalf("first row should scroll out of the import picker:\n%s", rendered)
		}
		if !strings.Contains(rendered, "kiro-session-08") {
			t.Fatalf("selected row should stay visible after scrolling:\n%s", rendered)
		}
	})
}

func assertImportDialogBounded(t *testing.T, rendered string, height int) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	if len(lines) > height {
		t.Fatalf("rendered dialog height = %d, want <= %d\n%s", len(lines), height, rendered)
	}
}

func codexImportEntriesForScrollTest(count int) []session.CodexIndexEntry {
	entries := make([]session.CodexIndexEntry, 0, count)
	now := time.Now()
	for i := 0; i < count; i++ {
		entries = append(entries, session.CodexIndexEntry{
			ID:         fmt.Sprintf("44444444-4444-4444-4444-%012d", i),
			ThreadName: fmt.Sprintf("codex-session-%02d", i),
			Path:       fmt.Sprintf("/tmp/project-%02d", i),
			UpdatedAt:  now.Add(time.Duration(i) * time.Minute),
		})
	}
	return entries
}

func claudeImportEntriesForScrollTest(count int) []session.ClaudeImportCandidate {
	entries := make([]session.ClaudeImportCandidate, 0, count)
	now := time.Now()
	for i := 0; i < count; i++ {
		entries = append(entries, session.ClaudeImportCandidate{
			SessionID: fmt.Sprintf("55555555-5555-5555-5555-%012d", i),
			Name:      fmt.Sprintf("claude-session-%02d", i),
			CWD:       fmt.Sprintf("/tmp/project-%02d", i),
			UpdatedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	return entries
}

func openCodeImportEntriesForScrollTest(count int) []session.OpenCodeImportEntry {
	entries := make([]session.OpenCodeImportEntry, 0, count)
	now := time.Now()
	for i := 0; i < count; i++ {
		entries = append(entries, session.OpenCodeImportEntry{
			ID:        fmt.Sprintf("ses_%02d", i),
			Title:     fmt.Sprintf("opencode-session-%02d", i),
			Directory: fmt.Sprintf("/tmp/project-%02d", i),
			UpdatedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	return entries
}

func kiroImportEntriesForScrollTest(count int) []session.KiroSavedSession {
	entries := make([]session.KiroSavedSession, 0, count)
	now := time.Now()
	for i := 0; i < count; i++ {
		entries = append(entries, session.KiroSavedSession{
			ID:        fmt.Sprintf("66666666-6666-6666-6666-%012d", i),
			Title:     fmt.Sprintf("kiro-session-%02d", i),
			CWD:       fmt.Sprintf("/tmp/project-%02d", i),
			UpdatedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	return entries
}
