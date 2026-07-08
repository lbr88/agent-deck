package hub

import (
	"bytes"
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
			SentAt:       time.Unix(123, 0).UTC(),
			WebAvailable: true,
			Sessions: []SessionInfo{{
				ID:          "sess_1",
				Title:       "api-fix",
				Tool:        "claude",
				Status:      "waiting",
				Substate:    "auth-401",
				GroupPath:   "default",
				IsConductor: true,
				Windows: []WindowInfo{{
					Index:    0,
					Name:     "main",
					Activity: 123,
					Tool:     "claude",
				}, {
					Index: 1,
					Name:  "logs",
				}},
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
	if !payload.WebAvailable {
		t.Fatal("payload WebAvailable = false, want true")
	}
	if payload.Sessions[0].Substate != "auth-401" {
		t.Fatalf("payload substate = %q, want auth-401", payload.Sessions[0].Substate)
	}
	if !payload.Sessions[0].IsConductor {
		t.Fatal("payload is_conductor = false, want true")
	}
	if len(payload.Sessions[0].Windows) != 2 || payload.Sessions[0].Windows[0].Name != "main" || payload.Sessions[0].Windows[0].Tool != "claude" {
		t.Fatalf("payload windows = %+v", payload.Sessions[0].Windows)
	}
}

func TestAttachOpenPayloadRoundTripsWindowIndex(t *testing.T) {
	windowIndex := 0
	env, err := MarshalEnvelope(MsgAttachOpen, "requester", AttachOpenPayload{
		StreamID:    "stream_1",
		NodeID:      "owner",
		SessionID:   "sess_1",
		WindowIndex: &windowIndex,
		Cols:        100,
		Rows:        30,
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	var payload AttachOpenPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload.WindowIndex == nil || *payload.WindowIndex != 0 {
		t.Fatalf("window index = %+v, want 0", payload.WindowIndex)
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

func TestAttachDataAcceptsLargeTerminalImagePayload(t *testing.T) {
	frame := NewAttachData("str_1", bytes.Repeat([]byte("x"), 1024*1024))
	data, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes rejected terminal image-sized payload: %v", err)
	}
	if len(data) != 1024*1024 {
		t.Fatalf("decoded size = %d, want 1 MiB", len(data))
	}
}

func TestAttachDataRejectsOversizedPayload(t *testing.T) {
	frame := NewAttachData("str_1", bytes.Repeat([]byte("x"), MaxAttachFrameBytes+1))
	if _, err := frame.Bytes(); err == nil {
		t.Fatal("Bytes succeeded for oversized payload, want error")
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
