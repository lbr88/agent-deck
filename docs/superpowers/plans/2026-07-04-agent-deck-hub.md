# Agent Deck Hub Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an encrypted Agent Deck Hub so joined `agent-deck` instances can automatically connect on TUI startup, see each other's sessions inline, and interactively attach to sessions through the hub.

**Architecture:** Add a hub server process and an outbound node/client connection using `wss://` WebSockets. Each joined agent-deck remains authoritative for its own local sessions; the hub stores node membership, tracks live snapshots, and relays terminal/action streams between connected nodes.

**Tech Stack:** Go, `net/http`, `github.com/gorilla/websocket`, `modernc.org/sqlite`, Bubble Tea TUI, existing `internal/session` storage/tmux APIs.

---

## Scope

This plan builds the first useful hub version:

- `agent-deck hub serve`: encrypted hub server with SQLite state.
- `agent-deck hub invite`: creates expiring single-use join tokens.
- `agent-deck hub join`: exchanges an invite token over TLS for node credentials and stores hub config locally.
- Joined TUI instances auto-connect to the hub on startup.
- Connected nodes publish local session snapshots.
- The TUI projects sessions into the normal group tree as `<node> / <group>`, using `local` for the current machine while hub is configured.
- `Enter` on a hub-owned session attaches interactively through the hub relay.
- Basic session actions route through the owner node for hub-owned sessions.

This plan deliberately excludes:

- Browser/web UI for the hub.
- Human auth/RBAC beyond trusted joined-node credentials.
- Plaintext hub traffic.
- Offline command queues.
- File transfer/artifact sync.
- Talkyn/voice integration. The relay protocol leaves room for that integration later.

## File Structure

- Create `internal/hub/protocol.go`: versioned JSON message envelope and typed payloads shared by server, node client, and TUI client.
- Create `internal/hub/protocol_test.go`: JSON compatibility tests for snapshots, commands, and attach frames.
- Create `internal/hub/store.go`: hub SQLite schema, invite token hashing, node registry, latest snapshot persistence, and command/audit persistence.
- Create `internal/hub/store_test.go`: temp-db tests for schema, invite consumption, node upsert, snapshot replacement, and command lifecycle.
- Create `internal/hub/server.go`: HTTP/WebSocket hub runtime, TLS listener setup, node authentication, client authentication by joined-node credential, fanout, and relay routing.
- Create `internal/hub/server_test.go`: in-process TLS server tests for join, connect, snapshot, command relay, and attach relay.
- Create `internal/hub/client.go`: node/client connector used by the TUI and by `agent-deck hub connect`, with reconnect/backoff and local snapshot publishing.
- Create `internal/hub/client_test.go`: connector tests with fake server and fake local session source.
- Create `internal/hub/attach.go`: owner-side local tmux attach bridge and client-side terminal bridge abstractions.
- Create `internal/hub/attach_test.go`: frame routing and detach/resize tests using fake PTYs.
- Create `cmd/agent-deck/hub_cmd.go`: `hub serve`, `hub invite`, `hub join`, `hub nodes`, and `hub connect`.
- Create `cmd/agent-deck/hub_cmd_test.go`: CLI tests for invite/join/config persistence and unsafe plaintext rejection.
- Modify `cmd/agent-deck/main.go`: route `agent-deck hub ...`.
- Modify `internal/session/userconfig.go`: add `[hub]` settings and token-file helpers.
- Modify `internal/ui/home.go`: auto-connect to hub on TUI startup, merge hub snapshots into flat items, and route hub-owned attach/actions through the hub client.
- Create `internal/ui/hub_integration_test.go`: TUI projection and action-routing tests.
- Modify `skills/agent-deck/references/cli-reference.md` and `README.md`: document hub serve/join/connect and the inline `<node> / <group>` TUI model.

## Task 1: Protocol Types

**Files:**
- Create: `internal/hub/protocol.go`
- Create: `internal/hub/protocol_test.go`

- [ ] **Step 1: Write failing protocol round-trip tests**

Create `internal/hub/protocol_test.go`:

