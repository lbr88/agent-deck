// Package ui provides the Bubble Tea TUI for agent-deck.
// keyboard_compat.go implements compatibility helpers for the Kitty keyboard
// protocol (CSI u encoding) used by Wayland compositors and modern terminals
// such as Ghostty, Foot, and Alacritty.
//
// Background: Bubble Tea v1.3.10 does not parse Kitty keyboard protocol
// sequences. Agent Deck requests extended keyboard reporting so terminals can
// distinguish combinations such as Ctrl+Tab and Shift+Enter, then translates
// the resulting sequences before Bubble Tea sees them. This file provides:
//
//  1. Keyboard-mode helpers that negotiate Kitty CSI u and xterm
//     modifyOtherKeys while the TUI owns the terminal, then restore the shell.
//
//  2. ParseCSIu — a CSI u sequence parser used by the compatibility reader.
//
//  3. NewCSIuReader — a reader that translates CSI u sequences to legacy bytes
//     on the fly for Bubble Tea.
package ui

import (
	"bytes"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"github.com/asheshgoplani/agent-deck/internal/termreply"
	tea "github.com/charmbracelet/bubbletea"
)

// shiftEnterMarker is the Unicode Private-Use-Area rune used to relay
// Shift+Enter from the CSI u / xterm-modifyOtherKeys parser through
// Bubble Tea v1.3.10 (which has no native Shift+Enter representation) and
// out to home.go's keybinding switch. It is emitted in UTF-8 form by the
// csiuReader; Bubble Tea decodes the UTF-8 to a KeyRunes message; and
// Home.normalizeMainKey rewrites it back to the canonical "shift+enter"
// string the dispatch switch matches on. See issue #1093.
// U+E5E5 sits in the Basic Multilingual Plane Private Use Area
// (U+E000..U+F8FF), which Unicode reserves for application-private use
// and no standard keyboard or input method can produce.
const (
	shiftEnterMarker   rune = 0xE5E5
	ctrlTabMarker      rune = 0xE5E6
	ctrlShiftTabMarker rune = 0xE5E7
	ctrlReleaseMarker  rune = 0xE5E8
	ctrlTabFallback    rune = 0xE5E9
	ctrlShiftFallback  rune = 0xE5EA
)

// DisableKittyKeyboard writes the escape sequence that pops the Kitty keyboard
// protocol stack, restoring the previous keyboard mode. If nothing was on the
// stack, this is a safe no-op. After this call, Kitty-protocol-aware terminals
// stop sending CSI u sequences and revert to legacy key reporting. Terminals
// that do not support the protocol ignore the sequence.
func DisableKittyKeyboard(w io.Writer) {
	_, _ = io.WriteString(w, "\x1b[<u")
}

// EnableTUIKeyboardProtocolsCmd returns a tea.Cmd that resets any keyboard
// mode left by the foreground process and enables the protocols consumed by
// the dashboard on the given writer.
//
// Bubble Tea invokes Home.Init after entering its alternate screen and invokes
// attach callbacks after restoring that screen. Kitty-compatible terminals
// keep separate keyboard state for the main and alternate screens, so both
// boundaries must enable the protocol after the alternate screen is active.
//
// Takes a writer so tests can substitute a buffer for os.Stdout.
func EnableTUIKeyboardProtocolsCmd(w io.Writer) tea.Cmd {
	return func() tea.Msg {
		EnableTUIKeyboardProtocols(w)
		return nil
	}
}

// EnableTUIKeyboardProtocolsAfterSwitchCmd restores dashboard keyboard mode
// after an attached session switch. When the attach reader already consumed
// the final Ctrl release in the same read as Ctrl+Tab, the returned command
// relays that release only after protocol restoration has completed.
func EnableTUIKeyboardProtocolsAfterSwitchCmd(w io.Writer, ctrlReleased bool) tea.Cmd {
	return func() tea.Msg {
		EnableTUIKeyboardProtocols(w)
		if !ctrlReleased {
			return nil
		}
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ctrlReleaseMarker}}
	}
}

