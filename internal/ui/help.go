package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// wrapWithHangingIndent wraps text to fit within width, indenting continuation
// lines with the given indent string so wrapped descriptions stay aligned under
// their column instead of bleeding back to column 0.
//
// width is the visible character budget for each line (excluding the indent on
// continuation lines). If width <= 0 the text is returned unchanged.
func wrapWithHangingIndent(text string, width int, indent string) string {
	if text == "" || width <= 0 {
		return text
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var lines []string
	current := words[0]
	for _, w := range words[1:] {
		if len(current)+1+len(w) <= width {
			current += " " + w
			continue
		}
		lines = append(lines, current)
		current = w
	}
	lines = append(lines, current)
	if len(lines) == 1 {
		return lines[0]
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

// HelpOverlay shows keyboard shortcuts in a modal
type HelpOverlay struct {
	visible      bool
	width        int
	height       int
	scrollOffset int // Current scroll position for small screens
	hotkeys      map[string]string
}

// NewHelpOverlay creates a new help overlay
func NewHelpOverlay() *HelpOverlay {
	return &HelpOverlay{hotkeys: resolveHotkeys(nil)}
}

// Show makes the help overlay visible
func (h *HelpOverlay) Show() {
	h.visible = true
	h.scrollOffset = 0
}

// Hide hides the help overlay
func (h *HelpOverlay) Hide() {
	h.visible = false
}

// IsVisible returns whether the help overlay is visible
func (h *HelpOverlay) IsVisible() bool {
	return h.visible
}

// SetSize sets the dimensions for centering
func (h *HelpOverlay) SetSize(width, height int) {
	h.width = width
	h.height = height
}

// SetHotkeys updates displayed hotkeys for dynamic help rendering.
func (h *HelpOverlay) SetHotkeys(bindings map[string]string) {
	h.hotkeys = make(map[string]string, len(bindings))
	for action, key := range bindings {
		h.hotkeys[action] = key
	}
}

func (h *HelpOverlay) key(action, fallback string) string {
	if h.hotkeys == nil {
		return fallback
	}
	if key, ok := h.hotkeys[action]; ok {
		trimmed := strings.TrimSpace(key)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type helpSection struct {
	title string
	items [][2]string
}

func actionCategoryTitle(category ActionCategory) string {
	switch category {
	case ActionCategorySelected:
		return "SELECTED ITEM"
	case ActionCategorySessions:
		return "SESSIONS"
	case ActionCategoryManage:
		return "MANAGE"
	case ActionCategoryView:
		return "VIEW"
	case ActionCategoryApp:
		return "APPLICATION"
	default:
		return strings.ToUpper(string(category))
	}
}

// sections returns structural controls plus the enabled accelerator catalog.
// The menu and help must never advertise a disabled shortcut, and catalog
// labels must have one rendering source so menus, settings, and help agree.
func (h *HelpOverlay) sections() []helpSection {
	showNotes := false
	if cfg, _ := session.LoadUserConfig(); cfg != nil {
		showNotes = cfg.GetShowNotes()
	}
	sections := []helpSection{{
		title: "ESSENTIAL CONTROLS",
		items: [][2]string{
			{"Space", "Menu"},
			{"↑ / ↓", "Move selection"},
			{"← / →", "Collapse / expand"},
			{"Enter", "Open selected item"},
			{"Esc", "Back / close"},
		},
	}}

	byCategory := make(map[ActionCategory]*helpSection)
	for _, definition := range actionDefinitions() {
		if definition.ID == ActionEditNotes && !showNotes {
			continue
		}
		if definition.HotkeyAction == "" {
			continue
		}
		key := h.key(definition.HotkeyAction, "")
		if key == "" {
			continue
		}
		section := byCategory[definition.Category]
		if section == nil {
			sections = append(sections, helpSection{title: actionCategoryTitle(definition.Category)})
			section = &sections[len(sections)-1]
			byCategory[definition.Category] = section
		}
		section.items = append(section.items, [2]string{key, definition.Label})
	}
	return sections
}

// Update handles messages for the help overlay
func (h *HelpOverlay) Update(msg tea.Msg) (*HelpOverlay, tea.Cmd) {
	if !h.visible {
		return h, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "j", "down":
			h.scrollOffset++
			return h, nil
		case "k", "up":
			if h.scrollOffset > 0 {
				h.scrollOffset--
			}
			return h, nil
		case "ctrl+d", "pgdown":
			h.scrollOffset += 10
			return h, nil
		case "ctrl+u", "pgup":
			if h.scrollOffset > 10 {
				h.scrollOffset -= 10
			} else {
				h.scrollOffset = 0
			}
			return h, nil
		case "home":
			h.scrollOffset = 0
			return h, nil
		case "end":
			h.scrollOffset = 9999 // Will be clamped in View()
			return h, nil
		case "g":
			h.scrollOffset = 0
			return h, nil
		case "G":
			h.scrollOffset = 9999 // Will be clamped in View()
			return h, nil
		default:
			// Any other key closes the help overlay
			h.Hide()
		}
	}
	return h, nil
}

// View renders the help overlay
func (h *HelpOverlay) View() string {
	if !h.visible {
		return ""
	}

	sections := h.sections()

	// Styles
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorAccent)

	sectionStyle := lipgloss.NewStyle().
		Foreground(ColorCyan).
		Bold(true)

	// Responsive dialog width: prefer wider so descriptions don't wrap
	// awkwardly. Default 70, scale up to ~80 when the terminal is roomy; the
	// shared helper handles shrinking (and the never-overflow clamp) on narrow
	// terminals.
	preferred := 70
	if h.width >= 100 {
		preferred = 80
	}
	dialogWidth := fitDialogWidth(preferred, 35, h.width)
	keyWidth := 14
	if dialogWidth < 45 {
		keyWidth = 10 // Compact key column for small screens
	}
	// Description column budget: dialogWidth minus border (2) + padding (4)
	// + leading "  " (2) + key column. Hanging indent for wrapped lines is
	// the same width as the leading spaces + key column so continuations sit
	// aligned under the description column.
	descWidth := dialogWidth - 2 - 4 - 2 - keyWidth
	if descWidth < 10 {
		descWidth = 10
	}
	hangingIndent := strings.Repeat(" ", 2+keyWidth)

	keyStyle := lipgloss.NewStyle().
		Foreground(ColorPurple).
		Width(keyWidth)

	descStyle := lipgloss.NewStyle().
		Foreground(ColorText)

	separatorStyle := lipgloss.NewStyle().Foreground(ColorBorder)
	versionStyle := lipgloss.NewStyle().
		Foreground(ColorComment).
		Italic(true)
	footerStyle := lipgloss.NewStyle().
		Foreground(ColorComment).
		Italic(true)
	scrollIndicatorStyle := lipgloss.NewStyle().
		Foreground(ColorYellow).
		Bold(true)

	// Build content as lines for scrolling support
	var lines []string

	lines = append(lines, titleStyle.Render("KEYBOARD SHORTCUTS"))
	lines = append(lines, "")

	for i, section := range sections {
		lines = append(lines, sectionStyle.Render(section.title))
		for _, item := range section.items {
			wrapped := wrapWithHangingIndent(item[1], descWidth, hangingIndent)
			line := "  " + keyStyle.Render(item[0]) + descStyle.Render(wrapped)
			lines = append(lines, line)
		}
		if i < len(sections)-1 {
			lines = append(lines, "")
		}
	}

	// Version info
	separatorWidth := dialogWidth - 8
	if separatorWidth < 20 {
		separatorWidth = 20
	}
	lines = append(lines, "")
	lines = append(lines, separatorStyle.Render(strings.Repeat("─", separatorWidth)))
	lines = append(lines, versionStyle.Render("Agent Deck v"+Version))

	totalLines := len(lines)

	// Calculate available height for content (screen height minus dialog borders, padding, footer)
	// Dialog box has 2 lines for border (top/bottom) + 1 padding each side + 2 for footer area
	availableHeight := h.height - 8
	if availableHeight < 10 {
		availableHeight = 10
	}

	// Check if scrolling is needed
	needsScroll := totalLines > availableHeight

	// Clamp scroll offset
	maxScroll := totalLines - availableHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if h.scrollOffset > maxScroll {
		h.scrollOffset = maxScroll
	}
	if h.scrollOffset < 0 {
		h.scrollOffset = 0
	}

	// Build visible content
	var content strings.Builder

	if needsScroll {
		// Show scroll indicator at top if not at beginning
		if h.scrollOffset > 0 {
			content.WriteString(scrollIndicatorStyle.Render("▲ more above"))
			content.WriteString("\n")
			availableHeight-- // Account for indicator line
		}

		// Determine end index
		endIdx := h.scrollOffset + availableHeight
		if h.scrollOffset > 0 {
			// Leave room for bottom indicator if needed
			if endIdx < totalLines {
				availableHeight--
				endIdx = h.scrollOffset + availableHeight
			}
		}
		if endIdx > totalLines {
			endIdx = totalLines
		}

		// Render visible lines
		for i := h.scrollOffset; i < endIdx; i++ {
			content.WriteString(lines[i])
			if i < endIdx-1 {
				content.WriteString("\n")
			}
		}

		// Show scroll indicator at bottom if more content below
		if endIdx < totalLines {
			content.WriteString("\n")
			content.WriteString(scrollIndicatorStyle.Render("▼ more below"))
		}
	} else {
		// No scrolling needed, render all lines
		for i, line := range lines {
			content.WriteString(line)
			if i < len(lines)-1 {
				content.WriteString("\n")
			}
		}
	}

	// Footer with appropriate hint
	content.WriteString("\n\n")
	if needsScroll {
		content.WriteString(footerStyle.Render("j/k scroll • any other key to close"))
	} else {
		content.WriteString(footerStyle.Render("Press any key to close"))
	}

	// Wrap in dialog box
	box := DialogBoxStyle.
		Width(dialogWidth).
		Render(content.String())

	return centerInScreen(box, h.width, h.height)
}
