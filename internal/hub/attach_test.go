package hub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestHubTmuxAttachCommandForcesSaneTERM(t *testing.T) {
	t.Setenv("TERM", "dumb")

	cmd := hubTmuxAttachCommand(context.Background(), "agentdeck", "agentdeck_session")
	wantArgs := []string{"tmux", "-u", "-L", "agentdeck", "attach-session", "-t", "agentdeck_session"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("attach argv = %v, want %v", cmd.Args, wantArgs)
	}
	n, term := countTERMEntries(cmd.Env)
	if n != 1 || term != "TERM=xterm-256color" {
		t.Fatalf("TERM entries = %d last=%q env=%v", n, term, cmd.Env)
	}
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

	closed := requester.pop(t)
	if closed.Type != MsgAttachClosed {
		t.Fatalf("requester msg = %s, want %s", closed.Type, MsgAttachClosed)
	}
	var closePayload AttachClosePayload
	if err := json.Unmarshal(closed.Payload, &closePayload); err != nil {
		t.Fatalf("decode closed payload: %v", err)
	}
	if closePayload.StreamID != "stream_1" || !strings.Contains(closePayload.Reason, "owner disconnected") {
		t.Fatalf("closed payload = %+v", closePayload)
	}

	if err := router.ForwardFromRequester(requester.ID, NewAttachData("stream_1", []byte("after-disconnect"))); err == nil {
		t.Fatal("ForwardFromRequester after owner unregister succeeded, want missing stream error")
	}
}

