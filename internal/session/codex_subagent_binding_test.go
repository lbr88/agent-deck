package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCodexRolloutWithSource(t *testing.T, codexHome, sessionID, cwd string, source any) string {
	return writeCodexRolloutWithSourceAndRoot(t, codexHome, sessionID, sessionID, cwd, source)
}

func writeCodexRolloutWithSourceAndRoot(t *testing.T, codexHome, sessionID, rootID, cwd string, source any) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "07", "10")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	path := filepath.Join(dir, "rollout-2026-07-10T12-00-00-"+sessionID+".jsonl")
	record := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"type":      "session_meta",
		"payload": map[string]any{
			"id":         sessionID,
			"session_id": rootID,
			"cwd":        cwd,
			"source":     source,
		},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal rollout metadata: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return path
}

func TestUpdateHookStatusCodexRejectsGuardianSubagentID(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	const mainID = "11111111-1111-1111-1111-111111111111"
	const guardianID = "22222222-2222-2222-2222-222222222222"
	writeCodexRolloutWithSource(t, codexHome, mainID, cwd, "cli")
	writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, "", cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	inst := NewInstanceWithTool("main", cwd, "codex")
	inst.CodexSessionID = mainID
	inst.hookStatus = "running"
	inst.hookEvent = "turn/started"
	inst.hookLastUpdate = time.Now().Add(-time.Second)

	inst.UpdateHookStatus(&HookStatus{
		Status:    "waiting",
		SessionID: guardianID,
		Event:     "turn/completed",
		UpdatedAt: time.Now(),
	})

	if inst.CodexSessionID != mainID {
		t.Fatalf("guardian hook replaced top-level Codex ID: got %q, want %q", inst.CodexSessionID, mainID)
	}
	if inst.hookStatus != "running" || inst.hookEvent != "turn/started" {
		t.Fatalf("guardian hook changed parent status: status=%q event=%q", inst.hookStatus, inst.hookEvent)
	}
}

func TestSelectCodexRolloutCandidatePrefersTopLevelThread(t *testing.T) {
	codexHome := t.TempDir()
	cwd := t.TempDir()
	const mainID = "33333333-3333-3333-3333-333333333333"
	const guardianID = "44444444-4444-4444-4444-444444444444"
	mainPath := writeCodexRolloutWithSource(t, codexHome, mainID, cwd, "cli")
	guardianPath := writeCodexRolloutWithSource(t, codexHome, guardianID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	got := selectCodexRolloutCandidate([]codexRolloutCandidate{
		{ID: guardianID, Path: guardianPath},
		{ID: mainID, Path: mainPath},
	})
	if got != mainID {
		t.Fatalf("selected rollout = %q, want top-level %q", got, mainID)
	}
}

func TestSelectCodexRolloutCandidateRejectsOnlySubagent(t *testing.T) {
	codexHome := t.TempDir()
	cwd := t.TempDir()
	const rootID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const guardianID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	guardianPath := writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, rootID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	if got := selectCodexRolloutCandidate([]codexRolloutCandidate{{ID: guardianID, Path: guardianPath}}); got != "" {
		t.Fatalf("selected internal-only rollout %q", got)
	}
}

func TestSelectCodexRolloutCandidateHandlesWhitespaceInHome(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "Codex Data")
	cwd := t.TempDir()
	const mainID = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	const guardianID = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	mainPath := writeCodexRolloutWithSource(t, codexHome, mainID, cwd, "cli")
	guardianPath := writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, mainID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	output := []byte(guardianPath + "\n" + mainPath + "\n")
	if got := selectCodexRolloutCandidate(extractCodexRolloutCandidatesFromOutput(output)); got != mainID {
		t.Fatalf("selected rollout = %q, want whitespace-path top-level %q", got, mainID)
	}
}

