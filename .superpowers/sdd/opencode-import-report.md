# OpenCode Existing-Session Import Report

## Changed Files

- `internal/session/opencode_import.go`
- `internal/session/opencode_import_test.go`
- `internal/session/instance.go`
- `cmd/agent-deck/session_cmd.go`
- `cmd/agent-deck/session_import_opencode_test.go`
- `.superpowers/sdd/opencode-import-report.md`

## Test Commands and Results

- `TMPDIR=/var/tmp/agent-deck-tmp GOTMPDIR=/var/tmp/agent-deck-gotmp go test ./internal/session -run 'TestResolveOpenCodeImportTarget|TestNewOpenCodeImportedInstance|TestImportOpenCodeSession' -count=1`
  - Result: pass
- `TMPDIR=/var/tmp/agent-deck-tmp GOTMPDIR=/var/tmp/agent-deck-gotmp go test ./cmd/agent-deck -run 'TestHandleSessionImportOpenCode|TestSessionHelpMentionsImportOpenCode' -count=1`
  - Result: pass
- `TMPDIR=/var/tmp/agent-deck-tmp GOTMPDIR=/var/tmp/agent-deck-gotmp go test ./internal/session -run 'OpenCode|TestResolveOpenCodeImportTarget|TestNewOpenCodeImportedInstance|TestImportOpenCodeSession|TestSetField_OpenCodeSessionID' -count=1`
  - Result: pass
- `TMPDIR=/var/tmp/agent-deck-tmp GOTMPDIR=/var/tmp/agent-deck-gotmp go test ./internal/session ./cmd/agent-deck ./internal/ui -count=1`
  - Result: fail only at known baseline `internal/session TestCanRestartCursor`; `cmd/agent-deck` and `internal/ui` passed.

## Commit

- Implementation commit: `4470eb936885895da480e8966002288621ce8e64`

## Rename Sync

OpenCode rename sync is unsupported/skipped. The installed local CLI exposes `opencode session list` and `opencode session delete`; no stable supported rename command or metadata write mechanism was discovered, and this branch does not read or mutate OpenCode session content.

## TUI Status

TUI saved OpenCode import is implemented in this branch. Pressing `i` opens the source chooser immediately, existing tmux import remains selectable without waiting on `opencode session list`, and the saved OpenCode path surfaces deferred list errors or the empty-list message when selected.
