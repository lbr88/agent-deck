package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
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

func TestHandleSessionImportOpenCodeSkipsExactDuplicate(t *testing.T) {
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

	profile := "import_opencode_dupe_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	storage, instances, groups, err := loadSessionData(profile)
	if err != nil {
		t.Fatalf("loadSessionData: %v", err)
	}
	existing := session.NewInstanceWithGroupAndTool("Existing OpenCode session", projectPath, session.DefaultGroupPath, "opencode")
	existing.Command = "opencode"
	existing.Status = session.StatusStopped
	existing.OpenCodeSessionID = "ses_cli_import123"
	groupTree := session.NewGroupTreeWithGroups(append(instances, existing), groups)
	if err := storage.InsertSessionAndVerify(existing, groupTree); err != nil {
		t.Fatalf("seed duplicate session: %v", err)
	}

	output := captureStdout(t, func() {
		handleSessionImportOpenCode(profile, []string{"Existing OpenCode session", "--quiet"})
	})
	if !strings.Contains(output, "Session already exists with same title and path") {
		t.Fatalf("duplicate import output = %q, want exact-duplicate message", output)
	}

	_, stored, _, err := loadSessionData(profile)
	if err != nil {
		t.Fatalf("reload sessions: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored sessions = %d, want 1", len(stored))
	}
}

func TestHandleSessionImportOpenCodeRejectsDuplicateOpenCodeSessionIDCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess CLI test skipped in short mode")
	}
	for _, dir := range []string{
		"/var/tmp/agent-deck-go-cache",
		"/var/tmp/agent-deck-go-mod",
		"/var/tmp/agent-deck-go-tmp",
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	t.Setenv("GOCACHE", "/var/tmp/agent-deck-go-cache")
	t.Setenv("GOMODCACHE", "/var/tmp/agent-deck-go-mod")
	t.Setenv("GOTMPDIR", "/var/tmp/agent-deck-go-tmp")

	home := t.TempDir()
	existingPath := filepath.Join(home, "existing")
	importPath := filepath.Join(home, "imported")
	for _, path := range []string{existingPath, importPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	if stdout, stderr, code := runAgentDeck(t, home, "add", "-c", "opencode", "-t", "Existing Agent Deck", existingPath); code != 0 {
		t.Fatalf("agent-deck add failed (exit %d)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stdout, stderr, code := runAgentDeck(t, home, "session", "set", "Existing Agent Deck", "opencode-session-id", "ses_conflict123"); code != 0 {
		t.Fatalf("session set failed (exit %d)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	fakeDir := installFakeOpenCodeSessionListForCLI(t, []map[string]interface{}{
		{
			"id":        "ses_conflict123",
			"title":     "Imported OpenCode session",
			"directory": importPath,
			"created":   1768982195000,
			"updated":   1768982200000,
		},
	})

	stdout, stderr, code := runAgentDeckWithEnv(
		t,
		home,
		[]string{"PATH=" + fakeDir + string(os.PathListSeparator) + os.Getenv("PATH")},
		"session", "import-opencode", "ses_conflict123",
	)
	if code == 0 {
		t.Fatalf("expected duplicate OpenCode session ID import to fail\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "already imported by Agent Deck session") {
		t.Fatalf("duplicate ID error missing user-facing conflict message:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(combined, "Existing Agent Deck") || !strings.Contains(combined, "ses_conflict123") {
		t.Fatalf("duplicate ID error should mention existing session title and upstream ID:\nstdout: %s\nstderr: %s", stdout, stderr)
	}

	listJSON := readSessionsJSON(t, home)
	var sessions []map[string]interface{}
	if err := json.Unmarshal([]byte(listJSON), &sessions); err != nil {
		t.Fatalf("unmarshal list JSON: %v\n%s", err, listJSON)
	}
	if len(sessions) != 1 {
		t.Fatalf("stored sessions after failed import = %d, want 1", len(sessions))
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

func runAgentDeckWithEnv(
	t *testing.T,
	home string,
	extraEnv []string,
	args ...string,
) (stdout, stderr string, exitCode int) {
	t.Helper()

	bin := channelsCLIBinary(t)

	cmd := exec.Command(bin, args...)

	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX") {
			continue
		}
		if strings.HasPrefix(kv, "AGENTDECK_") {
			continue
		}
		if strings.HasPrefix(kv, "HOME=") {
			continue
		}
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			continue
		}
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+home,
		"AGENTDECK_PROFILE=ch_support_test",
		"TERM=dumb",
		"PATH="+os.Getenv("PATH"),
	)
	env = append(env, extraEnv...)
	cmd.Env = env

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run binary: %v\nstdout: %s\nstderr: %s", err, outBuf.String(), errBuf.String())
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}
