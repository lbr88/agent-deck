package hub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEnvelopeRoundTripSessionSnapshot(t *testing.T) {
	in := Envelope{
		Version: ProtocolVersion,
		Type:    MsgSnapshot,
		NodeID:  "node_123",
		Payload: mustRaw(t, SnapshotPayload{
			SentAt: time.Unix(123, 0).UTC(),
			Sessions: []SessionInfo{{
				ID:        "sess_1",
				Title:     "api-fix",
				Tool:      "claude",
				Status:    "waiting",
				GroupPath: "default",
			}},
		}),
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out Envelope
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Version != ProtocolVersion || out.Type != MsgSnapshot || out.NodeID != "node_123" {
		t.Fatalf("round trip = %+v", out)
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(out.Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].Title != "api-fix" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAttachDataIsBase64TerminalBytes(t *testing.T) {
	frame := NewAttachData("str_1", []byte{0x1b, '[', 'A'})
	data, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if string(data) != "\x1b[A" {
		t.Fatalf("decoded = %q", string(data))
	}
}

func TestMarshalEnvelopeEmbedsPayload(t *testing.T) {
	in := SnapshotPayload{
		SentAt: time.Unix(456, 0).UTC(),
		Sessions: []SessionInfo{{
			ID:        "sess_2",
			Title:     "hub-work",
			Tool:      "codex",
			Status:    "running",
			GroupPath: "default",
		}},
	}
	env, err := MarshalEnvelope(MsgSnapshot, "node_456", in)
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	if env.Version != ProtocolVersion || env.Type != MsgSnapshot || env.NodeID != "node_456" {
		t.Fatalf("envelope = %+v", env)
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "sess_2" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSessionInfoWithoutUpdatedAtOmitsField(t *testing.T) {
	data, err := json.Marshal(SessionInfo{
		ID:        "sess_3",
		Title:     "api-fix",
		Tool:      "claude",
		Status:    "waiting",
		GroupPath: "default",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "updated_at") {
		t.Fatalf("json includes updated_at: %s", data)
	}
}

func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}