func ctrlReleaseHandoffCallbacks(w io.Writer) (begin, cancel func()) {
	return func() { EnableKittyKeyboard(w) }, func() { DisableKittyKeyboard(w) }
}

// EnableKittyKeyboard writes the escape sequence that pushes the Kitty keyboard
// flags consumed by NewCSIuReader onto the protocol stack: disambiguated escape
// codes (1), event types (2), all keys as escape codes (8), and associated text
// (16). The latter two are required for standalone Ctrl release events without
// losing shifted punctuation or keyboard-layout text while all-key reporting is
// active.
// Call this before attaching to a session that needs Kitty keyboard support
// (e.g. Claude Code). Pair with DisableKittyKeyboard to pop the stack on
// return.
func EnableKittyKeyboard(w io.Writer) {
	_, _ = io.WriteString(w, "\x1b[>27u")
}

// EnableModifyOtherKeys writes the xterm escape sequence that requests
// modifyOtherKeys mode 1: terminals supporting the protocol (iTerm2, xterm,
// Konsole, …) start sending modified keys — including the previously
// indistinguishable Shift+Enter — as CSI 27;<modifier>;<codepoint>~
// sequences rather than the legacy unmodified byte. Unmodified keys still
// arrive as their normal bytes, so plain Enter is unaffected.
//
// This is the upstream half of the #1093 fix: without it, a fresh agent-deck
// launch in iTerm2 sees plain '\r' for both Enter and Shift+Enter and cannot
// distinguish them. With it, Shift+Enter arrives as \x1b[27;2;13~ and our
// csiuReader relays it through to home.go as "shift+enter".
//
// Pair with DisableModifyOtherKeys on TUI exit so the user's shell prompt
// returns to default key-reporting behavior.
func EnableModifyOtherKeys(w io.Writer) {
	_, _ = io.WriteString(w, "\x1b[>4;1m")
}

// DisableModifyOtherKeys writes the xterm escape sequence that returns the
// terminal to default key reporting (modifyOtherKeys mode 0). Call on TUI
// exit so the user's shell behaves normally again.
func DisableModifyOtherKeys(w io.Writer) {
	_, _ = io.WriteString(w, "\x1b[>4;0m")
}

// RestoreKittyKeyboard writes the escape sequence that pops the keyboard mode
// stack, restoring the terminal to its previous keyboard mode. Call this when
// the TUI exits so that the terminal returns to normal operation.
func RestoreKittyKeyboard(w io.Writer) {
	_, _ = io.WriteString(w, "\x1b[<u")
}

// EnableTUIKeyboardProtocols resets any leaked Kitty keyboard mode and enables
// the terminal protocols consumed by NewCSIuReader while the dashboard owns the
// terminal. Keeping this sequence in one helper prevents startup negotiation
// from drifting away from the input parser's capabilities.
func EnableTUIKeyboardProtocols(w io.Writer) {
	DisableKittyKeyboard(w)
	EnableKittyKeyboard(w)
	EnableModifyOtherKeys(w)
}

// DisableTUIKeyboardProtocols restores the terminal protocols changed by
// EnableTUIKeyboardProtocols before control returns to the user's shell.
func DisableTUIKeyboardProtocols(w io.Writer) {
	DisableModifyOtherKeys(w)
	RestoreKittyKeyboard(w)
}

// ParseCSIu parses a Kitty keyboard protocol (CSI u) escape sequence and
// returns the equivalent tea.KeyMsg. Returns nil if the data is not a valid
// CSI u sequence.
//
// The full CSI u format is:
//
//	ESC '[' <codepoint> [';' <modifier> [':' <event>]
//	    [';' <associated-text-codepoints>]] 'u'
//
// Modifier encoding (1 + bitmask):
//
//	1 = no modifier
//	2 = shift      (1 + 1)
//	3 = alt        (1 + 2)
//	4 = shift+alt  (1 + 1 + 2)
//	5 = ctrl       (1 + 4)
//	6 = shift+ctrl (1 + 1 + 4)
func ParseCSIu(data []byte) *tea.KeyMsg {
	msg, _ := parseCSIuEvent(data)
	return msg
}

