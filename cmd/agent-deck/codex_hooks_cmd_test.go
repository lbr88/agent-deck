package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestMapCodexNotifyToStatus(t *testing.T) {
	tests := []struct {
		event  string
		expect string
	}{
		{"agent-turn-complete", "waiting"},
		{"agent-turn-start", "running"},
		{"AGENT-TURN-COMPLETE", "waiting"},
		{"turn/completed", "waiting"},
		{"turn/started", "running"},
		{"turn.completed", "waiting"},
		{"turn.started", "running"},
		{"turn.failed", "waiting"},
		{"thread.started", "waiting"},
		{"foo turn start bar", "running"},
		{"foo turn complete bar", "waiting"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			got := mapCodexNotifyToStatus(tt.event)
			if got != tt.expect {
				t.Fatalf("mapCodexNotifyToStatus(%q) = %q, want %q", tt.event, got, tt.expect)
			}
		})
	}
}

func TestHandleCodexNotify_WritesStatus(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("AGENTDECK_INSTANCE_ID", "inst-1")
	t.Setenv("CODEX_SESSION_ID", "")

	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"agent-deck", "codex-notify"}

	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_, _ = w.WriteString(`{"type":"agent-turn-complete","session_id":"abc-123"}`)
	_ = w.Close()
	os.Stdin = r

	handleCodexNotify()

	hookPath := filepath.Join(getHooksDir(), "inst-1.json")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook file: %v", err)
	}
	var hook hookStatusFile
	if err := json.Unmarshal(data, &hook); err != nil {
		t.Fatalf("unmarshal hook: %v", err)
	}
	if hook.Status != "waiting" {
		t.Fatalf("hook status = %q, want waiting", hook.Status)
	}
	if hook.SessionID != "abc-123" {
		t.Fatalf("hook session_id = %q, want abc-123", hook.SessionID)
	}
}

func TestHandleCodexNotify_ArgPayload(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("AGENTDECK_INSTANCE_ID", "inst-arg")
	t.Setenv("CODEX_SESSION_ID", "")

	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"agent-deck", "codex-notify", `{"event":"turn/completed","thread_id":"thr-1"}`}

	handleCodexNotify()

	hookPath := filepath.Join(getHooksDir(), "inst-arg.json")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook file: %v", err)
	}
	var hook hookStatusFile
	if err := json.Unmarshal(data, &hook); err != nil {
		t.Fatalf("unmarshal hook: %v", err)
	}
	if hook.Status != "waiting" {
		t.Fatalf("hook status = %q, want waiting", hook.Status)
	}
	if hook.SessionID != "thr-1" {
		t.Fatalf("hook session_id = %q, want thr-1", hook.SessionID)
	}
}

func TestHandleCodexNotify_JSONRPCMethodPayload(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("AGENTDECK_INSTANCE_ID", "inst-method")
	t.Setenv("CODEX_SESSION_ID", "")

	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"agent-deck", "codex-notify", `{"method":"turn/completed","params":{"thread_id":"thr-42"}}`}

	handleCodexNotify()

	hookPath := filepath.Join(getHooksDir(), "inst-method.json")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook file: %v", err)
	}
	var hook hookStatusFile
	if err := json.Unmarshal(data, &hook); err != nil {
		t.Fatalf("unmarshal hook: %v", err)
	}
	if hook.Status != "waiting" {
		t.Fatalf("hook status = %q, want waiting", hook.Status)
	}
	if hook.SessionID != "thr-42" {
		t.Fatalf("hook session_id = %q, want thr-42", hook.SessionID)
	}
}

