package hub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	RestartFresh(ctx context.Context, sessionID string) error
	Fork(ctx context.Context, sessionID string) (string, error)
	Rename(ctx context.Context, sessionID, title string) error
	Create(ctx context.Context, req CreateSessionRequest) (string, error)
	Delete(ctx context.Context, sessionID string) error
	Archive(ctx context.Context, sessionID string) error
	Unarchive(ctx context.Context, sessionID string) error
	Remove(ctx context.Context, sessionID string) error
	Move(ctx context.Context, sessionID, groupPath string) error
	Update(ctx context.Context, req UpdateSessionRequest) (UpdateSessionResponse, error)
	UpdatePaths(ctx context.Context, req UpdateSessionPathsRequest) (UpdateSessionPathsResponse, error)
	ToggleYolo(ctx context.Context, sessionID string) error
	MarkUnread(ctx context.Context, sessionID string) error
	Preview(ctx context.Context, sessionID string) (string, error)
	ImportTmux(ctx context.Context) (int, error)
}

const MaxWebProxyBodyBytes = 8 * 1024 * 1024

type WebProxyBackend interface {
	ProxyWeb(ctx context.Context, req WebProxyRequest) (WebProxyResponse, error)
}

type WebProxyRequest struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Header  map[string][]string `json:"header,omitempty"`
	BodyB64 string              `json:"body_b64,omitempty"`
}

type WebProxyResponse struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header,omitempty"`
	BodyB64    string              `json:"body_b64,omitempty"`
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

type SessionFieldChange struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type UpdateSessionRequest struct {
	SessionID string               `json:"session_id"`
	Changes   []SessionFieldChange `json:"changes"`
}

type UpdateSessionResponse struct {
	Restarted bool `json:"restarted,omitempty"`
}

type UpdateSessionPathsRequest struct {
	SessionID string   `json:"session_id"`
	Paths     []string `json:"paths"`
}

type UpdateSessionPathsResponse struct {
	Restarted bool `json:"restarted,omitempty"`
}

