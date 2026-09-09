package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func TestOmpResumeFailureDoesNotPutEntireHistoryIntoTUIError(t *testing.T) {
	inst := session.NewInstanceWithTool("omp-long-failed-resume", t.TempDir(), "omp")
	ts := inst.GetTmuxSession()
	ts.RunCommandAsInitialProcess = true
	ts.OptionOverrides = map[string]string{"remain-on-exit": "on"}
	t.Cleanup(func() { _ = ts.Kill() })
	exitGate := filepath.Join(t.TempDir(), "allow-exit")
	if err := ts.Start("printf 'old-history-should-not-fill-the-error-footer\\n%.0s' {1..1000}; printf '\\033[31mactual-failure-at-end\\033[0m\\n'; while [ ! -f " + shellescape.Quote(exitGate) + " ]; do sleep 0.01; done; exit 23"); err != nil {
		t.Fatal(err)
	}
	// Establish the fixture's scrollback before exiting. A process writing a
	// large burst and exiting immediately can close its PTY before tmux drains
	// all bytes; that does not test the bounded TUI error renderer below.
	deadline := time.Now().Add(3 * time.Second)
	for {
		output, err := ts.CaptureFullHistory()
		if err == nil && strings.Contains(output, "actual-failure-at-end") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture history did not render before exit: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(exitGate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for !ts.IsPaneDead() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !ts.IsPaneDead() {
		t.Fatal("fixture did not exit")
	}
	// The status refresh can still describe a live pane after it has exited.
	// Enter/readiness must not accept its old rendered history as a live agent.
	tmux.SeedPaneInfoCacheForTest(t, map[string]tmux.PaneInfo{ts.Name: {Dead: false}})
	_, err := captureResumePane(inst)
	if !errors.Is(err, errResumeProcessExited) || !strings.Contains(err.Error(), "actual-failure-at-end") {
		t.Fatalf("missing actual failure: %v", err)
	}
	if len(err.Error()) > 8300 || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("unbounded or terminal-control-bearing error: %d bytes", len(err.Error()))
	}
}
