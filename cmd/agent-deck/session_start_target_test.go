package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func TestPrepareSessionStartTargetReplacesRetainedPaneUnderExactIdentity(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}

	pane := tmux.NewSession("cli-start-retained-pane", t.TempDir())
	pane.LaunchAs = "direct"
	pane.RunCommandAsInitialProcess = true
	pane.OptionOverrides = map[string]string{"remain-on-exit": "on"}
	t.Cleanup(func() { _ = pane.Kill() })
	if err := pane.Start("exit 23"); err != nil {
		t.Fatalf("start retained failure fixture: %v", err)
	}
	waitForRetainedDeadPane(t, pane)

	stableName := pane.Name
	inst := session.NewInstanceWithTool("cli retained restart", t.TempDir(), "test-sleeper")
	inst.ID = "cli-retained-restart"
	inst.Command = "sleep 30"
	inst.SetTmuxSessionForTest(pane)

	if err := prepareSessionStartTarget(inst); err != nil {
		t.Fatalf("prepare retained target: %v", err)
	}
	if !pane.PersistedIdentityReusePending() {
		t.Fatal("retained pane was not marked for exact persisted-identity reuse")
	}
	if err := inst.Start(); err != nil {
		t.Fatalf("restart retained target: %v", err)
	}
	if pane.Name != stableName {
		t.Fatalf("tmux identity drifted from %q to %q", stableName, pane.Name)
	}
	alive, err := pane.PrimaryPaneAliveFresh()
	if err != nil || !alive {
		t.Fatalf("exact replacement is not live: alive=%v err=%v", alive, err)
	}
}

func TestPrepareSessionStartTargetRejectsLivePane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}

	pane := tmux.NewSession("cli-start-live-pane", t.TempDir())
	pane.LaunchAs = "direct"
	pane.RunCommandAsInitialProcess = true
	t.Cleanup(func() { _ = pane.Kill() })
	if err := pane.Start("sleep 30"); err != nil {
		t.Fatalf("start live fixture: %v", err)
	}
	inst := session.NewInstanceWithTool("cli live session", t.TempDir(), "test-sleeper")
	inst.SetTmuxSessionForTest(pane)

	err := prepareSessionStartTarget(inst)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("live target error = %v, want already running", err)
	}
	if pane.PersistedIdentityReusePending() {
		t.Fatal("live pane must not be marked for replacement")
	}
}

func waitForRetainedDeadPane(t *testing.T, pane *tmux.Session) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive, err := pane.PrimaryPaneAliveFresh()
		if err == nil && !alive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pane did not become retained and dead")
}
