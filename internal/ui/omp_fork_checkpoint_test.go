package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/git"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
	"github.com/asheshgoplani/agent-deck/internal/web"
)

func seedUIOmpForkParent(t *testing.T, storage *session.Storage) *session.Instance {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	parent := session.NewInstanceWithGroupAndTool("OMP parent", t.TempDir(), "forks", "omp")
	dir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	root := filepath.Join(dir, "parent.jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("{\"type\":\"session\",\"id\":\"ui-parent-id\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-active-session"), []byte(fmt.Sprintf("1\n%s\nui-parent-id\nsaved\nlegacy-generation\n", root)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Save([]*session.Instance{parent}); err != nil {
		t.Fatal(err)
	}
	return parent
}

func installUIOmpStartCheckpointProbe(t *testing.T, profile string) error {
	t.Helper()
	probeErr := errors.New("provider probe stop")
	oldStart := startForkedInstanceForUI
	startForkedInstanceForUI = func(child *session.Instance) error {
		verify, err := session.NewStorageWithProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		defer verify.Close()
		loaded, err := verify.Load()
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range loaded {
			if candidate.ID == child.ID && candidate.IsForkAwaitingStart && candidate.ForkStartCommand == child.ForkStartCommand {
				return probeErr
			}
		}
		t.Fatalf("UI provider-start boundary has no durable exact OMP recipe for %s", child.ID)
		return probeErr
	}
	t.Cleanup(func() { startForkedInstanceForUI = oldStart })
	return probeErr
}

func TestAdoptCheckpointedForkReplacesWatcherPreloadByID(t *testing.T) {
	before := &session.Instance{ID: "before", Title: "before", GroupPath: "renamed-forks", Tool: "omp", Order: 0}
	preloaded := &session.Instance{ID: "checkpointed-child", Title: "user renamed while starting", GroupPath: "renamed-forks", Tool: "omp", Order: 1, TitleLocked: true}
	after := &session.Instance{ID: "after", Title: "after", GroupPath: "renamed-forks", Tool: "omp", Order: 2}
	result := &session.Instance{ID: preloaded.ID, Title: "stale launch title", GroupPath: "old-forks", Tool: "omp"}
	h := &Home{
		instances:    []*session.Instance{before, preloaded, after},
		instanceByID: map[string]*session.Instance{preloaded.ID: preloaded},
	}
	h.groupTree = session.NewGroupTree(h.instances)

	adopted, _, err := h.adoptCheckpointedFork(result)
	if err != nil || !adopted {
		t.Fatalf("adoptCheckpointedFork() = (%t, %v), want true, nil", adopted, err)
	}

	if len(h.instances) != 3 || h.instances[1] != result {
		t.Fatalf("checkpoint preload was duplicated instead of replaced: %#v", h.instances)
	}
	if result.Title != preloaded.Title || result.GroupPath != preloaded.GroupPath || result.Order != preloaded.Order || !result.TitleLocked {
		t.Fatalf("launch result clobbered user metadata from checkpoint preload: %+v", result)
	}
	if h.instanceByID[result.ID] != result {
		t.Fatal("instanceByID did not adopt launch-result instance")
	}
	group := h.groupTree.Groups[result.GroupPath]
	if group == nil || len(group.Sessions) != 3 || group.Sessions[0] != before || group.Sessions[1] != result || group.Sessions[2] != after {
		t.Fatalf("group tree retained duplicate checkpoint rows: %#v", group)
	}
}

func TestTUIForkCommandCheckpointsOmpBeforeProviderStart(t *testing.T) {
	const profile = "_test"
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	parent := seedUIOmpForkParent(t, storage)
	probeErr := installUIOmpStartCheckpointProbe(t, profile)
	h := &Home{profile: profile, storage: storage, forkingSessions: make(map[string]time.Time)}
	cmd := h.forkSessionCmdWithOptions(parent, "child", "forks", forkToggles{}, nil, git.WorktreeStateOptions{}, "", "", "")
	msg, ok := cmd().(sessionForkedMsg)
	if !ok || msg.instance == nil || !errors.Is(msg.err, probeErr) {
		t.Fatalf("TUI fork result = (%T, %+v), want checkpointed child + probe error", msg, msg)
	}
}

func TestWebForkEntryPointsCheckpointOmpBeforeProviderStart(t *testing.T) {
	for _, withOptions := range []bool{false, true} {
		t.Run(fmt.Sprintf("options=%t", withOptions), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			profile := "_test"
			h, storage := newHeadlessHomeForTest(t, profile)
			h.search = NewSearch()
			menu := web.NewMemoryMenuData(nil)
			h.SetWebMenuData(menu)
			menu.SetSnapshot(&web.MenuSnapshot{}) // pre-fork cached headless view
			parent := seedUIOmpForkParent(t, storage)
			probeErr := installUIOmpStartCheckpointProbe(t, profile)
			mutator := NewWebMutator(h)
			var id string
			var err error
			if withOptions {
				id, err = mutator.ForkSessionWithOptions(parent.ID, web.ForkSessionRequest{Title: "child"})
			} else {
				id, err = mutator.ForkSession(parent.ID)
			}
			if id == "" || !errors.Is(err, probeErr) {
				t.Fatalf("web checkpointed probe result = (%q, %v), want child ID + probe error", id, err)
			}
			if h.instanceByID[id] == nil {
				t.Fatalf("durable failed child %s was not adopted into instanceByID", id)
			}
			if h.flatItemIndexByID(id) < 0 {
				t.Fatalf("durable failed child %s is absent from rendered flat items", id)
			}
			foundSearch := false
			for _, item := range h.search.localItems {
				foundSearch = foundSearch || item.SessionID == id
			}
			if !foundSearch {
				t.Fatalf("durable failed child %s is absent from search", id)
			}
			snapshot, err := menu.LoadMenuSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			foundMenu := false
			for _, item := range snapshot.Items {
				foundMenu = foundMenu || item.Session != nil && item.Session.ID == id
			}
			if !foundMenu {
				t.Fatalf("durable failed child %s is absent from published web menu", id)
			}
		})
	}
}

func seedCheckpointedUIFork(t *testing.T, storage *session.Storage) *session.Instance {
	t.Helper()
	inst := session.NewInstanceWithGroupAndTool("launch title", t.TempDir(), "old-group", "omp")
	inst.Command = "omp"
	inst.ForkStartCommand = "generated-native-fork-recipe"
	inst.IsForkAwaitingStart = true
	if preserve, err := storage.CheckpointOmpForkBeforeStart(inst); err != nil || !preserve {
		t.Fatalf("checkpoint = (%t, %v), want true, nil", preserve, err)
	}
	return inst
}

func loadCheckpointedUIFork(t *testing.T, storage *session.Storage, id string) *session.Instance {
	t.Helper()
	loaded, err := storage.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range loaded {
		if candidate.ID == id {
			return candidate
		}
	}
	t.Fatalf("checkpoint %s was not reloadable", id)
	return nil
}

func writeLatestOmpForkMetadata(t *testing.T, storage *session.Storage, id string) {
	t.Helper()
	row, err := storage.GetDB().LoadInstanceByID(id)
	if err != nil || row == nil {
		t.Fatalf("load checkpoint row = (%v, %v)", row, err)
	}
	row.Title = "persisted-new"
	row.ProjectPath = t.TempDir()
	row.GroupPath = "new-group"
	row.Order = 7
	row.Command = "omp --model persisted"
	row.Wrapper = "env WRAPPED=1 {command}"
	row.Account = "new-account"
	row.NoTransitionNotify = true
	row.TitleLocked = true
	row.Pin = string(session.PinTop)
	row.ArchivedAt = time.Now().UTC().Truncate(time.Second)
	var data map[string]json.RawMessage
	if err := json.Unmarshal(row.ToolData, &data); err != nil {
		t.Fatal(err)
	}
	data["notes"] = json.RawMessage(`"new notes"`)
	data["color"] = json.RawMessage(`"#123456"`)
	data["tool_options"] = json.RawMessage(`{"tool":"omp","options":{"model":"persisted-model"}}`)
	data["idle_timeout_secs"] = json.RawMessage(`91`)
	row.ToolData, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.GetDB().SaveInstance(row); err != nil {
		t.Fatal(err)
	}
}

func assertLatestOmpForkMetadata(t *testing.T, inst *session.Instance) {
	t.Helper()
	opts, err := session.UnmarshalOmpOptions(inst.ToolOptionsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Title != "persisted-new" || inst.GroupPath != "new-group" || inst.Order != 7 ||
		inst.Command != "omp --model persisted" || inst.Wrapper != "env WRAPPED=1 {command}" ||
		inst.Account != "new-account" || !inst.NoTransitionNotify || !inst.TitleLocked ||
		inst.Pin != session.PinTop || inst.Notes != "new notes" || inst.Color != "#123456" ||
		inst.IdleTimeoutSecs != 91 || opts == nil || opts.Model != "persisted-model" || inst.ArchivedAt.IsZero() {
		t.Fatalf("launch result did not adopt latest persisted metadata: %+v opts=%+v", inst, opts)
	}
}

func TestAdoptCheckpointedForkPrefersLatestDatabaseOverOldPreload(t *testing.T) {
	for _, preload := range []bool{false, true} {
		t.Run(fmt.Sprintf("preload=%t", preload), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			storage, err := session.NewStorageWithProfile("_test")
			if err != nil {
				t.Fatal(err)
			}
			defer storage.Close()
			result := seedCheckpointedUIFork(t, storage)
			writeLatestOmpForkMetadata(t, storage, result.ID)
			result.ForkStartCommand = ""
			result.IsForkAwaitingStart = false
			result.Title = "stale-result"
			result.GroupPath = "old-group"
			result.Order = 0

			before := &session.Instance{ID: "before", Title: "before", GroupPath: "new-group", Order: 6, Tool: "omp"}
			after := &session.Instance{ID: "after", Title: "after", GroupPath: "new-group", Order: 8, Tool: "omp"}
			h := &Home{profile: "_test", storage: storage, instances: []*session.Instance{before, after}, instanceByID: make(map[string]*session.Instance)}
			if preload {
				old := &session.Instance{ID: result.ID, Title: "old-preload", GroupPath: "old-group", Order: 2, Tool: "omp"}
				h.instances = []*session.Instance{before, old, after}
				h.instanceByID[result.ID] = old
			}
			h.groupTree = session.NewGroupTree(h.instances)
			adopted, _, err := h.adoptCheckpointedFork(result)
			if err != nil || !adopted {
				t.Fatalf("adopt = (%t, %v)", adopted, err)
			}
			assertLatestOmpForkMetadata(t, result)
			group := h.groupTree.Groups["new-group"]
			if group == nil || len(group.Sessions) != 3 || group.Sessions[0] != before || group.Sessions[1] != result || group.Sessions[2] != after {
				t.Fatalf("latest durable order was not preserved among siblings: %#v", group)
			}
			if !h.forceSaveInstances() {
				t.Fatal("routine save after adoption failed")
			}
			row, err := storage.GetDB().LoadInstanceByID(result.ID)
			if err != nil || row == nil || row.Title != "persisted-new" || row.GroupPath != "new-group" || row.Command != "omp --model persisted" {
				t.Fatalf("later routine save clobbered persisted edit: row=%+v err=%v", row, err)
			}
		})
	}
}

func TestAdoptCheckpointedForkReappliesPendingUIIntent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile("_test")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	result := seedCheckpointedUIFork(t, storage)
	result.ParentSessionID = "must-not-receive-child-title"
	writeLatestOmpForkMetadata(t, storage, result.ID)
	result.ForkStartCommand = ""
	result.IsForkAwaitingStart = false
	h := &Home{
		profile:             "_test",
		storage:             storage,
		instances:           []*session.Instance{{ID: result.ID, Title: "old-preload", GroupPath: "new-group", Tool: "omp"}},
		instanceByID:        make(map[string]*session.Instance),
		pendingTitleChanges: map[string]pendingTitle{result.ID: {title: "pending-title", locked: true}},
		pendingGroupOps:     []pendingGroupOp{{kind: groupOpRename, oldPath: "new-group", name: "pending-group"}},
	}
	h.instanceByID[result.ID] = h.instances[0]
	h.groupTree = session.NewGroupTree(h.instances)
	adopted, titleCmd, err := h.adoptCheckpointedFork(result)
	if err != nil || !adopted {
		t.Fatalf("adopt = (%t, %v)", adopted, err)
	}
	if result.Title != "pending-title" || result.GroupPath != "pending-group" || !result.TitleLocked {
		t.Fatalf("pending UI intent lost after reconciliation: %+v", result)
	}
	if titleCmd == nil {
		t.Fatal("pending OMP title did not schedule asynchronous convergence")
	}
	titleIntentPath := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", result.ID, ".agent-deck-title.json")
	if _, err := os.Stat(titleIntentPath); !os.IsNotExist(err) {
		t.Fatalf("adoption performed provider title I/O synchronously: %v", err)
	}
	msg, ok := titleCmd().(ompForkTitleSyncResultMsg)
	if !ok || msg.err != nil {
		t.Fatalf("title convergence result = (%T, %+v)", msg, msg)
	}
	_, _ = h.updateInner(msg)
	if result.Title != "pending-title" || result.GroupPath != "pending-group" {
		t.Fatalf("title result reconciliation lost pending UI intent: %+v", result)
	}
	if data, err := os.ReadFile(titleIntentPath); err != nil || !strings.Contains(string(data), "pending-title") {
		t.Fatalf("provider title intent did not converge: %q, %v", data, err)
	}
	wrongIntent := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", result.ParentSessionID, ".agent-deck-title.json")
	if _, err := os.Stat(wrongIntent); !os.IsNotExist(err) {
		t.Fatalf("child title was written through the parent identity: %v", err)
	}
	if !h.forceSaveInstances() {
		t.Fatal("save pending UI intent failed")
	}
	row, err := storage.GetDB().LoadInstanceByID(result.ID)
	if err != nil || row == nil || row.Title != "pending-title" || row.GroupPath != "pending-group" {
		t.Fatalf("pending UI intent was not durable after routine save: row=%+v err=%v", row, err)
	}
}

