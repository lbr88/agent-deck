package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectKiroHooks_Fresh(t *testing.T) {
	tmpDir := t.TempDir()

	installed, err := InjectKiroHooks(tmpDir)
	if err != nil {
		t.Fatalf("InjectKiroHooks failed: %v", err)
	}
	if !installed {
		t.Fatal("expected hooks to be newly installed")
	}
	if !CheckKiroHooksInstalled(tmpDir) {
		t.Fatal("expected Kiro hooks installed")
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "agents", AgentDeckKiroAgentName+".json"))
	if err != nil {
		t.Fatalf("read agent config: %v", err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse agent config: %v", err)
	}
	var hooks map[string][]kiroHookEntry
	if err := json.Unmarshal(cfg["hooks"], &hooks); err != nil {
		t.Fatalf("parse hooks: %v", err)
	}
	for _, event := range kiroContextHookEventNames {
		if !kiroEventHasCommand(hooks[event], agentDeckKiroContextHookCommand) {
			t.Fatalf("event %s missing agent-deck hub context hook", event)
		}
	}
	if kiroEventHasCommand(hooks["userPromptSubmit"], agentDeckKiroContextHookCommand) {
		t.Fatal("userPromptSubmit must not include hub context hook")
	}
}

func TestInjectKiroHooks_PreservesExistingAgent(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	seed := `{
  "name": "agent-deck",
  "description": "custom",
  "tools": ["read"],
  "hooks": {
    "userPromptSubmit": [{"command": "echo user"}]
  }
}`
	if err := os.WriteFile(filepath.Join(agentsDir, AgentDeckKiroAgentName+".json"), []byte(seed), 0644); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	if _, err := InjectKiroHooks(tmpDir); err != nil {
		t.Fatalf("InjectKiroHooks failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(agentsDir, AgentDeckKiroAgentName+".json"))
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"tools"`) || !strings.Contains(text, `"echo user"`) {
		t.Fatalf("expected existing config preserved, got:\n%s", text)
	}
	if !strings.Contains(text, agentDeckKiroContextHookCommand) {
		t.Fatalf("expected hub context hook added, got:\n%s", text)
	}
}

func TestRemoveKiroHooks(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := InjectKiroHooks(tmpDir); err != nil {
		t.Fatalf("install: %v", err)
	}
	removed, err := RemoveKiroHooks(tmpDir)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !removed {
		t.Fatal("expected hooks removed")
	}
	if CheckKiroHooksInstalled(tmpDir) {
		t.Fatal("expected hooks not installed after remove")
	}
}
