package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type claudeImportSubmitMsg struct{}

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

	dialogWidth := fitDialogWidth(96, 44, d.width)
	innerWidth := importDialogInnerWidth(dialogWidth)
	fit := func(s string) string { return cellTruncate(s, innerWidth, "…") }

	var lines []string
	lines = append(lines, fit(titleStyle.Render("Import Saved Claude Session")))
	lines = append(lines, "")
	if len(d.entries) == 0 {
		lines = append(lines, fit(dimStyle.Render("No saved Claude sessions found.")))
	} else {
		visibleRows := savedSessionImportVisibleRows(d.height)
		start, end := windowBounds(d.cursor, len(d.entries), visibleRows)
		if start > 0 {
			lines = append(lines, fit(dimStyle.Render(fmt.Sprintf("  ↑ %d more", start))))
		}
		for i := start; i < end; i++ {
			entry := d.entries[i]
			title := strings.TrimSpace(entry.Name)
			if title == "" {
				title = shortClaudeImportID(entry.SessionID)
			}
			row := fmt.Sprintf("%s  %s  %s",
				title,
				dimStyle.Render(shortClaudeImportID(entry.SessionID)),
				dimStyle.Render(entry.UpdatedAt.Local().Format("2006-01-02 15:04")),
			)
			if path := importDialogPath(entry.CWD, entry.Path); path != "" {
				row += "  " + dimStyle.Render(path)
			}
			if i == d.cursor {
				lines = append(lines, fit("> "+selectedStyle.Render(row)))
			} else {
				lines = append(lines, fit("  "+normalStyle.Render(row)))
			}
		}
		if end < len(d.entries) {
			lines = append(lines, fit(dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(d.entries)-end))))
		}
	}
	lines = append(lines, "")
	lines = append(lines, fit(footerStyle.Render("Enter import | Esc cancel | j/k navigate")))

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