```go
package hub

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestEnvelopeRoundTripSessionSnapshot(t *testing.T) {
	in := Envelope{
		Version: ProtocolVersion,
		Type:    MsgSnapshot,
		NodeID:  "node_123",
		Payload: mustRaw(t, SnapshotPayload{
			SentAt: time.Unix(123, 0).UTC(),
			Sessions: []SessionInfo{{
				ID:        "sess_1",
				Title:     "api-fix",
				Tool:      "claude",
				Status:    "waiting",
				GroupPath: "default",
			}},
		}),
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out Envelope
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Version != ProtocolVersion || out.Type != MsgSnapshot || out.NodeID != "node_123" {
		t.Fatalf("round trip = %+v", out)
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(out.Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].Title != "api-fix" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAttachDataIsBase64TerminalBytes(t *testing.T) {
	frame := AttachDataPayload{StreamID: "str_1", DataB64: base64.StdEncoding.EncodeToString([]byte{0x1b, '[', 'A'})}
	data, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if string(data) != "\x1b[A" {
		t.Fatalf("decoded = %q", string(data))
	}
}

func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internal/hub -run 'TestEnvelopeRoundTripSessionSnapshot|TestAttachDataIsBase64TerminalBytes' -count=1
```

Expected: FAIL because `internal/hub` does not exist.

- [ ] **Step 3: Implement protocol types**

Create `internal/hub/protocol.go`:

```go
package hub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const ProtocolVersion = 1

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
	Token    string `json:"token"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

type WelcomePayload struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
}

type SessionInfo struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Tool             string    `json:"tool"`
	Status           string    `json:"status"`
	GroupPath        string    `json:"group_path"`
	ProjectPath      string    `json:"project_path,omitempty"`
	DisplaySessionID string    `json:"display_session_id,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type SnapshotPayload struct {
	SentAt   time.Time     `json:"sent_at"`
	Sessions []SessionInfo `json:"sessions"`
}

type CommandPayload struct {
	CommandID string          `json:"command_id"`
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
	StreamID  string `json:"stream_id"`
	NodeID    string `json:"node_id,omitempty"`
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}

type AttachDataPayload struct {
	StreamID string `json:"stream_id"`
	DataB64  string `json:"data_b64"`
}

func NewAttachData(streamID string, data []byte) AttachDataPayload {
	return AttachDataPayload{StreamID: streamID, DataB64: base64.StdEncoding.EncodeToString(data)}
}

func (p AttachDataPayload) Bytes() ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(p.DataB64)
	if err != nil {
		return nil, fmt.Errorf("decode attach data: %w", err)
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
```

- [ ] **Step 4: Run protocol tests**

Run:

```bash
go test ./internal/hub -run 'TestEnvelopeRoundTripSessionSnapshot|TestAttachDataIsBase64TerminalBytes' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hub/protocol.go internal/hub/protocol_test.go
git commit -m "feat(hub): define hub wire protocol"
```

## Task 2: Hub Store And Invite Tokens

**Files:**
- Create: `internal/hub/store.go`
- Create: `internal/hub/store_test.go`

- [ ] **Step 1: Write failing store tests**

Create `internal/hub/store_test.go`:

```go
package hub

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreInviteConsumeIsSingleUse(t *testing.T) {
	store := openTestStore(t)
	token, err := store.CreateInvite("laptop", time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	invite, err := store.ConsumeInvite(token)
	if err != nil {
		t.Fatalf("ConsumeInvite first: %v", err)
	}
	if invite.NodeName != "laptop" {
		t.Fatalf("NodeName = %q", invite.NodeName)
	}
	if _, err := store.ConsumeInvite(token); err == nil {
		t.Fatal("second ConsumeInvite succeeded, want single-use failure")
	}
}

func TestStoreSnapshotReplacesLatest(t *testing.T) {
	store := openTestStore(t)
	node, err := store.UpsertNode("node_1", "laptop", "secret_hash", "1.0.0", "linux", "amd64")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	first := SnapshotPayload{SentAt: time.Now(), Sessions: []SessionInfo{{ID: "s1", Title: "old", Status: "waiting"}}}
	second := SnapshotPayload{SentAt: time.Now(), Sessions: []SessionInfo{{ID: "s2", Title: "new", Status: "running"}}}
	if err := store.ReplaceSnapshot(node.ID, first); err != nil {
		t.Fatalf("ReplaceSnapshot first: %v", err)
	}
	if err := store.ReplaceSnapshot(node.ID, second); err != nil {
		t.Fatalf("ReplaceSnapshot second: %v", err)
	}
	got, err := store.LatestSessions()
	if err != nil {
		t.Fatalf("LatestSessions: %v", err)
	}
	if len(got) != 1 || len(got[0].Sessions) != 1 || got[0].Sessions[0].ID != "s2" {
		t.Fatalf("sessions = %+v", got)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internal/hub -run 'TestStoreInviteConsumeIsSingleUse|TestStoreSnapshotReplacesLatest' -count=1
```

