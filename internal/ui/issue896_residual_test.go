package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Issue #896 sub-bug 3 (paskal's enumeration): the path-suggestions popup is
// visible after typing a prefix, but pressing Enter does NOT select the
// highlighted suggestion — it submits whatever value is in the input. The
// picker selection looks interactive but Enter ignores it.
//
// Root cause: home.go's Enter handler only intercepts when
// IsSuggestionsActive() is true, which today requires the user to first press
// Space or Ctrl+N to enter "arrow-key mode". Plain arrow keys on a freshly
// shown popup do not activate it — they move dialog focus instead.
//
// Current contract verified here: plain Up/Down are reserved for form
// navigation; Ctrl+N/Ctrl+P explicitly enter suggestion navigation and the
// highlighted real suggestion is the one that gets applied.
func TestNewDialog_PopupEnter_SelectsHighlightedSuggestion_RegressionFor896(t *testing.T) {
	d := NewNewDialog()
	d.Show()

	suggestions := []string{"/p/alpha", "/p/beta", "/p/gamma"}
	d.SetPathSuggestions(suggestions)

	// Focus the path field — focusIndex 2 in the default layout
	// (focusName, focusMultiRepo, focusPath, ...).
	d.focusIndex = 2
	d.updateFocus()

	// User has typed a prefix; the popup is visible (path focused, not hidden).
	// In real use, the first keystroke on a soft-selected path clears the
	// pre-fill, focuses pathInput, and unsets pathSoftSelected (see soft-select
	// handler in newdialog.go). Mirror that post-typing state so the test
	// reflects an actively-editing user — which is the only state where
	// arrows should auto-activate the popup after the #1020 fix. See
	// [[issue1020_path_selector_ux_test]].
	d.pathInput.SetValue("/p/")
	d.pathInput.Focus()
	d.pathSoftSelected = false

	// User presses Ctrl+N twice — cursor should advance through:
	//   0 ("Type custom") -> 1 (/p/alpha) -> 2 (/p/beta)
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyCtrlN})

	// Precondition for the Enter handler in home.go: popup must be active so
	// IsSuggestionsActive() is true and apply-highlighted runs.
	if !d.IsSuggestionsActive() {
		t.Fatalf("after Ctrl+N on path suggestions, IsSuggestionsActive()=false; home.go's Enter handler will be skipped and form will submit with typed value")
	}
	if d.IsTypeCustomHighlighted() {
		t.Fatalf("after 2 Ctrl+N presses, Type-custom is highlighted; cursor=%d, expected to be on a real suggestion", d.pathSuggestionCursor)
	}
	if got := d.pathSuggestionCursor; got != 2 {
		t.Fatalf("after 2 Ctrl+N presses, pathSuggestionCursor=%d, want 2 (second real suggestion)", got)
	}

	// Now simulate exactly what home.handleNewDialogKey does on Enter when the
	// popup is active and a real suggestion is highlighted:
	//   d.ApplyHighlightedSuggestion(); d.DismissSuggestions();
	// then falls through to the form-submit Enter handler.
	d.ApplyHighlightedSuggestion()
	d.DismissSuggestions()

	got := d.pathInput.Value()
	want := "/p/beta"
	if got != want {
		t.Errorf("after popup-Enter on highlighted suggestion 2, pathInput=%q, want %q\n"+
			"this is issue #896 sub-bug 3: Enter selected the wrong target", got, want)
	}
	if got == "/p/" {
		t.Errorf("popup-Enter kept the typed prefix instead of applying the highlighted suggestion (#896 sub-bug 3)")
	}
	if got == "/p/alpha" {
		t.Errorf("popup-Enter applied suggestion 0 (off-by-one) instead of the highlighted suggestion 2 (#896 sub-bug 3)")
	}
}

// Issue #896 sub-bug 4 (paskal's enumeration): "I am not sure but I think
// sometimes arrows work in pop up, but most of the time and you need to
// operate using ctrl+n and space".
//
// Root cause: when the popup is visible on the path field, Up/Down arrow
// keys are routed to the popup only after suggestionsActive=true. That keeps
// normal field navigation predictable while preserving reliable arrows once
// the picker has been explicitly opened.
//
// Contract: arrows on an explicitly opened path picker must navigate the
// popup cursor reliably from press 1, no off-by-one, no skipped frames,
// wrap-around at the ends.
func TestNewDialog_PopupArrows_NavigateReliably_RegressionFor896(t *testing.T) {
	d := NewNewDialog()
	d.Show()

	suggestions := []string{"/p/a", "/p/b", "/p/c"}
	d.SetPathSuggestions(suggestions)

	d.focusIndex = 2 // focusPath
	d.updateFocus()

	// Simulate the user having typed in the path field (post soft-select).
	// In real flow, the first keystroke flips pathSoftSelected=false and
	// focuses pathInput; only then do arrows auto-activate the popup, per
	// the #1020 fix in newdialog.go. See [[issue1020_path_selector_ux_test]].
	d.pathInput.Focus()
	d.pathSoftSelected = false
	d.pathInput.SetValue(t.TempDir() + "/missing")

	// Explicitly open the picker before using arrow-key navigation.
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !d.IsSuggestionsActive() {
		t.Fatalf("test precondition: Enter should activate path suggestions")
	}

	// Press Down — cursor space is: 0 ("Type custom"), 1..3 (real suggestions).
	// On an active picker, Down must navigate the popup instead of moving form focus.
	steps := []struct {
		key      tea.KeyType
		wantCur  int
		wantName string
	}{
		{tea.KeyDown, 1, "down 1 (a)"},
		{tea.KeyDown, 2, "down 2 (b)"},
		{tea.KeyDown, 3, "down 3 (c)"},
		{tea.KeyDown, 0, "down 4 (wrap to Type-custom)"},
		{tea.KeyUp, 3, "up 1 (wrap back to c)"},
		{tea.KeyUp, 2, "up 2 (b)"},
		{tea.KeyUp, 1, "up 3 (a)"},
		{tea.KeyUp, 0, "up 4 (Type-custom)"},
	}

	for i, s := range steps {
		d, _ = d.Update(tea.KeyMsg{Type: s.key})

		if !d.IsSuggestionsActive() {
			t.Fatalf("step %d (%s): popup arrow deactivated suggestions", i, s.wantName)
		}
		if d.pathSuggestionCursor != s.wantCur {
			t.Fatalf("step %d (%s): pathSuggestionCursor=%d, want %d (issue #896 sub-bug 4: arrow navigation flaky)",
				i, s.wantName, d.pathSuggestionCursor, s.wantCur)
		}
		// Path field must remain focused — arrows must navigate the popup,
		// not jump to other dialog fields.
		if d.currentTarget() != focusPath {
			t.Fatalf("step %d (%s): focus left the path field (target=%v); popup arrows must stay scoped to the popup", i, s.wantName, d.currentTarget())
		}
	}
}
