package ui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ActionMenuItem combines stable action metadata with its availability in the
// current Home context. Disabled actions stay visible so the menu can explain
// why they are unavailable.
type ActionMenuItem struct {
	Action         ActionDefinition
	Enabled        bool
	DisabledReason string
}

// ActionMenuSelection is intentionally action-ID-only: Home dispatches menu
// choices directly instead of translating them back into shortcut keys.
type ActionMenuSelection struct {
	ID ActionID
}

// ActionMenu is a focused Bubble Tea overlay model. It owns presentation and
// selection state only; it never mutates sessions.
type ActionMenu struct {
	visible  bool
	items    []ActionMenuItem
	filtered []int
	cursor   int
	scroll   int
	query    string
	pending  *ActionMenuSelection
	width    int
	height   int
}

func NewActionMenu() *ActionMenu {
	return &ActionMenu{}
}

func (m *ActionMenu) Show(items []ActionMenuItem) {
	m.visible = true
	m.items = append(m.items[:0], items...)
	m.query = ""
	m.cursor = 0
	m.scroll = 0
	m.pending = nil
	m.refilter()
}

func (m *ActionMenu) Hide() {
	if m == nil {
		return
	}
	m.visible = false
	m.query = ""
	m.cursor = 0
	m.scroll = 0
}

func (m *ActionMenu) IsVisible() bool {
	return m != nil && m.visible
}

func (m *ActionMenu) SetSize(width, height int) {
	if m == nil {
		return
	}
	m.width = width
	m.height = height
	m.clampScroll()
}

func (m *ActionMenu) Query() string {
	if m == nil {
		return ""
	}
	return m.query
}

func (m *ActionMenu) HasAction(id ActionID) bool {
	if m == nil {
		return false
	}
	for _, item := range m.items {
		if item.Action.ID == id {
			return true
		}
	}
	return false
}

// ConsumeSelection returns a pending menu choice exactly once.
func (m *ActionMenu) ConsumeSelection() (ActionID, bool) {
	if m == nil || m.pending == nil {
		return "", false
	}
	id := m.pending.ID
	m.pending = nil
	return id, true
}

func (m *ActionMenu) Update(msg tea.Msg) (*ActionMenu, tea.Cmd) {
	if m == nil || !m.visible {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.moveCursor(-1)
		case tea.MouseButtonWheelDown:
			m.moveCursor(1)
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.Hide()
		case "up", "ctrl+p":
			m.moveCursor(-1)
		case "down", "ctrl+n":
			m.moveCursor(1)
		case "enter":
			m.selectCurrent()
		case "backspace", "ctrl+h":
			m.removeLastQueryRune()
		case "ctrl+u":
			m.query = ""
			m.refilter()
		default:
			if msg.Type == tea.KeyRunes {
				for _, r := range msg.Runes {
					if !unicode.IsControl(r) {
						m.query += string(r)
					}
				}
				m.refilter()
			}
		}
	}

	// The overlay deliberately emits no command for handled input. Home reads a
	// completed ActionID via ConsumeSelection, preventing keys from leaking into
	// the overview dispatcher.
	return m, nil
}

func (m *ActionMenu) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		m.cursor = 0
		m.scroll = 0
		return
	}
	m.cursor = (m.cursor + delta + len(m.filtered)) % len(m.filtered)
	m.clampScroll()
}

func (m *ActionMenu) selectCurrent() {
	if len(m.filtered) == 0 || m.cursor < 0 || m.cursor >= len(m.filtered) {
		return
	}
	item := m.items[m.filtered[m.cursor]]
	if !item.Enabled {
		return
	}
	m.pending = &ActionMenuSelection{ID: item.Action.ID}
	m.visible = false
}

func (m *ActionMenu) removeLastQueryRune() {
	runes := []rune(m.query)
	if len(runes) == 0 {
		return
	}
	m.query = string(runes[:len(runes)-1])
	m.refilter()
}

func (m *ActionMenu) refilter() {
	m.filtered = m.filtered[:0]
	query := strings.ToLower(strings.TrimSpace(m.query))
	for i, item := range m.items {
		haystack := strings.ToLower(strings.Join([]string{
			string(item.Action.ID),
			item.Action.Label,
			string(item.Action.Category),
		}, " "))
		if query == "" || strings.Contains(haystack, query) {
			m.filtered = append(m.filtered, i)
		}
	}
	m.cursor = 0
	m.scroll = 0
	m.clampScroll()
}

func (m *ActionMenu) visibleItemLimit() int {
	if m.height <= 0 {
		return 16
	}
	limit := m.height - 12
	if limit < 3 {
		return 3
	}
	if limit > 20 {
		return 20
	}
	return limit
}

func (m *ActionMenu) clampScroll() {
	limit := m.visibleItemLimit()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+limit {
		m.scroll = m.cursor - limit + 1
	}
	maxScroll := len(m.filtered) - limit
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func actionCategoryLabel(category ActionCategory) string {
	switch category {
	case ActionCategorySelected:
		return "Selected item"
	case ActionCategorySessions:
		return "Sessions"
	case ActionCategoryManage:
		return "Manage"
	case ActionCategoryView:
		return "View"
	case ActionCategoryApp:
		return "Application"
	default:
		return "Other"
	}
}

func (m *ActionMenu) View() string {
	if m == nil || !m.visible {
		return ""
	}

	titleStyle := DialogTitleStyle
	categoryStyle := lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(ColorText)
	disabledStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	footerStyle := lipgloss.NewStyle().Foreground(ColorComment).Italic(true)

	lines := []string{titleStyle.Render("Actions"), ""}
	query := m.query
	if query == "" {
		query = DimStyle.Render("type to filter")
	}
	lines = append(lines, SearchPromptStyle.Render("Search: ")+query)
	lines = append(lines, "")

	if len(m.filtered) == 0 {
		lines = append(lines, disabledStyle.Render("No matching actions"))
	} else {
		limit := m.visibleItemLimit()
		end := m.scroll + limit
		if end > len(m.filtered) {
			end = len(m.filtered)
		}
		lastCategory := ActionCategory("")
		for filteredIndex := m.scroll; filteredIndex < end; filteredIndex++ {
			item := m.items[m.filtered[filteredIndex]]
			if item.Action.Category != lastCategory {
				if lastCategory != "" {
					lines = append(lines, "")
				}
				lines = append(lines, categoryStyle.Render(actionCategoryLabel(item.Action.Category)))
				lastCategory = item.Action.Category
			}

			label := item.Action.Label
			if !item.Enabled && item.DisabledReason != "" {
				label += " — " + item.DisabledReason
			}
			prefix := "  "
			style := normalStyle
			if !item.Enabled {
				style = disabledStyle
			}
			if filteredIndex == m.cursor {
				prefix = "> "
				if item.Enabled {
					style = selectedStyle
				}
			}
			lines = append(lines, prefix+style.Render(label))
		}

		if m.scroll > 0 || end < len(m.filtered) {
			lines = append(lines, DimStyle.Render(fmt.Sprintf("%d–%d of %d", m.scroll+1, end, len(m.filtered))))
		}
	}

	lines = append(lines, "")
	lines = append(lines, footerStyle.Render("↑/↓ navigate · Enter choose · Esc close · type to filter"))

	dialogWidth := fitDialogWidth(72, 42, m.width)
	box := DialogBoxStyle.Width(dialogWidth).Render(strings.Join(lines, "\n"))
	return centerInScreen(box, m.width, m.height)
}
