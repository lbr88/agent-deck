package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

type fakeHubSessionsClient struct {
	commands []fakeHubSessionCommand
	results  map[string]json.RawMessage

	attachNodeID    string
	attachSessionID string
}

type fakeHubSessionCommand struct {
	nodeID  string
	action  string
	payload any
}

func (f *fakeHubSessionsClient) Command(_ context.Context, nodeID, action string, payload any) (json.RawMessage, error) {
	f.commands = append(f.commands, fakeHubSessionCommand{nodeID: nodeID, action: action, payload: payload})
	if f.results != nil {
		if raw := f.results[action]; len(raw) > 0 {
			return raw, nil
		}
	}
	return json.RawMessage(`{"session_id":"created_1"}`), nil
}

func (f *fakeHubSessionsClient) Attach(_ context.Context, nodeID, sessionID string, _ hub.TerminalSize) error {
	f.attachNodeID = nodeID
	f.attachSessionID = sessionID
	return nil
}

func mustMarshalJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return raw
}

func TestHubCommandRoutesSessions(t *testing.T) {
	data, err := os.ReadFile("hub_cmd.go")
	if err != nil {
		t.Fatalf("read hub_cmd.go: %v", err)
	}
	if !strings.Contains(string(data), `case "sessions":`) || !strings.Contains(string(data), "handleHubSessions(") {
		t.Fatalf("hub_cmd.go must route agent-deck hub sessions")
	}
}

func TestHubCommandRoutesGroups(t *testing.T) {
	data, err := os.ReadFile("hub_cmd.go")
	if err != nil {
		t.Fatalf("read hub_cmd.go: %v", err)
	}
	if !strings.Contains(string(data), `case "groups":`) || !strings.Contains(string(data), "handleHubGroups(") {
		t.Fatalf("hub_cmd.go must route agent-deck hub groups")
	}
}

func TestHubCommandRoutesMCPs(t *testing.T) {
	data, err := os.ReadFile("hub_cmd.go")
	if err != nil {
		t.Fatalf("read hub_cmd.go: %v", err)
	}
	if !strings.Contains(string(data), `case "mcps", "mcp":`) || !strings.Contains(string(data), "handleHubMCPs(") {
		t.Fatalf("hub_cmd.go must route agent-deck hub mcps")
	}
}

func TestHubCommandRoutesSkills(t *testing.T) {
	data, err := os.ReadFile("hub_cmd.go")
	if err != nil {
		t.Fatalf("read hub_cmd.go: %v", err)
	}
	if !strings.Contains(string(data), `case "skills", "skill":`) || !strings.Contains(string(data), "handleHubSkills(") {
		t.Fatalf("hub_cmd.go must route agent-deck hub skills")
	}
}

func TestHubCommandRoutesPlugins(t *testing.T) {
	data, err := os.ReadFile("hub_cmd.go")
	if err != nil {
		t.Fatalf("read hub_cmd.go: %v", err)
	}
	if !strings.Contains(string(data), `case "plugins", "plugin":`) || !strings.Contains(string(data), "handleHubPlugins(") {
		t.Fatalf("hub_cmd.go must route agent-deck hub plugins")
	}
}

func TestHubGroupsUsageDocumentsNativeGroupParity(t *testing.T) {
	var buf bytes.Buffer
	printHubGroupsUsage(&buf)
	text := buf.String()
	for _, action := range []string{"list", "create", "rename", "update", "delete", "change", "reorder"} {
		if !strings.Contains(text, action) {
			t.Fatalf("hub groups usage missing %q:\n%s", action, text)
		}
	}
}

func TestHubMCPsUsageDocumentsNativeMCPParity(t *testing.T) {
	var buf bytes.Buffer
	printHubMCPsUsage(&buf)
	text := buf.String()
	for _, action := range []string{"attached", "catalog", "attach", "detach", "move"} {
		if !strings.Contains(text, action) {
			t.Fatalf("hub mcps usage missing %q:\n%s", action, text)
		}
	}
}

func TestHubSkillsUsageDocumentsNativeSkillParity(t *testing.T) {
	var buf bytes.Buffer
	printHubSkillsUsage(&buf)
	text := buf.String()
	for _, action := range []string{"attached", "catalog", "attach", "detach"} {
		if !strings.Contains(text, action) {
			t.Fatalf("hub skills usage missing %q:\n%s", action, text)
		}
	}
}

func TestHubPluginsUsageDocumentsNativePluginParity(t *testing.T) {
	var buf bytes.Buffer
	printHubPluginsUsage(&buf)
	text := buf.String()
	for _, action := range []string{"attached", "catalog", "attach", "detach"} {
		if !strings.Contains(text, action) {
			t.Fatalf("hub plugins usage missing %q:\n%s", action, text)
		}
	}
}