func TestSandboxCodexCandidatesRebaseToHostMount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	const mainID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	const guardianID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	inst := NewInstanceWithTool("sandbox", cwd, "codex")
	inst.Sandbox = NewSandboxConfig("")
	hostCodexHome := filepath.Join(home, ".codex", "sandbox")
	mainPath := writeCodexRolloutWithSource(t, hostCodexHome, mainID, cwd, "cli")
	guardianPath := writeCodexRolloutWithSourceAndRoot(t, hostCodexHome, guardianID, mainID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	containerOutput := []byte(strings.Replace(guardianPath, hostCodexHome, "/root/.codex", 1) + "\n" +
		strings.Replace(mainPath, hostCodexHome, "/root/.codex", 1) + "\n")
	candidates := extractCodexRolloutCandidatesFromOutput(containerOutput)
	candidates = rebaseCodexRolloutCandidates(candidates, inst.getCodexHomeDir())
	if got := selectCodexRolloutCandidate(candidates); got != mainID {
		t.Fatalf("selected sandbox rollout = %q, want top-level %q", got, mainID)
	}
}

func TestQueryCodexSessionSkipsNewerSubagentRollout(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	const mainID = "55555555-5555-5555-5555-555555555555"
	const workerID = "66666666-6666-6666-6666-666666666666"
	mainPath := writeCodexRolloutWithSource(t, codexHome, mainID, cwd, "cli")
	workerPath := writeCodexRolloutWithSource(t, codexHome, workerID, cwd, map[string]any{
		"subagent": map[string]any{"thread_spawn": map[string]any{"depth": 1}},
	})
	now := time.Now()
	if err := os.Chtimes(mainPath, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("chtimes main rollout: %v", err)
	}
	if err := os.Chtimes(workerPath, now, now); err != nil {
		t.Fatalf("chtimes worker rollout: %v", err)
	}

	inst := NewInstanceWithTool("main", cwd, "codex")
	if got := inst.queryCodexSession(nil, false); got != mainID {
		t.Fatalf("queryCodexSession() = %q, want top-level %q", got, mainID)
	}
}

