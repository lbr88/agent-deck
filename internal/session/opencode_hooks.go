package session

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const openCodeAgentDeckPluginFile = "agent-deck-hub-context.js"
const openCodeAgentDeckPluginMarker = "AGENTDECK HUB CONTEXT PLUGIN"

// InjectOpenCodeHooks no longer installs the legacy global OpenCode hub-context
// plugin because it runs on chat prompt construction rather than a narrow
// session-start edge. If an older Agent Deck-owned plugin is present, remove it.
func InjectOpenCodeHooks(configDir string) (bool, error) {
	return RemoveOpenCodeHooks(configDir)
}

// RemoveOpenCodeHooks removes the Agent Deck OpenCode plugin when present.
func RemoveOpenCodeHooks(configDir string) (bool, error) {
	pluginPath := openCodeAgentDeckPluginPath(configDir)
	existing, err := os.ReadFile(pluginPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read OpenCode plugin: %w", err)
	}
	if !strings.Contains(string(existing), openCodeAgentDeckPluginMarker) {
		return false, nil
	}
	if err := os.Remove(pluginPath); err != nil {
		return false, fmt.Errorf("remove OpenCode plugin: %w", err)
	}

	sessionLog.Info("opencode_hooks_removed", slog.String("config_dir", configDir))
	return true, nil
}

// CheckOpenCodeHooksInstalled reports whether the legacy Agent Deck OpenCode
// context plugin is still present.
func CheckOpenCodeHooksInstalled(configDir string) bool {
	existing, err := os.ReadFile(openCodeAgentDeckPluginPath(configDir))
	if err != nil {
		return false
	}
	text := string(existing)
	return strings.Contains(text, openCodeAgentDeckPluginMarker) &&
		strings.Contains(text, "experimental.chat.system.transform") &&
		strings.Contains(text, "agent-context")
}

func openCodeAgentDeckPluginPath(configDir string) string {
	return filepath.Join(configDir, "plugins", openCodeAgentDeckPluginFile)
}
