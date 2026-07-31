# Hub Auto-Reconnect Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make hub clients recover automatically from silent network loss and keep the latest hub session state available while the TUI event loop is suspended.

**Architecture:** Add backward-compatible WebSocket ping/pong liveness on both client and server so half-open sockets become ordinary reconnect errors. Move authoritative hub snapshots out of the bounded Bubble Tea notification channel, and publish a separately locked hub overlay into web menu data so snapshot bursts coalesce without losing state.

**Tech Stack:** Go 1.25.12, Gorilla WebSocket, Bubble Tea, `sync.RWMutex`, Go `testing`/`httptest`.

## Global Constraints

- Default client and server heartbeat interval remains 30 seconds.
- Default liveness timeout is 45 seconds.
- Existing JSON `MsgHeartbeat` messages remain enabled.
- Mixed-version clients and hubs must remain compatible through standard WebSocket control frames.
- No new external dependency or hub application-protocol message type is introduced.
- Snapshot callbacks must never block on Bubble Tea and must retain the newest state for every hub node.
- All commits use Conventional Commits and contain no attribution trailers.

---

### Task 1: Client half-open connection detection

**Files:**
- Modify: `internal/hub/websocket.go`
- Modify: `internal/hub/client.go`
- Test: `internal/hub/client_test.go`

**Interfaces:**
- Consumes: existing `Client.Connect`, `ClientConfig.HeartbeatInterval`, `clientConn.mu`, and reconnect backoff.
- Produces: `ClientConfig.LivenessTimeout time.Duration`, `ClientConfig.livenessTimeout() time.Duration`, `configureWebSocketReadLiveness(*websocket.Conn, time.Duration) error`, and `writeWebSocketPing(*websocket.Conn) error`.

- [ ] **Step 1: Write a failing black-hole reconnect test**

Add `TestClientReconnectsWhenPeerStopsAnsweringPing`. The TLS test server sends
`MsgWelcome`, reads the first connection's hello and snapshot, then deliberately
stops calling `ReadJSON` so Gorilla cannot answer ping frames. A second
connection signals success.

```go
client := NewClient(ClientConfig{
    URL:                 "wss://" + strings.TrimPrefix(server.URL, "https://"),
    NodeID:              "node_1",
    Token:               "node_secret",
    TLSSkipVerify:       true,
    HeartbeatInterval:   20 * time.Millisecond,
    LivenessTimeout:     60 * time.Millisecond,
    SnapshotInterval:    time.Hour,
    ReconnectBaseDelay:  10 * time.Millisecond,
    ReconnectMaxDelay:   10 * time.Millisecond,
}, nil)
go func() { errCh <- client.Connect(ctx) }()

select {
case <-secondConnection:
case <-time.After(time.Second):
    t.Fatal("client did not reconnect after the peer stopped answering pings")
}
```

The handler must keep the first connection open until test cleanup instead of
closing it, which distinguishes this failure from the already-working clean
close path.

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test -count=1 ./internal/hub -run TestClientReconnectsWhenPeerStopsAnsweringPing
```

Expected: compilation fails because `ClientConfig.LivenessTimeout` does not
exist. After adding only the field to make the behavioral test compile, it
times out with `client did not reconnect`.

- [ ] **Step 3: Add shared WebSocket liveness helpers**

In `internal/hub/websocket.go`, retain `hubWriteTimeout` and add:

```go
const (
    defaultHubPingInterval = 30 * time.Second
    defaultHubPongWait     = 45 * time.Second
)

func configureWebSocketReadLiveness(conn *websocket.Conn, pongWait time.Duration) error {
    if conn == nil {
        return nil
    }
    refresh := func() error {
        return conn.SetReadDeadline(time.Now().Add(pongWait))
    }
    if err := refresh(); err != nil {
        return err
    }
    conn.SetPongHandler(func(string) error { return refresh() })
    return nil
}

