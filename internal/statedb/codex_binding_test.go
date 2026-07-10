package statedb

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func openCodexBindingTestDB(t *testing.T) *StateDB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(); err != nil {
		_ = db.Close()
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func codexBindingRow(id, sessionID string) *InstanceRow {
	toolData := json.RawMessage(`{}`)
	if sessionID != "" {
		toolData = json.RawMessage(`{"codex_session_id":"` + sessionID + `","codex_detected_at":1}`)
	}
	return &InstanceRow{
		ID: id, Title: "test", ProjectPath: "/tmp/test", GroupPath: "default",
		Command: "codex", Tool: "codex", Status: "idle", CreatedAt: time.Now(), ToolData: toolData,
	}
}

func readCodexBindingToolData(t *testing.T, db *StateDB, id string) json.RawMessage {
	t.Helper()
	var raw string
	if err := db.db.QueryRow("SELECT tool_data FROM instances WHERE id = ?", id).Scan(&raw); err != nil {
		t.Fatalf("read tool_data: %v", err)
	}
	return json.RawMessage(raw)
}

func TestCodexBindingRevisionToolDataHelpers(t *testing.T) {
	detectedAt := time.Unix(1_725_000_123, 0)
	raw := json.RawMessage(`{
		"codex_session_id":"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
		"codex_detected_at":1725000123,
		"unrelated":"preserved"
	}`)
	raw = WriteCodexBindingRevisionToToolData(raw, 7)

	if got := ReadCodexBindingRevisionFromToolData(raw); got != 7 {
		t.Fatalf("revision = %d, want 7", got)
	}
	id, gotDetectedAt, revision := ReadCodexSessionBindingFromToolData(raw)
	if id != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("session ID = %q, want normalized lowercase", id)
	}
	if !gotDetectedAt.Equal(detectedAt) {
		t.Fatalf("detected at = %v, want %v", gotDetectedAt, detectedAt)
	}
	if revision != 7 {
		t.Fatalf("binding tuple revision = %d, want 7", revision)
	}

	raw = WriteCodexBindingRevisionToToolData(raw, 0)
	if got := ReadCodexBindingRevisionFromToolData(raw); got != 0 {
		t.Fatalf("removed revision = %d, want 0", got)
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatalf("unmarshal helper result: %v", err)
	}
	if _, ok := values[codexBindingRevisionKey]; ok {
		t.Fatal("non-positive revision should remove key")
	}
	if values["unrelated"] != "preserved" {
		t.Fatalf("unrelated field = %v, want preserved", values["unrelated"])
	}
}

