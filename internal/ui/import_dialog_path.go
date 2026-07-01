package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

const savedSessionImportDialogChrome = 10
const savedSessionImportEntryLines = 2

func importDialogPath(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func savedSessionImportVisibleRows(height int) int {
	const def = 12
	if height <= 0 {
		return def
	}
	rows := (height - savedSessionImportDialogChrome) / savedSessionImportEntryLines
	if rows < 1 {
		return 1
	}
	if rows > def {
		return def
	}
	return rows
}

func importDialogFirstMatch(indexes []int) int {
	if len(indexes) == 0 {
		return 0
	}
	return indexes[0]
}

func importDialogAppendEntry(lines []string, selected bool, title, id, updated, path string, innerWidth int, selectedStyle, normalStyle, dimStyle lipgloss.Style) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSpace(id)
	}
	meta := make([]string, 0, 2)
	if strings.TrimSpace(id) != "" {
		meta = append(meta, strings.TrimSpace(id))
	}
	if strings.TrimSpace(updated) != "" {
		meta = append(meta, strings.TrimSpace(updated))
	}
	titleLine := title
	if len(meta) > 0 {
		titleLine += "  " + dimStyle.Render(strings.Join(meta, "  "))
	}

	firstPrefix := "  "
	bodyStyle := normalStyle
	if selected {
		firstPrefix = "> "
		bodyStyle = selectedStyle
	}
	lines = append(lines, cellTruncate(firstPrefix+bodyStyle.Render(titleLine), innerWidth, "…"))

	if path = strings.TrimSpace(path); path != "" {
		pathPrefix := "  Path: "
		pathBudget := innerWidth - cellWidth(pathPrefix)
		displayPath := importDialogDisplayPath(path, pathBudget)
		lines = append(lines, cellTruncate(pathPrefix+dimStyle.Render(displayPath), innerWidth, "…"))
	}
	return lines
}

func importDialogDisplayPath(path string, width int) string {
	path = importDialogCompactHome(strings.TrimSpace(path))
	if path == "" || width <= 0 {
		return ""
	}
	if cellWidth(path) <= width {
		return path
	}

	sep := string(filepath.Separator)
	parts := strings.Split(path, sep)
	for i := len(parts) - 1; i >= 0; i-- {
		tailParts := parts[i:]
		if len(tailParts) == 0 || strings.Join(tailParts, sep) == "" {
			continue
		}
		candidate := "…" + sep + strings.Join(tailParts, sep)
		if cellWidth(candidate) <= width {
			return candidate
		}
	}
	return importDialogTailTruncate(path, width)
}

func importDialogCompactHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	home = strings.TrimRight(home, string(filepath.Separator))
	if path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(path, prefix)
	}
	return path
}

func importDialogTailTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if cellWidth(s) <= width {
		return s
	}
	tail := "…"
	if cellWidth(tail) >= width {
		return cellTruncate(s, width, "")
	}
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0; i-- {
		candidate := tail + string(runes[i:])
		if cellWidth(candidate) > width {
			continue
		}
		return candidate
	}
	return tail
}

func importDialogInnerWidth(dialogWidth int) int {
	innerWidth := dialogWidth - 4
	if innerWidth < 1 {
		return 1
	}
	return innerWidth
}

func importDialogSearchIndexes(total int, query string, fields func(int) []string) []int {
	indexes := make([]int, 0, total)
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		for i := 0; i < total; i++ {
			indexes = append(indexes, i)
		}
		return indexes
	}

	type scoredIndex struct {
		index int
		score int
	}
	scored := make([]scoredIndex, 0, total)
	for i := 0; i < total; i++ {
		score, ok := importDialogSearchScore(query, fields(i))
		if ok {
			scored = append(scored, scoredIndex{index: i, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].index < scored[j].index
	})
	for _, match := range scored {
		indexes = append(indexes, match.index)
	}
	return indexes
}

func importDialogSearchScore(query string, values []string) (int, bool) {
	best := 0
	matched := false
	for i, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		bonus := 4000 - i*500
		if bonus < 0 {
			bonus = 0
		}
		if idx := strings.Index(value, query); idx >= 0 {
			score := 100000 + bonus - idx
			if !matched || score > best {
				best = score
				matched = true
			}
			continue
		}
		matches := fuzzy.Find(query, []string{value})
		if len(matches) == 0 {
			continue
		}
		score := matches[0].Score + bonus
		if !matched || score > best {
			best = score
			matched = true
		}
	}
	return best, matched
}

func importDialogNormalizeCursor(cursor int, indexes []int) int {
	if len(indexes) == 0 {
		return 0
	}
	for _, idx := range indexes {
		if idx == cursor {
			return cursor
		}
	}
	return indexes[0]
}

func importDialogMoveCursor(cursor int, indexes []int, delta int) int {
	if len(indexes) == 0 {
		return 0
	}
	pos := importDialogCursorPosition(cursor, indexes)
	if pos < 0 {
		return indexes[0]
	}
	pos = (pos + delta + len(indexes)) % len(indexes)
	return indexes[pos]
}

func importDialogCursorPosition(cursor int, indexes []int) int {
	for i, idx := range indexes {
		if idx == cursor {
			return i
		}
	}
	return -1
}

func importDialogHandleSearchKey(key tea.KeyMsg, active *bool, query *string) (bool, bool) {
	if !*active {
		return false, false
	}

	switch key.String() {
	case "enter", "up", "down":
		return false, false
	case "esc":
		*active = false
		*query = ""
		return true, true
	case "backspace", "ctrl+h":
		runes := []rune(*query)
		if len(runes) > 0 {
			*query = string(runes[:len(runes)-1])
		}
		return true, true
	case "ctrl+u":
		*query = ""
		return true, true
	}

	if key.Type == tea.KeyRunes {
		*query += string(key.Runes)
		return true, true
	}
	return true, false
}

func importDialogFooter(searchActive bool, query string) string {
	if searchActive || strings.TrimSpace(query) != "" {
		return "Search: " + query
	}
	return "Enter import | / search | Esc cancel | j/k navigate"
}
