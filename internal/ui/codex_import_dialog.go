package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type codexImportSubmitMsg struct{}

// CodexImportDialog lets the user choose a saved Codex session from the Codex
// session index.
type CodexImportDialog struct {
	visible  bool
	entries  []session.CodexIndexEntry
	cursor   int
	selected *session.CodexIndexEntry
	width    int
	height   int
}

func NewCodexImportDialog() *CodexImportDialog {
	return &CodexImportDialog{}
}

func (d *CodexImportDialog) Show(entries []session.CodexIndexEntry) {
	d.visible = true
	d.entries = append(d.entries[:0], entries...)
	d.cursor = 0
	d.selected = nil
}

func (d *CodexImportDialog) Hide() {
	d.visible = false
	d.entries = nil
	d.cursor = 0
	d.selected = nil
}

func (d *CodexImportDialog) Visible() bool {
	return d.visible
}

func (d *CodexImportDialog) IsVisible() bool {
	return d.visible
}

func (d *CodexImportDialog) SetSize(width, height int) {
	d.width = width
	d.height = height
}

func (d *CodexImportDialog) Selected() (session.CodexIndexEntry, bool) {
	if d.selected == nil {
		return session.CodexIndexEntry{}, false
	}
	return *d.selected, true
}

func (d *CodexImportDialog) Update(msg tea.Msg) (*CodexImportDialog, tea.Cmd) {
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
		return d, func() tea.Msg { return codexImportSubmitMsg{} }
	}
	return d, nil
}

func (d *CodexImportDialog) View() string {
	if !d.visible {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(ColorText)
	dimStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	footerStyle := lipgloss.NewStyle().Foreground(ColorComment).Italic(true)

	var lines []string
	lines = append(lines, titleStyle.Render("Import Saved Codex Session"))
	lines = append(lines, "")
	if len(d.entries) == 0 {
		lines = append(lines, dimStyle.Render("No saved Codex sessions found."))
	} else {
		for i, entry := range d.entries {
			title := strings.TrimSpace(entry.ThreadName)
			if title == "" {
				title = shortCodexID(entry.ID)
			}
			row := fmt.Sprintf("%s  %s  %s",
				title,
				dimStyle.Render(shortCodexID(entry.ID)),
				dimStyle.Render(entry.UpdatedAt.Local().Format("2006-01-02 15:04")),
			)
			if path := strings.TrimSpace(entry.Path); path != "" {
				row += "  " + dimStyle.Render(path)
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

	dialogWidth := fitDialogWidth(96, 44, d.width)
	box := DialogBoxStyle.Width(dialogWidth).Render(strings.Join(lines, "\n"))
	return centerInScreen(box, d.width, d.height)
}

func shortCodexID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
