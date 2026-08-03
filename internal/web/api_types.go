package web

import (
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Error code constants for API error responses.
const (
	ErrCodeUnauthorized     = "UNAUTHORIZED"
	ErrCodeForbidden        = "MUTATIONS_DISABLED"
	ErrCodeCSRF             = "CROSS_ORIGIN_BLOCKED"
	ErrCodeNotFound         = "NOT_FOUND"
	ErrCodeBadRequest       = "INVALID_REQUEST"
	ErrCodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	ErrCodeRateLimited      = "RATE_LIMITED"
	ErrCodeInternalError    = "INTERNAL_ERROR"
	ErrCodeNotImplemented   = "NOT_IMPLEMENTED"
	ErrCodeReadOnly         = "READ_ONLY"
	ErrCodeUnavailable      = "UNAVAILABLE"
)

// CreateSessionRequest is the body for POST /api/sessions.
type CreateSessionRequest struct {
	Title           string   `json:"title"`
	Tool            string   `json:"tool"`
	ProjectPath     string   `json:"projectPath"`
	AdditionalPaths []string `json:"additionalPaths,omitempty"`
	GroupPath       string   `json:"groupPath,omitempty"`
	ModelID         string   `json:"modelId,omitempty"`
	ReasoningEffort string   `json:"reasoningEffort,omitempty"`
	HubNodeID       string   `json:"hubNodeId,omitempty"`
}

// ForkSessionRequest is the body for POST /api/sessions/{id}/fork when the
// caller wants TUI Shift+F-style fork controls instead of a plain quick fork.
// Empty bodies remain valid and route to SessionMutator.ForkSession.
type ForkSessionRequest struct {
	Title        string `json:"title,omitempty"`
	GroupPath    string `json:"groupPath,omitempty"`
	Worktree     bool   `json:"worktree,omitempty"`
	Branch       string `json:"branch,omitempty"`
	WithState    bool   `json:"withState,omitempty"`
	WithIgnored  bool   `json:"withIgnored,omitempty"`
	Sandbox      bool   `json:"sandbox,omitempty"`
	SandboxImage string `json:"sandboxImage,omitempty"`
}

func (r ForkSessionRequest) HasOptions() bool {
	return r.Title != "" ||
		r.GroupPath != "" ||
		r.Worktree ||
		r.Branch != "" ||
		r.WithState ||
		r.WithIgnored ||
		r.Sandbox ||
		r.SandboxImage != ""
}

// CreateGroupRequest is the body for POST /api/groups.
type CreateGroupRequest struct {
	Name       string `json:"name"`
	ParentPath string `json:"parentPath,omitempty"`
}

// RenameGroupRequest is the body for PATCH /api/groups/:path.
type RenameGroupRequest struct {
	Name string `json:"name"`
}

// ReparentGroupRequest is the body for POST /api/groups/:path/change.
type ReparentGroupRequest struct {
	DestParentPath string `json:"destParentPath,omitempty"`
}

// ReorderGroupRequest is the body for POST /api/groups/:path/reorder.
type ReorderGroupRequest struct {
	Direction string `json:"direction,omitempty"`
	Position  *int   `json:"position,omitempty"`
}

// UpdateSessionRequest is the body for PATCH /api/sessions/{id}. Every field
// is optional; only the fields present in the request body are updated.
// Pointer types let the handler distinguish "not supplied" from "set to zero
// value" — important for booleans, where a missing field must not silently
// clear the flag.
//
// Field names mirror session.Field* constants so the handler can dispatch
// directly through session.SetField without a translation table.
type UpdateSessionRequest struct {
	Title           *string `json:"title,omitempty"`
	Notes           *string `json:"notes,omitempty"`
	Color           *string `json:"color,omitempty"`
	Tool            *string `json:"tool,omitempty"`
	ExtraArgs       *string `json:"extraArgs,omitempty"`
	Plugins         *string `json:"plugins,omitempty"`
	Channels        *string `json:"channels,omitempty"`
	SkipPermissions *bool   `json:"skipPermissions,omitempty"`
	AutoMode        *bool   `json:"autoMode,omitempty"`
}

// MoveSessionRequest is the body for POST /api/sessions/{id}/group.
type MoveSessionRequest struct {
	GroupPath string `json:"groupPath"`
}

// SendSessionRequest is the body for POST /api/sessions/{id}/send.
type SendSessionRequest struct {
	Message string `json:"message"`
}

// SendSessionOutputRequest is the body for POST /api/sessions/{id}/send-output.
// It mirrors the TUI `x` flow: source session output is wrapped and submitted
// to another session.
type SendSessionOutputRequest struct {
	TargetSessionID string `json:"targetSessionId"`
}

// SessionOutputResponse is returned by GET /api/sessions/{id}/output.
// It mirrors the TUI copy-output source extraction and is read-only, so web
// copy shortcuts can work from the session list without an active terminal.
type SessionOutputResponse struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title,omitempty"`
	Content   string `json:"content"`
}

