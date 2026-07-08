# Hub Session Action Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make hub session rows behave like first-class Agent Deck sessions for create, close, and delete workflows.

**Architecture:** Hub rows stay hub-native: they never route through the legacy SSH remote path. The TUI stores a pending hub target while the new-session dialog is open, then submits a hub `create` command. Destructive actions show confirmation dialogs and then dispatch hub `stop` or `delete` commands.

**Tech Stack:** Go, Bubble Tea TUI, Agent Deck hub command dispatcher, focused `internal/ui` and `internal/hub` tests.

---

## Gap Coverage Report

1. `n` on `ItemTypeHubSession` and `ItemTypeHubGroup` currently quick-creates immediately through hub `create`; expected behavior is to open the full `NewDialog`.
2. `Shift+N` on hub rows should keep quick-create behavior through hub `create`.
3. `handleNewDialogKey` has local and legacy remote submit routes, but no hub pending target, so hub dialog submission needs a new hub-native branch.
4. `D` on `ItemTypeHubSession` currently sends hub `stop` immediately; expected behavior is a close confirmation first.
5. `d` on `ItemTypeHubSession` currently shows an error. The hub action dispatcher lacks a delete/remove action, so true delete needs backend support.
6. Existing hub dialog support already exists for rename and prompt send. Those are not part of this fix except regression coverage.
7. Local-only features such as archive, unarchive, local registry remove, fork, handover, and local tool option toggles remain out of scope unless/until a hub protocol action exists for them.

## Files

- Modify: `internal/ui/home.go`
- Modify: `internal/ui/confirm_dialog.go`
- Modify: `internal/ui/hub_integration_test.go`
- Modify: `internal/hub/actions.go`
- Modify: `internal/hub/actions_test.go`

## Tasks

### Task 1: Hub `n` Opens New Dialog, `Shift+N` Quick Creates

- [ ] Write/keep tests in `internal/ui/hub_integration_test.go` proving lowercase `n` opens `NewDialog`, returns no command, pre-fills group/path/tool from the selected hub item, and sends no hub command.
- [ ] Verify the test fails before production changes with:
  `GOCACHE=/tmp/agent-deck-go-cache GOTMPDIR=/tmp go test ./internal/ui -run 'TestHubSessionNOpensNewSessionDialog|TestHubSessionShiftNQuickCreatesThroughHubCommand' -count=1`
- [ ] Add hub pending target state to `Home`.
- [ ] Add `showHubNewSessionDialog(item session.Item)` that pre-fills node, group, path, and tool without touching legacy remote state.
- [ ] Change lowercase `n` hub branch to call `showHubNewSessionDialog`.
- [ ] Add `handleNewDialogKey` branch that submits pending hub dialog values through hub `create`.
- [ ] Re-run the focused UI tests and confirm they pass.

### Task 2: Hub Close Confirmation

- [ ] Add confirm dialog type and show method for closing hub sessions.
- [ ] Add a UI test proving `D` on a hub session opens the confirmation dialog and sends no command before confirmation.
- [ ] Add a UI test proving confirming that dialog sends hub `stop` with the selected session ID and node ID.
- [ ] Change the `D` hub branch to show the confirmation instead of directly dispatching.
- [ ] Add `confirmAction` handling for confirmed hub close.
- [ ] Run focused UI tests.

### Task 3: Hub Delete Confirmation and Backend Action

- [ ] Add hub action backend method `Delete(ctx, sessionID)` and dispatcher action `"delete"`.
- [ ] Implement `LocalActionBackend.Delete` by loading the target instance, killing it, removing it from storage/group tree using existing durable remove helpers, and saving groups.
- [ ] Add hub action tests proving dispatcher calls `Delete`.
- [ ] Add confirm dialog type and show method for deleting hub sessions.
- [ ] Change `d` on hub sessions to show hub delete confirmation.
- [ ] Add `confirmAction` handling for confirmed hub delete that sends hub `"delete"`.
- [ ] Run focused hub and UI tests.

### Task 4: Verification

- [ ] Run `gofmt` on modified Go files.
- [ ] Run focused test set:
  `GOCACHE=/tmp/agent-deck-go-cache GOTMPDIR=/tmp go test ./internal/ui -run 'TestHubSession(NOpensNewSessionDialog|ShiftNQuickCreatesThroughHubCommand|Close|Delete|StopRestartAndPrompt|Rename|ImportHotkey)' -count=1`
- [ ] Run hub action tests:
  `GOCACHE=/tmp/agent-deck-go-cache GOTMPDIR=/tmp go test ./internal/hub -run 'TestCommandDispatcher.*(Delete|Import|Create|Stop|Restart|Rename)' -count=1`
- [ ] Run full packages if focused tests pass:
  `GOCACHE=/tmp/agent-deck-go-cache GOTMPDIR=/tmp go test ./internal/ui ./internal/hub -count=1`
