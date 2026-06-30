package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHandleSessionImportCodexCreatesStoppedSession(t *testing.T) {
	profile := codexImportTestProfile(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	id := "33333333-3333-3333-3333-333333333333"
	writeCodexIndexForCLI(t, home, id, "existing codex", "2026-06-30T10:00:00Z")
	writeCodexRolloutForCLI(t, home, id)
	projectPath := t.TempDir()

	handleSessionImportCodex(profile, []string{id, "--title", "imported", "--path", projectPath, "--quiet"})

	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances=%d, want 1", len(instances))
	}
	got := instances[0]
	if got.Tool != "codex" || got.CodexSessionID != id || got.Title != "imported" || got.Status != session.StatusStopped {
		t.Fatalf("bad imported instance: %#v", got)
	}
	if got.ProjectPath != projectPath {
		t.Fatalf("project path = %q, want %q", got.ProjectPath, projectPath)
	}
	if got.GroupPath != session.DefaultGroupPath {
		t.Fatalf("group path = %q, want %q", got.GroupPath, session.DefaultGroupPath)
	}
}

func TestHandleSessionImportCodexDefaultsTitleAndOutputsJSON(t *testing.T) {
	profile := codexImportTestProfile(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	id := "44444444-4444-4444-4444-444444444444"
	writeCodexIndexForCLI(t, home, id, "existing codex", "2026-06-30T10:00:00Z")
	writeCodexRolloutForCLI(t, home, id)
	projectPath := t.TempDir()

	stdout := captureStdoutForCodexImport(t, func() {
		handleSessionImportCodex(profile, []string{"existing codex", "--path", projectPath, "--command", "codex-nightly", "--json"})
	})

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json output %q failed to unmarshal: %v", stdout, err)
	}
	if payload["success"] != true || payload["title"] != "existing codex" || payload["codexSessionId"] != id {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}

	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances=%d, want 1", len(instances))
	}
	got := instances[0]
	if got.Title != "existing codex" || got.Command != "codex-nightly" {
		t.Fatalf("bad imported instance defaults: %#v", got)
	}
}

func TestHandleSessionImportCodexUsesCommandCodexHome(t *testing.T) {
	profile := codexImportTestProfile(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	id := "55555555-5555-5555-5555-555555555555"
	writeCodexIndexForCLI(t, home, id, "command home", "2026-06-30T10:00:00Z")
	writeCodexRolloutForCLI(t, home, id)
	command := "CODEX_HOME=" + home + " codex"

	handleSessionImportCodex(profile, []string{id, "--command", command, "--path", t.TempDir(), "--quiet"})

	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances=%d, want 1", len(instances))
	}
	if instances[0].Command != command || instances[0].CodexSessionID != id {
		t.Fatalf("bad imported command-home instance: %#v", instances[0])
	}
}