// parseCSIuEvent parses a CSI-u key event. handled distinguishes a valid event
// that intentionally produces no Bubble Tea key (notably key releases) from an
// invalid sequence that must pass through untouched.
func parseCSIuEvent(data []byte) (*tea.KeyMsg, bool) {
	// Minimum sequence: ESC [ <digit> u  (4 bytes)
	if len(data) < 4 {
		return nil, false
	}
	if data[0] != 0x1b || data[1] != '[' {
		return nil, false
	}
	// Must end with 'u'
	if data[len(data)-1] != 'u' {
		return nil, false
	}

	// Parse the interior. Alternate key codes are not requested, but tolerate
	// them by using the primary codepoint before the first colon.
	interior := data[2 : len(data)-1]
	fields := bytes.Split(interior, []byte{';'})
	if len(fields) < 1 || len(fields) > 3 {
		return nil, false
	}
	codepointField := fields[0]
	if colon := bytes.IndexByte(codepointField, ':'); colon >= 0 {
		codepointField = codepointField[:colon]
	}
	codepoint := parseDecimalBytes(codepointField)
	if codepoint < 0 {
		return nil, false
	}

	modifier := 1  // default: no modifier
	eventType := 1 // press is the protocol default
	if len(fields) >= 2 {
		modifierEvent := bytes.Split(fields[1], []byte{':'})
		if len(modifierEvent) < 1 || len(modifierEvent) > 2 {
			return nil, false
		}
		modifier = parseDecimalBytes(modifierEvent[0])
		if modifier < 1 {
			return nil, false
		}
		if len(modifierEvent) == 2 {
			eventType = parseDecimalBytes(modifierEvent[1])
			if eventType < 1 || eventType > 3 {
				return nil, false
			}
		}
	}

	// Decode modifier bitmask (modifier = 1 + bitmask)
	bitmask := modifier - 1
	shiftHeld := (bitmask & 0x01) != 0
	altHeld := (bitmask & 0x02) != 0
	ctrlHeld := (bitmask & 0x04) != 0

	// With report-all-keys enabled, modifier keys get their own events. Emit one
	// private marker only when the last physical Ctrl key is released; if the
	// other Ctrl remains down, the protocol keeps ctrlHeld set. All other
	// releases and modifier-only events are consumed so Bubble Tea never treats
	// them as duplicate presses or private-use text.
	const (
		leftShiftKey   = 57441
		leftControlKey = 57442
		rightMetaKey   = 57452
		rightCtrlKey   = 57448
	)
	if eventType == 3 {
		if (codepoint == leftControlKey || codepoint == rightCtrlKey) && !ctrlHeld {
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ctrlReleaseMarker}}
			return &msg, true
		}
		return nil, true
	}
	if codepoint >= leftShiftKey && codepoint <= rightMetaKey {
		return nil, true
	}

	// Associated text is authoritative for text-producing keys in report-all
	// mode. It preserves shifted punctuation and the active keyboard layout;
	// the primary codepoint is intentionally the unshifted physical key.
	if len(fields) == 3 {
		textFields := bytes.Split(fields[2], []byte{':'})
		runes := make([]rune, 0, len(textFields))
		for _, field := range textFields {
			value := parseDecimalBytes(field)
			if value < 0 || value > utf8.MaxRune || !utf8.ValidRune(rune(value)) {
				return nil, false
			}
			runes = append(runes, rune(value))
		}
		if len(runes) > 0 {
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: runes, Alt: altHeld}
			return &msg, true
		}
	}

	// Map well-known control codepoints to tea key types.
	switch codepoint {
	case 13: // CR = Enter
		if shiftHeld {
			// Bubble Tea v1.3.10 has no Shift+Enter representation, so we
			// relay it via a Private-Use-Area rune. home.go's
			// normalizeMainKey rewrites this back to "shift+enter". See
			// issue #1093.
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{shiftEnterMarker}, Alt: altHeld}
			return &msg, true
		}
		msg := tea.KeyMsg{Type: tea.KeyEnter, Alt: altHeld}
		return &msg, true
	case 9: // HT = Tab
		if ctrlHeld && !altHeld {
			marker := ctrlTabMarker
			if shiftHeld {
				marker = ctrlShiftTabMarker
			}
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{marker}}
			return &msg, true
		}
		if shiftHeld {
			msg := tea.KeyMsg{Type: tea.KeyShiftTab, Alt: altHeld}
			return &msg, true
		}
		msg := tea.KeyMsg{Type: tea.KeyTab, Alt: altHeld}
		return &msg, true
	case 27: // ESC
		msg := tea.KeyMsg{Type: tea.KeyEsc, Alt: altHeld}
		return &msg, true
	case 127: // DEL = Backspace
		msg := tea.KeyMsg{Type: tea.KeyBackspace, Alt: altHeld}
		return &msg, true
	case 32: // Space
		msg := tea.KeyMsg{Type: tea.KeySpace, Alt: altHeld}
		return &msg, true
	}

	// Ctrl-modified regular keys: Ctrl+a = 0x01, Ctrl+b = 0x02, …
	if ctrlHeld && codepoint >= 97 && codepoint <= 122 {
		// 'a'=97 -> ctrl sequence 1, 'b'=98 -> 2, …
		ctrlRune := rune(codepoint - 96)
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ctrlRune}, Alt: altHeld}
		return &msg, true
	}

	// Regular rune: apply shift to lowercase letters.
	r := rune(codepoint) // #nosec G115 -- codepoint parsed from CSI/xterm sequence, validated >= 0 above
	if !utf8.ValidRune(r) {
		return nil, false
	}
	if shiftHeld && r >= 'a' && r <= 'z' {
		r = r - 'a' + 'A'
	}

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: altHeld}
	return &msg, true
}

