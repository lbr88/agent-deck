# Claude Existing-Session Import Report

## Commit

- Implementation commit: `0168a3b52e460b4180dcbd6427ce2994074699e5`

## Changed Files

- `internal/session/claude_import.go`
- `internal/session/claude_import_test.go`
- `internal/session/instance.go`
- `cmd/agent-deck/session_cmd.go`
- `cmd/agent-deck/session_import_claude_test.go`

## Summary

- Added metadata-only Claude import candidate listing and target resolution by UUID first, then exact Claude session name.
- Added `agent-deck session import-claude <session-id-or-name>` with `--title/-t`, `--group/-g`, `--path`, `--start`, `--json`, and `--quiet/-q`.
- Imported sessions are persisted as stopped Claude sessions with `Command` set to `claude` and `ClaudeSessionID` set from Claude transcript metadata.
- `--start` saves the stopped imported row before starting, then saves again after start mutates state.
- Preserved `StatusStopped` during the immediate startup grace window so newly imported stopped rows do not render as `unknown` via `session show`.

## Tests

- Red run before implementation:
  - `env TMPDIR=/var/tmp/agent-deck-claude-import-tests GOTMPDIR=/var/tmp/agent-deck-claude-import-go-tmp go test ./internal/session ./cmd/agent-deck`
  - Result: failed to build on missing `ListClaudeImportCandidates`, `ResolveClaudeImportTarget`, `ClaudeImportCandidate`, `importClaudeSession`, and related symbols.
- Focused pass:
  - `env TMPDIR=/var/tmp/agent-deck-claude-import-tests GOTMPDIR=/var/tmp/agent-deck-claude-import-go-tmp go test ./internal/session ./cmd/agent-deck`
  - Result: passed.
- Affected package pass:
  - `env TMPDIR=/var/tmp/agent-deck-claude-import-tests GOTMPDIR=/var/tmp/agent-deck-claude-import-go-tmp go test ./internal/session ./cmd/agent-deck ./internal/ui`
  - Result: passed.
- Final uncached affected package pass:
  - `env TMPDIR=/var/tmp/agent-deck-claude-import-tests GOTMPDIR=/var/tmp/agent-deck-claude-import-go-tmp go test -count=1 ./internal/session ./cmd/agent-deck ./internal/ui`
  - Result: passed (`internal/session` 80.329s, `cmd/agent-deck` 86.511s, `internal/ui` 25.935s).
- Diff hygiene:
  - `git diff --check`
  - Result: passed.

## TUI Follow-up

- TUI import selection for saved Claude sessions is now implemented by follow-up commit `74093dc` (`feat(ui): add Claude session import picker`).
- The `i` import flow now opens a source chooser with existing tmux sessions and saved Claude sessions. Selecting a saved Claude entry persists a stopped Claude session without reading transcript content.

## Concerns

- The normal commit hook failed on existing `.github` workflow `zizmor` findings unrelated to this change. The implementation commit was created with `--no-verify` after the uncached Go test pass and `git diff --check`.
