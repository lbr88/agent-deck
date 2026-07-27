package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func TestHandleEditSessionDialogKeySkipsCodexSyncWhenForceSaveFails(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	storage, err := session.NewStorageWithProfile("_test_tui_postcommit_actual")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	inst := session.NewInstanceWithTool("old-title", t.TempDir(), "codex")
	inst.ID = "tui-postcommit-save-fail"
	inst.CodexSessionID = "66666666-6666-6666-6666-666666666666"

	h := homeForEditSessionPostCommitTest(storage, "_test_tui_postcommit_expected", inst)
	setEditSessionTitle(t, h.editSessionDialog, "new-title")

	_, _ = h.handleEditSessionDialogKey(tea.KeyMsg{Type: tea.KeyEnter})

	if h.err == nil {
		t.Fatal("expected failed save error to be surfaced")
	}
	if _, err := os.Stat(filepath.Join(codexHome, "session_index.jsonl")); err == nil {
		t.Fatal("Codex index sync ran even though Agent Deck save failed")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat codex index: %v", err)
	}
}

func TestHandleEditSessionDialogKeySurfacesCodexSyncWarning(t *testing.T) {
	storage, err := session.NewStorageWithProfile("_test_tui_postcommit_warning")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	homeFile := filepath.Join(t.TempDir(), "codex-home-file")
	if err := os.WriteFile(homeFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write codex home file: %v", err)
	}
	t.Setenv("CODEX_HOME", homeFile)

	inst := session.NewInstanceWithTool("old-title", t.TempDir(), "codex")
	inst.ID = "tui-postcommit-warning"
	inst.CodexSessionID = "66666666-6666-6666-6666-666666666666"

	h := homeForEditSessionPostCommitTest(storage, storage.Profile(), inst)
	setEditSessionTitle(t, h.editSessionDialog, "new-title")

	_, _ = h.handleEditSessionDialogKey(tea.KeyMsg{Type: tea.KeyEnter})

	if h.err == nil {
		t.Fatal("expected Codex sync warning to be surfaced")
	}
	if !strings.Contains(h.err.Error(), "Codex session name") {
		t.Fatalf("warning = %v, want Codex session name context", h.err)
	}
}

func homeForEditSessionPostCommitTest(storage *session.Storage, profile string, inst *session.Instance) *Home {
	d := NewEditSessionDialog()
	d.Show(inst)
	return &Home{
		profile:             profile,
		storage:             storage,
		instances:           []*session.Instance{inst},
		instanceByID:        map[string]*session.Instance{inst.ID: inst},
		groupTree:           session.NewGroupTree([]*session.Instance{inst}),
		editSessionDialog:   d,
		pendingTitleChanges: make(map[string]pendingTitle),
		previewCache:        make(map[string]string),
		previewCacheTime:    make(map[string]time.Time),
		insertBatchDuration: defaultInsertBatchDuration,
		insertOpenKeySender: defaultInsertOpenKeySender,
		newDialog:           NewNewDialog(),
	}
}

func setEditSessionTitle(t *testing.T, d *EditSessionDialog, title string) {
	t.Helper()
	for i := range d.fields {
		if d.fields[i].key == session.FieldTitle {
			d.fields[i].input.SetValue(title)
			return
		}
	}
	t.Fatal("title field not found")
}
