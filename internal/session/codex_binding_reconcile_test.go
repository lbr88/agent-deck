package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func seedPersistedCodexBinding(t *testing.T, db *statedb.StateDB, inst *Instance, sessionID string) {
	t.Helper()
	toolData, err := json.Marshal(map[string]any{
		"codex_session_id":  sessionID,
		"codex_detected_at": time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("marshal tool data: %v", err)
	}
	if err := db.SaveInstance(&statedb.InstanceRow{
		ID:          inst.ID,
		Title:       inst.Title,
		ProjectPath: inst.ProjectPath,
		GroupPath:   inst.GroupPath,
		Command:     "codex",
		Tool:        "codex",
		Status:      "idle",
		CreatedAt:   time.Now(),
		ToolData:    toolData,
	}); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	inst.stateDB = db
}

func setupCodexPromotionTest(t *testing.T) (codexHome, cwd string, db *statedb.StateDB, inst *Instance, rootID, guardianID, forkID string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	codexHome = filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	cwd = t.TempDir()
	rootID = "11111111-aaaa-4aaa-8aaa-111111111111"
	guardianID = "22222222-bbbb-4bbb-8bbb-222222222222"
	forkID = "33333333-cccc-4ccc-8ccc-333333333333"
	writeCodexRolloutWithSource(t, codexHome, rootID, cwd, "cli")
	writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, rootID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})
	writeCodexRolloutWithSource(t, codexHome, forkID, cwd, "cli")

	db = withTempGlobalStateDB(t)
	inst = NewInstanceWithTool("stale-peer", cwd, "codex")
	inst.Command = "codex"
	inst.CodexSessionID = guardianID
	seedPersistedCodexBinding(t, db, inst, forkID)
	WriteHookSessionAnchor(inst.ID, guardianID)
	return codexHome, cwd, db, inst, rootID, guardianID, forkID
}

func persistCompletedPromotionForTest(t *testing.T, db *statedb.StateDB, inst *Instance, sourceID, oldRootID, targetID string) {
	t.Helper()
	seedPersistedCodexBinding(t, db, inst, sourceID)
	started := time.Now().Add(-time.Second)
	completed := time.Now()
	state := codexSubagentMigrationState{
		SourceID: sourceID, OldRoot: oldRootID, Started: started,
		TargetID: targetID, Completed: completed,
	}
	if err := writeCodexSubagentMigrationState(inst.ID, state); err != nil {
		t.Fatalf("write completed migration: %v", err)
	}
	_, matched, err := db.WriteCodexSessionPromotion(
		inst.ID, targetID, completed, sourceID, oldRootID, started, completed,
		0,
	)
	if err != nil || !matched {
		t.Fatalf("persist completed promotion: matched=%v err=%v", matched, err)
	}
}

func TestAdoptPersistedCodexPromotionHealsStalePeerSnapshot(t *testing.T) {
	_, _, db, inst, _, guardianID, forkID := setupCodexPromotionTest(t)
	inst.hookStatus = "waiting"
	inst.hookEvent = "turn/completed"
	inst.hookSessionID = guardianID
	inst.hookLastUpdate = time.Now()

	if !inst.adoptPersistedCodexPromotion(false) {
		t.Fatal("persisted top-level promotion was not adopted")
	}
	if inst.CodexSessionID != forkID {
		t.Fatalf("CodexSessionID = %q, want promoted fork %q", inst.CodexSessionID, forkID)
	}
	if got := ReadHookSessionAnchor(inst.ID); got != forkID {
		t.Fatalf("hook anchor = %q, want promoted fork %q", got, forkID)
	}
	if inst.hookStatus != "" || inst.hookEvent != "" || inst.hookSessionID != forkID {
		t.Fatalf("stale hook state survived promotion: status=%q event=%q sid=%q",
			inst.hookStatus, inst.hookEvent, inst.hookSessionID)
	}
	if got := readCodexSessionIDFromDB(t, db, inst.ID); got != forkID {
		t.Fatalf("promotion rewrote authoritative DB unexpectedly: got %q want %q", got, forkID)
	}
}

