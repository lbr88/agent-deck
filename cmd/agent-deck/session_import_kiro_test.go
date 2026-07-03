package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHandleSessionImportKiroCreatesStoppedSession(t *testing.T) {
	profile := kiroImportTestProfile(t)
	kiroHome := t.TempDir()
	t.Setenv("KIRO_HOME", kiroHome)
	projectPath := filepath.Join(t.TempDir(), "git", "domutech", "domutech-github")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	id := "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d"
	writeKiroSessionForCLI(t, kiroHome, id, "github import", projectPath, "2026-07-03T10:00:00Z")

	handleSessionImportKiro(profile, []string{"github import", "--quiet"})

	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		t.Fatalf("loadSessionData: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances=%d, want 1", len(instances))
	}
	got := instances[0]
	if got.Tool != "kiro" || got.Command != "kiro-cli chat --tui" || got.Title != "github import" || got.Status != session.StatusStopped {
		t.Fatalf("bad imported instance: %#v", got)
	}
	if got.ProjectPath != projectPath {
		t.Fatalf("ProjectPath = %q, want %q", got.ProjectPath, projectPath)
	}
	if got.GroupPath != session.GroupPathForProject(projectPath) {
		t.Fatalf("GroupPath = %q, want %q", got.GroupPath, session.GroupPathForProject(projectPath))
	}
	if got.KiroSessionID != id {
		t.Fatalf("KiroSessionID = %q, want %q", got.KiroSessionID, id)
	}
}

func TestHandleSessionImportKiroOutputsJSONAndHonorsOverrides(t *testing.T) {
	profile := kiroImportTestProfile(t)
	kiroHome := t.TempDir()
	t.Setenv("KIRO_HOME", kiroHome)
	projectPath := t.TempDir()
	overridePath := t.TempDir()
	id := "37a7454c-f9d3-434a-bd7e-03318ef6b72a"
	writeKiroSessionForCLI(t, kiroHome, id, "saved title", projectPath, "2026-07-03T10:00:00Z")

	stdout := captureStdout(t, func() {
		handleSessionImportKiro(profile, []string{id, "--title", "imported", "--group", "work", "--path", overridePath, "--command", "kiro-cli chat --tui --trust-all-tools", "--json"})
	})

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json output %q failed to unmarshal: %v", stdout, err)
	}
	if payload["success"] != true || payload["title"] != "imported" || payload["kiro_session_id"] != id {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}

	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		t.Fatalf("loadSessionData: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances=%d, want 1", len(instances))
	}
	got := instances[0]
	if got.Command != "kiro-cli chat --tui --trust-all-tools" || got.ProjectPath != overridePath || got.GroupPath != "work" {
		t.Fatalf("bad imported overrides: %#v", got)
	}
}

func TestFindKiroImportSessionIDConflict(t *testing.T) {
	existing := &session.Instance{ID: "existing", Title: "Existing Agent Deck", KiroSessionID: "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d"}
	if got := findKiroImportSessionIDConflict([]*session.Instance{existing}, "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d"); got != existing {
		t.Fatalf("findKiroImportSessionIDConflict returned %#v, want existing", got)
	}
	if got := findKiroImportSessionIDConflict([]*session.Instance{existing}, ""); got != nil {
		t.Fatalf("empty conflict = %#v, want nil", got)
	}
}

func TestSessionHelpMentionsImportKiro(t *testing.T) {
	output := captureStdout(t, printSessionHelp)
	if !strings.Contains(output, "import-kiro") {
		t.Fatalf("session help missing import-kiro:\n%s", output)
	}
}

func kiroImportTestProfile(t *testing.T) string {
	t.Helper()
	return "import_kiro_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
}

func writeKiroSessionForCLI(t *testing.T, kiroHome, id, title, cwd, updatedAt string) {
	t.Helper()
	dir := filepath.Join(kiroHome, "sessions", "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir kiro sessions dir: %v", err)
	}
	body := `{"session_id":"` + id + `","cwd":"` + cwd + `","title":"` + title + `","created_at":"2026-07-03T09:00:00Z","updated_at":"` + updatedAt + `","session_state":{"agent_name":"kiro_default"}}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write kiro session: %v", err)
	}
}
