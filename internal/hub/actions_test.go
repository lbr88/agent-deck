package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestCommandDispatcherRejectsUnknownAction(t *testing.T) {
	dispatcher := CommandDispatcher{}
	_, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "unknown",
	})
	if err == nil {
		t.Fatal("unknown action succeeded")
	}
}

func TestCommandDispatcherSendUsesSessionIDAndMessage(t *testing.T) {
	fake := &fakeActionBackend{}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(map[string]string{"session_id": "s1", "message": "run tests"})
	_, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "send",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch send: %v", err)
	}
	if fake.sentSessionID != "s1" || fake.sentMessage != "run tests" {
		t.Fatalf("fake = %+v", fake)
	}
}

func TestCommandDispatcherCreateUsesCreateRequest(t *testing.T) {
	fake := &fakeActionBackend{createSessionID: "created_1"}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(CreateSessionRequest{
		Title:       "deploy",
		Tool:        "codex",
		ProjectPath: "/srv/app",
		GroupPath:   "ops",
		ModelID:     "gpt-5",
	})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "create",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch create: %v", err)
	}
	if fake.createReq.Title != "deploy" || fake.createReq.Tool != "codex" || fake.createReq.ProjectPath != "/srv/app" || fake.createReq.GroupPath != "ops" || fake.createReq.ModelID != "gpt-5" {
		t.Fatalf("create request = %+v", fake.createReq)
	}
	var result actionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode create result: %v", err)
	}
	if result.SessionID != "created_1" {
		t.Fatalf("create result session_id = %q, want created_1", result.SessionID)
	}
}

func TestCommandDispatcherDeleteUsesSessionID(t *testing.T) {
	fake := &fakeActionBackend{}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(map[string]string{"session_id": "s1"})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "delete",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch delete: %v", err)
	}
	if fake.deletedSessionID != "s1" {
		t.Fatalf("deleted session id = %q, want s1", fake.deletedSessionID)
	}
	var result actionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode delete result: %v", err)
	}
	if result.SessionID != "s1" {
		t.Fatalf("delete result session_id = %q, want s1", result.SessionID)
	}
}

func TestCommandDispatcherParityActionsUseBackend(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{"restart_fresh", "restart_fresh:s1"},
		{"archive", "archive:s1"},
		{"unarchive", "unarchive:s1"},
		{"remove", "remove:s1"},
		{"toggle_yolo", "toggle_yolo:s1"},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			fake := &fakeActionBackend{}
			dispatcher := CommandDispatcher{Backend: fake}
			payload, _ := json.Marshal(map[string]string{"session_id": "s1"})
			_, err := dispatcher.Dispatch(context.Background(), CommandPayload{
				CommandID: "cmd_1",
				Action:    tc.action,
				Payload:   payload,
			})
			if err != nil {
				t.Fatalf("Dispatch %s: %v", tc.action, err)
			}
			if fake.lastAction != tc.want {
				t.Fatalf("lastAction = %q, want %q", fake.lastAction, tc.want)
			}
		})
	}
}

func TestCommandDispatcherMoveAndUpdateUseBackend(t *testing.T) {
	fake := &fakeActionBackend{}
	dispatcher := CommandDispatcher{Backend: fake}
	movePayload, _ := json.Marshal(map[string]string{"session_id": "s1", "group_path": "ops"})
	if _, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "move", Payload: movePayload}); err != nil {
		t.Fatalf("Dispatch move: %v", err)
	}
	if fake.movedSessionID != "s1" || fake.movedGroupPath != "ops" {
		t.Fatalf("move = session %q group %q", fake.movedSessionID, fake.movedGroupPath)
	}

	updatePayload, _ := json.Marshal(UpdateSessionRequest{
		SessionID: "s1",
		Changes:   []SessionFieldChange{{Field: "title", Value: "renamed"}},
	})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_2", Action: "update", Payload: updatePayload})
	if err != nil {
		t.Fatalf("Dispatch update: %v", err)
	}
	if fake.updateReq.SessionID != "s1" || len(fake.updateReq.Changes) != 1 || fake.updateReq.Changes[0].Value != "renamed" {
		t.Fatalf("update req = %+v", fake.updateReq)
	}
	var result UpdateSessionResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode update result: %v", err)
	}
	if !result.Restarted {
		t.Fatal("update result Restarted = false, want true")
	}
}

func TestCommandDispatcherPreviewReturnsContent(t *testing.T) {
	fake := &fakeActionBackend{previewContent: "remote pane content"}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(map[string]string{"session_id": "s1"})

	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "preview",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch preview: %v", err)
	}
	if fake.previewSessionID != "s1" {
		t.Fatalf("preview session id = %q, want s1", fake.previewSessionID)
	}
	var result PreviewSessionResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode preview result: %v", err)
	}
	if result.Content != "remote pane content" {
		t.Fatalf("preview content = %q, want remote pane content", result.Content)
	}
}

func TestCommandDispatcherImportTmuxUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{importTmuxCount: 2}
	dispatcher := CommandDispatcher{Backend: fake}

	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "import_tmux",
	})
	if err != nil {
		t.Fatalf("Dispatch import_tmux: %v", err)
	}
	if !fake.importTmuxCalled {
		t.Fatal("ImportTmux was not called")
	}
	var result ImportTmuxSessionsResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode import_tmux result: %v", err)
	}
	if result.Imported != 2 {
		t.Fatalf("imported count = %d, want 2", result.Imported)
	}
}

func TestCommandDispatcherWebProxyUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{
		webProxyResp: WebProxyResponse{
			StatusCode: 200,
			Header:     map[string][]string{"Content-Type": {"text/html"}},
			BodyB64:    base64.StdEncoding.EncodeToString([]byte("<html>remote</html>")),
		},
	}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(WebProxyRequest{Method: "GET", Path: "/api/menu"})

	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "web_proxy",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch web_proxy: %v", err)
	}
	if fake.webProxyReq.Path != "/api/menu" || fake.webProxyReq.Method != "GET" {
		t.Fatalf("web proxy req = %+v", fake.webProxyReq)
	}
	var result WebProxyResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode web proxy result: %v", err)
	}
	body, err := base64.StdEncoding.DecodeString(result.BodyB64)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(body) != "<html>remote</html>" {
		t.Fatalf("body = %q", body)
	}
}

func TestSanitizeWebProxyPathRejectsAbsoluteURL(t *testing.T) {
	if _, err := sanitizeWebProxyPath("http://example.com/api/menu"); err == nil {
		t.Fatal("absolute URL accepted")
	}
	if _, err := sanitizeWebProxyPath("//example.com/api/menu"); err == nil {
		t.Fatal("// URL accepted")
	}
}

type fakeActionBackend struct {
	sentSessionID    string
	sentMessage      string
	createReq        CreateSessionRequest
	createSessionID  string
	deletedSessionID string
	previewSessionID string
	previewContent   string
	importTmuxCalled bool
	importTmuxCount  int
	lastAction       string
	movedSessionID   string
	movedGroupPath   string
	updateReq        UpdateSessionRequest
	webProxyReq      WebProxyRequest
	webProxyResp     WebProxyResponse
}

func (b *fakeActionBackend) Send(_ context.Context, sessionID, message string) error {
	b.sentSessionID = sessionID
	b.sentMessage = message
	return nil
}

func (b *fakeActionBackend) Start(context.Context, string) error {
	return nil
}

func (b *fakeActionBackend) Stop(context.Context, string) error {
	return nil
}

func (b *fakeActionBackend) Restart(context.Context, string) error {
	return nil
}

func (b *fakeActionBackend) RestartFresh(_ context.Context, sessionID string) error {
	b.lastAction = "restart_fresh:" + sessionID
	return nil
}

func (b *fakeActionBackend) Rename(context.Context, string, string) error {
	return nil
}

func (b *fakeActionBackend) Create(_ context.Context, req CreateSessionRequest) (string, error) {
	b.createReq = req
	return b.createSessionID, nil
}

func (b *fakeActionBackend) Delete(_ context.Context, sessionID string) error {
	b.deletedSessionID = sessionID
	return nil
}

func (b *fakeActionBackend) Archive(_ context.Context, sessionID string) error {
	b.lastAction = "archive:" + sessionID
	return nil
}

func (b *fakeActionBackend) Unarchive(_ context.Context, sessionID string) error {
	b.lastAction = "unarchive:" + sessionID
	return nil
}

func (b *fakeActionBackend) Remove(_ context.Context, sessionID string) error {
	b.lastAction = "remove:" + sessionID
	return nil
}

func (b *fakeActionBackend) Move(_ context.Context, sessionID, groupPath string) error {
	b.movedSessionID = sessionID
	b.movedGroupPath = groupPath
	return nil
}

func (b *fakeActionBackend) Update(_ context.Context, req UpdateSessionRequest) (UpdateSessionResponse, error) {
	b.updateReq = req
	return UpdateSessionResponse{Restarted: true}, nil
}

func (b *fakeActionBackend) ToggleYolo(_ context.Context, sessionID string) error {
	b.lastAction = "toggle_yolo:" + sessionID
	return nil
}

func (b *fakeActionBackend) Preview(_ context.Context, sessionID string) (string, error) {
	b.previewSessionID = sessionID
	return b.previewContent, nil
}

func (b *fakeActionBackend) ImportTmux(context.Context) (int, error) {
	b.importTmuxCalled = true
	return b.importTmuxCount, nil
}

func (b *fakeActionBackend) ProxyWeb(_ context.Context, req WebProxyRequest) (WebProxyResponse, error) {
	b.webProxyReq = req
	return b.webProxyResp, nil
}
