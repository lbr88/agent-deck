package web

import (
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type staticMenuLoader struct {
	calls    int
	snapshot *MenuSnapshot
}

func (s *staticMenuLoader) LoadMenuSnapshot() (*MenuSnapshot, error) {
	s.calls++
	return s.snapshot, nil
}

func TestMemoryMenuData_LoadMenuSnapshotFallbackAndCache(t *testing.T) {
	loader := &staticMenuLoader{
		snapshot: &MenuSnapshot{
			Profile:       "default",
			GeneratedAt:   time.Now().UTC(),
			TotalGroups:   1,
			TotalSessions: 1,
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID:     "sess-1",
						Title:  "Session 1",
						Status: session.StatusIdle,
					},
				},
			},
		},
	}
	store := NewMemoryMenuData(loader)

	first, err := store.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("first LoadMenuSnapshot() error = %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("fallback loader calls = %d, want 1", loader.calls)
	}

	// Mutating the returned snapshot must not mutate internal store state.
	first.Items[0].Session.Title = "mutated"

	second, err := store.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("second LoadMenuSnapshot() error = %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("fallback loader calls after cache = %d, want 1", loader.calls)
	}
	if got := second.Items[0].Session.Title; got != "Session 1" {
		t.Fatalf("cached snapshot title = %q, want %q", got, "Session 1")
	}
}

func TestMemoryMenuData_InvalidateCacheForcesReload(t *testing.T) {
	loader := &staticMenuLoader{
		snapshot: &MenuSnapshot{
			Profile:       "default",
			GeneratedAt:   time.Now().UTC(),
			TotalGroups:   1,
			TotalSessions: 1,
			Items: []MenuItem{
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID: "sess-1", Title: "Original",
					},
				},
			},
		},
	}
	store := NewMemoryMenuData(loader)

	// First load populates the cache from the fallback loader.
	first, err := store.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("first LoadMenuSnapshot() error = %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("fallback calls after first load = %d, want 1", loader.calls)
	}

	// Second load returns the cached snapshot without calling the fallback.
	_, err = store.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("second LoadMenuSnapshot() error = %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("cached load triggered fallback: calls = %d, want 1", loader.calls)
	}

	// Verify the first snapshot content is correct
	if got := first.Items[0].Session.Title; got != "Original" {
		t.Fatalf("first load title = %q, want %q", got, "Original")
	}

	// Mutate the fallback data to simulate a storage-side change.
	loader.snapshot.Items[0].Session.Title = "Updated"

	// Invalidate the cache — next LoadMenuSnapshot must go back to fallback.
	store.InvalidateCache()

	// Third load must call the fallback and get the updated title.
	third, err := store.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("third LoadMenuSnapshot() error = %v", err)
	}
	if loader.calls != 2 {
		t.Fatalf("fallback calls after invalidate = %d, want 2", loader.calls)
	}
	if got := third.Items[0].Session.Title; got != "Updated" {
		t.Fatalf("after invalidation title = %q, want %q", got, "Updated")
	}
}

func TestMemoryMenuData_UpdateSessionStates(t *testing.T) {
	store := NewMemoryMenuData(nil)
	store.SetSnapshot(&MenuSnapshot{
		Profile:       "default",
		GeneratedAt:   time.Now().UTC(),
		TotalGroups:   1,
		TotalSessions: 1,
		Items: []MenuItem{
			{
				Type: MenuItemTypeSession,
				Session: &MenuSession{
					ID:     "sess-2",
					Tool:   "claude",
					Status: session.StatusIdle,
				},
			},
		},
	})

	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	store.UpdateSessionStates(map[string]MenuSessionState{
		"sess-2": {
			Status: session.StatusWaiting,
			Tool:   "codex",
		},
	}, ts)

	snapshot, err := store.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot() error = %v", err)
	}
	if got := snapshot.Items[0].Session.Status; got != session.StatusWaiting {
		t.Fatalf("session status = %q, want %q", got, session.StatusWaiting)
	}
	if got := snapshot.Items[0].Session.Tool; got != "codex" {
		t.Fatalf("session tool = %q, want %q", got, "codex")
	}
	if !snapshot.GeneratedAt.Equal(ts) {
		t.Fatalf("generatedAt = %s, want %s", snapshot.GeneratedAt, ts)
	}
}

