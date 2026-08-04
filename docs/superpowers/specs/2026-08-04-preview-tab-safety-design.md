# Preview Tab Safety Design

## Problem

`tmux capture-pane -e` may preserve horizontal tabs in pane snapshots. The
`iac-cicd` Codex session currently contains hundreds of these tabs in a large,
ANSI-colored Go diff. Agent Deck preserves the tabs in preview content, while
its ANSI width calculation treats them as zero-width. A real terminal instead
advances each tab to an absolute tab stop, so preview rows exceed the width
Agent Deck reserved for them, wrap, and corrupt the joined TUI layout.

## Considered Approaches

1. **Expand tabs at the visual-preview boundary (chosen).** Convert each tab to
   the spaces needed to reach the next eight-column tab stop while preserving
   ANSI styling. Width measurement and truncation then operate on explicit
   cells, and copied pane output remains unchanged.
2. **Drop tabs.** This prevents terminal movement but collapses indentation and
   makes diffs harder to read.
3. **Change tmux capture globally.** Escaping or flattening all captured control
   data would affect prompt detection, clipboard behavior, web output, and
   session lifecycle code that already relies on ANSI-rich captures.

## Design

Keep the capture and cache layers unchanged. In
`stripControlCharsPreserveANSI`, which is already the safety boundary for
preview and notes rendering, replace horizontal tabs with ordinary spaces.
Expansion uses terminal tab stops of eight cells and calculates the current
visible column without counting ANSI escape sequences. Newlines reset the
column. Existing behavior for ANSI SGR, newlines, and rejected C0 controls is
preserved.

The transformation happens before both per-line and final-frame width checks,
so no raw tab can reach Lip Gloss or the outer terminal. Clipboard
normalization is intentionally unchanged and may retain tabs because copied
text is not rendered as part of the TUI.

## Testing

Add a regression test using ANSI-colored, tab-indented Codex diff lines that
matches the live failure mode. The test must fail before the production change
because a tab survives rendering. After the fix it must prove:

- no horizontal tab reaches the rendered preview;
- indentation expands to the correct eight-column tab stops;
- every rendered row remains within the preview width;
- ANSI styling remains present and existing preview safety tests continue to
  pass.

Run the focused regression test, the complete `internal/ui` package tests, the
full Go test suite, lint, and a production build. Install the verified binary
locally, restart only Agent Deck itself if required, and validate the current
`iac-cicd` pane snapshot through the same preview renderer without modifying or
restarting the session.

## Non-Goals

- Reformatting the underlying Codex terminal output.
- Changing copied terminal text or hub/web output in this fix.
- Altering tmux capture semantics or terminal tab width globally.
