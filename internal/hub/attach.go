package hub

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
	"github.com/creack/pty"
	"golang.org/x/term"
)

type TerminalSize struct {
	Cols int
	Rows int
}

type AttachStream interface {
	io.ReadWriteCloser
	Resize(TerminalSize) error
}

type AttachBackend interface {
	Open(ctx context.Context, sessionID string, size TerminalSize) (AttachStream, error)
}

type AttachWindowBackend interface {
	OpenWindow(ctx context.Context, sessionID string, windowIndex int, size TerminalSize) (AttachStream, error)
}

type TmuxAttachBackend struct {
	Profile string
}

type tmuxAttachTarget struct {
	SocketName  string `json:"socket,omitempty"`
	SessionName string `json:"session"`
	ExpiresAt   int64  `json:"exp"`
}

const tmuxAttachTokenPrefix = "tmuxattach:" // #nosec G101 -- protocol prefix, not a credential.

var (
	tmuxAttachTokenSecretOnce sync.Once
	tmuxAttachTokenSecret     []byte
	tmuxAttachTokenSecretErr  error
)

func NewTmuxAttachBackend(profile string) TmuxAttachBackend {
	return TmuxAttachBackend{Profile: profile}
}

func (b TmuxAttachBackend) Open(ctx context.Context, sessionID string, size TerminalSize) (AttachStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return b.open(ctx, sessionID, nil, size)
}

func (b TmuxAttachBackend) OpenWindow(ctx context.Context, sessionID string, windowIndex int, size TerminalSize) (AttachStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if windowIndex < 0 {
		return nil, fmt.Errorf("tmux window index must be non-negative")
	}
	return b.open(ctx, sessionID, &windowIndex, size)
}

func (b TmuxAttachBackend) open(ctx context.Context, sessionID string, windowIndex *int, size TerminalSize) (AttachStream, error) {
	if target, ok, err := parseTmuxAttachToken(sessionID); ok || err != nil {
		if err != nil {
			return nil, err
		}
		if windowIndex != nil {
			return nil, fmt.Errorf("tmux attach token does not support window selection")
		}
		return openTmuxAttachTarget(ctx, target, size)
	}
	row, err := b.findSession(sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.TmuxSession) == "" {
		return nil, fmt.Errorf("session %q has no tmux session", sessionID)
	}
	target := tmuxAttachTarget{SocketName: row.TmuxSocketName, SessionName: row.TmuxSession}
	if windowIndex != nil {
		if err := selectTmuxAttachWindow(ctx, target, *windowIndex); err != nil {
			return nil, err
		}
	}
	return openTmuxAttachTarget(ctx, target, size)
}

func selectTmuxAttachWindow(ctx context.Context, target tmuxAttachTarget, windowIndex int) error {
	sessionName := strings.TrimSpace(target.SessionName)
	if sessionName == "" {
		return fmt.Errorf("tmux attach target session is required")
	}
	if windowIndex < 0 {
		return fmt.Errorf("tmux window index must be non-negative")
	}
	tmuxTarget := fmt.Sprintf("%s:%d", sessionName, windowIndex)
	if err := tmux.ExecContext(ctx, target.SocketName, "select-window", "-t", tmuxTarget).Run(); err != nil {
		return fmt.Errorf("select tmux window %s: %w", tmuxTarget, err)
	}
	return nil
}

func openTmuxAttachTarget(ctx context.Context, target tmuxAttachTarget, size TerminalSize) (AttachStream, error) {
	sessionName := strings.TrimSpace(target.SessionName)
	if sessionName == "" {
		return nil, fmt.Errorf("tmux attach target session is required")
	}
	if err := tmux.ExecContext(ctx, target.SocketName, "has-session", "-t", sessionName).Run(); err != nil {
		return nil, fmt.Errorf("tmux session %q is not available: %w", sessionName, err)
	}
	attachCtx, cancel := context.WithCancel(ctx)
	cmd := hubTmuxAttachCommand(attachCtx, target.SocketName, sessionName)
	ptmx, err := startPTYWithSize(cmd, size)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start attach pty: %w", err)
	}
	if _, err := term.MakeRaw(int(ptmx.Fd())); err != nil {
		cancel()
		_ = ptmx.Close()
		return nil, fmt.Errorf("set attach pty raw mode: %w", err)
	}

	stream := &tmuxAttachStream{
		ptmx:     ptmx,
		cancel:   cancel,
		waitDone: make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(stream.waitDone)
	}()
	return stream, nil
}

