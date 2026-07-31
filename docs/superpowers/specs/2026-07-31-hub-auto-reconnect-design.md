# Hub Auto-Reconnect Design

## Problem

Agent Deck's hub client retries after a WebSocket read or write error, but it
does not verify that an apparently open connection is still alive. A connection
left half-open by laptop sleep, Wi-Fi changes, or a broken network path can
therefore remain in the connected state indefinitely and never enter the retry
loop.

Hub session snapshots have a second failure mode. `Home.handleHubSnapshot`
places every snapshot in a bounded channel that is drained by the Bubble Tea
event loop. Attaching to a terminal suspends that loop. Once the channel fills,
new snapshots are discarded, leaving the web interface with stale hub state
until the connector or process is restarted. The running local process produced
1,601 `hub_snapshot_queue_full` warnings during investigation.

## Success Criteria

- A silently broken hub connection is detected and enters the existing
  reconnect loop within roughly 45 seconds under default settings.
- The client remains compatible with existing Gorilla WebSocket hub servers.
- Hub snapshots remain current for web consumers while the TUI event loop is
  suspended or absent.
- Snapshot bursts are coalesced without losing the newest state for any node.
- Existing attach, command, trust, and normal heartbeat behavior remains intact.

## Considered Approaches

### 1. WebSocket ping/pong plus immediate snapshot application

Send standard WebSocket ping control frames on the existing heartbeat cadence
and require inbound activity before a liveness deadline. Gorilla WebSocket
automatically replies to ping frames while its read loop is active, including in
older Agent Deck hub versions. Apply incoming snapshots immediately under the
existing hub-state mutex, then use the Bubble Tea channel only as a best-effort
notification that the derived UI view needs rebuilding.

This is the selected approach. It detects half-open connections without a hub
protocol migration and removes the snapshot channel as a data-loss boundary.

### 2. Application-level heartbeat acknowledgements

Make the server reply to `MsgHeartbeat` and reconnect when acknowledgements stop.
This provides explicit protocol semantics but would make an upgraded client
continually disconnect from an older server that does not send acknowledgements.
It is rejected because mixed-version compatibility is important during fleet
upgrades.

### 3. TCP keepalive only

Rely on operating-system TCP keepalive settings. This requires little code but
commonly takes minutes or hours to identify a dead peer and varies by host. It
does not meet the recovery-time requirement and does not solve dropped
snapshots.

## Design

### Client connection liveness

The client connection will expose a serialized ping write using Gorilla's
control-frame API. `connectOnce` will:

1. establish a read deadline based on a configurable liveness timeout;
2. refresh that deadline whenever a pong is received;
3. send a WebSocket ping alongside the existing JSON heartbeat; and
4. return the ping or read-timeout error to `Connect`, which already performs
   bounded exponential reconnect retries.

The default heartbeat remains 30 seconds. The default liveness timeout is 45
seconds, so a silent failure is detected within approximately 45 seconds of the
last healthy pong. Tests may provide shorter values.

The JSON heartbeat remains in place for protocol compatibility and observability.
No new application envelope type is introduced.

### Server connection liveness

The server will apply the same read-deadline and pong-refresh pattern and send
periodic WebSocket pings to connected nodes. This removes phantom peers from
the online-node count and attach router when a node disappears without a clean
close. Existing clients remain compatible because Gorilla's default ping
handler sends pong frames while their read loop is active.

The ping loop will stop when the handler exits. Ping failures close the
connection so the existing deferred peer cleanup runs.

### Snapshot state delivery

`handleHubSnapshot` will apply the snapshot to `hubSessions` immediately through
the existing mutex-protected `applyHubSnapshot` method. The channel will no
longer carry authoritative state; it will only wake the Bubble Tea loop so it
can rebuild derived rows and publish the web menu snapshot.

Notifications are coalesced: if a notification is already queued, another
snapshot only updates the authoritative map. A full channel is therefore
expected and harmless, and will no longer generate data-loss warnings.

When the TUI is suspended or running headless, web reads continue to observe the
latest mutex-protected hub snapshots. When the TUI resumes, one notification is
enough to rebuild from the already-current map.

## Error Handling

- Ping and read-timeout errors follow the existing reconnect backoff.
- Context cancellation still exits cleanly without another reconnect attempt.
- Server ping failure closes only the affected peer and runs normal route and
  online-state cleanup.
- Invalid or empty-node snapshots retain their existing validation behavior.

## Testing

Regression tests will prove:

- a client reconnects after a clean WebSocket close;
- a client reconnects when the first server connection stays open but stops
  answering ping frames;
- cancellation terminates a liveness-enabled client cleanly;
- the server removes a silent peer after its liveness timeout;
- more snapshots than the notification-channel capacity preserve the newest
  snapshot for every node without blocking;
- snapshots update authoritative hub state even when no Bubble Tea loop drains
  notifications.

Focused package tests, the repository's standard Go test suite, static analysis,
and a race-sensitive test for the changed paths will run before installation.
The verified binary will then be installed locally and the running connector
restarted so the fix is active.
