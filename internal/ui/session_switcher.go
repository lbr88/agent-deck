package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// switcherIdleCommit is how long the switcher waits after the last quick-cycle
// key before auto-committing to the highlighted session. It approximates
// "switch when I let go of the key" — terminals do not deliver key-release
// events, so we commit on a brief idle instead. Enter commits immediately; Esc
// cancels; arrow-key navigation cancels the auto-commit (manual mode).
const switcherIdleCommit = 1 * time.Second

// switcherRepeatGuard is the minimum gap between accepted quick-cycle
// advances. Terminal auto-repeat fires far faster than this (~15–40ms), so
// holding the key down advances at most a step or two instead of spinning
// through every session; deliberate taps (~100ms+ apart) all register.
const switcherRepeatGuard = 80 * time.Millisecond

// SessionSwitcher is the session switcher overlay. Ctrl+Tab opens it from the
// overview or an attached session and immediately selects the most recent other
// session. Ctrl+Tab / Ctrl+Shift+Tab cycle forward / backward; the configurable
// Ctrl-letter fallback still opens on the origin and uses Ctrl+S / Ctrl+A for
// cycling. Arrow keys browse, and the highlight is attached on Enter or after a
// brief idle once quick cycling starts.
type SessionSwitcher struct {
	visible       bool
	width, height int
	sessions      []*session.Instance // active sessions, MRU-ordered
	cursor        int
	fromID        string            // session the picker was opened from
	subtitles     map[string]string // sessionID -> dim conversation/pane title (matches the overview)
	// labels carries the render-snapshot state per session so View() can build
	// rows lock-free (#1753): the switcher opens on the event loop right after
	// a switch-return, and per-row Instance.mu reads could block behind a
	// mid-sweep UpdateStatus writer for seconds. Nil (e.g. in direct Show
	// callers/tests) falls back to the Instance getters.
	labels           map[string]sessionRenderState
	reattachOnCancel bool // Esc re-attaches to fromID (opened while attached) vs. just closing (opened from the overview)
	// commitGen is bumped on every open/cycle/cancel so a stale idle-commit
	// timer (scheduled before a later keypress) is ignored when it fires. It is
	// intentionally monotonic — never reset — so a timer from a previous
	// switcher session can never collide with a new one.
	commitGen int
	// lastCycleAt is the time of the last accepted quick-cycle advance, used
	// to swallow terminal key-repeat (see switcherRepeatGuard).
	lastCycleAt time.Time
}

// bumpCommitGen advances and returns the commit generation. armSwitcherCommit
// schedules a timer tagged with the returned value; calling it WITHOUT
// scheduling a new timer (e.g. on arrow navigation) simply invalidates any
// pending auto-commit. Only a timer carrying the current generation commits
// (see Home.handleSwitcherCommit).
func (s *SessionSwitcher) bumpCommitGen() int {
	s.commitGen++
	return s.commitGen
}

// cycle advances the highlight one step (forward => next, else prev) unless the
// previous accepted advance was within switcherRepeatGuard, which swallows
// key-repeat from a held shortcut. It reports whether it moved.
func (s *SessionSwitcher) cycle(forward bool, now time.Time) bool {
	if !s.lastCycleAt.IsZero() && now.Sub(s.lastCycleAt) < switcherRepeatGuard {
		return false
	}
	s.lastCycleAt = now
	if forward {
		s.next()
	} else {
		s.prev()
	}
	return true
}

// NewSessionSwitcher creates a new (hidden) session switcher.
func NewSessionSwitcher() *SessionSwitcher { return &SessionSwitcher{} }