func TestAdoptPersistedCodexPromotionRejectsRecordedOldRoot(t *testing.T) {
	_, _, db, inst, rootID, guardianID, _ := setupCodexPromotionTest(t)
	seedPersistedCodexBinding(t, db, inst, rootID)

	if inst.adoptPersistedCodexPromotion(false) {
		t.Fatal("recorded old root was incorrectly accepted as the migration target")
	}
	if inst.CodexSessionID != guardianID {
		t.Fatalf("CodexSessionID = %q, want guardian migration source %q", inst.CodexSessionID, guardianID)
	}
	if got := ReadHookSessionAnchor(inst.ID); got != guardianID {
		t.Fatalf("hook anchor = %q, want unchanged guardian %q", got, guardianID)
	}
}

func TestAdoptPersistedCodexPromotionHealsStaleOldRootPeer(t *testing.T) {
	_, _, _, inst, rootID, _, forkID := setupCodexPromotionTest(t)
	inst.CodexSessionID = rootID
	WriteHookSessionAnchor(inst.ID, rootID)

	if !inst.adoptPersistedCodexPromotion(false) {
		t.Fatal("newer persisted fork was not adopted by stale old-root peer")
	}
	if inst.CodexSessionID != forkID || ReadHookSessionAnchor(inst.ID) != forkID {
		t.Fatalf("stale root peer not healed: id=%q anchor=%q", inst.CodexSessionID, ReadHookSessionAnchor(inst.ID))
	}
}

func TestAdoptPersistedCodexPromotionHealsEmptyPeerWithCompletedProvenance(t *testing.T) {
	_, _, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	persistCompletedPromotionForTest(t, db, inst, guardianID, rootID, forkID)
	inst.CodexSessionID = ""
	ClearHookSessionAnchor(inst.ID)

	if !inst.adoptPersistedCodexPromotion(false) {
		t.Fatal("completed promotion did not heal pre-detection empty peer")
	}
	if inst.CodexSessionID != forkID || ReadHookSessionAnchor(inst.ID) != forkID {
		t.Fatalf("empty peer not healed: id=%q anchor=%q", inst.CodexSessionID, ReadHookSessionAnchor(inst.ID))
	}
}

func TestAcceptCodexSessionIDRepairsStaleAnchorWhenUnchanged(t *testing.T) {
	_, _, _, inst, _, guardianID, forkID := setupCodexPromotionTest(t)
	inst.CodexSessionID = forkID
	WriteHookSessionAnchor(inst.ID, guardianID)

	if changed := inst.acceptCodexSessionID(forkID, false); changed {
		t.Fatal("unchanged top-level binding reported a rebind")
	}
	if got := ReadHookSessionAnchor(inst.ID); got != forkID {
		t.Fatalf("hook anchor = %q, want healed top-level %q", got, forkID)
	}
}

func TestUnchangedCompletedPromotionDoesNotAdvanceBindingRevision(t *testing.T) {
	_, _, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	persistCompletedPromotionForTest(t, db, inst, guardianID, rootID, forkID)
	inst.CodexSessionID = forkID
	_, _, beforeRevision, err := db.ReadCodexSessionBinding(inst.ID)
	if err != nil {
		t.Fatalf("read revision before unchanged accept: %v", err)
	}

	for range 3 {
		if changed := inst.acceptCodexSessionID(forkID, false); changed {
			t.Fatal("unchanged promoted target reported a rebind")
		}
	}
	_, _, afterRevision, err := db.ReadCodexSessionBinding(inst.ID)
	if err != nil {
		t.Fatalf("read revision after unchanged accept: %v", err)
	}
	if afterRevision != beforeRevision {
		t.Fatalf("unchanged completed target advanced revision: before=%d after=%d",
			beforeRevision, afterRevision)
	}
}

func TestMetadataReconcileRepairsAnchorForAlreadyPromotedInstance(t *testing.T) {
	_, _, _, inst, _, guardianID, forkID := setupCodexPromotionTest(t)
	inst.CodexSessionID = forkID
	WriteHookSessionAnchor(inst.ID, guardianID)

	if promoted := inst.adoptPersistedCodexPromotion(false); promoted {
		t.Fatal("already-promoted instance reported a new promotion")
	}
	if got := ReadHookSessionAnchor(inst.ID); got != forkID {
		t.Fatalf("metadata reconciliation left stale anchor %q, want %q", got, forkID)
	}
}

