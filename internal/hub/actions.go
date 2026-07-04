package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

type ActionBackend interface {
	Send(ctx context.Context, sessionID, message string) error
	Start(ctx context.Context, sessionID string) error
	Stop(ctx context.Context, sessionID string) error
	Restart(ctx context.Context, sessionID string) error
	Rename(ctx context.Context, sessionID, title string) error
	Create(ctx context.Context, req CreateSessionRequest) (string, error)
	Preview(ctx context.Context, sessionID string) (string, error)
}

type CreateSessionRequest struct {
	Title       string `json:"title"`
	Tool        string `json:"tool,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	GroupPath   string `json:"group_path,omitempty"`
	ModelID     string `json:"model_id,omitempty"`
}

type PreviewSessionResponse struct {
	Content string `json:"content"`
}

type CommandDispatcher struct {
	Backend ActionBackend
}

type actionResult struct {
	SessionID string `json:"session_id,omitempty"`
}

type sessionActionPayload struct {
	SessionID string `json:"session_id"`
}

type sendActionPayload struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

type renameActionPayload struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
}

func (d CommandDispatcher) Dispatch(ctx context.Context, cmd CommandPayload) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d.Backend == nil {
		return nil, fmt.Errorf("hub action backend is not configured")
	}
	action := strings.TrimSpace(cmd.Action)
	switch action {
	case "send":
		var payload sendActionPayload
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("send action session_id is required")
		}
		if strings.TrimSpace(payload.Message) == "" {
			return nil, fmt.Errorf("send action message is required")
		}
		if err := d.Backend.Send(ctx, payload.SessionID, payload.Message); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "start":
		payload, err := decodeSessionAction(cmd.Payload, "start")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.Start(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "stop":
		payload, err := decodeSessionAction(cmd.Payload, "stop")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.Stop(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "restart":
		payload, err := decodeSessionAction(cmd.Payload, "restart")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.Restart(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "rename":
		var payload renameActionPayload
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("rename action session_id is required")
		}
		if strings.TrimSpace(payload.Title) == "" {
			return nil, fmt.Errorf("rename action title is required")
		}
		if err := d.Backend.Rename(ctx, payload.SessionID, payload.Title); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "create":
		var payload CreateSessionRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.Title) == "" {
			return nil, fmt.Errorf("create action title is required")
		}
		sessionID, err := d.Backend.Create(ctx, payload)
		if err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: sessionID})
	case "preview":
		payload, err := decodeSessionAction(cmd.Payload, "preview")
		if err != nil {
			return nil, err
		}
		content, err := d.Backend.Preview(ctx, payload.SessionID)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(PreviewSessionResponse{Content: content})
		if err != nil {
			return nil, err
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("unknown hub action %q", action)
	}
}

func decodeSessionAction(raw json.RawMessage, action string) (sessionActionPayload, error) {
	var payload sessionActionPayload
	if err := decodeCommandPayload(raw, &payload); err != nil {
		return sessionActionPayload{}, err
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		return sessionActionPayload{}, fmt.Errorf("%s action session_id is required", action)
	}
	return payload, nil
}

func decodeCommandPayload(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return fmt.Errorf("command payload is required")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode command payload: %w", err)
	}
	return nil
}

func marshalActionResult(result actionResult) (json.RawMessage, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

type LocalActionBackend struct {
	Profile string
}

func (b LocalActionBackend) Send(ctx context.Context, sessionID, message string) error {
	inst, closeStorage, err := b.loadInstance(sessionID)
	if err != nil {
		return err
	}
	defer closeStorage()
	if !inst.Exists() {
		return fmt.Errorf("session %q is not running", inst.Title)
	}
	tmuxSess := inst.GetTmuxSession()
	if tmuxSess == nil {
		return fmt.Errorf("session %q has no tmux session", inst.Title)
	}
	if err := waitForLocalActionReady(ctx, tmuxSess, inst.Tool); err != nil {
		return err
	}
	if err := tmuxSess.SendKeysAndEnter(message); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}

func (b LocalActionBackend) Start(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := inst.Start(); err != nil {
		return err
	}
	inst.PostStartSync(3 * time.Second)
	return storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups))
}

func (b LocalActionBackend) Stop(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := inst.Kill(); err != nil {
		return err
	}
	return storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups))
}

func (b LocalActionBackend) Restart(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := inst.Restart(); err != nil {
		return err
	}
	inst.LastStartedAt = time.Now()
	inst.PostStartSync(3 * time.Second)
	return storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups))
}

func (b LocalActionBackend) Rename(ctx context.Context, sessionID, title string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	_, postCommit, err := session.SetField(inst, session.FieldTitle, title, nil)
	if err != nil {
		return err
	}
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return err
	}
	if postCommit != nil {
		return postCommit()
	}
	return nil
}

func (b LocalActionBackend) Create(ctx context.Context, req CreateSessionRequest) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return "", err
	}
	defer storage.Close()
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		return "", err
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = session.GenerateUniqueSessionName(instances, req.GroupPath)
	}
	projectPath := strings.TrimSpace(req.ProjectPath)
	if projectPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("determine project path: %w", err)
		}
		projectPath = cwd
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = session.GetDefaultTool()
	}
	if tool == "" {
		tool = "claude"
	}
	command := ""
	if tool != "shell" {
		command = session.GetToolCommand(tool)
		if command == "" {
			command = tool
		}
		if tool == "cursor" && command == "cursor" {
			command = "cursor agent"
		}
	}

	inst := session.NewInstanceWithGroupAndTool(title, projectPath, strings.TrimSpace(req.GroupPath), tool)
	inst.Command = command
	if strings.TrimSpace(req.ModelID) != "" {
		if err := inst.ApplyLaunchModel(req.ModelID); err != nil {
			return "", err
		}
	}
	if err := inst.Start(); err != nil {
		return "", err
	}
	inst.PostStartSync(3 * time.Second)
	instances = append(instances, inst)
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return "", err
	}
	return inst.ID, nil
}

func (b LocalActionBackend) Preview(ctx context.Context, sessionID string) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	inst, closeStorage, err := b.loadInstance(sessionID)
	if err != nil {
		return "", err
	}
	defer closeStorage()
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	content, err := inst.PreviewFull()
	if err != nil {
		return "", fmt.Errorf("preview session: %w", err)
	}
	return content, nil
}

func (b LocalActionBackend) loadInstance(sessionID string) (*session.Instance, func(), error) {
	storage, _, _, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return nil, func() {}, err
	}
	return inst, func() { _ = storage.Close() }, nil
}

func (b LocalActionBackend) loadSessionData(sessionID string) (*session.Storage, []*session.Instance, []*session.GroupData, *session.Instance, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil, nil, nil, fmt.Errorf("session_id is required")
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		_ = storage.Close()
		return nil, nil, nil, nil, err
	}
	for _, inst := range instances {
		if inst != nil && inst.ID == sessionID {
			return storage, instances, groups, inst, nil
		}
	}
	_ = storage.Close()
	return nil, nil, nil, nil, fmt.Errorf("session %q not found", sessionID)
}

func waitForLocalActionReady(ctx context.Context, tmuxSess *tmux.Session, tool string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := 10 * time.Minute
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return ctx.Err()
	}
	if err := send.WaitForAgentReady(tmuxSess, tool, timeout, send.PromptGates{
		ClaudeComposer: session.IsClaudeCompatible(tool),
		CodexPrompt:    session.IsCodexCompatible(tool),
	}); err != nil {
		return fmt.Errorf("wait for agent ready: %w", err)
	}
	return ctx.Err()
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
