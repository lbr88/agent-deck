package ui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

const savedSessionImportDialogChrome = 10

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
	rows := height - savedSessionImportDialogChrome
	if rows < 1 {
		return 1
	}
	if rows > def {
		return def
	}
	return rows
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
