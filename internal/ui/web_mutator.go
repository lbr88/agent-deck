package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/git"
	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/vcs"
	"github.com/asheshgoplani/agent-deck/internal/vcsbackend"
	"github.com/asheshgoplani/agent-deck/internal/web"
)

// Compile-time check: WebMutator must implement web.SessionMutator.
var _ web.SessionMutator = (*WebMutator)(nil)
var _ web.HubSessionCreator = (*WebMutator)(nil)
var _ web.HubDashboardProxy = (*WebMutator)(nil)
var _ web.HubTerminalAttacher = (*WebMutator)(nil)
var _ web.LocalTerminalAcknowledger = (*WebMutator)(nil)
var _ web.LocalSandboxShellOpener = (*WebMutator)(nil)
var _ web.SessionOutputProvider = (*WebMutator)(nil)
var _ web.SessionForkOptionsMutator = (*WebMutator)(nil)
var _ web.MCPManager = (*WebMutator)(nil)
var _ web.SkillsService = (*WebMutator)(nil)
var _ web.PluginManager = (*WebMutator)(nil)

// WebMutator bridges the web HTTP handlers to the TUI session/group management
// methods. It wraps the Home model and implements web.SessionMutator.
//
// The undoStack/undoWindow fields support the web's Chrome-style undo of
// deletes (POST /api/sessions/undelete). The TUI maintains its own
// in-memory stack in Home; the web stack is kept here so that web
// deletes/undos don't race with the Tea Update goroutine.
type WebMutator struct {
	h *Home

	undoMu     sync.Mutex
	undoStack  []webDeletedEntry
	undoWindow time.Duration

	// headlessTxMu serializes the full hydrate -> mutate -> persist transaction
	// in headless (`web --no-tui`) mode (#1397). Without it, two concurrent HTTP
	// handlers could each hydrate (replacing h.instances/instanceByID/groupTree),
	// mutate a now-detached snapshot, and persist over each other — a lost
	// update. Only contended in headless mode; in live-TUI mode the Tea loop
	// owns that state and the mutator never hydrates, so this is uncontended.
	headlessTxMu sync.Mutex
}

type webDeletedEntry struct {
	instance     *session.Instance
	hubNodeID    string
	hubSessionID string
	deletedAt    time.Time
}

// NewWebMutator returns a WebMutator backed by the given Home. The undo
// window defaults to web.DefaultUndoWindow (30s).
func NewWebMutator(h *Home) *WebMutator {
	return &WebMutator{h: h, undoWindow: web.DefaultUndoWindow}
}

// WithUndoWindow overrides the undo grace period (useful for tests that
// need to force expiry without sleeping).
func (m *WebMutator) WithUndoWindow(d time.Duration) *WebMutator {
	m.undoWindow = d
	return m
}

// beginHeadlessTx serializes and hydrates a headless mutation (#1397). It
// returns an unlock function the caller MUST defer.
//
// In `web --no-tui` mode no bubbletea loop ever populates
// h.instances/instanceByID/groupTree, so every lookup would miss pre-existing
// sessions and persistAllInstances([]) would trip the empty-sweep guard. This
// helper:
//
//  1. takes headlessTxMu so the whole hydrate -> mutate -> persist sequence runs
//     as one critical section (no concurrent handler can replace the in-memory
//     snapshot mid-mutation — prevents lost updates), and
//  2. reloads the registry from storage so the mutation sees the current state,
//     including out-of-band changes from a concurrent CLI add/rm.
//
// In live-TUI mode it is a pure no-op (returns a no-op unlock and does NOT
// hydrate): the Tea loop owns that state and re-reading/locking here would
// race it.
//
// Callers in live mode pay only a nil check and a closure; the mutex is never
// contended because hydration never runs there.
func (m *WebMutator) beginHeadlessTx() (unlock func(), err error) {
	if m.h == nil || !m.h.IsHeadless() {
		return func() {}, nil
	}
	m.headlessTxMu.Lock()
	if hErr := m.h.HydrateInstancesFromStorage(); hErr != nil {
		m.headlessTxMu.Unlock()
		return func() {}, hErr
	}
	return m.headlessTxMu.Unlock, nil
}

// CreateSession creates and starts a new session, persisting it to storage.
func (m *WebMutator) CreateSession(title, tool, projectPath, groupPath, modelID, reasoningEffort string) (string, error) {
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", err
	}
	defer unlock()
	// #1706: project_path is identity and must be absolute — the request may
	// carry a relative path, which tmux would resolve against the tmux server's
	// cwd rather than this process's.
	projectPath, err = session.ResolveProjectPath(projectPath)
	if err != nil {
		return "", err
	}
	var inst *session.Instance
	if groupPath != "" {
		inst = session.NewInstanceWithGroupAndTool(title, projectPath, groupPath, tool)
	} else {
		inst = session.NewInstanceWithTool(title, projectPath, tool)
	}
	if tool != "" && tool != "shell" {
		inst.Command = tool
	}

	if modelID = strings.TrimSpace(modelID); modelID != "" {
		if err := inst.ApplyLaunchModel(modelID); err != nil {
			return "", err
		}
	}
	if reasoningEffort = strings.TrimSpace(reasoningEffort); reasoningEffort != "" {
		if err := inst.ApplyLaunchReasoningEffort(reasoningEffort); err != nil {
			return "", err
		}
	}

	if err := inst.Start(); err != nil {
		return "", fmt.Errorf("start session: %w", err)
	}

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return "", fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	existing := make([]*session.Instance, len(m.h.instances))
	copy(existing, m.h.instances)
	m.h.instancesMu.RUnlock()

	allInstances := append(existing, inst) //nolint:gocritic
	if err := storage.SaveWithGroups(allInstances, m.h.groupTree); err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}
	return inst.ID, nil
}

func (m *WebMutator) CreateHubSession(title, tool, projectPath, groupPath, modelID, hubNodeID string) (string, error) {
	req := hub.CreateSessionRequest{
		Title:       strings.TrimSpace(title),
		Tool:        strings.TrimSpace(tool),
		ProjectPath: strings.TrimSpace(projectPath),
		GroupPath:   strings.Trim(strings.TrimSpace(groupPath), "/"),
		ModelID:     strings.TrimSpace(modelID),
	}
	if req.ProjectPath == "" {
		req.ProjectPath = "."
	}
	raw, err := m.hubCommand(strings.TrimSpace(hubNodeID), "create", req)
	if err != nil {
		return "", err
	}
	var result struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &result)
	if strings.TrimSpace(result.SessionID) == "" {
		m.publishHubWebSnapshot()
		return "", nil
	}
	m.publishHubWebSnapshot()
	return web.HubSessionWebID(hubNodeID, result.SessionID), nil
}

func (m *WebMutator) CreateSessionWithOptions(req web.CreateSessionRequest) (string, error) {
	req.Title = strings.TrimSpace(req.Title)
	req.Tool = strings.TrimSpace(req.Tool)
	req.ProjectPath = strings.TrimSpace(req.ProjectPath)
	req.GroupPath = strings.Trim(strings.TrimSpace(req.GroupPath), "/")
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.HubNodeID = strings.TrimSpace(req.HubNodeID)
	req.AdditionalPaths = normalizeWebCreateAdditionalPaths(req.AdditionalPaths)
	if req.HubNodeID != "" {
		hubReq := hub.CreateSessionRequest{
			Title:           req.Title,
			Tool:            req.Tool,
			ProjectPath:     req.ProjectPath,
			AdditionalPaths: append([]string(nil), req.AdditionalPaths...),
			GroupPath:       req.GroupPath,
			ModelID:         req.ModelID,
			ReasoningEffort: req.ReasoningEffort,
		}
		if hubReq.ProjectPath == "" {
			hubReq.ProjectPath = "."
		}
		raw, err := m.hubCommand(req.HubNodeID, "create", hubReq)
		if err != nil {
			return "", err
		}
		var result struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(raw, &result)
		m.publishHubWebSnapshot()
		if strings.TrimSpace(result.SessionID) == "" {
			return "", nil
		}
		return web.HubSessionWebID(req.HubNodeID, result.SessionID), nil
	}
	if len(req.AdditionalPaths) == 0 {
		return m.CreateSession(req.Title, req.Tool, req.ProjectPath, req.GroupPath, req.ModelID, req.ReasoningEffort)
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", err
	}
	defer unlock()
	if req.ProjectPath == "" {
		return "", fmt.Errorf("project path is required")
	}
	command := req.Tool
	if command == "" {
		command = session.GetDefaultTool()
	}
	if command == "" {
		command = "claude"
	}
	cmd := m.h.createSessionInGroupWithWorktreeAndOptions(
		req.Title,
		req.ProjectPath,
		command,
		req.GroupPath,
		"", "", "",
		false,
		false,
		nil,
		nil,
		"",
		req.ModelID,
		true,
		append([]string(nil), req.AdditionalPaths...),
		"", "",
		"",
		false,
	)
	msg, ok := cmd().(sessionCreatedMsg)
	if !ok {
		return "", fmt.Errorf("create session returned unexpected result")
	}
	if msg.err != nil {
		return "", msg.err
	}
	if msg.instance == nil {
		return "", fmt.Errorf("create session returned no session")
	}
	if strings.TrimSpace(req.ReasoningEffort) != "" {
		if err := msg.instance.ApplyLaunchReasoningEffort(req.ReasoningEffort); err != nil {
			return "", err
		}
	}
	m.finalizeCreatedSession(msg.instance)
	return msg.instance.ID, nil
}

func normalizeWebCreateAdditionalPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func (m *WebMutator) finalizeCreatedSession(inst *session.Instance) {
	if inst == nil || m == nil || m.h == nil {
		return
	}
	m.h.instancesMu.Lock()
	m.h.instances = append(m.h.instances, inst)
	if m.h.instanceByID == nil {
		m.h.instanceByID = make(map[string]*session.Instance)
	}
	m.h.instanceByID[inst.ID] = inst
	session.UpdateClaudeSessionsWithDedup(m.h.instances)
	m.h.instancesMu.Unlock()
	m.h.cachedStatusCounts.valid.Store(false)
	if m.h.groupTree == nil {
		m.h.groupTree = session.NewGroupTree(m.h.instances)
	}
	if inst.GroupPath != "" {
		m.h.groupTree.ExpandGroupWithParents(inst.GroupPath)
	}
	m.h.groupTree.AddSession(inst)
	m.h.rebuildFlatItems()
	if m.h.search != nil {
		m.h.search.SetItems(m.h.instances)
	}
	m.h.forceSaveInstances()
	m.h.publishWebMenuSnapshot()
}

func (m *WebMutator) ProxyHubWeb(ctx context.Context, nodeID string, req hub.WebProxyRequest) (hub.WebProxyResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := m.hubCommandWithContext(ctx, strings.TrimSpace(nodeID), "web_proxy", req)
	if err != nil {
		return hub.WebProxyResponse{}, err
	}
	var result hub.WebProxyResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return hub.WebProxyResponse{}, fmt.Errorf("decode hub web proxy response: %w", err)
	}
	return result, nil
}

func (m *WebMutator) OpenHubTerminal(ctx context.Context, nodeID, sessionID string, size hub.TerminalSize) (hub.AttachStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m == nil || m.h == nil || m.h.hubClient == nil {
		return nil, fmt.Errorf("hub client is not connected")
	}
	return m.h.hubClient.OpenAttach(ctx, strings.TrimSpace(nodeID), strings.TrimSpace(sessionID), size)
}

func (m *WebMutator) OpenHubSandboxShellTerminal(ctx context.Context, nodeID, sessionID string, size hub.TerminalSize) (hub.AttachStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	nodeID = strings.TrimSpace(nodeID)
	sessionID = strings.TrimSpace(sessionID)
	if nodeID == "" || sessionID == "" {
		return nil, fmt.Errorf("hub sandbox shell target is incomplete")
	}
	raw, err := m.hubCommandWithContext(ctx, nodeID, "sandbox_shell", hub.SandboxShellRequest{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	var resp hub.SandboxShellResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode hub sandbox shell response: %w", err)
	}
	attachSessionID := strings.TrimSpace(resp.AttachSessionID)
	if attachSessionID == "" {
		return nil, fmt.Errorf("hub sandbox shell returned no attach session")
	}
	if m == nil || m.h == nil || m.h.hubClient == nil {
		return nil, fmt.Errorf("hub client is not connected")
	}
	return m.h.hubClient.OpenAttach(ctx, nodeID, attachSessionID, size)
}

func (m *WebMutator) OpenLocalSandboxShell(ctx context.Context, sessionID string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", "", fmt.Errorf("session id is required")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", "", err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[sessionID]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return "", "", web.ErrSessionNotFound
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if !inst.IsSandboxed() || strings.TrimSpace(inst.SandboxContainer) == "" {
		return "", "", fmt.Errorf("session %q is not a running sandbox session", inst.Title)
	}
	tmuxName, err := inst.OpenContainerShell()
	if err != nil {
		return "", "", err
	}
	return tmuxName, inst.TmuxSocketName, nil
}

func (m *WebMutator) AcknowledgeLocalTerminal(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if _, _, ok := web.ParseHubSessionWebID(sessionID); ok {
		return nil
	}
	if m == nil || m.h == nil {
		return fmt.Errorf("web mutator is not attached")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()

	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[sessionID]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if !m.h.acknowledgeViewedSession(inst) {
		return nil
	}
	_ = inst.UpdateStatus()

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()
	if db := storage.GetDB(); db != nil {
		_ = db.SetAcknowledged(inst.ID, true)
		_ = db.WriteStatus(inst.ID, string(inst.GetStatusThreadSafe()), inst.GetToolThreadSafe())
	}
	m.h.cachedStatusCounts.valid.Store(false)
	m.h.refreshSessionRenderSnapshot(nil)
	m.h.publishCurrentSessionStates()
	return nil
}

// StartSession starts a stopped/idle session by ID.
func (m *WebMutator) StartSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		return m.hubSessionAction(nodeID, sessionID, "start")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	return inst.Start()
}

// StopSession kills (stops) a running session by ID.
func (m *WebMutator) StopSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		return m.hubSessionAction(nodeID, sessionID, "stop")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	return inst.Kill()
}

// RestartSession restarts a session by ID.
func (m *WebMutator) RestartSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		return m.hubSessionAction(nodeID, sessionID, "restart")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	return inst.Restart()
}

// RestartFreshSession restarts without resuming the current tool session
// binding. Mirrors TUI T and hub action restart_fresh.
func (m *WebMutator) RestartFreshSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		return m.hubSessionAction(nodeID, sessionID, "restart_fresh")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	restartErr := inst.RestartFresh()
	// RestartFresh clears the binding before touching tmux. Persist that explicit
	// tombstone even when the restart itself fails, matching the TUI/hub paths
	// and preventing the next web process from resurrecting the old thread.
	persistErr := m.persistAllInstances()
	if restartErr != nil || persistErr != nil {
		return errors.Join(restartErr, persistErr)
	}
	return nil
}

// DeleteSession kills a session and removes it from persistent storage.
// Before removal, the instance is pushed onto the web undo stack so a
// subsequent UndoDelete (POST /api/sessions/undelete) can restore it.
func (m *WebMutator) DeleteSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		if err := m.hubSessionAction(nodeID, sessionID, "delete"); err != nil {
			return err
		}
		m.pushHubUndo(nodeID, sessionID)
		m.h.removeHubSessionFromCache(nodeID, sessionID)
		m.publishHubWebSnapshot()
		return nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}

	// Kill the tmux session (ignore errors — may already be stopped)
	_ = inst.Kill()

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	if err := storage.DeleteInstance(id); err != nil {
		return err
	}
	m.pushUndo(inst)
	return nil
}

// RemoveSession removes stopped/error session metadata without killing the
// process or pruning worktrees. Mirrors TUI X / CLI `session remove`.
func (m *WebMutator) RemoveSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		if err := m.hubSessionAction(nodeID, sessionID, "remove"); err != nil {
			return err
		}
		m.h.removeHubSessionFromCache(nodeID, sessionID)
		m.publishHubWebSnapshot()
		return nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	status := session.Status("")
	if inst != nil {
		status = inst.GetStatusThreadSafe()
	}
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	if status != session.StatusStopped && status != session.StatusError {
		return fmt.Errorf("session must be stopped or errored to remove; got %s", status)
	}

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	remaining := make([]*session.Instance, 0, len(m.h.instances))
	for _, candidate := range m.h.instances {
		if candidate != nil && candidate.ID != id {
			remaining = append(remaining, candidate)
		}
	}
	m.h.instancesMu.RUnlock()
	if err := storage.RemoveSessionAndVerify(id, remaining, m.h.groupTree); err != nil {
		return fmt.Errorf("remove session: %w", err)
	}
	return nil
}

// ToggleYoloSession toggles tool-specific YOLO/auto-approve state. For running
// sessions it restarts the session so the setting takes effect immediately,
// matching the TUI action.
func (m *WebMutator) ToggleYoloSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		return m.hubSessionAction(nodeID, sessionID, "toggle_yolo")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}

	toggled := false
	m.h.instancesMu.Lock()
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
	m.h.instancesMu.Unlock()
	if !toggled {
		return fmt.Errorf("session tool %q does not support yolo toggle", inst.Tool)
	}
	if err := m.persistAllInstances(); err != nil {
		return err
	}
	status := inst.GetStatusThreadSafe()
	if status == session.StatusRunning || status == session.StatusWaiting {
		if err := inst.Restart(); err != nil {
			return err
		}
		return m.persistAllInstances()
	}
	return nil
}

// CloseSession stops the session process but keeps its metadata in
// storage. Mirrors the TUI's Shift+D handler (internal/ui/home.go
// closeSession). Identical to StopSession at the session.Instance level
// — both call Kill() — but is kept distinct so the parity matrix and
// the front-end can express the user-visible intent ("close, but don't
// delete").
func (m *WebMutator) CloseSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		return m.hubSessionAction(nodeID, sessionID, "stop")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	return inst.Kill()
}

