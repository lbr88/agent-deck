package hub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/agentpaths"
	"github.com/asheshgoplani/agent-deck/internal/git"
	"github.com/asheshgoplani/agent-deck/internal/jujutsu"
	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
	"github.com/asheshgoplani/agent-deck/internal/vcs"
	"github.com/asheshgoplani/agent-deck/internal/vcsbackend"
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
	UndoDelete(ctx context.Context) (string, error)
	Archive(ctx context.Context, sessionID string) error
	Unarchive(ctx context.Context, sessionID string) error
	Remove(ctx context.Context, sessionID string) error
	Move(ctx context.Context, sessionID, groupPath string) error
	Update(ctx context.Context, req UpdateSessionRequest) (UpdateSessionResponse, error)
	UpdatePaths(ctx context.Context, req UpdateSessionPathsRequest) (UpdateSessionPathsResponse, error)
	SetupWorktree(ctx context.Context, req WorktreeSetupRequest) (WorktreeSetupResponse, error)
	FinishWorktree(ctx context.Context, req WorktreeFinishRequest) (WorktreeFinishResponse, error)
	OpenSandboxShell(ctx context.Context, req SandboxShellRequest) (SandboxShellResponse, error)
	CreateGroup(ctx context.Context, req GroupCreateRequest) (GroupCreateResponse, error)
	RenameGroup(ctx context.Context, req GroupRenameRequest) (GroupRenameResponse, error)
	UpdateGroup(ctx context.Context, req GroupUpdateRequest) (GroupUpdateResponse, error)
	DeleteGroup(ctx context.Context, req GroupDeleteRequest) (GroupDeleteResponse, error)
	ReparentGroup(ctx context.Context, req GroupReparentRequest) (GroupReparentResponse, error)
	ReorderGroup(ctx context.Context, req GroupReorderRequest) (GroupReorderResponse, error)
	ListMCPs(ctx context.Context, req MCPListRequest) (MCPListResponse, error)
	AttachMCP(ctx context.Context, req MCPMutateRequest) (MCPMutateResponse, error)
	DetachMCP(ctx context.Context, req MCPMutateRequest) (MCPMutateResponse, error)
	MoveMCP(ctx context.Context, req MCPMoveRequest) (MCPMoveResponse, error)
	ListSkills(ctx context.Context, req SkillListRequest) (SkillListResponse, error)
	AttachSkill(ctx context.Context, req SkillMutateRequest) (SkillMutateResponse, error)
	DetachSkill(ctx context.Context, req SkillMutateRequest) (SkillMutateResponse, error)
	ListPlugins(ctx context.Context, req PluginListRequest) (PluginListResponse, error)
	AttachPlugin(ctx context.Context, req PluginMutateRequest) (PluginMutateResponse, error)
	DetachPlugin(ctx context.Context, req PluginMutateRequest) (PluginMutateResponse, error)
	ToggleYolo(ctx context.Context, sessionID string) error
	Acknowledge(ctx context.Context, sessionID string) error
	MarkUnread(ctx context.Context, sessionID string) error
	Preview(ctx context.Context, sessionID string) (string, error)
	ImportTmux(ctx context.Context) (int, error)
}

const MaxWebProxyBodyBytes = 8 * 1024 * 1024

type WebProxyBackend interface {
	ProxyWeb(ctx context.Context, req WebProxyRequest) (WebProxyResponse, error)
}

type ForkOptionsBackend interface {
	ForkWithOptions(ctx context.Context, req ForkSessionRequest) (string, error)
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
	Title           string   `json:"title"`
	Tool            string   `json:"tool,omitempty"`
	ProjectPath     string   `json:"project_path,omitempty"`
	AdditionalPaths []string `json:"additional_paths,omitempty"`
	GroupPath       string   `json:"group_path,omitempty"`
	ModelID         string   `json:"model_id,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
}

type ForkSessionRequest struct {
	SessionID    string `json:"session_id"`
	Title        string `json:"title,omitempty"`
	GroupPath    string `json:"group_path,omitempty"`
	Worktree     bool   `json:"worktree,omitempty"`
	Branch       string `json:"branch,omitempty"`
	WithState    bool   `json:"with_state,omitempty"`
	WithIgnored  bool   `json:"with_ignored,omitempty"`
	Sandbox      bool   `json:"sandbox,omitempty"`
	SandboxImage string `json:"sandbox_image,omitempty"`
}

type WorktreeFinishRequest struct {
	SessionID  string `json:"session_id"`
	Into       string `json:"into,omitempty"`
	NoMerge    bool   `json:"no_merge,omitempty"`
	KeepBranch bool   `json:"keep_branch,omitempty"`
	Force      bool   `json:"force,omitempty"`
}

type WorktreeSetupRequest struct {
	SessionID string `json:"session_id"`
}

type WorktreeSetupResponse struct {
	SessionID string `json:"session_id"`
}

type WorktreeFinishResponse struct {
	SessionID     string `json:"session_id"`
	Branch        string `json:"branch,omitempty"`
	MergedInto    string `json:"merged_into,omitempty"`
	Merged        bool   `json:"merged"`
	BranchDeleted bool   `json:"branch_deleted"`
}

type SandboxShellRequest struct {
	SessionID string `json:"session_id"`
}

type SandboxShellResponse struct {
	SessionID       string `json:"session_id"`
	AttachSessionID string `json:"attach_session_id"`
}

type GroupCreateRequest struct {
	Name          string `json:"name"`
	ParentPath    string `json:"parent_path,omitempty"`
	DefaultPath   string `json:"default_path,omitempty"`
	MaxConcurrent *int   `json:"max_concurrent,omitempty"`
}

type GroupCreateResponse struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	DefaultPath   string `json:"default_path,omitempty"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"`
	Existed       bool   `json:"existed,omitempty"`
}

type GroupRenameRequest struct {
	GroupPath string `json:"group_path"`
	Name      string `json:"name"`
}

type GroupRenameResponse struct {
	OldPath string `json:"old_path"`
	Path    string `json:"path"`
	Name    string `json:"name"`
}

type GroupUpdateRequest struct {
	GroupPath        string  `json:"group_path"`
	DefaultPath      *string `json:"default_path,omitempty"`
	ClearDefaultPath bool    `json:"clear_default_path,omitempty"`
	MaxConcurrent    *int    `json:"max_concurrent,omitempty"`
}

type GroupUpdateResponse struct {
	Path          string `json:"path"`
	DefaultPath   string `json:"default_path,omitempty"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"`
}

type GroupDeleteRequest struct {
	GroupPath string `json:"group_path"`
	Force     bool   `json:"force,omitempty"`
}

type GroupDeleteResponse struct {
	Path          string `json:"path"`
	SessionsMoved int    `json:"sessions_moved"`
	MovedTo       string `json:"moved_to"`
}

type GroupReparentRequest struct {
	GroupPath      string `json:"group_path"`
	DestParentPath string `json:"dest_parent_path,omitempty"`
}

type GroupReparentResponse struct {
	OldPath        string `json:"old_path"`
	Path           string `json:"path"`
	DestParentPath string `json:"dest_parent_path,omitempty"`
}

type GroupReorderRequest struct {
	GroupPath string `json:"group_path"`
	Direction string `json:"direction,omitempty"`
	Position  *int   `json:"position,omitempty"`
}

type GroupReorderResponse struct {
	Path         string `json:"path"`
	FromPosition int    `json:"from_position"`
	ToPosition   int    `json:"to_position"`
}

type MCPListRequest struct {
	SessionID string `json:"session_id"`
}

type MCPListResponse struct {
	SessionID string            `json:"session_id"`
	Local     []string          `json:"local"`
	Global    []string          `json:"global"`
	User      []string          `json:"user"`
	Catalog   []MCPCatalogEntry `json:"catalog,omitempty"`
}

type MCPCatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Transport   string `json:"transport,omitempty"`
	Command     string `json:"command,omitempty"`
	URL         string `json:"url,omitempty"`
}

type MCPMutateRequest struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Scope     string `json:"scope,omitempty"`
}

type MCPMutateResponse struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
}

type MCPMoveRequest struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	FromScope string `json:"from_scope"`
	ToScope   string `json:"to_scope"`
}

type MCPMoveResponse struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	FromScope string `json:"from_scope"`
	ToScope   string `json:"to_scope"`
}

type SkillListRequest struct {
	SessionID string `json:"session_id"`
}

type SkillListResponse struct {
	SessionID string                           `json:"session_id"`
	Catalog   []session.SkillCandidate         `json:"catalog"`
	Attached  []session.ProjectSkillAttachment `json:"attached"`
}

type SkillMutateRequest struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Source    string `json:"source,omitempty"`
}

type SkillMutateResponse struct {
	SessionID string                          `json:"session_id"`
	Skill     *session.ProjectSkillAttachment `json:"skill,omitempty"`
}

