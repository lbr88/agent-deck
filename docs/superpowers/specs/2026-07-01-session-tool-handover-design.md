# Session Tool Handover Design

## Context

Agent Deck can already manage Claude, Codex, and OpenCode sessions as separate
tool types. Each tool has a different native history mechanism:

- Claude resumes from Claude JSONL transcripts via `ClaudeSessionID`.
- Codex resumes and forks from Codex rollout files via `CodexSessionID`.
- OpenCode resumes from OpenCode storage and can clone OpenCode sessions via
  its own export/import flow.

Those formats are not a stable shared interchange format. A native-history
migrator that rewrites native history files across all three tools would be
brittle and would need to track private format changes in every upstream CLI.

The first handover feature should therefore use a handover model: keep the
source Agent Deck session unchanged, create a new target-tool session in the
same project/group, and start it with a generated context packet that tells the
target tool what it needs to continue.

## Goals

- Add a CLI workflow to hand over one Agent Deck session into a new Claude,
  Codex, or OpenCode session.
- Add a TUI workflow with equivalent behavior. TUI parity is mandatory for this
  user-facing session feature.
- Keep CLI and TUI behavior on a shared handover service.
- Preserve the source session unchanged.
- Use source session metadata, project path, group, git context, and latest
  useful output to build a concise target-tool handover prompt.
- Start the handed-over session by default when requested, using the existing
  `StartWithMessage` path so delivery semantics stay consistent with launch and
  send.

## Non-Goals

- Do not rewrite Claude JSONL, Codex rollout, or OpenCode native session files.
- Do not mutate the source Agent Deck row in place.
- Do not promise full transcript continuity in the target tool.
- Do not add Gemini, Pi, Cursor, Hermes, shell, or custom-tool handover in
  this first version.
- Do not add AI summarization as a dependency. The first handover packet is
  deterministic and local.

## User-Facing Behavior

### CLI

Add:

```bash
agent-deck session handover <source-session> --to <claude|codex|opencode> [options]
```

Options:

- `--to`: required target tool.
- `--title`, `-t`: title for the new session. Defaults to
  `<source title> (<target>)`.
- `--group`, `-g`: group for the new session. Defaults to source group.
- `--path`: project path for the new session. Defaults to source project path.
- `--message`, `-m`: optional user instruction appended to the handover packet.
- `--start`: start the handed-over session and deliver the handover message.
- `--no-start`: create the stopped target session without starting it. This is
  useful for review or later manual start.
- `--json`: structured output.
- `--quiet`, `-q`: minimal output.

Default start behavior should match existing import conventions for safety:
create the new row stopped unless `--start` is provided. The TUI can make start
a visible checkbox so the user chooses intentionally.

The command refuses handover when the target tool equals the source tool. The
message should point users at the existing same-tool fork feature where
available.

### TUI

Add a handover action reachable from the selected session row. Use a two-key
route under the existing edit/action family, `P` then `h`, rather than taking a
new top-level key. The flow must be discoverable in help and must not require
dropping to a shell.

The handover dialog shows:

- source title and source tool
- source tool session id when known, shortened for display
- target tool selector with Claude, Codex, and OpenCode, excluding the source
  tool
- editable title, defaulting to `<source title> (<target>)`
- editable project path, defaulting to source project path
- editable group, defaulting to source group
- optional single-line message field
- start-now checkbox

On confirm, the TUI calls the same shared handover service as the CLI. If the
start-now checkbox is enabled, it starts the new target session with the
handover packet. If it is disabled, it persists a stopped target session and
surfaces a success notice.

## Shared Handover Service

Add a small service in `internal/session` rather than putting handover logic
directly in CLI or TUI handlers.

Suggested API shape:

```go
type HandoverTarget string

const (
    HandoverTargetClaude   HandoverTarget = "claude"
    HandoverTargetCodex    HandoverTarget = "codex"
    HandoverTargetOpenCode HandoverTarget = "opencode"
)

type HandoverOptions struct {
    Target      HandoverTarget
    Title       string
    GroupPath   string
    ProjectPath string
    Message     string
    Start       bool
    Peers       []*Instance
}

type HandoverResult struct {
    Source         *Instance
    Target         *Instance
    HandoverPrompt string
    Started        bool
    Warning        string
}

func HandoverSession(source *Instance, opts HandoverOptions) (*HandoverResult, error)
```

`HandoverSession` should:

1. Validate source and target.
2. Resolve target title/group/path defaults.
3. Build a target `Instance` with the target tool's normal base command:
   `claude`, `codex`, or `opencode`.
4. Copy safe shared metadata where appropriate: sandbox settings, account only
   for Claude-compatible target sessions, plugin/channel fields only if the
   target tool supports them today, and worktree metadata only when the
   handed-over session stays in the same project path.
5. Build a deterministic handover prompt.
6. Return the target instance and prompt to the caller.

Persistence and starting can remain in the CLI/TUI layer because those layers
already own storage, group trees, and user-facing output. The handover
service should not write global state by itself.