func TestQueuedForkTitleSyncCannotOverwriteNewerSuccessfulRename(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile("_test")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	result := seedCheckpointedUIFork(t, storage)
	h := &Home{
		profile:             "_test",
		storage:             storage,
		instanceByID:        make(map[string]*session.Instance),
		pendingTitleChanges: map[string]pendingTitle{result.ID: {title: "older-pending", locked: true}},
	}
	h.groupTree = session.NewGroupTree(nil)
	adopted, titleCmd, err := h.adoptCheckpointedFork(result)
	if err != nil || !adopted || titleCmd == nil {
		t.Fatalf("adopt = (%t, cmd=%v, err=%v)", adopted, titleCmd != nil, err)
	}
	if _, err := storage.GetDB().WriteOmpForkUserTitle(result.ID, "launch title", "newer-success", true); err != nil {
		t.Fatal(err)
	}
	result.SetTitleThreadSafe("newer-success")
	delete(h.pendingTitleChanges, result.ID) // successful normal rename/save
	msg, ok := titleCmd().(ompForkTitleSyncResultMsg)
	if !ok || msg.err != nil {
		t.Fatalf("queued convergence = (%T, %+v)", msg, msg)
	}
	row, err := storage.GetDB().LoadInstanceByID(result.ID)
	if err != nil || row == nil || row.Title != "newer-success" {
		t.Fatalf("stale queued command overwrote newer DB title: row=%+v err=%v", row, err)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", result.ID, ".agent-deck-title.json"))
	if err != nil || !strings.Contains(string(data), "newer-success") || strings.Contains(string(data), "older-pending") {
		t.Fatalf("provider intent did not converge to newer title: %q, %v", data, err)
	}
}

func TestForkTitleSyncResultAdoptsNewerExternalTitleBeforeLaterSave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile("_test")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	result := seedCheckpointedUIFork(t, storage)
	h := &Home{
		profile:             "_test",
		storage:             storage,
		instanceByID:        make(map[string]*session.Instance),
		pendingTitleChanges: map[string]pendingTitle{result.ID: {title: "older-pending", locked: true}},
		search:              NewSearch(),
	}
	h.groupTree = session.NewGroupTree(nil)
	adopted, titleCmd, err := h.adoptCheckpointedFork(result)
	if err != nil || !adopted || titleCmd == nil {
		t.Fatalf("adopt = (%t, cmd=%v, err=%v)", adopted, titleCmd != nil, err)
	}
	if _, err := storage.GetDB().WriteOmpForkUserTitle(result.ID, "launch title", "external-cli-title", true); err != nil {
		t.Fatal(err)
	}
	msg := titleCmd()
	_, _ = h.updateInner(msg)
	if result.GetTitleThreadSafe() != "external-cli-title" {
		t.Fatalf("title result left stale in-memory title %q", result.GetTitleThreadSafe())
	}
	if _, ok := h.pendingTitleChanges[result.ID]; ok {
		t.Fatal("superseded pending title was not cleared after durable reconciliation")
	}
	_ = h.forceSaveInstances()
	row, err := storage.GetDB().LoadInstanceByID(result.ID)
	if err != nil || row == nil || row.Title != "external-cli-title" || !row.TitleLocked {
		t.Fatalf("later save overwrote external title: row=%+v err=%v", row, err)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", result.ID, ".agent-deck-title.json"))
	if err != nil || !strings.Contains(string(data), "external-cli-title") {
		t.Fatalf("provider intent missed external title: %q, %v", data, err)
	}
}

func TestForkTitleSyncErrorSettlesOnlyFailedIntent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		web         bool
		dbFailure   bool
		newer       *pendingTitle
		wantTitle   string
		wantLocked  bool
		wantDurable string
	}{
		{name: "lost-cas-transport", wantTitle: "external-cli-title", wantLocked: true, wantDurable: "external-cli-title"},
		{name: "newer-pending-title", newer: &pendingTitle{title: "newer-local-title", locked: true}, wantTitle: "newer-local-title", wantLocked: true, wantDurable: "external-cli-title"},
		{name: "newer-pending-lock", newer: &pendingTitle{title: "older-pending", locked: false}, wantTitle: "older-pending", wantDurable: "external-cli-title"},
		{name: "tui-persistence-error", dbFailure: true, wantTitle: "launch title", wantDurable: "launch title"},
		{name: "web-persistence-error", web: true, dbFailure: true, wantTitle: "launch title", wantDurable: "launch title"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			storage, err := session.NewStorageWithProfile("_test")
			if err != nil {
				t.Fatal(err)
			}
			defer storage.Close()
			result := seedCheckpointedUIFork(t, storage)
			h := &Home{
				profile:             "_test",
				storage:             storage,
				instanceByID:        make(map[string]*session.Instance),
				pendingTitleChanges: map[string]pendingTitle{result.ID: {title: "older-pending", locked: true}},
				pendingGroupOps: []pendingGroupOp{
					{kind: groupOpMove, sessionID: result.ID, targetPath: "pending-child-group"},
					{kind: groupOpRename, oldPath: "unrelated", name: "keep-rename"},
				},
				search: NewSearch(),
			}
			h.groupTree = session.NewGroupTree(nil)
			adopted, titleCmd, err := h.adoptCheckpointedFork(result)
			if err != nil || !adopted || titleCmd == nil {
				t.Fatalf("adopt = (%t, cmd=%v, err=%v)", adopted, titleCmd != nil, err)
			}
			intentDir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", result.ID)
			if tc.dbFailure {
				// Reject the actual title UPDATE while leaving the durable row
				// readable for error reconciliation, then recover before saving.
				if _, err := storage.GetDB().DB().Exec(`CREATE TRIGGER reject_test_title BEFORE UPDATE OF title ON instances BEGIN SELECT RAISE(ABORT, 'test title persistence failed'); END`); err != nil {
					t.Fatal(err)
				}
			} else {
				row, err := storage.GetDB().LoadInstanceByID(result.ID)
				if err != nil || row == nil {
					t.Fatalf("load checkpoint = (%v, %v)", row, err)
				}
				row.Title, row.TitleLocked = "external-cli-title", true
				row.Command = "omp --model external"
				if err := storage.GetDB().SaveInstance(row); err != nil {
					t.Fatal(err)
				}
				// A file in place of the child's directory makes the real local
				// provider sync fail without touching any live provider state.
				if err := os.MkdirAll(filepath.Dir(intentDir), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(intentDir, []byte("block title sync"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.newer != nil {
				// A different local intent arrives after the older command was
				// queued, but before its result is delivered.
				h.pendingTitleChanges[result.ID] = *tc.newer
				result.SetTitleThreadSafe(tc.newer.title)
				result.TitleLocked = tc.newer.locked
			}
			var originalErr error
			if tc.web {
				adopted, originalErr = h.presentCheckpointedFork(result)
				if !adopted {
					t.Fatal("web error discarded the durable child")
				}
			} else {
				msg, ok := titleCmd().(ompForkTitleSyncResultMsg)
				if !ok || msg.err == nil {
					t.Fatalf("title command = (%T, %+v), want a real failure", msg, msg)
				}
				originalErr = msg.err
				if !tc.dbFailure {
					if err := os.Remove(intentDir); err != nil {
						t.Fatal(err)
					}
				}
				_, nextCmd := h.updateInner(msg)
				if nextCmd != nil || !errors.Is(h.err, originalErr) {
					t.Fatalf("error delivery retried or replaced the original error: cmd=%v err=%v original=%v", nextCmd != nil, h.err, originalErr)
				}
			}
			wantError := "OMP title sync failed"
			if tc.dbFailure {
				wantError = "test title persistence failed"
				if _, err := storage.GetDB().DB().Exec(`DROP TRIGGER reject_test_title`); err != nil {
					t.Fatal(err)
				}
			}
			if originalErr == nil || !strings.Contains(originalErr.Error(), wantError) {
				t.Fatalf("original error = %v, want %q", originalErr, wantError)
			}
			if _, err := os.Stat(filepath.Join(intentDir, ".agent-deck-title.json")); !os.IsNotExist(err) {
				t.Fatalf("error delivery performed provider title I/O: %v", err)
			}
			if result.GetTitleThreadSafe() != tc.wantTitle || result.TitleLocked != tc.wantLocked {
				t.Errorf("error reconciliation title = (%q, locked=%t), want (%q, locked=%t)", result.GetTitleThreadSafe(), result.TitleLocked, tc.wantTitle, tc.wantLocked)
			}
			pending, hasPending := h.pendingTitleChanges[result.ID]
			if tc.newer == nil && hasPending || tc.newer != nil && (!hasPending || pending != *tc.newer) {
				t.Errorf("error settled the wrong pending intent: pending=%+v present=%t", pending, hasPending)
			}
			if len(h.pendingGroupOps) != 2 || h.pendingGroupOps[0].targetPath != "pending-child-group" || h.pendingGroupOps[1].name != "keep-rename" || result.GroupPath != "pending-child-group" {
				t.Fatalf("title error changed unrelated group intent: ops=%+v group=%q", h.pendingGroupOps, result.GroupPath)
			}
			row, err := storage.GetDB().LoadInstanceByID(result.ID)
			if err != nil || row == nil || row.Title != tc.wantDurable {
				t.Fatalf("error delivery changed durable title: row=%+v err=%v", row, err)
			}
			if !tc.dbFailure && result.Command != "omp --model external" {
				t.Fatalf("error lost latest durable metadata: command=%q", result.Command)
			}
			instances, groups, err := storage.LoadWithGroups()
			if err != nil {
				t.Fatal(err)
			}
			_, _ = h.updateInner(loadSessionsMsg{instances: instances, groups: groups})
			if !h.forceSaveInstances() {
				t.Fatal("routine save after error/reload failed")
			}
			row, err = storage.GetDB().LoadInstanceByID(result.ID)
			if err != nil || row == nil || row.Title != tc.wantTitle || row.TitleLocked != tc.wantLocked || row.GroupPath != "pending-child-group" {
				t.Fatalf("later reload/save persisted failed intent or lost newer title/group intent: row=%+v err=%v", row, err)
			}
		})
	}
}

func TestForkTitleSyncErrorPurgesDeletedOrSupersededPreload(t *testing.T) {
	for _, mode := range []string{"deleted", "superseded"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			storage, err := session.NewStorageWithProfile("_test")
			if err != nil {
				t.Fatal(err)
			}
			defer storage.Close()
			result := seedCheckpointedUIFork(t, storage)
			h := &Home{
				profile:             "_test",
				storage:             storage,
				instanceByID:        make(map[string]*session.Instance),
				pendingTitleChanges: map[string]pendingTitle{result.ID: {title: "pending", locked: true}},
				pendingGroupOps: []pendingGroupOp{
					{kind: groupOpMove, sessionID: result.ID, targetPath: "retired-child-group"},
					{kind: groupOpMove, sessionID: "unrelated", targetPath: "keep-this-group"},
				},
				search: NewSearch(),
			}
			h.groupTree = session.NewGroupTree(nil)
			adopted, titleCmd, err := h.adoptCheckpointedFork(result)
			if err != nil || !adopted || titleCmd == nil {
				t.Fatalf("adopt = (%t, cmd=%v, err=%v)", adopted, titleCmd != nil, err)
			}
			if mode == "deleted" {
				if err := storage.GetDB().DeleteInstanceRow(result.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				row, err := storage.GetDB().LoadInstanceByID(result.ID)
				if err != nil || row == nil {
					t.Fatalf("load row = (%v, %v)", row, err)
				}
				row.Tool = "claude"
				row.Title = "replacement"
				if err := storage.GetDB().SaveInstance(row); err != nil {
					t.Fatal(err)
				}
			}
			msg, ok := titleCmd().(ompForkTitleSyncResultMsg)
			if !ok || !errors.Is(msg.err, statedb.ErrInstanceNotStored) {
				t.Fatalf("title sync result = (%T, %+v), want ErrInstanceNotStored", msg, msg)
			}
			// A remote SSH/docker failure can race with the same deletion or
			// replacement after provider synchronization has begun. The UI must
			// reconcile storage on every error, not only the sentinel returned by
			// the local compare-and-set.
			msg.err = errors.New("provider title transport failed")
			_, _ = h.updateInner(msg)
			if h.instanceByID[result.ID] != nil || h.flatItemIndexByID(result.ID) >= 0 {
				t.Fatal("title-sync error left stale OMP preload visible")
			}
			if _, ok := h.pendingTitleChanges[result.ID]; ok {
				t.Fatal("retired child retained a pending title")
			}
			if len(h.pendingGroupOps) != 1 || h.pendingGroupOps[0].sessionID != "unrelated" {
				t.Fatalf("retired child move was not removed without affecting unrelated operations: %+v", h.pendingGroupOps)
			}
			instances, groups, loadErr := storage.LoadWithGroups()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			_, _ = h.updateInner(loadSessionsMsg{instances: instances, groups: groups})
			_ = h.forceSaveInstances()
			row, err := storage.GetDB().LoadInstanceByID(result.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "deleted" && row != nil {
				t.Fatalf("later save resurrected deleted row: %+v", row)
			}
			if mode == "superseded" && (row == nil || row.Tool != "claude" || row.Title != "replacement") {
				t.Fatalf("later save overwrote superseding row: %+v", row)
			}
		})
	}
}

func TestPresentCheckpointedForkReconcilesDeletionAfterGenericTitleTransportError(t *testing.T) {
	testPresentCheckpointedForkTitleTransportError(t, true)
}

func TestPresentCheckpointedForkReconcilesExternalTitleAfterGenericTitleTransportError(t *testing.T) {
	testPresentCheckpointedForkTitleTransportError(t, false)
}

func testPresentCheckpointedForkTitleTransportError(t *testing.T, deleted bool) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile("_test")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	result := seedCheckpointedUIFork(t, storage)
	result.SSHHost = "fake-host"
	h := &Home{
		profile:             "_test",
		storage:             storage,
		instanceByID:        make(map[string]*session.Instance),
		pendingTitleChanges: map[string]pendingTitle{result.ID: {title: "pending", locked: true}},
		pendingGroupOps:     []pendingGroupOp{{kind: groupOpMove, sessionID: result.ID, targetPath: "retired"}},
		search:              NewSearch(),
	}
	h.groupTree = session.NewGroupTree(nil)

	binDir := t.TempDir()
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	fakeSSH := filepath.Join(binDir, "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nprintf 'sync\\n' >> \"$OMP_TITLE_SYNC_STARTED\"\ni=0\nwhile [ ! -f \"$OMP_TITLE_SYNC_RELEASE\" ] && [ \"$i\" -lt 500 ]; do sleep 0.01; i=$((i + 1)); done\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OMP_TITLE_SYNC_STARTED", started)
	t.Setenv("OMP_TITLE_SYNC_RELEASE", release)
	editDone := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, statErr := os.Stat(started); statErr == nil {
				break
			}
			if time.Now().After(deadline) {
				editDone <- errors.New("fake SSH title sync did not start")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		var editErr error
		if deleted {
			editErr = storage.GetDB().DeleteInstanceRow(result.ID)
		} else {
			var changed bool
			changed, editErr = storage.GetDB().WriteOmpForkUserTitle(result.ID, "pending", "external-cli-title", true)
			if editErr == nil && !changed {
				editErr = errors.New("external rename did not replace the in-flight title")
			}
		}
		if releaseErr := os.WriteFile(release, []byte("release"), 0o600); editErr == nil {
			editErr = releaseErr
		}
		editDone <- editErr
	}()

	adopted, presentErr := h.presentCheckpointedFork(result)
	if err := <-editDone; err != nil {
		t.Fatal(err)
	}
	if adopted == deleted || presentErr == nil || !strings.Contains(presentErr.Error(), "OMP title sync failed") {
		t.Fatalf("present after generic transport failure = (%t, %v), deleted=%t; want reconciled child and original transport error", adopted, presentErr, deleted)
	}
	if data, err := os.ReadFile(started); err != nil || string(data) != "sync\n" {
		t.Fatalf("web error implicitly retried provider sync: calls=%q err=%v", data, err)
	}
	if deleted && (h.instanceByID[result.ID] != nil || h.flatItemIndexByID(result.ID) >= 0) {
		t.Fatal("web presentation retained deleted child after generic title error")
	}
	if !deleted && (h.instanceByID[result.ID] == nil || result.GetTitleThreadSafe() != "external-cli-title") {
		t.Errorf("web title error did not adopt latest durable title: child=%+v", result)
	}
	if _, ok := h.pendingTitleChanges[result.ID]; ok {
		t.Errorf("web cleanup retained failed title intent: titles=%+v", h.pendingTitleChanges)
	}
	if deleted && len(h.pendingGroupOps) != 0 || !deleted && (len(h.pendingGroupOps) != 1 || h.pendingGroupOps[0].sessionID != result.ID) {
		t.Fatalf("web cleanup changed the wrong group intent: %+v", h.pendingGroupOps)
	}
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.updateInner(loadSessionsMsg{instances: instances, groups: groups})
	_ = h.forceSaveInstances()
	row, loadErr := storage.GetDB().LoadInstanceByID(result.ID)
	if loadErr != nil || deleted && row != nil || !deleted && (row == nil || row.Title != "external-cli-title" || !row.TitleLocked) {
		t.Fatalf("later web reload/save resurrected or overwrote durable child: row=%+v err=%v", row, loadErr)
	}
}

func TestPresentCheckpointedForkDoesNotResurrectDeletedRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile("_test")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	result := seedCheckpointedUIFork(t, storage)
	preload := loadCheckpointedUIFork(t, storage, result.ID)
	h := &Home{
		profile:      "_test",
		storage:      storage,
		instances:    []*session.Instance{preload},
		instanceByID: map[string]*session.Instance{result.ID: preload},
		search:       NewSearch(),
	}
	h.groupTree = session.NewGroupTree(h.instances)
	menu := web.NewMemoryMenuData(nil)
	h.SetWebMenuData(menu)
	h.refreshCheckpointedForkPresentation()
	if err := storage.GetDB().DeleteInstanceRow(result.ID); err != nil {
		t.Fatal(err)
	}
	adopted, err := h.presentCheckpointedFork(result)
	if err != nil || adopted {
		t.Fatalf("present deleted child = (%t, %v), want false, nil", adopted, err)
	}
	if h.instanceByID[result.ID] != nil || h.flatItemIndexByID(result.ID) >= 0 {
		t.Fatalf("deleted child remained visible: map=%v flat=%d", h.instanceByID[result.ID], h.flatItemIndexByID(result.ID))
	}
	// An empty-list force save may legitimately report success; the row must
	// still stay absent, which is the actual no-resurrection contract.
	_ = h.forceSaveInstances()
	row, err := storage.GetDB().LoadInstanceByID(result.ID)
	if err != nil || row != nil {
		t.Fatalf("deleted child was resurrected: row=%+v err=%v", row, err)
	}
}

