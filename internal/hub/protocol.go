package hub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const ProtocolVersion = 1

type MessageType string

const (
	MsgHello         MessageType = "hello"
	MsgWelcome       MessageType = "welcome"
	MsgSnapshot      MessageType = "snapshot"
	MsgHeartbeat     MessageType = "heartbeat"
	MsgCommand       MessageType = "command"
	MsgCommandResult MessageType = "command_result"
	MsgAttachOpen    MessageType = "attach_open"
	MsgAttachReady   MessageType = "attach_ready"
	MsgAttachData    MessageType = "attach_data"
	MsgAttachResize  MessageType = "attach_resize"
	MsgAttachClose   MessageType = "attach_close"
	MsgAttachClosed  MessageType = "attach_closed"
	MsgError         MessageType = "error"
)

type Envelope struct {
	Version   int             `json:"version"`
	Type      MessageType     `json:"type"`
	NodeID    string          `json:"node_id,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type NodeHelloPayload struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Token    string `json:"token"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

type WelcomePayload struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
}

type SessionInfo struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Tool             string    `json:"tool"`
	Status           string    `json:"status"`
	GroupPath        string    `json:"group_path"`
	ProjectPath      string    `json:"project_path,omitempty"`
	DisplaySessionID string    `json:"display_session_id,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type SnapshotPayload struct {
	SentAt   time.Time     `json:"sent_at"`
	Sessions []SessionInfo `json:"sessions"`
}

type CommandPayload struct {
	CommandID string          `json:"command_id"`
	Action    string          `json:"action"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type CommandResultPayload struct {
	CommandID string          `json:"command_id"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

type AttachOpenPayload struct {
	StreamID  string `json:"stream_id"`
	NodeID    string `json:"node_id,omitempty"`
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}

type AttachDataPayload struct {
	StreamID string `json:"stream_id"`
	DataB64  string `json:"data_b64"`
}

func NewAttachData(streamID string, data []byte) AttachDataPayload {
	return AttachDataPayload{StreamID: streamID, DataB64: base64.StdEncoding.EncodeToString(data)}
}

func (p AttachDataPayload) Bytes() ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(p.DataB64)
	if err != nil {
		return nil, fmt.Errorf("decode attach data: %w", err)
	}
	return data, nil
}

type AttachResizePayload struct {
	StreamID string `json:"stream_id"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
}

type AttachClosePayload struct {
	StreamID string `json:"stream_id"`
	Reason   string `json:"reason,omitempty"`
}

type ErrorPayload struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

func MarshalEnvelope(typ MessageType, nodeID string, payload any) (Envelope, error) {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		raw = data
	}
	return Envelope{Version: ProtocolVersion, Type: typ, NodeID: nodeID, Payload: raw}, nil
}
