package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInjectOpenCodeHooks_FreshDoesNotInstallPromptContextPlugin(t *testing.T) {
	tmpDir := t.TempDir()

	installed, err := InjectOpenCodeHooks(tmpDir)
	if err != nil {
		t.Fatalf("InjectOpenCodeHooks failed: %v", err)
	}
	if installed {
		t.Fatal("fresh install should not add OpenCode prompt context plugin")
	}
	if CheckOpenCodeHooksInstalled(tmpDir) {
		t.Fatal("expected OpenCode context plugin not installed")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "plugins", openCodeAgentDeckPluginFile)); !os.IsNotExist(err) {
		t.Fatalf("plugin file stat = %v, want not exist", err)
	}
}

func TestInjectOpenCodeHooks_RemovesLegacyPromptContextPlugin(t *testing.T) {
	tmpDir := t.TempDir()
	pluginPath := filepath.Join(tmpDir, "plugins", openCodeAgentDeckPluginFile)
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(pluginPath, []byte(legacyOpenCodePromptContextPlugin()), 0644); err != nil {
		t.Fatalf("seed plugin: %v", err)
	}

	installed, err := InjectOpenCodeHooks(tmpDir)
	if err != nil {
		t.Fatalf("InjectOpenCodeHooks failed: %v", err)
	}
	if !installed {
		t.Fatal("expected legacy plugin cleanup to report change")
	}
	if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy plugin removed, stat err=%v", err)
	}
}

func TestRemoveOpenCodeHooks(t *testing.T) {
	tmpDir := t.TempDir()
	pluginPath := filepath.Join(tmpDir, "plugins", openCodeAgentDeckPluginFile)
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(pluginPath, []byte(legacyOpenCodePromptContextPlugin()), 0644); err != nil {
		t.Fatalf("seed plugin: %v", err)
	}
	removed, err := RemoveOpenCodeHooks(tmpDir)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !removed {
		t.Fatal("expected hooks removed")
	}
	if CheckOpenCodeHooksInstalled(tmpDir) {
		t.Fatal("expected hooks not installed after remove")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "plugins", openCodeAgentDeckPluginFile)); !os.IsNotExist(err) {
		t.Fatalf("expected plugin file removed, stat err=%v", err)
	}
}

func legacyOpenCodePromptContextPlugin() string {
	return `// ` + openCodeAgentDeckPluginMarker + `
export const AgentDeckHubContextPlugin = async () => ({
  "experimental.chat.system.transform": async () => "agent-context",
})
`
}