Expected: FAIL because `Store` is undefined.

- [ ] **Step 3: Implement the SQLite store**

Create `internal/hub/store.go` with:

- `OpenStore(path string) (*Store, error)`
- `Close() error`
- `CreateInvite(nodeName string, ttl time.Duration) (plainToken string, err error)`
- `ConsumeInvite(plainToken string) (Invite, error)`
- `UpsertNode(id, name, tokenHash, version, osName, arch string) (Node, error)`
- `AuthenticateNode(nodeID, plainToken string) (Node, error)`
- `MarkNodeOnline(nodeID string) error`
- `MarkNodeOffline(nodeID string) error`
- `ReplaceSnapshot(nodeID string, snapshot SnapshotPayload) error`
- `LatestSessions() ([]NodeSessions, error)`

Use `crypto/rand` for tokens and `crypto/sha256` for token hashes:

```go
func newSecret(prefix string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
```

Create these tables in `migrate()`:

```sql
CREATE TABLE IF NOT EXISTS invites (
  token_hash TEXT PRIMARY KEY,
  node_name TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER
);
CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  version TEXT,
  os TEXT,
  arch TEXT,
  status TEXT NOT NULL DEFAULT 'offline',
  last_seen_at INTEGER
);
CREATE TABLE IF NOT EXISTS snapshots (
  node_id TEXT PRIMARY KEY,
  sent_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  metadata_json TEXT
);
```

Use `modernc.org/sqlite` via blank import, matching `internal/statedb`.

- [ ] **Step 4: Run store tests**

Run:

```bash
go test ./internal/hub -run 'TestStoreInviteConsumeIsSingleUse|TestStoreSnapshotReplacesLatest' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hub/store.go internal/hub/store_test.go
git commit -m "feat(hub): persist nodes invites and snapshots"
```

## Task 3: Hub CLI, TLS Server, And Join Flow

**Files:**
- Create: `cmd/agent-deck/hub_cmd.go`
- Create: `cmd/agent-deck/hub_cmd_test.go`
- Create: `internal/hub/server.go`
- Create: `internal/hub/server_test.go`
- Modify: `cmd/agent-deck/main.go`
- Modify: `internal/session/userconfig.go`

- [ ] **Step 1: Add failing CLI routing/config tests**

Create `cmd/agent-deck/hub_cmd_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHubCommandIsRoutedFromMain(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(data), `case "hub":`) || !strings.Contains(string(data), "handleHub(") {
		t.Fatalf("main.go must route agent-deck hub commands")
	}
}

func TestSaveHubJoinConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfg := &session.UserConfig{}
	tokenPath := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	if err := saveHubJoinConfig(cfg, hubJoinResult{
		URL:       "wss://hub.local:8421",
		NodeID:    "node_abc",
		NodeName:  "laptop",
		NodeToken: "adhn_secret",
		TokenPath: tokenPath,
	}); err != nil {
		t.Fatalf("saveHubJoinConfig: %v", err)
	}
	if cfg.Hub.URL != "wss://hub.local:8421" || cfg.Hub.NodeName != "laptop" || !cfg.Hub.AutoConnect {
		t.Fatalf("hub config = %+v", cfg.Hub)
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if strings.TrimSpace(string(data)) != "adhn_secret" {
		t.Fatalf("token file = %q", string(data))
	}
}
```

- [ ] **Step 2: Run CLI tests and verify they fail**

Run:

```bash
go test ./cmd/agent-deck -run 'TestHubCommandIsRoutedFromMain|TestSaveHubJoinConfig' -count=1
```

Expected: FAIL because hub command/config does not exist.

- [ ] **Step 3: Add hub user config**

Modify `internal/session/userconfig.go`:

