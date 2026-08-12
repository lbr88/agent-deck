package ui

import (
	"fmt"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	searchBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorAccent).
			Padding(0, 1)

	resultItemStyle = lipgloss.NewStyle().
			Padding(0, 2)

	selectedResultStyle = lipgloss.NewStyle().
				Padding(0, 2).
				Background(ColorAccent).
				Foreground(ColorBg)

	overlayStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorAccent).
			Padding(1, 2)
)

// SearchScope controls which known Agent Deck sessions appear in the search
// overlay. Global is the fleet snapshot (local + SSH remotes + hub nodes),
// while Local contains only sessions owned by this machine.
type SearchScope string

const (
	SearchScopeGlobal SearchScope = "global"
	SearchScopeLocal  SearchScope = "local"
)

// SessionSearchSource identifies the activation path for a search result.
type SessionSearchSource string

const (
	SearchSourceLocal  SessionSearchSource = "local"
	SearchSourceRemote SessionSearchSource = "remote"
	SearchSourceHub    SessionSearchSource = "hub"
)

// SessionSearchResult is a searchable, fleet-aware session identity. It keeps
// stable IDs instead of retaining pointers into reloadable session snapshots.
type SessionSearchResult struct {
	Source      SessionSearchSource
	SessionID   string
	Title       string
	ProjectPath string
	GroupPath   string
	Tool        string
	Status      session.Status
	Host        string

	RemoteName string
	HubNodeID  string
}

func localSessionSearchResult(inst *session.Instance, host string) *SessionSearchResult {
	if inst == nil {
		return nil
	}
	return &SessionSearchResult{
		Source:      SearchSourceLocal,
		SessionID:   inst.ID,
		Title:       inst.GetTitleThreadSafe(),
		ProjectPath: inst.ProjectPath,
		GroupPath:   inst.GroupPath,
		Tool:        inst.GetToolThreadSafe(),
		Status:      inst.GetStatusThreadSafe(),
		Host:        host,
	}
}

// Search represents the search overlay component
type Search struct {
	input       textinput.Model
	results     []*SessionSearchResult
	cursor      int
	width       int
	height      int
	visible     bool
	scope       SearchScope
	allItems    []*SessionSearchResult
	localItems  []*SessionSearchResult
	fleetItems  []*SessionSearchResult
	scopedGroup string // Non-empty => filter items to this exact GroupPath (v1.7.60)
}

// NewSearch creates a new search overlay
func NewSearch() *Search {
	ti := textinput.New()
	ti.Placeholder = "Search sessions..."
	ti.Focus()
	ti.CharLimit = 100
	ti.Width = 50

	return &Search{
		input:   ti,
		results: []*SessionSearchResult{},
		cursor:  0,
		visible: false,
		scope:   SearchScopeLocal,
	}
}

// SetItems sets the full list of items to search through. When a group scope
// has been set via SetScopedGroup, items are filtered to that group before
// storage so background reloads do not leak out-of-group sessions into a
// scoped in-group search session.
func (s *Search) SetItems(items []*session.Instance) {
	localItems := make([]*SessionSearchResult, 0, len(items))
	for _, inst := range items {
		if result := localSessionSearchResult(inst, "local"); result != nil {
			localItems = append(localItems, result)
		}
	}
	if s.scopedGroup != "" {
		filtered := make([]*SessionSearchResult, 0, len(localItems))
		for _, it := range localItems {
			if it.GroupPath == s.scopedGroup {
				filtered = append(filtered, it)
			}
		}
		localItems = filtered
	}
	s.localItems = localItems
	if s.scope == SearchScopeLocal {
		s.allItems = s.localItems
		s.updateResults()
	}
}

// SetFleetItems replaces the in-memory fleet snapshot. It also derives the
// Local pool from the same rows so Tab never needs a disk or network refresh.
func (s *Search) SetFleetItems(items []*SessionSearchResult) {
	s.fleetItems = append(s.fleetItems[:0], items...)
	s.localItems = s.localItems[:0]
	for _, item := range s.fleetItems {
		if item != nil && item.Source == SearchSourceLocal {
			s.localItems = append(s.localItems, item)
		}
	}
	if s.scope == SearchScopeGlobal {
		s.allItems = s.fleetItems
	} else {
		s.allItems = s.localItems
	}
	s.updateResults()
}

