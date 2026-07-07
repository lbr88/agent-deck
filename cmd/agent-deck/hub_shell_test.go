package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

type fakeHubShellClient struct {
	commandNodeID  string
	commandAction  string
	commandPayload any
	commandResult  json.RawMessage
	commandErr     error

	attachNodeID    string
	attachSessionID string
	attachErr       error
}

func (f *fakeHubShellClient) Command(ctx context.Context, nodeID, action string, payload any) (json.RawMessage, error) {
	f.commandNodeID = nodeID
	f.commandAction = action
	f.commandPayload = payload
	if f.commandErr != nil {
		return nil, f.commandErr
	}
	return f.commandResult, nil
}

func (f *fakeHubShellClient) Attach(ctx context.Context, nodeID, sessionID string, size hub.TerminalSize) error {
	f.attachNodeID = nodeID
	f.attachSessionID = sessionID
	return f.attachErr
}

func TestHubCommandRoutesShell(t *testing.T) {
	data, err := os.ReadFile("hub_cmd.go")
	if err != nil {
		t.Fatalf("read hub_cmd.go: %v", err)
	}
	if !strings.Contains(string(data), `case "shell":`) || !strings.Contains(string(data), "handleHubShell(") {
		t.Fatalf("hub_cmd.go must route agent-deck hub shell")
	}
}

func TestRunHubShellWithClientCreatesShellAndAttaches(t *testing.T) {
	client := &fakeHubShellClient{commandResult: json.RawMessage(`{"session_id":"sess_shell"}`)}

	result, err := runHubShellWithClient(context.Background(), client, hubShellOptions{
		NodeID:   "node_work",
		NodeName: "work-laptop",
		Title:    "remote maintenance",
		CWD:      "/srv/app",
		Group:    "ops",
		Attach:   true,
	})
	if err != nil {
		t.Fatalf("runHubShellWithClient: %v", err)
	}

	if result.SessionID != "sess_shell" || result.NodeID != "node_work" || result.NodeName != "work-laptop" {
		t.Fatalf("result = %+v, want shell session on work-laptop", result)
	}
	if client.commandNodeID != "node_work" || client.commandAction != "create" {
		t.Fatalf("command = (%q, %q), want create on node_work", client.commandNodeID, client.commandAction)
	}
	req, ok := client.commandPayload.(hub.CreateSessionRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.CreateSessionRequest", client.commandPayload)
	}
	if req.Tool != "shell" || req.Title != "remote maintenance" || req.ProjectPath != "/srv/app" || req.GroupPath != "ops" {
		t.Fatalf("create request = %+v, want shell request with title/cwd/group", req)
	}
	if client.attachNodeID != "node_work" || client.attachSessionID != "sess_shell" {
		t.Fatalf("attach = (%q, %q), want node_work/sess_shell", client.attachNodeID, client.attachSessionID)
	}
}

func TestRunHubShellWithClientCanSkipAttach(t *testing.T) {
	client := &fakeHubShellClient{commandResult: json.RawMessage(`{"session_id":"sess_shell"}`)}

	result, err := runHubShellWithClient(context.Background(), client, hubShellOptions{
		NodeID: "node_work",
		Attach: false,
	})
	if err != nil {
		t.Fatalf("runHubShellWithClient: %v", err)
	}
	if result.SessionID != "sess_shell" {
		t.Fatalf("SessionID = %q, want sess_shell", result.SessionID)
	}
	if client.attachSessionID != "" {
		t.Fatalf("attachSessionID = %q, want no attach", client.attachSessionID)
	}
}

func TestResolveHubShellNodeSelectorFromSnapshots(t *testing.T) {
	snapshots := []hub.NodeSessions{
		{Node: hub.Node{ID: "node_work", Name: "work-laptop"}},
		{Node: hub.Node{ID: "node_private", Name: "laptop"}},
	}

	byID, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, "node_private")
	if err != nil {
		t.Fatalf("resolve by id: %v", err)
	}
	if byID.NodeID != "node_private" {
		t.Fatalf("resolve by id = %+v, want node_private", byID)
	}

	byName, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, "work-laptop")
	if err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
	if byName.NodeID != "node_work" || byName.NodeName != "work-laptop" {
		t.Fatalf("resolve by name = %+v, want work-laptop/node_work", byName)
	}
}

func TestResolveHubShellNodeSelectorRejectsAmbiguousName(t *testing.T) {
	snapshots := []hub.NodeSessions{
		{Node: hub.Node{ID: "node_a", Name: "server"}},
		{Node: hub.Node{ID: "node_b", Name: "server"}},
	}

	_, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, "server")
	if err == nil || !strings.Contains(err.Error(), "multiple") || !strings.Contains(err.Error(), "node_a") || !strings.Contains(err.Error(), "node_b") {
		t.Fatalf("error = %v, want ambiguous name with node ids", err)
	}
}

func TestDecodeHubCreateSessionResultRequiresSessionID(t *testing.T) {
	sessionID, err := decodeHubCreateSessionResult(json.RawMessage(`{"session_id":"sess_123"}`))
	if err != nil {
		t.Fatalf("decodeHubCreateSessionResult: %v", err)
	}
	if sessionID != "sess_123" {
		t.Fatalf("sessionID = %q, want sess_123", sessionID)
	}

	if _, err := decodeHubCreateSessionResult(json.RawMessage(`{}`)); err == nil {
		t.Fatal("decodeHubCreateSessionResult accepted missing session_id")
	}
}

func TestHubInstallSkillWritesHubSkillToPool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	out := captureStdout(t, func() {
		if err := handleHubInstallSkill([]string{"agent-deck-hub"}); err != nil {
			t.Fatalf("handleHubInstallSkill: %v", err)
		}
	})
	if !strings.Contains(out, "Installed skill: agent-deck-hub") {
		t.Fatalf("install output = %q, want installed skill confirmation", out)
	}

	skillPath := filepath.Join(home, ".config", "agent-deck", "skills", "pool", "agent-deck-hub", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "agent-deck hub shell") || !strings.Contains(content, "--no-attach --json") {
		t.Fatalf("installed skill content missing hub shell guidance:\n%s", content)
	}
}
