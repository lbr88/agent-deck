package hub

import (
	"context"
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

type fakeActionBackend struct {
	sentSessionID    string
	sentMessage      string
	createReq        CreateSessionRequest
	createSessionID  string
	previewSessionID string
	previewContent   string
	importTmuxCalled bool
	importTmuxCount  int
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

func (b *fakeActionBackend) Rename(context.Context, string, string) error {
	return nil
}

func (b *fakeActionBackend) Create(_ context.Context, req CreateSessionRequest) (string, error) {
	b.createReq = req
	return b.createSessionID, nil
}

func (b *fakeActionBackend) Preview(_ context.Context, sessionID string) (string, error) {
	b.previewSessionID = sessionID
	return b.previewContent, nil
}

func (b *fakeActionBackend) ImportTmux(context.Context) (int, error) {
	b.importTmuxCalled = true
	return b.importTmuxCount, nil
}