func TestBuildCodexCommandUsesPeerPromotedFork(t *testing.T) {
	_, _, _, inst, _, guardianID, forkID := setupCodexPromotionTest(t)

	command := inst.buildCodexCommand("codex")
	if !strings.Contains(command, "resume "+forkID) {
		t.Fatalf("buildCodexCommand() = %q, want resume of peer-promoted fork", command)
	}
	if strings.Contains(command, "fork "+guardianID) {
		t.Fatalf("buildCodexCommand launched a duplicate guardian fork: %q", command)
	}
	if inst.CodexSessionID != forkID || ReadHookSessionAnchor(inst.ID) != forkID {
		t.Fatalf("build did not heal stale snapshot: id=%q anchor=%q",
			inst.CodexSessionID, ReadHookSessionAnchor(inst.ID))
	}
}

func TestBuildCodexCommandReusesCompletedMigrationTargetWhenDBStillStale(t *testing.T) {
	for _, staleBinding := range []string{"source", "old_root"} {
		t.Run(staleBinding, func(t *testing.T) {
			_, _, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
			currentID := guardianID
			if staleBinding == "old_root" {
				currentID = rootID
			}
			seedPersistedCodexBinding(t, db, inst, currentID)
			inst.CodexSessionID = currentID
			WriteHookSessionAnchor(inst.ID, currentID)

			started := time.Now().Add(-time.Second)
			completed := time.Now()
			if err := writeCodexSubagentMigrationState(inst.ID, codexSubagentMigrationState{
				SourceID:  guardianID,
				OldRoot:   rootID,
				Started:   started,
				TargetID:  forkID,
				Completed: completed,
			}); err != nil {
				t.Fatalf("write completed migration sidecar: %v", err)
			}

			command := inst.buildCodexCommand("codex")
			if !strings.Contains(command, "resume "+forkID) {
				t.Fatalf("buildCodexCommand() = %q, want completed target %q", command, forkID)
			}
			if strings.Contains(command, "fork "+guardianID) {
				t.Fatalf("buildCodexCommand launched a duplicate guardian fork: %q", command)
			}
			if inst.CodexSessionID != forkID || ReadHookSessionAnchor(inst.ID) != forkID {
				t.Fatalf("completed target not adopted: id=%q anchor=%q",
					inst.CodexSessionID, ReadHookSessionAnchor(inst.ID))
			}
			if got := readCodexSessionIDFromDB(t, db, inst.ID); got != forkID {
				t.Fatalf("promotion retry left DB at %q, want %q", got, forkID)
			}
			state, ok := readCodexSubagentMigrationState(inst.ID)
			if !ok || state.TargetID != forkID || state.Completed.IsZero() {
				t.Fatalf("completed migration provenance was cleared: state=%+v ok=%v", state, ok)
			}
		})
	}
}

func TestUpdateHookStatusDoesNotUndoPeerPromotion(t *testing.T) {
	_, _, db, inst, rootID, _, forkID := setupCodexPromotionTest(t)

	inst.UpdateHookStatus(&HookStatus{
		Status:    "waiting",
		SessionID: rootID,
		Event:     "turn/completed",
		UpdatedAt: time.Now(),
	})
	if inst.CodexSessionID != forkID {
		t.Fatalf("stale hook replaced promoted fork: got %q want %q", inst.CodexSessionID, forkID)
	}
	if got := readCodexSessionIDFromDB(t, db, inst.ID); got != forkID {
		t.Fatalf("stale hook regressed DB: got %q want %q", got, forkID)
	}
	if got := ReadHookSessionAnchor(inst.ID); got != forkID {
		t.Fatalf("stale hook regressed anchor: got %q want %q", got, forkID)
	}
}

func TestDelayedOldRootHookCannotUndoCompletedPromotion(t *testing.T) {
	_, _, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	persistCompletedPromotionForTest(t, db, inst, guardianID, rootID, forkID)
	inst.CodexSessionID = forkID
	inst.hookStatus = "running"
	inst.hookEvent = "turn/started"
	inst.hookLastUpdate = time.Now().Add(-time.Second)
	WriteHookSessionAnchor(inst.ID, forkID)

	hookPath := hookStatusFilePath(inst.ID)
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o700); err != nil {
		t.Fatalf("mkdir hook dir: %v", err)
	}
	if err := os.WriteFile(hookPath, []byte(`{"status":"waiting","session_id":"`+rootID+`"}`), 0o600); err != nil {
		t.Fatalf("write delayed root hook: %v", err)
	}
	inst.UpdateHookStatus(&HookStatus{
		Status: "waiting", SessionID: rootID, Event: "turn/completed", UpdatedAt: time.Now(),
	})

	if inst.CodexSessionID != forkID || readCodexSessionIDFromDB(t, db, inst.ID) != forkID {
		t.Fatalf("delayed root hook regressed promotion: memory=%q db=%q",
			inst.CodexSessionID, readCodexSessionIDFromDB(t, db, inst.ID))
	}
	if inst.hookStatus != "running" || inst.hookEvent != "turn/started" {
		t.Fatalf("delayed root hook status stuck: status=%q event=%q", inst.hookStatus, inst.hookEvent)
	}
	if got := ReadHookSessionAnchor(inst.ID); got != forkID {
		t.Fatalf("delayed root hook regressed anchor: %q", got)
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatalf("delayed root hook file survived quarantine: %v", err)
	}
}