// ParseModifyOtherKeys parses an xterm modifyOtherKeys escape sequence and
// returns the equivalent tea.KeyMsg. Returns nil if the data is not a valid
// modifyOtherKeys sequence.
//
// The modifyOtherKeys format is:  ESC '[' '27' ';' <modifier> ';' <codepoint> '~'
//
// tmux with extended-keys sends this format. The modifier encoding is the same
// as CSI u (1 + bitmask).
func ParseModifyOtherKeys(data []byte) *tea.KeyMsg {
	// Minimum: ESC [ 2 7 ; <mod> ; <code> ~  (9 bytes)
	if len(data) < 9 {
		return nil
	}
	if data[0] != 0x1b || data[1] != '[' {
		return nil
	}
	if data[len(data)-1] != '~' {
		return nil
	}

	// Interior between '[' and '~': must be "27;<modifier>;<codepoint>"
	interior := data[2 : len(data)-1]

	// Split on semicolons: expect exactly ["27", modifier, codepoint]
	parts := bytes.Split(interior, []byte{';'})
	if len(parts) != 3 {
		return nil
	}
	prefix := parseDecimalBytes(parts[0])
	if prefix != 27 {
		return nil
	}
	modifier := parseDecimalBytes(parts[1])
	if modifier < 1 {
		return nil
	}
	codepoint := parseDecimalBytes(parts[2])
	if codepoint < 0 {
		return nil
	}

	// Reuse the same modifier logic as ParseCSIu
	bitmask := modifier - 1
	shiftHeld := (bitmask & 0x01) != 0
	altHeld := (bitmask & 0x02) != 0
	ctrlHeld := (bitmask & 0x04) != 0

	switch codepoint {
	case 13:
		if shiftHeld {
			// See ParseCSIu / shiftEnterMarker comment. Issue #1093.
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{shiftEnterMarker}, Alt: altHeld}
			return &msg
		}
		msg := tea.KeyMsg{Type: tea.KeyEnter, Alt: altHeld}
		return &msg
	case 9:
		if ctrlHeld && !altHeld {
			// xterm modifyOtherKeys has no release-event protocol. Use a
			// distinct marker so the switcher can retain its idle-commit
			// compatibility path instead of waiting forever for Ctrl release.
			marker := ctrlTabFallback
			if shiftHeld {
				marker = ctrlShiftFallback
			}
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{marker}}
			return &msg
		}
		if shiftHeld {
			msg := tea.KeyMsg{Type: tea.KeyShiftTab, Alt: altHeld}
			return &msg
		}
		msg := tea.KeyMsg{Type: tea.KeyTab, Alt: altHeld}
		return &msg
	case 27:
		msg := tea.KeyMsg{Type: tea.KeyEsc, Alt: altHeld}
		return &msg
	case 127:
		msg := tea.KeyMsg{Type: tea.KeyBackspace, Alt: altHeld}
		return &msg
	case 32:
		msg := tea.KeyMsg{Type: tea.KeySpace, Alt: altHeld}
		return &msg
	}

	if ctrlHeld && codepoint >= 97 && codepoint <= 122 {
		ctrlRune := rune(codepoint - 96)
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ctrlRune}, Alt: altHeld}
		return &msg
	}

	r := rune(codepoint) // #nosec G115 -- codepoint parsed from CSI/xterm sequence, validated >= 0 above
	if shiftHeld && r >= 'a' && r <= 'z' {
		r = r - 'a' + 'A'
	}

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: altHeld}
	return &msg
}

