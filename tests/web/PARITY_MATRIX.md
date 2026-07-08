# Web UI ↔ TUI Parity Matrix

**Date:** 2026-04-29  
**Scope:** Internal API design parity check for the agent-deck repository.

**Note:** All file references below are repo-relative (e.g. `internal/ui/home.go:6179`).
This matrix is consumed by `tests/web/e2e/parity-actions.spec.js` and
`internal/web/parity_test.go`; both fail loudly if the row count or MISSING
set diverges from the live code.

---

## TUI Action Matrix

Every keyboard action in the TUI that mutates state or navigates must have a web API counterpart.

| Action | TUI Trigger | Web Endpoint | Mutator Method | Test | Notes |
|--------|-------------|--------------|-----------------|------|-------|
| **SESSION LIFECYCLE** |
| Create session | `internal/ui/home.go:6179` (`n` key) | POST `/api/sessions` | `CreateSession` | `handlers_sessions_test.go` | NewDialog spawns, initiates session creation |
| Quick create session | `internal/ui/home.go:6286` (`N` key) | POST `/api/sessions` | `CreateSession` | `handlers_sessions_test.go` | Auto-generated name, smart group context |
| Start session | `internal/ui/home.go:6284` (via dialog/menu) | POST `/api/sessions/{id}/start` | `StartSession` | `handlers_sessions_test.go` | Resumes stopped/idle session |
| Stop session | `internal/ui/home.go:6284` (via dialog/menu) | POST `/api/sessions/{id}/stop` | `StopSession` | `handlers_sessions_test.go` | Kills running tmux session |
| Restart session | `internal/ui/home.go:6473` (`R` key) | POST `/api/sessions/{id}/restart` | `RestartSession` | `handlers_sessions_test.go` | Recreate tmux with resume |
| Restart fresh | `internal/ui/home.go:6494` (`T` key) | POST `/api/sessions/{id}/restart-fresh` | `RestartFreshSession` | `handlers_sessions_test.go`, `tests/web/e2e/parity-actions.spec.js` | Discards tool binding and restarts from a fresh conversation; web row/header Fresh action |
| Delete session | `internal/ui/home.go:6302` (`d` key) | DELETE `/api/sessions/{id}` | `DeleteSession` | `handlers_sessions_test.go` | Kills + removes from storage |
| Close session | `internal/ui/home.go:6318` (`D` key) | POST `/api/sessions/{id}/close` | `CloseSession` | `handlers_sessions_test.go`, `tests/web/e2e/close-undo.spec.js` | Non-destructive close (stops process, keeps metadata); Shift+D in web UI |
| Archive session | `internal/ui/home.go` (`A` key) | POST `/api/sessions/{id}/archive` | `ArchiveSession` | `handlers_sessions_test.go`, `tests/e2e/session-lifecycle.spec.ts` | Stops tmux/agent then hides from active lists (`archived_at`); web sidebar ⌂ button |
| Unarchive session | `internal/ui/home.go` (`shift+u` in archived view) | POST `/api/sessions/{id}/unarchive` | `UnarchiveSession` | `handlers_sessions_test.go`, `tests/e2e/session-lifecycle.spec.ts` | Clears `archived_at`; does not auto-start |
| View archived sessions | `internal/ui/home.go` (`^` filter) | GET `/api/sessions/archived` | `LoadArchivedMenuSnapshot` | `handlers_sessions_test.go`, `tests/e2e/session-lifecycle.spec.ts` | Web Archived tab + TUI archived-only list |
| Fork session | `internal/ui/home.go` (`f` key, quick) | POST `/api/sessions/{id}/fork` | `ForkSession` | `handlers_sessions_test.go`, WebUI action tests | Web creates a plain tool-native fork; TUI quick fork also applies `[fork]` defaults |
| Fork with dialog | `internal/ui/home.go` (`F`/`shift+f`) | POST `/api/sessions/{id}/fork` | `ForkSessionWithOptions` | `handlers_sessions_test.go`, `static_files_test.go` | Web ForkSessionDialog mirrors Shift+F title/group/branch/worktree/state/sandbox controls; hub sessions route through hub fork options |
| Rename session | `internal/ui/home.go:6119` (`r` key) | PATCH `/api/sessions/{id}` | `UpdateSession` | `handlers_sessions_test.go`, `tests/web/e2e/edit-session.spec.js`, `tests/web/e2e/keyboard-parity.spec.js` | Title edit via web EditSessionDialog; `r` opens the same dialog |
| Undo delete | `internal/ui/home.go:6572` (`ctrl+z`) | POST `/api/sessions/undelete` | `UndoDelete` | `handlers_sessions_test.go`, `tests/web/e2e/close-undo.spec.js` | Chrome-style undo within 30s window (web.DefaultUndoWindow); Ctrl+Z in web UI |
| **GROUP OPERATIONS** |
| Create group | `internal/ui/home.go:6094` (`g` key) | POST `/api/groups` | `CreateGroup` | `handlers_groups_test.go` | Root or as subgroup |
| Rename group | `internal/ui/home.go:6119` (`r` key, group) | PATCH `/api/groups/{path}` | `RenameGroup` | `handlers_groups_test.go` | Via GroupDialog |
| Delete group | `internal/ui/home.go:6302` (`d` key, group) | DELETE `/api/groups/{path}` | `DeleteGroup` | `handlers_groups_test.go` | Moves children to default group |
| Reorder group | `internal/ui/home.go` (`K`/`J`/`+`/`-`, group) | POST `/api/groups/{path}/reorder` | `ReorderGroup` | `handlers_groups_test.go`, `hub_integration_test.go` | Moves groups among siblings; hub group paths route through hub `group_reorder` |
| Move session to group | `internal/ui/home.go:6028` (`M`/`shift+m`) | POST `/api/sessions/{id}/group` | `MoveSessionToGroup` | `handlers_sessions_test.go`, `internal/web/parity_test.go`, `tests/web/e2e/parity-actions.spec.js` | Web Move dialog and Shift+M shortcut; routes hub sessions through hub command `move` |
| **MCP MANAGEMENT** |
| Attach MCP | `internal/ui/home.go:5965` (`m` key → MCPDialog) | POST `/api/sessions/{id}/mcps/{name}` | `MCPManager.Attach` | `handlers_mcps_test.go` | Body `{scope?}`; default scope=local; writes `.mcp.json` via session helpers |
| Detach MCP | `internal/ui/home.go:5965` (`m` key → MCPDialog) | DELETE `/api/sessions/{id}/mcps/{name}` | `MCPManager.Detach` | `handlers_mcps_test.go` | Body `{scope?}`; scope auto-detected if omitted |
| List MCPs | `internal/ui/home.go:5965` (`m` key → MCPDialog) | GET `/api/sessions/{id}/mcps` | `MCPManager.ListAttached` | `handlers_mcps_test.go` | Returns `{local,global,user}`; catalog at GET `/api/mcps` |
| Toggle pooled ↔ local | `internal/ui/home.go:5965` (`m` key → MCPDialog) | PATCH `/api/sessions/{id}/mcps/{name}` | `MCPManager.Move` | `handlers_mcps_test.go` | Body `{scope}` or `{pooled:bool}`; pooled=true→global, pooled=false→local |
| **SKILLS MANAGEMENT** |
| Attach skill | `internal/ui/home.go:6015` (`s` key → SkillDialog) | POST `/api/sessions/{id}/skills/{name}` | `apiFetch('POST', …)` from `SkillsPane.js` | `tests/web/e2e/skills.spec.js` | Wired via `web.SkillsService`; writes project config |
| Detach skill | `internal/ui/home.go:6015` (`s` key → SkillDialog) | DELETE `/api/sessions/{id}/skills/{name}` | `apiFetch('DELETE', …)` from `SkillsPane.js` | `tests/web/e2e/skills.spec.js` | Wired via `web.SkillsService` |
| List skills (catalog) | `internal/ui/home.go:6015` (`s` key → SkillDialog) | GET `/api/skills` | `SkillsPane.js` catalog column | `tests/web/e2e/skills.spec.js` | Mirrors `session.ListAvailableSkills` |
| List skills (attached) | `internal/ui/home.go:6015` (`s` key → SkillDialog) | GET `/api/sessions/{id}/skills` | `SkillsPane.js` attached column | `tests/web/e2e/skills.spec.js` | Mirrors `session.GetAttachedProjectSkills(projectPath)` |
| **SETTINGS & DISPLAY** |
| Edit session settings | `internal/ui/home.go:5953` (`P`/`shift+p` → EditSessionDialog) | PATCH `/api/sessions/{id}` | `UpdateSession` (delegates to `session.SetField`) | `handlers_sessions_test.go` + `tests/web/e2e/edit-session.spec.js` | Title, color, notes, tool, extra-args, plugins, channels, skip-permissions, auto-mode. Returns `restartRequired` for restart-policy fields. Web UI: `EditSessionDialog.js` + Sidebar Edit button. |
| Edit multi-repo paths | `internal/ui/home.go:5942` (`p` → EditPathsDialog) | POST `/api/sessions/{id}/paths` | `UpdateSessionPaths` | `handlers_sessions_test.go`, `internal/web/parity_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | Multi-repo session paths; web `p` opens a paths dialog and routes hub sessions through hub `update_paths` |
| Edit notes inline | `internal/ui/home.go:6548` (`e` key) | POST `/api/sessions/{id}/notes` | `UpdateSessionNotes` | `handlers_sessions_test.go`, `internal/web/parity_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | Web `e` opens inline notes dialog; routes hub sessions through hub update `notes` |
| Toggle YOLO mode | `internal/ui/home.go:6418` (`y` key) | POST `/api/sessions/{id}/toggle-yolo` | `ToggleYoloSession` | `handlers_sessions_test.go`, `static_files_test.go` | Gemini/Codex/Hermes; requires restart for some tools; web row/header YOLO action |
| Open settings panel | `internal/ui/home.go:6148` (`S` key) | GET `/api/settings` | N/A | `handlers_settings_test.go` | Read-only; displays profile, version |
| **WORKFLOW & NAVIGATION** |
| Prompt session | `internal/ui/home.go` (`o` key) | POST `/api/sessions/{id}/send` | `SendSessionPrompt` | `handlers_sessions_test.go`, `internal/web/parity_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | One-line prompt without attaching; web `o` opens PromptSessionDialog and supports hub sessions |
| Mark session unread | `internal/ui/home.go:6366` (`u` key) | POST `/api/sessions/{id}/unread` | `MarkSessionUnread` | `handlers_sessions_test.go`, `internal/web/parity_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | idle → waiting transition; routes hub sessions through hub command `mark_unread` |
| Quick approve | `internal/ui/home.go:6387` (default hotkey) | POST `/api/sessions/{id}/approve` | `QuickApproveSession` | `handlers_sessions_test.go`, `internal/web/parity_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | Send "1"+Enter without attach; routes hub sessions through hub `send` |
| Copy output | `internal/ui/home.go:6511` (`c` key) | UI `c` shortcut + GET `/api/sessions/{id}/output` fallback | `TerminalPanel` buffer / `SessionOutputProvider` → clipboard | `static_files_test.go`, `handlers_sessions_test.go`, `hub_integration_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | Copies focused terminal scrollback when mounted; otherwise reads local output or hub preview for the focused session |
| Copy session info | `internal/ui/home.go:6521` (`C`/`shift+c`) | UI `Shift+C` shortcut | `sessionInfoText` → clipboard | `static_files_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | Repo/path/branch/session metadata → clipboard |
| Send output to session | `internal/ui/home.go:6532` (`x` key) | POST `/api/sessions/{id}/send-output` | `SendSessionOutput` | `handlers_sessions_test.go`, `internal/web/parity_test.go`, `static_files_test.go` | Web `x` opens a target picker; works with hub session IDs through preview/send commands |
| Exec shell | `internal/ui/home.go:6161` (`E` key) | WS `/ws/session/{id}?shell=sandbox` | `OpenLocalSandboxShell` / `OpenHubSandboxShellTerminal` | `handlers_ws_test.go`, `static_files_test.go` | Sandbox container shell for local and hub sessions; web Shift+E and row More → Sandbox shell open the sandbox terminal mode |
| Toggle preview mode | `internal/ui/home.go:6413` (`v` key) | UI `v` shortcut | `previewModeSignal` / `cyclePreviewMode` | `static_files_test.go` | Web maps both → terminal+rail, output → terminal-only, analytics → costs+usage rail |
| Open search | `internal/ui/home.go:6133` (`/` key) | UI `/` shortcut | Sidebar filter/search UI | `tests/web/e2e/keyboard-parity.spec.js` | Web `/` focuses the session filter; search pane covers session search navigation |
| Open global search | `internal/ui/home.go:5691` (`G` key) | GET `/api/search/global?q=...` + UI `G` shortcut | `GlobalSearchIndex` | `handlers_search_test.go`, `static_files_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | Claude conversation index search; web Search pane Global mode |
| Open help | `internal/ui/home.go:6143` (`?` key) | UI `?` shortcut | `KeyboardShortcuts.js` | `tests/web/e2e/keyboard-parity.spec.js` | Web `?` toggles the keyboard shortcuts overlay |
| Manual refresh | `internal/ui/home.go:6590` (`ctrl+r`) | GET `/api/menu` | `refreshMenuSnapshot` | `tests/web/e2e/keyboard-parity.spec.js` | Ctrl/Cmd+R refreshes the session/hub-node snapshot without a full page reload |
| Jump mode | `internal/ui/home.go:6406` (`space` key) | UI `Space` shortcut | `jumpModeSignal` / `JumpOverlay` | `static_files_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | Vimium-style hint navigation over current sessions |
| Attach session | `internal/ui/home.go:5744` (`enter` key) | WS `/ws/session/{id}` | `TerminalBridge` / `OpenHubTerminal` | `handlers_ws_test.go`, `tests/web/e2e/keyboard-parity.spec.js` | Enter switches to the terminal pane; TerminalPanel streams local and hub sessions through the websocket bridge |
| **WORKTREE OPERATIONS** |
| Finish worktree | `internal/ui/home.go:6038` (`W`/`shift+w`) | POST `/api/sessions/{id}/worktree/finish` | `FinishWorktree` | `issue1126_worktree_finish_test.go`, `tests/web/e2e/worktree-finish.spec.js` | Merge + cleanup; body accepts `into`, `noMerge`, `keepBranch`, `force` (mirrors CLI flags). Hub sessions route through hub `worktree_finish`. Issue #1126. |
| **COST TRACKING** |
| View costs dashboard | `internal/ui/home.go` (TUI only) | GET `/api/costs/summary` | N/A | `handlers_costs_test.go` | Sessions cost aggregation. **e2e parity: degraded-only** — fixture omits the SQLite cost store, so the e2e probe asserts the documented 503 `UNAVAILABLE` response. Happy-path (200 + payload) coverage is `parity-test-deferred` to PR-B fixture wiring. |
| Cost export | N/A | GET `/api/costs/export` | N/A | `handlers_costs_test.go` | Web-only; CSV/JSON export. **e2e parity: degraded-only** (503 without cost store). Happy-path `parity-test-deferred` to PR-B. |
| **PUSH NOTIFICATIONS** |
| Subscribe to push | `internal/ui/home.go` (TUI none) | POST `/api/push/subscribe` | N/A | `handlers_push_test.go` | Web browser push only. **e2e parity: degraded-only** — fixture has no push service (no VAPID keys + subscription db), so the probe asserts 503 `PUSH_NOT_CONFIGURED`. Happy-path `parity-test-deferred` to PR-B. |
| Unsubscribe push | `internal/ui/home.go` (TUI none) | POST `/api/push/unsubscribe` | N/A | `handlers_push_test.go` | Web browser push only. **e2e parity: degraded-only** (503 without push service). Happy-path `parity-test-deferred` to PR-B. |
| Update push presence | `internal/ui/home.go` (TUI none) | POST `/api/push/presence` | N/A | `handlers_push_test.go` | Web browser focus tracking. **e2e parity: degraded-only** (503 without push service). Happy-path `parity-test-deferred` to PR-B. |

---

## State Fields Matrix

Every observable session field shown in the TUI must appear in the web API JSON response.

| State Field | TUI Display | Web JSON Location | Notes |
|-------------|-------------|------------------|-------|
| **CORE IDENTITY** |
| `id` | Session list | `MenuSession.id` | ✅ Present |
| `title` | Session row label | `MenuSession.title` | ✅ Present |
| `tool` | Session row icon/label | `MenuSession.tool` | ✅ Present (claude, gemini, shell, etc.) |
| `status` | Session row color/icon | `MenuSession.status` | ✅ Present (running, waiting, idle, error, stopped, starting) |
| `group_path` | Folder hierarchy | `MenuSession.groupPath` | ✅ Present |
| **LOCATION & TIME** |
| `project_path` | Preview pane | `MenuSession.projectPath` | ✅ Present |
| `created_at` | Info section | `MenuSession.createdAt` | ✅ Present |
| `last_accessed_at` | Info section | `MenuSession.lastAccessedAt` | ✅ Present |
| `archived_at` | Archived list / filter | `MenuSession.archivedAt` | ✅ Present; non-zero when session is archived (omitempty on active menu) |
| **RELATIONSHIPS** |
| `parent_session_id` | Sub-session indicator | `MenuSession.parentSessionId` + `GET /api/sessions/{id}/children` | ✅ Present; tree endpoint surfaces full conductor child topology in the right-rail Children card (`internal/web/handlers_children.go`, `tests/web/e2e/children-panel.spec.js`) |
| `is_conductor` | (Not shown in TUI) | `MenuSession.isConductor` | ✅ Present; conductor metadata. Tree topology also surfaced at `GET /api/sessions/{id}/children` (kind derived UI-side from title/groupPath in `dataModel.js`) |
| **PROCESS STATE** |
| `tmux_session` | Internal reference | `MenuSession.tmuxSession` | ✅ Present (tmux session name) |
| `tmux_socket_name` | (Internal) | `MenuSession.tmuxSocketName` | ✅ Present; issue #687 |
| **TOOL-SPECIFIC** |
| `claude_session_id` | (Tooltip, not prominent) | `MenuSession.claudeSessionId` | ✅ Present |
| `gemini_session_id` | (Tooltip, not prominent) | `MenuSession.geminiSessionId` | ✅ Present |
| `gemini_model` | (Not shown) | `MenuSession.geminiModel` | ✅ Present; active Gemini model |
| `gemini_yolo_mode` | (Toggle via `y` key) | `MenuSession.geminiYoloMode` | ✅ Present; *bool, `&false` marshals as `false` |
| `codex_session_id` | (Not shown) | `MenuSession.codexSessionId` | ✅ Present |
| `opencode_session_id` | (Not shown) | `MenuSession.opencodeSessionId` | ✅ Present |
| **CONTENT** |
| `latest_prompt` | (Not shown in TUI) | `MenuSession.latestPrompt` | ✅ Present; last user input |
| `notes` | Preview pane (if enabled) | `MenuSession.notes` | ✅ Present |
| **APPEARANCE** |
| `color` | Row background tint | `MenuSession.color` | ✅ Present; lipgloss color spec |
| **CONFIGURATION** |
| `command` | (Edit dialog) | `MenuSession.command` | ✅ Present |
| `wrapper` | (Edit dialog) | `MenuSession.wrapper` | ✅ Present |
| `channels` | (Edit dialog) | `MenuSession.channels` | ✅ Present; Claude plugin channel ids |
| `extra_args` | (Edit dialog) | `MenuSession.extraArgs` | ✅ Present |
| `tool_options_json` | (Edit dialog) | `MenuSession.toolOptions` | ✅ Present; raw JSON tool-specific options |
| **SANDBOX & REMOTE** |
| `sandbox` | (Edit dialog) | `MenuSession.sandbox` | ✅ Present; Docker sandbox config |
| `sandbox_container` | (Not shown) | `MenuSession.sandboxContainer` | ✅ Present |
| `ssh_host` | (Not shown) | `MenuSession.sshHost` | ✅ Present |
| `ssh_remote_path` | (Not shown) | `MenuSession.sshRemotePath` | ✅ Present |
| **MULTIREPO** |
| `multi_repo_enabled` | (Not shown) | `MenuSession.multiRepoEnabled` | ✅ Present |
| `additional_paths` | (Edit dialog) | `MenuSession.additionalPaths` | ✅ Present |
| `multi_repo_temp_dir` | (Not shown) | `MenuSession.multiRepoTempDir` | ✅ Present |
| `multi_repo_worktrees` | (Not shown) | `MenuSession.multiRepoWorktrees` | ✅ Present |
| **WORKTREE** |
| `worktree_path` | (Edit dialog) | `MenuSession.worktreePath` | ✅ Present |
| `worktree_repo_root` | (Edit dialog) | `MenuSession.worktreeRepoRoot` | ✅ Present |
| `worktree_branch` | (Edit dialog) | `MenuSession.worktreeBranch` | ✅ Present |
| **PERSISTENCE & FLAGS** |
| `order` | Row position in group | `MenuSession.order` | ✅ Present |
| `title_locked` | (Not shown) | `MenuSession.titleLocked` | ✅ Present |
| `no_transition_notify` | (Not shown) | `MenuSession.noTransitionNotify` | ✅ Present |
| **MCP & LIFECYCLE** |
| `loaded_mcp_names` | (MCP dialog) | `MenuSession.loadedMcpNames` | ✅ Present |
| `is_fork_awaiting_start` | (Internal) | MISSING | Transient `json:"-"` field on Instance, not persisted |
| `skip_mcp_regenerate` | (Internal) | MISSING | Transient `json:"-"` field on Instance, not persisted |
| **ANALYTICS (Conditional)** |
| `claude_analytics` | Cost/token panel | MISSING | No `ClaudeAnalytics` struct on `*session.Instance` today |
| `gemini_analytics` | Cost/token panel | `MenuSession.geminiAnalytics` | ✅ Present |

---

## Behavioral Coverage Status (PR-A)

Every IMPLEMENTED row above is exercised by either the Playwright e2e suite
(`tests/web/e2e/parity-actions.spec.js`), the Go runtime parity test
(`internal/web/parity_test.go`), or both. Rows split into three coverage
tiers:

- **Happy-path** (web mutation + state observation): session lifecycle
  (create/start/stop/restart/delete/fork), group ops (create/rename/delete),
  `GET /api/settings`. Go parity test additionally pins web↔direct-mutator
  parity for create/start/stop/delete sessions and create/rename/delete
  groups.
- **Degraded-only** (503 + documented error code): cost endpoints
  (`/api/costs/summary`, `/api/costs/export`) and push endpoints
  (`/api/push/{subscribe,unsubscribe,presence}`). The fixture binary
  intentionally omits the SQLite cost store and the push service; happy-path
  coverage requires fixture wiring deferred to PR-B.
- **MISSING-stays-missing** (regression guard, 404/405 expected): 0 of the
  0 MISSING actions have plausible URL patterns probed by
  `inferMissingProbe()` in `tests/web/helpers/parity-matrix.js`.

## Summary Statistics

### Action Parity
- **Total TUI actions:** 53 (session/group/MCP/skills/settings/workflow/costs/push)
- **Web/API/UI surfaces implemented:** 53
- **MISSING web actions:** 0
- **Key gaps:** none in the tracked TUI action matrix

### State Field Parity
- **Total TUI-visible fields:** ~50
- **Web JSON fields:** 42
- **MISSING web fields:** 3 (~7% gap) — two transients (`is_fork_awaiting_start`, `skip_mcp_regenerate`) and one not-yet-modeled (`claude_analytics`)
- **Remaining gaps:**
  - `is_fork_awaiting_start`, `skip_mcp_regenerate`: `json:"-"` on `*session.Instance`; nothing to surface
  - `claude_analytics`: no `ClaudeAnalytics` struct on the Instance yet (gemini-only today)

---

## Key Insights

### Sync Gaps (Actions)

1. **Session Metadata Parity**: Web has PATCH `/api/sessions/{id}` for EditSessionDialog settings, POST `/api/sessions/{id}/notes` for inline notes, POST `/api/sessions/{id}/group` for group moves, and POST `/api/sessions/{id}/paths` for multi-repo path editing.

2. **Workflow Action Gaps**: No tracked TUI action rows remain missing in the web/API/UI parity matrix.

3. **Implemented Non-HTTP Surfaces**: Some parity rows are intentionally UI/WS rather than HTTP: `/` search focus, `?` help overlay, Enter terminal attach via `/ws/session/{id}`, and Ctrl/Cmd+R manual refresh via GET `/api/menu`.

### Sync Gaps (State)

1. **Transient Instance Flags**: `is_fork_awaiting_start` and `skip_mcp_regenerate` are intentionally transient `json:"-"` fields; no persisted web state exists to expose.

2. **Claude Analytics**: Gemini analytics are surfaced, but there is no modeled `ClaudeAnalytics` field on `*session.Instance` today.

---

## NOT IN CODE (Documented but Not Implemented)

- **Watcher Management** (create, fire, remove): Documented in CLAUDE.md but not found in codebase. Internal event watcher system exists (`internal/watcher/`) but has no TUI/web entry points.
- **Conductor Operations** (create, attach channel, send, receive): Not implemented in this codebase snapshot. Conductor sessions are recognized as a flag but no specific conductor management actions are implemented.
- **Channel Management**: Channels are configuration fields surfaced through the session edit path; there is no standalone channel-management workflow.

---

## Recommendations

1. Decide whether exec-shell should be a true web parity feature or explicitly documented TUI-only surface.
2. Add/update API documentation for the current HTTP, UI, WS, and hub-proxy parity surfaces.

---

## Hub Native Parity Audit (2026-07-08)

This section tracks whether sessions owned by connected Agent Deck hub nodes feel
native across TUI, web, and CLI. It is intentionally broader than web↔TUI local
parity above.

### Implemented hub-native surfaces

- **Hub session projection:** remote hub sessions and empty hub nodes/groups are
  projected into the TUI and web menu snapshots with short node names.
- **Hub create:** TUI `n`/`N`, web new-session dialog, and CLI
  `agent-deck hub sessions create` can create sessions on selected hub nodes.
- **Hub attach:** TUI Enter, web terminal websocket, and CLI attach relay
  interactive attach through the hub.
- **Hub lifecycle/actions:** start, stop/close, restart, restart fresh, delete,
  archive, unarchive, metadata remove, mark unread, quick approve, prompt/send,
  preview, notes, move group, rename, edit paths, YOLO toggle, fork, fork with
  dialog/options, and worktree finish route through hub commands.
- **Hub group CRUD/settings:** TUI `g`/`r`/`d` on hub groups, web `/api/groups`
  for `hub/<node>/<group>` paths, and CLI `agent-deck hub groups` support group
  list, create, rename, update `default_path`/`max_concurrent`, and delete.
- **Hub group reparent/reorder:** hub command dispatch and the local
  owner backend support remote group reparent/reorder. CLI exposes
  `agent-deck hub groups change` and `agent-deck hub groups reorder`; web API
  exposes `POST /api/groups/{path}/change` and `/reorder`; TUI group-order keys
  (`K`/`J`/`+`/`-`) reorder hub groups through the hub command channel and
  preserve remote group order in TUI/web menu projections. TUI `M` on a local
  or hub group opens a reparent dialog with root/self/descendant safeguards, and
  the web sidebar exposes visible group create/rename/move/reorder/delete
  controls that route hub group paths through the hub command channel.
- **Hub MCP management foundation:** hub command dispatch and owner backend
  support remote MCP list/attach/detach/move across local/global/user scopes.
  TUI `m` on a hub session loads/applies MCP state through the hub command
  channel, web MCP endpoints route `hub/<node>/<session>` ids through the hub
  command channel using the remote node catalog, and CLI exposes
  `agent-deck hub mcps attached|catalog|attach|detach|move`.
- **Hub plugin management:** hub command dispatch and owner backend support
  remote plugin catalog/list/attach/detach with channel auto-link state. TUI
  `L`, the web Plugins pane/endpoints, and CLI `agent-deck hub plugins` route
  hub sessions through the hub command channel.
- **Hub undo delete:** hub session deletes now push an owner-local undo window;
  TUI Ctrl+Z, web `POST /api/sessions/undelete`, and CLI
  `agent-deck hub sessions undo-delete <node>` route restore through the owner
  node without exporting deleted session internals over the hub.
- **Hub sandbox shell:** TUI `E`, web Shift+E / row More → Sandbox shell, and
  CLI `agent-deck hub sessions sandbox-shell` open the owner-side container
  shell via `sandbox_shell` and attach through a short-lived signed tmux
  attach token over the existing hub relay.
- **Hub web dashboard proxy:** the local web UI can proxy/switch to another
  node's native web dashboard through the hub command channel; the switcher only
  lists nodes whose latest snapshot reports `web_available`, and direct proxy
  requests to known-unavailable nodes return a structured unavailable response.
- **Hub management basics:** CLI covers serving, invites, join, status, node
  listing/rename/promote/revoke flows, trust requests/decisions, shell, sessions,
  groups, MCPs, skills, bundled skill install, and installing/removing/statusing
  `hub connect` as either a per-user or system-wide systemd service.
- **Hub web admin management:** when the local web server is configured as a hub
  admin node, the web Hub Nodes dialog lists/renames/promotes/demotes/revokes
  nodes, creates/revokes invites without exposing stored invite tokens, and
  lists/allows/denies pending trust requests through the local server proxy.
- **Hub TUI admin management:** when the local TUI node is an admin hub node,
  `H` opens a hub admin dialog for node-management-adjacent flows: listing
  pending trust requests, allowing/denying missed trust requests, listing
  invites, creating user/admin invites, and revoking invites. Non-admin TUI
  nodes hide/deny these controls with explicit admin-required guidance.

### Still missing for full hub/native parity

1. **Hub rich live/status metadata:** hub snapshots now carry edit/dialog
   metadata such as color, command/wrapper, channels/plugins, extra args, tool
   options JSON, sandbox config/container, multi-repo/worktree details, loaded
   MCP names, title-lock/no-transition flags, display session ids, and
   Honest-Status-v2 substate, and conductor parent/child topology. Remaining
   gaps are richer live telemetry such as tmux window lists and analytics beyond
   current projected fields.
2. **Hub web-dashboard proxy hardening:** the dashboard switcher/proxy now hides
   and rejects nodes whose latest snapshot reports no remote web server, but
   still needs broader route coverage, error UX, and security/permission review
   before it should be considered complete parity.
3. **Hub CLI breadth:** CLI now covers hub sessions, groups, MCP
    attach/detach/list/catalog/move, skills attach/detach/list/catalog, and remote
    plugins, but richer admin workflows remain incomplete.
