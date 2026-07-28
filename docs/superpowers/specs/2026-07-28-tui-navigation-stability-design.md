# TUI Navigation Stability Design

## Context

Rapid session-list navigation currently schedules one 150 ms Bubble Tea command
for every cursor movement. Superseded commands skip the pane capture, but they
still return a stale message. Bubble Tea runs `Update` and `View` for each stale
message, creating a second wave of expensive full-list renders after a key
burst.

The list can also be repartitioned by asynchronous status data. Local
active-on-top repartitioning is paused while `isNavigating` is true, but remote
and drained hub refreshes rebuild immediately. Those rebuilds can change the row
under the numeric cursor. The preview-message backlog can also delay effective
navigation long enough for the current 700 ms navigation guard to expire.

## Goals

- Coalesce a burst of preview requests into one debounce command and one
  settled preview message.
- Never change the selected row merely because asynchronous state reordered the
  list.
- Keep volatile active-on-top ordering fixed while a navigation gesture is
  active.
- Apply deferred state changes after navigation settles, following the selected
  session by stable identity.

## Design

### Single in-flight preview debounce

`Home` will keep one in-flight preview debounce command. A cursor movement
updates the pending preview request and its timestamp. If a debounce command is
already waiting, the new movement wakes that command and returns no additional
command.

The waiting command resets its timer from the latest request timestamp. Once no
new request has arrived for 150 ms, it returns exactly one
`previewDebounceMsg`, containing the newest local, remote, or hub target. The
existing message handler remains responsible for validating that the returned
target is still current before beginning a capture.

This removes both redundant sleeping goroutines and redundant Bubble Tea
messages/renders.

### Deferred asynchronous list rebuilds

Async remote and hub refreshes will update their backing snapshots immediately,
but will not repartition `flatItems` while navigation is active. They instead
set a pending-rebuild flag.

On the first tick after navigation settles, `Home` will:

1. Capture the selected row's stable identity.
2. Rebuild and repartition the list once.
3. Restore the cursor to that identity and synchronize the viewport.
4. Clear the pending-rebuild flag.

Async rebuilds that arrive outside navigation use the same identity-preserving
path immediately. Local active-on-top status changes continue to use the
existing navigation pause, with an added assertion that the selected session
remains selected after the settled repartition.

Mouse selection and wheel navigation will use the same navigation-hot tracking
as keyboard movement so background workers cannot bypass the guard for those
inputs.

## Testing

Regression tests will prove that:

- Multiple preview requests made before settlement produce one command and the
  final request.
- The debounce timer is measured from the most recent request.
- A remote refresh cannot repartition the active-on-top list or replace the
  selected session during navigation.
- The deferred rebuild occurs after settlement and follows the selected session
  to its new row.
- Mouse selection establishes the same navigation-hot window as keyboard
  movement.

The focused UI tests, full race-enabled Go suite, lint, vulnerability scan,
production build, local install, and a controlled PTY navigation burst will be
run before completion.
