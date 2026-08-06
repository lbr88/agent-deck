# Unified Preview Safety and Release Design

## Problem

Agent Deck has three TUI preview sources: local tmux sessions, SSH remote
sessions, and hub sessions. Commit `50e0ad32` expanded tabs only in the local
preview renderer. The SSH and hub renderers still append raw capture-pane
content directly to their preview builders, so a tab or another terminal
control sequence can escape their width accounting and corrupt the full TUI.

This was reproduced through the `lbr sandbox machine` data path: the AWS
workstation already had `50e0ad32`, but it consumed the session as a hub
preview and therefore bypassed the local-only sanitizer.

## Requirements

- Local, SSH remote, and hub TUI previews must apply identical terminal-safety
  rules before content reaches the outer terminal.
- Horizontal tabs must expand to eight-cell terminal tab stops before width
  measurement.
- Dangerous C0 controls and display-erasing CSI sequences must not reach the
  outer terminal; SGR color must remain supported.
- Every rendered preview line, including metadata lines, must fit the preview
  pane's cell-width budget and must end with an SGR reset when it contains ANSI.
- Raw pane data in caches, hub responses, SSH responses, transcripts, and
  clipboard paths must remain unchanged.
- The fix is incomplete until it is committed, pushed to `origin/main`, shipped
  in a verified GitHub release, installed on the AWS workstation, and exercised
  there against a hub preview.

## Approaches Considered

### 1. Shared render-boundary safety helpers (selected)

Sanitize capture content and enforce final width only when a TUI renderer emits
the preview. Local, SSH remote, and hub renderers call the same helpers. This
keeps transport and cache semantics unchanged while closing every TUI source.

### 2. Sanitize when fetching or caching previews

This would remove unsafe bytes early, but it would also make cached content no
longer represent the source pane and could change clipboard, web, or diagnostic
behavior. It also duplicates policy across local, SSH, and hub fetch paths.

### 3. Sanitize on the remote or hub server

This protects hub payload consumers but does not fix SSH or older server/client
combinations consistently. It also changes the wire contract from raw pane data
to presentation-specific data.

## Design

Add two focused render-boundary helpers in `internal/ui/home.go`:

1. A captured-content helper processes each source line with the existing
   control-character, display-erase, theme-background, cell-width truncation,
   and ANSI-reset rules.
2. A final preview-width helper applies the cell-width budget and ANSI reset to
   every line of a completed preview, including title and metadata lines.

The local renderer uses the same helpers without changing its scrolling and
empty-line behavior. `renderRemotePreview` and `renderHubPreview` sanitize the
cached raw pane content before appending it and pass their completed result
through final width enforcement.

The preview cache continues to store the exact content returned by tmux, SSH,
or the hub. Fetch commands, hub protocol types, CLI output, web responses, and
clipboard code remain untouched.

## Testing

- Add a regression test whose ANSI-rich pane content contains horizontal tabs.
- Exercise both `renderRemotePreview` and `renderHubPreview` with the same
  fixture and assert that no tab survives, ANSI styling remains, and every
  output row stays within the requested cell width.
- Keep the existing local regression test and run it alongside the new test.
- Run the full race-enabled Go suite, `golangci-lint`, `govulncheck`, and the
  production build.
- Launch the built and released binary in an isolated TUI using copied state,
  select the affected session through the hub path, capture the frame, and
  assert zero tab bytes and stable pane alignment.

## Release and Deployment

Update the code version and changelog for `v1.11.1`, push the commits to
`origin/main`, then push tag `v1.11.1`. The existing release workflow must
publish all four platform archives plus `checksums.txt`; publication is not
accepted until the workflow succeeds and the release is non-draft.

Configure the AWS workstation to update from `lbr88/agent-deck`, install the
published Linux amd64 artifact through Agent Deck's verified updater, restart
the hub connector if required, and confirm the running executable reports
`v1.11.1`. Long-running tmux agent sessions must remain untouched.