func newTmuxAttachToken(socketName, sessionName string, ttl time.Duration) (string, error) {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return "", fmt.Errorf("tmux attach token session is required")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	payload := tmuxAttachTarget{
		SocketName:  strings.TrimSpace(socketName),
		SessionName: sessionName,
		ExpiresAt:   time.Now().Add(ttl).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	secret, err := tmuxAttachTokenSigningSecret()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(raw)
	return tmuxAttachTokenPrefix + base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func parseTmuxAttachToken(token string) (tmuxAttachTarget, bool, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, tmuxAttachTokenPrefix) {
		return tmuxAttachTarget{}, false, nil
	}
	encoded := strings.TrimPrefix(token, tmuxAttachTokenPrefix)
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 {
		return tmuxAttachTarget{}, true, fmt.Errorf("invalid tmux attach token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return tmuxAttachTarget{}, true, fmt.Errorf("decode tmux attach token: %w", err)
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tmuxAttachTarget{}, true, fmt.Errorf("decode tmux attach token signature: %w", err)
	}
	secret, err := tmuxAttachTokenSigningSecret()
	if err != nil {
		return tmuxAttachTarget{}, true, err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(raw)
	if !hmac.Equal(gotSig, mac.Sum(nil)) {
		return tmuxAttachTarget{}, true, fmt.Errorf("invalid tmux attach token signature")
	}
	var payload tmuxAttachTarget
	if err := json.Unmarshal(raw, &payload); err != nil {
		return tmuxAttachTarget{}, true, fmt.Errorf("decode tmux attach token payload: %w", err)
	}
	if strings.TrimSpace(payload.SessionName) == "" {
		return tmuxAttachTarget{}, true, fmt.Errorf("tmux attach token missing session")
	}
	if payload.ExpiresAt > 0 && time.Now().Unix() > payload.ExpiresAt {
		return tmuxAttachTarget{}, true, fmt.Errorf("tmux attach token expired")
	}
	return payload, true, nil
}

func tmuxAttachTokenSigningSecret() ([]byte, error) {
	tmuxAttachTokenSecretOnce.Do(func() {
		tmuxAttachTokenSecret = make([]byte, 32)
		if _, err := rand.Read(tmuxAttachTokenSecret); err != nil {
			tmuxAttachTokenSecretErr = fmt.Errorf("generate tmux attach token secret: %w", err)
		}
	})
	return tmuxAttachTokenSecret, tmuxAttachTokenSecretErr
}

func hubTmuxAttachCommand(ctx context.Context, socketName, sessionName string) *exec.Cmd {
	return tmux.EnsureSaneAttachTERM(tmux.ExecContext(ctx, socketName, "attach-session", "-t", sessionName))
}

func (b TmuxAttachBackend) findSession(sessionID string) (*statedb.InstanceRow, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	effectiveProfile := session.GetEffectiveProfile(b.Profile)
	profileDir, err := session.GetProfileDir(effectiveProfile)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(profileDir, "state.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("profile %q has no session state", effectiveProfile)
		}
		return nil, fmt.Errorf("stat local session state db: %w", err)
	}
	db, err := statedb.OpenReadOnly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.LoadInstances()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil && row.ID == sessionID {
			return row, nil
		}
	}
	return nil, fmt.Errorf("session %q not found in profile %q", sessionID, effectiveProfile)
}

type tmuxAttachStream struct {
	ptmx      *os.File
	cancel    context.CancelFunc
	waitDone  chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func (s *tmuxAttachStream) Read(p []byte) (int, error) {
	return s.ptmx.Read(p)
}

func (s *tmuxAttachStream) Write(p []byte) (int, error) {
	return s.ptmx.Write(p)
}

func (s *tmuxAttachStream) Resize(size TerminalSize) error {
	if size.Cols <= 0 || size.Rows <= 0 {
		return nil
	}
	winsize, err := ptyWinsize(size)
	if err != nil {
		return err
	}
	return pty.Setsize(s.ptmx, winsize)
}

func (s *tmuxAttachStream) Close() error {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.ptmx != nil {
			s.closeErr = s.ptmx.Close()
		}
		select {
		case <-s.waitDone:
		case <-time.After(250 * time.Millisecond):
		}
	})
	return s.closeErr
}