type PluginCatalogEntry struct {
	Name         string `json:"name"`
	PluginName   string `json:"plugin_name,omitempty"`
	Source       string `json:"source,omitempty"`
	ID           string `json:"id"`
	Description  string `json:"description,omitempty"`
	EmitsChannel bool   `json:"emits_channel,omitempty"`
	AutoInstall  bool   `json:"auto_install,omitempty"`
}

type PluginListRequest struct {
	SessionID string `json:"session_id"`
}

type PluginListResponse struct {
	SessionID                 string               `json:"session_id"`
	Catalog                   []PluginCatalogEntry `json:"catalog"`
	Plugins                   []string             `json:"plugins"`
	Channels                  []string             `json:"channels,omitempty"`
	PluginChannelLinkDisabled bool                 `json:"plugin_channel_link_disabled,omitempty"`
}

type PluginMutateRequest struct {
	SessionID     string `json:"session_id"`
	Name          string `json:"name"`
	NoChannelLink bool   `json:"no_channel_link,omitempty"`
}

type PluginMutateResponse struct {
	SessionID                 string   `json:"session_id"`
	Plugins                   []string `json:"plugins"`
	Channels                  []string `json:"channels,omitempty"`
	PluginChannelLinkDisabled bool     `json:"plugin_channel_link_disabled,omitempty"`
}