type ImportTmuxSessionsResponse struct {
	Imported int `json:"imported"`
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

type moveActionPayload struct {
	SessionID string `json:"session_id"`
	GroupPath string `json:"group_path"`
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
	case "restart_fresh":
		payload, err := decodeSessionAction(cmd.Payload, "restart_fresh")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.RestartFresh(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "fork":
		payload, err := decodeSessionAction(cmd.Payload, "fork")
		if err != nil {
			return nil, err
		}
		sessionID, err := d.Backend.Fork(ctx, payload.SessionID)
		if err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: sessionID})
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
	case "delete":
		payload, err := decodeSessionAction(cmd.Payload, "delete")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.Delete(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "archive":
		payload, err := decodeSessionAction(cmd.Payload, "archive")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.Archive(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "unarchive":
		payload, err := decodeSessionAction(cmd.Payload, "unarchive")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.Unarchive(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "remove":
		payload, err := decodeSessionAction(cmd.Payload, "remove")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.Remove(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "move":
		var payload moveActionPayload
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("move action session_id is required")
		}
		if strings.TrimSpace(payload.GroupPath) == "" {
			return nil, fmt.Errorf("move action group_path is required")
		}
		if err := d.Backend.Move(ctx, payload.SessionID, payload.GroupPath); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "update":
		var payload UpdateSessionRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("update action session_id is required")
		}
		if len(payload.Changes) == 0 {
			return nil, fmt.Errorf("update action changes are required")
		}
		result, err := d.Backend.Update(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "update_paths":
		var payload UpdateSessionPathsRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("update_paths action session_id is required")
		}
		if len(payload.Paths) < 2 {
			return nil, fmt.Errorf("update_paths action requires at least two paths")
		}
		result, err := d.Backend.UpdatePaths(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "toggle_yolo":
		payload, err := decodeSessionAction(cmd.Payload, "toggle_yolo")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.ToggleYolo(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
	case "mark_unread":
		payload, err := decodeSessionAction(cmd.Payload, "mark_unread")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.MarkUnread(ctx, payload.SessionID); err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: payload.SessionID})
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
	case "import_tmux":
		imported, err := d.Backend.ImportTmux(ctx)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(ImportTmuxSessionsResponse{Imported: imported})
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "web_proxy":
		backend, ok := d.Backend.(WebProxyBackend)
		if !ok {
			return nil, fmt.Errorf("hub web proxy backend is not configured")
		}
		var payload WebProxyRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		result, err := backend.ProxyWeb(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
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

func sanitizeWebProxyPath(rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "/", nil
	}
	if strings.HasPrefix(rawPath, "//") {
		return "", fmt.Errorf("web proxy path must not start with //")
	}
	u, err := url.ParseRequestURI(rawPath)
	if err != nil {
		return "", fmt.Errorf("invalid web proxy path: %w", err)
	}
	if u.IsAbs() || u.Host != "" {
		return "", fmt.Errorf("web proxy path must be relative to the local agent-deck web server")
	}
	if !strings.HasPrefix(u.Path, "/") {
		return "", fmt.Errorf("web proxy path must start with /")
	}
	return u.RequestURI(), nil
}

func decodeWebProxyBody(bodyB64 string) ([]byte, error) {
	bodyB64 = strings.TrimSpace(bodyB64)
	if bodyB64 == "" {
		return nil, nil
	}
	if len(bodyB64) > base64.StdEncoding.EncodedLen(MaxWebProxyBodyBytes) {
		return nil, fmt.Errorf("web proxy request body exceeds %d bytes", MaxWebProxyBodyBytes)
	}
	body, err := base64.StdEncoding.DecodeString(bodyB64)
	if err != nil {
		return nil, fmt.Errorf("decode web proxy request body: %w", err)
	}
	if len(body) > MaxWebProxyBodyBytes {
		return nil, fmt.Errorf("web proxy request body exceeds %d bytes", MaxWebProxyBodyBytes)
	}
	return body, nil
}

func copyAllowedWebProxyHeaders(dst http.Header, src map[string][]string) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(key))
		switch canonical {
		case "Accept", "Accept-Encoding", "Accept-Language", "Content-Type", "If-Modified-Since", "If-None-Match", "User-Agent":
		default:
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				dst.Add(canonical, value)
			}
		}
	}
}

func filteredWebProxyResponseHeader(src http.Header) map[string][]string {
	out := make(map[string][]string)
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(key))
		switch canonical {
		case "Cache-Control", "Content-Type", "ETag", "Expires", "Last-Modified", "Location":
		default:
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				out[canonical] = append(out[canonical], value)
			}
		}
	}
	return out
}

func readLimitedWebProxyBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, MaxWebProxyBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read web proxy response body: %w", err)
	}
	if len(data) > MaxWebProxyBodyBytes {
		return nil, fmt.Errorf("web proxy response body exceeds %d bytes", MaxWebProxyBodyBytes)
	}
	return data, nil
}

type LocalActionBackend struct {
	Profile string
}

func (b LocalActionBackend) ProxyWeb(ctx context.Context, req WebProxyRequest) (WebProxyResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return WebProxyResponse{}, fmt.Errorf("web proxy method %q is not allowed", method)
	}
	path, err := sanitizeWebProxyPath(req.Path)
	if err != nil {
		return WebProxyResponse{}, err
	}
	body, err := decodeWebProxyBody(req.BodyB64)
	if err != nil {
		return WebProxyResponse{}, err
	}
	proxyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(proxyCtx, method, "http://127.0.0.1:8420"+path, bytes.NewReader(body))
	if err != nil {
		return WebProxyResponse{}, err
	}
	copyAllowedWebProxyHeaders(httpReq.Header, req.Header)
	httpReq.Header.Set("X-Agent-Deck-Hub-Proxy", "1")
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return WebProxyResponse{}, fmt.Errorf("remote agent-deck web server is not reachable on 127.0.0.1:8420: %w", err)
	}
	defer resp.Body.Close()
	data, err := readLimitedWebProxyBody(resp.Body)
	if err != nil {
		return WebProxyResponse{}, err
	}
	return WebProxyResponse{
		StatusCode: resp.StatusCode,
		Header:     filteredWebProxyResponseHeader(resp.Header),
		BodyB64:    base64.StdEncoding.EncodeToString(data),
	}, nil
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