func TestNewerTopLevelRotationSupersedesCompletedPromotion(t *testing.T) {
	codexHome, cwd, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	persistCompletedPromotionForTest(t, db, inst, guardianID, rootID, forkID)
	inst.CodexSessionID = forkID
	WriteHookSessionAnchor(inst.ID, forkID)
	time.Sleep(time.Millisecond)
	const newerID = "44444444-dddd-4ddd-8ddd-444444444444"
	writeCodexRolloutWithSource(t, codexHome, newerID, cwd, "cli")

	inst.UpdateHookStatus(&HookStatus{
		Status: "running", SessionID: newerID, Event: "turn/started", UpdatedAt: time.Now(),
	})
	if inst.CodexSessionID != newerID || readCodexSessionIDFromDB(t, db, inst.ID) != newerID {
		t.Fatalf("newer top-level rotation rejected: memory=%q db=%q",
			inst.CodexSessionID, readCodexSessionIDFromDB(t, db, inst.ID))
	}
	if _, ok := readCodexSubagentMigrationState(inst.ID); ok {
		t.Fatal("completed migration marker survived a genuine newer rotation")
	}
}

func TestSetFieldRejectsCodexGuardianID(t *testing.T) {
	_, _, _, inst, _, guardianID, forkID := setupCodexPromotionTest(t)
	inst.CodexSessionID = forkID
	if _, _, err := SetField(inst, FieldCodexSessionID, guardianID, nil); err == nil {
		t.Fatal("direct codex-session-id mutator accepted guardian ID")
	}
	if inst.CodexSessionID != forkID {
		t.Fatalf("rejected guardian mutated binding: %q", inst.CodexSessionID)
	}
}

func TestExplicitCodexBindingClearSupersedesCompletedPromotion(t *testing.T) {
	_, _, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	persistCompletedPromotionForTest(t, db, inst, guardianID, rootID, forkID)
	inst.CodexSessionID = forkID
	WriteHookSessionAnchor(inst.ID, forkID)

	if _, _, err := SetField(inst, FieldCodexSessionID, "", nil); err != nil {
		t.Fatalf("SetField clear: %v", err)
	}
	if err := (&Storage{db: db}).SaveWithGroups([]*Instance{inst}, nil); err != nil {
		t.Fatalf("SaveWithGroups clear: %v", err)
	}
	if got := readCodexSessionIDFromDB(t, db, inst.ID); got != "" {
		t.Fatalf("explicit clear was undone by promotion reconciliation: %q", got)
	}
	if ReadHookSessionAnchor(inst.ID) != "" {
		t.Fatal("explicit clear left a stale hook anchor")
	}
	if _, ok := readCodexSubagentMigrationState(inst.ID); ok {
		t.Fatal("explicit clear left completed migration provenance")
	}
}

