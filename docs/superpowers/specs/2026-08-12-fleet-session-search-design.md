# Fleet Session Search Design

## Problem

The `/` overlay currently searches only local `session.Instance` values. Its `Tab` hint says “Global,” but the switch depends on a separate Claude conversation-file index that is deliberately disabled because it previously consumed several gigabytes of memory. Consequently, `Tab` silently does nothing. Even when enabled, that index searches local Claude transcripts rather than Agent Deck sessions across the fleet.

Selecting a result with `Enter` currently hides the overlay and moves the main-list cursor. It does not run the normal session activation path.

## Approved behavior

- `/` opens session search in **Global** scope by default.
- Global scope searches every session currently known to Agent Deck: local sessions, configured SSH-remote sessions, and sessions received from hub snapshots.
- `Tab` toggles between **Global** and **Local** scope.
- Local scope contains only sessions owned by the current machine.
- Each global result shows its owning machine or hub node so equal titles remain distinguishable.
- Search matches title, path, group, tool, status, and host while preserving the existing local fuzzy-search behavior.
- `Enter` activates the selected result through the same local, SSH-remote, or hub attach/restart path used by `Enter` in the main list.
- `Alt+/` remains a local, current-group search.
- The disabled transcript index remains separate and is not re-enabled.

## Architecture

The existing `Search` overlay will operate on a lightweight `SessionSearchResult` model containing display/search fields plus the exact local, remote, or hub identity required for activation. It keeps separate global and local source slices and changes scope in memory when `Tab` is pressed.

`Home` will build a snapshot of search results from `instances`, `remoteSessions`, and `hubSessions` immediately before opening the overlay. This avoids watchers, disk indexing, network calls, and stale identity reconstruction. A single activation helper will dispatch a selected result to `activateLocalSession`, `attachRemoteSession`, or `attachHubSession`; both search and the main list continue using the established attach implementations.

## Error handling and state

- An empty result set keeps the overlay open and makes `Enter` a no-op.
- A hub result uses its snapshot status to preserve the existing stopped/error restart-before-attach behavior.
- If a remote configuration or hub connection disappears between search and selection, the existing attach helper reports or safely returns the same way it does from the main list.
- Opening and closing search clears its query and cursor. Scope resets to Global for ordinary `/` and Local for `Alt+/`.

## Verification

Regression tests will prove that ordinary `/` starts globally with local, SSH-remote, and hub results; `Tab` changes to local-only results and can toggle back; result rendering includes the host; and `Enter` returns the real activation command for a selected hub session rather than only moving the cursor. Existing search, group-navigation, hub, and UI tests must remain green.
