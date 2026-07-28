# TUI Navigation Stability Implementation Plan

**Goal:** Remove session-list navigation render amplification and prevent
asynchronous state changes from moving selection during scrolling.

**Architecture:** Replace per-key sleeping debounce commands with one resettable
in-flight command. Route asynchronous remote/hub list refreshes through a
navigation-aware, identity-preserving rebuild helper.

## Task 1: Preview debounce regression tests

- Add focused tests that schedule multiple local/remote/hub preview targets.
- Assert only the first request returns a command.
- Assert that command emits the newest target after the last request settles.
- Run the tests and confirm they fail against the current implementation.

## Task 2: Coalesced preview debounce

- Add pending request, request timestamp, wake channel, and in-flight state.
- Implement one resettable debounce command shared by all preview target types.
- Retain stale-target validation in the message handler.
- Run the focused tests and race detector.

## Task 3: Selection stability regression tests

- Reproduce an active-on-top status flip followed by an async remote refresh
  during navigation.
- Assert no repartition and no selected-session change during navigation.
- Assert the settled tick applies the reorder and restores the same session ID.
- Assert mouse selection establishes the worker suppression window.

## Task 4: Navigation-aware async rebuilds

- Add a pending async list rebuild flag.
- Defer remote and drained hub list rebuilds while navigation is active.
- Apply deferred work once after settlement with stable identity restoration.
- Make immediate async rebuilds preserve selection too.
- Use the common navigation activity helper for mouse selection.

## Task 5: Stable multi-key Space-jump

- Reproduce a multi-key Space-jump with an async refresh between Space and the
  hint characters.
- Keep jump mode active across the refresh and apply deferred rows on
  completion/cancel.
- Assert the hint selects its displayed row without invoking delete.

## Task 6: Verification and delivery

- Run focused UI tests with `-race`.
- Run the full race-enabled test suite and lint.
- Run `govulncheck` and `uvx zizmor .github/workflows`.
- Build and install the binary locally.
- Re-run the controlled PTY burst and compare render/message counts.
- Commit with Conventional Commits, push, and verify GitHub checks.
