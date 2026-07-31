# Hub Attach Acknowledgement Design

## Problem

Opening a waiting session through the hub successfully attaches its terminal,
but the session remains `waiting`. Local TUI, CLI, and web attaches acknowledge
the viewed session before opening tmux; hub attaches currently send only the
terminal-open protocol message. The owner therefore keeps the completion hook,
the unacknowledged tmux state, and the persisted `waiting` status, and republishes
that stale state to every hub client.

## Requirements

- Opening a normal hub session that is `waiting` must make the owner persist it
  as `idle` before the terminal stream opens.
- The owner must remain authoritative. Requesters must not maintain a local-only
  status override that a later snapshot can undo.
- TUI, CLI, and web hub attaches must share the same behavior.
- Multi-window attaches must behave like normal session attaches.
- Sandbox-shell attach tokens must not acknowledge the parent session.
- An older owner that does not understand acknowledgement must still allow the
  terminal to open.
- A successful acknowledgement must publish a fresh owner snapshot immediately.

## Considered Approaches

### 1. Central pre-attach hub action (selected)

Before opening a normal terminal stream, the hub client sends an
`acknowledge` command to the owner. The owner's local action backend clears the
persisted hook, marks the tmux state acknowledged, changes `waiting` to `idle`,
persists both status and acknowledgement, and returns. Mutating hub commands
already publish a snapshot before their command result, so the requester sees
the authoritative state before it opens the PTY.

This covers every caller of `Client.Attach` and `Client.OpenAttach` without
duplicating behavior across TUI, CLI, and web handlers.

### 2. Acknowledge inside the tmux attach backend

This is owner-authoritative but mixes terminal transport with session lifecycle
state and makes signed maintenance/sandbox attach tokens easier to mishandle.
It also makes the state change harder to test independently from a real PTY.

### 3. Change only the requester cache

This makes the row appear idle immediately, but the next owner snapshot restores
`waiting`. It does not fix the authoritative state and is rejected.

## Data Flow

1. A requester resolves a node and normal session ID.
2. The shared hub client sends `command(action=acknowledge, session_id=...)`.
3. The owner loads that session from its profile database.
4. If the session is `waiting`, the owner clears its hook record, acknowledges
   tmux, sets `idle`, and persists both status and acknowledgement.
5. The owner publishes a fresh snapshot, then returns the command result.
6. The requester opens the normal attach stream.
7. If step 2 fails because the owner is older or state persistence fails, the
   failure is logged and step 6 still proceeds so viewing a session never breaks.

Signed `tmuxattach:` tokens bypass steps 2-5 because they represent auxiliary
shell streams rather than the viewed parent session.

## Testing

- Dispatcher test: `acknowledge` routes the requested session ID to the backend.
- Owner-state test: acknowledging a stored waiting session removes its hook and
  persists `idle` plus `acknowledged=true`.
- Client sequencing test: normal `OpenAttach` emits `acknowledge` before
  `attach_open`.
- Compatibility test: an acknowledgement error does not prevent `attach_open`.
- Token test: a signed attach token emits `attach_open` without acknowledgement.
- Existing hub, web, TUI, and full Go suites must remain green.