func TestMemoryMenuDataReplacesHubSnapshots(t *testing.T) {
	store := NewMemoryMenuData(nil)
	store.SetSnapshot(&MenuSnapshot{
		Profile:       "default",
		TotalGroups:   2,
		TotalSessions: 2,
		HubNodes:      []HubNode{{ID: "node_1", Name: "old-laptop"}},
		Items: []MenuItem{
			{
				Index: 0,
				Type:  MenuItemTypeGroup,
				Group: &MenuGroup{Name: "local", Path: "local"},
			},
			{
				Index:   1,
				Type:    MenuItemTypeSession,
				Session: &MenuSession{ID: "local-1", Title: "local"},
			},
			{
				Index: 2,
				Type:  MenuItemTypeGroup,
				Group: &MenuGroup{
					Name:      "old-laptop / default",
					Path:      "hub/node_1/default",
					HubNodeID: "node_1",
				},
			},
			{
				Index: 3,
				Type:  MenuItemTypeSession,
				Session: &MenuSession{
					ID:        "hub/node_1/old",
					Title:     "old",
					HubNodeID: "node_1",
				},
			},
		},
	})

	store.SetHubSnapshots(&MenuSnapshot{
		HubNodes: []HubNode{{ID: "node_1", Name: "laptop"}},
		Items: []MenuItem{
			{
				Type: MenuItemTypeGroup,
				Group: &MenuGroup{
					Name:      "laptop / default",
					Path:      "hub/node_1/default",
					HubNodeID: "node_1",
				},
			},
			{
				Type: MenuItemTypeSession,
				Session: &MenuSession{
					ID:        "hub/node_1/new",
					Title:     "new",
					HubNodeID: "node_1",
				},
			},
		},
	}, &MenuSnapshot{})

	got, err := store.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot: %v", err)
	}
	if got.TotalGroups != 2 || got.TotalSessions != 2 {
		t.Fatalf("totals = groups:%d sessions:%d, want 2/2", got.TotalGroups, got.TotalSessions)
	}
	if len(got.HubNodes) != 1 || got.HubNodes[0].Name != "laptop" {
		t.Fatalf("hub nodes = %+v, want replacement laptop node", got.HubNodes)
	}
	if len(got.Items) != 4 {
		t.Fatalf("items = %d, want 4: %+v", len(got.Items), got.Items)
	}
	for i, item := range got.Items {
		if item.Index != i {
			t.Fatalf("item %d index = %d, want %d", i, item.Index, i)
		}
		if item.Session != nil && item.Session.ID == "hub/node_1/old" {
			t.Fatal("old hub session survived replacement")
		}
	}
	if got.Items[1].Session == nil || got.Items[1].Session.ID != "local-1" {
		t.Fatalf("local session was not preserved: %+v", got.Items)
	}
	if got.Items[3].Session == nil || got.Items[3].Session.ID != "hub/node_1/new" {
		t.Fatalf("new hub session missing: %+v", got.Items)
	}
}

func TestMemoryMenuDataUpdatesHubOverlayNodeName(t *testing.T) {
	store := NewMemoryMenuData(nil)
	store.SetSnapshot(&MenuSnapshot{})
	store.SetArchivedSnapshot(&MenuSnapshot{})
	store.SetHubSnapshots(
		hubNodeMenuSnapshot("node_1", "old-laptop", "active"),
		hubNodeMenuSnapshot("node_1", "old-laptop", "archived"),
	)

	store.UpdateHubNodeName("node_1", "desktop")

	for _, load := range []struct {
		name string
		fn   func() (*MenuSnapshot, error)
	}{
		{name: "active", fn: store.LoadMenuSnapshot},
		{name: "archived", fn: store.LoadArchivedMenuSnapshot},
	} {
		t.Run(load.name, func(t *testing.T) {
			got, err := load.fn()
			if err != nil {
				t.Fatalf("load snapshot: %v", err)
			}
			if len(got.HubNodes) != 1 || got.HubNodes[0].Name != "desktop" {
				t.Fatalf("hub nodes = %+v, want desktop", got.HubNodes)
			}
			for _, item := range got.Items {
				if item.Session != nil && item.Session.HubNodeName != "desktop" {
					t.Fatalf("session hub node name = %q, want desktop", item.Session.HubNodeName)
				}
			}
		})
	}
}