// ArchiveSession stops the session process and marks it archived so it
// is hidden from active lists but retained in storage.
func (m *WebMutator) ArchiveSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		if err := m.hubSessionAction(nodeID, sessionID, "archive"); err != nil {
			return err
		}
		m.h.updateHubSessionArchived(nodeID, sessionID, time.Now().UTC())
		m.publishHubWebSnapshot()
		return nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	if err := inst.Kill(); err != nil {
		return fmt.Errorf("failed to stop session: %w", err)
	}
	m.h.instancesMu.Lock()
	inst.ArchivedAt = time.Now().UTC()
	m.h.instancesMu.Unlock()
	return m.persistAllInstances()
}

// UnarchiveSession clears the archive flag without starting tmux.
func (m *WebMutator) UnarchiveSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		if err := m.hubSessionAction(nodeID, sessionID, "unarchive"); err != nil {
			return err
		}
		m.h.updateHubSessionArchived(nodeID, sessionID, time.Time{})
		m.publishHubWebSnapshot()
		return nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	m.h.instancesMu.Lock()
	if !inst.IsArchived() {
		m.h.instancesMu.Unlock()
		return fmt.Errorf("session is not archived: %s", id)
	}
	inst.ArchivedAt = time.Time{}
	m.h.instancesMu.Unlock()
	return m.persistAllInstances()
}

func (m *WebMutator) persistAllInstances() error {
	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	if err := storage.SaveWithGroups(instances, m.h.groupTree); err != nil {
		return fmt.Errorf("save sessions: %w", err)
	}
	return nil
}