// translateKittyFunctionalEvent converts the event-typed form of Kitty legacy
// functional keys back to the escape sequences Bubble Tea v1 understands.
// Examples: CSI 1;1:1 A becomes CSI A, and CSI 5;1:1 ~ becomes CSI 5 ~.
// Release events are consumed rather than replayed as duplicate key presses.
func translateKittyFunctionalEvent(data []byte) ([]byte, bool) {
	if len(data) < 6 || data[0] != 0x1b || data[1] != '[' {
		return nil, false
	}
	final := data[len(data)-1]
	switch final {
	case '~', 'A', 'B', 'C', 'D', 'E', 'F', 'H', 'P', 'Q', 'R', 'S':
		// Kitty's legacy functional-key terminators.
	default:
		return nil, false
	}
	interior := data[2 : len(data)-1]
	fields := bytes.Split(interior, []byte{';'})
	if len(fields) < 2 {
		return nil, false
	}
	modifierEvent := bytes.Split(fields[len(fields)-1], []byte{':'})
	if len(modifierEvent) != 2 {
		return nil, false
	}
	modifier := parseDecimalBytes(modifierEvent[0])
	eventType := parseDecimalBytes(modifierEvent[1])
	if modifier < 1 || eventType < 1 || eventType > 3 {
		return nil, false
	}
	if eventType == 3 {
		return nil, true
	}

	baseFields := fields[:len(fields)-1]
	out := []byte{0x1b, '['}
	if final == '~' {
		out = append(out, bytes.Join(baseFields, []byte{';'})...)
		if modifier != 1 {
			out = append(out, ';')
			out = append(out, modifierEvent[0]...)
		}
	} else if modifier != 1 {
		out = append(out, bytes.Join(baseFields, []byte{';'})...)
		out = append(out, ';')
		out = append(out, modifierEvent[0]...)
	}
	out = append(out, final)
	return out, true
}

