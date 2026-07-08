package main

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func TestAcknowledgeSessionForCLIAttachMarksWaitingSessionSeen(t *testing.T) {
	inst := session.NewInstanceWithTool("remote-waiting", t.TempDir(), "claude")
	inst.ID = "cli-attach-waiting"
	inst.SetStatusThreadSafe(session.StatusWaiting)
	tmuxSess := tmux.ReconnectSessionLazy("cli-attach-waiting-tmux", "remote-waiting", inst.ProjectPath, "claude", "waiting")
	inst.SetTmuxSessionForTest(tmuxSess)

	if tmuxSess.IsAcknowledged() {
		t.Fatal("precondition: waiting tmux session should start unacknowledged")
	}
	if !acknowledgeSessionForCLIAttach(inst, nil) {
		t.Fatal("waiting session was not acknowledged for CLI attach")
	}
	if !tmuxSess.IsAcknowledged() {
		t.Fatal("tmux session was not marked acknowledged")
	}
}

func TestAcknowledgeSessionForCLIAttachIgnoresNonWaitingSession(t *testing.T) {
	inst := session.NewInstanceWithTool("remote-running", t.TempDir(), "claude")
	inst.ID = "cli-attach-running"
	inst.SetStatusThreadSafe(session.StatusRunning)
	tmuxSess := tmux.ReconnectSessionLazy("cli-attach-running-tmux", "remote-running", inst.ProjectPath, "claude", "running")
	inst.SetTmuxSessionForTest(tmuxSess)

	if acknowledgeSessionForCLIAttach(inst, nil) {
		t.Fatal("running session should not be acknowledged by CLI attach helper")
	}
	if tmuxSess.IsAcknowledged() {
		t.Fatal("running tmux session should not be force-acknowledged")
	}
}