// Show builds the switchable list with the origin first and every other session
// in MRU order. This makes the first forward step the most recently used other
// session even when the overview cursor was not already on the newest row. It
// pre-selects the origin, so an immediate Enter still drops the user right back
// where they were.
// subtitles maps a session ID to its dim conversation/pane title (the same text
// the overview shows next to an entry); a nil map renders no subtitles. It
// returns false (and stays hidden) when fewer than two sessions are available —
// there is nothing to switch between, so the caller falls back to a normal detach.
//
// Scope: the switcher is local-only by design — it takes local
// *session.Instance rows and re-attaches via the local tmux attach loop. Remote
// (SSH) sessions reach a session over a different attach path, so they are
// intentionally excluded from the picker for now (see
// TestSessionSwitcher_RemoteSessionsUnsupported); supporting them needs a remote
// re-attach path and is tracked as a follow-up.
func (s *SessionSwitcher) Show(fromID string, allInstances []*session.Instance, subtitles map[string]string) bool {
	list := make([]*session.Instance, 0, len(allInstances))
	for _, inst := range allInstances {
		if inst == nil {
			continue
		}
		// Mirror the send-output picker: only switchable (live) sessions.
		switch inst.GetStatusThreadSafe() {
		case session.StatusError, session.StatusStopped:
			continue
		}
		list = append(list, inst)
	}
	if len(list) < 2 {
		// Nothing to switch between. Clear any prior selection so a switcher that
		// was already open (e.g. live-session count just dropped below two) does
		// not linger on stale state — Show's contract is "stays hidden" here.
		s.Hide()
		return false
	}

	// Most-recently-accessed first, then move the origin to the front while
	// preserving the remaining MRU order. The just-detached session is normally
	// already first because attachSession marks it accessed; the move also makes
	// overview-triggered switching correct when the cursor is on an older row.
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].LastAccessedAt.After(list[j].LastAccessedAt)
	})

	origin := -1
	for i, inst := range list {
		if inst.ID == fromID {
			origin = i
			break
		}
	}
	if origin > 0 {
		from := list[origin]
		copy(list[1:origin+1], list[:origin])
		list[0] = from
	}

	s.visible = true
	s.sessions = list
	s.cursor = 0
	s.fromID = fromID
	s.subtitles = subtitles
	return true
}

// Hide closes the switcher and resets state. commitGen is intentionally left
// untouched (monotonic) so a pending timer from this session can't commit after
// a future re-open.
func (s *SessionSwitcher) Hide() {
	s.visible = false
	s.cursor = 0
	s.sessions = nil
	s.fromID = ""
	s.subtitles = nil
	s.labels = nil
	s.reattachOnCancel = false
	s.lastCycleAt = time.Time{}
}

// IsVisible reports whether the switcher is currently shown.
func (s *SessionSwitcher) IsVisible() bool { return s != nil && s.visible }

// SetSize updates the dimensions used for centering.
func (s *SessionSwitcher) SetSize(w, h int) {
	s.width = w
	s.height = h
}

// GetSelected returns the highlighted session, or nil.
func (s *SessionSwitcher) GetSelected() *session.Instance {
	if len(s.sessions) == 0 || s.cursor < 0 || s.cursor >= len(s.sessions) {
		return nil
	}
	return s.sessions[s.cursor]
}

func (s *SessionSwitcher) next() {
	if len(s.sessions) > 0 {
		s.cursor = (s.cursor + 1) % len(s.sessions)
	}
}

func (s *SessionSwitcher) prev() {
	if len(s.sessions) > 0 {
		s.cursor = (s.cursor - 1 + len(s.sessions)) % len(s.sessions)
	}
}

