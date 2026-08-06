package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func TestNewHookActivityClearsPersistedAcknowledgement(t *testing.T) {
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const id = "codex-new-activity"
	row := &statedb.InstanceRow{
		ID: id, Title: "viewed completion", ProjectPath: t.TempDir(), GroupPath: DefaultGroupPath,
		Tool: "codex", Status: string(StatusIdle), CreatedAt: time.Now(), ToolData: json.RawMessage("{}"),
	}
	if err := db.SaveInstance(row); err != nil {
		t.Fatalf("SaveInstance: %v", err)
	}
	if err := db.SetAcknowledged(id, true); err != nil {
		t.Fatalf("SetAcknowledged(true): %v", err)
	}

	inst := &Instance{
		ID: id, Tool: "codex", Status: StatusIdle, CreatedAt: time.Now(), stateDB: db,
		tmuxSession: tmux.NewSession("codex-new-activity", t.TempDir()),
	}
	inst.tmuxSession.Acknowledge()
	inst.UpdateHookStatus(&HookStatus{
		Status: "running", Event: "UserPromptSubmit", UpdatedAt: time.Now(),
	})

	statuses, err := db.ReadAllStatuses()
	if err != nil {
		t.Fatalf("ReadAllStatuses: %v", err)
	}
	if statuses[id].Acknowledged {
		t.Fatal("new Codex activity left SQLite acknowledged=true; shared sync can hide the next completion")
	}
}
