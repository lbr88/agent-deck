package openclaw

import "testing"

func TestBridgeShutdownMsgCancelsModelAndQuits(t *testing.T) {
	model := NewBridgeModel("ws://127.0.0.1:1", "", "agent", "Agent")
	got, cmd := model.Update(bridgeShutdownMsg{})
	if got != model {
		t.Fatal("shutdown replaced bridge model")
	}
	if cmd == nil {
		t.Fatal("shutdown did not return Bubble Tea quit command")
	}
	select {
	case <-model.ctx.Done():
	default:
		t.Fatal("shutdown did not cancel bridge context")
	}
}