func (b LocalActionBackend) RestartFresh(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := inst.RestartFresh(); err != nil {
		return err
	}
	inst.LastStartedAt = time.Now()
	inst.PostStartSync(3 * time.Second)
	return storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups))
}

func (b LocalActionBackend) Fork(ctx context.Context, sessionID string) (string, error) {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return "", err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	if !inst.CanFork() {
		return "", fmt.Errorf("session %q cannot be forked", inst.Title)
	}
	forkTitle := strings.TrimSpace(inst.Title)
	if forkTitle == "" {
		forkTitle = inst.ID
	}
	forked, _, err := inst.CreateForkedInstanceForTool(forkTitle+" (fork)", inst.GroupPath, nil)
	if err != nil {
		return "", fmt.Errorf("create fork: %w", err)
	}
	if err := forked.Start(); err != nil {
		return "", fmt.Errorf("start fork: %w", err)
	}
	forked.PostStartSync(3 * time.Second)
	instances = append(instances, forked)
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return "", err
	}
	return forked.ID, nil
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
		if err := postCommit(); err != nil {
			return err
		}
	}
	if err := session.SyncClaudeSessionNameForInstance(inst); err != nil {
		return fmt.Errorf("sync session name: %w", err)
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

func (b LocalActionBackend) Delete(ctx context.Context, sessionID string) error {
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
	if inst.IsWorktree() {
		if _, err := session.RemoveSessionWorktreeUnlessShared(inst, instances); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}
	if inst.IsMultiRepo() && strings.TrimSpace(inst.MultiRepoTempDir) != "" {
		_ = os.RemoveAll(inst.MultiRepoTempDir)
	}
	filtered := instances[:0]
	for _, candidate := range instances {
		if candidate == nil || candidate.ID == sessionID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return storage.SaveWithGroups(filtered, session.NewGroupTreeWithGroups(filtered, groups))
}

func (b LocalActionBackend) Archive(ctx context.Context, sessionID string) error {
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
	inst.ArchivedAt = time.Now().UTC()
	return storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups))
}

func (b LocalActionBackend) Unarchive(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	inst.ArchivedAt = time.Time{}
	return storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups))
}