func (r ForkSessionRequest) HasOptions() bool {
	return strings.TrimSpace(r.Title) != "" ||
		strings.TrimSpace(r.GroupPath) != "" ||
		r.Worktree ||
		strings.TrimSpace(r.Branch) != "" ||
		r.WithState ||
		r.WithIgnored ||
		r.Sandbox ||
		strings.TrimSpace(r.SandboxImage) != ""
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
		var payload ForkSessionRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("fork action session_id is required")
		}
		var sessionID string
		var err error
		if payload.HasOptions() {
			forker, ok := d.Backend.(ForkOptionsBackend)
			if !ok {
				return nil, fmt.Errorf("hub fork options backend is not configured")
			}
			sessionID, err = forker.ForkWithOptions(ctx, payload)
		} else {
			sessionID, err = d.Backend.Fork(ctx, payload.SessionID)
		}
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
	case "undo_delete":
		sessionID, err := d.Backend.UndoDelete(ctx)
		if err != nil {
			return nil, err
		}
		return marshalActionResult(actionResult{SessionID: sessionID})
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
	case "worktree_setup":
		var payload WorktreeSetupRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("worktree_setup action session_id is required")
		}
		result, err := d.Backend.SetupWorktree(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "worktree_finish":
		var payload WorktreeFinishRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("worktree_finish action session_id is required")
		}
		result, err := d.Backend.FinishWorktree(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "sandbox_shell":
		var payload SandboxShellRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("sandbox_shell action session_id is required")
		}
		result, err := d.Backend.OpenSandboxShell(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "group_create":
		var payload GroupCreateRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.Name) == "" {
			return nil, fmt.Errorf("group_create action name is required")
		}
		result, err := d.Backend.CreateGroup(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "group_rename":
		var payload GroupRenameRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.GroupPath) == "" {
			return nil, fmt.Errorf("group_rename action group_path is required")
		}
		if strings.TrimSpace(payload.Name) == "" {
			return nil, fmt.Errorf("group_rename action name is required")
		}
		result, err := d.Backend.RenameGroup(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "group_update":
		var payload GroupUpdateRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.GroupPath) == "" {
			return nil, fmt.Errorf("group_update action group_path is required")
		}
		if payload.DefaultPath == nil && !payload.ClearDefaultPath && payload.MaxConcurrent == nil {
			return nil, fmt.Errorf("group_update action requires a setting change")
		}
		result, err := d.Backend.UpdateGroup(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "group_delete":
		var payload GroupDeleteRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.GroupPath) == "" {
			return nil, fmt.Errorf("group_delete action group_path is required")
		}
		result, err := d.Backend.DeleteGroup(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "group_reparent":
		var payload GroupReparentRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.GroupPath) == "" {
			return nil, fmt.Errorf("group_reparent action group_path is required")
		}
		result, err := d.Backend.ReparentGroup(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "group_reorder":
		var payload GroupReorderRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.GroupPath) == "" {
			return nil, fmt.Errorf("group_reorder action group_path is required")
		}
		if strings.TrimSpace(payload.Direction) == "" && payload.Position == nil {
			return nil, fmt.Errorf("group_reorder action requires direction or position")
		}
		result, err := d.Backend.ReorderGroup(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "mcp_list":
		var payload MCPListRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("mcp_list action session_id is required")
		}
		result, err := d.Backend.ListMCPs(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "mcp_attach":
		var payload MCPMutateRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.Name) == "" {
			return nil, fmt.Errorf("mcp_attach action session_id and name are required")
		}
		result, err := d.Backend.AttachMCP(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "mcp_detach":
		var payload MCPMutateRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.Name) == "" {
			return nil, fmt.Errorf("mcp_detach action session_id and name are required")
		}
		result, err := d.Backend.DetachMCP(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "mcp_move":
		var payload MCPMoveRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.Name) == "" ||
			strings.TrimSpace(payload.FromScope) == "" || strings.TrimSpace(payload.ToScope) == "" {
			return nil, fmt.Errorf("mcp_move action session_id, name, from_scope, and to_scope are required")
		}
		result, err := d.Backend.MoveMCP(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "skill_list":
		var payload SkillListRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("skill_list action session_id is required")
		}
		result, err := d.Backend.ListSkills(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "skill_attach":
		var payload SkillMutateRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.Name) == "" {
			return nil, fmt.Errorf("skill_attach action session_id and name are required")
		}
		result, err := d.Backend.AttachSkill(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "skill_detach":
		var payload SkillMutateRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.Name) == "" {
			return nil, fmt.Errorf("skill_detach action session_id and name are required")
		}
		result, err := d.Backend.DetachSkill(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "plugin_list":
		var payload PluginListRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("plugin_list action session_id is required")
		}
		result, err := d.Backend.ListPlugins(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "plugin_attach":
		var payload PluginMutateRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.Name) == "" {
			return nil, fmt.Errorf("plugin_attach action session_id and name are required")
		}
		result, err := d.Backend.AttachPlugin(ctx, payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "plugin_detach":
		var payload PluginMutateRequest
		if err := decodeCommandPayload(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.Name) == "" {
			return nil, fmt.Errorf("plugin_detach action session_id and name are required")
		}
		result, err := d.Backend.DetachPlugin(ctx, payload)
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
	case "acknowledge":
		payload, err := decodeSessionAction(cmd.Payload, "acknowledge")
		if err != nil {
			return nil, err
		}
		if err := d.Backend.Acknowledge(ctx, payload.SessionID); err != nil {
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

var (
	// ErrHubUndoNothing means the owner node has no deleted hub session to restore.
	ErrHubUndoNothing = errors.New("hub undo delete: nothing to undo")
	// ErrHubUndoExpired means the most recent deleted hub session exceeded the undo window.
	ErrHubUndoExpired = errors.New("hub undo delete: undo window expired")
)

const hubDeleteUndoWindow = 30 * time.Second

type hubDeletedSessionEntry struct {
	instance  *session.Instance
	deletedAt time.Time
}

type hubDeleteUndoStack struct {
	mu      sync.Mutex
	entries []hubDeletedSessionEntry
}

var hubDeleteUndoStacks sync.Map // profile name -> *hubDeleteUndoStack

func hubUndoProfileKey(profile string) string {
	return session.GetEffectiveProfile(profile)
}

func hubUndoStackForProfile(profile string) *hubDeleteUndoStack {
	key := hubUndoProfileKey(profile)
	stack, _ := hubDeleteUndoStacks.LoadOrStore(key, &hubDeleteUndoStack{})
	return stack.(*hubDeleteUndoStack)
}

func cloneSessionForHubUndo(inst *session.Instance) *session.Instance {
	if inst == nil {
		return nil
	}
	clone := &session.Instance{
		ID:                        inst.ID,
		Title:                     inst.Title,
		ProjectPath:               inst.ProjectPath,
		GroupPath:                 inst.GroupPath,
		Order:                     inst.Order,
		Pin:                       inst.Pin,
		ParentSessionID:           inst.ParentSessionID,
		ParentProjectPath:         inst.ParentProjectPath,
		IsConductor:               inst.IsConductor,
		NoTransitionNotify:        inst.NoTransitionNotify,
		TitleLocked:               inst.TitleLocked,
		AutoName:                  inst.GetAutoName(),
		WorktreePath:              inst.WorktreePath,
		WorktreeRepoRoot:          inst.WorktreeRepoRoot,
		WorktreeBranch:            inst.WorktreeBranch,
		WorktreeType:              inst.WorktreeType,
		Account:                   inst.Account,
		MultiRepoEnabled:          inst.MultiRepoEnabled,
		AdditionalPaths:           append([]string(nil), inst.AdditionalPaths...),
		MultiRepoTempDir:          inst.MultiRepoTempDir,
		MultiRepoWorktrees:        append([]session.MultiRepoWorktree(nil), inst.MultiRepoWorktrees...),
		Command:                   inst.Command,
		Wrapper:                   inst.Wrapper,
		Tool:                      inst.Tool,
		Status:                    inst.Status,
		CreatedAt:                 inst.CreatedAt,
		LastAccessedAt:            inst.LastAccessedAt,
		ArchivedAt:                inst.ArchivedAt,
		LastStartedAt:             inst.LastStartedAt,
		ClaudeSessionID:           inst.ClaudeSessionID,
		ClaudeDetectedAt:          inst.ClaudeDetectedAt,
		GeminiSessionID:           inst.GeminiSessionID,
		GeminiDetectedAt:          inst.GeminiDetectedAt,
		GeminiYoloMode:            cloneBoolPtr(inst.GeminiYoloMode),
		GeminiModel:               inst.GeminiModel,
		GeminiAnalytics:           cloneGeminiAnalytics(inst.GeminiAnalytics),
		OpenCodeSessionID:         inst.OpenCodeSessionID,
		OpenCodeDetectedAt:        inst.OpenCodeDetectedAt,
		OpenCodeStartedAt:         inst.OpenCodeStartedAt,
		CodexSessionID:            inst.CodexSessionID,
		CodexDetectedAt:           inst.CodexDetectedAt,
		CodexStartedAt:            inst.CodexStartedAt,
		KiroSessionID:             inst.KiroSessionID,
		KiroDetectedAt:            inst.KiroDetectedAt,
		KiroStartedAt:             inst.KiroStartedAt,
		CopilotSessionID:          inst.CopilotSessionID,
		CopilotDetectedAt:         inst.CopilotDetectedAt,
		CopilotStartedAt:          inst.CopilotStartedAt,
		CopilotModel:              inst.CopilotModel,
		CopilotAllowAll:           inst.CopilotAllowAll,
		LatestPrompt:              inst.LatestPrompt,
		Notes:                     inst.Notes,
		Color:                     inst.Color,
		Sandbox:                   cloneSandboxConfig(inst.Sandbox),
		SandboxContainer:          inst.SandboxContainer,
		SSHHost:                   inst.SSHHost,
		SSHRemotePath:             inst.SSHRemotePath,
		TmuxSocketName:            inst.TmuxSocketName,
		LoadedMCPNames:            append([]string(nil), inst.LoadedMCPNames...),
		TrackedMCPPIDs:            append([]int(nil), inst.TrackedMCPPIDs...),
		Channels:                  append([]string(nil), inst.Channels...),
		Plugins:                   append([]string(nil), inst.Plugins...),
		InheritTelegramEnv:        inst.InheritTelegramEnv,
		PluginChannelLinkDisabled: inst.PluginChannelLinkDisabled,
		AutoLinkedChannels:        append([]string(nil), inst.AutoLinkedChannels...),
		WorkerScratchConfigDir:    inst.WorkerScratchConfigDir,
		IdleTimeoutSecs:           inst.IdleTimeoutSecs,
		IsForkAwaitingStart:       inst.IsForkAwaitingStart,
		ForkStartCommand:          inst.ForkStartCommand,
		ExtraArgs:                 append([]string(nil), inst.ExtraArgs...),
		ExitToShell:               cloneBoolPtr(inst.ExitToShell),
		LaunchShell:               cloneBoolPtr(inst.LaunchShell),
		StartupQuery:              inst.StartupQuery,
		ToolOptionsJSON:           append(json.RawMessage(nil), inst.ToolOptionsJSON...),
		SkipMCPRegenerate:         inst.SkipMCPRegenerate,
	}
	if desc := inst.GetAutoNameDescription(); desc != "" {
		clone.SetAutoNameDescription(desc)
	}
	return clone
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGeminiAnalytics(value *session.GeminiSessionAnalytics) *session.GeminiSessionAnalytics {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSandboxConfig(value *session.SandboxConfig) *session.SandboxConfig {
	if value == nil {
		return nil
	}
	clone := *value
	clone.CPULimit = cloneStringPtr(value.CPULimit)
	clone.MemoryLimit = cloneStringPtr(value.MemoryLimit)
	if value.ExtraVolumes != nil {
		clone.ExtraVolumes = make(map[string]string, len(value.ExtraVolumes))
		for hostPath, containerPath := range value.ExtraVolumes {
			clone.ExtraVolumes[hostPath] = containerPath
		}
	}
	return &clone
}

func pushHubDeletedSession(profile string, inst *session.Instance) {
	clone := cloneSessionForHubUndo(inst)
	if clone == nil {
		return
	}
	stack := hubUndoStackForProfile(profile)
	stack.mu.Lock()
	defer stack.mu.Unlock()
	stack.entries = append(stack.entries, hubDeletedSessionEntry{instance: clone, deletedAt: time.Now()})
	if len(stack.entries) > 10 {
		stack.entries = stack.entries[len(stack.entries)-10:]
	}
}

func popHubDeletedSession(profile string) (*session.Instance, error) {
	stack := hubUndoStackForProfile(profile)
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if len(stack.entries) == 0 {
		return nil, ErrHubUndoNothing
	}
	entry := stack.entries[len(stack.entries)-1]
	stack.entries = stack.entries[:len(stack.entries)-1]
	if time.Since(entry.deletedAt) > hubDeleteUndoWindow {
		return nil, ErrHubUndoExpired
	}
	if entry.instance == nil {
		return nil, ErrHubUndoNothing
	}
	return entry.instance, nil
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
	inst.PostStartSync(0)
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
	inst.PostStartSync(0)
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
	inst.PostStartSync(0)
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
	forked.PostStartSync(0)
	instances = append(instances, forked)
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return "", err
	}
	return forked.ID, nil
}

func (b LocalActionBackend) ForkWithOptions(ctx context.Context, req ForkSessionRequest) (string, error) {
	storage, instances, groups, inst, err := b.loadSessionData(req.SessionID)
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
	if req.WithIgnored {
		req.WithState = true
	}

	forkTitle := strings.TrimSpace(req.Title)
	if forkTitle == "" {
		forkTitle = strings.TrimSpace(inst.Title)
		if forkTitle == "" {
			forkTitle = inst.ID
		}
		forkTitle += " (fork)"
	}
	groupPath := strings.TrimSpace(req.GroupPath)
	if groupPath == "" {
		groupPath = inst.GroupPath
	}
	opts := inst.GetClaudeOptions()
	worktree, err := prepareHubForkWorktree(inst, req, forkTitle, opts)
	if err != nil {
		return "", err
	}
	if worktree.opts != nil {
		opts = worktree.opts
	}
	forked, _, err := inst.CreateForkedInstanceForTool(forkTitle, groupPath, opts)
	if err != nil {
		worktree.rollback()
		return "", fmt.Errorf("create fork: %w", err)
	}
	// A generated "(fork)" suffix is part of the child's independent identity,
	// not a temporary placeholder. Keep the shared fork-title invariant explicit
	// at this persistence boundary so future request-option changes cannot unlock it.
	forked.TitleLocked = true
	if worktree.backendType != "" {
		forked.WorktreeType = worktree.backendType
	}
	if req.Sandbox || strings.TrimSpace(req.SandboxImage) != "" {
		forked.Sandbox = session.NewSandboxConfig(strings.TrimSpace(req.SandboxImage))
	}
	if err := forked.Start(); err != nil {
		worktree.rollback()
		return "", fmt.Errorf("start fork: %w", err)
	}
	forked.PostStartSync(0)
	instances = append(instances, forked)
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		worktree.rollback()
		return "", err
	}
	return forked.ID, nil
}

type hubForkWorktree struct {
	opts        *session.ClaudeOptions
	backend     vcs.Backend
	backendType string
	path        string
	branch      string
	created     bool
}

func (w hubForkWorktree) rollback() {
	if !w.created || w.backend == nil || strings.TrimSpace(w.path) == "" {
		return
	}
	_ = w.backend.RemoveWorktree(w.path, true)
	if strings.TrimSpace(w.branch) != "" {
		_ = w.backend.DeleteBranch(w.branch, true)
	}
}

func prepareHubForkWorktree(inst *session.Instance, req ForkSessionRequest, forkTitle string, opts *session.ClaudeOptions) (hubForkWorktree, error) {
	branch := strings.TrimSpace(req.Branch)
	wantsWorktree := req.Worktree || req.WithState || req.WithIgnored || branch != ""
	if !wantsWorktree {
		return hubForkWorktree{}, nil
	}
	if inst == nil {
		return hubForkWorktree{}, fmt.Errorf("source session is required")
	}
	if strings.TrimSpace(inst.ProjectPath) == "" {
		return hubForkWorktree{}, fmt.Errorf("source session %q has no project path", inst.Title)
	}
	if branch == "" {
		branch = defaultHubForkBranch(inst, forkTitle)
	}
	backend, err := vcsbackend.Detect(inst.ProjectPath)
	if err != nil {
		return hubForkWorktree{}, fmt.Errorf("path is not a git or jujutsu repository: %w", err)
	}
	branchExplicit := strings.TrimSpace(req.Branch) != ""
	if !branchExplicit {
		branch = uniqueHubForkBranch(backend, branch)
	}
	wtSettings := session.GetWorktreeSettings()
	worktreePath := backend.WorktreePath(vcs.WorktreePathOptions{
		Branch:    branch,
		Location:  wtSettings.DefaultLocation,
		SessionID: git.GeneratePathID(),
		Template:  wtSettings.Template(),
	})
	if opts == nil {
		opts = &session.ClaudeOptions{}
	}
	opts.WorkDir = worktreePath
	opts.WorktreePath = worktreePath
	opts.WorktreeRepoRoot = backend.RepoDir()
	opts.WorktreeBranch = branch

	worktree := hubForkWorktree{
		opts:        opts,
		backend:     backend,
		backendType: string(backend.Type()),
		path:        worktreePath,
		branch:      branch,
	}
	if req.WithState {
		if err := createHubForkWorktreeWithState(inst.ProjectPath, backend, worktreePath, branch, req.WithIgnored); err != nil {
			return hubForkWorktree{}, err
		}
		worktree.created = true
		return worktree, nil
	}
	if existingPath, err := backend.GetWorktreeForBranch(branch); err == nil && existingPath != "" {
		worktree.path = existingPath
		opts.WorkDir = existingPath
		opts.WorktreePath = existingPath
		return worktree, nil
	}
	branchExisted := backend.BranchExists(branch)
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return hubForkWorktree{}, fmt.Errorf("failed to create worktree parent directory: %w", err)
	}
	var buf bytes.Buffer
	setupErr, err := vcsbackend.CreateWorktreeWithSetup(
		backend,
		worktreePath,
		branch,
		git.WorktreeCreateOptions{},
		&buf,
		&buf,
		session.GetWorktreeSettings().SetupTimeout(),
	)
	if err != nil {
		return hubForkWorktree{}, fmt.Errorf("worktree creation failed: %w", err)
	}
	if setupErr != nil {
		// Match the local TUI contract: setup hook failures are non-fatal.
		// There is no notice channel on hub command responses yet, so keep the
		// worktree and let the session start in it.
		_ = setupErr
	}
	worktree.created = true
	if branchExisted {
		worktree.branch = ""
	}
	return worktree, nil
}

func defaultHubForkBranch(inst *session.Instance, forkTitle string) string {
	base := strings.TrimSpace(forkTitle)
	if base == "" && inst != nil {
		base = strings.TrimSpace(inst.Title)
	}
	if base == "" && inst != nil {
		base = inst.ID
	}
	slug := git.SanitizeBranchName(strings.ToLower(base))
	if slug == "" {
		slug = "fork"
	}
	settings := session.GetWorktreeSettings()
	return settings.ApplyBranchPrefix(slug)
}

func uniqueHubForkBranch(backend vcs.Backend, base string) string {
	if backend == nil || strings.TrimSpace(base) == "" {
		return base
	}
	candidate := base
	for n := 2; hubForkBranchTaken(backend, candidate); n++ {
		candidate = fmt.Sprintf("%s-%d", base, n)
		if n > 1000 {
			return candidate
		}
	}
	return candidate
}

func hubForkBranchTaken(backend vcs.Backend, branch string) bool {
	if backend.BranchExists(branch) {
		return true
	}
	wt, err := backend.GetWorktreeForBranch(branch)
	return err == nil && wt != ""
}

func createHubForkWorktreeWithState(parentPath string, backend vcs.Backend, worktreePath, branch string, withIgnored bool) error {
	if backend == nil {
		return fmt.Errorf("VCS backend is required")
	}
	switch backend.Type() {
	case vcs.TypeGit:
		return createHubGitForkWorktreeWithState(parentPath, backend.RepoDir(), worktreePath, branch, withIgnored)
	case vcs.TypeJujutsu:
		return createHubJJForkWorktreeWithState(parentPath, backend.RepoDir(), worktreePath, branch, withIgnored)
	default:
		return fmt.Errorf("--with-state is not supported for this repository's VCS backend")
	}
}

func createHubGitForkWorktreeWithState(parentPath, repoRoot, worktreePath, branch string, withIgnored bool) error {
	if err := git.ValidateForkWithStateDestination(repoRoot, branch); err != nil {
		var collErr *git.DestinationCollisionError
		if errors.As(err, &collErr) {
			switch collErr.Kind {
			case git.CollisionWorktreeExists:
				return fmt.Errorf("branch %q already has a worktree at %s; choose a new destination branch for --with-state", collErr.Branch, collErr.Path)
			case git.CollisionBranchExists:
				return fmt.Errorf("branch %q already exists; choose a new destination branch for --with-state", collErr.Branch)
			}
		}
		return fmt.Errorf("failed to validate destination: %w", err)
	}
	if _, statErr := os.Stat(worktreePath); statErr == nil {
		return fmt.Errorf("worktree path already exists: %s", worktreePath)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("failed to stat worktree path: %w", statErr)
	}
	if kind, err := git.DetectInProgressOperation(parentPath); err != nil {
		return fmt.Errorf("failed to inspect parent repository state: %w", err)
	} else if kind != "" {
		return fmt.Errorf("cannot fork with state while parent repository has an in-progress %s", kind)
	}
	parentHead, err := git.HeadCommit(parentPath)
	if err != nil {
		return fmt.Errorf("failed to resolve parent session HEAD: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return fmt.Errorf("failed to create worktree parent directory: %w", err)
	}
	createdBranch, err := git.CreateWorktreeAtStartPoint(repoRoot, worktreePath, branch, parentHead)
	if err != nil {
		return fmt.Errorf("worktree creation failed: %w", err)
	}
	if err := git.MaterializeWipFromParent(parentPath, worktreePath, withIgnored); err != nil {
		var cleanupErrs []string
		if rmErr := git.RemoveWorktree(repoRoot, worktreePath, true); rmErr != nil {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("worktree remove failed: %v", rmErr))
		}
		if createdBranch {
			if brErr := git.DeleteBranch(repoRoot, branch, true); brErr != nil {
				cleanupErrs = append(cleanupErrs, fmt.Sprintf("branch delete failed: %v", brErr))
			}
		}
		if len(cleanupErrs) > 0 {
			return fmt.Errorf("failed to materialize parent state: %w; cleanup also failed (%s)", err, strings.Join(cleanupErrs, "; "))
		}
		return fmt.Errorf("failed to materialize parent state: %w; new worktree cleaned up", err)
	}
	var buf bytes.Buffer
	if err := git.ProcessWorktreeInclude(repoRoot, worktreePath, &buf); err != nil {
		// Non-fatal, matching the TUI path.
		_ = err
	}
	if err := git.RunWorktreeSetupAfterCreate(repoRoot, worktreePath, &buf, &buf, session.GetWorktreeSettings().SetupTimeout()); err != nil {
		// Non-fatal, matching the TUI path.
		_ = err
	}
	return nil
}

func createHubJJForkWorktreeWithState(parentPath, repoRoot, workspacePath, branch string, withIgnored bool) error {
	if _, statErr := os.Stat(workspacePath); statErr == nil {
		return fmt.Errorf("workspace path already exists: %s", workspacePath)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("failed to stat workspace path: %w", statErr)
	}
	if strings.TrimSpace(branch) != "" {
		if exists, err := jujutsu.BookmarkExists(repoRoot, branch); err != nil {
			return fmt.Errorf("failed to validate destination: %w", err)
		} else if exists {
			return fmt.Errorf("bookmark %q already exists; choose a new destination branch for --with-state", branch)
		}
	}
	parentBase, err := jujutsu.WorkingCopyParentRevision(parentPath)
	if err != nil {
		return fmt.Errorf("failed to resolve parent session committed anchor: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return fmt.Errorf("failed to create workspace parent directory: %w", err)
	}
	if err := jujutsu.CreateWorkspaceAtRevision(repoRoot, workspacePath, branch, parentBase); err != nil {
		return fmt.Errorf("workspace creation failed: %w", err)
	}
	if err := jujutsu.MaterializeWipFromParent(parentPath, workspacePath, withIgnored); err != nil {
		backend, detectErr := vcsbackend.Detect(repoRoot)
		if detectErr == nil {
			_ = backend.RemoveWorktree(workspacePath, true)
			if strings.TrimSpace(branch) != "" {
				_ = backend.DeleteBranch(branch, true)
			}
		}
		return fmt.Errorf("failed to materialize parent state: %w", err)
	}
	return nil
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
	if strings.TrimSpace(req.ReasoningEffort) != "" {
		if err := inst.ApplyLaunchReasoningEffort(req.ReasoningEffort); err != nil {
			return "", err
		}
	}
	if len(req.AdditionalPaths) > 0 {
		paths, err := normalizeMultiRepoPaths(append([]string{projectPath}, req.AdditionalPaths...))
		if err != nil {
			return "", err
		}
		root, err := hubMultiRepoWorktreesRoot()
		if err != nil {
			return "", err
		}
		tempDir := filepath.Join(root, inst.ID[:8])
		if err := rewriteMultiRepoSymlinkTree(inst, tempDir, paths); err != nil {
			return "", err
		}
		repoNames := make([]string, 0, len(inst.AllProjectPaths()))
		for _, p := range inst.AllProjectPaths() {
			repoNames = append(repoNames, filepath.Base(p))
		}
		// Match the TUI path: pre-seeding Claude trust/context is helpful, but
		// it must not make remote session creation fail. If this cannot write
		// ~/.claude.json or parent CLAUDE.md, the session can still start and
		// Claude will show its normal trust prompt.
		_ = session.ApplyMultiRepoClaudeContext(inst.Tool, inst.MultiRepoEnabled, session.GetUserMCPRootPath(), inst.MultiRepoTempDir, repoNames)
	}
	if err := inst.Start(); err != nil {
		return "", err
	}
	// Hub create runs inside a long-lived node process. Do not block the
	// command round-trip on CLI-oriented post-start probing; the live node will
	// publish an immediate snapshot and later snapshots can fill late metadata.
	inst.PostStartSync(0)
	instances = append(instances, inst)
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return "", err
	}
	return inst.ID, nil
}

func hubMultiRepoWorktreesRoot() (string, error) {
	dir, err := agentpaths.EffectiveDataPath("multi-repo-worktrees", "multi-repo-worktrees")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
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
	if err := storage.RemoveSessionAndVerify(sessionID, filtered, session.NewGroupTreeWithGroups(filtered, groups)); err != nil {
		return err
	}
	pushHubDeletedSession(b.Profile, inst)
	return nil
}

func (b LocalActionBackend) UndoDelete(ctx context.Context) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	inst, err := popHubDeletedSession(b.Profile)
	if err != nil {
		return "", err
	}
	if err := inst.Restart(); err != nil {
		return "", fmt.Errorf("restart restored session: %w", err)
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
	for _, candidate := range instances {
		if candidate != nil && candidate.ID == inst.ID {
			return inst.ID, nil
		}
	}
	all := append(instances, inst)
	if err := storage.InsertSessionAndVerify(inst, session.NewGroupTreeWithGroups(all, groups)); err != nil {
		return "", fmt.Errorf("restore session: %w", err)
	}
	return inst.ID, nil
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
		inst.PostStartSync(0)
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
		inst.PostStartSync(0)
		result.Restarted = true
		if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (b LocalActionBackend) SetupWorktree(ctx context.Context, req WorktreeSetupRequest) (WorktreeSetupResponse, error) {
	inst, closeStorage, err := b.loadInstance(req.SessionID)
	if err != nil {
		return WorktreeSetupResponse{}, err
	}
	defer closeStorage()
	if err := ctxErr(ctx); err != nil {
		return WorktreeSetupResponse{}, err
	}
	if !inst.IsWorktree() {
		return WorktreeSetupResponse{}, fmt.Errorf("session %q is not in a worktree", inst.Title)
	}
	repoRoot := strings.TrimSpace(inst.WorktreeRepoRoot)
	worktreePath := strings.TrimSpace(inst.WorktreePath)
	if repoRoot == "" || worktreePath == "" {
		return WorktreeSetupResponse{}, fmt.Errorf("session %q has incomplete worktree metadata", inst.Title)
	}
	scriptPath, scriptMode := git.FindWorktreeSetupScript(repoRoot)
	if scriptPath == "" {
		return WorktreeSetupResponse{}, fmt.Errorf("no setup script found at .agent-deck/worktree-setup.sh")
	}
	var buf bytes.Buffer
	if err := git.RunWorktreeSetupScript(scriptPath, scriptMode, repoRoot, worktreePath, &buf, &buf, session.GetWorktreeSettings().SetupTimeout()); err != nil {
		return WorktreeSetupResponse{}, err
	}
	return WorktreeSetupResponse{SessionID: inst.ID}, nil
}

func (b LocalActionBackend) FinishWorktree(ctx context.Context, req WorktreeFinishRequest) (WorktreeFinishResponse, error) {
	storage, instances, groups, inst, err := b.loadSessionData(req.SessionID)
	if err != nil {
		return WorktreeFinishResponse{}, err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return WorktreeFinishResponse{}, err
	}
	if !inst.IsWorktree() {
		return WorktreeFinishResponse{}, fmt.Errorf("session %q is not in a worktree", inst.Title)
	}

	repoRoot := strings.TrimSpace(inst.WorktreeRepoRoot)
	worktreePath := strings.TrimSpace(inst.WorktreePath)
	worktreeBranch := strings.TrimSpace(inst.WorktreeBranch)
	if repoRoot == "" || worktreePath == "" || worktreeBranch == "" {
		return WorktreeFinishResponse{}, fmt.Errorf("session %q has incomplete worktree metadata", inst.Title)
	}

	backend, err := vcsbackend.Detect(repoRoot)
	if err != nil {
		return WorktreeFinishResponse{}, fmt.Errorf("initialize VCS: %w", err)
	}

	if !req.Force {
		dirty, dErr := git.HasUncommittedChanges(worktreePath)
		if dErr != nil {
			if _, statErr := os.Stat(worktreePath); os.IsNotExist(statErr) {
				dirty = false
			} else {
				return WorktreeFinishResponse{}, fmt.Errorf("check worktree status: %w", dErr)
			}
		}
		if dirty {
			return WorktreeFinishResponse{}, fmt.Errorf("worktree has uncommitted changes (set force=true to override)")
		}
	}

	targetBranch := strings.TrimSpace(req.Into)
	if targetBranch == "" && !req.NoMerge {
		targetBranch, err = backend.GetDefaultBranch()
		if err != nil {
			return WorktreeFinishResponse{}, fmt.Errorf("determine target branch: %w (set into=<branch>)", err)
		}
	}
	if !req.NoMerge && targetBranch == worktreeBranch {
		return WorktreeFinishResponse{}, fmt.Errorf("cannot merge branch %q into itself", worktreeBranch)
	}

	if !req.NoMerge {
		checkout := exec.CommandContext(ctx, "git", "-C", repoRoot, "checkout", targetBranch)
		if out, cErr := checkout.CombinedOutput(); cErr != nil {
			return WorktreeFinishResponse{}, fmt.Errorf("checkout %s: %s", targetBranch, strings.TrimSpace(string(out)))
		}
		if mErr := backend.MergeBranch(worktreeBranch); mErr != nil {
			if backend.Type() == vcs.TypeGit {
				_ = exec.CommandContext(ctx, "git", "-C", repoRoot, "merge", "--abort").Run()
			}
			return WorktreeFinishResponse{}, fmt.Errorf("merge failed (aborted): %w", mErr)
		}
	}

	remaining := make([]*session.Instance, 0, len(instances))
	for _, candidate := range instances {
		if candidate == nil || candidate.ID == inst.ID {
			continue
		}
		remaining = append(remaining, candidate)
	}

	sharedWorktree := session.OtherSessionsShareWorktree(inst, remaining)
	branchDeleted := false
	if !sharedWorktree {
		if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
			_ = backend.RemoveWorktree(worktreePath, req.Force)
		}
		_ = backend.PruneWorktrees()
		if !req.KeepBranch {
			if dErr := backend.DeleteBranch(worktreeBranch, req.Force); dErr == nil {
				branchDeleted = true
			}
		}
	}

	if inst.Exists() {
		_ = inst.Kill()
	}

	groupTree := session.NewGroupTreeWithGroups(remaining, groups)
	if err := storage.RemoveSessionAndVerify(inst.ID, remaining, groupTree); err != nil {
		return WorktreeFinishResponse{}, fmt.Errorf("save session data: %w", err)
	}

	mergedInto := targetBranch
	if req.NoMerge {
		mergedInto = ""
	}
	return WorktreeFinishResponse{
		SessionID:     inst.ID,
		Branch:        worktreeBranch,
		MergedInto:    mergedInto,
		Merged:        !req.NoMerge,
		BranchDeleted: branchDeleted,
	}, nil
}

func (b LocalActionBackend) OpenSandboxShell(ctx context.Context, req SandboxShellRequest) (SandboxShellResponse, error) {
	inst, closeStorage, err := b.loadInstance(req.SessionID)
	if err != nil {
		return SandboxShellResponse{}, err
	}
	defer closeStorage()
	if err := ctxErr(ctx); err != nil {
		return SandboxShellResponse{}, err
	}
	if !inst.IsSandboxed() || strings.TrimSpace(inst.SandboxContainer) == "" {
		return SandboxShellResponse{}, fmt.Errorf("session %q is not a running sandbox session", inst.Title)
	}
	tmuxName, err := inst.OpenContainerShell()
	if err != nil {
		return SandboxShellResponse{}, err
	}
	attachSessionID, err := newTmuxAttachToken(inst.TmuxSocketName, tmuxName, 5*time.Minute)
	if err != nil {
		return SandboxShellResponse{}, err
	}
	return SandboxShellResponse{SessionID: inst.ID, AttachSessionID: attachSessionID}, nil
}

func (b LocalActionBackend) CreateGroup(ctx context.Context, req GroupCreateRequest) (GroupCreateResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return GroupCreateResponse{}, err
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return GroupCreateResponse{}, err
	}
	defer storage.Close()
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		return GroupCreateResponse{}, err
	}
	groupTree := session.NewGroupTreeWithGroups(instances, groups)
	if cfg, _ := session.LoadUserConfig(); cfg != nil {
		groupTree.DefaultMaxConcurrent = cfg.GroupDefaults.MaxConcurrent
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return GroupCreateResponse{}, fmt.Errorf("group name is required")
	}
	parentPath := normalizeHubGroupPath(req.ParentPath)
	if parentPath == session.DefaultGroupPath && strings.TrimSpace(req.ParentPath) == "" {
		parentPath = ""
	}
	var created *session.Group
	if parentPath != "" {
		if _, ok := groupTree.Groups[parentPath]; !ok {
			return GroupCreateResponse{}, fmt.Errorf("parent group %q not found", parentPath)
		}
		created = groupTree.CreateSubgroup(parentPath, name)
	} else {
		created = groupTree.CreateGroup(name)
	}
	if created == nil {
		return GroupCreateResponse{}, fmt.Errorf("failed to create group %q", name)
	}
	existed := false
	for _, group := range groups {
		if group != nil && group.Path == created.Path {
			existed = true
			break
		}
	}
	if strings.TrimSpace(req.DefaultPath) != "" {
		groupTree.SetDefaultPathForGroup(created.Path, req.DefaultPath)
	}
	if req.MaxConcurrent != nil {
		created.MaxConcurrent = *req.MaxConcurrent
	}
	if err := storage.SaveWithGroups(instances, groupTree); err != nil {
		return GroupCreateResponse{}, err
	}
	return GroupCreateResponse{
		Name:          created.Name,
		Path:          created.Path,
		DefaultPath:   groupTree.DefaultPathForGroup(created.Path),
		MaxConcurrent: created.MaxConcurrent,
		Existed:       existed,
	}, nil
}

