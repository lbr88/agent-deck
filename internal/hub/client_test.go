package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
	"github.com/gorilla/websocket"
)

type fakeSessionSource struct {
	sessions []SessionInfo
}

func (f fakeSessionSource) Snapshot(context.Context) (SnapshotPayload, error) {
	return SnapshotPayload{SentAt: time.Unix(456, 0).UTC(), Sessions: f.sessions}, nil
}

func TestClientBuildsSnapshotFromSource(t *testing.T) {
	src := fakeSessionSource{sessions: []SessionInfo{{ID: "s1", Title: "worker", GroupPath: "default"}}}
	c := &Client{source: src}
	snap, err := c.buildSnapshot(context.Background())
	if err != nil {
		t.Fatalf("buildSnapshot: %v", err)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].Title != "worker" {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestClientBuildsEmptySnapshotWithoutSource(t *testing.T) {
	c := NewClient(ClientConfig{}, nil)
	snap, err := c.buildSnapshot(context.Background())
	if err != nil {
		t.Fatalf("buildSnapshot: %v", err)
	}
	if snap.SentAt.IsZero() {
		t.Fatal("SentAt is zero")
	}
	if len(snap.Sessions) != 0 {
		t.Fatalf("Sessions length = %d, want 0", len(snap.Sessions))
	}
}

func TestSessionInfoFromRowCarriesRichNativeMetadata(t *testing.T) {
	yolo := true
	sandboxJSON := json.RawMessage(`{"enabled":true,"image":"sandbox:latest"}`)
	toolOptionsJSON := json.RawMessage(`{"approvalPolicy":"never"}`)
	toolData := statedb.MarshalToolData(
		"claude-session", time.Unix(10, 0),
		"gemini-session", time.Unix(20, 0),
		&yolo, "gemini-3.1",
		"opencode-session", time.Unix(30, 0),
		"codex-session", time.Unix(40, 0),
		"latest prompt", "notes", []string{"github"},
		toolOptionsJSON,
		sandboxJSON, "container_1",
		"ssh-host", "/ssh/path",
		true, []string{"/repo/lib"},
		"/tmp/multi", []statedb.MultiRepoWorktreeData{{
			OriginalPath: "/repo/app",
			WorktreePath: "/tmp/multi/app",
			RepoRoot:     "/repo",
			Branch:       "feature",
		}},
		[]string{"reviews"}, []string{"--fast"}, []string{"plugin-a"}, true, nil,
		"cyan",
	)
	row := &statedb.InstanceRow{
		ID:                 "s1",
		Title:              "api",
		ProjectPath:        "/repo/app",
		GroupPath:          "ops",
		Command:            "codex --ask-for-approval never",
		Wrapper:            "wrapper",
		Tool:               "codex",
		Status:             "waiting",
		ParentSessionID:    "conductor-1",
		IsConductor:        true,
		TmuxSession:        "tmux-s1",
		TmuxSocketName:     "agent-deck",
		NoTransitionNotify: true,
		TitleLocked:        true,
		WorktreePath:       "/worktrees/api",
		WorktreeRepo:       "/repo",
		WorktreeBranch:     "feature",
		ToolData:           toolData,
	}

	got := sessionInfoFromRow(row)

	if got.Command != row.Command || got.Wrapper != row.Wrapper || got.TmuxSession != row.TmuxSession || got.TmuxSocketName != row.TmuxSocketName {
		t.Fatalf("core rich metadata missing: %+v", got)
	}
	if got.Color != "cyan" || got.LatestPrompt != "latest prompt" || got.Notes != "notes" {
		t.Fatalf("text metadata = color %q prompt %q notes %q", got.Color, got.LatestPrompt, got.Notes)
	}
	if got.ClaudeSessionID != "claude-session" || got.GeminiSessionID != "gemini-session" || got.GeminiModel != "gemini-3.1" ||
		got.OpenCodeSessionID != "opencode-session" || got.CodexSessionID != "codex-session" {
		t.Fatalf("tool session metadata = %+v", got)
	}
	if got.ParentSessionID != "conductor-1" || !got.IsConductor {
		t.Fatalf("conductor topology = parent %q isConductor %v, want conductor-1/true", got.ParentSessionID, got.IsConductor)
	}
	if got.GeminiYoloMode == nil || !*got.GeminiYoloMode {
		t.Fatalf("GeminiYoloMode = %v, want true", got.GeminiYoloMode)
	}
	if len(got.LoadedMCPNames) != 1 || got.LoadedMCPNames[0] != "github" || len(got.Plugins) != 1 || got.Plugins[0] != "plugin-a" ||
		len(got.Channels) != 1 || got.Channels[0] != "reviews" || len(got.ExtraArgs) != 1 || got.ExtraArgs[0] != "--fast" ||
		!got.PluginChannelLinkDisabled {
		t.Fatalf("list metadata not projected: %+v", got)
	}
	if string(got.ToolOptionsJSON) != string(toolOptionsJSON) || string(got.Sandbox) != string(sandboxJSON) ||
		got.SandboxContainer != "container_1" || got.SSHHost != "ssh-host" || got.SSHRemotePath != "/ssh/path" {
		t.Fatalf("config metadata missing: toolOptions=%s sandbox=%s container=%q ssh=%q/%q", got.ToolOptionsJSON, got.Sandbox, got.SandboxContainer, got.SSHHost, got.SSHRemotePath)
	}
	if !got.MultiRepoEnabled || len(got.AdditionalPaths) != 1 || got.AdditionalPaths[0] != "/repo/lib" ||
		got.MultiRepoTempDir != "/tmp/multi" || len(got.MultiRepoWorktrees) != 1 || got.MultiRepoWorktrees[0].WorktreePath != "/tmp/multi/app" {
		t.Fatalf("multi-repo metadata missing: %+v", got)
	}
	if !got.TitleLocked || !got.NoTransitionNotify {
		t.Fatalf("flags = titleLocked %v noTransition %v, want true/true", got.TitleLocked, got.NoTransitionNotify)
	}
}

func TestClientRejectsPlaintextURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := NewClient(ClientConfig{
		URL:    "ws://hub.local/ws/node",
		NodeID: "node_1",
		Token:  "node_secret",
	}, nil).Connect(ctx)

	if err == nil {
		t.Fatal("Connect accepted ws:// URL, want wss:// rejection")
	}
	if !strings.Contains(err.Error(), "wss://") {
		t.Fatalf("Connect error = %q, want wss:// guidance", err.Error())
	}
}

func TestClientConnectSendsHelloSnapshotAndHeartbeat(t *testing.T) {
	messages := make(chan observedMessage, 8)
	authHeaders := make(chan string, 1)
	nodeIDs := make(chan string, 1)
	done := make(chan struct{})

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nodeIDs <- r.URL.Query().Get("node_id")
		authHeaders <- r.Header.Get("Authorization")

		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		welcome, _ := MarshalEnvelope(MsgWelcome, "node_1", WelcomePayload{NodeID: "node_1", NodeName: "laptop"})
		_ = conn.WriteJSON(welcome)

		for {
			var env Envelope
			if err := conn.ReadJSON(&env); err != nil {
				close(done)
				return
			}
			messages <- observedMessage{envelope: env, payload: env.Payload}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	client := NewClient(ClientConfig{
		URL:               "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:            " node_1 ",
		NodeName:          " laptop ",
		Token:             " node_secret ",
		Version:           "test",
		TLSSkipVerify:     true,
		HeartbeatInterval: 10 * time.Millisecond,
		SnapshotInterval:  time.Hour,
	}, fakeSessionSource{sessions: []SessionInfo{{ID: "s1", Title: "worker", GroupPath: "default"}}})
	go func() { errCh <- client.Connect(ctx) }()

	if got := waitString(t, nodeIDs); got != "node_1" {
		t.Fatalf("node_id query = %q, want node_1", got)
	}
	if got := waitString(t, authHeaders); got != "Bearer node_secret" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
	hello := assertNextMessageType(t, messages, MsgHello)
	assertHelloHasNoToken(t, hello, "node_secret")
	snapshot := assertNextMessageType(t, messages, MsgSnapshot)
	var payload SnapshotPayload
	if err := json.Unmarshal(snapshot.payload, &payload); err != nil {
		t.Fatalf("snapshot payload: %v", err)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].Title != "worker" {
		t.Fatalf("snapshot payload = %+v", payload)
	}
	assertNextMessageType(t, messages, MsgHeartbeat)

	cancel()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Connect returned error after context cancellation: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server did not observe websocket close")
	}
}

func TestClientHelloDoesNotIncludeNodeToken(t *testing.T) {
	messages := make(chan observedMessage, 4)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return
		}
		messages <- observedMessage{envelope: env, payload: env.Payload}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewClient(ClientConfig{
			URL:               "wss://" + strings.TrimPrefix(server.URL, "https://"),
			NodeID:            "node_1",
			NodeName:          "laptop",
			Token:             "node_secret",
			TLSSkipVerify:     true,
			HeartbeatInterval: time.Hour,
			SnapshotInterval:  time.Hour,
		}, nil).Connect(ctx)
	}()

	hello := assertNextMessageType(t, messages, MsgHello)
	assertHelloHasNoToken(t, hello, "node_secret")
	cancel()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Connect returned error after context cancellation: %v", err)
	}
}

