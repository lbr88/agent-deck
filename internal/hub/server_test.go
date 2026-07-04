package hub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHubHealthz(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("GET /healthz body = %q, want ok", rec.Body.String())
	}
}

func TestServerJoinConsumesInviteAndReturnsNodeCredentials(t *testing.T) {
	server := newTestServer(t)
	inviteToken, err := server.store.CreateInvite("laptop", time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	body := bytes.NewBufferString(`{"invite_token":` + strconvQuote(inviteToken) + `,"node_name":"laptop","version":"1.0.0","os":"linux","arch":"amd64"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/join", body)
	req.Host = "hub.local:8421"
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/join status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		URL       string `json:"url"`
		NodeID    string `json:"node_id"`
		NodeName  string `json:"node_name"`
		NodeToken string `json:"node_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode join response: %v", err)
	}
	if got.URL != "wss://hub.local:8421" || got.NodeName != "laptop" || got.NodeID == "" || got.NodeToken == "" {
		t.Fatalf("join response = %+v", got)
	}
	if _, err := server.store.AuthenticateNode(got.NodeID, got.NodeToken); err != nil {
		t.Fatalf("AuthenticateNode returned credentials: %v", err)
	}

	reuseReq := httptest.NewRequest(http.MethodPost, "/api/join", bytes.NewBufferString(`{"invite_token":`+strconvQuote(inviteToken)+`}`))
	reuseRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(reuseRec, reuseReq)
	if reuseRec.Code == http.StatusOK {
		t.Fatal("reused invite succeeded, want failure")
	}
}

func TestServerJoinRequiresInviteToken(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/join", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/join missing token status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHubNodeWebSocketRequiresAuthentication(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	unauthReq := httptest.NewRequest(http.MethodGet, "/ws/node?node_id=node_1", nil)
	unauthRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /ws/node status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}

	badReq := httptest.NewRequest(http.MethodGet, "/ws/node?node_id=node_1", nil)
	badReq.Header.Set("Authorization", "Bearer wrong")
	badRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token /ws/node status = %d, want %d", badRec.Code, http.StatusUnauthorized)
	}

	goodReq := httptest.NewRequest(http.MethodGet, "/ws/node?node_id=node_1", nil)
	goodReq.Header.Set("Authorization", "Bearer node_secret")
	goodRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(goodRec, goodReq)
	if goodRec.Code == http.StatusUnauthorized {
		t.Fatal("authenticated /ws/node was rejected before websocket upgrade")
	}
}

