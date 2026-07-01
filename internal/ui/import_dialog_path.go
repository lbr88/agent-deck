package ui

import "strings"

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