func TestTargetedCodexBindingWritesAdvanceRevisionAndRotatePromotion(t *testing.T) {
	const (
		id       = "targeted-revision"
		sourceID = "11111111-1111-4111-8111-111111111111"
		oldRoot  = "22222222-2222-4222-8222-222222222222"
		targetID = "33333333-3333-4333-8333-333333333333"
		newID    = "44444444-4444-4444-8444-444444444444"
	)
	db := openCodexBindingTestDB(t)
	if err := db.SaveInstance(codexBindingRow(id, sourceID)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	promotionRevision, matched, err := db.WriteCodexSessionPromotion(
		id, targetID, time.Unix(100, 0), sourceID, oldRoot, time.Unix(90, 0), time.Unix(100, 0),
		0,
	)
	if err != nil || !matched || promotionRevision != 1 {
		t.Fatalf("promotion: revision=%d matched=%v err=%v", promotionRevision, matched, err)
	}
	bindingRevision, matched, err := db.WriteCodexSessionBinding(id, newID, time.Unix(200, 0), promotionRevision)
	if err != nil || !matched || bindingRevision != 2 {
		t.Fatalf("binding: revision=%d matched=%v err=%v", bindingRevision, matched, err)
	}

	gotID, gotDetectedAt, gotRevision, err := db.ReadCodexSessionBinding(id)
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if gotID != newID || !gotDetectedAt.Equal(time.Unix(200, 0)) || gotRevision != 2 {
		t.Fatalf("binding = (%q, %v, %d), want (%q, %v, 2)", gotID, gotDetectedAt, gotRevision, newID, time.Unix(200, 0))
	}
	var values map[string]any
	if err := json.Unmarshal(readCodexBindingToolData(t, db, id), &values); err != nil {
		t.Fatalf("unmarshal tool_data: %v", err)
	}
	for _, key := range codexPromotionKeys {
		if _, ok := values[key]; ok {
			t.Errorf("ordinary targeted binding retained promotion key %q", key)
		}
	}
}

func TestCodexExplicitClearRevisionSurvivesStaleFullRowWriters(t *testing.T) {
	const (
		id       = "durable-clear"
		sourceID = "55555555-5555-4555-8555-555555555555"
		oldRoot  = "66666666-6666-4666-8666-666666666666"
		targetID = "77777777-7777-4777-8777-777777777777"
	)

	for _, saveOne := range []bool{false, true} {
		name := "SaveInstances"
		if saveOne {
			name = "SaveInstance"
		}
		t.Run(name, func(t *testing.T) {
			db := openCodexBindingTestDB(t)
			if err := db.SaveInstance(codexBindingRow(id, sourceID)); err != nil {
				t.Fatalf("seed: %v", err)
			}
			promotionRevision, matched, err := db.WriteCodexSessionPromotion(
				id, targetID, time.Unix(100, 0), sourceID, oldRoot, time.Unix(90, 0), time.Unix(100, 0),
				0,
			)
			if err != nil || !matched {
				t.Fatalf("promote: revision=%d matched=%v err=%v", promotionRevision, matched, err)
			}
			stale := codexBindingRow(id, targetID)
			stale.ToolData = append(json.RawMessage(nil), readCodexBindingToolData(t, db, id)...)

			clear := codexBindingRow(id, "")
			clear.CodexBindingOverrideIntent = true
			if saveOne {
				err = db.SaveInstance(clear)
			} else {
				err = db.SaveInstances([]*InstanceRow{clear})
			}
			if err != nil {
				t.Fatalf("explicit clear: %v", err)
			}
			if got := ReadCodexBindingRevisionFromToolData(clear.ToolData); got != promotionRevision+1 {
				t.Fatalf("returned clear revision = %d, want %d", got, promotionRevision+1)
			}

			// A process serialized before the clear still carries the target and
			// completed-promotion keys at the previous generation.
			if saveOne {
				err = db.SaveInstance(stale)
			} else {
				err = db.SaveInstances([]*InstanceRow{stale})
			}
			if err != nil {
				t.Fatalf("stale save: %v", err)
			}
			gotID, _, gotRevision, err := db.ReadCodexSessionBinding(id)
			if err != nil {
				t.Fatalf("read after stale save: %v", err)
			}
			if gotID != "" || gotRevision != promotionRevision+1 {
				t.Fatalf("stale save restored (%q, rev %d), want clear at rev %d", gotID, gotRevision, promotionRevision+1)
			}
			var values map[string]any
			if err := json.Unmarshal(readCodexBindingToolData(t, db, id), &values); err != nil {
				t.Fatalf("unmarshal final tool_data: %v", err)
			}
			for _, key := range codexPromotionKeys {
				if _, ok := values[key]; ok {
					t.Errorf("stale save resurrected promotion key %q", key)
				}
			}
		})
	}
}

func TestCodexBindingReconcileUsesRevisionOrdering(t *testing.T) {
	const (
		id          = "revision-order"
		authorityID = "88888888-8888-4888-8888-888888888888"
		conflictID  = "99999999-9999-4999-8999-999999999999"
		newerID     = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	)
	db := openCodexBindingTestDB(t)
	seed := codexBindingRow(id, authorityID)
	seed.ToolData = WriteCodexBindingRevisionToToolData(seed.ToolData, 4)
	if err := db.SaveInstance(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	equalConflict := codexBindingRow(id, conflictID)
	equalConflict.ToolData = WriteCodexBindingRevisionToToolData(equalConflict.ToolData, 4)
	if err := db.SaveInstances([]*InstanceRow{equalConflict}); err != nil {
		t.Fatalf("equal-generation conflict save: %v", err)
	}
	gotID, _, gotRevision, err := db.ReadCodexSessionBinding(id)
	if err != nil {
		t.Fatalf("read equal conflict: %v", err)
	}
	if gotID != authorityID || gotRevision != 4 {
		t.Fatalf("equal conflict committed (%q, rev %d), want authoritative (%q, rev 4)", gotID, gotRevision, authorityID)
	}

	newer := codexBindingRow(id, newerID)
	newer.ToolData = WriteCodexBindingRevisionToToolData(newer.ToolData, 5)
	if err := db.SaveInstances([]*InstanceRow{newer}); err != nil {
		t.Fatalf("newer-generation save: %v", err)
	}
	gotID, _, gotRevision, err = db.ReadCodexSessionBinding(id)
	if err != nil {
		t.Fatalf("read newer binding: %v", err)
	}
	if gotID != newerID || gotRevision != 5 {
		t.Fatalf("newer save committed (%q, rev %d), want (%q, rev 5)", gotID, gotRevision, newerID)
	}
}

func TestSaveInstancesPreservesCompletedCodexPromotionAtWriteBoundary(t *testing.T) {
	const (
		id       = "instance"
		sourceID = "11111111-1111-4111-8111-111111111111"
		oldRoot  = "22222222-2222-4222-8222-222222222222"
		targetID = "33333333-3333-4333-8333-333333333333"
	)

	for _, staleID := range []string{sourceID, oldRoot, ""} {
		t.Run("stale_"+staleID, func(t *testing.T) {
			db := openCodexBindingTestDB(t)
			if err := db.SaveInstance(codexBindingRow(id, sourceID)); err != nil {
				t.Fatalf("seed: %v", err)
			}
			revision, matched, err := db.WriteCodexSessionPromotion(
				id, targetID, time.Now(), sourceID, oldRoot,
				time.Now().Add(-time.Second), time.Now(),
				0,
			)
			if err != nil || !matched {
				t.Fatalf("promote: revision=%d matched=%v err=%v", revision, matched, err)
			}

			// This row represents a long-running process that serialized its stale
			// snapshot before the targeted promotion committed.
			stale := codexBindingRow(id, staleID)
			stale.Title = "unrelated stale-process edit"
			if err := db.SaveInstances([]*InstanceRow{stale}); err != nil {
				t.Fatalf("SaveInstances: %v", err)
			}
			got, _, _, err := db.ReadCodexSessionBinding(id)
			if err != nil {
				t.Fatalf("ReadCodexSessionBinding: %v", err)
			}
			if got != targetID {
				t.Fatalf("stale %q replaced promoted target: got %q want %q", staleID, got, targetID)
			}
		})
	}
}

func TestSaveInstancesAllowsExplicitCodexBindingClearAfterPromotion(t *testing.T) {
	const (
		id       = "instance-clear"
		sourceID = "44444444-4444-4444-8444-444444444444"
		oldRoot  = "55555555-5555-4555-8555-555555555555"
		targetID = "66666666-6666-4666-8666-666666666666"
	)
	db := openCodexBindingTestDB(t)
	if err := db.SaveInstance(codexBindingRow(id, sourceID)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, matched, err := db.WriteCodexSessionPromotion(id, targetID, time.Now(), sourceID, oldRoot, time.Now(), time.Now(), 0)
	if err != nil || !matched {
		t.Fatalf("promote: matched=%v err=%v", matched, err)
	}

	clearRow := codexBindingRow(id, "")
	clearRow.CodexBindingOverrideIntent = true
	if err := db.SaveInstances([]*InstanceRow{clearRow}); err != nil {
		t.Fatalf("SaveInstances clear: %v", err)
	}
	got, _, _, err := db.ReadCodexSessionBinding(id)
	if err != nil {
		t.Fatalf("ReadCodexSessionBinding: %v", err)
	}
	if got != "" {
		t.Fatalf("explicit clear preserved promoted target %q", got)
	}
}

func TestWriteCodexBindingReportsMissingRow(t *testing.T) {
	db := openCodexBindingTestDB(t)
	revision, matched, err := db.WriteCodexSessionBinding("missing", "77777777-7777-4777-8777-777777777777", time.Now(), 0)
	if err != nil {
		t.Fatalf("WriteCodexSessionBinding: %v", err)
	}
	if matched {
		t.Fatal("missing row reported a successful binding write")
	}
	if revision != 0 {
		t.Fatalf("missing row revision = %d, want 0", revision)
	}
}

func TestCodexBindingCASRejectsStaleRevision(t *testing.T) {
	const (
		id      = "cas-conflict"
		oldID   = "11111111-aaaa-4aaa-8aaa-111111111111"
		staleID = "22222222-bbbb-4bbb-8bbb-222222222222"
		oldRoot = "33333333-cccc-4ccc-8ccc-333333333333"
	)
	db := openCodexBindingTestDB(t)
	if err := db.SaveInstance(codexBindingRow(id, oldID)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	clearRevision, matched, err := db.WriteCodexSessionBindingOverride(id, "", time.Unix(200, 0))
	if err != nil || !matched || clearRevision != 1 {
		t.Fatalf("explicit clear: revision=%d matched=%v err=%v", clearRevision, matched, err)
	}

	if revision, matched, err := db.WriteCodexSessionBinding(id, staleID, time.Unix(300, 0), 0); err != nil || matched || revision != 0 {
		t.Fatalf("stale binding CAS: revision=%d matched=%v err=%v", revision, matched, err)
	}
	if revision, matched, err := db.WriteCodexSessionPromotion(
		id, staleID, time.Unix(300, 0), oldID, oldRoot, time.Unix(250, 0), time.Unix(300, 0), 0,
	); err != nil || matched || revision != 0 {
		t.Fatalf("stale promotion CAS: revision=%d matched=%v err=%v", revision, matched, err)
	}

	gotID, _, gotRevision, err := db.ReadCodexSessionBinding(id)
	if err != nil {
		t.Fatalf("read after stale CAS: %v", err)
	}
	if gotID != "" || gotRevision != clearRevision {
		t.Fatalf("stale CAS changed binding: id=%q revision=%d, want empty revision=%d", gotID, gotRevision, clearRevision)
	}
}

func TestExplicitCodexOverrideOnNewRowStartsAtRevisionOne(t *testing.T) {
	for _, saveOne := range []bool{false, true} {
		t.Run(map[bool]string{false: "SaveInstances", true: "SaveInstance"}[saveOne], func(t *testing.T) {
			db := openCodexBindingTestDB(t)
			row := codexBindingRow("new-explicit", "")
			row.CodexBindingOverrideIntent = true
			var err error
			if saveOne {
				err = db.SaveInstance(row)
			} else {
				err = db.SaveInstances([]*InstanceRow{row})
			}
			if err != nil {
				t.Fatalf("save explicit new row: %v", err)
			}
			if got := ReadCodexBindingRevisionFromToolData(row.ToolData); got != 1 {
				t.Fatalf("new explicit row revision = %d, want 1", got)
			}
			if row.CodexBindingOverrideIntent {
				t.Fatal("committed row retained transient override intent")
			}
		})
	}
}
