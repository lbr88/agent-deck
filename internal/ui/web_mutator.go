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
	instance  *session.Instance
	deletedAt time.Time
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
func (m *WebMutator) CreateSession(title, tool, projectPath, groupPath, modelID string) (string, error) {
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return "", err
	}
	defer unlock()
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
	return inst.RestartFresh()
}

// DeleteSession kills a session and removes it from persistent storage.
// Before removal, the instance is pushed onto the web undo stack so a
// subsequent UndoDelete (POST /api/sessions/undelete) can restore it.
func (m *WebMutator) DeleteSession(id string) error {
	if nodeID, sessionID, ok := web.ParseHubSessionWebID(id); ok {
		if err := m.hubSessionAction(nodeID, sessionID, "delete"); err != nil {
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
		if oldValue == value {
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
	return m.persistAllInstances()
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

// CreateGroup creates a new group (or subgroup if parentPath is non-empty) and
// persists the group tree to storage.
func (m *WebMutator) CreateGroup(name, parentPath string) (string, error) {
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
	unlock, err := m.beginHeadlessTx()
	if err != nil {
		return err
	}
	defer unlock()
	m.h.groupTree.RenameGroup(groupPath, newName)

	storage, err := session.NewStorageWithProfile(m.h.profile)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	return storage.SaveWithGroups(instances, m.h.groupTree)
}

// FinishWorktree merges (or skips), removes the worktree, optionally
// deletes the source branch, kills the tmux session, and removes the
// session from storage. Mirrors `agent-deck worktree finish` (see
// cmd/agent-deck/worktree_cmd.go handleWorktreeFinish) — the
// orchestration is duplicated rather than refactored to keep the
// fix minimally invasive (issue #1126).
func (m *WebMutator) FinishWorktree(id string, opts web.WorktreeFinishOptions) (web.WorktreeFinishResult, error) {
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
	// SaveWithGroups(existing, ...). When the finished worktree is the LAST
	// session, `existing` is empty and SaveWithGroups → SaveInstances([]) trips
	// the S1 empty-sweep data-loss guard AFTER the irreversible git steps,
	// orphaning the row. The targeted DELETE + SaveGroupsOnly path persists the
	// last-session removal without ever calling SaveInstances([]).
	if sErr := storage.RemoveSessionAndVerify(id, existing, m.h.groupTree); sErr != nil {
		return web.WorktreeFinishResult{}, fmt.Errorf("save session data: %w", sErr)
	}

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

	m.h.instancesMu.RLock()
	instances := make([]*session.Instance, len(m.h.instances))
	copy(instances, m.h.instances)
	m.h.instancesMu.RUnlock()

	return storage.SaveWithGroups(instances, m.h.groupTree)
}