// parseDecimalBytes parses a decimal integer from a byte slice.
// Returns -1 if the slice is empty or contains non-digit characters.
func parseDecimalBytes(b []byte) int {
	if len(b) == 0 {
		return -1
	}
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// csiuReader is an io.Reader that intercepts Kitty keyboard protocol (CSI u)
// sequences in the byte stream and translates them into legacy byte sequences
// that Bubble Tea can parse. All other bytes pass through unchanged.
type csiuReader struct {
	src         io.Reader
	outBuf      []byte // pending translated bytes to emit
	inBuf       []byte // buffered input bytes not yet processed
	err         error  // pending source error to return after draining buffers
	replyFilter termreply.Filter
	// pollFn checks whether more bytes follow a lone ESC within a timeout.
	pollFn func(time.Duration) bool
}

// csiuFileReader wraps a *os.File and overrides Read with CSI u translation.
// It preserves the *os.File interface (including Fd()) so that Bubble Tea can
// still call terminal.MakeRaw on stdin to enter raw mode.
//
// This is required because tea.WithInput(plainIoReader) loses the *os.File
// type assertion, which Bubble Tea needs to set the terminal to raw mode.
// Without raw mode, arrow keys and other escape sequences appear as visible
// text instead of being interpreted.
type csiuFileReader struct {
	*os.File
	inner *csiuReader
}

// Read implements io.Reader using the CSI u translation layer.
func (r *csiuFileReader) Read(p []byte) (int, error) {
	return r.inner.Read(p)
}

// NewCSIuReader returns a reader that wraps r and translates CSI u and
// modifyOtherKeys sequences to their legacy equivalents for Bubble Tea.
//
// If r is a *os.File, the returned reader also implements the *os.File
// interface (preserving Fd() for terminal raw-mode setup by Bubble Tea).
// If r is any other io.Reader, a plain io.Reader is returned.
func NewCSIuReader(r io.Reader) io.Reader {
	inner := &csiuReader{
		src:   r,
		inBuf: make([]byte, 0, 256),
	}
	if f, ok := r.(*os.File); ok {
		fd := int(f.Fd())
		inner.pollFn = func(timeout time.Duration) bool {
			return pollFdReady(fd, timeout)
		}
		return &csiuFileReader{File: f, inner: inner}
	}
	return inner
}

// Read implements io.Reader. It reads from the underlying source, translates
// CSI u sequences, and returns the result.
func (c *csiuReader) Read(p []byte) (int, error) {
	// Drain any previously translated bytes first.
	if len(c.outBuf) > 0 {
		n := copy(p, c.outBuf)
		c.outBuf = c.outBuf[n:]
		return n, nil
	}

	for {
		if c.err != nil {
			return 0, c.err
		}

		// Read new bytes from the source into the internal buffer.
		tmp := make([]byte, len(p))
		n, err := c.src.Read(tmp)
		if n > 0 {
			chunk := tmp[:n]
			// Always run the reply filter. Escape-string families (DCS/OSC/
			// APC/PM/SOS) are never keyboard input and can arrive outside
			// any explicit quarantine window (e.g. iTerm2 XTVERSION reply on
			// focus/resize — #731). `armed` stays tied to termreply.Active()
			// so generic CSI pass-through works for keyboard input.
			chunk = c.replyFilter.Consume(chunk, termreply.Active(), false)
			c.inBuf = append(c.inBuf, chunk...)
		}
		if err == io.EOF {
			c.inBuf = append(c.inBuf, c.replyFilter.Consume(nil, termreply.Active(), true)...)
		}

		processed := c.translate(err == io.EOF)
		if len(processed) > 0 {
			copied := copy(p, processed)
			if copied < len(processed) {
				c.outBuf = append(c.outBuf, processed[copied:]...)
			}
			if err != nil {
				c.err = err
			}
			return copied, nil
		}

		if err != nil {
			c.err = err
			return 0, err
		}

		// Flush lone ESC after 50ms poll timeout (ncurses ESCDELAY convention).
		if len(c.inBuf) == 1 && c.inBuf[0] == 0x1b && c.pollFn != nil {
			if !c.pollFn(50 * time.Millisecond) {
				p[0] = c.inBuf[0]
				c.inBuf = c.inBuf[:0]
				return 1, nil
			}
			// More bytes incoming — loop to bundle them with the ESC.
		}
	}
}

// appendLegacyKey converts a parsed key to the byte sequence Bubble Tea's
// legacy input decoder expects. In legacy terminal encoding, an ESC prefix
// carries the Alt modifier for both printable and special keys.
func appendLegacyKey(out []byte, msg *tea.KeyMsg, fallback []byte) []byte {
	start := len(out)
	if msg.Alt {
		out = append(out, 0x1b)
	}

	switch msg.Type {
	case tea.KeyEnter:
		return append(out, '\r')
	case tea.KeyTab:
		return append(out, '\t')
	case tea.KeyShiftTab:
		return append(out, []byte("\x1b[Z")...)
	case tea.KeyEsc:
		return append(out, 0x1b)
	case tea.KeyBackspace:
		return append(out, 127)
	case tea.KeySpace:
		return append(out, ' ')
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			out = append(out, []byte(string(r))...)
		}
		return out
	default:
		return append(out[:start], fallback...)
	}
}

