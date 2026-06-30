package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHomeRenameSessionSyncsClaudeMetadataAfterSave(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)
	seedClaudeSessionMetadata(t, homeDir, "1234.json", map[string]any{
		"sessionId": "sid-tui",
		"name":      "before",
		"updatedAt": int64(1000),
	})

	storage, err := session.NewStorageWithProfile("_test_claude_rename_sync_tui")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	home := NewHome()
	home.profile = storage.Profile()
	home.storage = storage
	home.width = 100
	home.height = 30

	inst := session.NewInstanceWithTool("original-name", "/tmp/project", "claude")
	inst.ClaudeSessionID = "sid-tui"
	home.instancesMu.Lock()
	home.instances = []*session.Instance{inst}
	home.instanceByID[inst.ID] = inst
	home.instancesMu.Unlock()
	home.groupTree = session.NewGroupTree(home.instances)
	home.rebuildFlatItems()

	for i, item := range home.flatItems {
		if item.Type == session.ItemTypeSession {
			home.cursor = i
			break
		}
	}
	home.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	home.groupDialog.nameInput.SetValue("new tui name")

	model, _ := home.Update(tea.KeyMsg{Type: tea.KeyEnter})
	h := model.(*Home)

	if h.instances[0].Title != "new tui name" {
		t.Fatalf("title = %q, want new tui name", h.instances[0].Title)
	}
	if got := session.ClaudeSessionNameIn(filepath.Join(homeDir, ".claude"), "sid-tui"); got != "new tui name" {
		t.Fatalf("Claude metadata name = %q, want new tui name", got)
	}
}

func TestWebMutatorTitleUpdateSyncsClaudeMetadataAfterSave(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)
	seedClaudeSessionMetadata(t, homeDir, "1234.json", map[string]any{
		"sessionId": "sid-web",
		"name":      "before",
		"updatedAt": int64(1000),
	})

	h, storage := newHeadlessHomeForTest(t, "_test_claude_rename_sync_web")
	inst := &session.Instance{
		ID:              "web-sync-001",
		Title:           "before",
		ProjectPath:     "/tmp/web-sync-project",
		GroupPath:       session.DefaultGroupPath,
		Tool:            "claude",
		ClaudeSessionID: "sid-web",
		Status:          session.StatusStopped,
		CreatedAt:       time.Now(),
	}
	if err := storage.SaveWithGroups([]*session.Instance{inst}, session.NewGroupTree([]*session.Instance{inst})); err != nil {
		t.Fatalf("seed SaveWithGroups: %v", err)
	}

	m := NewWebMutator(h)
	changed, _, err := m.UpdateSession("web-sync-001", map[string]string{
		session.FieldTitle: "new web name",
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if len(changed) != 1 || changed[0] != session.FieldTitle {
		t.Fatalf("changed fields = %v, want [title]", changed)
	}
	if got := session.ClaudeSessionNameIn(filepath.Join(homeDir, ".claude"), "sid-web"); got != "new web name" {
		t.Fatalf("Claude metadata name = %q, want new web name", got)
	}
}

func seedClaudeSessionMetadata(t *testing.T, home, file string, fields map[string]any) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir Claude sessions: %v", err)
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal Claude metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
		t.Fatalf("write Claude metadata: %v", err)
	}
}