func TestAttachRouterUnregisterRequesterNotifiesOwner(t *testing.T) {
	router := NewAttachRouter()
	requester := newFakePeer("laptop")
	owner := newFakePeer("workstation")
	router.Register(requester)
	router.Register(owner)

	if err := router.Open(context.Background(), requester.ID, owner.ID, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = owner.pop(t)

	router.Unregister(requester.ID)

	closed := owner.pop(t)
	if closed.Type != MsgAttachClose {
		t.Fatalf("owner msg = %s, want %s", closed.Type, MsgAttachClose)
	}
	var closePayload AttachClosePayload
	if err := json.Unmarshal(closed.Payload, &closePayload); err != nil {
		t.Fatalf("decode close payload: %v", err)
	}
	if closePayload.StreamID != "stream_1" || !strings.Contains(closePayload.Reason, "requester disconnected") {
		t.Fatalf("close payload = %+v", closePayload)
	}

	if err := router.ForwardFromOwner(owner.ID, NewAttachData("stream_1", []byte("after-disconnect"))); err == nil {
		t.Fatal("ForwardFromOwner after requester unregister succeeded, want missing stream error")
	}
}

func TestAttachRouterOpenNotifiesRequesterWhenOwnerUnavailable(t *testing.T) {
	router := NewAttachRouter()
	requester := newFakePeer("laptop")
	router.Register(requester)

	err := router.Open(context.Background(), requester.ID, "workstation", AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"})
	if err == nil {
		t.Fatal("Open succeeded, want owner unavailable error")
	}

	closed := requester.pop(t)
	if closed.Type != MsgAttachClosed {
		t.Fatalf("requester msg = %s, want %s", closed.Type, MsgAttachClosed)
	}
	var closePayload AttachClosePayload
	if err := json.Unmarshal(closed.Payload, &closePayload); err != nil {
		t.Fatalf("decode closed payload: %v", err)
	}
	if closePayload.StreamID != "stream_1" || !strings.Contains(closePayload.Reason, "not connected") {
		t.Fatalf("closed payload = %+v", closePayload)
	}
}

func TestAttachRouterOpenFailureNotifiesOpeningRequesterPeer(t *testing.T) {
	router := NewAttachRouter()
	requesterA := newFakePeerConn("laptop", "laptop-a")
	requesterB := newFakePeerConn("laptop", "laptop-b")
	router.Register(requesterA)
	router.Register(requesterB)

	err := router.OpenFromPeer(context.Background(), requesterA, "workstation", AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"})
	if err == nil {
		t.Fatal("OpenFromPeer succeeded, want owner unavailable error")
	}

	closed := requesterA.pop(t)
	if closed.Type != MsgAttachClosed {
		t.Fatalf("requester A msg = %s, want %s", closed.Type, MsgAttachClosed)
	}
	if len(requesterB.messages) != 0 {
		t.Fatalf("requester B received %d messages, want none", len(requesterB.messages))
	}
}

func TestAttachRouterOwnerSendFailureNotifiesOpeningRequesterPeer(t *testing.T) {
	router := NewAttachRouter()
	requesterA := newFakePeerConn("laptop", "laptop-a")
	requesterB := newFakePeerConn("laptop", "laptop-b")
	owner := newFakePeer("workstation")
	owner.sendErr = errors.New("write failed")
	router.Register(requesterA)
	router.Register(owner)
	router.Register(requesterB)

	err := router.OpenFromPeer(context.Background(), requesterA, owner.ID, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"})
	if err == nil {
		t.Fatal("OpenFromPeer succeeded, want owner send error")
	}

	closed := requesterA.pop(t)
	if closed.Type != MsgAttachClosed {
		t.Fatalf("requester A msg = %s, want %s", closed.Type, MsgAttachClosed)
	}
	if len(requesterB.messages) != 0 {
		t.Fatalf("requester B received %d messages, want none", len(requesterB.messages))
	}
}

func TestAttachRouterRejectsDuplicateStreamID(t *testing.T) {
	router := NewAttachRouter()
	requesterA := newFakePeerConn("laptop", "laptop-a")
	requesterB := newFakePeerConn("laptop", "laptop-b")
	owner := newFakePeer("workstation")
	router.Register(requesterA)
	router.Register(requesterB)
	router.Register(owner)

	if err := router.OpenFromPeer(context.Background(), requesterA, owner.ID, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"}); err != nil {
		t.Fatalf("first OpenFromPeer: %v", err)
	}
	_ = owner.pop(t)

	err := router.OpenFromPeer(context.Background(), requesterB, owner.ID, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_2"})
	if err == nil {
		t.Fatal("second OpenFromPeer succeeded, want duplicate stream error")
	}
	closed := requesterB.pop(t)
	if closed.Type != MsgAttachClosed {
		t.Fatalf("requester B msg = %s, want %s", closed.Type, MsgAttachClosed)
	}
	var closePayload AttachClosePayload
	if err := json.Unmarshal(closed.Payload, &closePayload); err != nil {
		t.Fatalf("decode closed payload: %v", err)
	}
	if closePayload.StreamID != "stream_1" || !strings.Contains(closePayload.Reason, "already exists") {
		t.Fatalf("closed payload = %+v", closePayload)
	}
	if len(owner.messages) != 0 {
		t.Fatalf("owner received duplicate open, messages=%d", len(owner.messages))
	}

	if err := router.ForwardFromOwner(owner.ID, NewAttachData("stream_1", []byte("first-stream"))); err != nil {
		t.Fatalf("ForwardFromOwner after duplicate reject: %v", err)
	}
	output := requesterA.pop(t)
	if output.Type != MsgAttachData {
		t.Fatalf("requester A output msg = %s, want %s", output.Type, MsgAttachData)
	}
	assertAttachDataBytes(t, output, "first-stream")
}

func TestAttachRouterPinsStreamToOpeningRequesterConnection(t *testing.T) {
	router := NewAttachRouter()
	requesterA := newFakePeerConn("laptop", "laptop-a")
	requesterB := newFakePeerConn("laptop", "laptop-b")
	owner := newFakePeer("workstation")
	router.Register(requesterA)
	router.Register(owner)
	router.Register(requesterB)

	if err := router.OpenFromPeer(context.Background(), requesterA, owner.ID, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"}); err != nil {
		t.Fatalf("OpenFromPeer: %v", err)
	}
	_ = owner.pop(t)

	if err := router.ForwardReadyFromOwnerPeer(owner, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"}); err != nil {
		t.Fatalf("ForwardReadyFromOwnerPeer: %v", err)
	}
	ready := requesterA.pop(t)
	if ready.Type != MsgAttachReady {
		t.Fatalf("requester A msg = %s, want %s", ready.Type, MsgAttachReady)
	}
	if len(requesterB.messages) != 0 {
		t.Fatalf("requester B received %d messages, want none", len(requesterB.messages))
	}

	router.UnregisterPeer(requesterB)

	if err := router.ForwardFromOwner(owner.ID, NewAttachData("stream_1", []byte("still-open"))); err != nil {
		t.Fatalf("ForwardFromOwner after other peer unregister: %v", err)
	}
	output := requesterA.pop(t)
	if output.Type != MsgAttachData {
		t.Fatalf("requester A output msg = %s, want %s", output.Type, MsgAttachData)
	}
	assertAttachDataBytes(t, output, "still-open")
}

func TestAttachRouterUnregisterNodeNotifiesPinnedSurvivingPeer(t *testing.T) {
	router := NewAttachRouter()
	requesterA := newFakePeerConn("laptop", "laptop-a")
	requesterB := newFakePeerConn("laptop", "laptop-b")
	owner := newFakePeer("workstation")
	router.Register(requesterA)
	router.Register(owner)
	router.Register(requesterB)

	if err := router.OpenFromPeer(context.Background(), requesterA, owner.ID, AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1"}); err != nil {
		t.Fatalf("OpenFromPeer: %v", err)
	}
	_ = owner.pop(t)

	router.Unregister(owner.ID)

	closed := requesterA.pop(t)
	if closed.Type != MsgAttachClosed {
		t.Fatalf("requester A msg = %s, want %s", closed.Type, MsgAttachClosed)
	}
	if len(requesterB.messages) != 0 {
		t.Fatalf("requester B received %d messages, want none", len(requesterB.messages))
	}
}

func TestTmuxAttachTokenRejectsTampering(t *testing.T) {
	token, err := newTmuxAttachToken("agent-deck", "ad-term-session", time.Minute)
	if err != nil {
		t.Fatalf("newTmuxAttachToken: %v", err)
	}
	target, ok, err := parseTmuxAttachToken(token)
	if err != nil {
		t.Fatalf("parseTmuxAttachToken valid token: %v", err)
	}
	if !ok {
		t.Fatal("parseTmuxAttachToken valid token did not report token")
	}
	if target.SocketName != "agent-deck" || target.SessionName != "ad-term-session" {
		t.Fatalf("attach target = %+v", target)
	}

	_, ok, err = parseTmuxAttachToken(token + "x")
	if !ok {
		t.Fatal("tampered token was not recognized as attach token")
	}
	if err == nil {
		t.Fatal("tampered attach token parsed without error")
	}
}

type fakePeer struct {
	ID       string
	peerID   string
	messages []Envelope
	sendErr  error
}

func newFakePeer(id string) *fakePeer {
	return newFakePeerConn(id, id)
}

func newFakePeerConn(nodeID, peerID string) *fakePeer {
	return &fakePeer{ID: nodeID, peerID: peerID}
}

func (p *fakePeer) NodeID() string {
	return p.ID
}

func (p *fakePeer) PeerID() string {
	return p.peerID
}

func (p *fakePeer) Send(env Envelope) error {
	if p.sendErr != nil {
		return p.sendErr
	}
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

func countTERMEntries(env []string) (n int, last string) {
	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			n++
			last = kv
		}
	}
	return n, last
}