func TestHandleCodexNotify_IgnoresGuardianSubagent(t *testing.T) {
	tmpHome := t.TempDir()
	codexHome := filepath.Join(tmpHome, ".codex")
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("AGENTDECK_INSTANCE_ID", "inst-guardian")
	t.Setenv("CODEX_SESSION_ID", "")

	const rootID = "11111111-1111-1111-1111-111111111111"
	const guardianID = "22222222-2222-2222-2222-222222222222"
	const newerRootID = "33333333-3333-3333-3333-333333333333"
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "07", "10")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	guardianMeta := map[string]any{
		"type": "session_meta",
		"payload": map[string]any{
			"id":         guardianID,
			"session_id": rootID,
			"source": map[string]any{
				"subagent": map[string]any{"other": "guardian"},
			},
		},
	}
	data, err := json.Marshal(guardianMeta)
	if err != nil {
		t.Fatalf("marshal guardian rollout: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-07-10T12-00-00-"+guardianID+".jsonl")
	if err := os.WriteFile(rolloutPath, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write guardian rollout: %v", err)
	}

	writeHookStatus("inst-guardian", "running", rootID, "turn/started")
	// Model a late guardian from rootID completing after the interactive thread
	// has already rotated and established a newer sticky anchor.
	session.WriteHookSessionAnchor("inst-guardian", newerRootID)
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"agent-deck", "codex-notify",
		`{"event":"turn/completed","thread_id":"` + guardianID + `"}`}

	handleCodexNotify()

	hookPath := filepath.Join(getHooksDir(), "inst-guardian.json")
	hookData, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook file: %v", err)
	}
	var hook hookStatusFile
	if err := json.Unmarshal(hookData, &hook); err != nil {
		t.Fatalf("unmarshal hook: %v", err)
	}
	if hook.SessionID != rootID || hook.Status != "running" || hook.Event != "turn/started" {
		t.Fatalf("guardian event replaced parent hook: %+v", hook)
	}
	if got := session.ReadHookSessionAnchor("inst-guardian"); got != newerRootID {
		t.Fatalf("late guardian replaced newer hook anchor: got %q, want %q", got, newerRootID)
	}
}

func TestHandleCodexNotify_GuardianBootstrapUsesSharedLock(t *testing.T) {
	tmpHome := t.TempDir()
	codexHome := filepath.Join(tmpHome, ".codex")
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("AGENTDECK_INSTANCE_ID", "inst-guardian-lock")
	t.Setenv("CODEX_SESSION_ID", "")

	const rootID = "61111111-1111-4111-8111-111111111111"
	const guardianID = "62222222-2222-4222-8222-222222222222"
	const promotedID = "63333333-3333-4333-8333-333333333333"
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "07", "10")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	guardianMeta, err := json.Marshal(map[string]any{
		"type": "session_meta",
		"payload": map[string]any{
			"id": guardianID, "session_id": rootID,
			"source": map[string]any{"subagent": map[string]any{"other": "guardian"}},
		},
	})
	if err != nil {
		t.Fatalf("marshal guardian rollout: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-07-10T12-00-00-"+guardianID+".jsonl")
	if err := os.WriteFile(rolloutPath, append(guardianMeta, '\n'), 0o600); err != nil {
		t.Fatalf("write guardian rollout: %v", err)
	}

	release, err := session.AcquireHookSessionLock("inst-guardian-lock")
	if err != nil {
		t.Fatalf("acquire shared hook lock: %v", err)
	}
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"agent-deck", "codex-notify",
		`{"event":"turn/completed","thread_id":"` + guardianID + `"}`}

	done := make(chan struct{})
	go func() {
		handleCodexNotify()
		close(done)
	}()
	select {
	case <-done:
		release()
		t.Fatal("guardian notify did not wait for shared hook lock")
	case <-time.After(100 * time.Millisecond):
	}

	// Model the runtime completing a promotion while it owns the shared lock.
	// The guardian must recheck after acquiring the lock and preserve this ID.
	session.WriteHookSessionAnchor("inst-guardian-lock", promotedID)
	release()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("guardian notify remained blocked after shared lock release")
	}
	if got := session.ReadHookSessionAnchor("inst-guardian-lock"); got != promotedID {
		t.Fatalf("guardian replaced promoted anchor: got %q, want %q", got, promotedID)
	}
}