func startPTYWithSize(cmd *exec.Cmd, size TerminalSize) (*os.File, error) {
	if size.Cols > 0 && size.Rows > 0 {
		winsize, err := ptyWinsize(size)
		if err != nil {
			return nil, err
		}
		return pty.StartWithSize(cmd, winsize)
	}
	return pty.Start(cmd)
}

func ptyWinsize(size TerminalSize) (*pty.Winsize, error) {
	const maxPTYDimension = int(^uint16(0))
	if size.Cols <= 0 || size.Rows <= 0 {
		return nil, fmt.Errorf("terminal size must be positive")
	}
	if size.Cols > maxPTYDimension || size.Rows > maxPTYDimension {
		return nil, fmt.Errorf("terminal size %dx%d exceeds pty limit", size.Cols, size.Rows)
	}
	return &pty.Winsize{Cols: uint16(size.Cols), Rows: uint16(size.Rows)}, nil
}

type Peer interface {
	NodeID() string
	PeerID() string
	Send(Envelope) error
}

type AttachRouter struct {
	mu      sync.Mutex
	peers   map[string]Peer
	streams map[string]attachRoute
}

type attachRoute struct {
	requesterNodeID string
	ownerNodeID     string
	requesterPeer   Peer
	ownerPeer       Peer
}

func NewAttachRouter() *AttachRouter {
	return &AttachRouter{
		peers:   make(map[string]Peer),
		streams: make(map[string]attachRoute),
	}
}

func (r *AttachRouter) Register(peer Peer) {
	if r == nil || peer == nil {
		return
	}
	nodeID := strings.TrimSpace(peer.NodeID())
	if nodeID == "" {
		return
	}
	if strings.TrimSpace(peer.PeerID()) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers[nodeID] = peer
}

func (r *AttachRouter) UnregisterPeer(peer Peer) {
	if r == nil || peer == nil {
		return
	}
	nodeID := strings.TrimSpace(peer.NodeID())
	if nodeID == "" || strings.TrimSpace(peer.PeerID()) == "" {
		return
	}
	type notification struct {
		peer Peer
		env  Envelope
	}
	var notifications []notification

	r.mu.Lock()
	if samePeer(r.peers[nodeID], peer) {
		delete(r.peers, nodeID)
	}
	for streamID, route := range r.streams {
		switch {
		case samePeer(route.requesterPeer, peer):
			if route.ownerPeer != nil {
				if env, err := MarshalEnvelope(MsgAttachClose, nodeID, AttachClosePayload{StreamID: streamID, Reason: "attach requester disconnected"}); err == nil {
					notifications = append(notifications, notification{peer: route.ownerPeer, env: env})
				}
			}
			delete(r.streams, streamID)
		case samePeer(route.ownerPeer, peer):
			if route.requesterPeer != nil {
				if env, err := MarshalEnvelope(MsgAttachClosed, nodeID, AttachClosePayload{StreamID: streamID, Reason: "attach owner disconnected"}); err == nil {
					notifications = append(notifications, notification{peer: route.requesterPeer, env: env})
				}
			}
			delete(r.streams, streamID)
		}
	}
	r.mu.Unlock()

	for _, msg := range notifications {
		_ = msg.peer.Send(msg.env)
	}
}

func (r *AttachRouter) Unregister(nodeID string) {
	if r == nil {
		return
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return
	}
	type notification struct {
		peer Peer
		env  Envelope
	}
	var notifications []notification

	r.mu.Lock()
	delete(r.peers, nodeID)
	for streamID, route := range r.streams {
		if route.requesterNodeID == nodeID || route.ownerNodeID == nodeID {
			if route.requesterNodeID == nodeID {
				if route.ownerPeer != nil {
					if env, err := MarshalEnvelope(MsgAttachClose, nodeID, AttachClosePayload{StreamID: streamID, Reason: "attach requester disconnected"}); err == nil {
						notifications = append(notifications, notification{peer: route.ownerPeer, env: env})
					}
				}
			} else if route.requesterPeer != nil {
				if env, err := MarshalEnvelope(MsgAttachClosed, nodeID, AttachClosePayload{StreamID: streamID, Reason: "attach owner disconnected"}); err == nil {
					notifications = append(notifications, notification{peer: route.requesterPeer, env: env})
				}
			}
			delete(r.streams, streamID)
		}
	}
	r.mu.Unlock()

	for _, msg := range notifications {
		_ = msg.peer.Send(msg.env)
	}
}

