# Hub Attach Acknowledgement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make opening a waiting hub session persist and publish `idle` from the owner before attaching its terminal.

**Architecture:** Add an owner-side `acknowledge` action and invoke it from the shared hub client before every normal attach. Mutating-command snapshot publication supplies the updated state to all requesters; sandbox tokens bypass acknowledgement and acknowledgement failures remain non-blocking for cross-version compatibility.

**Tech Stack:** Go, SQLite state storage, tmux state tracking, Agent Deck hub WebSocket protocol.

## Global Constraints

- The owner node is authoritative for session status.
- Normal TUI, CLI, web, and window attaches share one client path.
- `tmuxattach:` sandbox/maintenance tokens do not acknowledge parent sessions.
- A failed or unsupported acknowledgement never blocks terminal access.
- Production changes follow strict red-green TDD.

---

### Task 1: Owner acknowledgement action

**Files:**
- Modify: `internal/hub/actions.go`
- Test: `internal/hub/actions_test.go`

**Interfaces:**
- Consumes: `LocalActionBackend.loadSessionData`, `session.Instance.ClearHookStatus`, `statedb.StateDB.SetAcknowledged`.
- Produces: `ActionBackend.Acknowledge(context.Context, string) error` and dispatcher action `acknowledge`.

- [ ] **Step 1: Write the failing dispatcher and persisted-state tests**

Add tests that dispatch `acknowledge` with `session_id=s1` and assert the fake
backend receives `s1`. Add a real-storage test with a waiting Codex instance and
hook file, invoke `LocalActionBackend.Acknowledge`, then assert literal status
`idle`, `acknowledged=true`, and hook-file absence.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `env -u CODEX_HOME go test -count=1 ./internal/hub -run 'Test(CommandDispatcherAcknowledge|LocalActionBackendAcknowledge)'`

Expected: FAIL because `Acknowledge` and the `acknowledge` dispatcher branch do
not exist.

- [ ] **Step 3: Implement the minimal owner action**

Extend `ActionBackend`, dispatch `acknowledge`, and implement
`LocalActionBackend.Acknowledge`. For a waiting instance, clear its hook, call
tmux acknowledgement when available, set `StatusIdle`, save the session, then
persist `acknowledged=true` and the idle status in SQLite. Non-waiting sessions
are unchanged.

- [ ] **Step 4: Publish snapshots for acknowledgement**

Add `acknowledge` to `commandActionPublishesSnapshot` so the owner sends its
fresh snapshot before returning the command result.

- [ ] **Step 5: Run the focused tests and verify GREEN**

Run: `env -u CODEX_HOME go test -count=1 ./internal/hub -run 'Test(CommandDispatcherAcknowledge|LocalActionBackendAcknowledge|ClientOwnerCommandPublishes)'`

Expected: PASS.

### Task 2: Shared pre-attach acknowledgement

**Files:**
- Modify: `internal/hub/client.go`
- Test: `internal/hub/client_test.go`

**Interfaces:**
- Consumes: `Client.Command`, dispatcher action `acknowledge`, and `tmuxAttachTokenPrefix`.
- Produces: a shared best-effort pre-attach helper used by both `Attach` and `OpenAttach`.

- [ ] **Step 1: Write failing attach-sequencing tests**

Add a requester-client test that calls `OpenAttach`, asserts the first outgoing
message is `MsgCommand` with action `acknowledge` and the literal session ID,
returns a successful command result, then asserts the next message is
`MsgAttachOpen`. Add cases proving a failed command still opens and a signed
attach token skips the command.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `env -u CODEX_HOME go test -count=1 ./internal/hub -run 'TestClientOpenAttachAcknowledge'`

Expected: FAIL because `MsgAttachOpen` is currently sent first.

- [ ] **Step 3: Implement the shared pre-attach helper**

Add a client helper that returns immediately for `tmuxattach:` tokens and
otherwise sends `acknowledge` with `session_id`. Log errors without returning
them. Call it from both interactive `Attach` and stream-based `OpenAttach`
after validation and connection checks but before allocating/sending the attach
stream.

- [ ] **Step 4: Run focused and package tests**

Run: `env -u CODEX_HOME go test -count=1 ./internal/hub`

Expected: PASS.

### Task 3: Verify, install, and deploy

**Files:**
- No additional source files.

**Interfaces:**
- Consumes: repository build/install targets and the existing hub Docker image workflow.
- Produces: verified local and remote binaries with matching commit revision.

- [ ] **Step 1: Run repository verification**

Run: `env -u CODEX_HOME GOMODCACHE=/home/lrasmussen/go/pkg/mod GOCACHE=/home/lrasmussen/.cache/go-build go test -count=1 ./...`

Run: `make lint`

Expected: all tests pass and lint reports zero issues.

- [ ] **Step 2: Commit with Conventional Commits**

Commit source and tests as `fix(hub): acknowledge sessions when attached`.

- [ ] **Step 3: Install locally**

Run: `make install-user` and verify the installed binary revision and checksum.

- [ ] **Step 4: Update owner connectors and hub image**

Deploy the verified binary to connected owner nodes that host the affected hub
sessions. Push the commit, wait for the exact-revision hub image workflow, and
recreate the hub service only if the server artifact changed.

- [ ] **Step 5: Reproduce the live transition**

Mark the affected remote session unread/waiting, open it through the hub, and
verify the owner registry and hub snapshot both become `idle`. Confirm public
hub health and automatic connector reconnection after any server recreation.

