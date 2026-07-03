# Kiro CLI Support Design

## Context

Agent Deck currently treats Claude, Codex, OpenCode, Gemini, and several other
terminal tools as first-class session providers. A first-class provider owns its
default command, icon/style, TUI and CLI creation paths, native session ID
storage, tmux environment sync, restart behavior, and import workflow when the
underlying tool has saved sessions.

Kiro CLI is installed locally as `kiro-cli 2.2.1`. Current Kiro CLI
documentation describes `kiro-cli chat` as the interactive terminal entrypoint,
`kiro-cli chat --resume-id <SESSION_ID>` as the explicit resume path, and
`kiro-cli acp` as a separate Agent Client Protocol entrypoint. Local saved Kiro
sessions are stored under `~/.kiro/sessions/cli/*.json`, with metadata fields
that are sufficient for Agent Deck import:

```json
{
  "session_id": "37a7454c-f9d3-434a-bd7e-03318ef6b72a",
  "cwd": "/home/lrasmussen/ai-workspaces/domutech-infra",
  "title": "Hi",
  "created_at": "2026-05-08T05:27:02.624242907Z",
  "updated_at": "2026-05-08T06:31:51.570257120Z",
  "session_state": {
    "agent_name": "kiro_default"
  }
}
```

The first Agent Deck integration should follow the existing tmux-backed model
rather than introduce ACP as a second transport layer.

## Goals

- Add `kiro` as a built-in first-class Agent Deck tool.
- Start new Kiro sessions with the terminal chat UI.
- Resume imported or detected Kiro sessions by native Kiro session ID.
- Persist Kiro session IDs in Agent Deck state and SQLite `tool_data`.
- Sync `KIRO_SESSION_ID` into tmux environments.
- Show Kiro in CLI, TUI, and web-facing tool selectors where built-ins appear.
- Add CLI and TUI import flows for saved Kiro sessions.
- Group imported Kiro sessions by the saved session `cwd`, matching the import
  behavior expected for Codex and OpenCode.
- Keep TUI parity mandatory for new user-facing Kiro workflows.

## Non-Goals

- Do not implement `kiro-cli acp` integration in this first version.
- Do not implement Kiro headless automation in this first version.
- Do not rewrite Kiro transcript JSONL files.
- Do not infer Kiro session identity by scanning arbitrary terminal history.
- Do not require Kiro to be running to import a saved Kiro session.
- Do not fork Kiro sessions unless Kiro exposes and Agent Deck designs a native
  fork or handover path later.

## Command Model

Add a built-in `kiro` tool with default command:

```bash
kiro-cli chat --tui
```

Starting a fresh Agent Deck Kiro session runs the configured Kiro command in the
session project path. Resuming a known session runs:

```bash
kiro-cli chat --resume-id <session-id> --tui
```

The command builder must preserve configured options:

- `--agent <name>` when an agent is configured.
- `--model <model>` when a model is configured.
- `--trust-all-tools` when enabled.
- repeated `--trust-tools <tool>` values when configured.

If the user overrides the command in config, Agent Deck should still append
Kiro resume and option flags in the same style as other provider builders. The
default built-in command remains `kiro-cli chat --tui` so new users get the
interactive terminal UI by default.

## Configuration

Add a Kiro section to user config:

```toml
[kiro]
command = "kiro-cli chat --tui"
default_agent = "kiro_default"
default_model = ""
trust_all_tools = false
trust_tools = []
```

Add per-session Kiro options in `Instance.ToolOptionsJSON`:

```go
type KiroOptions struct {
    Agent         string   `json:"agent,omitempty"`
    Model         string   `json:"model,omitempty"`
    TrustAllTools bool     `json:"trust_all_tools,omitempty"`
    TrustTools    []string `json:"trust_tools,omitempty"`
}
```

Per-session options override user-config defaults. Empty values fall back to
user config, then to Kiro's own defaults.

## State Model

Extend Agent Deck's typed instance state with:

- `KiroSessionID string`
- `KiroDetectedAt time.Time`
- `KiroStartedAt int64`

Persist these through existing JSON and SQLite `tool_data` mechanisms using
snake_case keys:

- `kiro_session_id`
- `kiro_detected_at`
- `kiro_started_at`

`Instance.DisplaySessionID()` should return `KiroSessionID` for Kiro sessions.
`Instance.SyncSessionIDsToTmux()` should write `KIRO_SESSION_ID` when present.
Restart capability should match Codex/OpenCode resume semantics:

- Kiro sessions with a known ID can restart into that ID.
- Kiro sessions without a known ID can restart fresh.
- "restart fresh" should clear the Kiro session ID and timestamps.
- Kiro does not advertise first-class fork support in this design.

## Saved Session Import

Create a shared Kiro session index helper in `internal/session` that reads
`~/.kiro/sessions/cli/*.json` by default and returns entries sorted by
`updated_at` descending.

