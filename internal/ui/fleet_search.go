package ui

import (
	"os"
	"sort"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func (h *Home) localSearchHost() string {
	if host := strings.TrimSpace(h.hubLocalNodeName); host != "" {
		return host
	}
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return "local"
}

// fleetSearchResults snapshots every session identity already known to the
// TUI. It deliberately performs no I/O: configured SSH remotes and hub nodes
// contribute their most recent in-memory snapshots.
func (h *Home) fleetSearchResults() []*SessionSearchResult {
	h.instancesMu.RLock()
	instances := append([]*session.Instance(nil), h.instances...)
	h.instancesMu.RUnlock()

	results := make([]*SessionSearchResult, 0, len(instances))
	localHost := h.localSearchHost()
	for _, inst := range instances {
		if result := localSessionSearchResult(inst, localHost); result != nil {
			results = append(results, result)
		}
	}

	h.remoteSessionsMu.RLock()
	remoteNames := make([]string, 0, len(h.remoteSessions))
	remotes := make(map[string][]session.RemoteSessionInfo, len(h.remoteSessions))
	for name, sessions := range h.remoteSessions {
		remoteNames = append(remoteNames, name)
		remotes[name] = append([]session.RemoteSessionInfo(nil), sessions...)
	}
	h.remoteSessionsMu.RUnlock()
	sort.Strings(remoteNames)
	for _, remoteName := range remoteNames {
		for _, info := range remotes[remoteName] {
			results = append(results, &SessionSearchResult{
				Source:      SearchSourceRemote,
				SessionID:   info.ID,
				Title:       info.Title,
				ProjectPath: info.Path,
				GroupPath:   info.Group,
				Tool:        info.Tool,
				Status:      session.Status(strings.TrimSpace(info.Status)),
				Host:        remoteName,
				RemoteName:  remoteName,
			})
		}
	}

	if h.hubConfigured {
		for _, node := range h.hubSessionSnapshots() {
			// The local node mirrors h.instances in a hub snapshot; indexing it
			// again would produce indistinguishable duplicate results.
			if h.isLocalHubNode(node) {
				continue
			}
			nodeID := strings.TrimSpace(node.Node.ID)
			if nodeID == "" {
				continue
			}
			host := hubNodeDisplayName(node)
			for _, info := range node.Sessions {
				results = append(results, &SessionSearchResult{
					Source:      SearchSourceHub,
					SessionID:   info.ID,
					Title:       info.Title,
					ProjectPath: info.ProjectPath,
					GroupPath:   info.GroupPath,
					Tool:        info.Tool,
					Status:      session.Status(strings.TrimSpace(info.Status)),
					Host:        host,
					HubNodeID:   nodeID,
				})
			}
		}
	}

	return results
}

func (h *Home) openFleetSearch() {
	h.search.SetFleetItems(h.fleetSearchResults())
	h.search.SetSize(h.width, h.height)
	h.search.ShowGlobal()
}

// focusSearchResult retains the useful old behavior of moving the main-list
// selection, but activation no longer stops there. If a status/group filter
// hides a remote or hub result, activation still proceeds from its stable ID.
func (h *Home) focusSearchResult(result *SessionSearchResult) {
	if result == nil {
		return
	}
	if result.Source == SearchSourceLocal {
		inst := h.getInstanceByID(result.SessionID)
		if inst == nil {
			return
		}
		h.pinSearchResultSession(result.SessionID)
		if inst.GroupPath != "" {
			h.groupTree.ExpandGroupWithParents(inst.GroupPath)
		}
		h.rebuildFlatItems()
	}

	for i, item := range h.flatItems {
		matches := false
		switch result.Source {
		case SearchSourceLocal:
			matches = item.Type == session.ItemTypeSession && item.Session != nil && item.Session.ID == result.SessionID
		case SearchSourceRemote:
			matches = item.Type == session.ItemTypeRemoteSession && item.RemoteSession != nil && item.RemoteName == result.RemoteName && item.RemoteSession.ID == result.SessionID
		case SearchSourceHub:
			matches = item.Type == session.ItemTypeHubSession && item.HubSession != nil && item.HubNodeID == result.HubNodeID && item.HubSession.ID == result.SessionID
		}
		if matches {
			h.cursor = i
			h.syncViewport()
			return
		}
	}
}

func (h *Home) activateSearchResult(result *SessionSearchResult) tea.Cmd {
	if result == nil {
		return nil
	}
	switch result.Source {
	case SearchSourceLocal:
		return h.activateLocalSession(h.getInstanceByID(result.SessionID))
	case SearchSourceRemote:
		return h.attachRemoteSession(result.RemoteName, result.SessionID)
	case SearchSourceHub:
		return h.attachHubSession(result.HubNodeID, result.SessionID, h.hubSearchResultNeedsRestart(result))
	default:
		return nil
	}
}

func (h *Home) hubSearchResultNeedsRestart(result *SessionSearchResult) bool {
	if result == nil {
		return false
	}
	status := result.Status
	h.hubSessionsMu.RLock()
	if node, ok := h.hubSessions[result.HubNodeID]; ok {
		for _, info := range node.Sessions {
			if info.ID == result.SessionID {
				status = session.Status(strings.TrimSpace(info.Status))
				break
			}
		}
	}
	h.hubSessionsMu.RUnlock()
	return status == session.StatusStopped || status == session.StatusError
}
