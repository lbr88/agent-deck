package ui

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	testCtrlTabMarker      rune = 0xE5E6
	testCtrlShiftTabMarker rune = 0xE5E7
	testCtrlReleaseMarker  rune = 0xE5E8
	testCtrlTabFallback    rune = 0xE5E9
	testCtrlShiftFallback  rune = 0xE5EA
)

func TestEnhancedCtrlTabInputKeepsDirection(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantMarker rune
	}{
		{name: "CSI u forward", input: "\x1b[9;5u", wantMarker: testCtrlTabMarker},
		{name: "CSI u backward", input: "\x1b[9;6u", wantMarker: testCtrlShiftTabMarker},
		{name: "modifyOtherKeys forward", input: "\x1b[27;5;9~", wantMarker: testCtrlTabFallback},
		{name: "modifyOtherKeys backward", input: "\x1b[27;6;9~", wantMarker: testCtrlShiftFallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewCSIuReader(bytes.NewReader([]byte(tt.input)))
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if string(got) != string(tt.wantMarker) {
				t.Fatalf("translated %q to %q, want marker U+%04X", tt.input, string(got), tt.wantMarker)
			}
		})
	}
}

func TestEnhancedKeyboardInputReportsOnlyFinalCtrlRelease(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "left Ctrl release", input: "\x1b[57442;1:3u", want: string(testCtrlReleaseMarker)},
		{name: "right Ctrl release", input: "\x1b[57448;1:3u", want: string(testCtrlReleaseMarker)},
		{name: "one Ctrl remains held", input: "\x1b[57442;5:3u", want: ""},
		{name: "Tab release", input: "\x1b[9;5:3u", want: ""},
		{name: "letter release", input: "\x1b[97;1:3u", want: ""},
		{name: "letter press", input: "\x1b[97;1:1;97u", want: "a"},
		{name: "shifted letter uses associated text", input: "\x1b[97;2:1;65u", want: "A"},
		{name: "shifted punctuation uses associated text", input: "\x1b[49;2:1;33u", want: "!"},
		{name: "Ctrl C remains a control byte", input: "\x1b[99;5:1u", want: "\x03"},
		{name: "Enter press remains carriage return", input: "\x1b[13;1:1u", want: "\r"},
		{name: "Ctrl Tab repeat", input: "\x1b[9;5:2u", want: string(testCtrlTabMarker)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewCSIuReader(bytes.NewReader([]byte(tt.input)))
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("ReadAll(%q): %v", tt.input, err)
			}
			if string(got) != tt.want {
				t.Fatalf("translated %q to %q, want %q", tt.input, string(got), tt.want)
			}
		})
	}
}

func TestEnhancedKeyboardInputPreservesFunctionalPressesAndDropsReleases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "Up press", input: "\x1b[1;1:1A", want: "\x1b[A"},
		{name: "Down repeat", input: "\x1b[1;1:2B", want: "\x1b[B"},
		{name: "Ctrl Up press", input: "\x1b[1;5:1A", want: "\x1b[1;5A"},
		{name: "Page Up press", input: "\x1b[5;1:1~", want: "\x1b[5~"},
		{name: "Up release", input: "\x1b[1;1:3A", want: ""},
		{name: "unrelated colon CSI passes through", input: "\x1b[38;5:2m", want: "\x1b[38;5:2m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewCSIuReader(bytes.NewReader([]byte(tt.input)))
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("ReadAll(%q): %v", tt.input, err)
			}
			if string(got) != tt.want {
				t.Fatalf("translated %q to %q, want %q", tt.input, string(got), tt.want)
			}
		})
	}
}

func TestEnhancedKeyboardInputKeepsPressAndFinalCtrlReleaseAcrossChunks(t *testing.T) {
	r := NewCSIuReader(&chunkedReader{chunks: [][]byte{
		[]byte("\x1b[9;5:1u\x1b[9;5:"),
		[]byte("3u\x1b[57442;1:"),
		[]byte("3u"),
	}})
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := string([]rune{testCtrlTabMarker, testCtrlReleaseMarker})
	if string(got) != want {
		t.Fatalf("translated chunked press/release stream to %q, want %q", string(got), want)
	}
}