func TestExplicitCodexBindingClearCannotBeResurrectedByStalePeer(t *testing.T) {
	_, _, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	persistCompletedPromotionForTest(t, db, inst, guardianID, rootID, forkID)
	storage := &Storage{db: db}

	loadOne := func(label string) *Instance {
		t.Helper()
		instances, err := storage.Load()
		if err != nil {
			t.Fatalf("load %s peer: %v", label, err)
		}
		if len(instances) != 1 {
			t.Fatalf("load %s peer returned %d instances, want 1", label, len(instances))
		}
		return instances[0]
	}
	clearingPeer := loadOne("clearing")
	stalePeer := loadOne("stale")
	staleRevision := stalePeer.CodexBindingRevision
	if staleRevision == 0 {
		t.Fatal("promoted binding loaded without a durable revision")
	}

	if _, _, err := SetField(clearingPeer, FieldCodexSessionID, "", nil); err != nil {
		t.Fatalf("SetField clear: %v", err)
	}
	if err := storage.SaveWithGroups([]*Instance{clearingPeer}, nil); err != nil {
		t.Fatalf("save explicit clear: %v", err)
	}
	if clearingPeer.CodexBindingRevision <= staleRevision {
		t.Fatalf("clear revision = %d, want greater than stale revision %d",
			clearingPeer.CodexBindingRevision, staleRevision)
	}

	// This peer was loaded before the clear and now saves unrelated metadata.
	// Its stale fork ID must be replaced in memory and rejected at SQLite.
	stalePeer.Notes = "unrelated edit from stale web process"
	if err := storage.SaveWithGroups([]*Instance{stalePeer}, nil); err != nil {
		t.Fatalf("save stale peer: %v", err)
	}
	if stalePeer.CodexSessionID != "" {
		t.Fatalf("stale peer resurrected fork in memory: %q", stalePeer.CodexSessionID)
	}
	if got := readCodexSessionIDFromDB(t, db, inst.ID); got != "" {
		t.Fatalf("stale peer resurrected fork in SQLite: %q", got)
	}
	if ReadHookSessionAnchor(inst.ID) != "" {
		t.Fatal("stale peer restored the cleared hook anchor")
	}
}

func TestCommittedCodexClearRejectsDelayedOldHook(t *testing.T) {
	_, _, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	persistCompletedPromotionForTest(t, db, inst, guardianID, rootID, forkID)
	inst.CodexSessionID = forkID
	_, _, revision, err := db.ReadCodexSessionBinding(inst.ID)
	if err != nil {
		t.Fatalf("read promoted binding: %v", err)
	}
	inst.CodexBindingRevision = revision

	if _, _, err := SetField(inst, FieldCodexSessionID, "", nil); err != nil {
		t.Fatalf("clear binding: %v", err)
	}
	if err := (&Storage{db: db}).SaveWithGroups([]*Instance{inst}, nil); err != nil {
		t.Fatalf("save clear: %v", err)
	}
	inst.UpdateHookStatus(&HookStatus{
		Status: "waiting", SessionID: forkID, Event: "turn/completed", UpdatedAt: time.Now(),
	})

	if inst.CodexSessionID != "" || readCodexSessionIDFromDB(t, db, inst.ID) != "" {
		t.Fatalf("delayed hook resurrected clear: memory=%q db=%q",
			inst.CodexSessionID, readCodexSessionIDFromDB(t, db, inst.ID))
	}
	if ReadHookSessionAnchor(inst.ID) != "" {
		t.Fatal("delayed hook restored anchor after clear")
	}
}

func TestExplicitCodexOverrideRejectsDelayedPreviouslyNewerHook(t *testing.T) {
	_, _, db, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	persistCompletedPromotionForTest(t, db, inst, guardianID, rootID, forkID)
	inst.CodexSessionID = forkID
	_, _, revision, err := db.ReadCodexSessionBinding(inst.ID)
	if err != nil {
		t.Fatalf("read promoted binding: %v", err)
	}
	inst.CodexBindingRevision = revision

	// The user deliberately selects an older saved thread. Its bind-time floor,
	// not thread chronology alone, must reject a delayed event from the formerly
	// current (chronologically newer) fork.
	if _, _, err := SetField(inst, FieldCodexSessionID, rootID, nil); err != nil {
		t.Fatalf("set explicit older binding: %v", err)
	}
	if err := (&Storage{db: db}).SaveWithGroups([]*Instance{inst}, nil); err != nil {
		t.Fatalf("save explicit override: %v", err)
	}
	inst.UpdateHookStatus(&HookStatus{
		Status: "waiting", SessionID: forkID, Event: "turn/completed", UpdatedAt: time.Now(),
	})

	if inst.CodexSessionID != rootID || readCodexSessionIDFromDB(t, db, inst.ID) != rootID {
		t.Fatalf("delayed newer hook undid explicit override: memory=%q db=%q want=%q",
			inst.CodexSessionID, readCodexSessionIDFromDB(t, db, inst.ID), rootID)
	}
}

