package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHandleSessionImportOpenCodeCreatesStoppedSession(t *testing.T) {
	projectPath := t.TempDir()
	fakeDir := installFakeOpenCodeSessionListForCLI(t, []map[string]interface{}{
		{
			"id":        "ses_cli_import123",
			"title":     "Existing OpenCode session",
			"directory": projectPath,
			"created":   1768982195000,
			"updated":   1768982200000,
		},
	})
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	profile := "import_opencode_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	handleSessionImportOpenCode(profile, []string{"Existing OpenCode session", "--quiet"})

	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		t.Fatalf("loadSessionData: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("stored sessions = %d, want 1", len(instances))
	}

	inst := instances[0]
	if inst.Title != "Existing OpenCode session" {
		t.Fatalf("Title = %q", inst.Title)
	}
	if inst.ProjectPath != projectPath {
		t.Fatalf("ProjectPath = %q, want %q", inst.ProjectPath, projectPath)
	}
	if inst.GroupPath != session.DefaultGroupPath {
		t.Fatalf("GroupPath = %q, want %q", inst.GroupPath, session.DefaultGroupPath)
	}
	if inst.Status != session.StatusStopped {
		t.Fatalf("Status = %q, want stopped", inst.Status)
	}
	if inst.Tool != "opencode" || inst.Command != "opencode" {
		t.Fatalf("Tool/Command = %q/%q, want opencode/opencode", inst.Tool, inst.Command)
	}
	if inst.OpenCodeSessionID != "ses_cli_import123" {
		t.Fatalf("OpenCodeSessionID = %q, want ses_cli_import123", inst.OpenCodeSessionID)
	}
}

func TestSessionHelpMentionsImportOpenCode(t *testing.T) {
	output := captureStdout(t, printSessionHelp)
	if !strings.Contains(output, "import-opencode") {
		t.Fatalf("session help missing import-opencode:\n%s", output)
	}
}

func installFakeOpenCodeSessionListForCLI(t *testing.T, sessions []map[string]interface{}) string {
	t.Helper()

	payload, err := json.Marshal(sessions)
	if err != nil {
		t.Fatalf("marshal fake sessions: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"list\" ] && [ \"$3\" = \"--format\" ] && [ \"$4\" = \"json\" ]; then\n" +
		"cat <<'JSON'\n" +
		string(payload) + "\n" +
		"JSON\n" +
		"exit 0\n" +
		"fi\n" +
		"exit 64\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake opencode: %v", err)
	}
	return dir
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout pipe reader: %v", err)
	}
	return string(out)
}