func TestHandleCodexNotify_IgnoresDelayedOlderTopLevel(t *testing.T) {
	tmpHome := t.TempDir()
	codexHome := filepath.Join(tmpHome, ".codex")
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("AGENTDECK_INSTANCE_ID", "inst-old-root")
	t.Setenv("CODEX_SESSION_ID", "")

	const (
		oldRootID = "44444444-4444-4444-8444-444444444444"
		forkID    = "55555555-5555-4555-8555-555555555555"
	)
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "07", "10")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	for _, rec := range []struct {
		id, timestamp, filenameTime string
	}{
		{oldRootID, "2026-07-10T10:00:00Z", "10-00-00"},
		{forkID, "2026-07-10T11:00:00Z", "11-00-00"},
	} {
		data, err := json.Marshal(map[string]any{
			"timestamp": rec.timestamp,
			"type":      "session_meta",
			"payload": map[string]any{
				"id": rec.id, "session_id": rec.id, "source": "cli",
			},
		})
		if err != nil {
			t.Fatalf("marshal rollout: %v", err)
		}
		path := filepath.Join(rolloutDir, "rollout-2026-07-10T"+rec.filenameTime+"-"+rec.id+".jsonl")
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatalf("write rollout: %v", err)
		}
	}

	writeHookStatus("inst-old-root", "running", forkID, "turn/started")
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"agent-deck", "codex-notify",
		`{"event":"turn/completed","thread_id":"` + oldRootID + `"}`}

	handleCodexNotify()

	hookPath := filepath.Join(getHooksDir(), "inst-old-root.json")
	hookData, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	var hook hookStatusFile
	if err := json.Unmarshal(hookData, &hook); err != nil {
		t.Fatalf("unmarshal hook: %v", err)
	}
	if hook.SessionID != forkID || hook.Status != "running" {
		t.Fatalf("delayed old root replaced promoted hook: %+v", hook)
	}
	if got := session.ReadHookSessionAnchor("inst-old-root"); got != forkID {
		t.Fatalf("delayed old root replaced promoted anchor: %q", got)
	}
}

func TestHandleCodexNotify_RejectsCandidateBelowEmptyBindingFloor(t *testing.T) {
	tmpHome := t.TempDir()
	codexHome := filepath.Join(tmpHome, ".codex")
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("AGENTDECK_INSTANCE_ID", "inst-clear-floor")
	t.Setenv("CODEX_SESSION_ID", "")
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	const oldID = "66666666-6666-4666-8666-666666666666"
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "07", "10")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	meta, _ := json.Marshal(map[string]any{
		"timestamp": "2026-07-10T10:00:00Z",
		"type":      "session_meta",
		"payload":   map[string]any{"id": oldID, "session_id": oldID, "source": "cli"},
	})
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-07-10T10-00-00-"+oldID+".jsonl")
	if err := os.WriteFile(rolloutPath, append(meta, '\n'), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	inst := session.NewInstanceWithTool("clear-floor", t.TempDir(), "codex")
	inst.ID = "inst-clear-floor"
	if _, _, err := session.SetField(inst, session.FieldCodexSessionID, "", nil); err != nil {
		t.Fatalf("create empty binding floor: %v", err)
	}
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"agent-deck", "codex-notify",
		`{"event":"turn/completed","thread_id":"` + oldID + `"}`}

	handleCodexNotify()

	if got := session.ReadHookSessionAnchor("inst-clear-floor"); got != "" {
		t.Fatalf("old candidate crossed empty binding floor into anchor: %q", got)
	}
	if _, err := os.Stat(filepath.Join(getHooksDir(), "inst-clear-floor.json")); !os.IsNotExist(err) {
		t.Fatalf("old candidate crossed empty binding floor into hook JSON: %v", err)
	}
}

func TestHandleCodexNotify_EmptyTailEventKeepsJSONEmptyAndPersistsAnchor(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("AGENTDECK_INSTANCE_ID", "inst-sticky")
	t.Setenv("CODEX_SESSION_ID", "")

	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Seed sticky mapping with a thread_id-bearing event.
	os.Args = []string{"agent-deck", "codex-notify", `{"event":"turn/started","thread_id":"thr-sticky"}`}
	handleCodexNotify()

	// Tail event has no session_id/thread_id; should backfill from sticky store.
	os.Args = []string{"agent-deck", "codex-notify", `{"event":"turn/completed"}`}
	handleCodexNotify()

	hookPath := filepath.Join(getHooksDir(), "inst-sticky.json")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook file: %v", err)
	}
	var hook hookStatusFile
	if err := json.Unmarshal(data, &hook); err != nil {
		t.Fatalf("unmarshal hook: %v", err)
	}
	if hook.SessionID != "" {
		t.Fatalf("hook session_id = %q, want empty for compatibility", hook.SessionID)
	}
	if got := session.ReadHookSessionAnchor("inst-sticky"); got != "thr-sticky" {
		t.Fatalf("session anchor = %q, want thr-sticky", got)
	}
}