func TestPostSaveSyncUsesNewerTargetedBinding(t *testing.T) {
	codexHome, cwd, db, inst, _, _, forkID := setupCodexPromotionTest(t)
	inst.CodexSessionID = forkID
	seedPersistedCodexBinding(t, db, inst, forkID)
	_, _, staleRevision, err := db.ReadCodexSessionBinding(inst.ID)
	if err != nil {
		t.Fatalf("read stale binding: %v", err)
	}
	inst.CodexBindingRevision = staleRevision
	staleToolData := statedb.WriteCodexBindingRevisionToToolData(json.RawMessage(
		`{"codex_session_id":"`+forkID+`","codex_detected_at":1}`), staleRevision)

	const newerID = "99999999-eeee-4eee-8eee-999999999999"
	time.Sleep(time.Millisecond)
	writeCodexRolloutWithSource(t, codexHome, newerID, cwd, "cli")
	newRevision, matched, err := db.WriteCodexSessionBinding(inst.ID, newerID, time.Now(), staleRevision)
	if err != nil || !matched {
		t.Fatalf("write newer targeted binding: revision=%d matched=%v err=%v", newRevision, matched, err)
	}
	WriteHookSessionAnchor(inst.ID, forkID)

	inst.syncCommittedCodexBinding(db, staleToolData, captureCodexBindingSaveSnapshot(inst))
	if inst.CodexSessionID != newerID || inst.CodexBindingRevision != newRevision {
		t.Fatalf("post-save sync regressed newer binding: id=%q revision=%d, want id=%q revision=%d",
			inst.CodexSessionID, inst.CodexBindingRevision, newerID, newRevision)
	}
	if got := ReadHookSessionAnchor(inst.ID); got != newerID {
		t.Fatalf("post-save sync regressed anchor: got %q want %q", got, newerID)
	}
}

func TestPostSaveSyncClearsOverrideIntentForNonCodexTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	inst := NewInstanceWithTool("non-codex-binding", t.TempDir(), "claude")
	inst.codexSessionBindingOverrideIntent = true
	toolData := statedb.WriteCodexBindingRevisionToToolData(json.RawMessage(
		`{"codex_session_id":"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa","codex_detected_at":1}`), 7)

	inst.syncCommittedCodexBinding(nil, toolData, captureCodexBindingSaveSnapshot(inst))
	if inst.codexSessionBindingOverrideIntent {
		t.Fatal("non-Codex post-save sync left override intent sticky")
	}
	if inst.CodexBindingRevision != 7 {
		t.Fatalf("non-Codex post-save revision = %d, want 7", inst.CodexBindingRevision)
	}
}

func TestPostSaveSyncPreservesNewerUncommittedExplicitEdit(t *testing.T) {
	_, _, db, inst, _, _, forkID := setupCodexPromotionTest(t)
	inst.CodexSessionID = forkID
	inst.CodexDetectedAt = time.Now().Add(-time.Second)
	inst.CodexBindingRevision = 1
	inst.codexSessionBindingOverrideIntent = false
	snapshot := captureCodexBindingSaveSnapshot(inst)
	committedToolData := statedb.WriteCodexSessionBindingToToolData(
		json.RawMessage(`{}`), snapshot.sessionID, snapshot.detectedAt, snapshot.revision)

	time.Sleep(time.Millisecond)
	if _, _, err := SetField(inst, FieldCodexSessionID, forkID, nil); err != nil {
		t.Fatalf("repeat explicit edit: %v", err)
	}
	newDetectedAt := inst.CodexDetectedAt
	if !inst.codexSessionBindingOverrideIntent {
		t.Fatal("repeat explicit edit did not establish pending intent")
	}

	inst.syncCommittedCodexBinding(db, committedToolData, snapshot)
	if !inst.CodexDetectedAt.Equal(newDetectedAt) || !inst.codexSessionBindingOverrideIntent {
		t.Fatalf("older post-save sync erased newer edit: detected=%v want=%v intent=%v",
			inst.CodexDetectedAt, newDetectedAt, inst.codexSessionBindingOverrideIntent)
	}
}