func (b LocalActionBackend) RenameGroup(ctx context.Context, req GroupRenameRequest) (GroupRenameResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return GroupRenameResponse{}, err
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return GroupRenameResponse{}, err
	}
	defer storage.Close()
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		return GroupRenameResponse{}, err
	}
	groupTree := session.NewGroupTreeWithGroups(instances, groups)
	oldPath := normalizeHubGroupPath(req.GroupPath)
	if _, ok := groupTree.Groups[oldPath]; !ok {
		return GroupRenameResponse{}, fmt.Errorf("group %q not found", oldPath)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return GroupRenameResponse{}, fmt.Errorf("group name is required")
	}
	if err := groupTree.RenameGroup(oldPath, name); err != nil {
		return GroupRenameResponse{}, err
	}
	newPath := renamedHubGroupPath(oldPath, name)
	group := groupTree.Groups[newPath]
	if group == nil {
		for path, candidate := range groupTree.Groups {
			if candidate != nil && candidate.Name == name && sameHubGroupParent(path, oldPath) {
				newPath = path
				group = candidate
				break
			}
		}
	}
	if group == nil {
		return GroupRenameResponse{}, fmt.Errorf("renamed group %q not found", name)
	}
	if err := storage.SaveWithGroups(groupTree.GetAllInstances(), groupTree); err != nil {
		return GroupRenameResponse{}, err
	}
	if oldPath != newPath {
		if err := storage.DeleteGroupSubtree(oldPath); err != nil {
			return GroupRenameResponse{}, err
		}
	}
	return GroupRenameResponse{OldPath: oldPath, Path: newPath, Name: group.Name}, nil
}

