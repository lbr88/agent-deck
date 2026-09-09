package statedb

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func ompForkOutcomeRow(id string) *InstanceRow {
	return &InstanceRow{
		ID:          id,
		Title:       "user-renamed",
		ProjectPath: "/workspace/child",
		GroupPath:   "user/moved",
		Order:       7,
		Command:     "omp",
		Tool:        "omp",
		Status:      "starting",
		TmuxSession: "agentdeck_child_before",
		CreatedAt:   time.Now(),
		TitleLocked: true,
		ToolData: json.RawMessage(`{
			"omp_pending_fork_command":"native-fork-recipe",
			"notes":"keep-me",
			"sandbox_container":"old-container"
		}`),
	}
}

func TestWriteOmpForkLaunchOutcomeUpdatesRuntimeWithoutClobberingUserMetadata(t *testing.T) {
	db := newTestDB(t)
	if err := db.SaveInstances([]*InstanceRow{ompForkOutcomeRow("omp-child")}); err != nil {
		t.Fatalf("SaveInstances: %v", err)
	}
	started := time.Unix(1_800_000_123, 0).UTC()

	if _, err := db.WriteOmpForkLaunchOutcome("omp-child", "agentdeck_child_after", "running", started, "new-container"); err != nil {
		t.Fatalf("WriteOmpForkLaunchOutcome: %v", err)
	}

	row, err := db.LoadInstanceByID("omp-child")
	if err != nil {
		t.Fatalf("LoadInstanceByID: %v", err)
	}
	if row.Title != "user-renamed" || row.GroupPath != "user/moved" || row.Order != 7 || !row.TitleLocked {
		t.Fatalf("targeted outcome clobbered user metadata: %+v", row)
	}
	if row.TmuxSession != "agentdeck_child_after" || row.Status != "running" {
		t.Fatalf("runtime outcome not recorded: tmux=%q status=%q", row.TmuxSession, row.Status)
	}
	var data map[string]any
	if err := json.Unmarshal(row.ToolData, &data); err != nil {
		t.Fatalf("tool_data: %v", err)
	}
	if data["omp_pending_fork_command"] != "" {
		t.Fatalf("pending recipe was not explicitly cleared: %#v", data)
	}
	if data["last_started_at"] != float64(started.Unix()) || data["sandbox_container"] != "new-container" || data["notes"] != "keep-me" {
		t.Fatalf("targeted tool_data update = %#v", data)
	}
}

func TestWriteOmpForkLaunchOutcomeDoesNotResurrectDeletedChild(t *testing.T) {
	db := newTestDB(t)

	_, err := db.WriteOmpForkLaunchOutcome("deleted-child", "agentdeck_deleted", "running", time.Now(), "")
	if !errors.Is(err, ErrInstanceNotStored) {
		t.Fatalf("error = %v, want ErrInstanceNotStored", err)
	}
	row, loadErr := db.LoadInstanceByID("deleted-child")
	if loadErr != nil || row != nil {
		t.Fatalf("targeted finalization resurrected deleted child: row=%+v err=%v", row, loadErr)
	}
}

func TestWriteOmpForkLaunchOutcomeRejectsRowReusedByAnotherTool(t *testing.T) {
	db := newTestDB(t)
	row := ompForkOutcomeRow("reused-child")
	row.Tool = "claude"
	if err := db.SaveInstances([]*InstanceRow{row}); err != nil {
		t.Fatalf("SaveInstances: %v", err)
	}

	_, err := db.WriteOmpForkLaunchOutcome(row.ID, "agentdeck_wrong", "running", time.Now(), "")
	if !errors.Is(err, ErrInstanceNotStored) {
		t.Fatalf("error = %v, want ErrInstanceNotStored", err)
	}
}

func TestWriteOmpForkUserTitleCompareAndSetRejectsStaleCompletion(t *testing.T) {
	db := newTestDB(t)
	row := ompForkOutcomeRow("title-child")
	row.Title = "newer-title"
	if err := db.SaveInstance(row); err != nil {
		t.Fatal(err)
	}
	applied, err := db.WriteOmpForkUserTitle(row.ID, "older-title", "stale-pending", true)
	if err != nil || applied {
		t.Fatalf("stale title CAS = (%t, %v), want false, nil", applied, err)
	}
	got, err := db.LoadInstanceByID(row.ID)
	if err != nil || got == nil || got.Title != "newer-title" {
		t.Fatalf("stale title CAS overwrote row: %+v, %v", got, err)
	}
}

func TestWriteOmpForkUserTitleRejectsDeletedOrReusedRow(t *testing.T) {
	for _, seedTool := range []string{"", "claude"} {
		t.Run(seedTool, func(t *testing.T) {
			db := newTestDB(t)
			if seedTool != "" {
				row := ompForkOutcomeRow("title-child")
				row.Tool = seedTool
				if err := db.SaveInstance(row); err != nil {
					t.Fatal(err)
				}
			}
			_, err := db.WriteOmpForkUserTitle("title-child", "old", "new", true)
			if !errors.Is(err, ErrInstanceNotStored) {
				t.Fatalf("error = %v, want ErrInstanceNotStored", err)
			}
		})
	}
}
