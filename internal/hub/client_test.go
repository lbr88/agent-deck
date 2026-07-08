package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
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
		SentAt:   time.Unix(123, 0).UTC(),
		Sessions: []SessionInfo{{
			ID:        "s1",
			Title:     "worker",
			Status:    "waiting",
			GroupPath: "ops",
		}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal snapshot payload: %v", err)
	}

	client.dispatch(Envelope{Version: ProtocolVersion, Type: MsgSnapshot, NodeID: "node_remote", Payload: raw})

	got := waitNodeSessions(t, snapshots)
	if got.Node.ID != "node_remote" || got.Node.Name != "server1" {
		t.Fatalf("snapshot node = %+v", got.Node)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].Title != "worker" {
		t.Fatalf("snapshot sessions = %+v", got.Sessions)
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

	open, err := MarshalEnvelope(MsgAttachOpen, "node_requester", AttachOpenPayload{
		StreamID:  "stream_1",
		SessionID: "sess_1",
		Cols:      100,
		Rows:      30,
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope open: %v", err)
	}
	if err := conn.WriteJSON(open); err != nil {
		t.Fatalf("server WriteJSON open: %v", err)
	}
	call := backend.waitOpen(t)
	if call.sessionID != "sess_1" || call.size.Cols != 100 || call.size.Rows != 30 {
		t.Fatalf("backend open call = %+v", call)
	}
	assertNextMessageType(t, messages, MsgAttachReady)

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
		ID:             "s1",
		Title:          "worker",
		ProjectPath:    "/repo",
		GroupPath:      "default",
		Tool:           "codex",
		Status:         session.StatusWaiting,
		CreatedAt:      now.Add(-time.Hour),
		LastAccessedAt: now,
		CodexSessionID: "codex-session-1",
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	snap, err := (LocalSessionSource{Profile: "hub-local"}).Snapshot(context.Background())
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
	if got.DisplaySessionID != "codex-session-1" {
		t.Fatalf("DisplaySessionID = %q, want codex-session-1", got.DisplaySessionID)
	}
	if got.UpdatedAt == nil || !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, now)
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
	sessionID string
	size      TerminalSize
}

func newFakeAttachBackend() *fakeAttachBackend {
	return &fakeAttachBackend{stream: newFakeAttachStream(), opens: make(chan fakeAttachOpenCall, 1)}
}

func (b *fakeAttachBackend) Open(ctx context.Context, sessionID string, size TerminalSize) (AttachStream, error) {
	b.opens <- fakeAttachOpenCall{sessionID: sessionID, size: size}
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
	if err := tlsConfig.VerifyPeerCertificate([][]byte{goodDER}, nil); err != nil {
		t.Fatalf("VerifyPeerCertificate good cert: %v", err)
	}
	if err := tlsConfig.VerifyPeerCertificate([][]byte{badDER}, nil); err == nil {
		t.Fatal("VerifyPeerCertificate accepted changed certificate")
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
var _ AttachStream = (*fakeAttachStream)(nil)