// View renders the centered switcher box.
func (s *SessionSwitcher) View() string {
	if !s.visible {
		return ""
	}

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorAccent)
	selectedStyle := lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true)
	normalStyle := lipgloss.NewStyle().
		Foreground(ColorText)
	footerStyle := lipgloss.NewStyle().
		Foreground(ColorComment).
		Italic(true)

	header := "Switch session"
	// The primary forward/back cycle keys are fixed. The configurable Ctrl-letter
	// fallback remains available but is intentionally secondary. Esc, however,
	// re-attaches to the origin only when the picker was opened while attached;
	// from the overview it just closes, so the hint reflects that. Built up front
	// so the footer width feeds the natural-width measurement below.
	escHint := "Esc close"
	if s.reattachOnCancel {
		escHint = "Esc back"
	}
	footerCycle := "Ctrl+Tab next · Ctrl+Shift+Tab prev"
	footerNav := "↑/↓ browse · Enter attach · " + escHint

	// Precompute each row once so we can measure the widest row (to auto-expand
	// the dialog) and render without recomputing. label/subtitle route through
	// the same helper the overview uses (sessionDisplayLabels) so the two render
	// paths stay consistent: an auto-named session shows Claude's live/persisted
	// task description as the title (and no subtitle), not its random handle.
	type switcherRow struct {
		marker     string
		labelStyle lipgloss.Style
		indicator  string
		title      string // label + tool (plain text)
		subtitle   string
		prefix     int // cells before the title: marker + indicator + space
	}
	rows := make([]switcherRow, len(s.sessions))

	// natural is the widest line's content width (excluding the box border +
	// horizontal padding). The dialog grows to fit it, capped at the terminal.
	natural := max(cellWidth(header), cellWidth(footerCycle), cellWidth(footerNav))
	for i, inst := range s.sessions {
		var indicator, label, subtitle string
		if state, ok := s.labels[inst.ID]; ok {
			// Lock-free path (#1753): status and labels from the render
			// snapshot captured at open time — no Instance.mu per row.
			indicator = statusIndicator(state.status)
			label, subtitle = sessionDisplayLabelsFromState(state)
		} else {
			indicator = statusIndicator(inst.GetStatusThreadSafe())
			label, subtitle = sessionDisplayLabels(inst, s.subtitles[inst.ID])
		}
		tool := ""
		if inst.Tool != "" {
			tool = fmt.Sprintf(" (%s)", inst.Tool)
		}
		title := label + tool

		marker := "  "
		labelStyle := normalStyle
		if i == s.cursor {
			marker = "> "
			labelStyle = selectedStyle
		}
		prefix := cellWidth(marker) + cellWidth(indicator) + 1 // marker + indicator + space
		rows[i] = switcherRow{marker: marker, labelStyle: labelStyle, indicator: indicator, title: title, subtitle: subtitle, prefix: prefix}

		rowWidth := prefix + cellWidth(title)
		if subtitle != "" {
			rowWidth += 1 + cellWidth(subtitle) // space + subtitle
		}
		natural = max(natural, rowWidth)
	}

	// Grow the dialog to fit the widest row, but never below the comfortable
	// default and never past the terminal width (leaving a small margin so the
	// bordered box doesn't touch the screen edges). +4 covers the Padding(1,2).
	const minDialogWidth = 56
	dialogWidth := max(minDialogWidth, natural+4)
	if s.width > 0 {
		// Clamp to the terminal: s.width-4 keeps the bordered box one cell off
		// each edge. The floor matches contentWidth's (10) rather than the
		// comfortable default, so a very narrow terminal still wins the clamp
		// instead of overflowing.
		dialogWidth = min(dialogWidth, max(10, s.width-4))
	}
	// Content area inside the rounded border + Padding(1,2): horizontal padding
	// eats 4 cells. Truncating rows to this keeps long titles/subtitles from
	// wrapping, which would break the centered box layout.
	contentWidth := max(10, dialogWidth-4)

	var lines []string
	lines = append(lines, titleStyle.Render(header))
	lines = append(lines, "")

	for _, r := range rows {
		title := r.title
		// Truncate the title only if it alone would overflow the row — i.e. when
		// the dialog hit the terminal-width cap. Below the cap the box grew to fit.
		if budget := contentWidth - r.prefix; budget > 0 && cellWidth(title) > budget {
			title = cellTruncate(title, budget, "…")
		}
		line := r.marker + r.indicator + " " + r.labelStyle.Render(title)

		// Append the dim conversation/pane title (same text the overview shows
		// next to an entry), truncated to the space left on the row.
		if r.subtitle != "" {
			used := r.prefix + cellWidth(title) // title may have been truncated above
			if remaining := contentWidth - used - 1; remaining >= 6 {
				line += " " + DimStyle.Render(cellTruncate(r.subtitle, remaining, "…"))
			}
		}
		lines = append(lines, line)
	}

	lines = append(lines, "")
	lines = append(lines, footerStyle.Render(footerCycle))
	lines = append(lines, footerStyle.Render(footerNav))

	content := strings.Join(lines, "\n")

	box := DialogBoxStyle.
		Width(dialogWidth).
		Render(content)

	return centerInScreen(box, s.width, s.height)
}