func TestCodexHookBindingFloorClassification(t *testing.T) {
	codexHome, cwd, _, inst, rootID, guardianID, forkID := setupCodexPromotionTest(t)
	floorAt := time.Now()
	writeCodexBindingFloorState(inst.ID, forkID, floorAt, 1)

	if CodexHookCandidateRejectedByBindingFloor(inst.ID, forkID, codexHome) {
		t.Fatal("binding floor rejected its current session ID")
	}
	if !CodexHookCandidateRejectedByBindingFloor(inst.ID, rootID, codexHome) {
		t.Fatal("binding floor accepted an older different top-level thread")
	}
	if !CodexHookCandidateRejectedByBindingFloor(inst.ID, guardianID, codexHome) {
		t.Fatal("binding floor accepted an internal guardian")
	}

	time.Sleep(time.Millisecond)
	const newerID = "abababab-eeee-4eee-8eee-abababababab"
	writeCodexRolloutWithSource(t, codexHome, newerID, cwd, "cli")
	if CodexHookCandidateRejectedByBindingFloor(inst.ID, newerID, codexHome) {
		t.Fatal("binding floor rejected a newer top-level thread")
	}

	writeCodexBindingFloorState(inst.ID, "", time.Now(), 2)
	if !CodexHookCandidateRejectedByBindingFloor(inst.ID, guardianID, codexHome) {
		t.Fatal("empty binding floor accepted a guardian")
	}
}

func TestFreshEmptyBindingNeverPersistsGuardianFromTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	_, _, db, inst, _, guardianID, _ := setupCodexPromotionTest(t)
	seedPersistedCodexBinding(t, db, inst, "")
	inst.CodexSessionID = ""
	inst.CodexDetectedAt = time.Time{}
	inst.CodexBindingRevision = 0
	inst.codexSessionBindingOverrideIntent = false
	ClearHookSessionAnchor(inst.ID)
	clearCodexBindingFloorState(inst.ID)

	tmuxName := "agentdeck_guardian_empty_" + strings.ReplaceAll(inst.ID, "-", "_")
	if err := exec.Command("tmux", "new-session", "-d", "-s", tmuxName).Run(); err != nil {
		t.Fatalf("create tmux fixture: %v", err)
	}
	defer func() { _ = exec.Command("tmux", "kill-session", "-t", tmuxName).Run() }()
	if err := exec.Command("tmux", "set-environment", "-t", tmuxName, "CODEX_SESSION_ID", guardianID).Run(); err != nil {
		t.Fatalf("set guardian env: %v", err)
	}
	inst.tmuxSession = tmux.ReconnectSession(tmuxName, inst.Title, inst.ProjectPath, "codex")

	inst.SyncSessionIDsFromTmux()
	if err := (&Storage{db: db}).SaveWithGroups([]*Instance{inst}, nil); err != nil {
		t.Fatalf("save after guardian env sync: %v", err)
	}
	if inst.CodexSessionID != "" || readCodexSessionIDFromDB(t, db, inst.ID) != "" {
		t.Fatalf("guardian became persisted owner: memory=%q db=%q",
			inst.CodexSessionID, readCodexSessionIDFromDB(t, db, inst.ID))
	}
	if envID, err := inst.tmuxSession.GetEnvironment("CODEX_SESSION_ID"); err == nil && envID != "" {
		t.Fatalf("guardian remained in tmux env: %q", envID)
	}
}

func TestSaveWithGroupsDoesNotClobberPeerPromotion(t *testing.T) {
	_, _, db, inst, _, _, forkID := setupCodexPromotionTest(t)
	storage := &Storage{db: db}
	inst.Notes = "unrelated web edit"

	if err := storage.SaveWithGroups([]*Instance{inst}, nil); err != nil {
		t.Fatalf("SaveWithGroups: %v", err)
	}
	if got := readCodexSessionIDFromDB(t, db, inst.ID); got != forkID {
		t.Fatalf("stale whole-row save regressed DB: got %q want %q", got, forkID)
	}
	if inst.CodexSessionID != forkID {
		t.Fatalf("stale in-memory instance was not promoted before save: %q", inst.CodexSessionID)
	}
}

func TestAdoptPersistedCodexPromotionRemovesStaleHookFile(t *testing.T) {
	_, _, _, inst, _, guardianID, _ := setupCodexPromotionTest(t)
	hookPath := hookStatusFilePath(inst.ID)
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o700); err != nil {
		t.Fatalf("mkdir hook dir: %v", err)
	}
	if err := os.WriteFile(hookPath, []byte(`{"status":"waiting","session_id":"`+guardianID+`"}`), 0o600); err != nil {
		t.Fatalf("write stale hook: %v", err)
	}

	if !inst.adoptPersistedCodexPromotion(false) {
		t.Fatal("persisted promotion was not adopted")
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatalf("stale hook file survived promotion: %v", err)
	}
}
