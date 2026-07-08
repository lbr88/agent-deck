package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/gorilla/websocket"
)

func wsURL(baseURL, path string) string {
	if strings.HasPrefix(baseURL, "https://") {
		return "wss://" + strings.TrimPrefix(baseURL, "https://") + path
	}
	return "ws://" + strings.TrimPrefix(baseURL, "http://") + path
}

func TestWSEndpointUnauthorized(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
		Profile:    "work",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-1",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-1"), nil)
	if err == nil {
		t.Fatal("expected websocket dial error for unauthorized request")
	}
	if resp == nil {
		t.Fatal("expected HTTP response for unauthorized websocket upgrade")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
	}
}

func TestWSEndpointAuthorizedWithQueryToken(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
		Profile:    "work",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-1",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-1?token=secret-token"), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectWSStatusEvent(t, conn, "connected")
}

func TestWSEndpointAuthorizedWithBearerToken(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
		Profile:    "work",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-2",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret-token")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-2"), headers)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectWSStatusEvent(t, conn, "connected")
}

func TestWSEndpointRejectsCrossOrigin(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-origin",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	headers := http.Header{}
	headers.Set("Origin", "https://evil.example")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-origin"), headers)
	if err == nil {
		t.Fatal("expected websocket dial error for cross-origin request")
	}
	if resp == nil {
		t.Fatal("expected HTTP response for rejected cross-origin websocket upgrade")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, resp.StatusCode)
	}
}

func TestWSEndpointSessionNotFound(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-existing",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-missing"), nil)
	if err == nil {
		t.Fatal("expected websocket dial error for missing session")
	}
	if resp == nil {
		t.Fatal("expected HTTP response for missing session websocket upgrade")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, resp.StatusCode)
	}
}

func TestWSEndpointConnectAndPing(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
		ReadOnly:   true,
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-123",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-123"), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	var msg1 wsServerMessage
	if err := conn.ReadJSON(&msg1); err != nil {
		t.Fatalf("failed to read first ws message: %v", err)
	}
	if msg1.Type != "status" || msg1.Event != "connected" || msg1.SessionID != "sess-123" {
		t.Fatalf("unexpected first ws message: %+v", msg1)
	}
	if !msg1.ReadOnly {
		t.Fatalf("expected readOnly=true in connected event, got: %+v", msg1)
	}

	var msg2 wsServerMessage
	if err := conn.ReadJSON(&msg2); err != nil {
		t.Fatalf("failed to read second ws message: %v", err)
	}
	if msg2.Type != "status" || msg2.Event != "ready" {
		t.Fatalf("unexpected second ws message: %+v", msg2)
	}

	if err := conn.WriteJSON(wsClientMessage{Type: "ping"}); err != nil {
		t.Fatalf("failed to write ping message: %v", err)
	}

	var msg3 wsServerMessage
	if err := conn.ReadJSON(&msg3); err != nil {
		t.Fatalf("failed to read pong ws message: %v", err)
	}
	if msg3.Type != "status" || msg3.Event != "pong" || msg3.SessionID != "sess-123" {
		t.Fatalf("unexpected pong message: %+v", msg3)
	}
}

func TestWSEndpointHubSessionAttachesThroughHubTerminal(t *testing.T) {
	stream := newFakeHubAttachStream()
	mutator := &fakeHubTerminalMutator{stream: stream}
	sessionID := HubSessionWebID("node_server", "remote_sess")
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
	})
	srv.mutator = mutator
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID:           sessionID,
						Source:       "hub",
						HubNodeID:    "node_server",
						HubNodeName:  "server",
						HubSessionID: "remote_sess",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/"+sessionID), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectWSStatusEvent(t, conn, "connected")
	expectWSStatusEvent(t, conn, "ready")
	expectWSStatusEvent(t, conn, "terminal_attached")
	if mutator.nodeID != "node_server" || mutator.sessionID != "remote_sess" {
		t.Fatalf("hub attach target = %q/%q, want node_server/remote_sess", mutator.nodeID, mutator.sessionID)
	}

	stream.emit([]byte("remote-output"))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hub terminal output: %v", err)
	}
	if msgType != websocket.BinaryMessage || string(data) != "remote-output" {
		t.Fatalf("hub terminal output message type=%d data=%q", msgType, string(data))
	}

	if err := conn.WriteJSON(wsClientMessage{Type: "input", Data: "echo hi\r"}); err != nil {
		t.Fatalf("write hub input: %v", err)
	}
	if got := string(stream.waitWrite(t)); got != "echo hi\r" {
		t.Fatalf("hub stream input = %q, want echo hi", got)
	}

	if err := conn.WriteJSON(wsClientMessage{Type: "resize", Cols: 120, Rows: 40}); err != nil {
		t.Fatalf("write hub resize: %v", err)
	}
	if got := stream.waitResize(t); got.Cols != 120 || got.Rows != 40 {
		t.Fatalf("hub stream resize = %+v, want 120x40", got)
	}
}

