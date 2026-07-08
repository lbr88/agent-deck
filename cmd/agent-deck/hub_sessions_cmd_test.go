package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/hub"
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

func TestHubCommandRoutesSessions(t *testing.T) {
	data, err := os.ReadFile("hub_cmd.go")
	if err != nil {
		t.Fatalf("read hub_cmd.go: %v", err)
	}
	if !strings.Contains(string(data), `case "sessions":`) || !strings.Contains(string(data), "handleHubSessions(") {
		t.Fatalf("hub_cmd.go must route agent-deck hub sessions")
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
		"notes",
		"close",
		"restart",
		"restart-fresh",
		"fork",
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
	if client.attachNodeID != "node_empty" || client.attachSessionID != "created_1" {
		t.Fatalf("attach = %q/%q", client.attachNodeID, client.attachSessionID)
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
	payload, ok := client.commands[0].payload.(map[string]string)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]string", client.commands[0].payload)
	}
	if payload["session_id"] != "sess_api" {
		t.Fatalf("fork payload = %+v", payload)
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