Suggested API:

```go
type KiroSavedSession struct {
    ID        string
    Title     string
    CWD       string
    AgentName string
    CreatedAt time.Time
    UpdatedAt time.Time
}

func KiroSessionsDir() string
func ListKiroSavedSessions(dir string) ([]KiroSavedSession, error)
func ResolveKiroSavedSession(dir, target string) (KiroSavedSession, error)
```

Resolution rules:

- Resolve exact UUID first.
- Then resolve exact title.
- Then resolve unambiguous case-insensitive title.
- Return an ambiguity error when multiple saved sessions match a non-ID target.
- Return a malformed-file error with the file path when a selected/imported
  session file cannot be parsed.

The helper should tolerate a missing Kiro sessions directory by returning an
empty list.

## CLI Import

Add:

```bash
agent-deck session import-kiro <session-id-or-title> [options]
```

Options:

- `--title`, `-t`: Agent Deck title. Defaults to the Kiro saved session title,
  then the short session ID.
- `--group`, `-g`: Agent Deck group path. Defaults to a group derived from the
  saved session `cwd` using existing import grouping helpers.
- `--path`: Project path. Defaults to the saved session `cwd`, then current
  working directory if `cwd` is empty.
- `--command`, `-c`: Kiro command. Defaults to configured Kiro command, then
  `kiro-cli chat --tui`.
- `--start`: Start the imported session immediately.
- `--json`: Return structured output.
- `--quiet`, `-q`: Minimal output.

The command creates a stopped Agent Deck session with:

- `Tool=kiro`
- `Command=<resolved command>`
- `ProjectPath=<resolved path>`
- `KiroSessionID=<saved session ID>`
- `KiroDetectedAt=<saved session updated_at>`

Duplicate detection should reject importing the same Kiro session ID into the
same Agent Deck profile more than once unless a later explicit force flag is
designed.

## TUI Import

Extend the import source picker with a saved Kiro source row:

```text
Saved Kiro sessions
```

Selecting it opens a Kiro import picker. The picker must match the current
Codex/OpenCode picker expectations:

- It supports `/` search without closing the picker.
- It searches title, short ID, full ID, agent name, and path.
- It shows enough path information to distinguish same-title sessions.
- It keeps the selection within the visible window and never renders rows
  outside the picker bounds.
- It avoids duplicate imports by checking existing Agent Deck sessions for the
  same `KiroSessionID`.

Recommended row format:

```text
> <title>  <short-id>  <updated-at>  <agent>  <cwd>
```

The path column can truncate from the left when space is tight, but the picker
should expose the full path in a detail/status line for the selected row.

Submitting a selected Kiro entry creates an Agent Deck session prefilled from
the saved metadata. The group default should be derived from `cwd`; sessions
from `/home/lrasmussen/git/domutech/domutech-github` should not fall into
`tmp` unless that path truly resolves to the tmp grouping rule.

## Built-In Tool Surface

Add Kiro everywhere built-in tools are enumerated:

- `internal/session/builtins.go`
- `internal/session/userconfig.go`
- `internal/ui/settings_panel.go`
- `internal/ui/styles.go`
- TUI new-session picker and any built-in tool value lists.
- Web/static create-session tool selector, if it has a fixed built-in list.
- tmux raw-pattern detection for panes containing `kiro-cli`.

Use a simple stable icon such as `K` for Kiro unless the project already has a
strong icon convention available. Avoid adding a new dependency for the icon.

## Status And Output

Initial Kiro status support can reuse generic terminal-output parsing plus raw
tmux prompt/busy patterns. Add conservative default raw patterns for `kiro` so
Agent Deck can detect Kiro panes and avoid obviously wrong states.

The first version does not need Kiro-specific transcript parsing. If Kiro emits
a stable hook, event, or status file later, that should be a separate targeted
design.

## Testing

Unit tests should cover:

- Kiro saved-session parsing and sorting.
- Missing Kiro sessions directory.
- UUID, exact title, case-insensitive title, and ambiguous title resolution.
- CLI import success, duplicate rejection, path/group defaults, and JSON output.
- Command builder fresh and resume forms, including model/agent/trust flags.
- State serialization and SQLite tool-data round trip for Kiro session fields.
- TUI import picker search, row formatting, path detail, and duplicate filtering.
- Built-in tool lists include Kiro in settings/new-session surfaces.

Because full-repo tests currently depend on inotify watcher availability, the
implementation should at minimum run targeted tests for modified packages and
report any environmental full-suite failures separately.

## References

- Kiro CLI docs: https://kiro.dev/docs/cli/
- Kiro CLI command reference: https://kiro.dev/docs/cli/reference/cli-commands/
- Kiro ACP docs: https://kiro.dev/docs/cli/acp/
