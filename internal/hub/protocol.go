package hub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const ProtocolVersion = 1

const (
	MaxAttachFrameBytes = 4 * 1024 * 1024
	maxHubEnvelopeBytes = 8 * 1024 * 1024
)

type MessageType string

const (
	MsgHello         MessageType = "hello"
	MsgWelcome       MessageType = "welcome"
	MsgSnapshot      MessageType = "snapshot"
	MsgHeartbeat     MessageType = "heartbeat"
	MsgCommand       MessageType = "command"
	MsgCommandResult MessageType = "command_result"
	MsgAttachOpen    MessageType = "attach_open"
	MsgAttachReady   MessageType = "attach_ready"
	MsgAttachData    MessageType = "attach_data"
	MsgAttachResize  MessageType = "attach_resize"
	MsgAttachClose   MessageType = "attach_close"
	MsgAttachClosed  MessageType = "attach_closed"
	MsgTrustRequest  MessageType = "trust_request"
	MsgTrustDecision MessageType = "trust_decision"
	MsgError         MessageType = "error"
)

type Envelope struct {
	Version   int             `json:"version"`
	Type      MessageType     `json:"type"`
	NodeID    string          `json:"node_id,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type NodeHelloPayload struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Token    string `json:"token,omitempty"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

type WelcomePayload struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Admin    bool   `json:"admin,omitempty"`
}

type SessionInfo struct {
	ID                        string          `json:"id"`
	Title                     string          `json:"title"`
	Tool                      string          `json:"tool"`
	Status                    string          `json:"status"`
	Substate                  string          `json:"substate,omitempty"`
	GroupPath                 string          `json:"group_path"`
	ProjectPath               string          `json:"project_path,omitempty"`
	ParentSessionID           string          `json:"parent_session_id,omitempty"`
	IsConductor               bool            `json:"is_conductor,omitempty"`
	Windows                   []WindowInfo    `json:"windows,omitempty"`
	Command                   string          `json:"command,omitempty"`
	Wrapper                   string          `json:"wrapper,omitempty"`
	TmuxSession               string          `json:"tmux_session,omitempty"`
	TmuxSocketName            string          `json:"tmux_socket_name,omitempty"`
	Color                     string          `json:"color,omitempty"`
	ClaudeSessionID           string          `json:"claude_session_id,omitempty"`
	GeminiSessionID           string          `json:"gemini_session_id,omitempty"`
	GeminiModel               string          `json:"gemini_model,omitempty"`
	GeminiYoloMode            *bool           `json:"gemini_yolo_mode,omitempty"`
	OpenCodeSessionID         string          `json:"opencode_session_id,omitempty"`
	CodexSessionID            string          `json:"codex_session_id,omitempty"`
	LatestPrompt              string          `json:"latest_prompt,omitempty"`
	AdditionalPaths           []string        `json:"additional_paths,omitempty"`
	MultiRepoEnabled          bool            `json:"multi_repo_enabled,omitempty"`
	MultiRepoTempDir          string          `json:"multi_repo_temp_dir,omitempty"`
	MultiRepoWorktrees        []Worktree      `json:"multi_repo_worktrees,omitempty"`
	WorktreePath              string          `json:"worktree_path,omitempty"`
	WorktreeRepoRoot          string          `json:"worktree_repo_root,omitempty"`
	WorktreeBranch            string          `json:"worktree_branch,omitempty"`
	Notes                     string          `json:"notes,omitempty"`
	LoadedMCPNames            []string        `json:"loaded_mcp_names,omitempty"`
	Plugins                   []string        `json:"plugins,omitempty"`
	Channels                  []string        `json:"channels,omitempty"`
	PluginChannelLinkDisabled bool            `json:"plugin_channel_link_disabled,omitempty"`
	ExtraArgs                 []string        `json:"extra_args,omitempty"`
	ToolOptionsJSON           json.RawMessage `json:"tool_options,omitempty"`
	Sandbox                   json.RawMessage `json:"sandbox,omitempty"`
	SandboxContainer          string          `json:"sandbox_container,omitempty"`
	SSHHost                   string          `json:"ssh_host,omitempty"`
	SSHRemotePath             string          `json:"ssh_remote_path,omitempty"`
	TitleLocked               bool            `json:"title_locked,omitempty"`
	NoTransitionNotify        bool            `json:"no_transition_notify,omitempty"`
	DisplaySessionID          string          `json:"display_session_id,omitempty"`
	CanFork                   bool            `json:"can_fork,omitempty"`
	UpdatedAt                 *time.Time      `json:"updated_at,omitempty"`
	ArchivedAt                *time.Time      `json:"archived_at,omitempty"`
}