func TestHandleSessionImportCodexRejectsAmbiguousName(t *testing.T) {
	if os.Getenv("AGENT_DECK_IMPORT_CODEX_HELPER") == "ambiguous" {
		handleSessionImportCodex(os.Getenv("AGENT_DECK_IMPORT_CODEX_PROFILE"), []string{"same name"})
		return
	}

	profile := codexImportTestProfile(t)
	home := t.TempDir()
	idA := "66666666-6666-6666-6666-666666666666"
	idB := "77777777-7777-7777-7777-777777777777"
	writeCodexIndexForCLI(t, home, idA, "same name", "2026-06-30T10:00:00Z")
	appendCodexIndexForCLI(t, home, idB, "same name", "2026-06-30T11:00:00Z")
	writeCodexRolloutForCLI(t, home, idA)
	writeCodexRolloutForCLI(t, home, idB)

	cmd := exec.Command(os.Args[0], "-test.run=TestHandleSessionImportCodexRejectsAmbiguousName")
	cmd.Env = append(os.Environ(),
		"AGENT_DECK_IMPORT_CODEX_HELPER=ambiguous",
		"AGENT_DECK_IMPORT_CODEX_PROFILE="+profile,
		"CODEX_HOME="+home,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("ambiguous import unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "ambiguous") {
		t.Fatalf("output %q does not mention ambiguity", output)
	}
	if !strings.Contains(string(output), idA) || !strings.Contains(string(output), idB) {
		t.Fatalf("output %q does not list ambiguous IDs %s and %s", output, idA, idB)
	}
}

func TestHandleSessionImportCodexRejectsUnknownTarget(t *testing.T) {
	if os.Getenv("AGENT_DECK_IMPORT_CODEX_HELPER") == "unknown" {
		handleSessionImportCodex(os.Getenv("AGENT_DECK_IMPORT_CODEX_PROFILE"), []string{"missing"})
		return
	}

	profile := codexImportTestProfile(t)
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestHandleSessionImportCodexRejectsUnknownTarget")
	cmd.Env = append(os.Environ(),
		"AGENT_DECK_IMPORT_CODEX_HELPER=unknown",
		"AGENT_DECK_IMPORT_CODEX_PROFILE="+profile,
		"CODEX_HOME="+home,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("unknown import unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "codex session not found") {
		t.Fatalf("output %q does not mention unknown Codex session", output)
	}
}

func TestHandleSessionImportCodexRejectsMissingRollout(t *testing.T) {
	if os.Getenv("AGENT_DECK_IMPORT_CODEX_HELPER") == "missing-rollout" {
		handleSessionImportCodex(os.Getenv("AGENT_DECK_IMPORT_CODEX_PROFILE"), []string{"no rollout"})
		return
	}

	profile := codexImportTestProfile(t)
	home := t.TempDir()
	id := "88888888-8888-8888-8888-888888888888"
	writeCodexIndexForCLI(t, home, id, "no rollout", "2026-06-30T10:00:00Z")

	cmd := exec.Command(os.Args[0], "-test.run=TestHandleSessionImportCodexRejectsMissingRollout")
	cmd.Env = append(os.Environ(),
		"AGENT_DECK_IMPORT_CODEX_HELPER=missing-rollout",
		"AGENT_DECK_IMPORT_CODEX_PROFILE="+profile,
		"CODEX_HOME="+home,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("missing-rollout import unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "codex rollout file missing") {
		t.Fatalf("output %q does not mention missing rollout", output)
	}
}

func TestHandleSessionImportCodexRejectsUnsupportedCommand(t *testing.T) {
	if os.Getenv("AGENT_DECK_IMPORT_CODEX_HELPER") == "unsupported-command" {
		handleSessionImportCodex(os.Getenv("AGENT_DECK_IMPORT_CODEX_PROFILE"), []string{"cmd", "--command", "echo codex"})
		return
	}

	profile := codexImportTestProfile(t)
	home := t.TempDir()
	id := "99999999-9999-9999-9999-999999999999"
	writeCodexIndexForCLI(t, home, id, "cmd", "2026-06-30T10:00:00Z")
	writeCodexRolloutForCLI(t, home, id)

	cmd := exec.Command(os.Args[0], "-test.run=TestHandleSessionImportCodexRejectsUnsupportedCommand")
	cmd.Env = append(os.Environ(),
		"AGENT_DECK_IMPORT_CODEX_HELPER=unsupported-command",
		"AGENT_DECK_IMPORT_CODEX_PROFILE="+profile,
		"CODEX_HOME="+home,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("unsupported command import unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "unsupported Codex command") {
		t.Fatalf("output %q does not mention unsupported command", output)
	}
}

func codexImportTestProfile(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	replacer := strings.NewReplacer("/", "_", " ", "_", "-", "_")
	return "codex_import_" + replacer.Replace(name) + "_" + time.Now().Format("150405.000000000")
}

func writeCodexIndexForCLI(t *testing.T, home, id, threadName, updatedAt string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	line := `{"id":"` + id + `","thread_name":"` + threadName + `","updated_at":"` + updatedAt + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write codex index: %v", err)
	}
}

func appendCodexIndexForCLI(t *testing.T, home, id, threadName, updatedAt string) {
	t.Helper()
	line := `{"id":"` + id + `","thread_name":"` + threadName + `","updated_at":"` + updatedAt + `"}` + "\n"
	f, err := os.OpenFile(filepath.Join(home, "session_index.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open codex index: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("append codex index: %v", err)
	}
}

func writeCodexRolloutForCLI(t *testing.T, home, sessionID string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "06", "30")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir codex rollout dir: %v", err)
	}
	path := filepath.Join(dir, "rollout-2026-06-30T10-00-00-"+sessionID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write codex rollout: %v", err)
	}
}

func captureStdoutForCodexImport(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = orig
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	return string(out)
}