func (r *AttachRouter) Open(ctx context.Context, requesterNodeID, ownerNodeID string, payload AttachOpenPayload) error {
	requester := r.peer(requesterNodeID)
	if requester == nil {
		return fmt.Errorf("attach requester node %q is not connected", strings.TrimSpace(requesterNodeID))
	}
	return r.OpenFromPeer(ctx, requester, ownerNodeID, payload)
}

func (r *AttachRouter) OpenFromPeer(ctx context.Context, requester Peer, ownerNodeID string, payload AttachOpenPayload) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	streamID := strings.TrimSpace(payload.StreamID)
	requesterNodeID := ""
	if requester != nil {
		requesterNodeID = strings.TrimSpace(requester.NodeID())
	}
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	if streamID == "" {
		return fmt.Errorf("attach stream id is required")
	}
	if requesterNodeID == "" {
		return fmt.Errorf("attach requester node id is required")
	}
	if requester == nil || strings.TrimSpace(requester.PeerID()) == "" {
		return fmt.Errorf("attach requester peer is required")
	}
	if ownerNodeID == "" {
		return fmt.Errorf("attach owner node id is required")
	}

	r.mu.Lock()
	owner := r.peers[ownerNodeID]
	if owner == nil {
		r.mu.Unlock()
		err := fmt.Errorf("attach owner node %q is not connected", ownerNodeID)
		if env, marshalErr := MarshalEnvelope(MsgAttachClosed, ownerNodeID, AttachClosePayload{StreamID: streamID, Reason: err.Error()}); marshalErr == nil {
			_ = requester.Send(env)
		}
		return err
	}
	if _, exists := r.streams[streamID]; exists {
		r.mu.Unlock()
		err := fmt.Errorf("attach stream %q already exists", streamID)
		if env, marshalErr := MarshalEnvelope(MsgAttachClosed, ownerNodeID, AttachClosePayload{StreamID: streamID, Reason: err.Error()}); marshalErr == nil {
			_ = requester.Send(env)
		}
		return err
	}
	r.streams[streamID] = attachRoute{
		requesterNodeID: requesterNodeID,
		ownerNodeID:     ownerNodeID,
		requesterPeer:   requester,
		ownerPeer:       owner,
	}
	r.mu.Unlock()

	payload.NodeID = ownerNodeID
	env, err := MarshalEnvelope(MsgAttachOpen, requesterNodeID, payload)
	if err != nil {
		r.removeStream(streamID)
		return err
	}
	if err := owner.Send(env); err != nil {
		r.removeStream(streamID)
		if closed, marshalErr := MarshalEnvelope(MsgAttachClosed, ownerNodeID, AttachClosePayload{StreamID: streamID, Reason: err.Error()}); marshalErr == nil {
			_ = requester.Send(closed)
		}
		return err
	}
	return nil
}

func (r *AttachRouter) ForwardFromRequester(requesterNodeID string, payload AttachDataPayload) error {
	return r.forwardData(requesterNodeID, payload.StreamID, MsgAttachData, payload, routeRequesterToOwner)
}

func (r *AttachRouter) ForwardFromOwner(ownerNodeID string, payload AttachDataPayload) error {
	return r.forwardData(ownerNodeID, payload.StreamID, MsgAttachData, payload, routeOwnerToRequester)
}

func (r *AttachRouter) ForwardDataFromNode(nodeID string, payload AttachDataPayload) error {
	direction, err := r.directionForNode(nodeID, payload.StreamID)
	if err != nil {
		return err
	}
	return r.forwardData(nodeID, payload.StreamID, MsgAttachData, payload, direction)
}

func (r *AttachRouter) ForwardDataFromPeer(peer Peer, payload AttachDataPayload) error {
	direction, err := r.directionForPeer(peer, payload.StreamID)
	if err != nil {
		return err
	}
	return r.forwardDataFromPeer(peer, payload.StreamID, MsgAttachData, payload, direction)
}

func (r *AttachRouter) ForwardResizeFromRequester(requesterNodeID string, payload AttachResizePayload) error {
	return r.forwardData(requesterNodeID, payload.StreamID, MsgAttachResize, payload, routeRequesterToOwner)
}

func (r *AttachRouter) ForwardResizeFromRequesterPeer(peer Peer, payload AttachResizePayload) error {
	return r.forwardDataFromPeer(peer, payload.StreamID, MsgAttachResize, payload, routeRequesterToOwner)
}