// translate scans c.inBuf for CSI u / modifyOtherKeys sequences and replaces
// complete matches with their legacy byte representation. Incomplete ESC[
// sequences are left buffered for the next Read call.
func (c *csiuReader) translate(final bool) []byte {
	if len(c.inBuf) == 0 {
		return nil
	}

	out := make([]byte, 0, len(c.inBuf))
	i := 0
	for i < len(c.inBuf) {
		// Look for ESC '[' to start a potential CSI sequence.
		if c.inBuf[i] != 0x1b {
			out = append(out, c.inBuf[i])
			i++
			continue
		}

		// Lone ESC at buffer end: if mid-stream, buffer it for the next Read so
		// SS3 (ESC OH / ESC OF) can be detected when the next byte arrives. On
		// final flush we pass it through to avoid hanging a standalone escape.
		if i+1 >= len(c.inBuf) {
			if !final {
				break
			}
			out = append(out, c.inBuf[i])
			i++
			continue
		}

		// SS3 (ESC O) handling: rewrite ESC OH / ESC OF to ESC [H / ESC [F
		// so Bubble Tea's escSeq table recognizes Home/End. Terminals that
		// emit application-mode (DECKPAM) SS3 sequences for Home/End include
		// iTerm2's default macOS profile over direct SSH. Other SS3 sequences
		// (arrows ESC OA-D, function keys ESC OP-S) are already in Bubble
		// Tea's escSeq table and must pass through unchanged.
		if c.inBuf[i+1] == 'O' {
			if i+2 >= len(c.inBuf) {
				if !final {
					break // buffer ESC O, wait for third byte
				}
				// final: pass through ESC O as-is.
				out = append(out, c.inBuf[i:i+2]...)
				i += 2
				continue
			}
			switch c.inBuf[i+2] {
			case 'H':
				out = append(out, 0x1b, '[', 'H')
				i += 3
				continue
			case 'F':
				out = append(out, 0x1b, '[', 'F')
				i += 3
				continue
			}
			// Other ESC O* sequence — Bubble Tea handles natively.
			out = append(out, c.inBuf[i:i+3]...)
			i += 3
			continue
		}

		if c.inBuf[i+1] != '[' {
			out = append(out, c.inBuf[i])
			i++
			continue
		}

		// We have ESC '['. Scan forward for the CSI final byte.
		// CSI final bytes are in the range 0x40-0x7E (@ through ~).
		// Only 'u' and '~' get special handling; everything else passes through.
		j := i + 2
		for j < len(c.inBuf) && (c.inBuf[j] < 0x40 || c.inBuf[j] > 0x7E) {
			j++
		}

		if j >= len(c.inBuf) {
			if final {
				out = append(out, c.inBuf[i:]...)
				i = len(c.inBuf)
			}
			break
		}

		seq := c.inBuf[i : j+1]
		if c.inBuf[j] != 'u' {
			if translated, handled := translateKittyFunctionalEvent(seq); handled {
				out = append(out, translated...)
				i = j + 1
				continue
			}
			// Check for modifyOtherKeys format: ESC[27;modifier;codepoint~
			if c.inBuf[j] == '~' {
				if msg := ParseModifyOtherKeys(seq); msg != nil {
					out = appendLegacyKey(out, msg, seq)
					i = j + 1
					continue
				}
			}
			// Not a CSI u or modifyOtherKeys sequence — pass through as-is
			out = append(out, seq...)
			i = j + 1
			continue
		}

		// Potential CSI u sequence: c.inBuf[i..j] inclusive
		msg, handled := parseCSIuEvent(seq)
		if !handled {
			// Not a valid CSI u, pass through
			out = append(out, seq...)
			i = j + 1
			continue
		}
		if msg != nil {
			out = appendLegacyKey(out, msg, seq)
		}

		i = j + 1
	}

	c.inBuf = append(c.inBuf[:0], c.inBuf[i:]...)
	return out
}
