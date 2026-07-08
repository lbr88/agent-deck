package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// SessionPickerDialog presents a list of sessions for the user to select from.
// Used by the "x" (send output) feature to pick a target session.
type SessionPickerDialog struct {
	visible       bool
	width, height int
	sessions      []*session.Instance // Filtered target sessions (excludes source)
	targets       []sendOutputTarget
	cursor        int
	sourceSession *session.Instance
	sourceTarget  sendOutputTarget
}

type sendOutputTargetKind int

const (
	sendOutputTargetLocal sendOutputTargetKind = iota
	sendOutputTargetHub
)

type sendOutputTarget struct {
	kind         sendOutputTargetKind
	local        *session.Instance
	hubNodeID    string
	hubNodeName  string
	hubSessionID string
	title        string
	tool         string
	status       session.Status
}

func localSendOutputTarget(inst *session.Instance) sendOutputTarget {
	if inst == nil {
		return sendOutputTarget{}
	}
	return sendOutputTarget{
		kind:   sendOutputTargetLocal,
		local:  inst,
		title:  inst.Title,
		tool:   inst.Tool,
		status: inst.GetStatusThreadSafe(),
	}
}

func hubSendOutputTarget(nodeID, nodeName string, hs *session.HubSessionInfo) sendOutputTarget {
	if hs == nil {
		return sendOutputTarget{}
	}
	return sendOutputTarget{
		kind:         sendOutputTargetHub,
		hubNodeID:    nodeID,
		hubNodeName:  nodeName,
		hubSessionID: hs.ID,
		title:        hs.Title,
		tool:         hs.Tool,
		status:       session.Status(hs.Status),
	}
}

func (t sendOutputTarget) isZero() bool {
	return t.local == nil && t.hubSessionID == "" && t.title == ""
}

func (t sendOutputTarget) sameSession(other sendOutputTarget) bool {
	if t.kind != other.kind {
		return false
	}
	switch t.kind {
	case sendOutputTargetLocal:
		return t.local != nil && other.local != nil && t.local.ID == other.local.ID
	case sendOutputTargetHub:
		return t.hubNodeID == other.hubNodeID && t.hubSessionID == other.hubSessionID
	default:
		return false
	}
}

func (t sendOutputTarget) displayTitle() string {
	title := strings.TrimSpace(t.title)
	if title == "" {
		switch t.kind {
		case sendOutputTargetLocal:
			if t.local != nil {
				title = t.local.ID
			}
		case sendOutputTargetHub:
			title = t.hubSessionID
		}
	}
	if t.kind == sendOutputTargetHub {
		node := strings.TrimSpace(t.hubNodeName)
		if node == "" {
			node = strings.TrimSpace(t.hubNodeID)
		}
		if node != "" && title != "" {
			return fmt.Sprintf("%s/%s", node, title)
		}
		if node != "" {
			return node
		}
	}
	return title
}

// NewSessionPickerDialog creates a new session picker dialog.
func NewSessionPickerDialog() *SessionPickerDialog {
	return &SessionPickerDialog{}
}

// Show opens the picker with the source session and all available instances.
// Filters out the source session and sessions in error status.
func (d *SessionPickerDialog) Show(source *session.Instance, allInstances []*session.Instance) {
	d.visible = true
	d.sourceSession = source
	d.sourceTarget = localSendOutputTarget(source)
	d.cursor = 0

	// Filter: exclude source session and error-status sessions
	d.sessions = nil
	d.targets = nil
	for _, inst := range allInstances {
		if inst.ID == source.ID {
			continue
		}
		if inst.Status == session.StatusError || inst.Status == session.StatusStopped {
			continue
		}
		d.sessions = append(d.sessions, inst)
		d.targets = append(d.targets, localSendOutputTarget(inst))
	}
}

func (d *SessionPickerDialog) ShowTargets(source sendOutputTarget, targets []sendOutputTarget) {
	d.visible = true
	d.sourceSession = source.local
	d.sourceTarget = source
	d.cursor = 0
	d.sessions = nil
	d.targets = nil
	for _, target := range targets {
		if target.isZero() || target.sameSession(source) {
			continue
		}
		if target.status == session.StatusError || target.status == session.StatusStopped {
			continue
		}
		d.targets = append(d.targets, target)
		if target.local != nil {
			d.sessions = append(d.sessions, target.local)
		}
	}
}

