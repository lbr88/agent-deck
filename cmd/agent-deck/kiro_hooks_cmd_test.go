package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestKiroHooksInstallUninstall(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("KIRO_HOME", configDir)

	handleKiroHooksInstall()

	configPath := filepath.Join(configDir, "agents", session.AgentDeckKiroAgentName+".json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Kiro agent config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"hooks"`) || !strings.Contains(text, "agent-context") {
		t.Fatalf("expected Kiro hub context hooks, got:\n%s", text)
	}

	handleKiroHooksUninstall()

	data, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Kiro agent config after uninstall: %v", err)
	}
	if strings.Contains(string(data), "agent-context") {
		t.Fatalf("expected Kiro hub context hooks removed, got:\n%s", string(data))
	}
}

func TestGetKiroConfigDirForHooks(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("KIRO_HOME", configDir)

	if got := getKiroConfigDirForHooks(); got != configDir {
		t.Fatalf("getKiroConfigDirForHooks() = %q, want %q", got, configDir)
	}
}