func (b LocalActionBackend) Remove(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	status := inst.GetStatusThreadSafe()
	if status != session.StatusStopped && status != session.StatusError {
		return fmt.Errorf("session must be stopped or errored to remove; got %s", status)
	}
	filtered := instances[:0]
	for _, candidate := range instances {
		if candidate == nil || candidate.ID == sessionID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return storage.SaveWithGroups(filtered, session.NewGroupTreeWithGroups(filtered, groups))
}

func (b LocalActionBackend) Move(ctx context.Context, sessionID, groupPath string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	groupPath = strings.Trim(strings.TrimSpace(groupPath), "/")
	if groupPath == "" {
		groupPath = session.DefaultGroupPath
	}
	groupTree := session.NewGroupTreeWithGroups(instances, groups)
	groupTree.MoveSessionToGroup(inst, groupPath)
	return storage.SaveWithGroups(groupTree.GetAllInstances(), groupTree)
}

func (b LocalActionBackend) Update(ctx context.Context, req UpdateSessionRequest) (UpdateSessionResponse, error) {
	storage, instances, groups, inst, err := b.loadSessionData(req.SessionID)
	if err != nil {
		return UpdateSessionResponse{}, err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return UpdateSessionResponse{}, err
	}

	ordered := orderFieldChangesForUpdate(req.Changes)
	restartRequired := false
	titleChanged := false
	var postCommits []func() error
	for _, change := range ordered {
		field := strings.TrimSpace(change.Field)
		if field == "" {
			return UpdateSessionResponse{}, fmt.Errorf("update action field is required")
		}
		_, postCommit, err := session.SetField(inst, field, change.Value, nil)
		if err != nil {
			return UpdateSessionResponse{}, err
		}
		if postCommit != nil {
			postCommits = append(postCommits, postCommit)
		}
		if field == session.FieldTitle {
			titleChanged = true
		}
		if session.RestartPolicyFor(field) == session.FieldRestartRequired {
			restartRequired = true
		}
	}
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return UpdateSessionResponse{}, err
	}
	for _, postCommit := range postCommits {
		if err := postCommit(); err != nil {
			return UpdateSessionResponse{}, err
		}
	}
	if titleChanged {
		if err := session.SyncClaudeSessionNameForInstance(inst); err != nil {
			return UpdateSessionResponse{}, fmt.Errorf("sync session name: %w", err)
		}
	}

	result := UpdateSessionResponse{}
	if restartRequired && inst.CanRestart() {
		if err := ctxErr(ctx); err != nil {
			return result, err
		}
		if err := inst.Restart(); err != nil {
			return result, err
		}
		inst.LastStartedAt = time.Now()
		inst.PostStartSync(3 * time.Second)
		result.Restarted = true
		if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (b LocalActionBackend) UpdatePaths(ctx context.Context, req UpdateSessionPathsRequest) (UpdateSessionPathsResponse, error) {
	storage, instances, groups, inst, err := b.loadSessionData(req.SessionID)
	if err != nil {
		return UpdateSessionPathsResponse{}, err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return UpdateSessionPathsResponse{}, err
	}
	paths, err := normalizeMultiRepoPaths(req.Paths)
	if err != nil {
		return UpdateSessionPathsResponse{}, err
	}
	if !inst.IsMultiRepo() {
		return UpdateSessionPathsResponse{}, fmt.Errorf("session %q is not a multi-repo session", inst.Title)
	}
	tempDir := strings.TrimSpace(inst.MultiRepoTempDir)
	if tempDir == "" {
		return UpdateSessionPathsResponse{}, fmt.Errorf("multi-repo session %q has no temp dir", inst.Title)
	}
	if err := rewriteMultiRepoSymlinkTree(inst, tempDir, paths); err != nil {
		return UpdateSessionPathsResponse{}, err
	}
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return UpdateSessionPathsResponse{}, err
	}
	result := UpdateSessionPathsResponse{}
	if inst.CanRestart() {
		if err := ctxErr(ctx); err != nil {
			return result, err
		}
		if err := inst.Restart(); err != nil {
			return result, err
		}
		inst.LastStartedAt = time.Now()
		inst.PostStartSync(3 * time.Second)
		result.Restarted = true
		if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
			return result, err
		}
	}
	return result, nil
}

func normalizeMultiRepoPaths(raw []string) ([]string, error) {
	paths := make([]string, 0, len(raw))
	seen := make(map[string]bool)
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = filepath.Clean(session.ExpandPath(p))
		if seen[p] {
			continue
		}
		seen[p] = true
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("path not found: %s", p)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("path is not a directory: %s", p)
		}
		paths = append(paths, p)
	}
	if len(paths) < 2 {
		return nil, fmt.Errorf("multi-repo requires at least two paths")
	}
	return paths, nil
}

func rewriteMultiRepoSymlinkTree(inst *session.Instance, tempDir string, paths []string) error {
	if err := validateMultiRepoTempDir(tempDir); err != nil {
		return err
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return fmt.Errorf("prepare multi-repo temp dir: %w", err)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return fmt.Errorf("read multi-repo temp dir: %w", err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(tempDir, entry.Name())); err != nil {
			return fmt.Errorf("remove old multi-repo entry %q: %w", entry.Name(), err)
		}
	}

	dirnames := session.DeduplicateDirnames(paths)
	additional := make([]string, 0, len(paths)-1)
	for i, p := range paths {
		linkPath := filepath.Join(tempDir, dirnames[i])
		if err := os.Symlink(p, linkPath); err != nil {
			return fmt.Errorf("link multi-repo path %q: %w", p, err)
		}
		if i == 0 {
			inst.ProjectPath = linkPath
		} else {
			additional = append(additional, linkPath)
		}
	}
	inst.MultiRepoEnabled = true
	inst.AdditionalPaths = additional
	if tmuxSess := inst.GetTmuxSession(); tmuxSess != nil {
		tmuxSess.WorkDir = tempDir
	}
	return nil
}