func writeWebSocketPing(conn *websocket.Conn) error {
    if conn == nil {
        return nil
    }
    return conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(hubWriteTimeout))
}
```

- [ ] **Step 4: Enable liveness in the client**

Add `LivenessTimeout time.Duration` to `ClientConfig`, with:

```go
func (cfg ClientConfig) livenessTimeout() time.Duration {
    if cfg.LivenessTimeout > 0 {
        return cfg.LivenessTimeout
    }
    return defaultHubPongWait
}
```

After dialing and setting the read limit, call
`configureWebSocketReadLiveness(conn, cfg.livenessTimeout())`. Add a serialized
`clientConn.writePing()` method using `clientConn.mu`. On every heartbeat tick,
send the existing JSON heartbeat and then the ping. Return either error so
`Connect` enters its existing retry loop.

- [ ] **Step 5: Verify GREEN and existing behavior**

Run:

```bash
go test -count=1 ./internal/hub -run 'TestClient(ConnectSendsHelloSnapshotAndHeartbeat|ReconnectsWhenPeerStopsAnsweringPing)'
go test -count=1 ./internal/hub
```

Expected: both focused tests and the package pass.

- [ ] **Step 6: Commit the client liveness change**

```bash
git add internal/hub/websocket.go internal/hub/client.go internal/hub/client_test.go
git commit -m "fix(hub): reconnect half-open clients"
```

### Task 2: Server stale-peer cleanup

**Files:**
- Modify: `internal/hub/server.go`
- Test: `internal/hub/server_test.go`

**Interfaces:**
- Consumes: `configureWebSocketReadLiveness`, `writeWebSocketPing`, `hubPeer`,
  `retainNodeConnection`, and `releaseNodeConnection`.
- Produces: `ServerConfig.PingInterval time.Duration`,
  `ServerConfig.LivenessTimeout time.Duration`, and a per-peer ping loop that
  ends with the WebSocket handler.

- [ ] **Step 1: Write a failing silent-peer cleanup test**

Add `TestHubNodeWebSocketSilentPeerBecomesOffline`. Configure short test-only
durations, connect, read the welcome, and then stop reading so the test client
does not answer server pings.

```go
server, err := NewServer(ServerConfig{
    DataDir:         t.TempDir(),
    PingInterval:    20 * time.Millisecond,
    LivenessTimeout: 60 * time.Millisecond,
})
if err != nil {
    t.Fatalf("NewServer: %v", err)
}
// Register node, dial, read welcome, and prove online first.
waitNodeStatus(t, server, "node_1", "online")
// Do not call ReadJSON again; the peer must expire.
waitNodeStatus(t, server, "node_1", "offline")
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test -count=1 ./internal/hub -run TestHubNodeWebSocketSilentPeerBecomesOffline
```

Expected: compilation fails because the server liveness fields do not exist.
After adding only the fields, the test times out waiting for `offline`.

- [ ] **Step 3: Add server liveness configuration and ping loop**

Add config helpers that default to `defaultHubPingInterval` and
`defaultHubPongWait`. In `handleNodeWebSocket`, configure the read deadline
before retaining the peer, then start:

```go
pingDone := make(chan struct{})
defer close(pingDone)
go func() {
    ticker := time.NewTicker(s.cfg.pingInterval())
    defer ticker.Stop()
    for {
        select {
        case <-pingDone:
            return
        case <-ticker.C:
            if err := writeWebSocketPing(conn); err != nil {
                _ = conn.Close()
                return
            }
        }
    }
}()
```

The existing blocking `ReadJSON` returns on its read deadline when no pong
arrives. Existing deferred `releaseNodeConnection` then removes the peer from
online counts, the attach router, and pending command routes.

- [ ] **Step 4: Verify GREEN and overlapping-connection behavior**

Run:

```bash
go test -count=1 ./internal/hub -run 'TestHubNodeWebSocket(SilentPeerBecomesOffline|AcceptsHeartbeat|OverlappingConnectionsKeepNodeOnline)'
go test -race -count=1 ./internal/hub
```

Expected: all tests pass with no race report.

- [ ] **Step 5: Commit the server liveness change**

```bash
git add internal/hub/server.go internal/hub/server_test.go
git commit -m "fix(hub): expire silent server peers"
```

### Task 3: Lossless snapshot state and web overlay

**Files:**
- Modify: `internal/web/memory_menu_data.go`
- Modify: `internal/ui/home.go`
- Test: `internal/web/memory_menu_data_test.go`
- Test: `internal/ui/hub_integration_test.go`

**Interfaces:**
- Consumes: `Home.applyHubSnapshot`, `Home.appendHubWebMenuItems`,
  `MemoryMenuData.LoadMenuSnapshot`, and the existing hub-state mutex.
- Produces: `MemoryMenuData.SetHubSnapshots(active, archived *MenuSnapshot)`,
  `mergeHubMenuSnapshot(base, overlay *MenuSnapshot) *MenuSnapshot`, and
  `Home.publishHubWebMenuSnapshots()`.

- [ ] **Step 1: Write failing web-overlay tests**

Add a test where a local base snapshot contains one local session and an old
hub session. Call `SetHubSnapshots` with a new hub-only projection, then load
the snapshot and assert that the local item remains, the old hub item is gone,
the new item appears once, indices are contiguous, and totals are correct.

```go
store.SetHubSnapshots(
    &MenuSnapshot{
        HubNodes: []HubNode{{ID: "node_1", Name: "laptop"}},
        Items: []MenuItem{{
            Type: MenuItemTypeSession,
            Session: &MenuSession{
                ID: "hub/node_1/new",
                HubNodeID: "node_1",
                Title: "new",
            },
        }},
    },
    &MenuSnapshot{},
)
got, err := store.LoadMenuSnapshot()
// Assert exactly one local item and one "new" hub item; no "old" item.
```

- [ ] **Step 2: Run the web test and verify RED**

Run:

```bash
go test -count=1 ./internal/web -run TestMemoryMenuDataReplacesHubSnapshots
```

Expected: compilation fails because `SetHubSnapshots` is undefined.

- [ ] **Step 3: Implement an atomic hub overlay in `MemoryMenuData`**

Add active and archived hub-only snapshots to the store. `SetHubSnapshots`
clones both while holding `m.mu`, releases the lock, and invokes `onChange`
once. `LoadMenuSnapshot` and `LoadArchivedMenuSnapshot` clone both the base and
the corresponding overlay, then call:

```go
func mergeHubMenuSnapshot(base, overlay *MenuSnapshot) *MenuSnapshot {
    if base == nil {
        base = &MenuSnapshot{}
    }
    merged := cloneMenuSnapshot(base)
    kept := merged.Items[:0]
    for _, item := range merged.Items {
        if (item.Group != nil && item.Group.HubNodeID != "") ||
            (item.Session != nil && item.Session.HubNodeID != "") {
            continue
        }
        kept = append(kept, item)
    }
    merged.Items = kept
    merged.HubNodes = nil
    if overlay != nil {
        merged.HubNodes = append([]HubNode(nil), overlay.HubNodes...)
        merged.Items = append(merged.Items, cloneMenuSnapshot(overlay).Items...)
        if overlay.GeneratedAt.After(merged.GeneratedAt) {
            merged.GeneratedAt = overlay.GeneratedAt
        }
    }
    merged.TotalGroups = 0
    merged.TotalSessions = 0
    for i := range merged.Items {
        merged.Items[i].Index = i
        switch merged.Items[i].Type {
        case MenuItemTypeGroup:
            merged.TotalGroups++
        case MenuItemTypeSession:
            merged.TotalSessions++
        }
    }
    return merged
}
```

`InvalidateCache` clears local base snapshots but retains the hub overlay, so a
headless storage refresh cannot erase current remote state.

- [ ] **Step 4: Write failing snapshot-burst tests**

Update the existing callback test and add
`TestHubSnapshotCallbackCoalescesWithoutLosingLatest`. Do not call `Update` or
drain the channel:

```go
for i := 0; i < 200; i++ {
    h.handleHubSnapshot(hub.NodeSessions{
        Node: hub.Node{ID: "node_server", Name: "server1"},
        Sessions: []hub.SessionInfo{{
            ID: "r1", Title: fmt.Sprintf("snapshot-%03d", i),
        }},
    })
}
snapshots := h.hubSessionSnapshots()
if got := snapshots[0].Sessions[0].Title; got != "snapshot-199" {
    t.Fatalf("latest title = %q, want snapshot-199", got)
}
if got := len(h.hubSnapshotCh); got != 1 {
    t.Fatalf("queued notifications = %d, want 1", got)
}
webSnapshot, err := menuData.LoadMenuSnapshot()
// Assert webSnapshot contains snapshot-199 before any Bubble Tea update.
```

- [ ] **Step 5: Run the UI test and verify RED**

Run:

```bash
go test -count=1 ./internal/ui -run 'TestHubSnapshotCallback(QueuesUpdateAndProjectsRemote|CoalescesWithoutLosingLatest)'
```

Expected: the authoritative map is still empty or stale until queued messages
are drained, and the notification channel contains more than one entry.

- [ ] **Step 6: Apply snapshots immediately and coalesce UI notifications**

Change `hubSnapshotMsg` into an empty wake-up message, size `hubSnapshotCh` to
one, and make `handleHubSnapshot`:

```go
func (h *Home) handleHubSnapshot(snapshot hub.NodeSessions) {
    h.applyHubSnapshot(snapshot)
    h.publishHubWebMenuSnapshots()
    if h.hubSnapshotCh == nil {
        return
    }
    select {
    case h.hubSnapshotCh <- hubSnapshotMsg{}:
    default:
    }
}
```

`publishHubWebMenuSnapshots` builds active and archived hub-only
`web.MenuSnapshot` values with `appendHubWebMenuItems` and passes them to
`SetHubSnapshots`. Remove snapshot application from the `hubSnapshotMsg` update
case and from `drainHubSnapshots`; those paths now only rebuild derived TUI
rows and publish ordinary snapshots after being woken.

- [ ] **Step 7: Verify GREEN and race safety**

Run:

```bash
go test -count=1 ./internal/web -run TestMemoryMenuDataReplacesHubSnapshots
go test -count=1 ./internal/ui -run 'TestHubSnapshotCallback(QueuesUpdateAndProjectsRemote|CoalescesWithoutLosingLatest)'
go test -race -count=1 ./internal/web ./internal/ui
```

Expected: all tests pass and the race detector is clean.

- [ ] **Step 8: Commit the lossless snapshot change**

```bash
git add internal/web/memory_menu_data.go internal/web/memory_menu_data_test.go internal/ui/home.go internal/ui/hub_integration_test.go
git commit -m "fix(hub): preserve snapshots while tui is paused"
```

### Task 4: End-to-end verification and local activation

**Files:**
- Verify: all changed Go files
- Build: `build/agent-deck`
- Install: `/home/lrasmussen/.local/bin/agent-deck`

**Interfaces:**
- Consumes: completed client, server, UI, and web changes.
- Produces: a tested local binary and an active connector running that binary.

- [ ] **Step 1: Format and inspect the final diff**

Run:

```bash
gofmt -w internal/hub/websocket.go internal/hub/client.go internal/hub/client_test.go internal/hub/server.go internal/hub/server_test.go internal/web/memory_menu_data.go internal/web/memory_menu_data_test.go internal/ui/home.go internal/ui/hub_integration_test.go
git diff --check
git status --short
```

Expected: no formatting or whitespace errors and only intended files changed.

- [ ] **Step 2: Run focused and full Go verification**

Run:

```bash
go test -count=1 ./internal/hub ./internal/web ./internal/ui
go test -race -count=1 ./internal/hub ./internal/web ./internal/ui
go test -count=1 ./...
golangci-lint run
govulncheck ./...
```

Expected: every command exits zero. Any known unrelated failure must be
reported with its exact command and output; it must not be described as a pass.

- [ ] **Step 3: Build and install the user binary**

Run:

```bash
make install-user
/home/lrasmussen/.local/bin/agent-deck --version
```

Expected: the build succeeds, the installed file is executable, and its commit
matches the implementation commit.

- [ ] **Step 4: Verify runtime handoff and hub connection**

The running `agent-deck web` process watches its executable for replacement.
After installation, verify that it handed off to the new binary and
reconnected:

```bash
ps -o pid,ppid,lstart,args -C agent-deck
agent-deck hub nodes --json
ss -tnp
```

Expected: the web process remains present after handoff, its hub node reports
online with the new commit/version, and an established TLS connection to the
configured hub exists.

- [ ] **Step 5: Commit any final formatting-only adjustment**

If formatting changed tracked implementation files after the component commits:

```bash
git add internal/hub internal/web internal/ui
git commit -m "style(hub): format reconnect changes"
```

If `git status --short` is clean, do not create an empty commit.