func (b LocalActionBackend) UpdateGroup(ctx context.Context, req GroupUpdateRequest) (GroupUpdateResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return GroupUpdateResponse{}, err
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return GroupUpdateResponse{}, err
	}
	defer storage.Close()
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		return GroupUpdateResponse{}, err
	}
	groupTree := session.NewGroupTreeWithGroups(instances, groups)
	groupPath := normalizeHubGroupPath(req.GroupPath)
	group := groupTree.Groups[groupPath]
	if group == nil {
		return GroupUpdateResponse{}, fmt.Errorf("group %q not found", groupPath)
	}
	if req.ClearDefaultPath {
		groupTree.SetDefaultPathForGroup(groupPath, "")
	} else if req.DefaultPath != nil {
		groupTree.SetDefaultPathForGroup(groupPath, *req.DefaultPath)
	}
	if req.MaxConcurrent != nil {
		group.MaxConcurrent = *req.MaxConcurrent
	}
	if err := storage.SaveWithGroups(instances, groupTree); err != nil {
		return GroupUpdateResponse{}, err
	}
	return GroupUpdateResponse{
		Path:          groupPath,
		DefaultPath:   groupTree.DefaultPathForGroup(groupPath),
		MaxConcurrent: group.MaxConcurrent,
	}, nil
}