func TestWebFailedForkDoesNotResurrectCheckpointDeletedDuringStart(t *testing.T) {
	for _, withOptions := range []bool{false, true} {
		t.Run(fmt.Sprintf("options=%t", withOptions), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			const profile = "_test"
			h, storage := newHeadlessHomeForTest(t, profile)
			parent := seedUIOmpForkParent(t, storage)
			probeErr := errors.New("provider failed after concurrent delete")
			childID := ""
			oldStart := startForkedInstanceForUI
			startForkedInstanceForUI = func(child *session.Instance) error {
				childID = child.ID
				if err := storage.GetDB().DeleteInstanceRow(child.ID); err != nil {
					t.Fatal(err)
				}
				return probeErr
			}
			t.Cleanup(func() { startForkedInstanceForUI = oldStart })
			mutator := NewWebMutator(h)
			var id string
			var err error
			if withOptions {
				id, err = mutator.ForkSessionWithOptions(parent.ID, web.ForkSessionRequest{Title: "child"})
			} else {
				id, err = mutator.ForkSession(parent.ID)
			}
			if id != "" || !errors.Is(err, probeErr) {
				t.Fatalf("deleted checkpoint result = (%q, %v), want blank ID + provider error", id, err)
			}
			if childID == "" {
				t.Fatal("provider-start probe was not invoked")
			}
			if h.instanceByID[childID] != nil {
				t.Fatalf("deleted checkpoint was re-adopted: instances=%+v", h.instances)
			}
			row, loadErr := storage.GetDB().LoadInstanceByID(childID)
			if loadErr != nil || row != nil {
				t.Fatalf("deleted checkpoint was resurrected: row=%+v err=%v", row, loadErr)
			}
		})
	}
}