func TestEnhancedCtrlAltTabIsNotClaimedByQuickSwitcher(t *testing.T) {
	for _, input := range []string{"\x1b[9;7u", "\x1b[27;7;9~"} {
		r := NewCSIuReader(bytes.NewReader([]byte(input)))
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("ReadAll(%q): %v", input, err)
		}
		if string(got) == string(testCtrlTabMarker) || string(got) == string(testCtrlShiftTabMarker) {
			t.Fatalf("Ctrl+Alt+Tab %q was claimed as a quick-switch key", input)
		}
	}
}

func TestNormalizeOverviewKeyTokenEnhancedCtrlTab(t *testing.T) {
	tests := []struct {
		pressed string
		want    string
	}{
		{pressed: string(testCtrlTabMarker), want: "ctrl+tab"},
		{pressed: string(testCtrlShiftTabMarker), want: "ctrl+shift+tab"},
		{pressed: string(testCtrlTabFallback), want: "ctrl+tab-fallback"},
		{pressed: string(testCtrlShiftFallback), want: "ctrl+shift+tab-fallback"},
		{pressed: "tab", want: "tab"},
	}
	for _, tt := range tests {
		if got := normalizeOverviewKeyToken(tt.pressed); got != tt.want {
			t.Errorf("normalizeOverviewKeyToken(%q) = %q, want %q", tt.pressed, got, tt.want)
		}
	}
}

func quickSwitchHome() *Home {
	now := time.Unix(1000, 0)
	a := &session.Instance{ID: "a", Title: "alpha", Status: session.StatusRunning, LastAccessedAt: now}
	b := &session.Instance{ID: "b", Title: "bravo", Status: session.StatusWaiting, LastAccessedAt: now.Add(-time.Minute)}
	c := &session.Instance{ID: "c", Title: "charlie", Status: session.StatusIdle, LastAccessedAt: now.Add(-2 * time.Minute)}
	h := NewHome()
	h.width = 100
	h.height = 30
	h.shortcutMode = shortcutModeMenu
	h.setHotkeys(resolveHotkeysForMode(nil, shortcutModeMenu))
	h.instances = []*session.Instance{a, b, c}
	h.instanceByID = map[string]*session.Instance{"a": a, "b": b, "c": c}
	h.flatItems = []session.Item{{Type: session.ItemTypeSession, Session: a}}
	h.cursor = 0
	return h
}

func TestCtrlTabFromOverviewSelectsMostRecentOtherSession(t *testing.T) {
	h := quickSwitchHome()
	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlTabMarker}})
	if !h.sessionSwitcher.IsVisible() {
		t.Fatal("Ctrl+Tab should open the session switcher")
	}
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "b" {
		t.Fatalf("Ctrl+Tab selected %v, want the most recent other session b", selected)
	}
	if cmd != nil {
		t.Fatal("Ctrl+Tab must wait for Ctrl release instead of arming an idle attach timer")
	}
}

func TestCtrlShiftTabFromOverviewCyclesBackward(t *testing.T) {
	h := quickSwitchHome()
	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlShiftTabMarker}})
	if !h.sessionSwitcher.IsVisible() {
		t.Fatal("Ctrl+Shift+Tab should open the session switcher")
	}
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "c" {
		t.Fatalf("Ctrl+Shift+Tab selected %v, want the previous MRU entry c", selected)
	}
	if cmd != nil {
		t.Fatal("Ctrl+Shift+Tab must wait for Ctrl release instead of arming an idle attach timer")
	}
}

func TestCtrlTabChoosesMostRecentOtherWhenOriginIsNotNewest(t *testing.T) {
	h := quickSwitchHome()
	h.flatItems[0].Session = h.instanceByID["b"]

	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlTabMarker}})
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "a" {
		t.Fatalf("Ctrl+Tab from b selected %v, want newest other session a", selected)
	}
}

func TestCtrlTabFromNonSwitchableOverviewRowSelectsNewestSession(t *testing.T) {
	for _, tt := range []struct {
		name string
		item session.Item
	}{
		{name: "group", item: session.Item{Type: session.ItemTypeGroup, Group: &session.Group{Name: "group"}}},
		{name: "stopped session", item: session.Item{Type: session.ItemTypeSession, Session: &session.Instance{ID: "dead", Status: session.StatusStopped}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := quickSwitchHome()
			h.flatItems = []session.Item{tt.item}

			_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlTabMarker}})
			if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "a" {
				t.Fatalf("Ctrl+Tab from a non-switchable row selected %v, want newest session a", selected)
			}
		})
	}
}

