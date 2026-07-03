package session

import (
	"testing"
	"time"
)

func TestStorageRoundTripPersistsKiroSessionBinding(t *testing.T) {
	t.Setenv("AGENT_DECK_HOME", "")
	t.Setenv("AGENT_DECK_PROFILE", "")
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	detectedAt := time.Unix(1783072800, 0).UTC()
	inst := &Instance{
		ID:             "kiro-persist-1",
		Title:          "kiro saved",
		ProjectPath:    t.TempDir(),
		GroupPath:      DefaultGroupPath,
		Command:        "kiro-cli chat --tui",
		Tool:           "kiro",
		Status:         StatusStopped,
		CreatedAt:      detectedAt,
		KiroSessionID:  "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d",
		KiroDetectedAt: detectedAt,
	}

	storage, err := NewStorageWithProfile("kiro-persistence")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	if err := storage.SaveWithGroups([]*Instance{inst}, nil); err != nil {
		t.Fatalf("SaveWithGroups: %v", err)
	}

	loaded, _, err := storage.LoadWithGroups()
	if err != nil {
		t.Fatalf("LoadWithGroups: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d sessions, want 1", len(loaded))
	}
	got := loaded[0]
	if got.KiroSessionID != inst.KiroSessionID {
		t.Fatalf("KiroSessionID = %q, want %q", got.KiroSessionID, inst.KiroSessionID)
	}
	if !got.KiroDetectedAt.Equal(detectedAt) {
		t.Fatalf("KiroDetectedAt = %v, want %v", got.KiroDetectedAt, detectedAt)
	}
}