func TestClientDispatchesSnapshotCallback(t *testing.T) {
	snapshots := make(chan NodeSessions, 1)
	client := NewClient(ClientConfig{
		OnSnapshot: func(snapshot NodeSessions) {
			snapshots <- snapshot
		},
	}, nil)
	payload := SnapshotPayload{
		NodeID:   "node_remote",
		NodeName: "server1",
		Admin:    true,
		SentAt:   time.Unix(123, 0).UTC(),
		Sessions: []SessionInfo{{
			ID:        "s1",
			Title:     "worker",
			Status:    "waiting",
			GroupPath: "ops",
		}},
		Groups: []GroupInfo{{
			Name: "empty",
			Path: "empty",
		}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal snapshot payload: %v", err)
	}

	client.dispatch(Envelope{Version: ProtocolVersion, Type: MsgSnapshot, NodeID: "node_remote", Payload: raw})

	got := waitNodeSessions(t, snapshots)
	if got.Node.ID != "node_remote" || got.Node.Name != "server1" || !got.Node.Admin {
		t.Fatalf("snapshot node = %+v", got.Node)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].Title != "worker" {
		t.Fatalf("snapshot sessions = %+v", got.Sessions)
	}
	if len(got.Groups) != 1 || got.Groups[0].Path != "empty" {
		t.Fatalf("snapshot groups = %+v", got.Groups)
	}
}

func TestClientDispatchesWelcomeCallback(t *testing.T) {
	welcomes := make(chan WelcomePayload, 1)
	client := NewClient(ClientConfig{
		OnWelcome: func(welcome WelcomePayload) {
			welcomes <- welcome
		},
	}, nil)
	raw, err := json.Marshal(WelcomePayload{NodeID: "node_admin", NodeName: "admin", Admin: true})
	if err != nil {
		t.Fatalf("Marshal welcome: %v", err)
	}

	client.dispatch(Envelope{Version: ProtocolVersion, Type: MsgWelcome, NodeID: "node_admin", Payload: raw})

	select {
	case got := <-welcomes:
		if got.NodeID != "node_admin" || got.NodeName != "admin" || !got.Admin {
			t.Fatalf("welcome = %+v, want admin node", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for welcome callback")
	}
}

func TestClientRenameNodeUsesAuthenticatedAdminAPI(t *testing.T) {
	var gotAuth, gotNodeIDQuery string
	var gotReq clientRenameNodeRequest
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/nodes/rename" {
			t.Fatalf("path = %s, want /api/nodes/rename", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotNodeIDQuery = r.URL.Query().Get("node_id")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"node_remote","name":"desktop","status":"online","admin":true}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		URL:           "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:        " node_admin ",
		Token:         " admin_secret ",
		TLSSkipVerify: true,
	}, nil)
	got, err := client.RenameNode(context.Background(), " node_remote ", " desktop ")
	if err != nil {
		t.Fatalf("RenameNode: %v", err)
	}
	if got.ID != "node_remote" || got.Name != "desktop" || got.Status != "online" || !got.Admin {
		t.Fatalf("renamed node = %+v", got)
	}
	if gotAuth != "Bearer admin_secret" || gotNodeIDQuery != "node_admin" {
		t.Fatalf("auth/query = %q/%q, want bearer admin_secret/node_admin", gotAuth, gotNodeIDQuery)
	}
	if gotReq.NodeID != "node_remote" || gotReq.Name != "desktop" {
		t.Fatalf("request = %+v, want node_remote/desktop", gotReq)
	}
}

func TestClientNodeAdminActionsUseAuthenticatedAdminAPI(t *testing.T) {
	tests := []struct {
		name       string
		call       func(*Client) (Node, error)
		wantPath   string
		wantAdmin  bool
		wantNodeID string
	}{
		{
			name:       "promote",
			call:       func(c *Client) (Node, error) { return c.PromoteNode(context.Background(), " node_remote ") },
			wantPath:   "/api/nodes/promote",
			wantAdmin:  true,
			wantNodeID: "node_remote",
		},
		{
			name:       "demote",
			call:       func(c *Client) (Node, error) { return c.DemoteNode(context.Background(), " node_remote ") },
			wantPath:   "/api/nodes/demote",
			wantAdmin:  false,
			wantNodeID: "node_remote",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotAuth, gotNodeIDQuery string
			var gotReq clientNodeIDRequest
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != tc.wantPath {
					t.Fatalf("path = %s, want %s", r.URL.Path, tc.wantPath)
				}
				gotAuth = r.Header.Get("Authorization")
				gotNodeIDQuery = r.URL.Query().Get("node_id")
				if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"node_remote","name":"desktop","status":"online","admin":%t}`, tc.wantAdmin)
			}))
			defer server.Close()

			client := NewClient(ClientConfig{
				URL:           "wss://" + strings.TrimPrefix(server.URL, "https://"),
				NodeID:        " node_admin ",
				Token:         " admin_secret ",
				TLSSkipVerify: true,
			}, nil)
			got, err := tc.call(client)
			if err != nil {
				t.Fatalf("%s node: %v", tc.name, err)
			}
			if got.ID != "node_remote" || got.Name != "desktop" || got.Status != "online" || got.Admin != tc.wantAdmin {
				t.Fatalf("node = %+v, want admin=%v", got, tc.wantAdmin)
			}
			if gotAuth != "Bearer admin_secret" || gotNodeIDQuery != "node_admin" {
				t.Fatalf("auth/query = %q/%q, want bearer admin_secret/node_admin", gotAuth, gotNodeIDQuery)
			}
			if gotReq.NodeID != tc.wantNodeID {
				t.Fatalf("request = %+v, want node id %q", gotReq, tc.wantNodeID)
			}
		})
	}
}

func TestClientRevokeNodeUsesAuthenticatedAdminAPI(t *testing.T) {
	var gotAuth, gotNodeIDQuery string
	var gotReq clientNodeIDRequest
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/nodes/revoke" {
			t.Fatalf("path = %s, want /api/nodes/revoke", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotNodeIDQuery = r.URL.Query().Get("node_id")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		URL:           "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:        " node_admin ",
		Token:         " admin_secret ",
		TLSSkipVerify: true,
	}, nil)
	if err := client.RevokeNode(context.Background(), " node_remote "); err != nil {
		t.Fatalf("RevokeNode: %v", err)
	}
	if gotAuth != "Bearer admin_secret" || gotNodeIDQuery != "node_admin" {
		t.Fatalf("auth/query = %q/%q, want bearer admin_secret/node_admin", gotAuth, gotNodeIDQuery)
	}
	if gotReq.NodeID != "node_remote" {
		t.Fatalf("request = %+v, want node_remote", gotReq)
	}
}

func TestClientDispatchesTrustRequestCallback(t *testing.T) {
	requests := make(chan TrustRequestPayload, 1)
	client := NewClient(ClientConfig{
		OnTrustRequest: func(request TrustRequestPayload) {
			requests <- request
		},
	}, nil)
	payload := TrustRequestPayload{
		NodeID:   "node_joining",
		NodeName: "new laptop",
		Version:  "1.0.0",
		OS:       "linux",
		Arch:     "amd64",
		Status:   string(TrustStatusPending),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal trust request payload: %v", err)
	}

	client.dispatch(Envelope{Version: ProtocolVersion, Type: MsgTrustRequest, NodeID: "node_owner", Payload: raw})

	got := waitTrustRequest(t, requests)
	if got.NodeID != "node_joining" || got.NodeName != "new laptop" || got.Status != string(TrustStatusPending) {
		t.Fatalf("trust request = %+v", got)
	}
}

func TestClientTrustDecisionSendsEnvelope(t *testing.T) {
	messages := make(chan observedMessage, 16)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		welcome, _ := MarshalEnvelope(MsgWelcome, "node_owner", WelcomePayload{NodeID: "node_owner", NodeName: "workstation"})
		_ = conn.WriteJSON(welcome)
		for {
			var env Envelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			messages <- observedMessage{envelope: env, payload: env.Payload}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	client := NewClient(ClientConfig{
		URL:               "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:            "node_owner",
		NodeName:          "workstation",
		Token:             "node_secret",
		TLSSkipVerify:     true,
		HeartbeatInterval: time.Hour,
		SnapshotInterval:  time.Hour,
	}, nil)
	go func() { errCh <- client.Connect(ctx) }()

	assertNextMessageType(t, messages, MsgHello)
	assertNextMessageType(t, messages, MsgSnapshot)
	if err := client.TrustDecision(ctx, "node_joining", true); err != nil {
		t.Fatalf("TrustDecision: %v", err)
	}
	decision := assertNextMessageType(t, messages, MsgTrustDecision)
	if decision.envelope.NodeID != "node_owner" {
		t.Fatalf("trust decision NodeID = %q, want node_owner", decision.envelope.NodeID)
	}
	var payload TrustDecisionPayload
	if err := json.Unmarshal(decision.payload, &payload); err != nil {
		t.Fatalf("decode trust decision: %v", err)
	}
	if payload.NodeID != "node_joining" || !payload.Allow {
		t.Fatalf("trust decision payload = %+v", payload)
	}

	cancel()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Connect returned error after context cancellation: %v", err)
	}
}

func TestClientOwnerAttachOpenUsesBackendAndBridgesFrames(t *testing.T) {
	backend := newFakeAttachBackend()
	messages := make(chan observedMessage, 16)
	serverReady := make(chan *websocket.Conn, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverReady <- conn
		defer conn.Close()
		welcome, _ := MarshalEnvelope(MsgWelcome, "node_owner", WelcomePayload{NodeID: "node_owner", NodeName: "workstation"})
		_ = conn.WriteJSON(welcome)
		for {
			var env Envelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			messages <- observedMessage{envelope: env, payload: env.Payload}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	client := NewClient(ClientConfig{
		URL:               "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:            "node_owner",
		NodeName:          "workstation",
		Token:             "node_secret",
		TLSSkipVerify:     true,
		HeartbeatInterval: time.Hour,
		SnapshotInterval:  time.Hour,
		AttachBackend:     backend,
	}, nil)
	go func() { errCh <- client.Connect(ctx) }()

	conn := waitWebSocketConn(t, serverReady)
	assertNextMessageType(t, messages, MsgHello)
	assertNextMessageType(t, messages, MsgSnapshot)

	windowIndex := 2
	open, err := MarshalEnvelope(MsgAttachOpen, "node_requester", AttachOpenPayload{
		StreamID:    "stream_1",
		SessionID:   "sess_1",
		WindowIndex: &windowIndex,
		Cols:        100,
		Rows:        30,
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope open: %v", err)
	}
	if err := conn.WriteJSON(open); err != nil {
		t.Fatalf("server WriteJSON open: %v", err)
	}
	call := backend.waitOpen(t)
	if call.sessionID != "sess_1" || call.size.Cols != 100 || call.size.Rows != 30 || call.windowIndex == nil || *call.windowIndex != 2 {
		t.Fatalf("backend open call = %+v", call)
	}
	ready := assertNextMessageType(t, messages, MsgAttachReady)
	var readyPayload AttachOpenPayload
	if err := json.Unmarshal(ready.payload, &readyPayload); err != nil {
		t.Fatalf("decode ready payload: %v", err)
	}
	if readyPayload.WindowIndex == nil || *readyPayload.WindowIndex != 2 {
		t.Fatalf("ready payload window index = %+v, want 2", readyPayload.WindowIndex)
	}

	backend.stream.emit([]byte("owner-output"))
	output := assertNextMessageType(t, messages, MsgAttachData)
	assertAttachDataBytes(t, output.envelope, "owner-output")

	input, err := MarshalEnvelope(MsgAttachData, "node_requester", NewAttachData("stream_1", []byte("requester-input")))
	if err != nil {
		t.Fatalf("MarshalEnvelope input: %v", err)
	}
	if err := conn.WriteJSON(input); err != nil {
		t.Fatalf("server WriteJSON input: %v", err)
	}
	if got := backend.stream.waitWrite(t); string(got) != "requester-input" {
		t.Fatalf("stream write = %q, want requester-input", got)
	}

	resize, err := MarshalEnvelope(MsgAttachResize, "node_requester", AttachResizePayload{StreamID: "stream_1", Cols: 120, Rows: 40})
	if err != nil {
		t.Fatalf("MarshalEnvelope resize: %v", err)
	}
	if err := conn.WriteJSON(resize); err != nil {
		t.Fatalf("server WriteJSON resize: %v", err)
	}
	if got := backend.stream.waitResize(t); got.Cols != 120 || got.Rows != 40 {
		t.Fatalf("stream resize = %+v, want 120x40", got)
	}

	closeFrame, err := MarshalEnvelope(MsgAttachClose, "node_requester", AttachClosePayload{StreamID: "stream_1", Reason: "detached"})
	if err != nil {
		t.Fatalf("MarshalEnvelope close: %v", err)
	}
	if err := conn.WriteJSON(closeFrame); err != nil {
		t.Fatalf("server WriteJSON close: %v", err)
	}
	backend.stream.waitClosed(t)
	closed := assertNextMessageType(t, messages, MsgAttachClosed)
	if closed.envelope.NodeID != "node_owner" {
		t.Fatalf("closed NodeID = %q, want node_owner", closed.envelope.NodeID)
	}

	cancel()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Connect returned error after context cancellation: %v", err)
	}
}

func TestClientOwnerAttachOpenRejectsDuplicateStreamID(t *testing.T) {
	backend := newFakeAttachBackend()
	messages := make(chan observedMessage, 16)
	serverReady := make(chan *websocket.Conn, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverReady <- conn
		defer conn.Close()
		welcome, _ := MarshalEnvelope(MsgWelcome, "node_owner", WelcomePayload{NodeID: "node_owner", NodeName: "workstation"})
		_ = conn.WriteJSON(welcome)
		for {
			var env Envelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			messages <- observedMessage{envelope: env, payload: env.Payload}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	client := NewClient(ClientConfig{
		URL:               "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:            "node_owner",
		NodeName:          "workstation",
		Token:             "node_secret",
		TLSSkipVerify:     true,
		HeartbeatInterval: time.Hour,
		SnapshotInterval:  time.Hour,
		AttachBackend:     backend,
	}, nil)
	go func() { errCh <- client.Connect(ctx) }()

	conn := waitWebSocketConn(t, serverReady)
	assertNextMessageType(t, messages, MsgHello)
	assertNextMessageType(t, messages, MsgSnapshot)

	first, err := MarshalEnvelope(MsgAttachOpen, "node_requester", AttachOpenPayload{
		StreamID:  "stream_1",
		SessionID: "sess_1",
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope first open: %v", err)
	}
	if err := conn.WriteJSON(first); err != nil {
		t.Fatalf("server WriteJSON first open: %v", err)
	}
	_ = backend.waitOpen(t)
	assertNextMessageType(t, messages, MsgAttachReady)

	duplicate, err := MarshalEnvelope(MsgAttachOpen, "node_requester", AttachOpenPayload{
		StreamID:  "stream_1",
		SessionID: "sess_2",
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope duplicate open: %v", err)
	}
	if err := conn.WriteJSON(duplicate); err != nil {
		t.Fatalf("server WriteJSON duplicate open: %v", err)
	}
	closed := assertNextMessageType(t, messages, MsgAttachClosed)
	var closePayload AttachClosePayload
	if err := json.Unmarshal(closed.payload, &closePayload); err != nil {
		t.Fatalf("decode closed payload: %v", err)
	}
	if closePayload.StreamID != "stream_1" || !strings.Contains(closePayload.Reason, "already exists") {
		t.Fatalf("closed payload = %+v", closePayload)
	}
	backend.assertNoOpen(t)

	input, err := MarshalEnvelope(MsgAttachData, "node_requester", NewAttachData("stream_1", []byte("requester-input")))
	if err != nil {
		t.Fatalf("MarshalEnvelope input: %v", err)
	}
	if err := conn.WriteJSON(input); err != nil {
		t.Fatalf("server WriteJSON input: %v", err)
	}
	if got := backend.stream.waitWrite(t); string(got) != "requester-input" {
		t.Fatalf("stream write = %q, want requester-input", got)
	}

	cancel()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Connect returned error after context cancellation: %v", err)
	}
}

func TestOwnerAttachCloseReasonSanitizesPTYErrors(t *testing.T) {
	raw := fmt.Errorf("read /dev/ptmx: input/output error")
	reason := ownerAttachCloseReason(raw)
	if reason != "remote terminal attach failed" {
		t.Fatalf("ownerAttachCloseReason = %q, want sanitized remote attach failure", reason)
	}
	if strings.Contains(reason, "/dev/ptmx") || strings.Contains(reason, "input/output") {
		t.Fatalf("reason leaked raw PTY error: %q", reason)
	}

	err := attachClosedError(AttachClosePayload{StreamID: "stream_1", Reason: reason})
	if err == nil || !strings.Contains(err.Error(), "remote terminal attach failed") {
		t.Fatalf("attachClosedError = %v, want sanitized attach failure", err)
	}
	if strings.Contains(err.Error(), "/dev/ptmx") || strings.Contains(err.Error(), "input/output") {
		t.Fatalf("requester error leaked raw PTY error: %v", err)
	}

	if eofReason := ownerAttachCloseReason(io.EOF); eofReason != "" {
		t.Fatalf("EOF close reason = %q, want empty normal close reason", eofReason)
	}

	err = attachClosedError(AttachClosePayload{StreamID: "stream_1", Reason: "read /dev/ptmx: input/output error"})
	if err == nil || err.Error() != "hub attach closed: remote terminal attach failed" {
		t.Fatalf("raw close reason was not sanitized: %v", err)
	}
}

func TestClientOwnerCommandDispatchesActionAndReturnsResult(t *testing.T) {
	backend := &fakeActionBackend{}
	messages := make(chan observedMessage, 16)
	serverReady := make(chan *websocket.Conn, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverReady <- conn
		defer conn.Close()
		welcome, _ := MarshalEnvelope(MsgWelcome, "node_owner", WelcomePayload{NodeID: "node_owner", NodeName: "workstation"})
		_ = conn.WriteJSON(welcome)
		for {
			var env Envelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			messages <- observedMessage{envelope: env, payload: env.Payload}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	client := NewClient(ClientConfig{
		URL:               "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:            "node_owner",
		NodeName:          "workstation",
		Token:             "node_secret",
		TLSSkipVerify:     true,
		HeartbeatInterval: time.Hour,
		SnapshotInterval:  time.Hour,
		ActionBackend:     backend,
	}, nil)
	go func() { errCh <- client.Connect(ctx) }()

	conn := waitWebSocketConn(t, serverReady)
	assertNextMessageType(t, messages, MsgHello)
	assertNextMessageType(t, messages, MsgSnapshot)

	actionPayload, err := json.Marshal(map[string]string{"session_id": "sess_1", "message": "run tests"})
	if err != nil {
		t.Fatalf("marshal action payload: %v", err)
	}
	command, err := MarshalEnvelope(MsgCommand, "node_requester", CommandPayload{
		CommandID: "cmd_1",
		Action:    "send",
		Payload:   actionPayload,
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope command: %v", err)
	}
	if err := conn.WriteJSON(command); err != nil {
		t.Fatalf("server WriteJSON command: %v", err)
	}
	result := assertNextMessageType(t, messages, MsgCommandResult)
	var resultPayload CommandResultPayload
	if err := json.Unmarshal(result.payload, &resultPayload); err != nil {
		t.Fatalf("decode command result: %v", err)
	}
	if !resultPayload.OK || resultPayload.CommandID != "cmd_1" {
		t.Fatalf("command result = %+v", resultPayload)
	}
	if backend.sentSessionID != "sess_1" || backend.sentMessage != "run tests" {
		t.Fatalf("backend = %+v", backend)
	}

	cancel()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Connect returned error after context cancellation: %v", err)
	}
}

func TestClientOwnerCommandPublishesSnapshotBeforeMutatingResult(t *testing.T) {
	backend := &fakeActionBackend{createSessionID: "created_1"}
	source := fakeSessionSource{sessions: []SessionInfo{{
		ID:        "created_1",
		Title:     "worker",
		Status:    "running",
		GroupPath: "ops",
	}}}
	messages := make(chan observedMessage, 16)
	serverReady := make(chan *websocket.Conn, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverReady <- conn
		defer conn.Close()
		welcome, _ := MarshalEnvelope(MsgWelcome, "node_owner", WelcomePayload{NodeID: "node_owner", NodeName: "workstation"})
		_ = conn.WriteJSON(welcome)
		for {
			var env Envelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			messages <- observedMessage{envelope: env, payload: env.Payload}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	client := NewClient(ClientConfig{
		URL:               "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:            "node_owner",
		NodeName:          "workstation",
		Token:             "node_secret",
		TLSSkipVerify:     true,
		HeartbeatInterval: time.Hour,
		SnapshotInterval:  time.Hour,
		ActionBackend:     backend,
	}, source)
	go func() { errCh <- client.Connect(ctx) }()

	conn := waitWebSocketConn(t, serverReady)
	assertNextMessageType(t, messages, MsgHello)
	assertNextMessageType(t, messages, MsgSnapshot)

	createPayload, err := json.Marshal(CreateSessionRequest{Title: "worker", Tool: "codex", GroupPath: "ops"})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	command, err := MarshalEnvelope(MsgCommand, "node_requester", CommandPayload{
		CommandID: "cmd_create",
		Action:    "create",
		Payload:   createPayload,
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope command: %v", err)
	}
	if err := conn.WriteJSON(command); err != nil {
		t.Fatalf("server WriteJSON command: %v", err)
	}

	snapshot := assertNextMessageType(t, messages, MsgSnapshot)
	var snapPayload SnapshotPayload
	if err := json.Unmarshal(snapshot.payload, &snapPayload); err != nil {
		t.Fatalf("decode snapshot payload: %v", err)
	}
	if len(snapPayload.Sessions) != 1 || snapPayload.Sessions[0].ID != "created_1" {
		t.Fatalf("snapshot sessions = %+v, want created_1", snapPayload.Sessions)
	}
	result := assertNextMessageType(t, messages, MsgCommandResult)
	var resultPayload CommandResultPayload
	if err := json.Unmarshal(result.payload, &resultPayload); err != nil {
		t.Fatalf("decode command result: %v", err)
	}
	if !resultPayload.OK || resultPayload.CommandID != "cmd_create" {
		t.Fatalf("command result = %+v", resultPayload)
	}

	cancel()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Connect returned error after context cancellation: %v", err)
	}
}

func TestClientCommandSendsCommandAndWaitsForResult(t *testing.T) {
	messages := make(chan observedMessage, 16)
	serverReady := make(chan *websocket.Conn, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverReady <- conn
		defer conn.Close()
		welcome, _ := MarshalEnvelope(MsgWelcome, "node_requester", WelcomePayload{NodeID: "node_requester", NodeName: "laptop"})
		_ = conn.WriteJSON(welcome)
		for {
			var env Envelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			messages <- observedMessage{envelope: env, payload: env.Payload}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	client := NewClient(ClientConfig{
		URL:               "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:            "node_requester",
		NodeName:          "laptop",
		Token:             "node_secret",
		TLSSkipVerify:     true,
		HeartbeatInterval: time.Hour,
		SnapshotInterval:  time.Hour,
	}, nil)
	go func() { errCh <- client.Connect(ctx) }()

	conn := waitWebSocketConn(t, serverReady)
	assertNextMessageType(t, messages, MsgHello)
	assertNextMessageType(t, messages, MsgSnapshot)

	resultCh := make(chan error, 1)
	go func() {
		_, err := client.Command(ctx, "node_owner", "send", map[string]string{
			"session_id": "sess_1",
			"message":    "run tests",
		})
		resultCh <- err
	}()

	command := assertNextMessageType(t, messages, MsgCommand)
	var commandPayload CommandPayload
	if err := json.Unmarshal(command.payload, &commandPayload); err != nil {
		t.Fatalf("decode command payload: %v", err)
	}
	if commandPayload.CommandID == "" || commandPayload.NodeID != "node_owner" || commandPayload.Action != "send" {
		t.Fatalf("command payload = %+v", commandPayload)
	}
	result, err := MarshalEnvelope(MsgCommandResult, "node_owner", CommandResultPayload{CommandID: commandPayload.CommandID, OK: true})
	if err != nil {
		t.Fatalf("MarshalEnvelope result: %v", err)
	}
	if err := conn.WriteJSON(result); err != nil {
		t.Fatalf("server WriteJSON result: %v", err)
	}
	if err := waitErr(t, resultCh); err != nil {
		t.Fatalf("Command returned error: %v", err)
	}

	cancel()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Connect returned error after context cancellation: %v", err)
	}
}

func TestLocalSessionSourceLoadsStoredSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("XDG_DATA_HOME", home+"/.local/share")
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	storage, err := session.NewStorageWithProfile("hub-local")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	now := time.Unix(789, 0).UTC()
	if err := storage.Save([]*session.Instance{{
		ID:               "s1",
		Title:            "worker",
		ProjectPath:      "/repo",
		WorktreePath:     "/repo/.worktrees/worker",
		WorktreeRepoRoot: "/repo",
		WorktreeBranch:   "fork/worker",
		GroupPath:        "default",
		Tool:             "codex",
		Status:           session.StatusWaiting,
		CreatedAt:        now.Add(-time.Hour),
		LastAccessedAt:   now,
		CodexSessionID:   "codex-session-1",
		Notes:            "remote notes",
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	snap, err := (LocalSessionSource{
		Profile: "hub-local",
		substate: func(_ context.Context, row *statedb.InstanceRow) string {
			if row.ID == "s1" {
				return "auth-401"
			}
			return ""
		},
		windows: func(_ context.Context, row *statedb.InstanceRow) []WindowInfo {
			if row.ID == "s1" {
				return []WindowInfo{{Index: 0, Name: "main", Activity: 111}, {Index: 1, Name: "logs", Tool: "codex"}}
			}
			return nil
		},
	}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("Sessions length = %d, want 1", len(snap.Sessions))
	}
	got := snap.Sessions[0]
	if got.ID != "s1" || got.Title != "worker" || got.Tool != "codex" || got.Status != "waiting" {
		t.Fatalf("session mapping = %+v", got)
	}
	if got.Substate != "auth-401" {
		t.Fatalf("Substate = %q, want auth-401", got.Substate)
	}
	if len(got.Windows) != 2 || got.Windows[1].Index != 1 || got.Windows[1].Name != "logs" || got.Windows[1].Tool != "codex" {
		t.Fatalf("Windows = %+v, want remote tmux windows", got.Windows)
	}
	if got.DisplaySessionID != "codex-session-1" {
		t.Fatalf("DisplaySessionID = %q, want codex-session-1", got.DisplaySessionID)
	}
	if got.Notes != "remote notes" {
		t.Fatalf("Notes = %q, want remote notes", got.Notes)
	}
	if got.WorktreePath != "/repo/.worktrees/worker" || got.WorktreeRepoRoot != "/repo" || got.WorktreeBranch != "fork/worker" {
		t.Fatalf("worktree mapping = %+v", got)
	}
	if got.UpdatedAt == nil || !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, now)
	}
}

func TestLocalSessionSourceUsesNativeCodexRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	id := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	storage, err := session.NewStorageWithProfile("hub-codex-title")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	if err := storage.Save([]*session.Instance{{
		ID:             "codex-row",
		Title:          "stale agent deck title",
		ProjectPath:    "/repo",
		GroupPath:      "default",
		Tool:           "codex",
		Command:        "codex",
		Status:         session.StatusWaiting,
		CreatedAt:      time.Now(),
		CodexSessionID: id,
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatalf("mkdir CODEX_HOME: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, ".codex", "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open Codex state: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT NOT NULL)`); err != nil {
		t.Fatalf("create threads: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO threads (id, title) VALUES (?, ?)`, id, "renamed in codex"); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	_ = db.Close()

	snapshot, err := (LocalSessionSource{Profile: "hub-codex-title"}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].Title != "renamed in codex" {
		t.Fatalf("snapshot sessions = %+v", snapshot.Sessions)
	}
}

func TestParseHubWindowList(t *testing.T) {
	got := parseHubWindowList("0\x1f123\x1fmain\n1\x1f456\x1flogs pane\nbad\n2\x1fbad\x1fignored\n")
	if len(got) != 2 {
		t.Fatalf("windows length = %d, want 2: %+v", len(got), got)
	}
	if got[0].Index != 0 || got[0].Activity != 123 || got[0].Name != "main" {
		t.Fatalf("first window = %+v, want index/activity/name", got[0])
	}
	if got[1].Index != 1 || got[1].Activity != 456 || got[1].Name != "logs pane" {
		t.Fatalf("second window = %+v, want index/activity/name", got[1])
	}
}

func TestLocalSessionSourcePublishesEmptyGroups(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("XDG_DATA_HOME", home+"/.local/share")
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	inst := &session.Instance{
		ID:             "s1",
		Title:          "worker",
		ProjectPath:    "/repo",
		GroupPath:      session.DefaultGroupPath,
		Tool:           "codex",
		Status:         session.StatusWaiting,
		CreatedAt:      time.Unix(789, 0).UTC().Add(-time.Hour),
		LastAccessedAt: time.Unix(789, 0).UTC(),
	}
	groupTree := session.NewGroupTree([]*session.Instance{inst})
	if groupTree.CreateGroup("empty") == nil {
		t.Fatal("CreateGroup empty returned nil")
	}

	storage, err := session.NewStorageWithProfile("hub-groups")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	if err := storage.SaveWithGroups([]*session.Instance{inst}, groupTree); err != nil {
		t.Fatalf("SaveWithGroups: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	snap, err := (LocalSessionSource{Profile: "hub-groups"}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snapshotHasGroup(snap, "empty") {
		t.Fatalf("snapshot groups = %+v, want empty group", snap.Groups)
	}
}

func TestLocalSessionSourceMarksForkableSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	storage, err := session.NewStorageWithProfile("hub-forkable")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	if err := storage.Save([]*session.Instance{{
		ID:               "forkable",
		Title:            "fork me",
		ProjectPath:      "/repo",
		GroupPath:        "default",
		Tool:             "claude",
		Status:           session.StatusWaiting,
		ClaudeSessionID:  "11111111-1111-1111-1111-111111111111",
		ClaudeDetectedAt: time.Now(),
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	snap, err := (LocalSessionSource{Profile: "hub-forkable"}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("Sessions length = %d, want 1", len(snap.Sessions))
	}
	if !snap.Sessions[0].CanFork {
		t.Fatalf("CanFork = false for forkable session: %+v", snap.Sessions[0])
	}
}

func TestLocalSessionSourceMissingProfileDoesNotCreateStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	profileDir, err := session.GetProfileDir("hub-missing")
	if err != nil {
		t.Fatalf("GetProfileDir: %v", err)
	}

	snap, err := (LocalSessionSource{Profile: "hub-missing"}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Sessions) != 0 {
		t.Fatalf("Sessions length = %d, want 0", len(snap.Sessions))
	}
	if _, err := os.Stat(profileDir); !os.IsNotExist(err) {
		t.Fatalf("profile dir stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(profileDir, "state.db")); !os.IsNotExist(err) {
		t.Fatalf("state.db stat = %v, want not exist", err)
	}
}

func TestLocalSessionSourcePublishesWebAvailability(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	snap, err := (LocalSessionSource{
		Profile: "hub-web-available",
		webAvailable: func(context.Context) bool {
			return true
		},
	}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.WebAvailable {
		t.Fatal("WebAvailable = false, want true")
	}
}

func TestLocalSessionSourceEmptyProfileUsesAgentDeckProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("AGENTDECK_PROFILE", "hub-env")
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	storage, err := session.NewStorageWithProfile("hub-env")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	now := time.Unix(987, 0).UTC()
	if err := storage.Save([]*session.Instance{{
		ID:             "env-session",
		Title:          "env worker",
		ProjectPath:    "/repo",
		GroupPath:      "default",
		Tool:           "codex",
		Status:         session.StatusRunning,
		CreatedAt:      now.Add(-time.Hour),
		LastAccessedAt: now,
		CodexSessionID: "codex-env-session",
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	defaultProfileDir, err := session.GetProfileDir(session.DefaultProfile)
	if err != nil {
		t.Fatalf("GetProfileDir(default): %v", err)
	}

	snap, err := (LocalSessionSource{}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if len(snap.Sessions) != 1 {
		t.Fatalf("Sessions length = %d, want 1", len(snap.Sessions))
	}
	got := snap.Sessions[0]
	if got.ID != "env-session" || got.Title != "env worker" || got.Status != "running" {
		t.Fatalf("session mapping = %+v", got)
	}
	if got.DisplaySessionID != "codex-env-session" {
		t.Fatalf("DisplaySessionID = %q, want codex-env-session", got.DisplaySessionID)
	}
	if _, err := os.Stat(filepath.Join(defaultProfileDir, "state.db")); !os.IsNotExist(err) {
		t.Fatalf("default state.db stat = %v, want not exist", err)
	}
}

type observedMessage struct {
	envelope Envelope
	payload  json.RawMessage
}

func assertHelloHasNoToken(t *testing.T, msg observedMessage, secret string) {
	t.Helper()
	var hello NodeHelloPayload
	if err := json.Unmarshal(msg.payload, &hello); err != nil {
		t.Fatalf("hello payload: %v", err)
	}
	if hello.Token != "" {
		t.Fatalf("hello token = %q, want empty", hello.Token)
	}
	if strings.Contains(string(msg.payload), secret) {
		t.Fatalf("hello payload leaked token: %s", string(msg.payload))
	}
}

func assertNextMessageType(t *testing.T, messages <-chan observedMessage, want MessageType) observedMessage {
	t.Helper()
	select {
	case msg := <-messages:
		if msg.envelope.Type != want {
			t.Fatalf("message type = %q, want %q", msg.envelope.Type, want)
		}
		return msg
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", want)
		return observedMessage{}
	}
}

func waitString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for string")
		return ""
	}
}

func waitNodeSessions(t *testing.T, ch <-chan NodeSessions) NodeSessions {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for node sessions")
		return NodeSessions{}
	}
}

func snapshotHasGroup(snapshot SnapshotPayload, path string) bool {
	for _, group := range snapshot.Groups {
		if group.Path == path {
			return true
		}
	}
	return false
}

func waitTrustRequest(t *testing.T, ch <-chan TrustRequestPayload) TrustRequestPayload {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for trust request")
		return TrustRequestPayload{}
	}
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for error")
		return nil
	}
}

type fakeAttachBackend struct {
	stream *fakeAttachStream
	opens  chan fakeAttachOpenCall
}

type fakeAttachOpenCall struct {
	sessionID   string
	windowIndex *int
	size        TerminalSize
}

func newFakeAttachBackend() *fakeAttachBackend {
	return &fakeAttachBackend{stream: newFakeAttachStream(), opens: make(chan fakeAttachOpenCall, 1)}
}

func (b *fakeAttachBackend) Open(ctx context.Context, sessionID string, size TerminalSize) (AttachStream, error) {
	b.opens <- fakeAttachOpenCall{sessionID: sessionID, size: size}
	return b.stream, nil
}

func (b *fakeAttachBackend) OpenWindow(ctx context.Context, sessionID string, windowIndex int, size TerminalSize) (AttachStream, error) {
	b.opens <- fakeAttachOpenCall{sessionID: sessionID, windowIndex: &windowIndex, size: size}
	return b.stream, nil
}

func (b *fakeAttachBackend) waitOpen(t *testing.T) fakeAttachOpenCall {
	t.Helper()
	select {
	case call := <-b.opens:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for backend Open")
		return fakeAttachOpenCall{}
	}
}

func (b *fakeAttachBackend) assertNoOpen(t *testing.T) {
	t.Helper()
	select {
	case call := <-b.opens:
		t.Fatalf("unexpected backend Open call: %+v", call)
	case <-time.After(50 * time.Millisecond):
	}
}

type fakeAttachStream struct {
	readCh   chan []byte
	writes   chan []byte
	resizes  chan TerminalSize
	closed   chan struct{}
	closeReq chan struct{}
}

func newFakeAttachStream() *fakeAttachStream {
	return &fakeAttachStream{
		readCh:   make(chan []byte, 4),
		writes:   make(chan []byte, 4),
		resizes:  make(chan TerminalSize, 4),
		closed:   make(chan struct{}),
		closeReq: make(chan struct{}),
	}
}

func (s *fakeAttachStream) Read(p []byte) (int, error) {
	select {
	case data := <-s.readCh:
		return copy(p, data), nil
	case <-s.closeReq:
		return 0, io.EOF
	}
}

func (s *fakeAttachStream) Write(p []byte) (int, error) {
	data := append([]byte(nil), p...)
	s.writes <- data
	return len(p), nil
}

func (s *fakeAttachStream) Resize(size TerminalSize) error {
	s.resizes <- size
	return nil
}

func (s *fakeAttachStream) Close() error {
	select {
	case <-s.closeReq:
	default:
		close(s.closeReq)
	}
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

func (s *fakeAttachStream) emit(data []byte) {
	s.readCh <- append([]byte(nil), data...)
}

func (s *fakeAttachStream) waitWrite(t *testing.T) []byte {
	t.Helper()
	select {
	case data := <-s.writes:
		return data
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream Write")
		return nil
	}
}

func (s *fakeAttachStream) waitResize(t *testing.T) TerminalSize {
	t.Helper()
	select {
	case size := <-s.resizes:
		return size
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream Resize")
		return TerminalSize{}
	}
}

func (s *fakeAttachStream) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream Close")
	}
}

func TestClientTLSConfigRejectsChangedPinnedCertificate(t *testing.T) {
	goodServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer goodServer.Close()

	goodDER := goodServer.Certificate().Raw
	badDER := append([]byte(nil), goodDER...)
	badDER[len(badDER)-1] ^= 0xff
	tlsConfig, err := clientTLSConfig(ClientConfig{PinnedCertSHA256: CertificateFingerprintSHA256(goodDER)})
	if err != nil {
		t.Fatalf("clientTLSConfig: %v", err)
	}
	if !tlsConfig.InsecureSkipVerify {
		t.Fatal("pinned certificate mode should use custom verification")
	}
	goodCert, err := x509.ParseCertificate(goodDER)
	if err != nil {
		t.Fatalf("ParseCertificate good cert: %v", err)
	}
	badCert, err := x509.ParseCertificate(badDER)
	if err != nil {
		t.Fatalf("ParseCertificate bad cert: %v", err)
	}
	if err := tlsConfig.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{goodCert}}); err != nil {
		t.Fatalf("VerifyConnection good cert: %v", err)
	}
	if err := tlsConfig.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{badCert}}); err == nil {
		t.Fatal("VerifyConnection accepted changed certificate")
	}
}

func waitWebSocketConn(t *testing.T, ch <-chan *websocket.Conn) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-ch:
		return conn
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket connection")
		return nil
	}
}

var _ AttachBackend = (*fakeAttachBackend)(nil)
var _ AttachWindowBackend = (*fakeAttachBackend)(nil)
var _ AttachStream = (*fakeAttachStream)(nil)
