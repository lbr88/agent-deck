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