// Hide closes the dialog and resets state.
func (d *SessionPickerDialog) Hide() {
	d.visible = false
	d.cursor = 0
	d.sourceSession = nil
	d.sourceTarget = sendOutputTarget{}
	d.sessions = nil
	d.targets = nil
}

// IsVisible returns whether the dialog is currently shown.
func (d *SessionPickerDialog) IsVisible() bool {
	return d.visible
}

// SetSize updates the dialog dimensions for centering.
func (d *SessionPickerDialog) SetSize(w, h int) {
	d.width = w
	d.height = h
}

// GetSelected returns the session at the current cursor position, or nil.
func (d *SessionPickerDialog) GetSelected() *session.Instance {
	target := d.GetSelectedTarget()
	if target.local == nil {
		return nil
	}
	return target.local
}

func (d *SessionPickerDialog) GetSelectedTarget() sendOutputTarget {
	if len(d.targets) == 0 || d.cursor >= len(d.targets) {
		return sendOutputTarget{}
	}
	return d.targets[d.cursor]
}

// GetSource returns the source session.
func (d *SessionPickerDialog) GetSource() *session.Instance {
	return d.sourceSession
}

func (d *SessionPickerDialog) GetSourceTarget() sendOutputTarget {
	return d.sourceTarget
}

// Update handles key events for the picker.
func (d *SessionPickerDialog) Update(msg tea.KeyMsg) (*SessionPickerDialog, tea.Cmd) {
	if !d.visible {
		return d, nil
	}

	switch msg.String() {
	case "j", "down":
		if len(d.sessions) > 0 {
			d.cursor = (d.cursor + 1) % len(d.sessions)
		}
	case "k", "up":
		if len(d.sessions) > 0 {
			d.cursor = (d.cursor - 1 + len(d.sessions)) % len(d.sessions)
		}
	case "esc":
		d.Hide()
	case "enter":
		// Selection confirmed: parent handles the action
	}

	return d, nil
}

// View renders the session picker dialog.
func (d *SessionPickerDialog) View() string {
	if !d.visible {
		return ""
	}

	// Styles
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorAccent)

	sourceStyle := lipgloss.NewStyle().
		Foreground(ColorTextDim).
		MarginBottom(1)

	selectedStyle := lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true)

	normalStyle := lipgloss.NewStyle().
		Foreground(ColorText)

	footerStyle := lipgloss.NewStyle().
		Foreground(ColorComment).
		Italic(true)

	// Build content
	var lines []string
	lines = append(lines, titleStyle.Render("Send Output To..."))

	sourceName := "unknown"
	if d.sourceSession != nil {
		sourceName = d.sourceSession.Title
	} else if !d.sourceTarget.isZero() {
		sourceName = d.sourceTarget.displayTitle()
	}
	lines = append(lines, sourceStyle.Render(fmt.Sprintf("Source: \"%s\"", sourceName)))
	lines = append(lines, "")

	if len(d.targets) == 0 {
		lines = append(lines, normalStyle.Render("No sessions available"))
	} else {
		for i, target := range d.targets {
			indicator := statusIndicator(target.status)
			tool := ""
			if target.tool != "" {
				tool = fmt.Sprintf(" (%s)", target.tool)
			}

			label := fmt.Sprintf("%s %s%s", indicator, target.displayTitle(), tool)
			if i == d.cursor {
				lines = append(lines, "> "+selectedStyle.Render(label))
			} else {
				lines = append(lines, "  "+normalStyle.Render(label))
			}
		}
	}

	lines = append(lines, "")
	lines = append(lines, footerStyle.Render("Enter send | Esc cancel | j/k navigate"))

	content := strings.Join(lines, "\n")

	// Dialog box
	dialogWidth := fitDialogWidth(44, 30, d.width)

	box := DialogBoxStyle.
		Width(dialogWidth).
		Render(content)

	return centerInScreen(box, d.width, d.height)
}

// statusIndicator returns the status symbol for a session.
func statusIndicator(status session.Status) string {
	switch status {
	case session.StatusRunning:
		return lipgloss.NewStyle().Foreground(ColorGreen).Render("●")
	case session.StatusWaiting:
		return lipgloss.NewStyle().Foreground(ColorYellow).Render("◐")
	case session.StatusIdle:
		return lipgloss.NewStyle().Foreground(ColorTextDim).Render("○")
	default:
		return lipgloss.NewStyle().Foreground(ColorRed).Render("✕")
	}
}