```go
type UserConfig struct {
	// existing fields...
	Hub HubSettings `toml:"hub,omitempty"`
}

type HubSettings struct {
	URL            string `toml:"url,omitempty"`
	NodeID         string `toml:"node_id,omitempty"`
	NodeName       string `toml:"node_name,omitempty"`
	TokenFile      string `toml:"token_file,omitempty"`
	AutoConnect    bool   `toml:"auto_connect,omitempty"`
	TLSSkipVerify  bool   `toml:"tls_skip_verify,omitempty"`
	CAPemFile      string `toml:"ca_pem_file,omitempty"`
	ServerName     string `toml:"server_name,omitempty"`
}

func (h HubSettings) Enabled() bool {
	return strings.TrimSpace(h.URL) != "" && strings.TrimSpace(h.NodeID) != ""
}
```

Keep `TLSSkipVerify` for explicit lab use only. CLI help must describe it as unsafe.

- [ ] **Step 4: Implement `hub serve`, `hub invite`, `hub join`, and `hub nodes` CLI skeleton**

Create `cmd/agent-deck/hub_cmd.go` with:

- `handleHub(profile string, args []string)`
- `handleHubServe(args []string)`
- `handleHubInvite(args []string)`
- `handleHubJoin(args []string)`
- `handleHubNodes(args []string)`
- `saveHubJoinConfig(config *session.UserConfig, result hubJoinResult) error`

Add the route in `cmd/agent-deck/main.go`:

```go
case "hub":
	handleHub(profile, args[1:])
```

The join command must reject plaintext URLs:

```go
if !strings.HasPrefix(strings.ToLower(url), "wss://") {
	return fmt.Errorf("hub join requires wss://; use TLS even for local deployments")
}
```

- [ ] **Step 5: Implement TLS hub server start**

Create `internal/hub/server.go`:

```go
type ServerConfig struct {
	ListenAddr string
	DataDir    string
	CertFile   string
	KeyFile    string
}

type Server struct {
	cfg   ServerConfig
	store *Store
}

func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:8421"
	}
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("hub data dir is required")
	}
	store, err := OpenStore(filepath.Join(cfg.DataDir, "hub.db"))
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, store: store}, nil
}
```

Expose:

- `GET /healthz`
- `POST /api/join` for invite exchange
- `GET /ws/node` for authenticated node/client WebSockets

Use `http.Server.ListenAndServeTLS(certFile, keyFile)`. If cert/key are missing, return a clear error that `agent-deck hub serve` requires `--tls-cert` and `--tls-key`.

- [ ] **Step 6: Run CLI/server tests**

Run:

```bash
go test ./cmd/agent-deck ./internal/hub -run 'TestHub|TestSaveHubJoinConfig|Test.*Join|Test.*Serve' -count=1
```

Expected: PASS for tests added so far.

- [ ] **Step 7: Commit**

```bash
git add cmd/agent-deck/main.go cmd/agent-deck/hub_cmd.go cmd/agent-deck/hub_cmd_test.go internal/session/userconfig.go internal/hub/server.go internal/hub/server_test.go
git commit -m "feat(hub): add tls hub server and join commands"
```

## Task 4: Node Connector And Snapshot Publishing

**Files:**
- Create: `internal/hub/client.go`
- Create: `internal/hub/client_test.go`
- Modify: `cmd/agent-deck/hub_cmd.go`

- [ ] **Step 1: Write failing client snapshot test**

Create `internal/hub/client_test.go`:

```go
package hub

import (
	"context"
	"testing"
	"time"
)

type fakeSessionSource struct {
	sessions []SessionInfo
}

func (f fakeSessionSource) Snapshot(context.Context) (SnapshotPayload, error) {
	return SnapshotPayload{SentAt: time.Unix(456, 0).UTC(), Sessions: f.sessions}, nil
}

func TestClientBuildsSnapshotFromSource(t *testing.T) {
	src := fakeSessionSource{sessions: []SessionInfo{{ID: "s1", Title: "worker", GroupPath: "default"}}}
	c := &Client{source: src}
	snap, err := c.buildSnapshot(context.Background())
	if err != nil {
		t.Fatalf("buildSnapshot: %v", err)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].Title != "worker" {
		t.Fatalf("snapshot = %+v", snap)
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
go test ./internal/hub -run TestClientBuildsSnapshotFromSource -count=1
```