func (r *AttachRouter) ForwardCloseFromRequester(requesterNodeID string, payload AttachClosePayload) error {
	return r.forwardClose(requesterNodeID, payload.StreamID, MsgAttachClose, payload, routeRequesterToOwner)
}

func (r *AttachRouter) ForwardCloseFromRequesterPeer(peer Peer, payload AttachClosePayload) error {
	return r.forwardCloseFromPeer(peer, payload.StreamID, MsgAttachClose, payload, routeRequesterToOwner)
}

func (r *AttachRouter) ForwardReadyFromOwner(ownerNodeID string, payload AttachOpenPayload) error {
	return r.forwardData(ownerNodeID, payload.StreamID, MsgAttachReady, payload, routeOwnerToRequester)
}

func (r *AttachRouter) ForwardReadyFromOwnerPeer(peer Peer, payload AttachOpenPayload) error {
	return r.forwardDataFromPeer(peer, payload.StreamID, MsgAttachReady, payload, routeOwnerToRequester)
}

func (r *AttachRouter) ForwardClosedFromOwner(ownerNodeID string, payload AttachClosePayload) error {
	return r.forwardClose(ownerNodeID, payload.StreamID, MsgAttachClosed, payload, routeOwnerToRequester)
}

func (r *AttachRouter) ForwardClosedFromOwnerPeer(peer Peer, payload AttachClosePayload) error {
	return r.forwardCloseFromPeer(peer, payload.StreamID, MsgAttachClosed, payload, routeOwnerToRequester)
}

