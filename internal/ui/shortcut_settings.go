package ui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ShortcutSettings edits a draft [hotkeys] table. It owns no persistence;
// Home consumes the save signal and commits the full draft atomically.
type ShortcutSettings struct {
	visible       bool
	definitions   []ActionDefinition
	draft         map[string]string
	mode          string
	query         string
	filtered      []int
	cursor        int
	scroll        int
	capturing     bool
	pendingPreset string
	errText       string
	savePending   bool
	width         int
	height        int
}

func NewShortcutSettings() *ShortcutSettings {
	definitions := make([]ActionDefinition, 0, len(actionDefinitions()))
	for _, definition := range actionDefinitions() {
		if definition.HotkeyAction != "" && definition.Optional {
			definitions = append(definitions, definition)
		}
	}
	return &ShortcutSettings{definitions: definitions}
}

func (s *ShortcutSettings) Show(cfg *session.UserConfig) {
	if s == nil {
		return
	}
	s.visible = true
	s.query = ""
	s.cursor = 0
	s.scroll = 0
	s.capturing = false
	s.pendingPreset = ""
	s.errText = ""
	s.savePending = false
	s.mode = shortcutModeMenu
	s.draft = make(map[string]string)
	if cfg != nil {
		s.mode = cfg.UI.GetShortcutMode()
		for action, key := range cfg.Hotkeys {
			s.draft[action] = strings.TrimSpace(key)
		}
	}
	for _, definition := range s.definitions {
		action := definition.HotkeyAction
		if _, explicit := s.draft[action]; explicit {
			continue
		}
		if s.mode == shortcutModeLegacy && !defaultDisabledHotkeys[action] {
			s.draft[action] = definition.DefaultKey
		} else {
			s.draft[action] = ""
		}
	}
	s.refilter()
}

func (s *ShortcutSettings) Hide() {
	if s == nil {
		return
	}
	s.visible = false
	s.capturing = false
	s.pendingPreset = ""
	s.errText = ""
}

func (s *ShortcutSettings) IsVisible() bool {
	return s != nil && s.visible
}

func (s *ShortcutSettings) IsCapturing() bool {
	return s != nil && s.capturing
}

func (s *ShortcutSettings) Error() string {
	if s == nil {
		return ""
	}
	return s.errText
}

func (s *ShortcutSettings) SetSize(width, height int) {
	if s == nil {
		return
	}
	s.width = width
	s.height = height
	s.clampScroll()
}

func (s *ShortcutSettings) Select(id ActionID) bool {
	if s == nil {
		return false
	}
	for i, definitionIndex := range s.filtered {
		if s.definitions[definitionIndex].ID == id {
			s.cursor = i
			s.clampScroll()
			return true
		}
	}
	return false
}

func (s *ShortcutSettings) currentDefinition() (ActionDefinition, bool) {
	if s == nil || len(s.filtered) == 0 || s.cursor < 0 || s.cursor >= len(s.filtered) {
		return ActionDefinition{}, false
	}
	return s.definitions[s.filtered[s.cursor]], true
}

func (s *ShortcutSettings) Toggle() {
	definition, ok := s.currentDefinition()
	if !ok {
		return
	}
	action := definition.HotkeyAction
	if strings.TrimSpace(s.draft[action]) != "" {
		s.draft[action] = ""
		s.errText = ""
		return
	}
	if err := s.assignBinding(definition, definition.DefaultKey); err != nil {
		s.errText = err.Error()
	}
}