Expected: FAIL because `Client` and `SessionSource` do not exist.

- [ ] **Step 3: Implement client connector**

Create `internal/hub/client.go` with:

```go
type SessionSource interface {
	Snapshot(context.Context) (SnapshotPayload, error)
}

type ClientConfig struct {
	URL           string
	NodeID        string
	NodeName      string
	Token         string
	Version       string
	TLSSkipVerify bool
	CAPemFile     string
	ServerName    string
}

type Client struct {
	cfg    ClientConfig
	source SessionSource
}

func NewClient(cfg ClientConfig, source SessionSource) *Client {
	return &Client{cfg: cfg, source: source}
}

func (c *Client) buildSnapshot(ctx context.Context) (SnapshotPayload, error) {
	if c.source == nil {
		return SnapshotPayload{SentAt: time.Now().UTC()}, nil
	}
	return c.source.Snapshot(ctx)
}
```

Then implement:

- `Connect(ctx context.Context) error`
- reconnect with exponential backoff while context is alive
- send `MsgHello`
- publish `MsgSnapshot` immediately and then on a ticker
- send `MsgHeartbeat`
- dispatch `MsgCommand` and attach frames to handlers added in later tasks

- [ ] **Step 4: Add local session source**

Add a source implementation in `internal/hub/client.go` or `internal/hub/session_source.go`:

```go
type LocalSessionSource struct {
	Profile string
}
```

It loads local sessions through existing `session.NewStorageWithProfile` / load helpers and maps each `*session.Instance` into `hub.SessionInfo`. Use `inst.DisplaySessionID()` for `DisplaySessionID`.

- [ ] **Step 5: Wire `hub connect`**

In `cmd/agent-deck/hub_cmd.go`, implement:

```bash
agent-deck hub connect
```

It reads `[hub]`, reads the token file, creates a `hub.Client`, and blocks until interrupted.

- [ ] **Step 6: Run client tests**

Run:

```bash
go test ./internal/hub ./cmd/agent-deck -run 'TestClient|TestHubConnect|TestLocalSessionSource' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/hub/client.go internal/hub/client_test.go cmd/agent-deck/hub_cmd.go
git commit -m "feat(hub): connect nodes and publish snapshots"
```

## Task 5: TUI Auto-Connect And Inline Hub Projection

**Files:**
- Modify: `internal/ui/home.go`
- Create: `internal/ui/hub_integration_test.go`
- Modify: `internal/session/instance.go` or `internal/session/discovery.go` only if `session.Item` needs a hub owner field.

- [ ] **Step 1: Write failing projection tests**

Create `internal/ui/hub_integration_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHubConfiguredPrefixesLocalGroupsWithLocal(t *testing.T) {
	h := NewHomeWithInstances([]*session.Instance{
		{ID: "s1", Title: "api", GroupPath: "default", Tool: "claude"},
	})
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.rebuildFlatItems()
	got := h.View()
	if !strings.Contains(got, "local / default") {
		t.Fatalf("view missing local-prefixed group:\n%s", got)
	}
}

func TestHubRemoteSnapshotAppearsAsNodePrefixedGroup(t *testing.T) {
	h := NewHomeWithInstances(nil)
	h.hubConfigured = true
	h.hubLocalNodeName = "local"
	h.hubSessions = map[string]hub.NodeSessions{
		"node_server": {
			NodeID: "node_server",
			NodeName: "server1",
			Sessions: []hub.SessionInfo{{ID: "r1", Title: "deploy", Tool: "claude", Status: "waiting", GroupPath: "default"}},
		},
	}
	h.rebuildFlatItems()
	got := h.View()
	if !strings.Contains(got, "server1 / default") || !strings.Contains(got, "deploy") {
		t.Fatalf("view missing remote hub session:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internal/ui -run 'TestHubConfiguredPrefixesLocalGroupsWithLocal|TestHubRemoteSnapshotAppearsAsNodePrefixedGroup' -count=1
```

Expected: FAIL because hub fields/projection do not exist.

- [ ] **Step 3: Add TUI hub state**

Modify `internal/ui/home.go`:

```go
hubConfigured     bool
hubLocalNodeID    string
hubLocalNodeName  string
hubClient         hubClientAPI
hubSessionsMu     sync.RWMutex
hubSessions       map[string]hub.NodeSessions
hubStatus         string
```

