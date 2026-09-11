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
)

func TestEnhancedCtrlTabInputKeepsDirection(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantMarker rune
	}{
		{name: "CSI u forward", input: "\x1b[9;5u", wantMarker: testCtrlTabMarker},
		{name: "CSI u backward", input: "\x1b[9;6u", wantMarker: testCtrlShiftTabMarker},
		{name: "modifyOtherKeys forward", input: "\x1b[27;5;9~", wantMarker: testCtrlTabMarker},
		{name: "modifyOtherKeys backward", input: "\x1b[27;6;9~", wantMarker: testCtrlShiftTabMarker},
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
	if cmd == nil {
		t.Fatal("Ctrl+Tab should arm the idle attach timer")
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
	if cmd == nil {
		t.Fatal("Ctrl+Shift+Tab should arm the idle attach timer")
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

func TestCtrlTabCyclesVisibleSwitcherAndPlainTabDoesNot(t *testing.T) {
	h := quickSwitchHome()
	h.sessionSwitcher.Show("a", h.instances, nil)
	h.sessionSwitcher.lastCycleAt = time.Time{}

	_, cmd := h.handleSessionSwitcherKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCtrlTabMarker}})
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "b" {
		t.Fatalf("Ctrl+Tab selected %v, want b", selected)
	}
	if cmd == nil {
		t.Fatal("Ctrl+Tab should re-arm the idle attach timer")
	}

	_, _ = h.handleSessionSwitcherKey(tea.KeyMsg{Type: tea.KeyTab})
	if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != "b" {
		t.Fatalf("plain Tab changed selection to %v", selected)
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
			_, cmd := h.updateInner(openSwitcherMsg{fromSessionID: "a", quickDirection: tt.direction})
			if !h.sessionSwitcher.IsVisible() {
				t.Fatal("attached Ctrl+Tab return should open the switcher")
			}
			if selected := h.sessionSwitcher.GetSelected(); selected == nil || selected.ID != tt.want {
				t.Fatalf("attached Ctrl+Tab selected %v, want %s", selected, tt.want)
			}
			if cmd == nil {
				t.Fatal("attached Ctrl+Tab should schedule reconciliation and idle attach")
			}
		})
	}
}