type Worktree struct {
	OriginalPath string `json:"original_path,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	RepoRoot     string `json:"repo_root,omitempty"`
	Branch       string `json:"branch,omitempty"`
}

type WindowInfo struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Activity int64  `json:"activity,omitempty"`
	Tool     string `json:"tool,omitempty"`
}

type GroupInfo struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Expanded      bool   `json:"expanded,omitempty"`
	Order         int    `json:"order,omitempty"`
	DefaultPath   string `json:"default_path,omitempty"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"`
}

type SnapshotPayload struct {
	NodeID       string        `json:"node_id,omitempty"`
	NodeName     string        `json:"node_name,omitempty"`
	Admin        bool          `json:"admin,omitempty"`
	SentAt       time.Time     `json:"sent_at"`
	WebAvailable bool          `json:"web_available,omitempty"`
	Sessions     []SessionInfo `json:"sessions"`
	Groups       []GroupInfo   `json:"groups,omitempty"`
}

type CommandPayload struct {
	CommandID string          `json:"command_id"`
	NodeID    string          `json:"node_id,omitempty"`
	Action    string          `json:"action"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type CommandResultPayload struct {
	CommandID string          `json:"command_id"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

type AttachOpenPayload struct {
	StreamID    string `json:"stream_id"`
	NodeID      string `json:"node_id,omitempty"`
	SessionID   string `json:"session_id"`
	WindowIndex *int   `json:"window_index,omitempty"`
	Cols        int    `json:"cols,omitempty"`
	Rows        int    `json:"rows,omitempty"`
}

type AttachDataPayload struct {
	StreamID string `json:"stream_id"`
	DataB64  string `json:"data_b64"`
}

func NewAttachData(streamID string, data []byte) AttachDataPayload {
	return AttachDataPayload{StreamID: streamID, DataB64: base64.StdEncoding.EncodeToString(data)}
}

func (p AttachDataPayload) Bytes() ([]byte, error) {
	if len(p.DataB64) > base64.StdEncoding.EncodedLen(MaxAttachFrameBytes) {
		return nil, fmt.Errorf("attach data exceeds %d bytes", MaxAttachFrameBytes)
	}
	data, err := base64.StdEncoding.DecodeString(p.DataB64)
	if err != nil {
		return nil, fmt.Errorf("decode attach data: %w", err)
	}
	if len(data) > MaxAttachFrameBytes {
		return nil, fmt.Errorf("attach data exceeds %d bytes", MaxAttachFrameBytes)
	}
	return data, nil
}

type AttachResizePayload struct {
	StreamID string `json:"stream_id"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
}

type AttachClosePayload struct {
	StreamID string `json:"stream_id"`
	Reason   string `json:"reason,omitempty"`
}

type TrustRequestPayload struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Version  string `json:"version,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Status   string `json:"status,omitempty"`
}

type TrustDecisionPayload struct {
	NodeID string `json:"node_id"`
	Allow  bool   `json:"allow"`
}

type AdminInvite struct {
	ID              string     `json:"id,omitempty"`
	NodeName        string     `json:"node_name"`
	ExpiresAt       time.Time  `json:"expires_at"`
	ConsumedAt      *time.Time `json:"consumed_at,omitempty"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	Admin           bool       `json:"admin"`
	CreatedByNodeID string     `json:"created_by_node_id,omitempty"`
	Status          string     `json:"status"`
}

type CreateAdminInviteRequest struct {
	NodeName   string `json:"node_name"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
	Admin      bool   `json:"admin,omitempty"`
}

type CreateAdminInviteResponse struct {
	URL         string    `json:"url"`
	InviteToken string    `json:"invite_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type ErrorPayload struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

func MarshalEnvelope(typ MessageType, nodeID string, payload any) (Envelope, error) {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		raw = data
	}
	return Envelope{Version: ProtocolVersion, Type: typ, NodeID: nodeID, Payload: raw}, nil
}
