package statedb

import (
	"encoding/json"
	"testing"
	"time"
)

// TestWholeRowWritesPreserveAcknowledged guards the cross-process attach
// contract: once one process records that a waiting session was viewed, an
// unrelated metadata save must not erase that acknowledgement and make remote
// hub/SSH snapshots turn yellow again.
func TestWholeRowWritesPreserveAcknowledged(t *testing.T) {
	writers := []struct {
		name  string
		write func(*StateDB, *InstanceRow) error
	}{
		{name: "SaveInstance", write: func(db *StateDB, row *InstanceRow) error {
			return db.SaveInstance(row)
		}},
		{name: "UpsertInstances", write: func(db *StateDB, row *InstanceRow) error {
			return db.UpsertInstances([]*InstanceRow{row})
		}},
		{name: "SaveInstances", write: func(db *StateDB, row *InstanceRow) error {
			return db.SaveInstances([]*InstanceRow{row})
		}},
	}

	for _, tt := range writers {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			projectPath := t.TempDir()
			row := &InstanceRow{
				ID: "viewed", Title: "before", ProjectPath: projectPath, GroupPath: "grp",
				Tool: "codex", Status: "idle", CreatedAt: time.Now(), ToolData: json.RawMessage("{}"),
			}
			if err := db.SaveInstance(row); err != nil {
				t.Fatalf("seed SaveInstance: %v", err)
			}
			if err := db.SetAcknowledged(row.ID, true); err != nil {
				t.Fatalf("SetAcknowledged: %v", err)
			}

			row.Title = "metadata changed"
			if err := tt.write(db, row); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}

			statuses, err := db.ReadAllStatuses()
			if err != nil {
				t.Fatalf("ReadAllStatuses: %v", err)
			}
			if !statuses[row.ID].Acknowledged {
				t.Fatalf("%s erased acknowledged=true during an unrelated whole-row save", tt.name)
			}
		})
	}
}
