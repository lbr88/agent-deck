package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/atomicfile"
)

const AgentDeckKiroAgentName = "agent-deck"
const agentDeckKiroContextHookCommand = agentDeckPlainContextHookCommand

var kiroContextHookEventNames = []string{
	"agentSpawn",
}

type kiroHookEntry struct {
	Command         string `json:"command"`
	Matcher         string `json:"matcher,omitempty"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
	CacheTTLSeconds int    `json:"cache_ttl_seconds,omitempty"`
}

// GetKiroConfigDir returns the Kiro config directory.
func GetKiroConfigDir() string {
	if kiroHome := strings.TrimSpace(os.Getenv("KIRO_HOME")); kiroHome != "" {
		return kiroHome
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), ".kiro")
	}
	return filepath.Join(home, ".kiro")
}

func kiroAgentConfigPath(configDir string) string {
	return filepath.Join(configDir, "agents", AgentDeckKiroAgentName+".json")
}

// InjectKiroHooks creates or updates the Agent Deck Kiro custom agent.
// Kiro hooks live on custom agents, so the generated agent stays deliberately
// broad and only adds hub context collection hooks.
func InjectKiroHooks(configDir string) (bool, error) {
	agentPath := kiroAgentConfigPath(configDir)

	raw, err := readKiroAgentRaw(agentPath)
	if err != nil {
		return false, err
	}

	var hooks map[string][]kiroHookEntry
	if rawHooks, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(rawHooks, &hooks); err != nil {
			hooks = make(map[string][]kiroHookEntry)
		}
	} else {
		hooks = make(map[string][]kiroHookEntry)
	}

	changed := ensureKiroAgentDefaults(raw)
	if removeKiroContextHooksFromEvents(hooks, []string{"userPromptSubmit"}) {
		changed = true
	}
	for _, event := range kiroContextHookEventNames {
		merged, eventChanged := mergeKiroHookEvent(hooks[event], agentDeckKiroContextHookCommand)
		if eventChanged {
			changed = true
			hooks[event] = merged
		}
	}

	hooksRaw, err := json.Marshal(hooks)
	if err != nil {
		return false, fmt.Errorf("marshal Kiro hooks: %w", err)
	}
	raw["hooks"] = hooksRaw

	if !changed {
		return false, nil
	}

	if err := writeKiroAgentRaw(agentPath, raw); err != nil {
		return false, err
	}

	sessionLog.Info("kiro_hooks_installed", slog.String("config_dir", configDir))
	return true, nil
}

// RemoveKiroHooks removes Agent Deck hub context hooks from the generated Kiro
// agent while preserving any user edits and user hooks.
func RemoveKiroHooks(configDir string) (bool, error) {
	agentPath := kiroAgentConfigPath(configDir)
	raw, err := readKiroAgentRaw(agentPath)
	if err != nil {
		return false, err
	}

	rawHooks, ok := raw["hooks"]
	if !ok {
		return false, nil
	}
	var hooks map[string][]kiroHookEntry
	if err := json.Unmarshal(rawHooks, &hooks); err != nil {
		return false, nil
	}

	removed := false
	for _, event := range kiroContextHookEventNames {
		cleaned, didRemove := removeAgentDeckFromKiroEvent(hooks[event])
		if didRemove {
			removed = true
			if len(cleaned) == 0 {
				delete(hooks, event)
			} else {
				hooks[event] = cleaned
			}
		}
	}

	if !removed {
		return false, nil
	}

	if len(hooks) == 0 {
		delete(raw, "hooks")
	} else {
		hooksRaw, err := json.Marshal(hooks)
		if err != nil {
			return false, fmt.Errorf("marshal Kiro hooks: %w", err)
		}
		raw["hooks"] = hooksRaw
	}

	if err := writeKiroAgentRaw(agentPath, raw); err != nil {
		return false, err
	}

	sessionLog.Info("kiro_hooks_removed", slog.String("config_dir", configDir))
	return true, nil
}

// CheckKiroHooksInstalled reports whether the Agent Deck Kiro agent contains
// all required hub context hooks.
func CheckKiroHooksInstalled(configDir string) bool {
	raw, err := readKiroAgentRaw(kiroAgentConfigPath(configDir))
	if err != nil {
		return false
	}
	rawHooks, ok := raw["hooks"]
	if !ok {
		return false
	}
	var hooks map[string][]kiroHookEntry
	if err := json.Unmarshal(rawHooks, &hooks); err != nil {
		return false
	}
	for _, event := range kiroContextHookEventNames {
		if !kiroEventHasCommand(hooks[event], agentDeckKiroContextHookCommand) {
			return false
		}
	}
	return true
}

func readKiroAgentRaw(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]json.RawMessage), nil
		}
		return nil, fmt.Errorf("read Kiro agent config: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse Kiro agent config: %w", err)
	}
	if raw == nil {
		raw = make(map[string]json.RawMessage)
	}
	return raw, nil
}

func writeKiroAgentRaw(path string, raw map[string]json.RawMessage) error {
	finalData, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Kiro agent config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create Kiro agents dir: %w", err)
	}
	if err := atomicfile.WriteFile(path, finalData, 0644); err != nil {
		return fmt.Errorf("write Kiro agent config: %w", err)
	}
	return nil
}

func ensureKiroAgentDefaults(raw map[string]json.RawMessage) bool {
	changed := false
	if _, ok := raw["name"]; !ok {
		raw["name"] = mustMarshalRaw(AgentDeckKiroAgentName)
		changed = true
	}
	if _, ok := raw["description"]; !ok {
		raw["description"] = mustMarshalRaw("Default Kiro agent with Agent Deck hub context hooks")
		changed = true
	}
	if _, ok := raw["tools"]; !ok {
		raw["tools"] = mustMarshalRaw([]string{"*"})
		changed = true
	}
	if _, ok := raw["includeMcpJson"]; !ok {
		raw["includeMcpJson"] = mustMarshalRaw(true)
		changed = true
	}
	if _, ok := raw["prompt"]; !ok {
		raw["prompt"] = mustMarshalRaw("Keep normal Kiro coding-assistant behavior. Agent Deck may add hub context when this node is connected to a hub.")
		changed = true
	}
	return changed
}

func mustMarshalRaw(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func kiroEventHasCommand(defs []kiroHookEntry, command string) bool {
	for _, d := range defs {
		if kiroCommandMatches(d.Command, command) {
			return true
		}
	}
	return false
}

func removeKiroContextHooksFromEvents(hooks map[string][]kiroHookEntry, events []string) bool {
	removed := false
	for _, event := range events {
		cleaned, didRemove := removeKiroCommandFromEvent(hooks[event], agentDeckKiroContextHookCommand)
		if !didRemove {
			continue
		}
		removed = true
		if len(cleaned) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = cleaned
		}
	}
	return removed
}

func removeKiroCommandFromEvent(defs []kiroHookEntry, command string) ([]kiroHookEntry, bool) {
	removed := false
	cleaned := make([]kiroHookEntry, 0, len(defs))
	for _, d := range defs {
		if kiroCommandMatches(d.Command, command) {
			removed = true
			continue
		}
		cleaned = append(cleaned, d)
	}
	return cleaned, removed
}

func kiroCommandMatches(got, want string) bool {
	return strings.TrimSpace(got) == strings.TrimSpace(want)
}

func mergeKiroHookEvent(existing []kiroHookEntry, command string) ([]kiroHookEntry, bool) {
	if kiroEventHasCommand(existing, command) {
		return existing, false
	}
	return append(existing, kiroHookEntry{Command: command}), true
}

func removeAgentDeckFromKiroEvent(defs []kiroHookEntry) ([]kiroHookEntry, bool) {
	removed := false
	var cleaned []kiroHookEntry
	for _, d := range defs {
		if kiroCommandMatches(d.Command, agentDeckKiroContextHookCommand) {
			removed = true
			continue
		}
		cleaned = append(cleaned, d)
	}
	return cleaned, removed
}