func (s *ShortcutSettings) RequestPreset(mode string) {
	if s == nil {
		return
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != shortcutModeMenu && mode != shortcutModeLegacy {
		s.errText = fmt.Sprintf("unknown shortcut preset %q", mode)
		return
	}
	s.pendingPreset = mode
	s.errText = ""
}

func (s *ShortcutSettings) ConfirmPreset() bool {
	if s == nil || s.pendingPreset == "" {
		return false
	}
	mode := s.pendingPreset
	s.pendingPreset = ""
	s.mode = mode
	for _, definition := range s.definitions {
		action := definition.HotkeyAction
		if mode == shortcutModeLegacy && !defaultDisabledHotkeys[action] {
			s.draft[action] = definition.DefaultKey
		} else {
			s.draft[action] = ""
		}
	}
	if _, _, err := s.Bindings(); err != nil {
		s.errText = err.Error()
		return false
	}
	s.errText = ""
	return true
}

func (s *ShortcutSettings) Bindings() (string, map[string]string, error) {
	if s == nil {
		return shortcutModeMenu, nil, nil
	}
	out := make(map[string]string, len(s.draft))
	known := make(map[string]string, len(s.definitions))
	for action, key := range s.draft {
		out[action] = key
		if _, ok := defaultHotkeyBindings[action]; ok {
			known[action] = key
		}
	}
	if err := validateHotkeyBindings(known); err != nil {
		return s.mode, out, err
	}
	return s.mode, out, nil
}

func (s *ShortcutSettings) ConsumeSave() bool {
	if s == nil || !s.savePending {
		return false
	}
	s.savePending = false
	return true
}

func (s *ShortcutSettings) Update(msg tea.Msg) (*ShortcutSettings, tea.Cmd) {
	if s == nil || !s.visible {
		return s, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}

	if s.pendingPreset != "" {
		switch key.String() {
		case "y", "enter":
			s.ConfirmPreset()
		case "n", "esc":
			s.pendingPreset = ""
		}
		return s, nil
	}

	if s.capturing {
		if key.String() == "esc" {
			s.capturing = false
			s.errText = ""
			return s, nil
		}
		definition, exists := s.currentDefinition()
		if !exists {
			s.capturing = false
			return s, nil
		}
		binding := normalizeCapturedBinding(key)
		if err := s.assignBinding(definition, binding); err != nil {
			s.errText = err.Error()
			return s, nil
		}
		s.capturing = false
		s.errText = ""
		return s, nil
	}

	switch key.String() {
	case "esc":
		s.Hide()
	case "up", "ctrl+p":
		s.moveCursor(-1)
	case "down", "ctrl+n":
		s.moveCursor(1)
	case " ":
		s.Toggle()
	case "enter":
		if _, exists := s.currentDefinition(); exists {
			s.capturing = true
			s.errText = "Press a key for this action, or Escape to cancel"
		}
	case "backspace", "ctrl+h":
		s.removeLastQueryRune()
	case "ctrl+u":
		s.query = ""
		s.refilter()
	case "alt+m":
		s.RequestPreset(shortcutModeMenu)
	case "alt+l":
		s.RequestPreset(shortcutModeLegacy)
	case "ctrl+s":
		if _, _, err := s.Bindings(); err != nil {
			s.errText = err.Error()
		} else {
			s.savePending = true
		}
	default:
		if key.Type == tea.KeyRunes {
			for _, r := range key.Runes {
				if !unicode.IsControl(r) {
					s.query += string(r)
				}
			}
			s.refilter()
		}
	}
	return s, nil
}

func normalizeCapturedBinding(key tea.KeyMsg) string {
	binding := key.String()
	if binding == " " {
		return "space"
	}
	if binding == "alt+ " {
		return "alt+space"
	}
	return binding
}

func (s *ShortcutSettings) assignBinding(definition ActionDefinition, binding string) error {
	binding = normalizeHotkeyBinding(binding)
	if binding == "" {
		return fmt.Errorf("%s: press a supported key", definition.Label)
	}
	if strings.EqualFold(binding, "space") || binding == " " {
		return fmt.Errorf("%s cannot use Space because Space opens the global menu", definition.Label)
	}
	if !supportedHotkeyBinding(binding) {
		return fmt.Errorf("%s cannot use unsupported key %q", definition.Label, binding)
	}
	aliases := make(map[string]bool)
	for _, alias := range hotkeyAliases(binding) {
		aliases[alias] = true
	}
	for _, other := range s.definitions {
		if other.ID == definition.ID {
			continue
		}
		otherBinding := strings.TrimSpace(s.draft[other.HotkeyAction])
		if otherBinding == "" {
			continue
		}
		for _, alias := range hotkeyAliases(normalizeHotkeyBinding(otherBinding)) {
			if aliases[alias] {
				return fmt.Errorf("%s conflicts with %s on %q", definition.Label, other.Label, binding)
			}
		}
	}
	s.draft[definition.HotkeyAction] = binding
	return nil
}

func (s *ShortcutSettings) refilter() {
	s.filtered = s.filtered[:0]
	query := strings.ToLower(strings.TrimSpace(s.query))
	for i, definition := range s.definitions {
		haystack := strings.ToLower(strings.Join([]string{
			string(definition.ID), definition.Label, string(definition.Category), definition.DefaultKey,
		}, " "))
		if query == "" || strings.Contains(haystack, query) {
			s.filtered = append(s.filtered, i)
		}
	}
	s.cursor = 0
	s.scroll = 0
	s.clampScroll()
}

func (s *ShortcutSettings) removeLastQueryRune() {
	runes := []rune(s.query)
	if len(runes) == 0 {
		return
	}
	s.query = string(runes[:len(runes)-1])
	s.refilter()
}

func (s *ShortcutSettings) moveCursor(delta int) {
	if len(s.filtered) == 0 {
		return
	}
	s.cursor = (s.cursor + delta + len(s.filtered)) % len(s.filtered)
	s.clampScroll()
}

func (s *ShortcutSettings) visibleItemLimit() int {
	if s.height <= 0 {
		return 15
	}
	limit := s.height - 13
	if limit < 3 {
		return 3
	}
	if limit > 22 {
		return 22
	}
	return limit
}

func (s *ShortcutSettings) clampScroll() {
	limit := s.visibleItemLimit()
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	}
	if s.cursor >= s.scroll+limit {
		s.scroll = s.cursor - limit + 1
	}
	maxScroll := len(s.filtered) - limit
	if maxScroll < 0 {
		maxScroll = 0
	}
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
}