func TestHubSessionsUsageDocumentsAllNativeActions(t *testing.T) {
	var buf bytes.Buffer
	printHubSessionsUsage(&buf)
	text := buf.String()
	for _, action := range []string{
		"list",
		"create",
		"attach",
		"send",
		"approve",
		"notes",
		"start",
		"close",
		"restart",
		"restart-fresh",
		"fork",
		"worktree-finish",
		"rename",
		"move",
		"delete",
		"archive",
		"unarchive",
		"remove",
		"toggle-yolo",
		"unread",
		"preview",
	} {
		if !strings.Contains(text, action) {
			t.Fatalf("hub sessions usage missing %q:\n%s", action, text)
		}
	}
}

func TestRunHubSessionNotesUsesHubUpdateNotesAction(t *testing.T) {
	client := &fakeHubSessionsClient{}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:    "notes",
		NodeID:    "work",
		SessionID: "api",
		Notes:     "line one\nline two",
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient notes: %v", err)
	}
	if result.SessionID != "sess_api" || result.Action != "notes" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "update" {
		t.Fatalf("commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(hub.UpdateSessionRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.UpdateSessionRequest", client.commands[0].payload)
	}
	if payload.SessionID != "sess_api" || len(payload.Changes) != 1 || payload.Changes[0].Field != "notes" || payload.Changes[0].Value != "line one\nline two" {
		t.Fatalf("notes payload = %+v", payload)
	}
}