// UndoDelete restores the most-recently deleted session if its delete
// timestamp is within the configured undo window. Returns the restored
// session id. Returns web.ErrUndoNothing if the stack is empty, or
// web.ErrUndoExpired if the most recent entry is older than the window.
func (m *WebMutator) UndoDelete() (string, error) {
	m.undoMu.Lock()
	if len(m.undoStack) == 0 {
		m.undoMu.Unlock()
		return "", web.ErrUndoNothing
	}
	entry := m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	window := m.undoWindow
	m.undoMu.Unlock()

	if window == 0 {
		window = web.DefaultUndoWindow
	}
	if time.Since(entry.deletedAt) > window {
		return "", web.ErrUndoExpired
	}
	if entry.hubNodeID != "" {
		raw, err := m.hubCommand(entry.hubNodeID, "undo_delete", nil)
		if err != nil {
			if errors.Is(err, hub.ErrHubUndoNothing) {
				return "", web.ErrUndoNothing
			}
			if errors.Is(err, hub.ErrHubUndoExpired) {
				return "", web.ErrUndoExpired
			}
			return "", err
		}
		sessionID := entry.hubSessionID
		var result struct {
			SessionID string `json:"session_id"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &result); err != nil {
				return "", fmt.Errorf("decode hub undo_delete result: %w", err)
			}
			if strings.TrimSpace(result.SessionID) != "" {
				sessionID = strings.TrimSpace(result.SessionID)
			}
		}
		m.publishHubWebSnapshot()
		return web.HubSessionWebID(entry.hubNodeID, sessionID), nil
	}
	if entry.instance == nil {
		return "", web.ErrUndoNothing
	}

	// Restart the session and re-persist alongside the rest of the
	// current in-memory list. Note: Restart() may not succeed for every
	// tool (e.g. a tool the user has since uninstalled). Bubble the
	// error up so the handler returns 500; the entry has already been
	// popped, mirroring the TUI's ctrl+z semantics.
	if err := entry.instance.Restart(); err != nil {
		return "", fmt.Errorf("restart session: %w", err)
	}

	// #1397: hydrate + serialize before reading/persisting the in-memory list so
	// the restored row is appended to the CURRENT registry (in headless mode the
	// list would otherwise be empty, dropping every other session) and so this
	// re-persist does not race a concurrent mutation.
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", err
	}
	defer unlock()

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return "", fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	existing := make([]*session.Instance, len(m.h.instances))
	copy(existing, m.h.instances)
	m.h.instancesMu.RUnlock()
	allInstances := append(existing, entry.instance) //nolint:gocritic
	if err := storage.SaveWithGroups(allInstances, m.h.groupTree); err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}
	return entry.instance.ID, nil
}

// pushUndo records a freshly-deleted instance onto the web undo stack,
// capped at 10 entries (FIFO eviction) to bound memory.
func (m *WebMutator) pushUndo(inst *session.Instance) {
	m.undoMu.Lock()
	defer m.undoMu.Unlock()
	m.undoStack = append(m.undoStack, webDeletedEntry{
		instance:  inst,
		deletedAt: time.Now(),
	})
	if len(m.undoStack) > 10 {
		m.undoStack = m.undoStack[len(m.undoStack)-10:]
	}
}

func (m *WebMutator) pushHubUndo(nodeID, sessionID string) {
	nodeID = strings.TrimSpace(nodeID)
	sessionID = strings.TrimSpace(sessionID)
	if nodeID == "" || sessionID == "" {
		return
	}
	m.undoMu.Lock()
	defer m.undoMu.Unlock()
	m.undoStack = append(m.undoStack, webDeletedEntry{
		hubNodeID:    nodeID,
		hubSessionID: sessionID,
		deletedAt:    time.Now(),
	})
	if len(m.undoStack) > 10 {
		m.undoStack = m.undoStack[len(m.undoStack)-10:]
	}
}

// ForkSession forks an existing session using the proper tool-specific fork command.
func (m *WebMutator) ForkSession(id string) (string, error) {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		raw, err := m.hubCommand(nodeID, "fork", map[string]string{"session_id": sessionID})
		if err != nil {
			return "", err
		}
		var result struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", fmt.Errorf("decode hub fork result: %w", err)
		}
		result.SessionID = strings.TrimSpace(result.SessionID)
		if result.SessionID == "" {
			m.publishHubWebSnapshot()
			return "", nil
		}
		m.publishHubWebSnapshot()
		return web.HubSessionWebID(nodeID, result.SessionID), nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	parent := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if parent == nil {
		return "", fmt.Errorf("session not found: %s", id)
	}

	forked, _, err := parent.CreateForkedInstanceForTool(parent.Title+" (fork)", parent.GroupPath, nil)
	if err != nil {
		return "", fmt.Errorf("fork session: %w", err)
	}

	if err := forked.Start(); err != nil {
		return "", fmt.Errorf("start forked session: %w", err)
	}

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return "", fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	existing := make([]*session.Instance, len(m.h.instances))
	copy(existing, m.h.instances)
	m.h.instancesMu.RUnlock()

	allInstances := append(existing, forked) //nolint:gocritic
	if err := storage.SaveWithGroups(allInstances, m.h.groupTree); err != nil {
		return "", fmt.Errorf("save forked session: %w", err)
	}
	return forked.ID, nil
}

func (m *WebMutator) ForkSessionWithOptions(id string, req web.ForkSessionRequest) (string, error) {
	if !req.HasOptions() {
		return m.ForkSession(id)
	}
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		raw, err := m.hubCommand(nodeID, "fork", hub.ForkSessionRequest{
			SessionID:    sessionID,
			Title:        strings.TrimSpace(req.Title),
			GroupPath:    strings.TrimSpace(req.GroupPath),
			Worktree:     req.Worktree,
			Branch:       strings.TrimSpace(req.Branch),
			WithState:    req.WithState,
			WithIgnored:  req.WithIgnored,
			Sandbox:      req.Sandbox,
			SandboxImage: strings.TrimSpace(req.SandboxImage),
		})
		if err != nil {
			return "", err
		}
		var result struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", fmt.Errorf("decode hub fork result: %w", err)
		}
		result.SessionID = strings.TrimSpace(result.SessionID)
		m.publishHubWebSnapshot()
		if result.SessionID == "" {
			return "", nil
		}
		return web.HubSessionWebID(nodeID, result.SessionID), nil
	}

	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	parent := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if parent == nil {
		return "", fmt.Errorf("session not found: %s", id)
	}
	if !parent.CanFork() {
		return "", fmt.Errorf("session %q cannot be forked", parent.Title)
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(parent.Title)
		if title == "" {
			title = parent.ID
		}
		title += " (fork)"
	}
	groupPath := strings.TrimSpace(req.GroupPath)
	if groupPath == "" {
		groupPath = parent.GroupPath
	}
	branch := strings.TrimSpace(req.Branch)
	if req.Worktree && branch == "" {
		cfg, _ := session.LoadUserConfig()
		forkSettings := session.ForkSettings{}
		if cfg != nil {
			forkSettings = cfg.Fork
		}
		branch = uniqueForkBranch(parent.ProjectPath, quickForkInputs(parent, forkSettings, parent.IsSandboxed()).Branch)
	}
	if m.h.forkingSessions == nil {
		m.h.forkingSessions = make(map[string]time.Time)
	}
	if m.h.launchingSessions == nil {
		m.h.launchingSessions = make(map[string]time.Time)
	}
	result := m.h.buildForkCmd(
		parent,
		title,
		groupPath,
		branch,
		forkToggles{
			Worktree:         req.Worktree,
			WithState:        req.WithState,
			WithIgnored:      req.WithIgnored,
			Sandbox:          req.Sandbox,
			ExplicitWorktree: req.Worktree,
			LockTitle:        strings.TrimSpace(req.Title) != "",
		},
		parent.GetClaudeOptions(),
		parent.ParentSessionID,
		parent.ParentProjectPath,
	)
	if result.errMsg != "" {
		return "", fmt.Errorf("%s", result.errMsg)
	}
	if result.cmd == nil {
		return "", fmt.Errorf("fork command was not created")
	}
	msg, ok := result.cmd().(sessionForkedMsg)
	if !ok {
		return "", fmt.Errorf("unexpected fork result message")
	}
	if msg.sourceID != "" {
		delete(m.h.forkingSessions, msg.sourceID)
	}
	if msg.err != nil {
		return "", msg.err
	}
	if msg.instance == nil {
		return "", fmt.Errorf("fork did not return a session")
	}

	m.h.instancesMu.Lock()
	m.h.instances = append(m.h.instances, msg.instance)
	if m.h.instanceByID == nil {
		m.h.instanceByID = make(map[string]*session.Instance)
	}
	m.h.instanceByID[msg.instance.ID] = msg.instance
	session.UpdateClaudeSessionsWithDedup(m.h.instances)
	m.h.instancesMu.Unlock()

	m.h.cachedStatusCounts.valid.Store(false)
	m.h.launchingSessions[msg.instance.ID] = time.Now()
	if m.h.groupTree == nil {
		m.h.groupTree = session.NewGroupTree(m.h.instances)
	}
	if msg.instance.GroupPath != "" {
		m.h.groupTree.ExpandGroupWithParents(msg.instance.GroupPath)
	}
	m.h.groupTree.AddSession(msg.instance)
	m.h.rebuildFlatItems()
	m.h.search.SetItems(m.h.instances)

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return "", fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()
	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()
	if err := storage.SaveWithGroups(instances, m.h.groupTree); err != nil {
		return "", fmt.Errorf("save forked session: %w", err)
	}
	return msg.instance.ID, nil
}

// UpdateSession applies one or more field edits via session.SetField (the
// same path the TUI EditSessionDialog uses) and persists. Returns the list
// of fields that actually changed and whether any change requires a restart.
//
// instancesMu is held only across the SetField loop; the storage flush and
// postCommits run after unlock, mirroring the TUI's home.go edit handler so
// slow tmux/Codex/Claude side effects don't stall the status worker or precede
// persistence.
func (m *WebMutator) UpdateSession(id string, updates map[string]string) ([]string, bool, []string, error) {
	if len(updates) == 0 {
		return nil, false, nil, nil
	}
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		return m.updateHubSession(nodeID, sessionID, updates)
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return nil, false, nil, err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return nil, false, nil, fmt.Errorf("session not found: %s", id)
	}

	changed := make([]string, 0, len(updates))
	restartRequired := false
	var postCommits []func() error
	titleChanged := false

	m.h.instancesMu.Lock()
	for field, value := range updates {
		oldValue, postCommit, err := session.SetField(inst, field, value, nil)
		if err != nil {
			m.h.instancesMu.Unlock()
			return nil, false, nil, err
		}
		// #1706: SetField canonicalizes a project path, so a request carrying
		// another spelling of the stored path is a no-op — compare what was
		// actually stored, not the raw request value, or it would be reported
		// as changed and restart-required.
		newValue := value
		if field == session.FieldPath {
			newValue = inst.ProjectPath
		}
		if oldValue == newValue && field != session.FieldCodexSessionID {
			continue
		}
		changed = append(changed, field)
		if field == session.FieldTitle {
			titleChanged = true
		}
		if postCommit != nil {
			postCommits = append(postCommits, postCommit)
		}
		if session.RestartPolicyFor(field) == session.FieldRestartRequired {
			restartRequired = true
		}
	}
	m.h.instancesMu.Unlock()

	if len(changed) == 0 {
		return nil, false, nil, nil
	}

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return nil, false, nil, fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	if err := storage.SaveWithGroups(instances, m.h.groupTree); err != nil {
		return nil, false, nil, fmt.Errorf("save session: %w", err)
	}

	var warnings []string
	var postCommitErr error
	for _, fn := range postCommits {
		if err := fn(); err != nil {
			postCommitErr = errors.Join(postCommitErr, err)
		}
	}
	if postCommitErr != nil {
		warnings = append(warnings, session.NewNonFatalWarning("post-save sync failed", postCommitErr).Error())
	}

	if titleChanged {
		if syncErr := session.SyncClaudeSessionNameForInstance(inst); syncErr != nil {
			warnings = append(warnings, fmt.Sprintf("Claude name sync failed: %v", syncErr))
			uiLog.Warn("claude_name_sync_failed",
				slog.String("session_id", id),
				slog.String("claude_session_id", inst.ClaudeSessionID),
				slog.String("error", syncErr.Error()),
			)
		}
	}
	return changed, restartRequired, warnings, nil
}

func (m *WebMutator) updateHubSession(nodeID, sessionID string, updates map[string]string) ([]string, bool, []string, error) {
	changes := make([]hub.SessionFieldChange, 0, len(updates))
	changed := make([]string, 0, len(updates))
	for field, value := range updates {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		changes = append(changes, hub.SessionFieldChange{Field: field, Value: value})
		changed = append(changed, field)
	}
	if len(changes) == 0 {
		return nil, false, nil, nil
	}
	sort.Strings(changed)
	if _, err := m.hubCommand(nodeID, "update", hub.UpdateSessionRequest{SessionID: sessionID, Changes: changes}); err != nil {
		return nil, false, nil, err
	}
	m.h.applyHubSessionFieldChanges(nodeID, sessionID, changes)
	m.publishHubWebSnapshot()
	return changed, false, nil, nil
}

// UpdateSessionPaths mirrors the TUI EditPathsDialog for existing multi-repo
// sessions. Hub session IDs route to the owner node; local IDs rewrite the
// session's multi-repo symlink directory, persist state, and restart.
func (m *WebMutator) UpdateSessionPaths(id string, rawPaths []string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		_, err := m.hubCommand(nodeID, "update_paths", hub.UpdateSessionPathsRequest{
			SessionID: sessionID,
			Paths:     rawPaths,
		})
		if err != nil {
			return err
		}
		m.publishHubWebSnapshot()
		return nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	paths, err := normalizeWebMultiRepoPaths(rawPaths)
	if err != nil {
		return err
	}
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	if !inst.IsMultiRepo() {
		return fmt.Errorf("session %q is not a multi-repo session", inst.Title)
	}
	tempDir := strings.TrimSpace(inst.MultiRepoTempDir)
	if tempDir == "" {
		return fmt.Errorf("multi-repo session %q has no temp dir", inst.Title)
	}
	projectPath, additionalPaths, err := rewriteWebMultiRepoSymlinkTree(tempDir, paths)
	if err != nil {
		return err
	}
	m.h.instancesMu.Lock()
	inst.MultiRepoEnabled = true
	inst.ProjectPath = projectPath
	inst.AdditionalPaths = additionalPaths
	if tmuxSess := inst.GetTmuxSession(); tmuxSess != nil {
		tmuxSess.WorkDir = tempDir
	}
	m.h.instancesMu.Unlock()
	if err := m.persistAllInstances(); err != nil {
		return err
	}
	if err := inst.Restart(); err != nil {
		return err
	}
	return m.persistAllInstances()
}

func normalizeWebMultiRepoPaths(raw []string) ([]string, error) {
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

func rewriteWebMultiRepoSymlinkTree(tempDir string, paths []string) (string, []string, error) {
	if err := validateWebMultiRepoTempDir(tempDir); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("prepare multi-repo temp dir: %w", err)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return "", nil, fmt.Errorf("read multi-repo temp dir: %w", err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(tempDir, entry.Name())); err != nil {
			return "", nil, fmt.Errorf("remove old multi-repo entry %q: %w", entry.Name(), err)
		}
	}
	dirnames := session.DeduplicateDirnames(paths)
	additional := make([]string, 0, len(paths)-1)
	projectPath := ""
	for i, p := range paths {
		linkPath := filepath.Join(tempDir, dirnames[i])
		if err := os.Symlink(p, linkPath); err != nil {
			return "", nil, fmt.Errorf("link multi-repo path %q: %w", p, err)
		}
		if i == 0 {
			projectPath = linkPath
		} else {
			additional = append(additional, linkPath)
		}
	}
	return projectPath, additional, nil
}

func validateWebMultiRepoTempDir(tempDir string) error {
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

// MoveSessionToGroup moves a session between groups. Hub session IDs route to
// the owner node's native move action; local IDs use the same GroupTree helper
// as the TUI move dialog.
func (m *WebMutator) MoveSessionToGroup(id, groupPath string) error {
	groupPath = strings.Trim(strings.TrimSpace(groupPath), "/")
	if groupPath == "" {
		return fmt.Errorf("group path is required")
	}
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		if _, err := m.hubCommand(nodeID, "move", map[string]string{"session_id": sessionID, "group_path": groupPath}); err != nil {
			return err
		}
		m.h.updateHubSessionGroup(nodeID, sessionID, groupPath)
		m.publishHubWebSnapshot()
		return nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	if m.h.groupTree == nil {
		m.h.instancesMu.RLock()
		instances := make([]*session.Instance, len(m.h.instances))
		copy(instances, m.h.instances)
		m.h.instancesMu.RUnlock()
		m.h.groupTree = session.NewGroupTree(instances)
	}
	m.h.groupTree.MoveSessionToGroup(inst, groupPath)
	m.h.instancesMu.Lock()
	m.h.instances = m.h.groupTree.GetAllInstances()
	m.h.instancesMu.Unlock()
	return m.persistAllInstances()
}

// SendSessionPrompt submits a one-line prompt to a local or hub session without
// opening an interactive attach. This is the web equivalent of the TUI `o`
// prompt-session shortcut.
func (m *WebMutator) SendSessionPrompt(id, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("message is required")
	}
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		_, err := m.hubCommand(nodeID, "send", map[string]string{"session_id": sessionID, "message": message})
		return err
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	ts := inst.GetTmuxSession()
	if ts == nil || strings.TrimSpace(ts.Name) == "" {
		return fmt.Errorf("session %q is not running; start it before prompting", inst.Title)
	}
	tmuxName := ts.Name
	go func() {
		if err := deliverToConductorPane(ts, message); err != nil {
			uiLog.Warn("web_prompt_send_failed",
				slog.String("tmux_session", tmuxName),
				slog.String("error", err.Error()))
		}
	}()
	return nil
}

// SendSessionOutput mirrors the TUI `x` workflow: capture the source session's
// displayable output, wrap it with source labels, and submit it to a target
// session without opening an attach. Local sessions use the same content
// extraction helper as the TUI; hub sessions use the owner's preview command.
func (m *WebMutator) SendSessionOutput(sourceID, targetID string) error {
	sourceID = strings.TrimSpace(sourceID)
	targetID = strings.TrimSpace(targetID)
	if sourceID == "" {
		return fmt.Errorf("source session id is required")
	}
	if targetID == "" {
		return fmt.Errorf("target session id is required")
	}
	if sourceID == targetID {
		return fmt.Errorf("target session must be different from source")
	}
	content, title, err := m.sessionOutputForWeb(sourceID)
	if err != nil {
		return err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("no output available for source session")
	}
	if len(content) > maxTransferSize {
		content = content[:maxTransferSize] + "\n[Truncated at 500KB]"
	}
	if strings.TrimSpace(title) == "" {
		title = sourceID
	}
	wrapped := fmt.Sprintf("--- Output from [%s] ---\n%s\n--- End output from [%s] ---\n", title, content, title)
	return m.SendSessionPrompt(targetID, wrapped)
}

func (m *WebMutator) SessionOutput(id string) (web.SessionOutputResponse, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return web.SessionOutputResponse{}, fmt.Errorf("session id is required")
	}
	content, title, err := m.sessionOutputForWeb(id)
	if err != nil {
		return web.SessionOutputResponse{}, err
	}
	return web.SessionOutputResponse{
		SessionID: id,
		Title:     title,
		Content:   content,
	}, nil
}

func (m *WebMutator) sessionOutputForWeb(id string) (content, title string, err error) {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		raw, err := m.hubCommand(nodeID, "preview", map[string]string{"session_id": sessionID})
		if err != nil {
			return "", "", err
		}
		var result hub.PreviewSessionResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", "", fmt.Errorf("decode hub preview response: %w", err)
		}
		return result.Content, m.hubSessionTitle(nodeID, sessionID, id), nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", "", err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return "", "", fmt.Errorf("source session not found: %s", id)
	}
	content, err = getSessionContent(inst)
	if err != nil {
		return "", "", err
	}
	return content, inst.Title, nil
}

func (m *WebMutator) hubSessionTitle(nodeID, sessionID, fallback string) string {
	if m == nil || m.h == nil {
		return fallback
	}
	m.h.hubSessionsMu.RLock()
	defer m.h.hubSessionsMu.RUnlock()
	snapshot, ok := m.h.hubSessions[nodeID]
	if !ok {
		return fallback
	}
	for _, info := range snapshot.Sessions {
		if info.ID == sessionID {
			if title := strings.TrimSpace(info.Title); title != "" {
				return title
			}
			break
		}
	}
	return fallback
}

// QuickApproveSession mirrors the TUI `a` quick-approve shortcut: send
// "1"+Enter to Claude-compatible sessions without opening an attach. Non-Claude
// sessions intentionally no-op, matching Home.handleMainKey's guard.
func (m *WebMutator) QuickApproveSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		if !m.hubSessionClaudeCompatible(nodeID, sessionID) {
			return nil
		}
		_, err := m.hubCommand(nodeID, "send", map[string]string{"session_id": sessionID, "message": "1"})
		return err
	}
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	if !session.IsClaudeCompatible(inst.Tool) {
		return nil
	}
	m.h.quickApprove(inst, -1)
	return nil
}

func (m *WebMutator) hubSessionClaudeCompatible(nodeID, sessionID string) bool {
	m.h.hubSessionsMu.RLock()
	defer m.h.hubSessionsMu.RUnlock()
	snapshot, ok := m.h.hubSessions[nodeID]
	if !ok {
		return false
	}
	for _, info := range snapshot.Sessions {
		if info.ID == sessionID {
			return session.IsClaudeCompatible(info.Tool)
		}
	}
	return false
}

// UpdateSessionNotes saves inline notes for a local or hub session. This is the
// web equivalent of the TUI `e` notes editor and intentionally uses the same
// session.FieldNotes update path as the full settings dialog.
func (m *WebMutator) UpdateSessionNotes(id, notes string) error {
	_, _, _, err := m.UpdateSession(id, map[string]string{session.FieldNotes: notes})
	return err
}

// MarkSessionUnread marks an idle/acknowledged session as needing attention.
// Hub session IDs route to the owner node so the authoritative acknowledged
// state changes where the session lives.
func (m *WebMutator) MarkSessionUnread(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		if _, err := m.hubCommand(nodeID, "mark_unread", map[string]string{"session_id": sessionID}); err != nil {
			return err
		}
		m.h.updateHubSessionStatus(nodeID, sessionID, session.StatusWaiting)
		m.publishHubWebSnapshot()
		return nil
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	ts := inst.GetTmuxSession()
	if ts == nil || strings.TrimSpace(ts.Name) == "" {
		return fmt.Errorf("session %q has no tmux session", inst.Title)
	}
	ts.ResetAcknowledged()
	inst.ForceNextStatusCheck()
	_ = inst.UpdateStatus()
	if err := m.persistAllInstances(); err != nil {
		return err
	}
	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return fmt.Errorf("open storage to persist unread state: %w", err)
	}
	defer storage.Close()
	if err := storage.GetDB().SetAcknowledged(inst.ID, false); err != nil {
		return fmt.Errorf("persist unread state: %w", err)
	}
	return nil
}

func (m *WebMutator) hubSessionAction(nodeID, sessionID, action string) error {
	_, err := m.hubCommand(nodeID, action, map[string]string{"session_id": sessionID})
	if err == nil {
		m.publishHubWebSnapshot()
	}
	return err
}

func (m *WebMutator) hubCommand(nodeID, action string, payload any) (json.RawMessage, error) {
	ctx := m.h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return m.hubCommandWithContext(ctx, nodeID, action, payload)
}

func (m *WebMutator) hubCommandWithContext(ctx context.Context, nodeID, action string, payload any) (json.RawMessage, error) {
	if m == nil || m.h == nil || m.h.hubClient == nil {
		return nil, fmt.Errorf("hub client is not connected")
	}
	nodeID = strings.TrimSpace(nodeID)
	action = strings.TrimSpace(action)
	if nodeID == "" || action == "" {
		return nil, fmt.Errorf("hub action target is incomplete")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return m.h.hubClient.Command(ctx, nodeID, action, payload)
}

func (m *WebMutator) publishHubWebSnapshot() {
	if m == nil || m.h == nil {
		return
	}
	m.h.publishWebMenuSnapshot()
}

func (m *WebMutator) ListCatalog() []web.MCPCatalogEntry {
	return web.NewDefaultMCPManager().ListCatalog()
}

func (m *WebMutator) ListAttached(sessionID, projectPath string) (map[string][]string, error) {
	state, err := m.ListSessionMCPs(sessionID, projectPath)
	if err != nil {
		return nil, err
	}
	return map[string][]string{
		"local":  append([]string(nil), state.Local...),
		"global": append([]string(nil), state.Global...),
		"user":   append([]string(nil), state.User...),
	}, nil
}

func (m *WebMutator) ListSessionMCPs(sessionID, projectPath string) (web.SessionMCPsResponse, error) {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		raw, err := m.hubCommand(nodeID, "mcp_list", hub.MCPListRequest{SessionID: hubSessionID})
		if err != nil {
			return web.SessionMCPsResponse{}, err
		}
		var result hub.MCPListResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return web.SessionMCPsResponse{}, fmt.Errorf("decode hub mcp list response: %w", err)
		}
		return web.SessionMCPsResponse{
			SessionID: sessionID,
			Local:     appendSortedStringCopy(result.Local),
			Global:    appendSortedStringCopy(result.Global),
			User:      appendSortedStringCopy(result.User),
			Catalog:   webMCPCatalogFromHub(result.Catalog),
		}, nil
	}
	if provider, ok := web.NewDefaultMCPManager().(web.SessionMCPStateProvider); ok {
		return provider.ListSessionMCPs(sessionID, projectPath)
	}
	attached, err := web.NewDefaultMCPManager().ListAttached(sessionID, projectPath)
	if err != nil {
		return web.SessionMCPsResponse{}, err
	}
	return web.SessionMCPsResponse{
		SessionID: sessionID,
		Local:     appendSortedStringCopy(attached["local"]),
		Global:    appendSortedStringCopy(attached["global"]),
		User:      appendSortedStringCopy(attached["user"]),
		Catalog:   web.NewDefaultMCPManager().ListCatalog(),
	}, nil
}

func (m *WebMutator) Attach(sessionID, projectPath, name, scope string) error {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		_, err := m.hubCommand(nodeID, "mcp_attach", hub.MCPMutateRequest{
			SessionID: hubSessionID,
			Name:      strings.TrimSpace(name),
			Scope:     strings.TrimSpace(scope),
		})
		return err
	}
	return web.NewDefaultMCPManager().Attach(sessionID, projectPath, name, scope)
}

func (m *WebMutator) Detach(sessionID, projectPath, name, scope string) error {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		_, err := m.hubCommand(nodeID, "mcp_detach", hub.MCPMutateRequest{
			SessionID: hubSessionID,
			Name:      strings.TrimSpace(name),
			Scope:     strings.TrimSpace(scope),
		})
		return err
	}
	return web.NewDefaultMCPManager().Detach(sessionID, projectPath, name, scope)
}

func (m *WebMutator) Move(sessionID, projectPath, name, fromScope, toScope string) error {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		_, err := m.hubCommand(nodeID, "mcp_move", hub.MCPMoveRequest{
			SessionID: hubSessionID,
			Name:      strings.TrimSpace(name),
			FromScope: strings.TrimSpace(fromScope),
			ToScope:   strings.TrimSpace(toScope),
		})
		return err
	}
	return web.NewDefaultMCPManager().Move(sessionID, projectPath, name, fromScope, toScope)
}

func webMCPCatalogFromHub(entries []hub.MCPCatalogEntry) []web.MCPCatalogEntry {
	out := make([]web.MCPCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		out = append(out, web.MCPCatalogEntry{
			Name:        name,
			Description: entry.Description,
			Transport:   entry.Transport,
			Command:     entry.Command,
			URL:         entry.URL,
		})
	}
	return out
}

func (m *WebMutator) ListSkillCatalog() ([]session.SkillCandidate, error) {
	return session.ListAvailableSkills()
}

func (m *WebMutator) ListSessionSkills(sessionID, projectPath string) (web.SkillSessionState, error) {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		raw, err := m.hubCommand(nodeID, "skill_list", hub.SkillListRequest{SessionID: hubSessionID})
		if err != nil {
			return web.SkillSessionState{}, err
		}
		var result hub.SkillListResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return web.SkillSessionState{}, fmt.Errorf("decode hub skill list response: %w", err)
		}
		return web.SkillSessionState{
			Catalog:  append([]session.SkillCandidate(nil), result.Catalog...),
			Attached: append([]session.ProjectSkillAttachment(nil), result.Attached...),
		}, nil
	}
	catalog, err := session.ListAvailableSkills()
	if err != nil {
		return web.SkillSessionState{}, err
	}
	attached, err := session.GetAttachedProjectSkills(projectPath)
	if err != nil {
		return web.SkillSessionState{}, err
	}
	return web.SkillSessionState{Catalog: catalog, Attached: attached}, nil
}

func (m *WebMutator) AttachSkill(sessionID, projectPath, tool, skillRef, source string) (*session.ProjectSkillAttachment, error) {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		raw, err := m.hubCommand(nodeID, "skill_attach", hub.SkillMutateRequest{
			SessionID: hubSessionID,
			Name:      strings.TrimSpace(skillRef),
			Source:    strings.TrimSpace(source),
		})
		if err != nil {
			return nil, err
		}
		var result hub.SkillMutateResponse
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &result); err != nil {
				return nil, fmt.Errorf("decode hub skill attach response: %w", err)
			}
		}
		return result.Skill, nil
	}
	return session.AttachSkillToProject(projectPath, tool, skillRef, source)
}

func (m *WebMutator) DetachSkill(sessionID, projectPath, skillRef, source string) (*session.ProjectSkillAttachment, error) {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		raw, err := m.hubCommand(nodeID, "skill_detach", hub.SkillMutateRequest{
			SessionID: hubSessionID,
			Name:      strings.TrimSpace(skillRef),
			Source:    strings.TrimSpace(source),
		})
		if err != nil {
			return nil, err
		}
		var result hub.SkillMutateResponse
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &result); err != nil {
				return nil, fmt.Errorf("decode hub skill detach response: %w", err)
			}
		}
		return result.Skill, nil
	}
	return session.DetachSkillFromProject(projectPath, skillRef, source)
}

func (m *WebMutator) ListPluginCatalog() []web.PluginCatalogEntry {
	return web.NewDefaultPluginManager().ListPluginCatalog()
}

func (m *WebMutator) ListSessionPlugins(sessionID string, sess *web.MenuSession) (web.SessionPluginsResponse, error) {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		raw, err := m.hubCommand(nodeID, "plugin_list", hub.PluginListRequest{SessionID: hubSessionID})
		if err != nil {
			return web.SessionPluginsResponse{}, err
		}
		var result hub.PluginListResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return web.SessionPluginsResponse{}, fmt.Errorf("decode hub plugin list response: %w", err)
		}
		return web.SessionPluginsResponse{
			SessionID:                 sessionID,
			Catalog:                   webPluginCatalogFromHub(result.Catalog),
			Plugins:                   appendSortedStringCopy(result.Plugins),
			Channels:                  appendSortedStringCopy(result.Channels),
			PluginChannelLinkDisabled: result.PluginChannelLinkDisabled,
		}, nil
	}
	return web.NewDefaultPluginManager().ListSessionPlugins(sessionID, sess)
}

func (m *WebMutator) AttachPlugin(sessionID string, sess *web.MenuSession, name string, noChannelLink bool) (web.PluginMutateResponse, error) {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		raw, err := m.hubCommand(nodeID, "plugin_attach", hub.PluginMutateRequest{
			SessionID:     hubSessionID,
			Name:          strings.TrimSpace(name),
			NoChannelLink: noChannelLink,
		})
		if err != nil {
			return web.PluginMutateResponse{}, err
		}
		var result hub.PluginMutateResponse
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &result); err != nil {
				return web.PluginMutateResponse{}, fmt.Errorf("decode hub plugin attach response: %w", err)
			}
		}
		m.applyHubPluginState(nodeID, hubSessionID, result)
		m.publishHubWebSnapshot()
		return webPluginMutateResponse(sessionID, result), nil
	}
	return m.mutateLocalPlugin(sessionID, sess, name, "attach", noChannelLink)
}

func (m *WebMutator) DetachPlugin(sessionID string, sess *web.MenuSession, name string) (web.PluginMutateResponse, error) {
	if nodeID, hubSessionID, ok := web.ParseHubSessionWebID(sessionID); ok {
		raw, err := m.hubCommand(nodeID, "plugin_detach", hub.PluginMutateRequest{
			SessionID: hubSessionID,
			Name:      strings.TrimSpace(name),
		})
		if err != nil {
			return web.PluginMutateResponse{}, err
		}
		var result hub.PluginMutateResponse
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &result); err != nil {
				return web.PluginMutateResponse{}, fmt.Errorf("decode hub plugin detach response: %w", err)
			}
		}
		m.applyHubPluginState(nodeID, hubSessionID, result)
		m.publishHubWebSnapshot()
		return webPluginMutateResponse(sessionID, result), nil
	}
	return m.mutateLocalPlugin(sessionID, sess, name, "detach", false)
}

func (m *WebMutator) mutateLocalPlugin(sessionID string, _ *web.MenuSession, name, op string, noChannelLink bool) (web.PluginMutateResponse, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return web.PluginMutateResponse{}, fmt.Errorf("plugin name is required")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return web.PluginMutateResponse{}, err
	}
	defer unlock()

	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[sessionID]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return web.PluginMutateResponse{}, fmt.Errorf("session not found: %s", sessionID)
	}

	current := append([]string(nil), inst.Plugins...)
	updated := mutatePluginNames(current, name, op)
	pluginsUnchanged := stringSlicesEqual(current, updated)
	flagToggle := op == "attach" && noChannelLink && !inst.PluginChannelLinkDisabled
	if pluginsUnchanged && !flagToggle {
		return web.PluginMutateResponse{
			SessionID:                 sessionID,
			Plugins:                   appendSortedStringCopy(inst.Plugins),
			Channels:                  appendSortedStringCopy(inst.Channels),
			PluginChannelLinkDisabled: inst.PluginChannelLinkDisabled,
			RestartRequired:           false,
		}, nil
	}

	m.h.instancesMu.Lock()
	if flagToggle {
		inst.PluginChannelLinkDisabled = true
	}
	_, _, mutErr := session.SetField(inst, session.FieldPlugins, strings.Join(updated, ","), nil)
	if mutErr != nil {
		m.h.instancesMu.Unlock()
		return web.PluginMutateResponse{}, mutErr
	}
	resp := web.PluginMutateResponse{
		SessionID:                 sessionID,
		Plugins:                   appendSortedStringCopy(inst.Plugins),
		Channels:                  appendSortedStringCopy(inst.Channels),
		PluginChannelLinkDisabled: inst.PluginChannelLinkDisabled,
		RestartRequired:           true,
	}
	m.h.instancesMu.Unlock()

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return web.PluginMutateResponse{}, fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()
	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()
	if err := storage.SaveWithGroups(instances, m.h.groupTree); err != nil {
		return web.PluginMutateResponse{}, fmt.Errorf("save session: %w", err)
	}
	return resp, nil
}

func mutatePluginNames(current []string, name, op string) []string {
	name = strings.TrimSpace(name)
	seen := make(map[string]bool, len(current)+1)
	out := make([]string, 0, len(current)+1)
	for _, existing := range current {
		existing = strings.TrimSpace(existing)
		if existing == "" || seen[existing] {
			continue
		}
		seen[existing] = true
		if op == "detach" && existing == name {
			continue
		}
		out = append(out, existing)
	}
	if op == "attach" && name != "" && !seen[name] {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func webPluginCatalogFromHub(entries []hub.PluginCatalogEntry) []web.PluginCatalogEntry {
	out := make([]web.PluginCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		out = append(out, web.PluginCatalogEntry{
			Name:         name,
			PluginName:   entry.PluginName,
			Source:       entry.Source,
			EmitsChannel: entry.EmitsChannel,
			AutoInstall:  entry.AutoInstall,
		})
	}
	return out
}

func webPluginMutateResponse(sessionID string, result hub.PluginMutateResponse) web.PluginMutateResponse {
	return web.PluginMutateResponse{
		SessionID:                 sessionID,
		Plugins:                   appendSortedStringCopy(result.Plugins),
		Channels:                  appendSortedStringCopy(result.Channels),
		PluginChannelLinkDisabled: result.PluginChannelLinkDisabled,
		RestartRequired:           true,
	}
}

func (m *WebMutator) applyHubPluginState(nodeID, sessionID string, result hub.PluginMutateResponse) {
	changes := []hub.SessionFieldChange{
		{Field: session.FieldPlugins, Value: strings.Join(result.Plugins, ",")},
		{Field: session.FieldChannels, Value: strings.Join(result.Channels, ",")},
	}
	m.h.applyHubSessionFieldChanges(nodeID, sessionID, changes)
}

// CreateGroup creates a new group (or subgroup if parentPath is non-empty) and
// persists the group tree to storage.
func (m *WebMutator) CreateGroup(name, parentPath string) (string, error) {
	if nodeID, hubParentPath, ok := web.ParseHubGroupWebPath(parentPath); ok {
		raw, err := m.hubCommand(nodeID, "group_create", hub.GroupCreateRequest{
			Name:       strings.TrimSpace(name),
			ParentPath: hubParentPath,
		})
		if err != nil {
			return "", err
		}
		var result hub.GroupCreateResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", fmt.Errorf("decode hub group create response: %w", err)
		}
		m.h.addOrUpdateHubGroupInCache(nodeID, result.Path, result.Name, result.DefaultPath, result.MaxConcurrent)
		m.publishHubWebSnapshot()
		return web.HubGroupWebPath(nodeID, result.Path), nil
	}

	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", err
	}
	defer unlock()
	// Seed the new-group default from [group_defaults].max_concurrent.
	if cfg, _ := session.LoadUserConfig(); cfg != nil {
		m.h.groupTree.DefaultMaxConcurrent = cfg.GroupDefaults.MaxConcurrent
	}
	var grp *session.Group
	if parentPath != "" {
		grp = m.h.groupTree.CreateSubgroup(parentPath, name)
	} else {
		grp = m.h.groupTree.CreateGroup(name)
	}
	if grp == nil {
		return "", fmt.Errorf("failed to create group %q", name)
	}

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return "", fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	if err := storage.SaveWithGroups(instances, m.h.groupTree); err != nil {
		return "", fmt.Errorf("save group: %w", err)
	}
	return grp.Path, nil
}

// RenameGroup renames a group identified by groupPath to newName and persists.
func (m *WebMutator) RenameGroup(groupPath, newName string) error {
	if nodeID, hubGroupPath, ok := web.ParseHubGroupWebPath(groupPath); ok {
		raw, err := m.hubCommand(nodeID, "group_rename", hub.GroupRenameRequest{
			GroupPath: hubGroupPath,
			Name:      strings.TrimSpace(newName),
		})
		if err != nil {
			return err
		}
		var result hub.GroupRenameResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("decode hub group rename response: %w", err)
		}
		m.h.renameHubGroupInCache(nodeID, result.OldPath, result.Path, result.Name)
		m.publishHubWebSnapshot()
		return nil
	}

	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.h.groupTree.RenameGroup(groupPath, newName); err != nil {
		return err
	}

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	// SaveGroups is additive (never prunes), so the old path's rows must be
	// deleted explicitly before the save re-adds the renamed paths — otherwise
	// the group reappears under its old name on the next reload. Done before the
	// save so a no-op rename (same name) is correctly re-added.
	if err := storage.DeleteGroupSubtree(groupPath); err != nil {
		return fmt.Errorf("delete old group rows: %w", err)
	}

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	return storage.SaveWithGroups(instances, m.h.groupTree)
}

// ReparentGroup moves a group under a new parent and persists the group tree.
func (m *WebMutator) ReparentGroup(groupPath, destParentPath string) (string, error) {
	if nodeID, hubGroupPath, ok := web.ParseHubGroupWebPath(groupPath); ok {
		remoteDest := strings.Trim(strings.TrimSpace(destParentPath), "/")
		if destNodeID, destGroupPath, destOK := web.ParseHubGroupWebPath(destParentPath); destOK {
			if destNodeID != nodeID {
				return "", fmt.Errorf("cannot reparent hub group across nodes")
			}
			remoteDest = destGroupPath
		}
		raw, err := m.hubCommand(nodeID, "group_reparent", hub.GroupReparentRequest{
			GroupPath:      hubGroupPath,
			DestParentPath: remoteDest,
		})
		if err != nil {
			return "", err
		}
		var result hub.GroupReparentResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", fmt.Errorf("decode hub group reparent response: %w", err)
		}
		m.h.renameHubGroupInCache(nodeID, result.OldPath, result.Path, hubGroupLeafName(result.Path))
		m.publishHubWebSnapshot()
		return web.HubGroupWebPath(nodeID, result.Path), nil
	}
	if _, _, ok := web.ParseHubGroupWebPath(destParentPath); ok {
		return "", fmt.Errorf("cannot reparent local group under a hub group")
	}

	groupPath = strings.Trim(strings.TrimSpace(groupPath), "/")
	destParentPath = strings.Trim(strings.TrimSpace(destParentPath), "/")
	if strings.EqualFold(destParentPath, "root") {
		destParentPath = ""
	}
	if groupPath == "" {
		return "", fmt.Errorf("group path is required")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := m.h.groupTree.MoveGroupTo(groupPath, destParentPath); err != nil {
		return "", err
	}
	newPath := reparentedWebGroupPath(groupPath, destParentPath)

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return "", fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	if err := storage.SaveWithGroups(instances, m.h.groupTree); err != nil {
		return "", err
	}
	return newPath, nil
}

// ReorderGroup moves a group among siblings and persists the group tree.
func (m *WebMutator) ReorderGroup(groupPath, direction string, position *int) (int, int, error) {
	if nodeID, hubGroupPath, ok := web.ParseHubGroupWebPath(groupPath); ok {
		raw, err := m.hubCommand(nodeID, "group_reorder", hub.GroupReorderRequest{
			GroupPath: hubGroupPath,
			Direction: strings.TrimSpace(direction),
			Position:  position,
		})
		if err != nil {
			return 0, 0, err
		}
		var result hub.GroupReorderResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return 0, 0, fmt.Errorf("decode hub group reorder response: %w", err)
		}
		m.h.reorderHubGroupInCache(nodeID, result.Path, direction, position)
		m.publishHubWebSnapshot()
		return result.FromPosition, result.ToPosition, nil
	}

	groupPath = strings.Trim(strings.TrimSpace(groupPath), "/")
	if groupPath == "" {
		return 0, 0, fmt.Errorf("group path is required")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return 0, 0, err
	}
	defer unlock()
	from, siblings := webGroupSiblingPosition(m.h.groupTree, groupPath)
	if from < 0 {
		return 0, 0, fmt.Errorf("group %q not found", groupPath)
	}
	if position != nil {
		target := *position
		if target < 0 {
			target = 0
		}
		if target >= len(siblings) {
			target = len(siblings) - 1
		}
		for {
			current, _ := webGroupSiblingPosition(m.h.groupTree, groupPath)
			if current == target {
				break
			}
			if current > target {
				m.h.groupTree.MoveGroupUp(groupPath)
			} else {
				m.h.groupTree.MoveGroupDown(groupPath)
			}
			next, _ := webGroupSiblingPosition(m.h.groupTree, groupPath)
			if next == current {
				break
			}
		}
	} else {
		switch strings.ToLower(strings.TrimSpace(direction)) {
		case "up":
			m.h.groupTree.MoveGroupUp(groupPath)
		case "down":
			m.h.groupTree.MoveGroupDown(groupPath)
		default:
			return 0, 0, fmt.Errorf("group reorder direction must be up or down")
		}
	}
	to, _ := webGroupSiblingPosition(m.h.groupTree, groupPath)

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return 0, 0, fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	if err := storage.SaveWithGroups(instances, m.h.groupTree); err != nil {
		return 0, 0, err
	}
	return from, to, nil
}

// FinishWorktree merges (or skips), removes the worktree, optionally
// deletes the source branch, kills the tmux session, and removes the
// session from storage. Mirrors `agent-deck worktree finish` (see
// cmd/agent-deck/worktree_cmd.go handleWorktreeFinish) — the
// orchestration is duplicated rather than refactored to keep the
// fix minimally invasive (issue #1126).
func (m *WebMutator) FinishWorktree(id string, opts web.WorktreeFinishOptions) (web.WorktreeFinishResult, error) {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		raw, err := m.hubCommand(nodeID, "worktree_finish", hub.WorktreeFinishRequest{
			SessionID:  sessionID,
			Into:       strings.TrimSpace(opts.Into),
			NoMerge:    opts.NoMerge,
			KeepBranch: opts.KeepBranch,
			Force:      opts.Force,
		})
		if err != nil {
			return web.WorktreeFinishResult{}, err
		}
		var result hub.WorktreeFinishResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return web.WorktreeFinishResult{}, fmt.Errorf("decode hub worktree finish response: %w", err)
		}
		m.h.removeHubSessionFromCache(nodeID, sessionID)
		m.publishHubWebSnapshot()
		return web.WorktreeFinishResult{
			SessionID:     web.HubSessionWebID(nodeID, result.SessionID),
			Branch:        result.Branch,
			MergedInto:    result.MergedInto,
			Merged:        result.Merged,
			BranchDeleted: result.BranchDeleted,
		}, nil
	}

	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return web.WorktreeFinishResult{}, err
	}
	defer unlock()
	m.h.instancesMu.RLock()
	inst := m.h.instanceByID[id]
	m.h.instancesMu.RUnlock()
	if inst == nil {
		return web.WorktreeFinishResult{}, web.ErrSessionNotFound
	}
	if !inst.IsWorktree() {
		return web.WorktreeFinishResult{}, web.ErrNotAWorktree
	}

	repoRoot := inst.WorktreeRepoRoot
	worktreePath := inst.WorktreePath
	worktreeBranch := inst.WorktreeBranch

	backend, err := vcsbackend.Detect(repoRoot)
	if err != nil {
		return web.WorktreeFinishResult{}, fmt.Errorf("initialize VCS: %w", err)
	}

	if !opts.Force {
		dirty, dErr := git.HasUncommittedChanges(worktreePath)
		if dErr != nil {
			if _, statErr := os.Stat(worktreePath); os.IsNotExist(statErr) {
				dirty = false
			} else {
				return web.WorktreeFinishResult{}, fmt.Errorf("check worktree status: %w", dErr)
			}
		}
		if dirty {
			return web.WorktreeFinishResult{}, fmt.Errorf("worktree has uncommitted changes (set force=true to override)")
		}
	}

	targetBranch := opts.Into
	if targetBranch == "" && !opts.NoMerge {
		targetBranch, err = backend.GetDefaultBranch()
		if err != nil {
			return web.WorktreeFinishResult{}, fmt.Errorf("determine target branch: %w (set into=<branch>)", err)
		}
	}
	if !opts.NoMerge && targetBranch == worktreeBranch {
		return web.WorktreeFinishResult{}, fmt.Errorf("cannot merge branch %q into itself", worktreeBranch)
	}

	if !opts.NoMerge {
		// Checkout target in main repo, then merge.
		checkout := exec.Command("git", "-C", repoRoot, "checkout", targetBranch)
		if out, cErr := checkout.CombinedOutput(); cErr != nil {
			return web.WorktreeFinishResult{}, fmt.Errorf("checkout %s: %s", targetBranch, strings.TrimSpace(string(out)))
		}
		if mErr := backend.MergeBranch(worktreeBranch); mErr != nil {
			if backend.Type() == vcs.TypeGit {
				_ = exec.Command("git", "-C", repoRoot, "merge", "--abort").Run()
			}
			return web.WorktreeFinishResult{}, fmt.Errorf("merge failed (aborted): %w", mErr)
		}
	}

	if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
		// Best-effort: log via error wrapping only if it bubbles. CLI
		// treats this as a warning; we mirror that by swallowing here so
		// the rest of cleanup proceeds.
		_ = backend.RemoveWorktree(worktreePath, opts.Force)
	}
	_ = backend.PruneWorktrees()

	branchDeleted := false
	if !opts.KeepBranch {
		if dErr := backend.DeleteBranch(worktreeBranch, opts.Force); dErr == nil {
			branchDeleted = true
		}
	}

	if inst.Exists() {
		_ = inst.Kill()
	}

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return web.WorktreeFinishResult{}, fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	existing := make([]*session.Instance, 0, len(m.h.instances))
	for _, x := range m.h.instances {
		if x.ID != id {
			existing = append(existing, x)
		}
	}
	m.h.instancesMu.RUnlock()
	// #1396: use the targeted RemoveSessionAndVerify path, NOT
	// SaveWithGroups(existing, ...). Historically an empty `existing` tripped
	// the S1 empty-sweep guard AFTER the irreversible git steps, orphaning the
	// row; since #1550 SaveWithGroups is upsert-only and would not delete the
	// row at all. Either way, removal requires the targeted DELETE.
	if sErr := storage.RemoveSessionAndVerify(id, existing, m.h.groupTree); sErr != nil {
		return web.WorktreeFinishResult{}, fmt.Errorf("save session data: %w", sErr)
	}

	// Issue #1576: sweep transition-notifier state (inbox JSONL lines +
	// runtime/transition-notify-state.json dedup record) for the removed
	// session, mirroring the #910 cleanup on `agent-deck rm`. Best-effort —
	// never fails the finish.
	_, _ = session.SweepInboxesForChildSession(id)
	_, _ = session.RemoveNotifyStateRecord(id)

	mergedInto := targetBranch
	if opts.NoMerge {
		mergedInto = ""
	}
	return web.WorktreeFinishResult{
		SessionID:     id,
		Branch:        worktreeBranch,
		MergedInto:    mergedInto,
		Merged:        !opts.NoMerge,
		BranchDeleted: branchDeleted,
	}, nil
}

// DeleteGroup deletes a group (and its subgroups), moving sessions to the default
// group. Returns an error if groupPath is the default group.
func (m *WebMutator) DeleteGroup(groupPath string) error {
	if nodeID, hubGroupPath, ok := web.ParseHubGroupWebPath(groupPath); ok {
		if hubGroupPath == session.DefaultGroupPath {
			return fmt.Errorf("cannot delete default group")
		}
		raw, err := m.hubCommand(nodeID, "group_delete", hub.GroupDeleteRequest{GroupPath: hubGroupPath, Force: true})
		if err != nil {
			return err
		}
		var result hub.GroupDeleteResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("decode hub group delete response: %w", err)
		}
		m.h.deleteHubGroupFromCache(nodeID, result.Path)
		m.publishHubWebSnapshot()
		return nil
	}

	if groupPath == session.DefaultGroupPath {
		return fmt.Errorf("cannot delete default group")
	}
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()

	m.h.groupTree.DeleteGroup(groupPath)

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	// SaveGroups is additive (never prunes), so the deleted group's rows must be
	// removed explicitly or the group resurrects on the next reload.
	if err := storage.DeleteGroupSubtree(groupPath); err != nil {
		return fmt.Errorf("delete group rows: %w", err)
	}

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	return storage.SaveWithGroups(instances, m.h.groupTree)
}

func reparentedWebGroupPath(sourcePath, destParentPath string) string {
	sourcePath = strings.Trim(strings.TrimSpace(sourcePath), "/")
	destParentPath = strings.Trim(strings.TrimSpace(destParentPath), "/")
	baseName := sourcePath
	if idx := strings.LastIndex(sourcePath, "/"); idx >= 0 {
		baseName = sourcePath[idx+1:]
	}
	if destParentPath == "" {
		return baseName
	}
	return destParentPath + "/" + baseName
}

func webGroupSiblingPosition(groupTree *session.GroupTree, groupPath string) (int, []string) {
	if groupTree == nil {
		return -1, nil
	}
	groupPath = strings.Trim(strings.TrimSpace(groupPath), "/")
	parent := hubParentGroupPath(groupPath)
	level := session.GetGroupLevel(groupPath)
	siblings := make([]string, 0)
	for _, group := range groupTree.GroupList {
		if group == nil {
			continue
		}
		if hubParentGroupPath(group.Path) == parent && session.GetGroupLevel(group.Path) == level {
			siblings = append(siblings, group.Path)
		}
	}
	for i, sibling := range siblings {
		if sibling == groupPath {
			return i, siblings
		}
	}
	return -1, siblings
}