func validateMultiRepoTempDir(tempDir string) error {
	clean := filepath.Clean(strings.TrimSpace(tempDir))
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("unsafe multi-repo temp dir: %s", tempDir)
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "multi-repo-worktrees" {
			return nil
		}
	}
	return fmt.Errorf("unsafe multi-repo temp dir %q: expected path under multi-repo-worktrees", tempDir)
}

func (b LocalActionBackend) ToggleYolo(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}

	toggled := false
	switch inst.Tool {
	case "gemini":
		currentYolo := false
		if inst.GeminiYoloMode != nil {
			currentYolo = *inst.GeminiYoloMode
		} else if userConfig, _ := session.LoadUserConfig(); userConfig != nil {
			currentYolo = userConfig.Gemini.YoloMode
		}
		newYolo := !currentYolo
		inst.GeminiYoloMode = &newYolo
		toggled = true
	case "codex":
		currentYolo := false
		opts := inst.GetCodexOptions()
		if opts != nil && opts.YoloMode != nil {
			currentYolo = *opts.YoloMode
		} else if userConfig, _ := session.LoadUserConfig(); userConfig != nil {
			currentYolo = userConfig.Codex.YoloMode
		}
		newYolo := !currentYolo
		if opts == nil {
			opts = &session.CodexOptions{}
		}
		opts.YoloMode = &newYolo
		_ = inst.SetCodexOptions(opts)
		toggled = true
	case "hermes":
		currentYolo := false
		opts := inst.GetHermesOptions()
		if opts != nil && opts.YoloMode != nil {
			currentYolo = *opts.YoloMode
		} else if userConfig, _ := session.LoadUserConfig(); userConfig != nil {
			currentYolo = userConfig.Hermes.YoloMode
		}
		newYolo := !currentYolo
		if opts == nil {
			opts = &session.HermesOptions{}
		}
		opts.YoloMode = &newYolo
		_ = inst.SetHermesOptions(opts)
		toggled = true
	}
	if !toggled {
		return fmt.Errorf("session tool %q does not support yolo toggle", inst.Tool)
	}
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return err
	}
	status := inst.GetStatusThreadSafe()
	if status == session.StatusRunning || status == session.StatusWaiting {
		if err := inst.Restart(); err != nil {
			return err
		}
		inst.LastStartedAt = time.Now()
		inst.PostStartSync(3 * time.Second)
		return storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups))
	}
	return nil
}

func (b LocalActionBackend) MarkUnread(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	tmuxSess := inst.GetTmuxSession()
	if tmuxSess == nil || strings.TrimSpace(tmuxSess.Name) == "" {
		return fmt.Errorf("session %q has no tmux session", inst.Title)
	}
	tmuxSess.ResetAcknowledged()
	inst.ForceNextStatusCheck()
	_ = inst.UpdateStatus()
	return storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups))
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

func (b LocalActionBackend) ImportTmux(ctx context.Context) (int, error) {
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return 0, err
	}
	defer storage.Close()
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		return 0, err
	}
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	discovered, err := session.DiscoverExistingTmuxSessions(instances)
	if err != nil {
		return 0, err
	}
	if len(discovered) == 0 {
		return 0, nil
	}
	instances = append(instances, discovered...)
	groupTree := session.NewGroupTreeWithGroups(instances, groups)
	for _, inst := range discovered {
		groupTree.AddSession(inst)
	}
	if err := storage.SaveWithGroups(instances, groupTree); err != nil {
		return 0, err
	}
	return len(discovered), nil
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

func orderFieldChangesForUpdate(changes []SessionFieldChange) []SessionFieldChange {
	ordered := make([]SessionFieldChange, 0, len(changes))
	for _, change := range changes {
		if change.Field != session.FieldTool {
			ordered = append(ordered, change)
		}
	}
	for _, change := range changes {
		if change.Field == session.FieldTool {
			ordered = append(ordered, change)
		}
	}
	return ordered
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
