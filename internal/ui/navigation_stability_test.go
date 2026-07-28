package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPreviewDebounceCoalescesBurstIntoLatestRequest(t *testing.T) {
	home := &Home{}

	first := home.fetchPreviewDebounced("local-session", -1)
	if first == nil {
		t.Fatal("first preview request must start the debounce command")
	}

	result := make(chan tea.Msg, 1)
	go func() {
		result <- first()
	}()

	time.Sleep(50 * time.Millisecond)
	lastRequestAt := time.Now()
	second := home.fetchRemotePreviewDebounced("dev", "remote-session")
	if second != nil {
		t.Fatal("a preview request inside the debounce window must reuse the in-flight command")
	}

	select {
	case raw := <-result:
		if elapsed := time.Since(lastRequestAt); elapsed < 120*time.Millisecond {
			t.Fatalf("debounce completed %v after the latest request; want at least 120ms", elapsed)
		}
		msg, ok := raw.(previewDebounceMsg)
		if !ok {
			t.Fatalf("debounce returned %T, want previewDebounceMsg", raw)
		}
		if msg.remoteName != "dev" || msg.sessionID != "remote-session" {
			t.Fatalf("debounce returned stale target: %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("preview debounce did not complete")
	}
}

func TestPreviewDebounceDropsTargetWhenSelectionHasNoPreview(t *testing.T) {
	home := &Home{
		width:  200,
		height: 40,
		flatItems: []session.Item{{
			Type:  session.ItemTypeGroup,
			Path:  "group",
			Group: &session.Group{Name: "group", Path: "group"},
		}},
	}

	cmd := home.fetchPreviewDebounced("old-session", -1)
	if cmd == nil {
		t.Fatal("initial preview request did not start a debounce command")
	}
	if next := home.fetchSelectedPreview(); next != nil {
		t.Fatal("a group row must not schedule a preview command")
	}

	raw := cmd()
	msg, ok := raw.(previewDebounceMsg)
	if !ok {
		t.Fatalf("debounce returned %T, want previewDebounceMsg", raw)
	}
	if msg.previewKey != "" {
		t.Fatalf("group selection left stale preview target %q", msg.previewKey)
	}
}

func TestPreviewDebounceRejectsTargetThatIsNoLongerSelected(t *testing.T) {
	first := session.NewInstance("first", "/tmp/first")
	second := session.NewInstance("second", "/tmp/second")
	home := &Home{
		width:        200,
		height:       40,
		instanceByID: map[string]*session.Instance{first.ID: first, second.ID: second},
		flatItems: []session.Item{
			{Type: session.ItemTypeSession, Session: first},
			{Type: session.ItemTypeSession, Session: second},
		},
	}

	cmd := home.fetchPreviewDebounced(first.ID, -1)
	if cmd == nil {
		t.Fatal("initial preview request did not start a debounce command")
	}
	home.cursor = 1 // Simulate a later row change that did not schedule another fetch.

	home.Update(cmd())

	if home.previewFetchingID != "" {
		t.Fatalf("stale debounce started a preview capture for %q", home.previewFetchingID)
	}
}

func TestRemoteRefreshDefersActiveTopRepartitionDuringNavigation(t *testing.T) {
	home, _ := buildTwoGroupHome(t)
	setOnlySessionRunning(t, home, "a1")
	home.groupViewMode = session.GroupViewActiveTop
	home.rebuildFlatItems()

	a2Before := sessionIndexByTitle(home, "a2")
	divBefore := dividerIndex(home)
	if a2Before <= divBefore {
		t.Fatalf("precondition: a2=%d must start below divider=%d", a2Before, divBefore)
	}
	home.cursor = a2Before

	home.instancesMu.Lock()
	for _, inst := range home.instances {
		if inst.Title == "a2" {
			inst.Status = session.StatusRunning
			break
		}
	}
	home.instancesMu.Unlock()

	home.markNavigationActivity()
	home.Update(remoteSessionsFetchedMsg{
		sessions: map[string][]session.RemoteSessionInfo{},
	})

	if got := sessionIndexByTitle(home, "a2"); got != a2Before {
		t.Fatalf("async refresh repartitioned during navigation: a2 moved from %d to %d", a2Before, got)
	}
	if selected := home.selectedLocalSessionID(); selected != sessionIDByTitle(t, home, "a2") {
		t.Fatalf("async refresh moved selection from a2 to %q", selected)
	}

	home.lastNavigationTime = time.Now().Add(-time.Second)
	home.Update(tickMsg{})

	a2After := sessionIndexByTitle(home, "a2")
	divAfter := dividerIndex(home)
	if a2After < 0 || a2After >= divAfter {
		t.Fatalf("settled refresh did not repartition a2 above divider: a2=%d divider=%d", a2After, divAfter)
	}
	if home.cursor != a2After || home.selectedLocalSessionID() != sessionIDByTitle(t, home, "a2") {
		t.Fatalf("settled repartition did not follow a2: cursor=%d a2=%d selected=%q", home.cursor, a2After, home.selectedLocalSessionID())
	}
}

func TestStorageReloadDefersActiveTopRepartitionDuringNavigation(t *testing.T) {
	home, _ := buildTwoGroupHome(t)
	setOnlySessionRunning(t, home, "a1")
	home.groupViewMode = session.GroupViewActiveTop
	home.rebuildFlatItems()

	a2Before := sessionIndexByTitle(home, "a2")
	divBefore := dividerIndex(home)
	if a2Before <= divBefore {
		t.Fatalf("precondition: a2=%d must start below divider=%d", a2Before, divBefore)
	}
	home.cursor = a2Before
	state := home.preserveState()

	home.instancesMu.Lock()
	for _, inst := range home.instances {
		if inst.Title == "a2" {
			inst.Status = session.StatusRunning
			break
		}
	}
	instances := append([]*session.Instance(nil), home.instances...)
	home.instancesMu.Unlock()

	home.markNavigationActivity()
	home.Update(loadSessionsMsg{
		instances:    instances,
		restoreState: &state,
	})

	if home.pendingSessionReload == nil {
		t.Fatal("storage reload was not deferred during navigation")
	}
	if got := sessionIndexByTitle(home, "a2"); got != a2Before {
		t.Fatalf("storage reload repartitioned during navigation: a2 moved from %d to %d", a2Before, got)
	}

	home.lastNavigationTime = time.Now().Add(-time.Second)
	home.Update(tickMsg{})

	a2After := sessionIndexByTitle(home, "a2")
	divAfter := dividerIndex(home)
	if a2After < 0 || a2After >= divAfter {
		t.Fatalf("settled reload did not repartition a2 above divider: a2=%d divider=%d", a2After, divAfter)
	}
	if home.pendingSessionReload != nil {
		t.Fatal("settled tick did not consume the deferred storage reload")
	}
	if home.cursor != a2After || home.selectedLocalSessionID() != sessionIDByTitle(t, home, "a2") {
		t.Fatalf("settled reload did not follow a2: cursor=%d a2=%d selected=%q", home.cursor, a2After, home.selectedLocalSessionID())
	}
}

func TestMouseSelectionMarksNavigationHotForWorkers(t *testing.T) {
	home, _ := buildTwoGroupHome(t)
	home.cursor = 0
	before := time.Now()

	home.Update(tea.MouseMsg{
		X:      5,
		Y:      5,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})

	if !home.isNavigating {
		t.Fatal("mouse selection did not mark navigation active")
	}
	hotUntil := time.Unix(0, home.navigationHotUntil.Load())
	if !hotUntil.After(before) {
		t.Fatalf("mouse selection did not establish navigation hot window: %v", hotUntil)
	}
}

func sessionIDByTitle(t *testing.T, home *Home, title string) string {
	t.Helper()
	for _, inst := range home.instances {
		if inst.Title == title {
			return inst.ID
		}
	}
	t.Fatalf("session %q not found", title)
	return ""
}