func TestRunHubGroupActionsUseHubRequests(t *testing.T) {
	client := &fakeHubSessionsClient{
		results: map[string]json.RawMessage{
			"group_create":   mustMarshalJSON(t, hub.GroupCreateResponse{Path: "ops/api", Name: "api", MaxConcurrent: 2}),
			"group_rename":   mustMarshalJSON(t, hub.GroupRenameResponse{OldPath: "ops/api", Path: "ops/backend", Name: "backend"}),
			"group_update":   mustMarshalJSON(t, hub.GroupUpdateResponse{Path: "ops/backend", DefaultPath: "/srv/backend", MaxConcurrent: 4}),
			"group_delete":   mustMarshalJSON(t, hub.GroupDeleteResponse{Path: "ops/backend", SessionsMoved: 0, MovedTo: "ops"}),
			"group_reparent": mustMarshalJSON(t, hub.GroupReparentResponse{OldPath: "ops/backend", Path: "platform/backend", DestParentPath: "platform"}),
			"group_reorder":  mustMarshalJSON(t, hub.GroupReorderResponse{Path: "platform/backend", FromPosition: 2, ToPosition: 1}),
		},
	}
	snapshots := []hub.NodeSessions{{Node: hub.Node{ID: "node_work", Name: "work"}}}
	maxCreate := 2
	result, err := runHubGroupWithClient(context.Background(), client, snapshots, hubGroupOptions{
		Action:        "create",
		NodeID:        "work",
		Name:          "api",
		ParentPath:    "ops",
		DefaultPath:   "/srv/api",
		MaxConcurrent: &maxCreate,
	})
	if err != nil {
		t.Fatalf("runHubGroupWithClient create: %v", err)
	}
	if result.Path != "ops/api" || result.NodeID != "node_work" {
		t.Fatalf("create result = %+v", result)
	}
	createReq, ok := client.commands[0].payload.(hub.GroupCreateRequest)
	if !ok {
		t.Fatalf("create payload type = %T, want hub.GroupCreateRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_work" || client.commands[0].action != "group_create" || createReq.Name != "api" || createReq.ParentPath != "ops" || createReq.MaxConcurrent == nil || *createReq.MaxConcurrent != 2 {
		t.Fatalf("create command = %+v req=%+v", client.commands[0], createReq)
	}

	result, err = runHubGroupWithClient(context.Background(), client, snapshots, hubGroupOptions{
		Action:    "rename",
		NodeID:    "work",
		GroupPath: "ops/api",
		Name:      "backend",
	})
	if err != nil {
		t.Fatalf("runHubGroupWithClient rename: %v", err)
	}
	if result.Path != "ops/backend" {
		t.Fatalf("rename result = %+v", result)
	}
	renameReq, ok := client.commands[1].payload.(hub.GroupRenameRequest)
	if !ok {
		t.Fatalf("rename payload type = %T, want hub.GroupRenameRequest", client.commands[1].payload)
	}
	if client.commands[1].action != "group_rename" || renameReq.GroupPath != "ops/api" || renameReq.Name != "backend" {
		t.Fatalf("rename command = %+v req=%+v", client.commands[1], renameReq)
	}

	maxUpdate := 4
	result, err = runHubGroupWithClient(context.Background(), client, snapshots, hubGroupOptions{
		Action:        "update",
		NodeID:        "work",
		GroupPath:     "ops/backend",
		DefaultPath:   "/srv/backend",
		MaxConcurrent: &maxUpdate,
	})
	if err != nil {
		t.Fatalf("runHubGroupWithClient update: %v", err)
	}
	if result.Path != "ops/backend" {
		t.Fatalf("update result = %+v", result)
	}
	updateReq, ok := client.commands[2].payload.(hub.GroupUpdateRequest)
	if !ok {
		t.Fatalf("update payload type = %T, want hub.GroupUpdateRequest", client.commands[2].payload)
	}
	if client.commands[2].action != "group_update" || updateReq.GroupPath != "ops/backend" || updateReq.DefaultPath == nil || *updateReq.DefaultPath != "/srv/backend" || updateReq.MaxConcurrent == nil || *updateReq.MaxConcurrent != 4 {
		t.Fatalf("update command = %+v req=%+v", client.commands[2], updateReq)
	}

	result, err = runHubGroupWithClient(context.Background(), client, snapshots, hubGroupOptions{
		Action:    "delete",
		NodeID:    "work",
		GroupPath: "ops/backend",
		Force:     true,
	})
	if err != nil {
		t.Fatalf("runHubGroupWithClient delete: %v", err)
	}
	if result.Path != "ops/backend" {
		t.Fatalf("delete result = %+v", result)
	}
	deleteReq, ok := client.commands[3].payload.(hub.GroupDeleteRequest)
	if !ok {
		t.Fatalf("delete payload type = %T, want hub.GroupDeleteRequest", client.commands[3].payload)
	}
	if client.commands[3].action != "group_delete" || deleteReq.GroupPath != "ops/backend" || !deleteReq.Force {
		t.Fatalf("delete command = %+v req=%+v", client.commands[3], deleteReq)
	}

	result, err = runHubGroupWithClient(context.Background(), client, snapshots, hubGroupOptions{
		Action:         "change",
		NodeID:         "work",
		GroupPath:      "ops/backend",
		DestParentPath: "platform",
	})
	if err != nil {
		t.Fatalf("runHubGroupWithClient change: %v", err)
	}
	if result.Path != "platform/backend" || result.OldPath != "ops/backend" {
		t.Fatalf("change result = %+v", result)
	}
	reparentReq, ok := client.commands[4].payload.(hub.GroupReparentRequest)
	if !ok {
		t.Fatalf("reparent payload type = %T, want hub.GroupReparentRequest", client.commands[4].payload)
	}
	if client.commands[4].action != "group_reparent" || reparentReq.GroupPath != "ops/backend" || reparentReq.DestParentPath != "platform" {
		t.Fatalf("reparent command = %+v req=%+v", client.commands[4], reparentReq)
	}

	pos := 1
	result, err = runHubGroupWithClient(context.Background(), client, snapshots, hubGroupOptions{
		Action:    "reorder",
		NodeID:    "work",
		GroupPath: "platform/backend",
		Position:  &pos,
	})
	if err != nil {
		t.Fatalf("runHubGroupWithClient reorder: %v", err)
	}
	if result.Path != "platform/backend" || result.FromPosition != 2 || result.ToPosition != 1 {
		t.Fatalf("reorder result = %+v", result)
	}
	reorderReq, ok := client.commands[5].payload.(hub.GroupReorderRequest)
	if !ok {
		t.Fatalf("reorder payload type = %T, want hub.GroupReorderRequest", client.commands[5].payload)
	}
	if client.commands[5].action != "group_reorder" || reorderReq.GroupPath != "platform/backend" || reorderReq.Position == nil || *reorderReq.Position != 1 {
		t.Fatalf("reorder command = %+v req=%+v", client.commands[5], reorderReq)
	}
}

func TestRunHubMCPActionsUseHubRequests(t *testing.T) {
	client := &fakeHubSessionsClient{
		results: map[string]json.RawMessage{
			"mcp_list":   mustMarshalJSON(t, hub.MCPListResponse{SessionID: "sess_api", Local: []string{"exa"}, Global: []string{"memory"}, User: []string{"browser"}, Catalog: []hub.MCPCatalogEntry{{Name: "slack", Description: "team chat"}, {Name: "exa", Description: "search"}}}),
			"mcp_attach": mustMarshalJSON(t, hub.MCPMutateResponse{SessionID: "sess_api", Name: "slack", Scope: "local"}),
			"mcp_detach": mustMarshalJSON(t, hub.MCPMutateResponse{SessionID: "sess_api", Name: "slack", Scope: "local"}),
			"mcp_move":   mustMarshalJSON(t, hub.MCPMoveResponse{SessionID: "sess_api", Name: "exa", FromScope: "local", ToScope: "global"}),
		},
	}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubMCPWithClient(context.Background(), client, snapshots, hubMCPOptions{
		Action:    "mcp_list",
		NodeID:    "work",
		SessionID: "api",
	})
	if err != nil {
		t.Fatalf("runHubMCPWithClient list: %v", err)
	}
	if result.SessionID != "sess_api" || len(result.Local) != 1 || result.Local[0] != "exa" || result.Global[0] != "memory" || result.User[0] != "browser" {
		t.Fatalf("list result = %+v", result)
	}
	if len(result.Catalog) != 2 || result.Catalog[0].Name != "exa" || result.Catalog[1].Name != "slack" {
		t.Fatalf("catalog result = %+v", result.Catalog)
	}
	listReq, ok := client.commands[0].payload.(hub.MCPListRequest)
	if !ok {
		t.Fatalf("list payload type = %T, want hub.MCPListRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_work" || client.commands[0].action != "mcp_list" || listReq.SessionID != "sess_api" {
		t.Fatalf("list command = %+v req=%+v", client.commands[0], listReq)
	}

	result, err = runHubMCPWithClient(context.Background(), client, snapshots, hubMCPOptions{
		Action:    "mcp_attach",
		NodeID:    "work",
		SessionID: "api",
		Name:      "slack",
		Scope:     "local",
		Restart:   true,
	})
	if err != nil {
		t.Fatalf("runHubMCPWithClient attach: %v", err)
	}
	if !result.Restarted {
		t.Fatalf("attach result = %+v, want Restarted", result)
	}
	attachReq, ok := client.commands[1].payload.(hub.MCPMutateRequest)
	if !ok {
		t.Fatalf("attach payload type = %T, want hub.MCPMutateRequest", client.commands[1].payload)
	}
	if client.commands[1].action != "mcp_attach" || attachReq.SessionID != "sess_api" || attachReq.Name != "slack" || attachReq.Scope != "local" {
		t.Fatalf("attach command = %+v req=%+v", client.commands[1], attachReq)
	}
	restartReq, ok := client.commands[2].payload.(map[string]string)
	if !ok || client.commands[2].action != "restart" || restartReq["session_id"] != "sess_api" {
		t.Fatalf("restart command = %+v payload=%+v", client.commands[2], client.commands[2].payload)
	}

	result, err = runHubMCPWithClient(context.Background(), client, snapshots, hubMCPOptions{
		Action:    "mcp_detach",
		NodeID:    "work",
		SessionID: "api",
		Name:      "slack",
		Scope:     "global",
	})
	if err != nil {
		t.Fatalf("runHubMCPWithClient detach: %v", err)
	}
	detachReq, ok := client.commands[3].payload.(hub.MCPMutateRequest)
	if !ok {
		t.Fatalf("detach payload type = %T, want hub.MCPMutateRequest", client.commands[3].payload)
	}
	if client.commands[3].action != "mcp_detach" || detachReq.Scope != "global" {
		t.Fatalf("detach command = %+v req=%+v", client.commands[3], detachReq)
	}

	result, err = runHubMCPWithClient(context.Background(), client, snapshots, hubMCPOptions{
		Action:    "mcp_move",
		NodeID:    "work",
		SessionID: "api",
		Name:      "exa",
		ToScope:   "global",
	})
	if err != nil {
		t.Fatalf("runHubMCPWithClient move: %v", err)
	}
	if result.FromScope != "local" || result.ToScope != "global" {
		t.Fatalf("move result = %+v", result)
	}
	if client.commands[4].action != "mcp_list" {
		t.Fatalf("move should auto-detect current scope via mcp_list, got %+v", client.commands[4])
	}
	moveReq, ok := client.commands[5].payload.(hub.MCPMoveRequest)
	if !ok {
		t.Fatalf("move payload type = %T, want hub.MCPMoveRequest", client.commands[5].payload)
	}
	if client.commands[5].action != "mcp_move" || moveReq.FromScope != "local" || moveReq.ToScope != "global" {
		t.Fatalf("move command = %+v req=%+v", client.commands[5], moveReq)
	}
}

func TestRunHubSkillActionsUseHubRequests(t *testing.T) {
	client := &fakeHubSessionsClient{
		results: map[string]json.RawMessage{
			"skill_list":   mustMarshalJSON(t, hub.SkillListResponse{SessionID: "sess_api", Catalog: []session.SkillCandidate{{ID: "pool/alpha", Name: "alpha", Source: "pool"}}, Attached: []session.ProjectSkillAttachment{{ID: "pool/beta", Name: "beta", Source: "pool"}}}),
			"skill_attach": mustMarshalJSON(t, hub.SkillMutateResponse{SessionID: "sess_api", Skill: &session.ProjectSkillAttachment{ID: "pool/alpha", Name: "alpha", Source: "pool"}}),
			"skill_detach": mustMarshalJSON(t, hub.SkillMutateResponse{SessionID: "sess_api", Skill: &session.ProjectSkillAttachment{ID: "pool/beta", Name: "beta", Source: "pool"}}),
		},
	}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSkillWithClient(context.Background(), client, snapshots, hubSkillOptions{
		Action:    "skill_list",
		NodeID:    "work",
		SessionID: "api",
	})
	if err != nil {
		t.Fatalf("runHubSkillWithClient list: %v", err)
	}
	if result.SessionID != "sess_api" || result.Catalog[0].Name != "alpha" || result.Attached[0].Name != "beta" {
		t.Fatalf("list result = %+v", result)
	}
	listReq, ok := client.commands[0].payload.(hub.SkillListRequest)
	if !ok {
		t.Fatalf("list payload type = %T, want hub.SkillListRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_work" || client.commands[0].action != "skill_list" || listReq.SessionID != "sess_api" {
		t.Fatalf("list command = %+v req=%+v", client.commands[0], listReq)
	}

	result, err = runHubSkillWithClient(context.Background(), client, snapshots, hubSkillOptions{
		Action:    "skill_attach",
		NodeID:    "work",
		SessionID: "api",
		Name:      "alpha",
		Source:    "pool",
		Restart:   true,
	})
	if err != nil {
		t.Fatalf("runHubSkillWithClient attach: %v", err)
	}
	if !result.Restarted {
		t.Fatalf("attach result = %+v, want Restarted", result)
	}
	attachReq, ok := client.commands[1].payload.(hub.SkillMutateRequest)
	if !ok {
		t.Fatalf("attach payload type = %T, want hub.SkillMutateRequest", client.commands[1].payload)
	}
	if client.commands[1].action != "skill_attach" || attachReq.SessionID != "sess_api" || attachReq.Name != "alpha" || attachReq.Source != "pool" {
		t.Fatalf("attach command = %+v req=%+v", client.commands[1], attachReq)
	}
	restartReq, ok := client.commands[2].payload.(map[string]string)
	if !ok || client.commands[2].action != "restart" || restartReq["session_id"] != "sess_api" {
		t.Fatalf("restart command = %+v payload=%+v", client.commands[2], client.commands[2].payload)
	}

	result, err = runHubSkillWithClient(context.Background(), client, snapshots, hubSkillOptions{
		Action:    "skill_detach",
		NodeID:    "work",
		SessionID: "api",
		Name:      "beta",
		Source:    "pool",
	})
	if err != nil {
		t.Fatalf("runHubSkillWithClient detach: %v", err)
	}
	detachReq, ok := client.commands[3].payload.(hub.SkillMutateRequest)
	if !ok {
		t.Fatalf("detach payload type = %T, want hub.SkillMutateRequest", client.commands[3].payload)
	}
	if client.commands[3].action != "skill_detach" || detachReq.SessionID != "sess_api" || detachReq.Name != "beta" || detachReq.Source != "pool" {
		t.Fatalf("detach command = %+v req=%+v", client.commands[3], detachReq)
	}
}

func TestRunHubPluginActionsUseHubRequests(t *testing.T) {
	client := &fakeHubSessionsClient{
		results: map[string]json.RawMessage{
			"plugin_list":   mustMarshalJSON(t, hub.PluginListResponse{SessionID: "sess_api", Catalog: []hub.PluginCatalogEntry{{Name: "octopus", ID: "octopus@local"}}, Plugins: []string{"discord"}}),
			"plugin_attach": mustMarshalJSON(t, hub.PluginMutateResponse{SessionID: "sess_api", Plugins: []string{"discord", "octopus"}}),
			"plugin_detach": mustMarshalJSON(t, hub.PluginMutateResponse{SessionID: "sess_api", Plugins: []string{"discord"}}),
		},
	}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubPluginWithClient(context.Background(), client, snapshots, hubPluginOptions{
		Action:    "plugin_list",
		NodeID:    "work",
		SessionID: "api",
	})
	if err != nil {
		t.Fatalf("runHubPluginWithClient list: %v", err)
	}
	if result.SessionID != "sess_api" || result.Catalog[0].Name != "octopus" || result.Plugins[0] != "discord" {
		t.Fatalf("list result = %+v", result)
	}
	listReq, ok := client.commands[0].payload.(hub.PluginListRequest)
	if !ok {
		t.Fatalf("list payload type = %T, want hub.PluginListRequest", client.commands[0].payload)
	}
	if client.commands[0].nodeID != "node_work" || client.commands[0].action != "plugin_list" || listReq.SessionID != "sess_api" {
		t.Fatalf("list command = %+v req=%+v", client.commands[0], listReq)
	}

	result, err = runHubPluginWithClient(context.Background(), client, snapshots, hubPluginOptions{
		Action:        "plugin_attach",
		NodeID:        "work",
		SessionID:     "api",
		Name:          "octopus",
		NoChannelLink: true,
		Restart:       true,
	})
	if err != nil {
		t.Fatalf("runHubPluginWithClient attach: %v", err)
	}
	if !result.Restarted {
		t.Fatalf("attach result = %+v, want Restarted", result)
	}
	attachReq, ok := client.commands[1].payload.(hub.PluginMutateRequest)
	if !ok {
		t.Fatalf("attach payload type = %T, want hub.PluginMutateRequest", client.commands[1].payload)
	}
	if client.commands[1].action != "plugin_attach" || attachReq.SessionID != "sess_api" || attachReq.Name != "octopus" || !attachReq.NoChannelLink {
		t.Fatalf("attach command = %+v req=%+v", client.commands[1], attachReq)
	}
	restartReq, ok := client.commands[2].payload.(map[string]string)
	if !ok || client.commands[2].action != "restart" || restartReq["session_id"] != "sess_api" {
		t.Fatalf("restart command = %+v payload=%+v", client.commands[2], client.commands[2].payload)
	}

	result, err = runHubPluginWithClient(context.Background(), client, snapshots, hubPluginOptions{
		Action:    "plugin_detach",
		NodeID:    "work",
		SessionID: "api",
		Name:      "octopus",
	})
	if err != nil {
		t.Fatalf("runHubPluginWithClient detach: %v", err)
	}
	detachReq, ok := client.commands[3].payload.(hub.PluginMutateRequest)
	if !ok {
		t.Fatalf("detach payload type = %T, want hub.PluginMutateRequest", client.commands[3].payload)
	}
	if client.commands[3].action != "plugin_detach" || detachReq.SessionID != "sess_api" || detachReq.Name != "octopus" {
		t.Fatalf("detach command = %+v req=%+v", client.commands[3], detachReq)
	}
}

func TestRunHubSessionApproveUsesHubSendOneAction(t *testing.T) {
	client := &fakeHubSessionsClient{}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:    "approve",
		NodeID:    "work",
		SessionID: "api",
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient approve: %v", err)
	}
	if result.SessionID != "sess_api" || result.Action != "approve" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "send" {
		t.Fatalf("commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(map[string]string)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]string", client.commands[0].payload)
	}
	if payload["session_id"] != "sess_api" || payload["message"] != "1" {
		t.Fatalf("approve payload = %+v", payload)
	}
}

func TestRunHubSessionUnreadUsesHubMarkUnreadAction(t *testing.T) {
	client := &fakeHubSessionsClient{}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:    "mark_unread",
		NodeID:    "work",
		SessionID: "api",
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient mark_unread: %v", err)
	}
	if result.SessionID != "sess_api" || result.Action != "mark_unread" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "mark_unread" {
		t.Fatalf("commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(map[string]string)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]string", client.commands[0].payload)
	}
	if payload["session_id"] != "sess_api" {
		t.Fatalf("mark_unread payload = %+v", payload)
	}
}

func TestListHubSessionRowsSupportsNodeShortName(t *testing.T) {
	snapshots := []hub.NodeSessions{
		{
			Node: hub.Node{ID: "node_work", Name: "work"},
			Sessions: []hub.SessionInfo{{
				ID:        "sess_1",
				Title:     "api",
				Tool:      "codex",
				Status:    "waiting",
				GroupPath: "services",
			}},
		},
		{
			Node:     hub.Node{ID: "node_empty", Name: "empty"},
			Sessions: nil,
		},
	}

	rows, err := listHubSessionRows(snapshots, "work")
	if err != nil {
		t.Fatalf("listHubSessionRows: %v", err)
	}
	if len(rows) != 1 || rows[0].NodeID != "node_work" || rows[0].ID != "sess_1" || rows[0].Title != "api" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestRunHubSessionCreateUsesEmptyNodeShortNameAndCanAttach(t *testing.T) {
	client := &fakeHubSessionsClient{}
	snapshots := []hub.NodeSessions{{Node: hub.Node{ID: "node_empty", Name: "empty"}, Sessions: nil}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action: "create",
		NodeID: "empty",
		Title:  "new task",
		Tool:   "codex",
		CWD:    ".",
		Group:  "default",
		AdditionalPaths: []string{
			"/repos/lib",
			"  /repos/ops  ",
			"/repos/lib",
			"",
		},
		Attach: true,
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient create: %v", err)
	}
	if result.SessionID != "created_1" || result.NodeID != "node_empty" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_empty" || client.commands[0].action != "create" {
		t.Fatalf("commands = %+v", client.commands)
	}
	req, ok := client.commands[0].payload.(hub.CreateSessionRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.CreateSessionRequest", client.commands[0].payload)
	}
	if req.Title != "new task" || req.Tool != "codex" || req.ProjectPath != "." || req.GroupPath != "default" {
		t.Fatalf("create request = %+v", req)
	}
	if len(req.AdditionalPaths) != 2 || req.AdditionalPaths[0] != "/repos/lib" || req.AdditionalPaths[1] != "/repos/ops" {
		t.Fatalf("create additional paths = %+v, want [/repos/lib /repos/ops]", req.AdditionalPaths)
	}
	if client.attachNodeID != "node_empty" || client.attachSessionID != "created_1" {
		t.Fatalf("attach = %q/%q", client.attachNodeID, client.attachSessionID)
	}
}

func TestRunHubSessionUndoDeleteUsesNodeOnlyAction(t *testing.T) {
	client := &fakeHubSessionsClient{results: map[string]json.RawMessage{
		"undo_delete": json.RawMessage(`{"session_id":"restored_1"}`),
	}}
	snapshots := []hub.NodeSessions{{Node: hub.Node{ID: "node_work", Name: "work"}, Sessions: nil}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action: "undo_delete",
		NodeID: "work",
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient undo_delete: %v", err)
	}
	if result.Action != "undo_delete" || result.NodeID != "node_work" || result.SessionID != "restored_1" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "undo_delete" || client.commands[0].payload != nil {
		t.Fatalf("commands = %+v", client.commands)
	}
}

func TestRunHubSessionActionResolvesSessionTitle(t *testing.T) {
	client := &fakeHubSessionsClient{}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:    "rename",
		NodeID:    "work",
		SessionID: "api",
		Title:     "api v2",
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient rename: %v", err)
	}
	if result.SessionID != "sess_api" || result.SessionTitle != "api v2" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "rename" {
		t.Fatalf("commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(map[string]string)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]string", client.commands[0].payload)
	}
	if payload["session_id"] != "sess_api" || payload["title"] != "api v2" {
		t.Fatalf("rename payload = %+v", payload)
	}
}

func TestRunHubSessionForkUsesHubForkAction(t *testing.T) {
	client := &fakeHubSessionsClient{
		results: map[string]json.RawMessage{
			"fork": json.RawMessage(`{"session_id":"forked_1"}`),
		},
	}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:    "fork",
		NodeID:    "work",
		SessionID: "api",
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient fork: %v", err)
	}
	if result.SessionID != "forked_1" || result.SessionTitle != "api (fork)" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "fork" {
		t.Fatalf("commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(hub.ForkSessionRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.ForkSessionRequest", client.commands[0].payload)
	}
	if payload.SessionID != "sess_api" || payload.HasOptions() {
		t.Fatalf("fork payload = %+v", payload)
	}
}

func TestRunHubSessionForkWithOptionsUsesHubForkRequest(t *testing.T) {
	client := &fakeHubSessionsClient{
		results: map[string]json.RawMessage{
			"fork": json.RawMessage(`{"session_id":"forked_1"}`),
		},
	}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:      "fork",
		NodeID:      "work",
		SessionID:   "api",
		Title:       "api experiment",
		Group:       "ops",
		Worktree:    true,
		Branch:      "fork/api-experiment",
		WithState:   true,
		WithIgnored: true,
		Sandbox:     true,
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient fork options: %v", err)
	}
	if result.SessionID != "forked_1" || result.SessionTitle != "api experiment" {
		t.Fatalf("result = %+v", result)
	}
	payload, ok := client.commands[0].payload.(hub.ForkSessionRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.ForkSessionRequest", client.commands[0].payload)
	}
	if payload.SessionID != "sess_api" || payload.Title != "api experiment" || payload.GroupPath != "ops" ||
		payload.Branch != "fork/api-experiment" || !payload.Worktree || !payload.WithState || !payload.WithIgnored || !payload.Sandbox {
		t.Fatalf("fork options payload = %+v", payload)
	}
}

func TestRunHubSessionWorktreeFinishUsesHubRequest(t *testing.T) {
	client := &fakeHubSessionsClient{}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:     "worktree_finish",
		NodeID:     "work",
		SessionID:  "api",
		Into:       "main",
		NoMerge:    true,
		KeepBranch: true,
		Force:      true,
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient worktree_finish: %v", err)
	}
	if result.SessionID != "sess_api" || result.Action != "worktree_finish" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "worktree_finish" {
		t.Fatalf("commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(hub.WorktreeFinishRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.WorktreeFinishRequest", client.commands[0].payload)
	}
	if payload.SessionID != "sess_api" || payload.Into != "main" || !payload.NoMerge || !payload.KeepBranch || !payload.Force {
		t.Fatalf("worktree_finish payload = %+v", payload)
	}
}

