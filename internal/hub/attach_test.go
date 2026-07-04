package hub

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAttachRouterForwardsInputToOwnerAndOutputToRequester(t *testing.T) {
	router := NewAttachRouter()
	requester := newFakePeer("laptop")
	owner := newFakePeer("workstation")
	router.Register(requester)
	router.Register(owner)

	if err := router.Open(context.Background(), requester.ID, owner.ID, AttachOpenPayload{
		StreamID:  "stream_1",
		NodeID:    owner.ID,
		SessionID: "sess_1",
		Cols:      120,
		Rows:      40,
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	open := owner.pop(t)
	if open.Type != MsgAttachOpen {
		t.Fatalf("owner first msg = %s, want %s", open.Type, MsgAttachOpen)
	}
	if open.NodeID != requester.ID {
		t.Fatalf("open NodeID = %q, want requester %q", open.NodeID, requester.ID)
	}
	var openPayload AttachOpenPayload
	if err := json.Unmarshal(open.Payload, &openPayload); err != nil {
		t.Fatalf("decode open payload: %v", err)
	}
	if openPayload.StreamID != "stream_1" || openPayload.SessionID != "sess_1" || openPayload.Cols != 120 || openPayload.Rows != 40 {
		t.Fatalf("open payload = %+v", openPayload)
	}

	if err := router.ForwardFromRequester(requester.ID, NewAttachData("stream_1", []byte("hello"))); err != nil {
		t.Fatalf("ForwardFromRequester: %v", err)
	}
	input := owner.pop(t)
	if input.Type != MsgAttachData {
		t.Fatalf("owner second msg = %s, want %s", input.Type, MsgAttachData)
	}
	assertAttachDataBytes(t, input, "hello")

	if err := router.ForwardFromOwner(owner.ID, NewAttachData("stream_1", []byte("world"))); err != nil {
		t.Fatalf("ForwardFromOwner: %v", err)
	}
	output := requester.pop(t)
	if output.Type != MsgAttachData {
		t.Fatalf("requester msg = %s, want %s", output.Type, MsgAttachData)
	}
	assertAttachDataBytes(t, output, "world")
}

func TestAttachRouterRoutesResizeAndCloseFrames(t *testing.T) {
	router := NewAttachRouter()
	requester := newFakePeer("laptop")
	owner := newFakePeer("workstation")
	router.Register(requester)
	router.Register(owner)

	if err := router.Open(context.Background(), requester.ID, owner.ID, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = owner.pop(t)

	if err := router.ForwardResizeFromRequester(requester.ID, AttachResizePayload{StreamID: "stream_1", Cols: 100, Rows: 30}); err != nil {
		t.Fatalf("ForwardResizeFromRequester: %v", err)
	}
	resize := owner.pop(t)
	if resize.Type != MsgAttachResize {
		t.Fatalf("resize msg = %s, want %s", resize.Type, MsgAttachResize)
	}

	if err := router.ForwardCloseFromRequester(requester.ID, AttachClosePayload{StreamID: "stream_1", Reason: "detached"}); err != nil {
		t.Fatalf("ForwardCloseFromRequester: %v", err)
	}
	closed := owner.pop(t)
	if closed.Type != MsgAttachClose {
		t.Fatalf("close msg = %s, want %s", closed.Type, MsgAttachClose)
	}

	if err := router.ForwardFromOwner(owner.ID, NewAttachData("stream_1", []byte("after-close"))); err == nil {
		t.Fatal("ForwardFromOwner after close succeeded, want missing stream error")
	}
}

func TestAttachRouterUnregisterCleansStreamsForDisconnectedPeer(t *testing.T) {
	router := NewAttachRouter()
	requester := newFakePeer("laptop")
	owner := newFakePeer("workstation")
	router.Register(requester)
	router.Register(owner)

	if err := router.Open(context.Background(), requester.ID, owner.ID, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = owner.pop(t)

	router.Unregister(owner.ID)

	if err := router.ForwardFromRequester(requester.ID, NewAttachData("stream_1", []byte("after-disconnect"))); err == nil {
		t.Fatal("ForwardFromRequester after owner unregister succeeded, want missing stream error")
	}
}

type fakePeer struct {
	ID       string
	messages []Envelope
}

func newFakePeer(id string) *fakePeer {
	return &fakePeer{ID: id}
}

func (p *fakePeer) NodeID() string {
	return p.ID
}

func (p *fakePeer) Send(env Envelope) error {
	p.messages = append(p.messages, env)
	return nil
}

func (p *fakePeer) pop(t *testing.T) Envelope {
	t.Helper()
	if len(p.messages) == 0 {
		t.Fatal("no messages")
	}
	env := p.messages[0]
	p.messages = p.messages[1:]
	return env
}

func assertAttachDataBytes(t *testing.T, env Envelope, want string) {
	t.Helper()
	var payload AttachDataPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("decode attach data payload: %v", err)
	}
	got, err := payload.Bytes()
	if err != nil {
		t.Fatalf("decode attach data bytes: %v", err)
	}
	if string(got) != want {
		t.Fatalf("attach data bytes = %q, want %q", got, want)
	}
}