Define a small `hubClientAPI` interface in `internal/ui/home.go` so tests can inject a fake:

```go
type hubClientAPI interface {
	Attach(ctx context.Context, nodeID, sessionID string, size hub.TerminalSize) (hub.AttachStream, error)
	SendCommand(ctx context.Context, nodeID, action string, payload any) (json.RawMessage, error)
	Close() error
}
```

- [ ] **Step 4: Auto-connect on TUI startup**

During `NewHome` setup, load `session.UserConfig.Hub`. If `Enabled()` and `AutoConnect` are true:

- set `hubConfigured = true`
- set `hubLocalNodeName` to config node name, falling back to `"local"`
- start a background hub client command
- keep local TUI usable if connection fails
- surface status as `hub offline` / `hub connected`

- [ ] **Step 5: Project hub sessions into normal group tree**

When hub is configured:

- local group display path becomes `<localNodeName> / <groupPath>`
- remote hub session display path becomes `<nodeName> / <groupPath>`
- underlying local session `GroupPath` remains unchanged
- hub sessions are not saved into local storage

If `session.Item` cannot represent hub-owned sessions without overloading SSH remote fields, add explicit fields:

```go
HubNodeID      string
HubNodeName    string
HubSession     *hub.SessionInfo
```

Use `ItemTypeRemoteSession` only if the existing action/rendering code can stay correct; otherwise add `ItemTypeHubSession` and `ItemTypeHubGroup`.

- [ ] **Step 6: Run TUI projection tests**

Run:

```bash
go test ./internal/ui -run 'TestHubConfiguredPrefixesLocalGroupsWithLocal|TestHubRemoteSnapshotAppearsAsNodePrefixedGroup|TestIssue1066|TestIssue1170' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/home.go internal/ui/hub_integration_test.go internal/session/instance.go internal/session/discovery.go
git commit -m "feat(tui): show hub sessions inline by node"
```

## Task 6: Interactive Attach Relay

**Files:**
- Create: `internal/hub/attach.go`
- Create: `internal/hub/attach_test.go`
- Modify: `internal/hub/client.go`
- Modify: `internal/hub/server.go`
- Modify: `internal/ui/home.go`

- [ ] **Step 1: Write failing attach stream tests**

Create `internal/hub/attach_test.go`:

```go
package hub

import (
	"context"
	"testing"
)

func TestAttachRouterForwardsInputToOwnerAndOutputToRequester(t *testing.T) {
	router := NewAttachRouter()
	requester := newFakePeer("laptop")
	owner := newFakePeer("workstation")
	router.Register(requester)
	router.Register(owner)

	if err := router.Open(context.Background(), requester.ID, owner.ID, AttachOpenPayload{
		StreamID: "stream_1",
		NodeID: "workstation",
		SessionID: "sess_1",
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := owner.popType(); got != MsgAttachOpen {
		t.Fatalf("owner first msg = %s", got)
	}
	if err := router.ForwardFromRequester("laptop", NewAttachData("stream_1", []byte("hello"))); err != nil {
		t.Fatalf("ForwardFromRequester: %v", err)
	}
	if got := owner.popType(); got != MsgAttachData {
		t.Fatalf("owner second msg = %s", got)
	}
	if err := router.ForwardFromOwner("workstation", NewAttachData("stream_1", []byte("world"))); err != nil {
		t.Fatalf("ForwardFromOwner: %v", err)
	}
	if got := requester.popType(); got != MsgAttachData {
		t.Fatalf("requester msg = %s", got)
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
go test ./internal/hub -run TestAttachRouterForwardsInputToOwnerAndOutputToRequester -count=1
```

Expected: FAIL because attach router does not exist.

- [ ] **Step 3: Implement attach router**

In `internal/hub/attach.go`, implement:

- `type AttachRouter`
- `Register(peer Peer)`
- `Unregister(nodeID string)`
- `Open(ctx, requesterNodeID, ownerNodeID string, payload AttachOpenPayload) error`
- `ForwardFromRequester(requesterNodeID string, payload AttachDataPayload) error`
- `ForwardFromOwner(ownerNodeID string, payload AttachDataPayload) error`
- close cleanup when either side disconnects

Keep the router in memory. Do not persist attach streams.

