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
	visible      bool
	entries      []session.OpenCodeImportEntry
	cursor       int
	selected     *session.OpenCodeImportEntry
	width        int
	height       int
	searchActive bool
	searchQuery  string
}

func NewOpenCodeImportDialog() *OpenCodeImportDialog {
	return &OpenCodeImportDialog{}
}

func (d *OpenCodeImportDialog) Show(entries []session.OpenCodeImportEntry) {
	d.visible = true
	d.entries = append(d.entries[:0], entries...)
	d.cursor = 0
	d.selected = nil
	d.searchActive = false
	d.searchQuery = ""
}

func (d *OpenCodeImportDialog) Hide() {
	d.visible = false
	d.entries = nil
	d.cursor = 0
	d.selected = nil
	d.searchActive = false
	d.searchQuery = ""
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

	if handled, changed := importDialogHandleSearchKey(key, &d.searchActive, &d.searchQuery); handled {
		if changed {
			d.cursor = importDialogFirstMatch(d.matchingIndexes())
		}
		return d, nil
	}

	switch key.String() {
	case "/":
		d.searchActive = true
		d.searchQuery = ""
		d.cursor = importDialogFirstMatch(d.matchingIndexes())
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
	indexes := d.matchingIndexes()
	if len(d.entries) == 0 {
		lines = append(lines, fit(dimStyle.Render("No saved OpenCode sessions found.")))
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
			title := strings.TrimSpace(entry.Title)
			if title == "" {
				title = shortOpenCodeID(entry.ID)
			}
			updated := ""
			if !entry.UpdatedAt.IsZero() {
				updated = entry.UpdatedAt.Local().Format("2006-01-02 15:04")
			}
			lines = importDialogAppendEntry(
				lines,
				i == renderCursor,
				title,
				shortOpenCodeID(entry.ID),
				updated,
				importDialogPath(entry.Directory, entry.Path),
				innerWidth,
				selectedStyle,
				normalStyle,
				dimStyle,
			)
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

func (d *OpenCodeImportDialog) matchingIndexes() []int {
	return importDialogSearchIndexes(len(d.entries), d.searchQuery, func(i int) []string {
		entry := d.entries[i]
		return []string{
			entry.Title,
			entry.ID,
			shortOpenCodeID(entry.ID),
			entry.Directory,
			entry.Path,
		}
	})
}

func shortOpenCodeID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