func (b LocalActionBackend) DeleteGroup(ctx context.Context, req GroupDeleteRequest) (GroupDeleteResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return GroupDeleteResponse{}, err
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return GroupDeleteResponse{}, err
	}
	defer storage.Close()
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		return GroupDeleteResponse{}, err
	}
	groupTree := session.NewGroupTreeWithGroups(instances, groups)
	groupPath := normalizeHubGroupPath(req.GroupPath)
	if groupPath == session.DefaultGroupPath {
		return GroupDeleteResponse{}, fmt.Errorf("cannot delete default group")
	}
	group := groupTree.Groups[groupPath]
	if group == nil {
		return GroupDeleteResponse{}, fmt.Errorf("group %q not found", groupPath)
	}
	sessionCount := len(group.Sessions)
	for path, candidate := range groupTree.Groups {
		if strings.HasPrefix(path, groupPath+"/") && candidate != nil {
			sessionCount += len(candidate.Sessions)
		}
	}
	if sessionCount > 0 && !req.Force {
		return GroupDeleteResponse{}, fmt.Errorf("group %q has %d sessions; set force=true to move them to %s", groupPath, sessionCount, session.DefaultGroupPath)
	}
	moved := groupTree.DeleteGroup(groupPath)
	if err := storage.SaveWithGroups(groupTree.GetAllInstances(), groupTree); err != nil {
		return GroupDeleteResponse{}, err
	}
	if err := storage.DeleteGroupSubtree(groupPath); err != nil {
		return GroupDeleteResponse{}, err
	}
	return GroupDeleteResponse{Path: groupPath, SessionsMoved: len(moved), MovedTo: session.DefaultGroupPath}, nil
}

func (b LocalActionBackend) ReparentGroup(ctx context.Context, req GroupReparentRequest) (GroupReparentResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return GroupReparentResponse{}, err
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return GroupReparentResponse{}, err
	}
	defer storage.Close()
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		return GroupReparentResponse{}, err
	}
	groupTree := session.NewGroupTreeWithGroups(instances, groups)
	sourcePath := normalizeHubGroupPath(req.GroupPath)
	destPath := strings.Trim(strings.TrimSpace(req.DestParentPath), "/")
	if strings.EqualFold(destPath, "root") {
		destPath = ""
	}
	if destPath != "" {
		destPath = normalizeHubGroupPath(destPath)
	}
	if err := groupTree.MoveGroupTo(sourcePath, destPath); err != nil {
		return GroupReparentResponse{}, err
	}
	newPath := reparentedHubGroupPath(sourcePath, destPath)
	if err := storage.SaveWithGroups(groupTree.GetAllInstances(), groupTree); err != nil {
		return GroupReparentResponse{}, err
	}
	if sourcePath != newPath {
		if err := storage.DeleteGroupSubtree(sourcePath); err != nil {
			return GroupReparentResponse{}, err
		}
	}
	return GroupReparentResponse{OldPath: sourcePath, Path: newPath, DestParentPath: destPath}, nil
}

