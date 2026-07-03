package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type HandoverDialog struct {
	visible       bool
	sourceID      string
	sourceTitle   string
	sourceTool    string
	sourceToolID  string
	width         int
	height        int
	targetOptions []string
	targetCursor  int
	titleInput    textinput.Model
	pathInput     textinput.Model
	groupInput    textinput.Model
	messageInput  textinput.Model
	startNow      bool
	focusIndex    int
	defaultTitle  string
	titleEdited   bool
	validationErr string
}

type HandoverDialogValues struct {
	SourceID    string
	Target      string
	Title       string
	ProjectPath string
	GroupPath   string
	Message     string
	StartNow    bool
}

func NewHandoverDialog() *HandoverDialog {
	return &HandoverDialog{}
}

func (d *HandoverDialog) Show(source *session.Instance) {
	d.visible = true
	d.sourceID = source.ID
	d.sourceTitle = source.Title
	d.sourceTool = canonicalHandoverDialogTool(source)
	d.sourceToolID = handoverDialogSourceToolID(source)
	d.targetOptions = handoverDialogTargets(d.sourceTool)
	d.targetCursor = 0
	d.focusIndex = 0
	d.startNow = false
	d.titleEdited = false
	d.validationErr = ""
	target := d.currentTarget()
	d.defaultTitle = handoverDialogDefaultTitle(source.Title, target)
	d.titleInput = mkInput("Title", MaxNameLength, d.defaultTitle)
	d.pathInput = mkInput("Project path", 512, source.ProjectPath)
	d.groupInput = mkInput("Group path", 256, source.GroupPath)
	d.messageInput = mkInput("Optional instruction", 512, "")
	d.updateFocus()
}

func (d *HandoverDialog) Hide() {
	d.visible = false
	d.sourceID = ""
	d.sourceTitle = ""
	d.sourceTool = ""
	d.sourceToolID = ""
	d.targetOptions = nil
	d.validationErr = ""
	d.blurInputs()
}

func (d *HandoverDialog) IsVisible() bool {
	return d != nil && d.visible
}

func (d *HandoverDialog) SetSize(width, height int) {
	d.width = width
	d.height = height
}

func (d *HandoverDialog) Values() HandoverDialogValues {
	return HandoverDialogValues{
		SourceID:    d.sourceID,
		Target:      d.currentTarget(),
		Title:       strings.TrimSpace(d.titleInput.Value()),
		ProjectPath: strings.TrimSpace(d.pathInput.Value()),
		GroupPath:   strings.TrimSpace(d.groupInput.Value()),
		Message:     strings.TrimSpace(d.messageInput.Value()),
		StartNow:    d.startNow,
	}
}

func (d *HandoverDialog) Validate() string {
	if strings.TrimSpace(d.titleInput.Value()) == "" {
		return "Title cannot be empty"
	}
	if strings.TrimSpace(d.pathInput.Value()) == "" {
		return "Project path cannot be empty"
	}
	if d.currentTarget() == "" {
		return "Target tool is required"
	}
	return ""
}

func (d *HandoverDialog) SetError(msg string) {
	d.validationErr = msg
}

func (d *HandoverDialog) Update(msg tea.Msg) (*HandoverDialog, tea.Cmd) {
	if !d.visible {
		return d, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}

	switch key.String() {
	case "tab", "down", "j":
		d.focusIndex = (d.focusIndex + 1) % 6
		d.updateFocus()
		return d, nil
	case "shift+tab", "up", "k":
		d.focusIndex = (d.focusIndex + 5) % 6
		d.updateFocus()
		return d, nil
	case "left", "h":
		if d.focusIndex == 0 {
			d.moveTarget(-1)
			return d, nil
		}
	case "right", "l":
		if d.focusIndex == 0 {
			d.moveTarget(1)
			return d, nil
		}
	case " ":
		if d.focusIndex == 5 {
			d.startNow = !d.startNow
			return d, nil
		}
	}

	oldTitle := d.titleInput.Value()
	var cmd tea.Cmd
	switch d.focusIndex {
	case 1:
		d.titleInput, cmd = d.titleInput.Update(msg)
		if d.titleInput.Value() != oldTitle {
			d.titleEdited = true
		}
	case 2:
		d.pathInput, cmd = d.pathInput.Update(msg)
	case 3:
		d.groupInput, cmd = d.groupInput.Update(msg)
	case 4:
		d.messageInput, cmd = d.messageInput.Update(msg)
	}
	return d, cmd
}