func TestMemoryMenuDataRemovesNodeFromHubOverlay(t *testing.T) {
	store := NewMemoryMenuData(nil)
	store.SetSnapshot(&MenuSnapshot{})
	store.SetArchivedSnapshot(&MenuSnapshot{})
	store.SetHubSnapshots(
		hubNodeMenuSnapshot("node_1", "laptop", "active"),
		hubNodeMenuSnapshot("node_1", "laptop", "archived"),
	)

	store.RemoveHubNode("node_1")

	for _, load := range []struct {
		name string
		fn   func() (*MenuSnapshot, error)
	}{
		{name: "active", fn: store.LoadMenuSnapshot},
		{name: "archived", fn: store.LoadArchivedMenuSnapshot},
	} {
		t.Run(load.name, func(t *testing.T) {
			got, err := load.fn()
			if err != nil {
				t.Fatalf("load snapshot: %v", err)
			}
			if len(got.HubNodes) != 0 || len(got.Items) != 0 {
				t.Fatalf("removed hub node survived: nodes=%+v items=%+v", got.HubNodes, got.Items)
			}
		})
	}
}

func hubNodeMenuSnapshot(nodeID, nodeName, sessionID string) *MenuSnapshot {
	return &MenuSnapshot{
		HubNodes: []HubNode{{ID: nodeID, Name: nodeName}},
		Items: []MenuItem{
			{
				Type: MenuItemTypeGroup,
				Group: &MenuGroup{
					HubNodeID:   nodeID,
					HubNodeName: nodeName,
				},
			},
			{
				Type: MenuItemTypeSession,
				Session: &MenuSession{
					ID:          "hub/" + nodeID + "/" + sessionID,
					HubNodeID:   nodeID,
					HubNodeName: nodeName,
				},
			},
		},
	}
}

func TestMemoryMenuData_OnChangeFiresForSnapshotAndStateUpdates(t *testing.T) {
	store := NewMemoryMenuData(nil)
	changed := make(chan struct{}, 4)
	store.SetOnChange(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})

	store.SetSnapshot(&MenuSnapshot{
		Items: []MenuItem{{
			Type:    MenuItemTypeSession,
			Session: &MenuSession{ID: "sess-1", Status: session.StatusIdle},
		}},
	})
	waitMemoryMenuChange(t, changed, "set snapshot")

	store.UpdateSessionStates(map[string]MenuSessionState{
		"sess-1": {Status: session.StatusWaiting},
	}, time.Now())
	waitMemoryMenuChange(t, changed, "update session state")

	store.SetArchivedSnapshot(&MenuSnapshot{})
	waitMemoryMenuChange(t, changed, "set archived snapshot")

	store.UpdateSessionStates(map[string]MenuSessionState{
		"missing": {Status: session.StatusRunning},
	}, time.Now())
	select {
	case <-changed:
		t.Fatal("unexpected change notification for missing session state")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestNewServerWiresMemoryMenuDataChangesToSubscribers(t *testing.T) {
	store := NewMemoryMenuData(nil)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: store})
	ch := srv.subscribeMenuChanges()
	defer srv.unsubscribeMenuChanges(ch)

	store.SetSnapshot(&MenuSnapshot{Profile: "default"})
	select {
	case <-ch:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected server subscriber notification after memory menu change")
	}
}

func TestServerInvalidatesMemoryMenuDataBeforeNotifyingSubscribers(t *testing.T) {
	loader := &staticMenuLoader{
		snapshot: &MenuSnapshot{
			Items: []MenuItem{{
				Type:    MenuItemTypeSession,
				Session: &MenuSession{ID: "old", Title: "old"},
			}},
		},
	}
	store := NewMemoryMenuData(loader)
	if _, err := store.LoadMenuSnapshot(); err != nil {
		t.Fatalf("initial LoadMenuSnapshot: %v", err)
	}
	loader.snapshot = &MenuSnapshot{
		Items: []MenuItem{{
			Type:    MenuItemTypeSession,
			Session: &MenuSession{ID: "new", Title: "new"},
		}},
	}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: store})
	ch := srv.subscribeMenuChanges()
	defer srv.unsubscribeMenuChanges(ch)

	srv.notifyMenuChanged()
	select {
	case <-ch:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected menu change notification")
	}
	got, err := store.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot after notify: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Session == nil || got.Items[0].Session.ID != "new" {
		t.Fatalf("snapshot after notification = %+v, want freshly reloaded new session", got.Items)
	}
}

func waitMemoryMenuChange(t *testing.T, ch <-chan struct{}, action string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("timed out waiting for onChange after %s", action)
	}
}