func (r *AttachRouter) directionForNode(nodeID, streamID string) (routeDirection, error) {
	if r == nil {
		return routeRequesterToOwner, fmt.Errorf("attach router is nil")
	}
	nodeID = strings.TrimSpace(nodeID)
	streamID = strings.TrimSpace(streamID)
	if nodeID == "" {
		return routeRequesterToOwner, fmt.Errorf("attach source node id is required")
	}
	if streamID == "" {
		return routeRequesterToOwner, fmt.Errorf("attach stream id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	route, ok := r.streams[streamID]
	if !ok {
		return routeRequesterToOwner, fmt.Errorf("attach stream %q is not open", streamID)
	}
	switch nodeID {
	case route.requesterNodeID:
		return routeRequesterToOwner, nil
	case route.ownerNodeID:
		return routeOwnerToRequester, nil
	default:
		return routeRequesterToOwner, fmt.Errorf("attach stream %q does not belong to node %q", streamID, nodeID)
	}
}

func (r *AttachRouter) directionForPeer(peer Peer, streamID string) (routeDirection, error) {
	if r == nil {
		return routeRequesterToOwner, fmt.Errorf("attach router is nil")
	}
	if peer == nil || strings.TrimSpace(peer.NodeID()) == "" || strings.TrimSpace(peer.PeerID()) == "" {
		return routeRequesterToOwner, fmt.Errorf("attach source peer is required")
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return routeRequesterToOwner, fmt.Errorf("attach stream id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	route, ok := r.streams[streamID]
	if !ok {
		return routeRequesterToOwner, fmt.Errorf("attach stream %q is not open", streamID)
	}
	switch {
	case samePeer(peer, route.requesterPeer):
		return routeRequesterToOwner, nil
	case samePeer(peer, route.ownerPeer):
		return routeOwnerToRequester, nil
	default:
		return routeRequesterToOwner, fmt.Errorf("attach stream %q does not belong to peer %q", streamID, peer.PeerID())
	}
}

type routeDirection int

const (
	routeRequesterToOwner routeDirection = iota
	routeOwnerToRequester
)

func (r *AttachRouter) forwardData(fromNodeID, streamID string, typ MessageType, payload any, direction routeDirection) error {
	peer, _, err := r.targetPeer(fromNodeID, streamID, direction)
	if err != nil {
		return err
	}
	env, err := MarshalEnvelope(typ, strings.TrimSpace(fromNodeID), payload)
	if err != nil {
		return err
	}
	if err := peer.Send(env); err != nil {
		r.removeStream(streamID)
		return err
	}
	return nil
}

func (r *AttachRouter) forwardDataFromPeer(from Peer, streamID string, typ MessageType, payload any, direction routeDirection) error {
	peer, _, err := r.targetPeerFromPeer(from, streamID, direction)
	if err != nil {
		return err
	}
	env, err := MarshalEnvelope(typ, strings.TrimSpace(from.NodeID()), payload)
	if err != nil {
		return err
	}
	if err := peer.Send(env); err != nil {
		r.removeStream(streamID)
		return err
	}
	return nil
}

func (r *AttachRouter) forwardClose(fromNodeID, streamID string, typ MessageType, payload any, direction routeDirection) error {
	peer, _, err := r.targetPeer(fromNodeID, streamID, direction)
	if err != nil {
		return err
	}
	env, err := MarshalEnvelope(typ, strings.TrimSpace(fromNodeID), payload)
	if err != nil {
		return err
	}
	sendErr := peer.Send(env)
	r.removeStream(streamID)
	return sendErr
}

func (r *AttachRouter) forwardCloseFromPeer(from Peer, streamID string, typ MessageType, payload any, direction routeDirection) error {
	peer, _, err := r.targetPeerFromPeer(from, streamID, direction)
	if err != nil {
		return err
	}
	env, err := MarshalEnvelope(typ, strings.TrimSpace(from.NodeID()), payload)
	if err != nil {
		return err
	}
	sendErr := peer.Send(env)
	r.removeStream(streamID)
	return sendErr
}

func (r *AttachRouter) targetPeer(fromNodeID, streamID string, direction routeDirection) (Peer, attachRoute, error) {
	if r == nil {
		return nil, attachRoute{}, fmt.Errorf("attach router is nil")
	}
	fromNodeID = strings.TrimSpace(fromNodeID)
	streamID = strings.TrimSpace(streamID)
	if fromNodeID == "" {
		return nil, attachRoute{}, fmt.Errorf("attach source node id is required")
	}
	if streamID == "" {
		return nil, attachRoute{}, fmt.Errorf("attach stream id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	route, ok := r.streams[streamID]
	if !ok {
		return nil, attachRoute{}, fmt.Errorf("attach stream %q is not open", streamID)
	}
	var expectedFrom, targetNodeID string
	if direction == routeRequesterToOwner {
		expectedFrom = route.requesterNodeID
		targetNodeID = route.ownerNodeID
	} else {
		expectedFrom = route.ownerNodeID
		targetNodeID = route.requesterNodeID
	}
	if fromNodeID != expectedFrom {
		return nil, attachRoute{}, fmt.Errorf("attach stream %q does not belong to node %q", streamID, fromNodeID)
	}
	var peer Peer
	if direction == routeRequesterToOwner {
		peer = route.ownerPeer
	} else {
		peer = route.requesterPeer
	}
	if peer == nil {
		delete(r.streams, streamID)
		return nil, attachRoute{}, fmt.Errorf("attach target node %q is not connected", targetNodeID)
	}
	return peer, route, nil
}

func (r *AttachRouter) targetPeerFromPeer(from Peer, streamID string, direction routeDirection) (Peer, attachRoute, error) {
	if r == nil {
		return nil, attachRoute{}, fmt.Errorf("attach router is nil")
	}
	if from == nil || strings.TrimSpace(from.NodeID()) == "" || strings.TrimSpace(from.PeerID()) == "" {
		return nil, attachRoute{}, fmt.Errorf("attach source peer is required")
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return nil, attachRoute{}, fmt.Errorf("attach stream id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	route, ok := r.streams[streamID]
	if !ok {
		return nil, attachRoute{}, fmt.Errorf("attach stream %q is not open", streamID)
	}
	var expectedFrom, target Peer
	var targetNodeID string
	if direction == routeRequesterToOwner {
		expectedFrom = route.requesterPeer
		target = route.ownerPeer
		targetNodeID = route.ownerNodeID
	} else {
		expectedFrom = route.ownerPeer
		target = route.requesterPeer
		targetNodeID = route.requesterNodeID
	}
	if !samePeer(from, expectedFrom) {
		return nil, attachRoute{}, fmt.Errorf("attach stream %q does not belong to peer %q", streamID, from.PeerID())
	}
	if target == nil {
		delete(r.streams, streamID)
		return nil, attachRoute{}, fmt.Errorf("attach target node %q is not connected", targetNodeID)
	}
	return target, route, nil
}

func (r *AttachRouter) removeStream(streamID string) {
	if r == nil {
		return
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.streams, streamID)
}

func (r *AttachRouter) peer(nodeID string) Peer {
	if r == nil {
		return nil
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peers[nodeID]
}

func samePeer(a, b Peer) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.TrimSpace(a.NodeID()) == strings.TrimSpace(b.NodeID()) &&
		strings.TrimSpace(a.PeerID()) == strings.TrimSpace(b.PeerID())
}
