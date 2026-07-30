# Codex Resume Loading Experience Design

## Context

The existing Codex resume-readiness state machine correctly keeps Agent Deck
responsive, polls a blank tmux pane in the background, and attaches only after
Codex renders its first frame. It fixes the empty-terminal failure described in
`2026-07-30-codex-resume-readiness-design.md`.

The remaining problem is feedback. During a large resume, the preview currently
shows a generic “Resuming Codex session” animation and elapsed time. It does not
say that Codex is restoring conversation history, visibly confirm that Enter
queued an open request, or notify the user when a background resume becomes
ready after selection moved elsewhere.

## Goals

1. Make a multi-minute Codex resume look active rather than broken.
2. Explain the known wait without claiming progress Agent Deck cannot measure.
3. Confirm whether the session will auto-open when ready.
4. Let the user navigate normally while the resume continues.
5. Notify the user when a background resume becomes ready without stealing
   focus or moving the session-list cursor.

This change improves the CLI TUI only. It does not attempt to make Codex rebuild
history faster, change Codex persistence formats, or change web terminal
behavior.

## Loading Presentation

During a tracked Codex resume, the preview uses a dedicated message:

- Title: `Restoring Codex history`
- Elapsed time: a stable, human-readable duration such as `18s` or `2m 14s`
- Before ten seconds: `Waiting for Codex to render its first frame.`
- From ten seconds onward: `Large Codex sessions can take several minutes to restore.`

The action line reflects the generation-matched attach request:

- No queued request: `Press Enter to open automatically when ready.`
- Queued request: `Will open automatically while this session remains selected.`

The copy must not expose guessed percentages, estimated completion times, or
phases derived from Codex logs. The spinner and elapsed clock continue updating
through the existing Bubble Tea animation tick, so navigation remains
non-blocking.

Other tools and new-session launch animations retain their existing wording.

## Ready Notification

If the first Codex frame appears while that session is still selected and an
attach request is queued, Agent Deck attaches exactly once as it does today.

If selection moved elsewhere, Agent Deck must not attach or move the cursor. It
instead:

1. records an acknowledgement-required ready notice for that session;
2. replaces the row's normal status glyph with a green ready check without
   adding row width; and
3. shows a footer notice that the named session is ready to open.

The ready notice clears when the session is opened, restarted again, or
deleted. Starting another resume also replaces any earlier ready notice. The
notice is state only; it must not reorder the session list or modify persisted
session metadata.

## State and Error Handling

The existing generation timestamp remains the authority for readiness and
attach intent. Loading text reads that state but does not create a second
readiness mechanism.

A small in-memory ready-notice map tracks sessions whose delayed readiness was
not auto-attached. Stale readiness messages cannot create notices for a newer
generation. Restart failure and the existing ten-minute timeout continue to
clear the resume and attach state and show the current retry guidance.

No reads from Codex SQLite logs or rollout JSONL files are added. Those stores
are large, internal, and unnecessary for honest loading feedback.

## Testing

Implementation follows test-first development:

1. render tests pin the Codex-specific title, elapsed duration, ten-second
   explanatory threshold, and queued/open action line;
2. state tests prove a delayed ready event attaches only while the same session
   remains selected;
3. state and row-render tests prove navigation away creates a ready notice
   without moving the cursor and without increasing row width;
4. lifecycle tests prove open, restart, and delete clear the notice;
5. a controlled delayed-pane test keeps the probe empty before emitting a
   frame, proving the TUI remains in the loading state and transitions exactly
   once; and
6. the focused UI suite, full Go test suite, race-sensitive tests, build, and
   local binary installation must succeed before completion is reported.
