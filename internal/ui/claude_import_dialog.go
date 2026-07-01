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
	visible      bool
	entries      []session.ClaudeImportCandidate
	cursor       int
	selected     *session.ClaudeImportCandidate
	width        int
	height       int
	searchActive bool
	searchQuery  string
}

func NewClaudeImportDialog() *ClaudeImportDialog {
	return &ClaudeImportDialog{}
}

func (d *ClaudeImportDialog) Show(entries []session.ClaudeImportCandidate) {
	d.visible = true
	d.entries = append(d.entries[:0], entries...)
	d.cursor = 0
	d.selected = nil
	d.searchActive = false
	d.searchQuery = ""
}

func (d *ClaudeImportDialog) Hide() {
	d.visible = false
	d.entries = nil
	d.cursor = 0
	d.selected = nil
	d.searchActive = false
	d.searchQuery = ""
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

	if handled, changed := importDialogHandleSearchKey(key, &d.searchActive, &d.searchQuery); handled {
		if changed {
			d.cursor = importDialogNormalizeCursor(d.cursor, d.matchingIndexes())
		}
		return d, nil
	}

	switch key.String() {
	case "/":
		d.searchActive = true
		d.searchQuery = ""
		d.cursor = importDialogNormalizeCursor(d.cursor, d.matchingIndexes())
	case "j", "down":
		if indexes := d.matchingIndexes(); len(indexes) > 0 {
			d.cursor = importDialogMoveCursor(d.cursor, indexes, 1)
		}
	case "k", "up":
		if indexes := d.matchingIndexes(); len(indexes) > 0 {
			d.cursor = importDialogMoveCursor(d.cursor, indexes, -1)
		}
	case "esc":
		d.Hide()
	case "enter":
		indexes := d.matchingIndexes()
		if len(indexes) == 0 {
			return d, nil
		}
		d.cursor = importDialogNormalizeCursor(d.cursor, indexes)
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
	indexes := d.matchingIndexes()
	if len(d.entries) == 0 {
		lines = append(lines, fit(dimStyle.Render("No saved Claude sessions found.")))
	} else if len(indexes) == 0 {
		lines = append(lines, fit(dimStyle.Render(fmt.Sprintf("No matches for %q.", d.searchQuery))))
	} else {
		visibleRows := savedSessionImportVisibleRows(d.height)
		renderCursor := importDialogNormalizeCursor(d.cursor, indexes)
		cursorPos := importDialogCursorPosition(renderCursor, indexes)
		start, end := windowBounds(cursorPos, len(indexes), visibleRows)
		if start > 0 {
			lines = append(lines, fit(dimStyle.Render(fmt.Sprintf("  ↑ %d more", start))))
		}
		for pos := start; pos < end; pos++ {
			i := indexes[pos]
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
			if i == renderCursor {
				lines = append(lines, fit("> "+selectedStyle.Render(row)))
			} else {
				lines = append(lines, fit("  "+normalStyle.Render(row)))
			}
		}
		if end < len(indexes) {
			lines = append(lines, fit(dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(indexes)-end))))
		}
	}
	lines = append(lines, "")
	lines = append(lines, fit(footerStyle.Render(importDialogFooter(d.searchActive, d.searchQuery))))

	box := DialogBoxStyle.Width(dialogWidth).Render(strings.Join(lines, "\n"))
	return centerInScreen(box, d.width, d.height)
}

func (d *ClaudeImportDialog) matchingIndexes() []int {
	return importDialogSearchIndexes(len(d.entries), d.searchQuery, func(i int) []string {
		entry := d.entries[i]
		return []string{
			entry.Name,
			entry.SessionID,
			shortClaudeImportID(entry.SessionID),
			entry.CWD,
			entry.Path,
		}
	})
}

func shortClaudeImportID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