func TestHubNodeWebSocketPersistsSnapshot(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	conn := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret")
	defer conn.Close()
	readTestWelcome(t, conn)

	snapshot, err := MarshalEnvelope(MsgSnapshot, "node_1", SnapshotPayload{
		SentAt: time.Unix(123, 0).UTC(),
		Sessions: []SessionInfo{{
			ID:        "s1",
			Title:     "worker",
			GroupPath: "default",
			Status:    "waiting",
		}},
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	if err := conn.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON snapshot: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("snapshot was not persisted")
		default:
		}
		got, err := server.store.LatestSessions()
		if err != nil {
			t.Fatalf("LatestSessions: %v", err)
		}
		if len(got) == 1 && len(got[0].Sessions) == 1 && got[0].Sessions[0].ID == "s1" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHubNodeWebSocketFansOutSnapshots(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret_1"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode node_1: %v", err)
	}
	if _, err := server.store.UpsertNode("node_2", "server1", hashSecret("node_secret_2"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode node_2: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	first := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret_1")
	defer first.Close()
	readTestWelcome(t, first)
	second := dialTestNodeWebSocket(t, httpServer.URL, "node_2", "node_secret_2")
	defer second.Close()
	readTestWelcome(t, second)

	snapshot, err := MarshalEnvelope(MsgSnapshot, "node_1", SnapshotPayload{
		SentAt: time.Unix(125, 0).UTC(),
		Sessions: []SessionInfo{{
			ID:        "s1",
			Title:     "worker",
			GroupPath: "ops",
			Status:    "waiting",
		}},
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope snapshot: %v", err)
	}
	if err := first.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON snapshot: %v", err)
	}

	var got Envelope
	if err := second.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := second.ReadJSON(&got); err != nil {
		t.Fatalf("second node did not receive snapshot fanout: %v", err)
	}
	if got.Type != MsgSnapshot || got.NodeID != "node_1" {
		t.Fatalf("fanout envelope = %+v", got)
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("fanout payload: %v", err)
	}
	if payload.NodeName != "laptop" || len(payload.Sessions) != 1 || payload.Sessions[0].Title != "worker" {
		t.Fatalf("fanout payload = %+v", payload)
	}
}

func TestHubNodeWebSocketAcceptsHeartbeat(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	conn := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret")
	defer conn.Close()
	readTestWelcome(t, conn)

	heartbeat, err := MarshalEnvelope(MsgHeartbeat, "node_1", nil)
	if err != nil {
		t.Fatalf("MarshalEnvelope heartbeat: %v", err)
	}
	if err := conn.WriteJSON(heartbeat); err != nil {
		t.Fatalf("WriteJSON heartbeat: %v", err)
	}
	snapshot, err := MarshalEnvelope(MsgSnapshot, "node_1", SnapshotPayload{
		SentAt:   time.Unix(124, 0).UTC(),
		Sessions: []SessionInfo{{ID: "after-heartbeat", Title: "worker"}},
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope snapshot: %v", err)
	}
	if err := conn.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON after heartbeat: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("connection did not accept snapshot after heartbeat")
		default:
		}
		got, err := server.store.LatestSessions()
		if err != nil {
			t.Fatalf("LatestSessions: %v", err)
		}
		if len(got) == 1 && len(got[0].Sessions) == 1 && got[0].Sessions[0].ID == "after-heartbeat" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHubNodeWebSocketOverlappingConnectionsKeepNodeOnline(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	first := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret")
	readTestWelcome(t, first)
	second := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret")
	readTestWelcome(t, second)
	waitNodeStatus(t, server, "node_1", "online")

	if err := first.Close(); err != nil {
		t.Fatalf("close first websocket: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	assertNodeStatus(t, server, "node_1", "online")

	if err := second.Close(); err != nil {
		t.Fatalf("close second websocket: %v", err)
	}
	waitNodeStatus(t, server, "node_1", "offline")
}

func TestServerServeRequiresTLSCertAndKey(t *testing.T) {
	server := newTestServer(t)
	err := server.Serve()
	if err == nil {
		t.Fatal("Serve without TLS cert/key succeeded, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--tls-cert") || !strings.Contains(msg, "--tls-key") {
		t.Fatalf("Serve error = %q, want --tls-cert and --tls-key guidance", msg)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func strconvQuote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func dialTestNodeWebSocket(t *testing.T, serverURL, nodeID, token string) *websocket.Conn {
	t.Helper()
	u := "ws://" + strings.TrimPrefix(serverURL, "http://") + "/ws/node?node_id=" + nodeID
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		t.Fatalf("Dial node websocket: %v", err)
	}
	return conn
}

func readTestWelcome(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	var env Envelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("ReadJSON welcome: %v", err)
	}
	if env.Type != MsgWelcome {
		t.Fatalf("welcome type = %q, want %q", env.Type, MsgWelcome)
	}
}

func waitNodeStatus(t *testing.T, server *Server, nodeID, want string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			assertNodeStatus(t, server, nodeID, want)
			return
		default:
		}
		if nodeStatus(t, server, nodeID) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertNodeStatus(t *testing.T, server *Server, nodeID, want string) {
	t.Helper()
	if got := nodeStatus(t, server, nodeID); got != want {
		t.Fatalf("node %s status = %q, want %q", nodeID, got, want)
	}
}

func nodeStatus(t *testing.T, server *Server, nodeID string) string {
	t.Helper()
	nodes, err := server.store.Nodes()
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	for _, node := range nodes {
		if node.ID == nodeID {
			return node.Status
		}
	}
	t.Fatalf("node %s not found", nodeID)
	return ""
}
