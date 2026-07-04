package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
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