func TestTUIFailedForkResultDoesNotReAdoptDeletedCheckpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile("_test")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	result := seedCheckpointedUIFork(t, storage)
	if err := storage.GetDB().DeleteInstanceRow(result.ID); err != nil {
		t.Fatal(err)
	}
	h := NewHome()
	h.profile = "_test"
	h.storage = storage
	_, _ = h.updateInner(sessionForkedMsg{instance: result, err: errors.New("provider failed")})
	if h.instanceByID[result.ID] != nil || h.flatItemIndexByID(result.ID) >= 0 {
		t.Fatalf("TUI re-adopted a deleted failed child: map=%v flat=%d", h.instanceByID[result.ID], h.flatItemIndexByID(result.ID))
	}
	row, err := storage.GetDB().LoadInstanceByID(result.ID)
	if err != nil || row != nil {
		t.Fatalf("TUI resurrected deleted child: row=%+v err=%v", row, err)
	}
}

func TestAdoptCheckpointedForkPurgesPreloadWhenRowToolWasReused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile("_test")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	result := seedCheckpointedUIFork(t, storage)
	preload := loadCheckpointedUIFork(t, storage, result.ID)
	row, err := storage.GetDB().LoadInstanceByID(result.ID)
	if err != nil || row == nil {
		t.Fatalf("load row = (%v, %v)", row, err)
	}
	row.Tool = "claude"
	row.Title = "replacement"
	if err := storage.GetDB().SaveInstance(row); err != nil {
		t.Fatal(err)
	}
	h := &Home{
		profile:      "_test",
		storage:      storage,
		instances:    []*session.Instance{preload},
		instanceByID: map[string]*session.Instance{result.ID: preload},
		search:       NewSearch(),
	}
	h.groupTree = session.NewGroupTree(h.instances)
	adopted, _, err := h.adoptCheckpointedFork(result)
	if adopted || !errors.Is(err, session.ErrOmpForkCheckpointSuperseded) {
		t.Fatalf("adopt superseded row = (%t, %v)", adopted, err)
	}
	if h.instanceByID[result.ID] != nil || h.flatItemIndexByID(result.ID) >= 0 {
		t.Fatal("stale OMP preload survived superseded-row reconciliation")
	}
	_ = h.forceSaveInstances()
	row, err = storage.GetDB().LoadInstanceByID(result.ID)
	if err != nil || row == nil || row.Tool != "claude" || row.Title != "replacement" {
		t.Fatalf("later save overwrote superseding row: row=%+v err=%v", row, err)
	}
}