## Handover Packet

The generated prompt should be concise, explicit, and stable enough to test. It
should not pretend that native history was migrated.

Recommended structure:

```text
You are continuing work from an Agent Deck session handed over from <source tool>
to <target tool>.

Source session:
- Agent Deck title: <title>
- Agent Deck id: <id>
- Source tool: <tool>
- Source tool session id: <tool id or unknown>
- Project path: <path>
- Group: <group>

Git context:
<branch/status summary or "not a git repository / unavailable">

Latest useful source output:
<bounded latest output or "No latest output was available.">

Operator instruction:
<optional --message / TUI message, or "Continue the task from the context above.">

Important:
- Native transcript history was not migrated.
- Treat this handover as the context to continue from.
- Inspect the repository before making changes.
```

The latest output should come from `GetLastResponseBestEffortChecked(peers)`
when possible so Claude transcript collision guards remain active. For Codex
and OpenCode, the current best effort may be terminal-output based; the handover
builder should cap content by characters or lines so handover cannot flood a
new session with huge pane history.

Git context should be best effort and local:

- current branch
- short status summary
- current HEAD short hash when available
- clear message when git is unavailable, the path is not a repo, or a command
  fails

## Data Flow

CLI:

1. Load profile sessions and groups.
2. Resolve source by id/title using existing session resolution.
3. Call `session.HandoverSession(source, opts)`.
4. Append target instance to storage and ensure its group exists.
5. If `--start`, call `target.StartWithMessage(result.HandoverPrompt)`.
6. Save target state again after start so status and session ids can persist.
7. Print result.

TUI:

1. User opens the handover dialog for selected source session.
2. Dialog collects target, title, path, group, optional message, and start-now.
3. Submit handler calls the same `session.HandoverSession`.
4. Add the target to `h.instances`, `h.groupTree`, and storage using existing
   session-created plumbing where practical.
5. If start-now is enabled, use existing start/create command paths that already
   handle status, errors, and loading placeholders.
6. Rebuild flat items and preserve selection on the newly handed-over target when
   possible.

## Error Handling

- Missing source session: existing not-found behavior.
- Unsupported target: stable error naming allowed targets.
- Target equals source tool: refuse and suggest same-tool fork when supported.
- Empty source project path: use current working directory as fallback and
  surface a warning in JSON/TUI notice.
- Failed latest-output extraction: handover still succeeds with a warning and
  a handover packet that says no latest output was available.
- Failed git context extraction: handover still succeeds with a warning-free
  "git context unavailable" line in the handover packet.
- Start failure after the row is created: keep the row, mark/start failure
  using existing `StartWithMessage` behavior, and surface the error.

## Duplicate Handling

Handover creates a new Agent Deck session. It should not reject merely
because another session has the same project path. It should avoid exact title
collisions by auto-suffixing generated default titles:

- `foo (codex)`
- `foo (codex 2)`
- `foo (codex 3)`

If the user explicitly passes a title that collides, keep current Agent Deck
behavior for explicit titles unless there is already a central title-collision
rule. Do not invent a broader uniqueness policy in this feature.

## Testing

Shared handover tests in `internal/session`:

- Claude -> Codex builds a Codex target with same path/group and a handover
  packet containing source metadata.
- Codex -> Claude builds a Claude target and refuses target equals source.
- OpenCode -> Claude includes OpenCode session id when present.
- Missing latest output produces a deterministic fallback section.
- Generated default title suffixes around existing peer titles.
- Handover packet caps long latest output.
- Git context helper reports branch/status for a real temp repo and a clear
  unavailable message outside git.

CLI tests in `cmd/agent-deck`:

- `session handover <source> --to codex` creates a stopped Codex row.
- `--start` calls the start path with the generated handover prompt.
- `--json` includes source id, target id, target tool, started, and warning.
- target equals source exits with a clear error.
- unknown target exits with a clear error.

TUI tests in `internal/ui`:

- Handover action opens a dialog for the selected session.
- Target selector excludes the source tool.
- Confirm creates a persisted target row with same group/path defaults.
- Start-now path invokes the create/start command with the generated handover
  prompt.
- TUI surfaces handover errors without mutating the session list.
- Help includes the handover action.

## Implementation Notes

- Keep the first version deterministic. Do not introduce model calls to
  summarize transcripts.
- Prefer reusing `StartWithMessage`, `GetLastResponseBestEffortChecked`, and
  existing group-tree save paths instead of creating separate delivery or
  storage flows.
- Keep native same-tool fork behavior separate. Same-tool handover is not a
  replacement for `session fork`.
- Handover targets use the existing configured tool command resolution at
  start time through normal `Instance` command building.
- The TUI optional message is single-line in the first version. Multiline
  editing can be added later if real usage shows that the extra UI complexity
  is worth it.
- CLI handover creates stopped sessions by default. Starting requires
  `--start`; the TUI mirrors this with an unchecked start-now checkbox.