func TestListCodexIndexExcludesSubagentThreads(t *testing.T) {
	codexHome := t.TempDir()
	cwd := t.TempDir()
	const mainID = "56565656-5656-5656-5656-565656565656"
	const guardianID = "78787878-7878-7878-7878-787878787878"
	writeCodexRolloutWithSource(t, codexHome, mainID, cwd, "cli")
	writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, mainID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	var indexData []byte
	for _, id := range []string{mainID, guardianID} {
		line, err := json.Marshal(map[string]any{
			"id": id, "thread_name": id, "cwd": cwd, "updated_at": time.Now().Format(time.RFC3339Nano),
		})
		if err != nil {
			t.Fatalf("marshal index: %v", err)
		}
		indexData = append(indexData, append(line, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "session_index.jsonl"), indexData, 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	entries, err := ListCodexIndex(codexHome)
	if err != nil {
		t.Fatalf("ListCodexIndex: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != mainID {
		t.Fatalf("index entries = %+v, want only top-level %s", entries, mainID)
	}
}

func TestBuildCodexCommandNeverResumesSubagentThread(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	const guardianID = "77777777-7777-7777-7777-777777777777"
	writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, "", cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	inst := NewInstanceWithTool("main", cwd, "codex")
	inst.CodexSessionID = guardianID
	command := inst.buildCodexCommand("codex")

	if strings.Contains(command, "resume "+guardianID) {
		t.Fatalf("buildCodexCommand resumed internal subagent: %q", command)
	}
	if !strings.Contains(command, "fork "+guardianID) {
		t.Fatalf("buildCodexCommand did not migrate internal subagent via fork: %q", command)
	}
	if inst.CodexSessionID != guardianID {
		t.Fatalf("migration source was not retained for retry: %q", inst.CodexSessionID)
	}
}

func TestBuildCodexCommandForksFullSubagentTranscriptInsteadOfOldRoot(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	const mainID = "88888888-8888-8888-8888-888888888888"
	const guardianID = "99999999-9999-9999-9999-999999999999"
	writeCodexRolloutWithSource(t, codexHome, mainID, cwd, "cli")
	writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, mainID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	inst := NewInstanceWithTool("main", cwd, "codex")
	inst.CodexSessionID = guardianID
	command := inst.buildCodexCommand("codex")

	if !strings.Contains(command, "fork "+guardianID) {
		t.Fatalf("buildCodexCommand() = %q, want full-history fork from %q", command, guardianID)
	}
	if strings.Contains(command, "resume "+mainID) {
		t.Fatalf("buildCodexCommand silently abandoned child history for old root: %q", command)
	}
	if inst.codexSubagentMigrationSourceID != guardianID {
		t.Fatalf("migration source = %q, want %q", inst.codexSubagentMigrationSourceID, guardianID)
	}
	if !inst.codexSubagentMigrationStartedAt.IsZero() {
		t.Fatal("migration was marked started before tmux launch succeeded")
	}
}

func TestCodexSubagentMigrationAcceptsOnlyFreshFork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	const oldRootID = "10101010-1010-1010-1010-101010101010"
	const guardianID = "20202020-2020-2020-2020-202020202020"
	const freshForkID = "30303030-3030-3030-3030-303030303030"
	oldRootPath := writeCodexRolloutWithSource(t, codexHome, oldRootID, cwd, "cli")
	guardianPath := writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, oldRootID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	inst := NewInstanceWithTool("main", cwd, "codex")
	inst.CodexSessionID = guardianID
	_ = inst.buildCodexCommand("codex")
	if inst.isFreshCodexMigrationTarget(oldRootID) {
		t.Fatal("old root accepted before migration launch")
	}
	inst.markCodexSubagentMigrationStarted()
	if inst.isFreshCodexMigrationTarget(oldRootID) {
		t.Fatal("abandoned old root accepted as migration target")
	}

	freshForkPath := writeCodexRolloutWithSource(t, codexHome, freshForkID, cwd, "cli")
	now := time.Now()
	if err := os.Chtimes(oldRootPath, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("age old root: %v", err)
	}
	if err := os.Chtimes(freshForkPath, now, now); err != nil {
		t.Fatalf("touch fresh fork: %v", err)
	}
	if !inst.isFreshCodexMigrationTarget(freshForkID) {
		t.Fatal("fresh top-level fork was not accepted as migration target")
	}
	// A short-lived CLI process exits after launch. A freshly-loaded instance
	// must recover the migration boundary from the cross-process sidecar.
	reloaded := NewInstanceWithTool("main", cwd, "codex")
	reloaded.ID = inst.ID
	reloaded.CodexSessionID = guardianID
	if !reloaded.isFreshCodexMigrationTarget(freshForkID) {
		t.Fatal("fresh fork was not recognized after cross-process reload")
	}
	if reloaded.isFreshCodexMigrationTarget(oldRootID) {
		t.Fatal("cross-process reload accepted abandoned old root")
	}
	fallback := NewInstanceWithTool("main", cwd, "codex")
	fallback.CodexSessionID = guardianID
	fallback.LastStartedAt = inst.codexSubagentMigrationStartedAt
	if !fallback.isFreshCodexMigrationTarget(freshForkID) {
		t.Fatal("LastStartedAt fallback did not recover migration boundary")
	}
	if fallback.isFreshCodexMigrationTarget(oldRootID) {
		t.Fatal("LastStartedAt fallback accepted abandoned old root")
	}
	got := selectCodexRolloutCandidate([]codexRolloutCandidate{
		{ID: oldRootID, Path: oldRootPath},
		{ID: guardianID, Path: guardianPath},
		{ID: freshForkID, Path: freshForkPath},
	})
	if got != freshForkID {
		t.Fatalf("process probe selected %q, want fresh fork %q", got, freshForkID)
	}
	reloaded.acceptCodexSessionID(freshForkID, false)
	completed, ok := readCodexSubagentMigrationState(inst.ID)
	if !ok || completed.TargetID != freshForkID || completed.Completed.IsZero() {
		t.Fatalf("completed migration provenance was not retained: %+v ok=%v", completed, ok)
	}
}

func TestTransitionDaemonQuarantinesPersistedCodexSubagentHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	const rootID = "12121212-1212-1212-1212-121212121212"
	const guardianID = "34343434-3434-3434-3434-343434343434"
	writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, rootID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	inst := NewInstanceWithTool("main", cwd, "codex")
	hook := &HookStatus{Status: "waiting", SessionID: guardianID, Event: "turn/completed", UpdatedAt: time.Now()}
	hookDir := GetHooksDir()
	if err := os.MkdirAll(hookDir, 0o700); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	hookPath := filepath.Join(hookDir, inst.ID+".json")
	hookJSON, err := json.Marshal(map[string]any{
		"status": "waiting", "session_id": guardianID, "event": "turn/completed", "ts": time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("marshal hook: %v", err)
	}
	if err := os.WriteFile(hookPath, hookJSON, 0o600); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	watcher := &StatusFileWatcher{statuses: map[string]*HookStatus{inst.ID: hook}}
	daemon := &TransitionDaemon{hookWatcher: watcher}
	if got := daemon.hookStatusForInstance(inst); got != nil {
		t.Fatalf("guardian hook remained authoritative: %+v", got)
	}
	if watcher.GetHookStatus(inst.ID) != nil {
		t.Fatal("guardian hook remained in watcher cache")
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatalf("poisoned hook file was not removed: %v", err)
	}
}

func TestTransitionDaemonQuarantinesOldRootHookForLegacySubagentBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	const rootID = "45454545-4545-4545-4545-454545454545"
	const guardianID = "67676767-6767-6767-6767-676767676767"
	writeCodexRolloutWithSource(t, codexHome, rootID, cwd, "cli")
	writeCodexRolloutWithSourceAndRoot(t, codexHome, guardianID, rootID, cwd, map[string]any{
		"subagent": map[string]any{"other": "guardian"},
	})

	inst := NewInstanceWithTool("main", cwd, "codex")
	inst.CodexSessionID = guardianID
	hook := &HookStatus{Status: "waiting", SessionID: rootID, Event: "turn/completed", UpdatedAt: time.Now()}
	hookDir := GetHooksDir()
	if err := os.MkdirAll(hookDir, 0o700); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	hookPath := filepath.Join(hookDir, inst.ID+".json")
	hookJSON, err := json.Marshal(map[string]any{
		"status": "waiting", "session_id": rootID, "event": "turn/completed", "ts": time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("marshal hook: %v", err)
	}
	if err := os.WriteFile(hookPath, hookJSON, 0o600); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	watcher := &StatusFileWatcher{statuses: map[string]*HookStatus{inst.ID: hook}}
	daemon := &TransitionDaemon{hookWatcher: watcher}
	if got := daemon.hookStatusForInstance(inst); got != nil {
		t.Fatalf("old-root hook remained authoritative for hidden guardian: %+v", got)
	}
	if watcher.GetHookStatus(inst.ID) != nil {
		t.Fatal("old-root hook remained in watcher cache")
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatalf("old-root hook file was not removed: %v", err)
	}
}

func TestSandboxHookQuarantineRemovesScopedAndLegacyFlatFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := NewInstanceWithTool("sandbox", t.TempDir(), "codex")
	inst.Sandbox = &SandboxConfig{Enabled: true}
	hooksDir := GetHooksDir()
	flatPath := filepath.Join(hooksDir, inst.ID+".json")
	scopedPath := filepath.Join(hooksDir, "sandbox", inst.ID, inst.ID+".json")
	if err := os.MkdirAll(filepath.Dir(scopedPath), 0o700); err != nil {
		t.Fatalf("mkdir scoped hooks: %v", err)
	}
	for _, path := range []string{flatPath, scopedPath} {
		if err := os.WriteFile(path, []byte(`{"status":"waiting"}`), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	inst.removePersistedHookStatusFile()
	for _, path := range []string{flatPath, scopedPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("quarantine left %s: %v", path, err)
		}
	}
}