func preloadOmpCheckpointForTest(t *testing.T, h *Home, storage *session.Storage, id string) {
	t.Helper()
	candidate := loadCheckpointedUIFork(t, storage, id)
	h.instancesMu.Lock()
	h.instances = append(h.instances, candidate)
	if h.instanceByID == nil {
		h.instanceByID = make(map[string]*session.Instance)
	}
	h.instanceByID[id] = candidate
	h.instancesMu.Unlock()
	if h.groupTree == nil {
		h.groupTree = session.NewGroupTree(h.instances)
	} else {
		h.groupTree.AddSession(candidate)
	}
}

func TestWebSuccessfulStartPurgesCheckpointDeletedBeforeFinalization(t *testing.T) {
	for _, withOptions := range []bool{false, true} {
		t.Run(fmt.Sprintf("options=%t", withOptions), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			const profile = "_test"
			h, storage := newHeadlessHomeForTest(t, profile)
			h.search = NewSearch()
			menu := web.NewMemoryMenuData(nil)
			h.SetWebMenuData(menu)
			parent := seedUIOmpForkParent(t, storage)
			childID := ""
			oldStart := startForkedInstanceForUI
			startForkedInstanceForUI = func(child *session.Instance) error {
				childID = child.ID
				preloadOmpCheckpointForTest(t, h, storage, child.ID)
				if err := storage.GetDB().DeleteInstanceRow(child.ID); err != nil {
					t.Fatal(err)
				}
				child.IsForkAwaitingStart = false
				child.ForkStartCommand = ""
				child.SetStatusThreadSafe(session.StatusRunning)
				return nil
			}
			t.Cleanup(func() { startForkedInstanceForUI = oldStart })
			mutator := NewWebMutator(h)
			var id string
			var err error
			if withOptions {
				id, err = mutator.ForkSessionWithOptions(parent.ID, web.ForkSessionRequest{Title: "child"})
			} else {
				id, err = mutator.ForkSession(parent.ID)
			}
			if childID == "" || id != "" || !errors.Is(err, statedb.ErrInstanceNotStored) {
				t.Fatalf("deleted positive-start result = child=%q id=%q err=%v", childID, id, err)
			}
			if h.instanceByID[childID] != nil || h.flatItemIndexByID(childID) >= 0 {
				t.Fatal("positive-start completion retained deleted watcher preload")
			}
			_ = h.forceSaveInstances()
			row, loadErr := storage.GetDB().LoadInstanceByID(childID)
			if loadErr != nil || row != nil {
				t.Fatalf("later save resurrected deleted positive-start child: row=%+v err=%v", row, loadErr)
			}
		})
	}
}

