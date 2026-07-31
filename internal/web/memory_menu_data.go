package web

import (
	"fmt"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// MenuSessionState is a lightweight status/tool update for one session.
type MenuSessionState struct {
	Status session.Status
	Tool   string
}

// MemoryMenuData is an in-memory menu snapshot store used by web mode.
// It can optionally fall back to a loader (e.g. storage-backed) until the
// first in-memory snapshot is published.
type MemoryMenuData struct {
	mu               sync.RWMutex
	snapshot         *MenuSnapshot
	archivedSnapshot *MenuSnapshot
	hubSnapshot      *MenuSnapshot
	archivedHub      *MenuSnapshot
	fallback         MenuDataLoader
	onChange         func()
}

// NewMemoryMenuData creates an in-memory menu data store.
func NewMemoryMenuData(fallback MenuDataLoader) *MemoryMenuData {
	return &MemoryMenuData{
		fallback: fallback,
	}
}

// SetOnChange registers a callback fired after in-memory snapshots change.
// The callback runs after the store lock is released.
func (m *MemoryMenuData) SetOnChange(onChange func()) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.onChange = onChange
	m.mu.Unlock()
}

// LoadMenuSnapshot returns the latest in-memory snapshot.
// If no snapshot exists yet, it falls back once to the configured loader.
func (m *MemoryMenuData) LoadMenuSnapshot() (*MenuSnapshot, error) {
	m.mu.RLock()
	current := cloneMenuSnapshot(m.snapshot)
	hubSnapshot := cloneMenuSnapshot(m.hubSnapshot)
	m.mu.RUnlock()
	if current != nil {
		return mergeHubMenuSnapshot(current, hubSnapshot), nil
	}
	if m.fallback == nil {
		return nil, fmt.Errorf("menu snapshot is unavailable")
	}

	snapshot, err := m.fallback.LoadMenuSnapshot()
	if err != nil {
		return nil, err
	}
	m.SetSnapshot(snapshot)
	m.mu.RLock()
	hubSnapshot = cloneMenuSnapshot(m.hubSnapshot)
	m.mu.RUnlock()
	return mergeHubMenuSnapshot(snapshot, hubSnapshot), nil
}

// LoadArchivedMenuSnapshot returns archived sessions from the storage fallback.
func (m *MemoryMenuData) LoadArchivedMenuSnapshot() (*MenuSnapshot, error) {
	if m == nil {
		return nil, fmt.Errorf("menu snapshot is unavailable")
	}
	m.mu.RLock()
	current := cloneMenuSnapshot(m.archivedSnapshot)
	hubSnapshot := cloneMenuSnapshot(m.archivedHub)
	m.mu.RUnlock()
	if current != nil {
		return mergeHubMenuSnapshot(current, hubSnapshot), nil
	}
	if m.fallback == nil {
		return nil, fmt.Errorf("menu snapshot is unavailable")
	}
	if loader, ok := m.fallback.(interface {
		LoadArchivedMenuSnapshot() (*MenuSnapshot, error)
	}); ok {
		snapshot, err := loader.LoadArchivedMenuSnapshot()
		if err != nil {
			return nil, err
		}
		m.mu.RLock()
		hubSnapshot = cloneMenuSnapshot(m.archivedHub)
		m.mu.RUnlock()
		return mergeHubMenuSnapshot(snapshot, hubSnapshot), nil
	}
	return nil, fmt.Errorf("archived session list is not available")
}

// InvalidateCache clears the cached snapshot so the next call to
// LoadMenuSnapshot reloads from the fallback storage-backed loader.
// Used after mutations in headless (--no-tui) mode to ensure the menu
// reflects the current persisted state on the next fetch.
func (m *MemoryMenuData) InvalidateCache() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.snapshot = nil
	m.archivedSnapshot = nil
	m.mu.Unlock()
}

// SetSnapshot replaces the stored menu snapshot.
func (m *MemoryMenuData) SetSnapshot(snapshot *MenuSnapshot) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.snapshot = cloneMenuSnapshot(snapshot)
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

// SetArchivedSnapshot replaces the stored archived menu snapshot.
func (m *MemoryMenuData) SetArchivedSnapshot(snapshot *MenuSnapshot) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.archivedSnapshot = cloneMenuSnapshot(snapshot)
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

// SetHubSnapshots replaces the active and archived hub-only menu projections.
// They are merged into the local base snapshots when readers load menu data,
// allowing hub callbacks to update web state without touching Bubble Tea state.
func (m *MemoryMenuData) SetHubSnapshots(active, archived *MenuSnapshot) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.hubSnapshot = cloneMenuSnapshot(active)
	m.archivedHub = cloneMenuSnapshot(archived)
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