// SetScopedGroup restricts SetItems to a single group path. Pass "" to clear.
func (s *Search) SetScopedGroup(groupPath string) {
	s.scopedGroup = groupPath
}

// SetSize sets the dimensions of the search overlay
func (s *Search) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// Show preserves the historical local-search behavior for callers such as
// Alt+/. Ordinary / uses ShowGlobal explicitly.
func (s *Search) Show() {
	s.ShowLocal()
}

// ShowGlobal opens search over the complete fleet snapshot.
func (s *Search) ShowGlobal() {
	s.show(SearchScopeGlobal)
}

// ShowLocal opens search over sessions owned by this machine.
func (s *Search) ShowLocal() {
	s.show(SearchScopeLocal)
}

func (s *Search) show(scope SearchScope) {
	s.scope = scope
	if scope == SearchScopeGlobal {
		s.allItems = s.fleetItems
	} else {
		s.allItems = s.localItems
	}
	s.visible = true
	s.input.Focus()
	s.input.SetValue("")
	s.updateResults()
}

// Scope returns the active search scope.
func (s *Search) Scope() SearchScope {
	return s.scope
}

// Hide hides the search overlay and clears any group scope.
func (s *Search) Hide() {
	s.visible = false
	s.input.Blur()
	s.input.SetValue("")
	s.updateResults()
	s.scopedGroup = ""
}

// IsVisible returns whether the search overlay is visible
func (s *Search) IsVisible() bool {
	return s.visible
}

// Selected returns the currently selected item
func (s *Search) Selected() *SessionSearchResult {
	if len(s.results) == 0 {
		return nil
	}
	if s.cursor >= len(s.results) {
		s.cursor = len(s.results) - 1
	}
	return s.results[s.cursor]
}

// Update handles messages for the search overlay
// Returns the updated Search and any command to execute
func (s *Search) Update(msg tea.Msg) (*Search, tea.Cmd) {
	if !s.visible {
		return s, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			s.Hide()
			return s, nil

		case "enter":
			if len(s.results) > 0 {
				s.Hide()
				// Parent should handle the selection
			}
			return s, nil

		case "up", "ctrl+k":
			if s.cursor > 0 {
				s.cursor--
			}
			return s, nil

		case "down", "ctrl+j":
			if s.cursor < len(s.results)-1 {
				s.cursor++
			}
			return s, nil

		case "tab":
			// Alt+/ is intentionally group-local; ordinary fleet search toggles
			// between the complete snapshot and this machine's rows.
			if s.scopedGroup != "" {
				return s, nil
			}
			if s.scope == SearchScopeGlobal {
				s.scope = SearchScopeLocal
				s.allItems = s.localItems
			} else {
				s.scope = SearchScopeGlobal
				s.allItems = s.fleetItems
			}
			s.updateResults()
			return s, nil

		default:
			// Update text input
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			s.updateResults()
			return s, cmd
		}
	}

	return s, nil
}

// updateResults filters the items based on the current input
func (s *Search) updateResults() {
	s.results = filterSessionSearchResults(s.allItems, s.input.Value())
	s.cursor = 0
}

func filterSessionSearchResults(items []*SessionSearchResult, query string) []*SessionSearchResult {
	if strings.TrimSpace(query) == "" {
		return append([]*SessionSearchResult(nil), items...)
	}

	// Reuse the established local title/path/tool/status and fuzzy semantics by
	// adapting each fleet result to the same search surface. Host and group are
	// appended to ProjectPath so they participate without changing session's
	// generic query API.
	proxies := make([]*session.Instance, 0, len(items))
	byProxy := make(map[*session.Instance]*SessionSearchResult, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		proxy := &session.Instance{
			Title:       item.Title,
			ProjectPath: strings.Join([]string{item.ProjectPath, item.GroupPath, item.Host}, " "),
			Tool:        item.Tool,
			Status:      item.Status,
		}
		proxies = append(proxies, proxy)
		byProxy[proxy] = item
	}

	matches := session.FilterByQuery(proxies, query)
	results := make([]*SessionSearchResult, 0, len(matches))
	for _, match := range matches {
		if item := byProxy[match]; item != nil {
			results = append(results, item)
		}
	}
	return results
}