func TestTUISuccessfulStartPurgesCheckpointDeletedBeforeFinalization(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storage, err := session.NewStorageWithProfile("_test")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	child := seedCheckpointedUIFork(t, storage)
	h := NewHome()
	h.profile = "_test"
	h.storage = storage
	preloadOmpCheckpointForTest(t, h, storage, child.ID)
	if err := storage.GetDB().DeleteInstanceRow(child.ID); err != nil {
		t.Fatal(err)
	}
	child.IsForkAwaitingStart = false
	child.ForkStartCommand = ""
	child.SetStatusThreadSafe(session.StatusRunning)
	finalizeErr := storage.FinalizeOmpForkLaunch(child)
	if !errors.Is(finalizeErr, statedb.ErrInstanceNotStored) {
		t.Fatalf("finalize error = %v", finalizeErr)
	}
	_, _ = h.updateInner(sessionForkedMsg{instance: child, err: finalizeErr})
	if h.instanceByID[child.ID] != nil || h.flatItemIndexByID(child.ID) >= 0 {
		t.Fatal("TUI positive-start completion retained deleted watcher preload")
	}
	_ = h.forceSaveInstances()
	row, err := storage.GetDB().LoadInstanceByID(child.ID)
	if err != nil || row != nil {
		t.Fatalf("TUI later save resurrected deleted child: row=%+v err=%v", row, err)
	}
}
