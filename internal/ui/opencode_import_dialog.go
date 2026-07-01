package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type openCodeImportSubmitMsg struct{}

// OpenCodeImportDialog lets the user choose a saved OpenCode session from the
// OpenCode session list metadata.
type OpenCodeImportDialog struct {
	visible  bool
	entries  []session.OpenCodeImportEntry
	cursor   int
	selected *session.OpenCodeImportEntry
	width    int
	height   int
}

func NewOpenCodeImportDialog() *OpenCodeImportDialog {
	return &OpenCodeImportDialog{}
}

func (d *OpenCodeImportDialog) Show(entries []session.OpenCodeImportEntry) {
	d.visible = true
	d.entries = append(d.entries[:0], entries...)
	d.cursor = 0
	d.selected = nil
}

func (d *OpenCodeImportDialog) Hide() {
	d.visible = false
	d.entries = nil
	d.cursor = 0
	d.selected = nil
}

func (d *OpenCodeImportDialog) Visible() bool {
	return d.visible
}

func (d *OpenCodeImportDialog) IsVisible() bool {
	return d.visible
}

func (d *OpenCodeImportDialog) SetSize(width, height int) {
	d.width = width
	d.height = height
}

func (d *OpenCodeImportDialog) Selected() (session.OpenCodeImportEntry, bool) {
	if d.selected == nil {
		return session.OpenCodeImportEntry{}, false
	}
	return *d.selected, true
}

func (d *OpenCodeImportDialog) Update(msg tea.Msg) (*OpenCodeImportDialog, tea.Cmd) {
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
		return d, func() tea.Msg { return openCodeImportSubmitMsg{} }
	}
	return d, nil
}

func (d *OpenCodeImportDialog) View() string {
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
	lines = append(lines, fit(titleStyle.Render("Import Saved OpenCode Session")))
	lines = append(lines, "")
	if len(d.entries) == 0 {
		lines = append(lines, fit(dimStyle.Render("No saved OpenCode sessions found.")))
	} else {
		visibleRows := savedSessionImportVisibleRows(d.height)
		start, end := windowBounds(d.cursor, len(d.entries), visibleRows)
		if start > 0 {
			lines = append(lines, fit(dimStyle.Render(fmt.Sprintf("  ↑ %d more", start))))
		}
		for i := start; i < end; i++ {
			entry := d.entries[i]
			title := strings.TrimSpace(entry.Title)
			if title == "" {
				title = shortOpenCodeID(entry.ID)
			}
			updated := ""
			if !entry.UpdatedAt.IsZero() {
				updated = dimStyle.Render(entry.UpdatedAt.Local().Format("2006-01-02 15:04"))
			}
			row := fmt.Sprintf("%s  %s", title, dimStyle.Render(shortOpenCodeID(entry.ID)))
			if updated != "" {
				row = row + "  " + updated
			}
			if path := importDialogPath(entry.Directory, entry.Path); path != "" {
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

func shortOpenCodeID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