// UpdateSessionStates updates status/tool fields in-place for existing sessions.
func (m *MemoryMenuData) UpdateSessionStates(states map[string]MenuSessionState, generatedAt time.Time) {
	if m == nil || len(states) == 0 {
		return
	}

	m.mu.Lock()
	if m.snapshot == nil {
		m.mu.Unlock()
		return
	}

	changed := false
	for i := range m.snapshot.Items {
		item := &m.snapshot.Items[i]
		if item.Type != MenuItemTypeSession || item.Session == nil {
			continue
		}
		state, ok := states[item.Session.ID]
		if !ok {
			continue
		}

		item.Session.Status = state.Status
		if state.Tool != "" {
			item.Session.Tool = state.Tool
		}
		changed = true
	}
	if !changed {
		m.mu.Unlock()
		return
	}

	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	m.snapshot.GeneratedAt = generatedAt.UTC()
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

// UpdateHubNodeName updates an already-published hub node label in-place and
// notifies subscribers. It lets admin node renames reflect immediately in the
// web UI instead of waiting for the next remote snapshot.
func (m *MemoryMenuData) UpdateHubNodeName(nodeID, name string) {
	if m == nil || nodeID == "" || name == "" {
		return
	}

	m.mu.Lock()
	changed := false
	for _, snapshot := range []*MenuSnapshot{
		m.snapshot,
		m.archivedSnapshot,
		m.hubSnapshot,
		m.archivedHub,
	} {
		if updateHubNodeName(snapshot, nodeID, name) {
			changed = true
		}
	}
	if !changed {
		m.mu.Unlock()
		return
	}

	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

// RemoveHubNode removes a hub node and all projected groups/sessions owned by
// that node from the in-memory menu snapshot after an admin revoke.
func (m *MemoryMenuData) RemoveHubNode(nodeID string) {
	if m == nil || nodeID == "" {
		return
	}

	m.mu.Lock()
	changed := false
	for _, snapshot := range []*MenuSnapshot{
		m.snapshot,
		m.archivedSnapshot,
		m.hubSnapshot,
		m.archivedHub,
	} {
		if removeHubNode(snapshot, nodeID) {
			changed = true
		}
	}
	if !changed {
		m.mu.Unlock()
		return
	}

	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

func updateHubNodeName(snapshot *MenuSnapshot, nodeID, name string) bool {
	if snapshot == nil {
		return false
	}
	changed := false
	for i := range snapshot.HubNodes {
		if snapshot.HubNodes[i].ID == nodeID && snapshot.HubNodes[i].Name != name {
			snapshot.HubNodes[i].Name = name
			changed = true
		}
	}
	for i := range snapshot.Items {
		item := &snapshot.Items[i]
		if item.Group != nil && item.Group.HubNodeID == nodeID && item.Group.HubNodeName != name {
			item.Group.HubNodeName = name
			changed = true
		}
		if item.Session != nil && item.Session.HubNodeID == nodeID && item.Session.HubNodeName != name {
			item.Session.HubNodeName = name
			changed = true
		}
	}
	if changed {
		snapshot.GeneratedAt = time.Now().UTC()
	}
	return changed
}

func removeHubNode(snapshot *MenuSnapshot, nodeID string) bool {
	if snapshot == nil {
		return false
	}
	changed := false
	hubNodes := snapshot.HubNodes[:0]
	for _, node := range snapshot.HubNodes {
		if node.ID == nodeID {
			changed = true
			continue
		}
		hubNodes = append(hubNodes, node)
	}
	snapshot.HubNodes = hubNodes

	items := snapshot.Items[:0]
	for _, item := range snapshot.Items {
		if item.Group != nil && item.Group.HubNodeID == nodeID {
			changed = true
			continue
		}
		if item.Session != nil && item.Session.HubNodeID == nodeID {
			changed = true
			continue
		}
		item.Index = len(items)
		items = append(items, item)
	}
	snapshot.Items = items
	if !changed {
		return false
	}

	snapshot.TotalGroups = 0
	snapshot.TotalSessions = 0
	for _, item := range snapshot.Items {
		switch item.Type {
		case MenuItemTypeGroup:
			snapshot.TotalGroups++
		case MenuItemTypeSession:
			snapshot.TotalSessions++
		}
	}
	snapshot.GeneratedAt = time.Now().UTC()
	return true
}

func cloneMenuSnapshot(snapshot *MenuSnapshot) *MenuSnapshot {
	if snapshot == nil {
		return nil
	}

	cloned := *snapshot
	cloned.HubNodes = append([]HubNode(nil), snapshot.HubNodes...)
	cloned.Items = make([]MenuItem, len(snapshot.Items))

	for i, item := range snapshot.Items {
		cloned.Items[i] = item
		if item.Group != nil {
			groupCopy := *item.Group
			cloned.Items[i].Group = &groupCopy
		}
		if item.Session != nil {
			sessionCopy := *item.Session
			cloned.Items[i].Session = &sessionCopy
		}
	}

	return &cloned
}

func mergeHubMenuSnapshot(base, overlay *MenuSnapshot) *MenuSnapshot {
	if base == nil {
		base = &MenuSnapshot{}
	}
	merged := cloneMenuSnapshot(base)
	if overlay == nil {
		return merged
	}

	kept := merged.Items[:0]
	for _, item := range merged.Items {
		if item.Group != nil && item.Group.HubNodeID != "" {
			continue
		}
		if item.Session != nil && item.Session.HubNodeID != "" {
			continue
		}
		kept = append(kept, item)
	}
	merged.Items = kept
	overlay = cloneMenuSnapshot(overlay)
	merged.HubNodes = overlay.HubNodes
	merged.Items = append(merged.Items, overlay.Items...)
	if overlay.Profile != "" && merged.Profile == "" {
		merged.Profile = overlay.Profile
	}
	if overlay.GeneratedAt.After(merged.GeneratedAt) {
		merged.GeneratedAt = overlay.GeneratedAt
	}

	merged.TotalGroups = 0
	merged.TotalSessions = 0
	for i := range merged.Items {
		merged.Items[i].Index = i
		switch merged.Items[i].Type {
		case MenuItemTypeGroup:
			merged.TotalGroups++
		case MenuItemTypeSession:
			merged.TotalSessions++
		}
	}
	return merged
}
