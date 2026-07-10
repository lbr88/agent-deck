package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

func TestUpdateStatusReconcilesCodexTitleWithoutLiveTmux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	writeCodexStateThread(t, GetCodexHomeDir(), id, "renamed while detached", "preview", "/repo", time.Now().UnixMilli())
	inst := &Instance{
		ID:             "codex-title-refresh",
		Title:          "stale agent deck title",
		ProjectPath:    "/repo",
		Tool:           "codex",
		Status:         StatusStopped,
		CodexSessionID: id,
		TitleLocked:    true,
	}

	if err := inst.UpdateStatus(); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if inst.Title != "renamed while detached" {
		t.Fatalf("title = %q, want Codex /rename title", inst.Title)
	}
}

func TestReconcileTitleFromCodexPersistsThroughOwningStorageWithoutGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	previousGlobal := statedb.GetGlobal()
	statedb.SetGlobal(nil)
	t.Cleanup(func() { statedb.SetGlobal(previousGlobal) })

	const (
		profile    = "codex-title-owning-db"
		instanceID = "codex-owning-db-row"
		threadID   = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	)
	storage, err := NewStorageWithProfile(profile)
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	if err := storage.Save([]*Instance{{
		ID:             instanceID,
		Title:          "stale persisted title",
		ProjectPath:    "/repo",
		GroupPath:      DefaultGroupPath,
		Tool:           "codex",
		Command:        "codex",
		Status:         StatusStopped,
		CreatedAt:      time.Now(),
		CodexSessionID: threadID,
		TitleLocked:    true,
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	instances, err := storage.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("loaded %d instances, want 1", len(instances))
	}
	writeCodexStateThread(t, GetCodexHomeDir(), threadID, "native slash rename", "preview", "/repo", time.Now().UnixMilli())

	if _, changed, err := instances[0].ReconcileTitleFromCodex(); err != nil {
		t.Fatalf("ReconcileTitleFromCodex: %v", err)
	} else if !changed {
		t.Fatal("ReconcileTitleFromCodex reported no change")
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	verify, err := NewStorageWithProfile(profile)
	if err != nil {
		t.Fatalf("reopen storage: %v", err)
	}
	defer verify.Close()
	rows, err := verify.GetDB().LoadInstances()
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	if len(rows) != 1 || rows[0].Title != "native slash rename" {
		t.Fatalf("persisted rows = %#v, want native slash rename", rows)
	}
	var toolData map[string]any
	if err := json.Unmarshal(rows[0].ToolData, &toolData); err != nil {
		t.Fatalf("decode tool data: %v", err)
	}
	if got, _ := toolData["codex_session_id"].(string); got != threadID {
		t.Fatalf("persisted codex_session_id = %q, want %q", got, threadID)
	}
}