- [ ] **Step 4: Implement owner-side attach handling**

In `internal/hub/client.go`, when a node receives `MsgAttachOpen` for one of its local sessions:

- resolve the session ID locally
- start a local tmux attach bridge
- forward terminal output as `MsgAttachData`
- write incoming `MsgAttachData` bytes to the PTY
- apply `MsgAttachResize`
- close the stream on detach or error

Use a small interface around PTY creation so tests can fake it:

```go
type AttachBackend interface {
	Open(ctx context.Context, sessionID string, size TerminalSize) (AttachStream, error)
}
```

- [ ] **Step 5: Route `Enter` on hub sessions through attach relay**

In `internal/ui/home.go`, update the selected-session attach path:

- local item: existing attach behavior
- SSH remote item: existing SSH remote attach behavior
- hub item: `hubClient.Attach(ctx, nodeID, sessionID, currentTerminalSize)`

Reuse existing terminal raw-mode attach helpers where practical. The UI exits attach mode back to the TUI without killing the owner session.

- [ ] **Step 6: Run attach tests**

Run:

```bash
go test ./internal/hub ./internal/ui -run 'TestAttach|TestHub.*Attach|TestCuratedFooterRemoteSessionShowsAttach' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/hub/attach.go internal/hub/attach_test.go internal/hub/client.go internal/hub/server.go internal/ui/home.go
git commit -m "feat(hub): relay interactive session attach"
```

## Task 7: Hub-Owned Session Actions

**Files:**
- Modify: `internal/hub/client.go`
- Modify: `internal/ui/home.go`
- Create: `internal/hub/actions_test.go`
- Create: `internal/ui/hub_actions_test.go`

- [ ] **Step 1: Write failing action relay tests**

Create `internal/hub/actions_test.go`:

```go
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
		Action: "unknown",
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
		Action: "send",
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("Dispatch send: %v", err)
	}
	if fake.sentSessionID != "s1" || fake.sentMessage != "run tests" {
		t.Fatalf("fake = %+v", fake)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internal/hub -run 'TestCommandDispatcher' -count=1
```

Expected: FAIL because command dispatcher does not exist.

- [ ] **Step 3: Implement command dispatcher**

In `internal/hub/client.go` or `internal/hub/actions.go`, implement:

```go
type ActionBackend interface {
	Send(ctx context.Context, sessionID, message string) error
	Start(ctx context.Context, sessionID string) error
	Stop(ctx context.Context, sessionID string) error
	Restart(ctx context.Context, sessionID string) error
	Rename(ctx context.Context, sessionID, title string) error
	Create(ctx context.Context, req CreateSessionRequest) (string, error)
}
```

Supported action names:

- `send`
- `start`
- `stop`
- `restart`
- `rename`
- `create`

Return structured errors for unknown actions and malformed payloads.

- [ ] **Step 4: Implement local action backend**

The backend should call existing local session functions instead of shelling out where practical. If a handler is only available in `cmd/agent-deck`, extract a reusable session-layer helper before using it from hub code.

For `send`, call the same delivery path used by `agent-deck session send`.

- [ ] **Step 5: Route TUI actions for hub sessions**

In `internal/ui/home.go`, when the selected row is hub-owned:

- `R`: send `restart`
- delete/close actions: send `stop` for close only; keep destructive delete out of v1
- insert/send mode: send `send` or attach stream depending on current input mode
- rename: send `rename`
- new session under a hub-prefixed group: send `create` to that node

- [ ] **Step 6: Run action tests**

Run:

```bash
go test ./internal/hub ./internal/ui -run 'TestCommandDispatcher|TestHub.*Action|Test.*RemoteSession' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/hub/client.go internal/hub/actions_test.go internal/ui/home.go internal/ui/hub_actions_test.go
git commit -m "feat(hub): relay basic session actions"
```

## Task 8: Documentation, Docker, And Verification

**Files:**
- Modify: `README.md`
- Modify: `skills/agent-deck/references/cli-reference.md`
- Add: `docs/AGENT-DECK-HUB.md`
- Add: `deploy/hub/docker-compose.yml`
- Add: `deploy/hub/Dockerfile`

- [ ] **Step 1: Add user-facing hub guide**

Create `docs/AGENT-DECK-HUB.md` with:

