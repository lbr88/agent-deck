package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func TestWebMutatorSendSessionPromptRejectsNonTUIOmpBeforeDelivery(t *testing.T) {
	inst := &session.Instance{
		ID:      "web-nontui-prompt",
		Title:   "OMP print",
		Tool:    "omp",
		Command: "omp --mode=json",
	}
	inst.SetTmuxSessionForTest(tmux.NewSession("missing-web-nontui-prompt", t.TempDir()))
	h := newTestHomeWithItems(120, 30, []session.Item{{Type: session.ItemTypeSession, Session: inst}})
	h.instances = []*session.Instance{inst}
	h.instanceByID = map[string]*session.Instance{inst.ID: inst}

	err := NewWebMutator(h).SendSessionPrompt(inst.ID, "must not disappear")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "terminal prompt") {
		t.Fatalf("web prompt did not synchronously refuse non-TUI OMP before delivery: %v", err)
	}
}

func TestSendOutputToLocalTargetRejectsNonTUIOmpBeforeDelivery(t *testing.T) {
	inst := &session.Instance{
		ID:      "tui-nontui-output-target",
		Title:   "OMP RPC",
		Tool:    "omp",
		Command: "omp --mode rpc",
	}
	inst.SetTmuxSessionForTest(tmux.NewSession("missing-tui-nontui-output-target", t.TempDir()))
	h := newTestHomeWithItems(120, 30, nil)

	err := h.sendPromptToTarget(localSendOutputTarget(inst), "must not disappear")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "terminal prompt") {
		t.Fatalf("TUI output transfer did not refuse non-TUI OMP before delivery: %v", err)
	}
}