func TestHubDashboardWSSessionAttachesThroughHubTerminal(t *testing.T) {
	stream := newFakeHubAttachStream()
	mutator := &fakeHubTerminalMutator{stream: stream}
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
	})
	srv.mutator = mutator

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/hub/dashboard/node_server/ws/session/remote_sess"), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectWSStatusEvent(t, conn, "connected")
	expectWSStatusEvent(t, conn, "ready")
	expectWSStatusEvent(t, conn, "terminal_attached")
	if mutator.nodeID != "node_server" || mutator.sessionID != "remote_sess" {
		t.Fatalf("hub dashboard attach target = %q/%q, want node_server/remote_sess", mutator.nodeID, mutator.sessionID)
	}

	stream.emit([]byte("dashboard-output"))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hub dashboard terminal output: %v", err)
	}
	if msgType != websocket.BinaryMessage || string(data) != "dashboard-output" {
		t.Fatalf("hub dashboard terminal output type=%d data=%q", msgType, string(data))
	}

	if err := conn.WriteJSON(wsClientMessage{Type: "input", Data: "pwd\r"}); err != nil {
		t.Fatalf("write hub dashboard input: %v", err)
	}
	if got := string(stream.waitWrite(t)); got != "pwd\r" {
		t.Fatalf("hub dashboard stream input = %q, want pwd", got)
	}

	if err := conn.WriteJSON(wsClientMessage{Type: "resize", Cols: 132, Rows: 43}); err != nil {
		t.Fatalf("write hub dashboard resize: %v", err)
	}
	if got := stream.waitResize(t); got.Cols != 132 || got.Rows != 43 {
		t.Fatalf("hub dashboard stream resize = %+v, want 132x43", got)
	}
}

func TestWSEndpointInputWithoutTerminalBridge(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-bridge-missing",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-bridge-missing"), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectWSStatusEvent(t, conn, "connected")
	expectWSStatusEvent(t, conn, "ready")

	if err := conn.WriteJSON(wsClientMessage{Type: "input", Data: "echo hi\r"}); err != nil {
		t.Fatalf("failed to write input message: %v", err)
	}

	var msg wsServerMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("failed to read ws response: %v", err)
	}
	if msg.Type != "error" || msg.Code != "NO_TERMINAL_BRIDGE" {
		t.Fatalf("unexpected ws response: %+v", msg)
	}
}

func TestWSEndpointReadOnlyBlocksInput(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
		ReadOnly:   true,
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-read-only",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-read-only"), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectWSStatusEvent(t, conn, "connected")
	expectWSStatusEvent(t, conn, "ready")

	if err := conn.WriteJSON(wsClientMessage{Type: "input", Data: "echo blocked\r"}); err != nil {
		t.Fatalf("failed to write input message: %v", err)
	}

	var msg wsServerMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("failed to read ws response: %v", err)
	}
	if msg.Type != "error" || msg.Code != "READ_ONLY" {
		t.Fatalf("unexpected ws response: %+v", msg)
	}
}

func TestWSEndpointResizeWithoutTerminalBridge(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "work",
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-resize-missing",
					},
				},
			},
		},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(testServer.URL, "/ws/session/sess-resize-missing"), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectWSStatusEvent(t, conn, "connected")
	expectWSStatusEvent(t, conn, "ready")

	if err := conn.WriteJSON(wsClientMessage{Type: "resize", Cols: 120, Rows: 36}); err != nil {
		t.Fatalf("failed to write resize message: %v", err)
	}

	var msg wsServerMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("failed to read ws response: %v", err)
	}
	if msg.Type != "error" || msg.Code != "NO_TERMINAL_BRIDGE" {
		t.Fatalf("unexpected ws response: %+v", msg)
	}
}

func expectWSStatusEvent(t *testing.T, conn *websocket.Conn, event string) {
	t.Helper()

	var msg wsServerMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("failed to read ws status message: %v", err)
	}
	if msg.Type != "status" || msg.Event != event {
		t.Fatalf("expected status=%q message, got: %+v", event, msg)
	}
}

type fakeHubTerminalMutator struct {
	fakeMutator
	stream    *fakeHubAttachStream
	nodeID    string
	sessionID string
	size      hub.TerminalSize
}

func (m *fakeHubTerminalMutator) OpenHubTerminal(_ context.Context, nodeID, sessionID string, size hub.TerminalSize) (hub.AttachStream, error) {
	m.nodeID = nodeID
	m.sessionID = sessionID
	m.size = size
	return m.stream, nil
}

type fakeHubAttachStream struct {
	readCh   chan []byte
	writes   chan []byte
	resizes  chan hub.TerminalSize
	closeReq chan struct{}
}

func newFakeHubAttachStream() *fakeHubAttachStream {
	return &fakeHubAttachStream{
		readCh:   make(chan []byte, 4),
		writes:   make(chan []byte, 4),
		resizes:  make(chan hub.TerminalSize, 4),
		closeReq: make(chan struct{}),
	}
}

func (s *fakeHubAttachStream) Read(p []byte) (int, error) {
	select {
	case data := <-s.readCh:
		return copy(p, data), nil
	case <-s.closeReq:
		return 0, io.EOF
	}
}

func (s *fakeHubAttachStream) Write(p []byte) (int, error) {
	s.writes <- append([]byte(nil), p...)
	return len(p), nil
}

func (s *fakeHubAttachStream) Resize(size hub.TerminalSize) error {
	s.resizes <- size
	return nil
}

func (s *fakeHubAttachStream) Close() error {
	select {
	case <-s.closeReq:
	default:
		close(s.closeReq)
	}
	return nil
}

func (s *fakeHubAttachStream) emit(data []byte) {
	s.readCh <- append([]byte(nil), data...)
}

func (s *fakeHubAttachStream) waitWrite(t *testing.T) []byte {
	t.Helper()
	select {
	case data := <-s.writes:
		return data
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hub stream write")
		return nil
	}
}

func (s *fakeHubAttachStream) waitResize(t *testing.T) hub.TerminalSize {
	t.Helper()
	select {
	case size := <-s.resizes:
		return size
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hub stream resize")
		return hub.TerminalSize{}
	}
}

var _ hub.AttachStream = (*fakeHubAttachStream)(nil)