func (d *HandoverDialog) View() string {
	if !d.visible {
		return ""
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	labelStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	valueStyle := lipgloss.NewStyle().Foreground(ColorText)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	errorStyle := lipgloss.NewStyle().Foreground(ColorRed)
	footerStyle := lipgloss.NewStyle().Foreground(ColorComment).Italic(true)

	dialogWidth := fitDialogWidth(78, 44, d.width)
	innerWidth := importDialogInnerWidth(dialogWidth)
	fit := func(s string) string { return cellTruncate(s, innerWidth, "…") }

	var lines []string
	lines = append(lines, fit(titleStyle.Render("Hand Over Session")))
	lines = append(lines, "")
	lines = append(lines, fit(labelStyle.Render("Source: ")+valueStyle.Render(d.sourceTitle+" ("+d.sourceTool+")")))
	if id := shortHandoverDialogID(d.sourceToolID); id != "" {
		lines = append(lines, fit(labelStyle.Render("Source id: ")+valueStyle.Render(id)))
	}
	lines = append(lines, "")
	lines = append(lines, fit(labelStyle.Render("Target: ")+d.renderTargetPills(selectedStyle, valueStyle)))
	lines = append(lines, fit(labelStyle.Render("Title:  ")+d.titleInput.View()))
	lines = append(lines, fit(labelStyle.Render("Path:   ")+d.pathInput.View()))
	lines = append(lines, fit(labelStyle.Render("Group:  ")+d.groupInput.View()))
	lines = append(lines, fit(labelStyle.Render("Note:   ")+d.messageInput.View()))
	check := "[ ]"
	if d.startNow {
		check = "[x]"
	}
	startLine := check + " start now"
	if d.focusIndex == 5 {
		startLine = selectedStyle.Render(startLine)
	}
	lines = append(lines, fit(labelStyle.Render("Start:  ")+startLine))
	if d.validationErr != "" {
		lines = append(lines, "")
		lines = append(lines, fit(errorStyle.Render(d.validationErr)))
	}
	lines = append(lines, "")
	lines = append(lines, fit(footerStyle.Render("Enter create | Esc cancel | Tab next | <-/-> target | Space toggle")))

	box := DialogBoxStyle.Width(dialogWidth).Render(strings.Join(lines, "\n"))
	return centerInScreen(box, d.width, d.height)
}

func (d *HandoverDialog) renderTargetPills(selectedStyle, valueStyle lipgloss.Style) string {
	var pills []string
	for i, opt := range d.targetOptions {
		label := opt
		if i == d.targetCursor && d.focusIndex == 0 {
			label = selectedStyle.Render(" " + label + " ")
		} else {
			label = valueStyle.Render(" " + label + " ")
		}
		pills = append(pills, label)
	}
	return strings.Join(pills, " ")
}

func (d *HandoverDialog) currentTarget() string {
	if d.targetCursor < 0 || d.targetCursor >= len(d.targetOptions) {
		return ""
	}
	return d.targetOptions[d.targetCursor]
}

func (d *HandoverDialog) moveTarget(delta int) {
	if len(d.targetOptions) == 0 {
		return
	}
	d.targetCursor = (d.targetCursor + delta + len(d.targetOptions)) % len(d.targetOptions)
	nextDefault := handoverDialogDefaultTitle(d.sourceTitle, d.currentTarget())
	if !d.titleEdited || strings.TrimSpace(d.titleInput.Value()) == d.defaultTitle {
		d.titleInput.SetValue(nextDefault)
	}
	d.defaultTitle = nextDefault
}

func (d *HandoverDialog) updateFocus() {
	d.blurInputs()
	switch d.focusIndex {
	case 1:
		d.titleInput.Focus()
	case 2:
		d.pathInput.Focus()
	case 3:
		d.groupInput.Focus()
	case 4:
		d.messageInput.Focus()
	}
}

func (d *HandoverDialog) blurInputs() {
	d.titleInput.Blur()
	d.pathInput.Blur()
	d.groupInput.Blur()
	d.messageInput.Blur()
}

func canonicalHandoverDialogTool(source *session.Instance) string {
	switch {
	case source == nil:
		return ""
	case session.IsClaudeCompatible(source.Tool):
		return "claude"
	case session.IsCodexCompatible(source.Tool):
		return "codex"
	default:
		return source.Tool
	}
}

func handoverDialogTargets(sourceTool string) []string {
	all := []string{"claude", "codex", "opencode", "kiro"}
	targets := make([]string, 0, len(all)-1)
	for _, target := range all {
		if target != sourceTool {
			targets = append(targets, target)
		}
	}
	return targets
}

func handoverDialogDefaultTitle(sourceTitle, target string) string {
	sourceTitle = strings.TrimSpace(sourceTitle)
	if sourceTitle == "" {
		sourceTitle = "session"
	}
	return fmt.Sprintf("%s (%s)", sourceTitle, target)
}

func handoverDialogSourceToolID(source *session.Instance) string {
	if source == nil {
		return ""
	}
	switch canonicalHandoverDialogTool(source) {
	case "claude":
		return source.ClaudeSessionID
	case "codex":
		return source.CodexSessionID
	case "opencode":
		return source.OpenCodeSessionID
	case "kiro":
		return source.KiroSessionID
	default:
		return ""
	}
}

func shortHandoverDialogID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