func TestRunHubSessionWorktreeSetupUsesHubRequest(t *testing.T) {
	client := &fakeHubSessionsClient{}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:    "worktree_setup",
		NodeID:    "work",
		SessionID: "api",
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient worktree_setup: %v", err)
	}
	if result.SessionID != "sess_api" || result.Action != "worktree_setup" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "worktree_setup" {
		t.Fatalf("commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(hub.WorktreeSetupRequest)
	if !ok {
		t.Fatalf("payload type = %T, want hub.WorktreeSetupRequest", client.commands[0].payload)
	}
	if payload.SessionID != "sess_api" {
		t.Fatalf("worktree_setup payload = %+v", payload)
	}
}

func TestRunHubSessionAttachUsesHubAttach(t *testing.T) {
	client := &fakeHubSessionsClient{}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	if _, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{Action: "attach", NodeID: "work", SessionID: "api"}); err != nil {
		t.Fatalf("runHubSessionWithClient attach: %v", err)
	}
	if client.attachNodeID != "node_work" || client.attachSessionID != "sess_api" {
		t.Fatalf("attach = %q/%q", client.attachNodeID, client.attachSessionID)
	}
	if len(client.commands) != 0 {
		t.Fatalf("attach should not send command payloads, got %+v", client.commands)
	}
}

