package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestOpenCodeHooksInstallUninstall(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := session.GetOpenCodeConfigDir()
	pluginPath := filepath.Join(configDir, "plugins", "agent-deck-hub-context.js")
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(pluginPath, []byte("/* AGENTDECK HUB CONTEXT PLUGIN */\nagent-context\n"), 0644); err != nil {
		t.Fatalf("seed OpenCode plugin: %v", err)
	}

	handleOpenCodeHooksInstall()

	if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
		t.Fatalf("expected OpenCode context plugin removed by install, stat err=%v", err)
	}

	handleOpenCodeHooksUninstall()

	if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
		t.Fatalf("expected OpenCode plugin removed, stat err=%v", err)
	}
}

func TestGetOpenCodeConfigDirForHooks(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	got := getOpenCodeConfigDirForHooks()
	if !strings.HasSuffix(got, filepath.Join(".config", "opencode")) {
		t.Fatalf("getOpenCodeConfigDirForHooks() = %q, want ~/.config/opencode suffix", got)
	}
}
