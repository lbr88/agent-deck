package tmux

import "testing"

// TestParsePaneDeadStatus covers the "#{pane_dead}|#{pane_dead_status}" parsing
// that lets status detection tell a clean one-shot exit (code 0) from a crash.
func TestParsePaneDeadStatus(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantCode int
		wantOK   bool
	}{
		{"clean exit 0", "1|0|\n", 0, true},
		{"crash exit 1", "1|1|", 1, true},
		{"crash exit 137", "1|137|\n", 137, true},
		{"SIGTERM", "1||15\n", 143, true},
		{"SIGKILL", "1||9", 137, true},
		{"live pane", "0||", 0, false},
		{"live pane with stale status", "0|0|", 0, false},
		{"dead pane, no status or signal", "1||", 0, false},
		{"dead pane, non-numeric status", "1|foo|", 0, false},
		{"dead pane, non-numeric signal", "1||term", 0, false},
		{"malformed, no separator", "1", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, ok := parsePaneDeadStatus(tt.raw)
			if code != tt.wantCode || ok != tt.wantOK {
				t.Errorf("parsePaneDeadStatus(%q) = (%d, %v), want (%d, %v)",
					tt.raw, code, ok, tt.wantCode, tt.wantOK)
			}
		})
	}
}

func TestParsePaneDeadTerminationPreservesSignalSource(t *testing.T) {
	exitCode, signal, ok := parsePaneDeadTermination("1||15")
	if !ok || exitCode != 143 || signal != 15 {
		t.Fatalf("SIGTERM parse = (%d, %d, %v), want (143, 15, true)", exitCode, signal, ok)
	}

	exitCode, signal, ok = parsePaneDeadTermination("1|143|")
	if !ok || exitCode != 143 || signal != 0 {
		t.Fatalf("ordinary exit 143 parse = (%d, %d, %v), want (143, 0, true)", exitCode, signal, ok)
	}
}
