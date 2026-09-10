package tmux

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestInitialProcessRetainsImmediateFailureOutput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	s := NewSession("immediate-exit", t.TempDir())
	s.RunCommandAsInitialProcess = true
	s.OptionOverrides = map[string]string{"remain-on-exit": "on"}
	t.Cleanup(func() { _ = s.Kill() })
	if err := s.Start("printf 'immediate-launch-error\\n'; exit 23"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !s.IsPaneDead() {
		time.Sleep(10 * time.Millisecond)
	}
	if !s.IsPaneDead() {
		t.Fatal("immediate failure disappeared before remain-on-exit was applied")
	}
	out, err := s.CaptureFullHistory()
	if err != nil || !strings.Contains(out, "immediate-launch-error") {
		t.Fatalf("launch error lost: %v %q", err, out)
	}
	if code, ok := s.PaneDeadExitStatus(); !ok || code != 23 {
		t.Fatalf("lost exit status: %d %v", code, ok)
	}
}

func TestStartCommandSpec_RemainOnExitExecsOriginalCommand(t *testing.T) {
	s := NewSession("immediate-exit", t.TempDir())
	s.RunCommandAsInitialProcess = true
	s.OptionOverrides = map[string]string{"remain-on-exit": "on"}

	const command = "printf 'immediate-launch-error\\n'; exit 23"
	_, args := s.startCommandSpec(s.WorkDir, command)
	want := []string{
		bashBinary,
		"-c",
		`tmux -S "${TMUX%%,*}" set-option -p -t "$TMUX_PANE" remain-on-exit on || exit 1; exec "$@"`,
		"agent-deck-pane",
		bashBinary,
		"-c",
		command,
	}
	if len(args) < len(want) {
		t.Fatalf("initial-process argv too short: %q", args)
	}
	got := args[len(args)-len(want):]
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("initial-process argv[%d] = %q, want %q; argv tail: %q", i, got[i], want[i], got)
		}
	}
}