// UpdateSessionNotesRequest is the body for POST /api/sessions/{id}/notes.
// It backs the web `e` inline notes editor, distinct from the full PATCH
// settings dialog.
type UpdateSessionNotesRequest struct {
	Notes string `json:"notes"`
}

// UpdateSessionPathsRequest is the body for POST /api/sessions/{id}/paths.
// It mirrors the TUI EditPathsDialog for existing multi-repo sessions.
type UpdateSessionPathsRequest struct {
	Paths []string `json:"paths"`
}

// UpdateSessionResponse confirms a PATCH succeeded. RestartRequired is true
// when any updated field only takes effect on next launch (tool, extra-args,
// plugins, skip-permissions, auto-mode). Clients use it to prompt before/after
// issuing a separate POST .../restart.
type UpdateSessionResponse struct {
	SessionID       string   `json:"sessionId"`
	UpdatedFields   []string `json:"updatedFields"`
	RestartRequired bool     `json:"restartRequired"`
	Warnings        []string `json:"warnings,omitempty"`
}

// SessionActionResponse is returned by session action endpoints.
type SessionActionResponse struct {
	SessionID string         `json:"sessionId"`
	Status    session.Status `json:"status"`
}

// WorktreeFinishRequest is the body for POST /api/sessions/{id}/worktree/finish.
// All fields are optional. Mirrors `agent-deck worktree finish` CLI flags.
// See issue #1126.
type WorktreeFinishRequest struct {
	Into       string `json:"into,omitempty"`
	NoMerge    bool   `json:"noMerge,omitempty"`
	KeepBranch bool   `json:"keepBranch,omitempty"`
	Force      bool   `json:"force,omitempty"`
}

// WorktreeFinishResponse is returned by POST /api/sessions/{id}/worktree/finish.
type WorktreeFinishResponse struct {
	SessionID     string `json:"sessionId"`
	Branch        string `json:"branch"`
	MergedInto    string `json:"mergedInto,omitempty"`
	Merged        bool   `json:"merged"`
	BranchDeleted bool   `json:"branchDeleted"`
}

// SettingsResponse is returned by GET /api/settings.
type SettingsResponse struct {
	Profile       string `json:"profile"`
	ReadOnly      bool   `json:"readOnly"`
	WebMutations  bool   `json:"webMutations"`
	Version       string `json:"version"`
	HubConfigured bool   `json:"hubConfigured"`
	HubAdmin      bool   `json:"hubAdmin"`

	// show_only_installed_tools filter (issue #1259). ToolFilter reports the
	// flag is on; VisibleTools lists the tool names that resolved on PATH (the
	// web dialog intersects its static list against this); ToolFilterFallback
	// reports the empty-fallback so the dialog shows a "showing all" hint. With
	// the flag off ToolFilter is false and the dialog ignores the other fields.
	ToolFilter         bool     `json:"toolFilter"`
	VisibleTools       []string `json:"visibleTools"`
	ToolFilterFallback bool     `json:"toolFilterFallback"`

	// hidden_tools denylist from [ui]. HiddenTools is the configured list;
	// PickerTools is the ordered new-session picker after hidden_tools and
	// show_only_installed_tools ("" mapped to "shell" for web).
	HiddenTools []string `json:"hiddenTools"`
	PickerTools []string `json:"pickerTools"`

	// Link-open policy for the web terminal (issue #1682). TrustedDomains
	// are normalized hosts whose links open without a confirm;
	// ConfirmLinkOpen reports whether every other host still confirms.
	TrustedDomains  []string `json:"trustedDomains"`
	ConfirmLinkOpen bool     `json:"confirmLinkOpen"`
}