func TestRunHubSessionSandboxShellUsesCommandAndAttachesToken(t *testing.T) {
	client := &fakeHubSessionsClient{results: map[string]json.RawMessage{
		"sandbox_shell": mustMarshalJSON(t, hub.SandboxShellResponse{SessionID: "sess_api", AttachSessionID: "tmuxattach-token"}),
	}}
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{{
			ID:    "sess_api",
			Title: "api",
		}},
	}}

	result, err := runHubSessionWithClient(context.Background(), client, snapshots, hubSessionOptions{
		Action:    "sandbox_shell",
		NodeID:    "work",
		SessionID: "api",
		Attach:    true,
	})
	if err != nil {
		t.Fatalf("runHubSessionWithClient sandbox_shell: %v", err)
	}
	if result.Action != "sandbox_shell" || result.SessionID != "sess_api" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.commands) != 1 || client.commands[0].nodeID != "node_work" || client.commands[0].action != "sandbox_shell" {
		t.Fatalf("commands = %+v", client.commands)
	}
	payload, ok := client.commands[0].payload.(hub.SandboxShellRequest)
	if !ok || payload.SessionID != "sess_api" {
		t.Fatalf("sandbox_shell payload = %#v", client.commands[0].payload)
	}
	if client.attachNodeID != "node_work" || client.attachSessionID != "tmuxattach-token" {
		t.Fatalf("attach = %q/%q, want node_work/tmuxattach-token", client.attachNodeID, client.attachSessionID)
	}
}

func TestResolveHubSessionTargetRejectsAmbiguousSessionTitle(t *testing.T) {
	snapshots := []hub.NodeSessions{{
		Node: hub.Node{ID: "node_work", Name: "work"},
		Sessions: []hub.SessionInfo{
			{ID: "sess_a", Title: "api"},
			{ID: "sess_b", Title: "api"},
		},
	}}

	_, err := resolveHubSessionTarget(snapshots, "work", "api")
	if err == nil || !strings.Contains(err.Error(), "multiple") || !strings.Contains(err.Error(), "sess_a") || !strings.Contains(err.Error(), "sess_b") {
		t.Fatalf("error = %v, want ambiguous session ids", err)
	}
}