func (b LocalActionBackend) ReorderGroup(ctx context.Context, req GroupReorderRequest) (GroupReorderResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return GroupReorderResponse{}, err
	}
	storage, err := session.NewStorageWithProfile(b.Profile)
	if err != nil {
		return GroupReorderResponse{}, err
	}
	defer storage.Close()
	instances, groups, err := storage.LoadWithGroups()
	if err != nil {
		return GroupReorderResponse{}, err
	}
	groupTree := session.NewGroupTreeWithGroups(instances, groups)
	groupPath := normalizeHubGroupPath(req.GroupPath)
	if _, ok := groupTree.Groups[groupPath]; !ok {
		return GroupReorderResponse{}, fmt.Errorf("group %q not found", groupPath)
	}
	fromPos, siblings := hubGroupSiblingPosition(groupTree, groupPath)
	if fromPos < 0 {
		return GroupReorderResponse{}, fmt.Errorf("group %q is not in group list", groupPath)
	}
	if req.Position != nil {
		target := *req.Position
		if target < 0 {
			target = 0
		}
		if target >= len(siblings) {
			target = len(siblings) - 1
		}
		for {
			cur, _ := hubGroupSiblingPosition(groupTree, groupPath)
			if cur == target {
				break
			}
			if cur > target {
				groupTree.MoveGroupUp(groupPath)
			} else {
				groupTree.MoveGroupDown(groupPath)
			}
			next, _ := hubGroupSiblingPosition(groupTree, groupPath)
			if next == cur {
				break
			}
		}
	} else {
		switch strings.ToLower(strings.TrimSpace(req.Direction)) {
		case "up":
			groupTree.MoveGroupUp(groupPath)
		case "down":
			groupTree.MoveGroupDown(groupPath)
		default:
			return GroupReorderResponse{}, fmt.Errorf("group reorder direction must be up or down")
		}
	}
	toPos, _ := hubGroupSiblingPosition(groupTree, groupPath)
	if err := storage.SaveWithGroups(groupTree.GetAllInstances(), groupTree); err != nil {
		return GroupReorderResponse{}, err
	}
	return GroupReorderResponse{Path: groupPath, FromPosition: fromPos, ToPosition: toPos}, nil
}

func normalizeHubGroupPath(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return session.DefaultGroupPath
	}
	return strings.ReplaceAll(path, " ", "-")
}

func reparentedHubGroupPath(sourcePath, destParentPath string) string {
	baseName := sourcePath
	if idx := strings.LastIndex(sourcePath, "/"); idx >= 0 {
		baseName = sourcePath[idx+1:]
	}
	if strings.TrimSpace(destParentPath) == "" {
		return baseName
	}
	return strings.Trim(strings.TrimSpace(destParentPath), "/") + "/" + baseName
}

func hubGroupSiblingPosition(groupTree *session.GroupTree, groupPath string) (int, []string) {
	if groupTree == nil {
		return -1, nil
	}
	parentPath := hubParentGroupPath(groupPath)
	level := session.GetGroupLevel(groupPath)
	siblings := make([]string, 0)
	for _, group := range groupTree.GroupList {
		if group == nil {
			continue
		}
		if hubParentGroupPath(group.Path) == parentPath && session.GetGroupLevel(group.Path) == level {
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

func renamedHubGroupPath(oldPath, newName string) string {
	base := hubGroupBasePath(newName)
	parent := hubParentGroupPath(oldPath)
	if parent == "" {
		return base
	}
	return parent + "/" + base
}

func hubGroupBasePath(name string) string {
	tree := session.NewGroupTree(nil)
	group := tree.CreateGroup(name)
	if group == nil || strings.TrimSpace(group.Path) == "" {
		return "unnamed"
	}
	return group.Path
}

func hubParentGroupPath(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[:idx]
	}
	return ""
}

func sameHubGroupParent(path, oldPath string) bool {
	return hubParentGroupPath(path) == hubParentGroupPath(oldPath)
}

func (b LocalActionBackend) ListMCPs(ctx context.Context, req MCPListRequest) (MCPListResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return MCPListResponse{}, err
	}
	inst, closeFn, err := b.loadInstance(req.SessionID)
	if err != nil {
		return MCPListResponse{}, err
	}
	defer closeFn()
	return MCPListResponse{
		SessionID: inst.ID,
		Local:     hubFilterDefinedMCPNames(session.GetProjectMCPNames(inst.ProjectPath)),
		Global:    hubFilterDefinedMCPNames(session.GetGlobalMCPNames()),
		User:      hubFilterDefinedMCPNames(session.GetUserMCPNames()),
		Catalog:   hubMCPCatalogEntries(),
	}, nil
}

func hubMCPCatalogEntries() []MCPCatalogEntry {
	mcps := session.GetAvailableMCPs()
	names := session.GetAvailableMCPNames()
	out := make([]MCPCatalogEntry, 0, len(names))
	for _, name := range names {
		def := mcps[name]
		transport := def.Transport
		if transport == "" {
			if def.URL != "" {
				transport = "http"
			} else {
				transport = "stdio"
			}
		}
		out = append(out, MCPCatalogEntry{
			Name:        name,
			Description: def.Description,
			Transport:   transport,
			Command:     def.Command,
			URL:         def.URL,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		leftName := strings.ToLower(out[i].Name)
		rightName := strings.ToLower(out[j].Name)
		return leftName < rightName
	})
	return out
}

func (b LocalActionBackend) AttachMCP(ctx context.Context, req MCPMutateRequest) (MCPMutateResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return MCPMutateResponse{}, err
	}
	inst, closeFn, err := b.loadInstance(req.SessionID)
	if err != nil {
		return MCPMutateResponse{}, err
	}
	defer closeFn()
	scope := normalizeMCPScope(req.Scope, "local")
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return MCPMutateResponse{}, fmt.Errorf("mcp name is required")
	}
	names, err := hubMCPNamesAt(inst.ProjectPath, scope)
	if err != nil {
		return MCPMutateResponse{}, err
	}
	for _, existing := range names {
		if existing == name {
			return MCPMutateResponse{SessionID: inst.ID, Name: name, Scope: scope}, nil
		}
	}
	if err := hubWriteMCPScope(inst.ProjectPath, scope, append(names, name)); err != nil {
		return MCPMutateResponse{}, err
	}
	return MCPMutateResponse{SessionID: inst.ID, Name: name, Scope: scope}, nil
}

func (b LocalActionBackend) DetachMCP(ctx context.Context, req MCPMutateRequest) (MCPMutateResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return MCPMutateResponse{}, err
	}
	inst, closeFn, err := b.loadInstance(req.SessionID)
	if err != nil {
		return MCPMutateResponse{}, err
	}
	defer closeFn()
	scope := normalizeMCPScope(req.Scope, "local")
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return MCPMutateResponse{}, fmt.Errorf("mcp name is required")
	}
	names, err := hubMCPNamesAt(inst.ProjectPath, scope)
	if err != nil {
		return MCPMutateResponse{}, err
	}
	out := names[:0]
	for _, existing := range names {
		if existing != name {
			out = append(out, existing)
		}
	}
	if err := hubWriteMCPScope(inst.ProjectPath, scope, out); err != nil {
		return MCPMutateResponse{}, err
	}
	return MCPMutateResponse{SessionID: inst.ID, Name: name, Scope: scope}, nil
}

func (b LocalActionBackend) MoveMCP(ctx context.Context, req MCPMoveRequest) (MCPMoveResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return MCPMoveResponse{}, err
	}
	fromScope := normalizeMCPScope(req.FromScope, "")
	toScope := normalizeMCPScope(req.ToScope, "")
	name := strings.TrimSpace(req.Name)
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" || name == "" || fromScope == "" || toScope == "" {
		return MCPMoveResponse{}, fmt.Errorf("mcp move requires session_id, name, from_scope, and to_scope")
	}
	if fromScope == toScope {
		return MCPMoveResponse{SessionID: sessionID, Name: name, FromScope: fromScope, ToScope: toScope}, nil
	}
	if _, err := b.DetachMCP(ctx, MCPMutateRequest{SessionID: sessionID, Name: name, Scope: fromScope}); err != nil {
		return MCPMoveResponse{}, err
	}
	if _, err := b.AttachMCP(ctx, MCPMutateRequest{SessionID: sessionID, Name: name, Scope: toScope}); err != nil {
		return MCPMoveResponse{}, err
	}
	return MCPMoveResponse{SessionID: sessionID, Name: name, FromScope: fromScope, ToScope: toScope}, nil
}

