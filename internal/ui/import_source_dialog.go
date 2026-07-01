package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type importSourceSubmitMsg struct{}

type importSource int

const (
	importSourceTmux importSource = iota
	importSourceCodex
	importSourceClaude
)

type ImportSourceCounts struct {
	Codex  int
	Claude int
}

type ImportSourceDialog struct {
	visible  bool
	cursor   int
	selected *importSource
	counts   ImportSourceCounts
	width    int
	height   int
}

func NewImportSourceDialog() *ImportSourceDialog {
	return &ImportSourceDialog{}
}

func (d *ImportSourceDialog) Show(counts ImportSourceCounts) {
	d.visible = true
	d.cursor = 0
	d.selected = nil
	d.counts = counts
}

func (d *ImportSourceDialog) Hide() {
	d.visible = false
	d.cursor = 0
	d.selected = nil
}

func (d *ImportSourceDialog) IsVisible() bool {
	return d != nil && d.visible
}

func (d *ImportSourceDialog) CodexCount() int {
	if d == nil {
		return 0
	}
	return d.counts.Codex
}

func (d *ImportSourceDialog) ClaudeCount() int {
	if d == nil {
		return 0
	}
	return d.counts.Claude
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

	sources := d.sources()
	if len(sources) == 0 {
		return d, nil
	}

	switch key.String() {
	case "j", "down":
		d.cursor = (d.cursor + 1) % len(sources)
	case "k", "up":
		d.cursor = (d.cursor - 1 + len(sources)) % len(sources)
	case "esc":
		d.Hide()
	case "enter":
		selected := sources[d.cursor].source
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
	footerStyle := lipgloss.NewStyle().Foreground(ColorComment).Italic(true)

	sources := d.sources()
	var lines []string
	lines = append(lines, titleStyle.Render("Import Sessions"))
	lines = append(lines, "")
	for i, item := range sources {
		if i == d.cursor {
			lines = append(lines, "> "+selectedStyle.Render(item.label))
		} else {
			lines = append(lines, "  "+normalStyle.Render(item.label))
		}
	}
	lines = append(lines, "")
	lines = append(lines, footerStyle.Render("Enter choose | Esc cancel | j/k navigate"))

	dialogWidth := fitDialogWidth(58, 34, d.width)
	box := DialogBoxStyle.Width(dialogWidth).Render(strings.Join(lines, "\n"))
	return centerInScreen(box, d.width, d.height)
}

type importSourceItem struct {
	source importSource
	label  string
}

func (d *ImportSourceDialog) sources() []importSourceItem {
	dimStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	return []importSourceItem{
		{source: importSourceTmux, label: "Existing tmux sessions"},
		{source: importSourceCodex, label: fmt.Sprintf("Saved Codex sessions  %s", dimStyle.Render(fmt.Sprintf("%d", d.counts.Codex)))},
		{source: importSourceClaude, label: fmt.Sprintf("Saved Claude sessions %s", dimStyle.Render(fmt.Sprintf("%d", d.counts.Claude)))},
	}
}