// ProfilesResponse is returned by GET /api/profiles.
type ProfilesResponse struct {
	Current  string   `json:"current"`
	Profiles []string `json:"profiles"`
}

// HubNodesAdminResponse is returned by GET /api/hub/nodes. It is backed by
// the configured hub admin node credentials and exposes only node metadata,
// never node tokens or token hashes.
type HubNodesAdminResponse struct {
	Nodes []HubNodeAdmin `json:"nodes"`
}

// HubNodeAdmin mirrors the hub server's node response for web admin controls.
type HubNodeAdmin struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	OS         string     `json:"os,omitempty"`
	Arch       string     `json:"arch,omitempty"`
	Status     string     `json:"status,omitempty"`
	Admin      bool       `json:"admin,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// RenameHubNodeRequest is the body for PATCH /api/hub/nodes/{id}.
type RenameHubNodeRequest struct {
	Name string `json:"name"`
}

// HubInvitesAdminResponse is returned by GET /api/hub/invites. It intentionally
// excludes invite tokens; only explicit create responses include a one-time
// join command.
type HubInvitesAdminResponse struct {
	Invites []HubInviteAdmin `json:"invites"`
}

type HubInviteAdmin struct {
	ID              string     `json:"id,omitempty"`
	NodeName        string     `json:"nodeName"`
	ExpiresAt       time.Time  `json:"expiresAt"`
	ConsumedAt      *time.Time `json:"consumedAt,omitempty"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty"`
	Admin           bool       `json:"admin"`
	CreatedByNodeID string     `json:"createdByNodeId,omitempty"`
	Status          string     `json:"status"`
}

type CreateHubInviteRequest struct {
	NodeName   string `json:"nodeName"`
	TTLSeconds int64  `json:"ttlSeconds,omitempty"`
	Admin      bool   `json:"admin,omitempty"`
}

type CreateHubInviteResponse struct {
	URL         string    `json:"url"`
	InviteToken string    `json:"inviteToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
	JoinCommand string    `json:"joinCommand"`
}

type HubTrustRequestsAdminResponse struct {
	Requests []HubTrustRequestAdmin `json:"requests"`
}

type HubTrustRequestAdmin struct {
	NodeID   string `json:"nodeId"`
	NodeName string `json:"nodeName"`
	Version  string `json:"version,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Status   string `json:"status,omitempty"`
}

// GlobalSearchResponse is returned by GET /api/search/global.
type GlobalSearchResponse struct {
	Query      string               `json:"query"`
	Count      int                  `json:"count"`
	Results    []GlobalSearchResult `json:"results"`
	Tier       string               `json:"tier,omitempty"`
	EntryCount int                  `json:"entryCount,omitempty"`
	Loading    bool                 `json:"loading"`
}

// GlobalSearchResult mirrors the TUI global-search result shape for web.
type GlobalSearchResult struct {
	SessionID  string    `json:"sessionId"`
	Summary    string    `json:"summary,omitempty"`
	Snippet    string    `json:"snippet,omitempty"`
	Content    string    `json:"content,omitempty"`
	CWD        string    `json:"cwd,omitempty"`
	FilePath   string    `json:"filePath,omitempty"`
	ModTime    time.Time `json:"modTime,omitempty"`
	Score      int       `json:"score"`
	MatchCount int       `json:"matchCount"`
}

// SSESessionEvent is emitted on session:created and session:updated events.
type SSESessionEvent struct {
	EventType string       `json:"eventType"`
	Session   *MenuSession `json:"session"`
}

// SSEDeleteEvent is emitted on session:deleted events.
type SSEDeleteEvent struct {
	EventType string `json:"eventType"`
	ID        string `json:"id"`
}

// SSEGroupEvent is emitted on group:created and group:updated events.
type SSEGroupEvent struct {
	EventType string     `json:"eventType"`
	Group     *MenuGroup `json:"group"`
}

// SSEGroupDeleteEvent is emitted on group:deleted events.
type SSEGroupDeleteEvent struct {
	EventType string `json:"eventType"`
	Path      string `json:"path"`
}

// SSECostEvent is emitted on cost:updated events.
type SSECostEvent struct {
	EventType string  `json:"eventType"`
	SessionID string  `json:"sessionId"`
	Cost      float64 `json:"cost"`
}
