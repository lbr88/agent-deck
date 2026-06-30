# Codex Import And Rename Sync Design

## Context

Agent Deck already manages Codex-compatible sessions with `Instance.Tool`,
`Instance.CodexSessionID`, and the existing `codex resume <session-id>` launch
path. The current gap is that a Codex conversation started outside Agent Deck
cannot be brought into the Agent Deck registry through a first-class workflow,
and renaming an Agent Deck session only updates Agent Deck/tmux metadata, not
the Codex session name shown by `codex resume`.

The local Codex CLI (`codex-cli 0.142.4`) exposes `resume [SESSION_ID]`,
`resume [SESSION_NAME]`, `fork [SESSION_ID]`, archive/delete/unarchive, but no
rename subcommand. Codex keeps an append-style `session_index.jsonl` in
`CODEX_HOME` with records shaped like:

```json
{"id":"019f17d9-ffa7-77d3-b41f-71b495093c55","thread_name":"ai-users","updated_at":"2026-06-30T09:26:39.683524863Z"}
```

Observed duplicate IDs with later `thread_name` values show that appending a
new record is the least invasive way to update a displayed Codex session name.

## Goals

- Add a CLI workflow for importing an existing Codex session into Agent Deck.
- Add a TUI workflow for importing an existing Codex session into Agent Deck.
- Keep import and rename logic on shared Codex index helpers so CLI, TUI, and
  tests use one interpretation of Codex metadata.
- When an Agent Deck Codex-compatible session is renamed, update Codex's
  session index to the same title without rewriting Codex transcript files.
- Preserve Agent Deck's existing identity rule: rollout files may validate that
  an ID exists, but automatic disk scans must not silently rebind an unrelated
  Agent Deck instance.

## Non-Goals

- Do not parse or rewrite Codex transcript contents.
- Do not add a general Codex transcript browser beyond the import picker.
- Do not make disk scans authoritative for existing Agent Deck identity binding.
- Do not require a running Codex process to import an existing saved session.
- Do not block an Agent Deck rename if Codex index sync fails.

## Shared Codex Index Layer

Add a focused helper in `internal/session` that owns these operations:

- Resolve `CODEX_HOME` using the same command/config rules already used by
  `Instance.getCodexHomeDir()`.
- Read `session_index.jsonl` as JSONL.
- Collapse duplicate IDs so the latest record for each ID wins.
- Resolve a user-supplied import target as UUID first, then as session name.
- Validate that a resolved UUID has a matching rollout under
  `CODEX_HOME/sessions/YYYY/MM/DD/rollout-*-<uuid>.jsonl`.
- Append a new index record for rename sync.

The helper must tolerate a missing index file by returning an empty list and a
clear "not found" error for import resolution. Malformed JSONL lines should be
reported with line numbers for explicit import operations. Rename sync should
return an error to the caller so UI surfaces can warn, but should never mutate
Agent Deck state back after the title was accepted.

## Import Existing Codex Session

### CLI

Add:

```bash
agent-deck session import-codex <session-id-or-name> [options]
```

Options:

- `--title`, `-t`: Agent Deck title. Defaults to the Codex `thread_name`; if
  empty, fall back to the UUID prefix.
- `--group`, `-g`: Agent Deck group path. Defaults to `default`.
- `--path`: Project path for the Agent Deck session. Defaults to the current
  working directory.
- `--command`, `-c`: Codex command. Defaults to `codex`.
- `--start`: Start the imported session immediately via existing
  `codex resume <uuid>` dispatch.
- `--json`: Return structured output.
- `--quiet`, `-q`: Minimal output.

The command creates an Agent Deck session with `Tool=codex`,
`Command=<command>`, `ProjectPath=<path>`, and `CodexSessionID=<uuid>`. It must
refuse ambiguous name matches and list the matching IDs/timestamps so the user
can import by UUID.

### TUI

Add an import path for Codex from the TUI. It can live in the new-session flow
or command palette, but it must be reachable without dropping to the shell.
The picker lists recent Codex index entries with title, last updated time, and
short UUID. Selecting an entry opens the existing session creation/editing
flow prefilled with:

- title from Codex index
- tool `codex`
- command `codex`
- current/default group
- current/default path
- resolved `CodexSessionID`

Submitting creates the Agent Deck row. Starting immediately can reuse the
normal start action after creation; the import dialog does not need to start by
default.

## Rename Sync

Keep `session.SetField(FieldTitle, ...)` as the single source of truth for
renames. After the current title mutation, auto-name clearing, title locking,
and tmux display sync, call the shared Codex index append helper when:

- `session.IsCodexCompatible(inst.Tool)` is true
- `inst.CodexSessionID` is non-empty
- the new title is non-empty

Append a new JSONL record with the existing Codex UUID, new title, and current
UTC timestamp. This updates what `codex resume` displays without touching
rollout files. Existing non-Codex rename behavior must remain unchanged.

`SetField` already returns a `postCommit func()` for slow side effects. Codex
rename sync should follow that model so callers can persist Agent Deck state
first, drop UI locks, then append to the Codex index. CLI should print a
warning on post-commit failure. TUI and web mutation paths should surface a
non-fatal toast or warning where their current mutation plumbing supports it.

## Error Handling

- Import by unknown UUID/name fails before creating an Agent Deck row.
- Import by name with multiple latest records using the same name fails as
  ambiguous and asks for a UUID.
- Import of a UUID without a rollout file fails with a clear explanation that
  Agent Deck cannot safely resume it.
- Rename sync failure never reverts the Agent Deck title. It produces a warning
  that Codex's resume picker may still show the previous name.
- Missing `session_index.jsonl` is valid for rename; append creates it under
  `CODEX_HOME` after ensuring the directory exists.

## Testing

Unit tests in `internal/session` should cover index parsing, latest-record
collapse, UUID/name resolution, rollout validation, malformed JSONL, and
append-only rename sync.

CLI tests should cover successful import, unknown target failure, ambiguous
name failure, rollout-missing failure, and JSON output.

TUI tests should cover that the import picker can select a Codex session and
create a persisted Agent Deck session with the expected `CodexSessionID`.

Existing rename tests should be extended for Codex-compatible sessions so the
central title mutator emits the post-commit Codex index sync hook without
changing non-Codex behavior.

## Worktree Split

Implement in two isolated worktrees:

1. `feat/codex/import-existing-session`
   - Shared index read/resolve/list helpers.
   - CLI `session import-codex`.
   - TUI import picker/workflow.
   - Import-focused tests.

2. `fix/codex/rename-sync`
   - Shared index append helper if not already present in the worktree.
   - `SetField(FieldTitle)` post-commit wiring for Codex rename sync.
   - CLI/TUI/web warning behavior where practical.
   - Rename-focused tests.

The import branch owns the richer index discovery API. The rename branch should
keep its helper narrow enough to merge cleanly if both branches later touch the
same file.
