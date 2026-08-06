package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPreviewSources_ExpandTabsAndConstrainEveryLine(t *testing.T) {
	forceTrueColorProfile()

	const paneWidth = 40
	const maxLineWidth = paneWidth - 2
	pane := "\x1b[48;2;33;58;43m    \x1b[2m588 \x1b[0m\x1b[32m+\x1b[39m\t\t\x1b[35mreturn\x1b[0m err" + strings.Repeat("x", 80)

	assertSafe := func(t *testing.T, got string) {
		t.Helper()
		if strings.ContainsRune(got, '\t') {
			t.Fatalf("preview leaked a tab: %q", got)
		}
		if !strings.Contains(got, "\x1b[") {
			t.Fatalf("preview lost ANSI styling: %q", got)
		}
		for _, line := range strings.Split(got, "\n") {
			if width := cellWidth(line); width > maxLineWidth {
				t.Fatalf("preview row width = %d, want <= %d: %q", width, maxLineWidth, line)
			}
		}
	}

	t.Run("ssh remote", func(t *testing.T) {
		home := NewHome()
		remote := session.RemoteSessionInfo{
			ID:         "remote-tab-preview",
			Title:      "remote",
			Status:     "running",
			Tool:       "codex",
			RemoteName: "workstation",
		}
		item := session.Item{
			Type:          session.ItemTypeRemoteSession,
			RemoteName:    "workstation",
			RemoteSession: &remote,
		}
		key := remotePreviewCacheKey(item.RemoteName, remote.ID)
		home.previewCache[key] = pane

		assertSafe(t, home.renderRemotePreview(item, paneWidth, 24))
		if got := home.previewCache[key]; got != pane {
			t.Fatalf("remote preview cache was mutated: got %q, want raw pane %q", got, pane)
		}
	})

	t.Run("hub", func(t *testing.T) {
		home := NewHome()
		hubSession := session.HubSessionInfo{
			ID:     "hub-tab-preview",
			Title:  "hub",
			Status: "running",
			Tool:   "codex",
		}
		item := session.Item{
			Type:        session.ItemTypeHubSession,
			HubNodeID:   "node-workstation",
			HubNodeName: "workstation",
			HubSession:  &hubSession,
		}
		key := hubPreviewCacheKey(item.HubNodeID, hubSession.ID)
		home.previewCache[key] = pane

		assertSafe(t, home.renderHubPreview(item, paneWidth, 24))
		if got := home.previewCache[key]; got != pane {
			t.Fatalf("hub preview cache was mutated: got %q, want raw pane %q", got, pane)
		}
	})
}
