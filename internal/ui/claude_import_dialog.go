package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type claudeImportSubmitMsg struct{}
type importSourceSubmitMsg struct{}

// ClaudeImportDialog lets the user choose a saved Claude session from Claude metadata.
type ClaudeImportDialog struct {
	visible  bool
	entries  []session.ClaudeImportCandidate
	cursor   int
	selected *session.ClaudeImportCandidate
	width    int
	height   int
}

func NewClaudeImportDialog() *ClaudeImportDialog {
	return &ClaudeImportDialog{}
}

func (d *ClaudeImportDialog) Show(entries []session.ClaudeImportCandidate) {
	d.visible = true
	d.entries = append(d.entries[:0], entries...)
	d.cursor = 0
	d.selected = nil
}

func (d *ClaudeImportDialog) Hide() {
	d.visible = false
	d.entries = nil
	d.cursor = 0
	d.selected = nil
}

func (d *ClaudeImportDialog) Visible() bool {
	return d.visible
}

func (d *ClaudeImportDialog) IsVisible() bool {
	return d.visible
}

func (d *ClaudeImportDialog) SetSize(width, height int) {
	d.width = width
	d.height = height
}

func (d *ClaudeImportDialog) Selected() (session.ClaudeImportCandidate, bool) {
	if d.selected == nil {
		return session.ClaudeImportCandidate{}, false
	}
	return *d.selected, true
}

func (d *ClaudeImportDialog) Update(msg tea.Msg) (*ClaudeImportDialog, tea.Cmd) {
	if !d.visible {
		return d, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}

	switch key.String() {
	case "j", "down":
		if len(d.entries) > 0 {
			d.cursor = (d.cursor + 1) % len(d.entries)
		}
	case "k", "up":
		if len(d.entries) > 0 {
			d.cursor = (d.cursor - 1 + len(d.entries)) % len(d.entries)
		}
	case "esc":
		d.Hide()
	case "enter":
		if len(d.entries) == 0 {
			return d, nil
		}
		selected := d.entries[d.cursor]
		d.selected = &selected
		return d, func() tea.Msg { return claudeImportSubmitMsg{} }
	}
	return d, nil
}

func (d *ClaudeImportDialog) View() string {
	if !d.visible {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(ColorText)
	dimStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	footerStyle := lipgloss.NewStyle().Foreground(ColorComment).Italic(true)

	var lines []string
	lines = append(lines, titleStyle.Render("Import Saved Claude Session"))
	lines = append(lines, "")
	if len(d.entries) == 0 {
		lines = append(lines, dimStyle.Render("No saved Claude sessions found."))
	} else {
		for i, entry := range d.entries {
			title := strings.TrimSpace(entry.Name)
			if title == "" {
				title = shortClaudeImportID(entry.SessionID)
			}
			row := fmt.Sprintf("%s  %s  %s",
				title,
				dimStyle.Render(shortClaudeImportID(entry.SessionID)),
				dimStyle.Render(entry.UpdatedAt.Local().Format("2006-01-02 15:04")),
			)
			if i == d.cursor {
				lines = append(lines, "> "+selectedStyle.Render(row))
			} else {
				lines = append(lines, "  "+normalStyle.Render(row))
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, footerStyle.Render("Enter import | Esc cancel | j/k navigate"))

	dialogWidth := fitDialogWidth(76, 44, d.width)
	box := DialogBoxStyle.Width(dialogWidth).Render(strings.Join(lines, "\n"))
	return centerInScreen(box, d.width, d.height)
}

type importSource int

const (
	importSourceTmux importSource = iota
	importSourceClaude
)

type ImportSourceDialog struct {
	visible     bool
	cursor      int
	selected    *importSource
	claudeCount int
	width       int
	height      int
}

func NewImportSourceDialog() *ImportSourceDialog {
	return &ImportSourceDialog{}
}

func (d *ImportSourceDialog) Show(claudeCount int) {
	d.visible = true
	d.cursor = 0
	d.selected = nil
	d.claudeCount = claudeCount
}

func (d *ImportSourceDialog) Hide() {
	d.visible = false
	d.cursor = 0
	d.selected = nil
}

func (d *ImportSourceDialog) IsVisible() bool {
	return d != nil && d.visible
}

func (d *ImportSourceDialog) ClaudeCount() int {
	if d == nil {
		return 0
	}
	return d.claudeCount
}

func (d *ImportSourceDialog) SetSize(width, height int) {
	d.width = width
	d.height = height
}

func (d *ImportSourceDialog) Selected() (importSource, bool) {
	if d.selected == nil {
		return 0, false
	}
	return *d.selected, true
}

func (d *ImportSourceDialog) Update(msg tea.Msg) (*ImportSourceDialog, tea.Cmd) {
	if !d.visible {
		return d, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}

	switch key.String() {
	case "j", "down":
		d.cursor = (d.cursor + 1) % 2
	case "k", "up":
		d.cursor = (d.cursor - 1 + 2) % 2
	case "esc":
		d.Hide()
	case "enter":
		selected := importSource(d.cursor)
		d.selected = &selected
		return d, func() tea.Msg { return importSourceSubmitMsg{} }
	}
	return d, nil
}

func (d *ImportSourceDialog) View() string {
	if d == nil || !d.visible {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(ColorText)
	dimStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	footerStyle := lipgloss.NewStyle().Foreground(ColorComment).Italic(true)

	rows := []string{
		"Existing tmux sessions",
		fmt.Sprintf("Saved Claude sessions  %s", dimStyle.Render(fmt.Sprintf("%d", d.claudeCount))),
	}

	var lines []string
	lines = append(lines, titleStyle.Render("Import Sessions"))
	lines = append(lines, "")
	for i, row := range rows {
		if i == d.cursor {
			lines = append(lines, "> "+selectedStyle.Render(row))
		} else {
			lines = append(lines, "  "+normalStyle.Render(row))
		}
	}
	lines = append(lines, "")
	lines = append(lines, footerStyle.Render("Enter choose | Esc cancel | j/k navigate"))

	dialogWidth := fitDialogWidth(54, 34, d.width)
	box := DialogBoxStyle.Width(dialogWidth).Render(strings.Join(lines, "\n"))
	return centerInScreen(box, d.width, d.height)
}

func shortClaudeImportID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
