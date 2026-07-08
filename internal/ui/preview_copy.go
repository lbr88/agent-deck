package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/clipboard"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// buildSessionInfoForCopy produces a plain-text payload of the right-pane
// preview values (#791): Repo / Path / Branch for worktree sessions, the
// full project-path list for multi-repo sessions, or just Path for plain
// sessions. The shape is deliberately label-prefixed and newline-separated
// so a paste lands cleanly in a shell prompt, an issue tracker, or a doc
// without further editing.
func buildSessionInfoForCopy(inst *session.Instance) string {
	if inst == nil {
		return ""
	}

	var b strings.Builder

	if inst.IsMultiRepo() {
		b.WriteString("Paths:\n")
		for i, p := range inst.AllProjectPaths() {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, p)
		}
	} else if inst.IsWorktree() {
		if inst.WorktreeRepoRoot != "" {
			fmt.Fprintf(&b, "Repo: %s\n", inst.WorktreeRepoRoot)
		}
		path := inst.WorktreePath
		if path == "" {
			path = inst.ProjectPath
		}
		if path != "" {
			fmt.Fprintf(&b, "Path: %s\n", path)
		}
		if inst.WorktreeBranch != "" {
			fmt.Fprintf(&b, "Branch: %s\n", inst.WorktreeBranch)
		}
	} else if inst.ProjectPath != "" {
		fmt.Fprintf(&b, "Path: %s\n", inst.ProjectPath)
	}

	// Session ID line (matches the "Session:" value shown in the preview pane),
	// emitted only when the tool has a detected session so empty sessions don't
	// produce a dangling label.
	if id := inst.DisplaySessionID(); id != "" {
		fmt.Fprintf(&b, "Session: %s\n", id)
	}

	return strings.TrimRight(b.String(), "\n")
}

// buildHubSessionInfoForCopy mirrors buildSessionInfoForCopy for projected hub
// sessions. It only uses metadata supplied by the owner node snapshot; it never
// reaches into local tmux/session state.
func buildHubSessionInfoForCopy(nodeName string, hs *session.HubSessionInfo) string {
	if hs == nil {
		return ""
	}

	var b strings.Builder
	if nodeName = strings.TrimSpace(nodeName); nodeName != "" {
		fmt.Fprintf(&b, "Node: %s\n", nodeName)
	}

	if hs.MultiRepoEnabled {
		b.WriteString("Paths:\n")
		seen := map[string]bool{}
		writePath := func(path string) {
			path = strings.TrimSpace(path)
			if path == "" || seen[path] {
				return
			}
			seen[path] = true
			fmt.Fprintf(&b, "  %d. %s\n", len(seen), path)
		}
		writePath(hs.ProjectPath)
		for _, p := range hs.AdditionalPaths {
			writePath(p)
		}
	} else if hs.WorktreePath != "" || hs.WorktreeRepoRoot != "" || hs.WorktreeBranch != "" {
		if hs.WorktreeRepoRoot != "" {
			fmt.Fprintf(&b, "Repo: %s\n", hs.WorktreeRepoRoot)
		}
		path := hs.WorktreePath
		if path == "" {
			path = hs.ProjectPath
		}
		if path != "" {
			fmt.Fprintf(&b, "Path: %s\n", path)
		}
		if hs.WorktreeBranch != "" {
			fmt.Fprintf(&b, "Branch: %s\n", hs.WorktreeBranch)
		}
	} else if hs.ProjectPath != "" {
		fmt.Fprintf(&b, "Path: %s\n", hs.ProjectPath)
	}

	if id := strings.TrimSpace(hs.DisplaySessionID); id != "" {
		fmt.Fprintf(&b, "Session: %s\n", id)
	}
	if id := strings.TrimSpace(hs.ID); id != "" {
		fmt.Fprintf(&b, "Hub session: %s\n", id)
	}

	return strings.TrimRight(b.String(), "\n")
}

// copySessionInfo returns a tea.Cmd that copies the preview pane's
// session-info payload (#791) to the system clipboard, mirroring the
// fallback chain used by copySessionOutput.
func (h *Home) copySessionInfo(inst *session.Instance) tea.Cmd {
	return func() tea.Msg {
		payload := buildSessionInfoForCopy(inst)
		if payload == "" {
			return copyResultMsg{err: fmt.Errorf("no session info to copy")}
		}

		termInfo := tmux.GetTerminalInfo()
		result, err := clipboard.Copy(payload, termInfo.SupportsOSC52)
		if err != nil {
			return copyResultMsg{err: fmt.Errorf("clipboard: %w", err)}
		}
		return copyResultMsg{
			sessionTitle: inst.Title,
			lineCount:    result.LineCount,
		}
	}
}

func (h *Home) copyHubSessionInfo(nodeName string, hs *session.HubSessionInfo) tea.Cmd {
	return func() tea.Msg {
		payload := buildHubSessionInfoForCopy(nodeName, hs)
		if payload == "" {
			return copyResultMsg{err: fmt.Errorf("no hub session info to copy")}
		}

		termInfo := tmux.GetTerminalInfo()
		result, err := clipboard.Copy(payload, termInfo.SupportsOSC52)
		if err != nil {
			return copyResultMsg{err: fmt.Errorf("clipboard: %w", err)}
		}
		return copyResultMsg{
			sessionTitle: hs.Title,
			lineCount:    result.LineCount,
		}
	}
}