func TestCtrlTabCyclesVisibleSwitcherAndPlainTabDoesNot(t *testing.T) {
	h := quickSwitchHome()
	h.sessionSwitcher.Show("a", h.instances, nil)
	h.sessionSwitcher.lastCycleAt = time.Time{}

	_, cmd := h.handleSessionSwitcherKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlTabMarker}})
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "b" {
		t.Fatalf("Ctrl+Tab selected %v, want b", selected)
	}
	if cmd != nil {
		t.Fatal("Ctrl+Tab must not arm an idle attach timer while Ctrl remains held")
	}

	_, _ = h.handleSessionSwitcherKey(tea.KeyMsg{Type: tea.KeyTab})
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "b" {
		t.Fatalf("plain Tab changed selection to %v", selected)
	}
}

func TestCtrlTabSwitcherCommitsOnlyOnFinalCtrlRelease(t *testing.T) {
	h := quickSwitchHome()
	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlTabMarker}})

	if !h.sessionSwitcher.IsVisible() {
		t.Fatal("Ctrl+Tab should leave the switcher visible while Ctrl is held")
	}
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "b" {
		t.Fatalf("Ctrl+Tab selected %v, want b", selected)
	}

	_, _ = h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlReleaseMarker}})
	if h.sessionSwitcher.IsVisible() {
		t.Fatal("final Ctrl release should close the switcher")
	}
	h.lastNotifSwitchMu.Lock()
	got := h.lastNotifSwitchID
	h.lastNotifSwitchMu.Unlock()
	if got != "b" {
		t.Fatalf("Ctrl release prepared attach to %q, want b", got)
	}
}

func TestCtrlReleaseDoesNotCommitNonNativeSwitcher(t *testing.T) {
	h := quickSwitchHome()
	h.sessionSwitcher.Show("a", h.instances, nil)

	_, _ = h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlReleaseMarker}})
	if !h.sessionSwitcher.IsVisible() {
		t.Fatal("Ctrl release must not commit a switcher that was not opened by native Ctrl+Tab")
	}
}

func TestArrowBrowsingCancelsNativeCtrlReleaseCommit(t *testing.T) {
	h := quickSwitchHome()
	_, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlTabMarker}})
	_, _ = h.handleSessionSwitcherKey(tea.KeyMsg{Type: tea.KeyDown})

	_, _ = h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlReleaseMarker}})
	if !h.sessionSwitcher.IsVisible() {
		t.Fatal("Ctrl release must not commit after arrow navigation switches to deliberate browsing")
	}
}

func TestCtrlLetterSwitcherFallbackStillUsesIdleCommit(t *testing.T) {
	h := quickSwitchHome()
	h.sessionSwitcher.Show("a", h.instances, nil)
	h.sessionSwitcher.lastCycleAt = time.Time{}

	_, cmd := h.handleSessionSwitcherKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("Ctrl+S fallback should retain idle commit because terminals cannot report its modifier release portably")
	}
}

func TestModifyOtherKeysCtrlTabFallbackStillUsesIdleCommit(t *testing.T) {
	h := quickSwitchHome()
	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlTabFallback}})
	if !h.sessionSwitcher.IsVisible() {
		t.Fatal("modifyOtherKeys Ctrl+Tab should open the session switcher")
	}
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "b" {
		t.Fatalf("modifyOtherKeys Ctrl+Tab selected %v, want b", selected)
	}
	if cmd == nil {
		t.Fatal("modifyOtherKeys Ctrl+Tab should retain idle commit because xterm does not report Ctrl release")
	}
}

func TestAttachedCtrlTabMessageImmediatelyAdvances(t *testing.T) {
	for _, tt := range []struct {
		name      string
		direction switcherDirection
		want      string
	}{
		{name: "next", direction: switcherNext, want: "b"},
		{name: "previous", direction: switcherPrevious, want: "c"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := quickSwitchHome()
			_, cmd := h.updateInner(openSwitcherMsg{fromSessionID: "a", quickDirection: tt.direction, waitForRelease: true})
			if !h.sessionSwitcher.IsVisible() {
				t.Fatal("attached Ctrl+Tab return should open the switcher")
			}
			if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != tt.want {
				t.Fatalf("attached Ctrl+Tab selected %v, want %s", selected, tt.want)
			}
			if cmd == nil {
				t.Fatal("attached Ctrl+Tab should schedule attach-return reconciliation")
			}
		})
	}
}