func (s *ShortcutSettings) View() string {
	if s == nil || !s.visible {
		return ""
	}
	titleStyle := DialogTitleStyle
	categoryStyle := lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	enabledStyle := lipgloss.NewStyle().Foreground(ColorGreen)
	disabledStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	errorStyle := lipgloss.NewStyle().Foreground(ColorRed)
	footerStyle := lipgloss.NewStyle().Foreground(ColorComment).Italic(true)

	lines := []string{titleStyle.Render("Keyboard shortcuts"), ""}
	modeLabel := "Menu-first"
	if s.mode == shortcutModeLegacy {
		modeLabel = "Legacy"
	}
	lines = append(lines, fmt.Sprintf("Preset: %s  %s", modeLabel, DimStyle.Render("Alt+M menu-first · Alt+L legacy")))
	query := s.query
	if query == "" {
		query = DimStyle.Render("type to filter")
	}
	lines = append(lines, SearchPromptStyle.Render("Search: ")+query, "")

	if s.pendingPreset != "" {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(ColorYellow).Bold(true).Render("Apply "+s.pendingPreset+" preset?"),
			"This replaces every optional shortcut in the draft.",
			"",
			footerStyle.Render("Y/Enter apply · N/Esc cancel"),
		)
		return s.renderBox(lines)
	}

	if len(s.filtered) == 0 {
		lines = append(lines, disabledStyle.Render("No matching shortcuts"))
	} else {
		limit := s.visibleItemLimit()
		end := min(len(s.filtered), s.scroll+limit)
		lastCategory := ActionCategory("")
		for filteredIndex := s.scroll; filteredIndex < end; filteredIndex++ {
			definition := s.definitions[s.filtered[filteredIndex]]
			if definition.Category != lastCategory {
				if lastCategory != "" {
					lines = append(lines, "")
				}
				lines = append(lines, categoryStyle.Render(actionCategoryLabel(definition.Category)))
				lastCategory = definition.Category
			}
			binding := strings.TrimSpace(s.draft[definition.HotkeyAction])
			mark := "[ ]"
			bindingLabel := definition.DefaultKey
			style := disabledStyle
			if binding != "" {
				mark = "[x]"
				bindingLabel = binding
				style = enabledStyle
			}
			label := fmt.Sprintf("%s %-34s %s", mark, definition.Label, bindingLabel)
			prefix := "  "
			if filteredIndex == s.cursor {
				prefix = "> "
				style = selectedStyle
			}
			lines = append(lines, prefix+style.Render(label))
		}
		if s.scroll > 0 || end < len(s.filtered) {
			lines = append(lines, DimStyle.Render(fmt.Sprintf("%d–%d of %d", s.scroll+1, end, len(s.filtered))))
		}
	}

	if s.errText != "" {
		lines = append(lines, "", errorStyle.Render(s.errText))
	}
	if s.capturing {
		lines = append(lines, footerStyle.Render("Press replacement key · Esc cancel capture"))
	} else {
		lines = append(lines, "", footerStyle.Render("Space enable/disable · Enter change key · Ctrl+S save · Esc close"))
	}
	return s.renderBox(lines)
}

func (s *ShortcutSettings) renderBox(lines []string) string {
	dialogWidth := fitDialogWidth(86, 48, s.width)
	box := DialogBoxStyle.Width(dialogWidth).Render(strings.Join(lines, "\n"))
	return centerInScreen(box, s.width, s.height)
}
