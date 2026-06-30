package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type openCodeImportSubmitMsg struct{}
type importSourceSubmitMsg struct{}

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

	var lines []string
	lines = append(lines, titleStyle.Render("Import Saved OpenCode Session"))
	lines = append(lines, "")
	if len(d.entries) == 0 {
		lines = append(lines, dimStyle.Render("No saved OpenCode sessions found."))
	} else {
		for i, entry := range d.entries {
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
	importSourceOpenCode
)

type ImportSourceDialog struct {
	visible       bool
	cursor        int
	selected      *importSource
	openCodeCount int
	width         int
	height        int
}

func NewImportSourceDialog() *ImportSourceDialog {
	return &ImportSourceDialog{}
}

func (d *ImportSourceDialog) Show(openCodeCount int) {
	d.visible = true
	d.cursor = 0
	d.selected = nil
	d.openCodeCount = openCodeCount
}

func (d *ImportSourceDialog) Hide() {
	d.visible = false
	d.cursor = 0
	d.selected = nil
}

func (d *ImportSourceDialog) IsVisible() bool {
	return d != nil && d.visible
}

func (d *ImportSourceDialog) OpenCodeCount() int {
	if d == nil {
		return 0
	}
	return d.openCodeCount
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
		fmt.Sprintf("Saved OpenCode sessions  %s", dimStyle.Render(fmt.Sprintf("%d", d.openCodeCount))),
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

	dialogWidth := fitDialogWidth(55, 34, d.width)
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
