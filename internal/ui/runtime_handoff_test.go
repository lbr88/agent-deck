package ui

import "testing"

func TestRuntimeHandoffMsgSchedulesFullShutdown(t *testing.T) {
	h := &Home{}
	model, cmd := h.updateInner(RuntimeHandoffMsg{})
	if model != h {
		t.Fatal("runtime handoff replaced the Home model")
	}
	if !h.isQuitting {
		t.Fatal("runtime handoff did not mark the TUI as quitting")
	}
	if cmd == nil {
		t.Fatal("runtime handoff did not schedule final shutdown")
	}
	if !h.RuntimeHandoffAccepted() {
		t.Fatal("runtime handoff was not recorded as the shutdown cause")
	}
	if duplicate := h.performFinalShutdown(false); duplicate != nil {
		t.Fatal("final shutdown was scheduled more than once")
	}
}

func TestRuntimeHandoffMsgRejectsWhenUserQuitAlreadyStarted(t *testing.T) {
	h := &Home{isQuitting: true}
	rejected := false
	_, cmd := h.updateInner(RuntimeHandoffMsg{OnRejected: func() { rejected = true }})
	if cmd != nil {
		t.Fatal("runtime handoff scheduled shutdown after user quit")
	}
	if !rejected {
		t.Fatal("runtime handoff did not report rejection")
	}
	if h.RuntimeHandoffAccepted() {
		t.Fatal("runtime handoff was accepted after user quit")
	}
}