func normalizeMCPScope(scope, defaultScope string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = strings.ToLower(strings.TrimSpace(defaultScope))
	}
	return scope
}

func hubMCPNamesAt(projectPath, scope string) ([]string, error) {
	switch scope {
	case "local":
		return hubFilterDefinedMCPNames(session.GetProjectMCPNames(projectPath)), nil
	case "global":
		return hubFilterDefinedMCPNames(session.GetGlobalMCPNames()), nil
	case "user":
		return hubFilterDefinedMCPNames(session.GetUserMCPNames()), nil
	default:
		return nil, fmt.Errorf("invalid MCP scope: %s", scope)
	}
}

func hubWriteMCPScope(projectPath, scope string, names []string) error {
	switch scope {
	case "local":
		return session.WriteMCPJsonFromConfig(projectPath, names)
	case "global":
		return session.WriteGlobalMCP(names)
	case "user":
		return session.WriteUserMCP(names)
	default:
		return fmt.Errorf("invalid MCP scope: %s", scope)
	}
}

func hubFilterDefinedMCPNames(names []string) []string {
	catalog := session.GetAvailableMCPs()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := catalog[name]; ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (b LocalActionBackend) ListSkills(ctx context.Context, req SkillListRequest) (SkillListResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SkillListResponse{}, err
	}
	inst, closeFn, err := b.loadInstance(req.SessionID)
	if err != nil {
		return SkillListResponse{}, err
	}
	defer closeFn()
	catalog, err := session.ListAvailableSkills()
	if err != nil {
		return SkillListResponse{}, err
	}
	attached, err := session.GetAttachedProjectSkills(inst.ProjectPath)
	if err != nil {
		return SkillListResponse{}, err
	}
	if catalog == nil {
		catalog = []session.SkillCandidate{}
	}
	if attached == nil {
		attached = []session.ProjectSkillAttachment{}
	}
	return SkillListResponse{SessionID: inst.ID, Catalog: catalog, Attached: attached}, nil
}

func (b LocalActionBackend) AttachSkill(ctx context.Context, req SkillMutateRequest) (SkillMutateResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SkillMutateResponse{}, err
	}
	inst, closeFn, err := b.loadInstance(req.SessionID)
	if err != nil {
		return SkillMutateResponse{}, err
	}
	defer closeFn()
	if !session.SupportsProjectSkills(inst.Tool) {
		return SkillMutateResponse{}, fmt.Errorf("project skills are not supported for %s sessions", inst.Tool)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return SkillMutateResponse{}, fmt.Errorf("skill name is required")
	}
	attachment, err := session.AttachSkillToProject(inst.ProjectPath, inst.Tool, name, strings.TrimSpace(req.Source))
	if err != nil {
		return SkillMutateResponse{}, err
	}
	return SkillMutateResponse{SessionID: inst.ID, Skill: attachment}, nil
}

func (b LocalActionBackend) DetachSkill(ctx context.Context, req SkillMutateRequest) (SkillMutateResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SkillMutateResponse{}, err
	}
	inst, closeFn, err := b.loadInstance(req.SessionID)
	if err != nil {
		return SkillMutateResponse{}, err
	}
	defer closeFn()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return SkillMutateResponse{}, fmt.Errorf("skill name is required")
	}
	attachment, err := session.DetachSkillFromProject(inst.ProjectPath, name, strings.TrimSpace(req.Source))
	if err != nil {
		return SkillMutateResponse{}, err
	}
	return SkillMutateResponse{SessionID: inst.ID, Skill: attachment}, nil
}

func (b LocalActionBackend) ListPlugins(ctx context.Context, req PluginListRequest) (PluginListResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return PluginListResponse{}, err
	}
	inst, closeFn, err := b.loadInstance(req.SessionID)
	if err != nil {
		return PluginListResponse{}, err
	}
	defer closeFn()
	return pluginListResponseForInstance(inst), nil
}

func (b LocalActionBackend) AttachPlugin(ctx context.Context, req PluginMutateRequest) (PluginMutateResponse, error) {
	return b.mutatePlugin(ctx, req, "attach")
}

func (b LocalActionBackend) DetachPlugin(ctx context.Context, req PluginMutateRequest) (PluginMutateResponse, error) {
	return b.mutatePlugin(ctx, req, "detach")
}

func (b LocalActionBackend) mutatePlugin(ctx context.Context, req PluginMutateRequest, op string) (PluginMutateResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return PluginMutateResponse{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return PluginMutateResponse{}, fmt.Errorf("plugin name is required")
	}
	storage, instances, groups, inst, err := b.loadSessionData(req.SessionID)
	if err != nil {
		return PluginMutateResponse{}, err
	}
	defer storage.Close()

	current := append([]string(nil), inst.Plugins...)
	updated := hubPluginListMutation(current, name, op)
	changed := !sameStringList(current, updated)
	flagToggle := op == "attach" && req.NoChannelLink && !inst.PluginChannelLinkDisabled
	if !changed && !flagToggle {
		return pluginMutateResponseForInstance(inst), nil
	}
	if flagToggle {
		inst.PluginChannelLinkDisabled = true
	}
	if _, _, err := session.SetField(inst, session.FieldPlugins, strings.Join(updated, ","), nil); err != nil {
		return PluginMutateResponse{}, err
	}
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return PluginMutateResponse{}, err
	}
	return pluginMutateResponseForInstance(inst), nil
}

func pluginListResponseForInstance(inst *session.Instance) PluginListResponse {
	if inst == nil {
		return PluginListResponse{Catalog: pluginCatalogEntries()}
	}
	return PluginListResponse{
		SessionID:                 inst.ID,
		Catalog:                   pluginCatalogEntries(),
		Plugins:                   sortedStringCopy(inst.Plugins),
		Channels:                  sortedStringCopy(inst.Channels),
		PluginChannelLinkDisabled: inst.PluginChannelLinkDisabled,
	}
}

func pluginMutateResponseForInstance(inst *session.Instance) PluginMutateResponse {
	if inst == nil {
		return PluginMutateResponse{}
	}
	return PluginMutateResponse{
		SessionID:                 inst.ID,
		Plugins:                   sortedStringCopy(inst.Plugins),
		Channels:                  sortedStringCopy(inst.Channels),
		PluginChannelLinkDisabled: inst.PluginChannelLinkDisabled,
	}
}

func pluginCatalogEntries() []PluginCatalogEntry {
	plugins := session.GetAvailablePlugins()
	names := session.GetAvailablePluginNames()
	out := make([]PluginCatalogEntry, 0, len(names))
	for _, name := range names {
		def := plugins[name]
		out = append(out, PluginCatalogEntry{
			Name:         name,
			PluginName:   def.Name,
			Source:       def.Source,
			ID:           def.ID(),
			Description:  def.Description,
			EmitsChannel: def.EmitsChannel,
			AutoInstall:  def.AutoInstall,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		leftName := strings.ToLower(out[i].Name)
		rightName := strings.ToLower(out[j].Name)
		return leftName < rightName
	})
	return out
}

func hubPluginListMutation(current []string, name, op string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return append([]string(nil), current...)
	}
	if op == "attach" {
		for _, existing := range current {
			if existing == name {
				return append([]string(nil), current...)
			}
		}
		out := append([]string(nil), current...)
		return append(out, name)
	}
	out := make([]string, 0, len(current))
	for _, existing := range current {
		if existing != name {
			out = append(out, existing)
		}
	}
	return out
}

func sameStringList(a, b []string) bool {
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

func sortedStringCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
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
		inst.PostStartSync(0)
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
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return err
	}
	if db := storage.GetDB(); db != nil {
		return db.SetAcknowledged(inst.ID, false)
	}
	return nil
}

// Acknowledge records that a waiting session has been viewed through a hub
// terminal. The owner persists the transition so every requester observes the
// same idle state on the next snapshot.
func (b LocalActionBackend) Acknowledge(ctx context.Context, sessionID string) error {
	storage, instances, groups, inst, err := b.loadSessionData(sessionID)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if inst.GetStatusThreadSafe() != session.StatusWaiting {
		return nil
	}

	inst.ClearHookStatus()
	if tmuxSess := inst.GetTmuxSession(); tmuxSess != nil {
		tmuxSess.Acknowledge()
	}
	inst.SetStatusThreadSafe(session.StatusIdle)
	if err := storage.SaveWithGroups(instances, session.NewGroupTreeWithGroups(instances, groups)); err != nil {
		return err
	}
	if db := storage.GetDB(); db != nil {
		if err := db.SetAcknowledged(inst.ID, true); err != nil {
			return err
		}
		if err := db.WriteStatus(inst.ID, string(session.StatusIdle), inst.GetToolThreadSafe()); err != nil {
			return err
		}
	}
	return nil
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