func TestCodexHooksInstallUninstall(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", filepath.Join(tmpHome, ".codex"))

	handleCodexHooksInstall()

	configPath := getCodexConfigPath()
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, codexNotifyMarkerBegin) {
		t.Fatalf("config missing marker begin")
	}
	if !strings.Contains(text, codexNotifyLine) {
		t.Fatalf("config missing notify line")
	}
	if !strings.Contains(text, "# BEGIN AGENTDECK CODEX HUB CONTEXT") {
		t.Fatalf("config missing context hook marker")
	}
	if !strings.Contains(text, `[[hooks.SessionStart]]`) ||
		!strings.Contains(text, `matcher = "startup|resume|clear|compact"`) ||
		!strings.Contains(text, `command = "agent-deck agent-context --format hook-json"`) ||
		!strings.Contains(text, `statusMessage = "Loading Agent Deck hub context"`) {
		t.Fatalf("config missing native Codex context hooks:\n%s", text)
	}
	if strings.Contains(text, `hooks.UserPromptSubmit`) {
		t.Fatalf("context hook must not run on every user prompt:\n%s", text)
	}

	handleCodexHooksUninstall()

	content, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after uninstall: %v", err)
	}
	text = string(content)
	if strings.Contains(text, codexNotifyMarkerBegin) {
		t.Fatalf("expected codex notify block removed, got: %q", text)
	}
	if strings.Contains(text, "AGENTDECK CODEX HUB CONTEXT") || strings.Contains(text, "agent-context") {
		t.Fatalf("expected codex context hook block removed, got: %q", text)
	}
}

func TestCodexHooksStatusPartialWhenContextMissing(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", filepath.Join(tmpHome, ".codex"))

	configPath := getCodexConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := codexNotifyMarkerBegin + "\n" + codexNotifyLine + "\n" + codexNotifyMarkerEnd + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	out := captureStdout(t, handleCodexHooksStatus)
	if !strings.Contains(out, "Status: PARTIAL") || !strings.Contains(out, "Run 'agent-deck codex-hooks install'") {
		t.Fatalf("status output = %q, want PARTIAL with install guidance", out)
	}
}

func TestCodexHooksInstall_UpgradesPromptSubmitContextBlock(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", filepath.Join(tmpHome, ".codex"))

	configPath := getCodexConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := codexNotifyBlock() + "\n" +
		codexContextMarkerBegin + "\n" +
		`[[hooks.UserPromptSubmit]]

[[hooks.UserPromptSubmit.hooks]]
type = "command"
command = "agent-deck agent-context --format hook-json"
timeout = 5` + "\n" +
		codexContextMarkerEnd + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	handleCodexHooksInstall()

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "hooks.UserPromptSubmit") {
		t.Fatalf("stale prompt-submit context hook was not removed:\n%s", text)
	}
	if !strings.Contains(text, "[[hooks.SessionStart]]") || !strings.Contains(text, codexContextHookCommand) {
		t.Fatalf("session-start context hook missing after upgrade:\n%s", text)
	}
}

func TestCodexHooksInstall_UpgradesLegacyTableWithoutMarkers(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", filepath.Join(tmpHome, ".codex"))

	configPath := getCodexConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := "model = \"gpt-5\"\n\n[notify]\nprogram = [\"agent-deck\", \"codex-notify\"]\n"
	if err := os.WriteFile(configPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	handleCodexHooksInstall()

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, codexNotifyMarkerBegin) || !strings.Contains(text, codexNotifyLine) {
		t.Fatalf("expected agent-deck notify block after upgrade, got: %q", text)
	}
	if strings.Contains(text, "[notify]") || strings.Contains(text, "program =") {
		t.Fatalf("expected legacy notify table removed, got: %q", text)
	}
}

func TestCodexHooksInstall_UpgradesLegacyMarkerBlock(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", filepath.Join(tmpHome, ".codex"))

	configPath := getCodexConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := codexNotifyMarkerBegin + "\n[notify]\nprogram = [\"agent-deck\", \"codex-notify\"]\n" + codexNotifyMarkerEnd + "\n"
	if err := os.WriteFile(configPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	handleCodexHooksInstall()

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, codexNotifyLine) {
		t.Fatalf("expected upgraded notify line, got: %q", text)
	}
	if strings.Contains(text, "[notify]") || strings.Contains(text, "program =") {
		t.Fatalf("expected legacy notify format removed, got: %q", text)
	}
}

func TestGetCodexConfigPath_UsesCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex-home"))

	got := getCodexConfigPath()
	if !strings.HasSuffix(got, filepath.Join("codex-home", "config.toml")) {
		t.Fatalf("getCodexConfigPath() = %q, expected suffix codex-home/config.toml", got)
	}
}