```markdown
# Agent Deck Hub

Agent Deck Hub connects multiple trusted agent-deck instances through one encrypted relay. Each joined agent-deck owns its local sessions and connects outbound to the hub. Any connected TUI can see and attach to sessions from other connected nodes.

## Start A Hub

```bash
agent-deck hub serve --listen :8421 --data ~/.local/share/agent-deck-hub --tls-cert cert.pem --tls-key key.pem
```

Hub traffic uses `wss://`. Plaintext hub connections are refused.

## Join A Node

On the hub host:

```bash
agent-deck hub invite laptop
```

On the joining machine:

```bash
agent-deck hub join wss://hub.example:8421 --token <token>
```

After join, `agent-deck` auto-connects to the hub when the TUI starts.

## TUI Layout

When hub is configured, all groups are shown with their owner:

```text
local / default
server1 / default
macbook / experiments
```

Selecting a session and pressing Enter attaches through the hub relay.
```
```

- [ ] **Step 2: Add Docker deployment files**

Create `deploy/hub/Dockerfile`:

```dockerfile
FROM gcr.io/distroless/base-debian12
COPY agent-deck /usr/local/bin/agent-deck
EXPOSE 8421
ENTRYPOINT ["/usr/local/bin/agent-deck", "hub", "serve"]
```

Create `deploy/hub/docker-compose.yml`:

```yaml
services:
  agent-deck-hub:
    image: agent-deck-hub:local
    command:
      - --listen=:8421
      - --data=/data
      - --tls-cert=/certs/tls.crt
      - --tls-key=/certs/tls.key
    ports:
      - "8421:8421"
    volumes:
      - ./data:/data
      - ./certs:/certs:ro
    restart: unless-stopped
```

- [ ] **Step 3: Update README and CLI reference**

Add a concise README section:

- hub is an encrypted relay, not a web app
- joined nodes are trusted
- nodes auto-connect on TUI startup
- group display uses `<node> / <group>`
- no SSH between nodes is required

Add CLI reference entries for:

- `hub serve`
- `hub invite`
- `hub join`
- `hub connect`
- `hub nodes`

- [ ] **Step 4: Run verification**

Run:

```bash
go test ./internal/hub ./cmd/agent-deck ./internal/ui -run 'TestHub|TestCommandDispatcher|TestAttach|TestHubConfigured|TestHubRemote' -count=1
git diff --check
```

Expected: tests PASS and `git diff --check` prints no output.

- [ ] **Step 5: Commit**

```bash
git add README.md skills/agent-deck/references/cli-reference.md docs/AGENT-DECK-HUB.md deploy/hub/Dockerfile deploy/hub/docker-compose.yml
git commit -m "docs(hub): document encrypted hub deployment"
```

## Full Verification

Run before merging:

```bash
go test ./internal/hub ./cmd/agent-deck ./internal/ui ./internal/session -count=1
go test ./... -run 'Hub|Remote|SessionDataService|Attach|CommandDispatcher' -count=1
git diff --check
```

Manual smoke test:

```bash
agent-deck hub serve --listen 127.0.0.1:8421 --data /tmp/agent-deck-hub --tls-cert /tmp/hub.crt --tls-key /tmp/hub.key
agent-deck hub invite laptop --data /tmp/agent-deck-hub
agent-deck hub join wss://127.0.0.1:8421 --token <printed-token> --tls-skip-verify
agent-deck
```

Expected manual result:

- TUI starts normally.
- Status shows hub connected.
- Local groups render as `local / <group>`.
- A second joined checkout appears as `<node> / <group>`.
- Pressing Enter on a hub-owned session attaches through the relay.
- Detaching leaves the owner session alive.

## Self-Review

- Spec coverage: central hub server, token join, encrypted traffic, auto-connect on TUI startup, trusted joined nodes, inline node/group display, interactive attach relay, and basic actions are covered by Tasks 1-8.
- Placeholder scan: this plan does not rely on deferred filler instructions or undefined future hooks. Out-of-scope items are named explicitly in Scope.
- Type consistency: protocol names use `Envelope`, `SessionInfo`, `SnapshotPayload`, `CommandPayload`, `Attach*Payload`; store names use `Store`, `Node`, `Invite`, `NodeSessions`; TUI projection uses hub node/session ownership consistently.