// View renders the search overlay
func (s *Search) View() string {
	if !s.visible {
		return ""
	}

	// Header
	headerText := "🔍 Global Search (all Agent Deck sessions)"
	if s.scope == SearchScopeLocal {
		headerText = "🔍 Local Search (this machine)"
	}
	header := lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true).
		Render(headerText)

	// Build search input box
	searchBox := searchBoxStyle.Render(s.input.View())

	// Build results list
	var resultsStr strings.Builder
	maxResults := 10
	start := 0
	if s.cursor >= maxResults {
		start = s.cursor - maxResults + 1
	}
	end := min(start+maxResults, len(s.results))
	shown := s.results[start:end]

	for offset, item := range shown {
		resultIndex := start + offset
		label := item.Title + " (" + item.Tool + ")"
		if s.scope == SearchScopeGlobal && strings.TrimSpace(item.Host) != "" {
			label += "  · " + item.Host
		}
		var line string
		if resultIndex == s.cursor {
			line = selectedResultStyle.Render("› " + label)
		} else {
			line = resultItemStyle.Render("  " + label)
		}
		resultsStr.WriteString(line)
		if offset < len(shown)-1 {
			resultsStr.WriteString("\n")
		}
	}

	// Show count
	countStr := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Render("  " + formatCount(len(s.results)))

	// Show filter hint when search is empty
	hintStr := ""
	if s.input.Value() == "" {
		hintStr = lipgloss.NewStyle().
			Foreground(ColorComment).
			Italic(true).
			Render("  Tip: waiting / running / idle to filter by status")
	}

	// Keyboard shortcuts hint
	keys := "  [Enter] Open  [↑↓] Navigate  [Esc] Cancel"
	if s.scopedGroup == "" {
		nextScope := "Local"
		if s.scope == SearchScopeLocal {
			nextScope = "Global"
		}
		keys = "  [Enter] Open  [↑↓] Navigate  [Tab] " + nextScope + "  [Esc] Cancel"
	}
	keysHint := lipgloss.NewStyle().Foreground(ColorComment).Render(keys)

	// Combine everything
	var content string
	if hintStr != "" {
		content = header + "\n\n" + searchBox + "\n" + hintStr + "\n\n" + resultsStr.String() + "\n" + countStr + "\n" + keysHint
	} else {
		content = header + "\n\n" + searchBox + "\n\n" + resultsStr.String() + "\n" + countStr + "\n" + keysHint
	}

	// Wrap in overlay box - responsive width
	overlayWidth := 60
	if s.width > 0 && s.width < overlayWidth+10 {
		overlayWidth = s.width - 10
		if overlayWidth < 30 {
			overlayWidth = 30
		}
	}
	overlay := overlayStyle.Width(overlayWidth).Render(content)

	// Center in the screen
	return centerInScreen(overlay, s.width, s.height)
}

// formatCount formats the result count
func formatCount(count int) string {
	if count == 0 {
		return "No results"
	}
	if count == 1 {
		return "1 result"
	}
	return lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d", count)) + " results"
}

// centerInScreen centers content in the terminal
func centerInScreen(content string, screenWidth, screenHeight int) string {
	lines := strings.Split(content, "\n")
	contentHeight := len(lines)
	contentWidth := 0
	for _, line := range lines {
		// cellWidth (not len): the box lines carry ANSI styling, so byte length
		// over-counts and the dialog ends up shifted left of true center.
		if w := cellWidth(line); w > contentWidth {
			contentWidth = w
		}
	}

	// Calculate vertical padding
	verticalPad := (screenHeight - contentHeight) / 2
	if verticalPad < 0 {
		verticalPad = 0
	}

	// Calculate horizontal padding
	horizontalPad := (screenWidth - contentWidth) / 2
	if horizontalPad < 0 {
		horizontalPad = 0
	}

	// Add vertical padding
	var result strings.Builder
	for i := 0; i < verticalPad; i++ {
		result.WriteString("\n")
	}

	// Add horizontal padding and content
	padding := strings.Repeat(" ", horizontalPad)
	for _, line := range lines {
		result.WriteString(padding + line + "\n")
	}

	return result.String()
}
