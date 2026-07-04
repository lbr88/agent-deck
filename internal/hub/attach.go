package hub

import (
	"context"
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

type TmuxAttachBackend struct {
	Profile string
}

func NewTmuxAttachBackend(profile string) TmuxAttachBackend {
	return TmuxAttachBackend{Profile: profile}
}

func (b TmuxAttachBackend) Open(ctx context.Context, sessionID string, size TerminalSize) (AttachStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	row, err := b.findSession(sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.TmuxSession) == "" {
		return nil, fmt.Errorf("session %q has no tmux session", sessionID)
	}
	if err := tmux.ExecContext(ctx, row.TmuxSocketName, "has-session", "-t", row.TmuxSession).Run(); err != nil {
		return nil, fmt.Errorf("tmux session %q is not available: %w", row.TmuxSession, err)
	}

	attachCtx, cancel := context.WithCancel(ctx)
	cmd := tmux.ExecContext(attachCtx, row.TmuxSocketName, "attach-session", "-t", row.TmuxSession)
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
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: uint16(size.Cols), Rows: uint16(size.Rows)})
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
		return pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(size.Cols), Rows: uint16(size.Rows)})
	}
	return pty.Start(cmd)
}

type Peer interface {
	NodeID() string
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers[nodeID] = peer
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
				if owner := r.peers[route.ownerNodeID]; owner != nil {
					if env, err := MarshalEnvelope(MsgAttachClose, nodeID, AttachClosePayload{StreamID: streamID, Reason: "attach requester disconnected"}); err == nil {
						notifications = append(notifications, notification{peer: owner, env: env})
					}
				}
			} else if requester := r.peers[route.requesterNodeID]; requester != nil {
				if env, err := MarshalEnvelope(MsgAttachClosed, nodeID, AttachClosePayload{StreamID: streamID, Reason: "attach owner disconnected"}); err == nil {
					notifications = append(notifications, notification{peer: requester, env: env})
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	streamID := strings.TrimSpace(payload.StreamID)
	requesterNodeID = strings.TrimSpace(requesterNodeID)
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	if streamID == "" {
		return fmt.Errorf("attach stream id is required")
	}
	if requesterNodeID == "" {
		return fmt.Errorf("attach requester node id is required")
	}
	if ownerNodeID == "" {
		return fmt.Errorf("attach owner node id is required")
	}

	r.mu.Lock()
	owner := r.peers[ownerNodeID]
	if owner == nil {
		requester := r.peers[requesterNodeID]
		r.mu.Unlock()
		err := fmt.Errorf("attach owner node %q is not connected", ownerNodeID)
		if requester != nil {
			if env, marshalErr := MarshalEnvelope(MsgAttachClosed, ownerNodeID, AttachClosePayload{StreamID: streamID, Reason: err.Error()}); marshalErr == nil {
				_ = requester.Send(env)
			}
		}
		return err
	}
	r.streams[streamID] = attachRoute{requesterNodeID: requesterNodeID, ownerNodeID: ownerNodeID}
	r.mu.Unlock()

	payload.NodeID = ownerNodeID
	env, err := MarshalEnvelope(MsgAttachOpen, requesterNodeID, payload)
	if err != nil {
		r.removeStream(streamID)
		return err
	}
	if err := owner.Send(env); err != nil {
		r.removeStream(streamID)
		if requester := r.peer(requesterNodeID); requester != nil {
			if closed, marshalErr := MarshalEnvelope(MsgAttachClosed, ownerNodeID, AttachClosePayload{StreamID: streamID, Reason: err.Error()}); marshalErr == nil {
				_ = requester.Send(closed)
			}
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

func (r *AttachRouter) ForwardResizeFromRequester(requesterNodeID string, payload AttachResizePayload) error {
	return r.forwardData(requesterNodeID, payload.StreamID, MsgAttachResize, payload, routeRequesterToOwner)
}

func (r *AttachRouter) ForwardCloseFromRequester(requesterNodeID string, payload AttachClosePayload) error {
	return r.forwardClose(requesterNodeID, payload.StreamID, MsgAttachClose, payload, routeRequesterToOwner)
}

func (r *AttachRouter) ForwardReadyFromOwner(ownerNodeID string, payload AttachOpenPayload) error {
	return r.forwardData(ownerNodeID, payload.StreamID, MsgAttachReady, payload, routeOwnerToRequester)
}

func (r *AttachRouter) ForwardClosedFromOwner(ownerNodeID string, payload AttachClosePayload) error {
	return r.forwardClose(ownerNodeID, payload.StreamID, MsgAttachClosed, payload, routeOwnerToRequester)
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
	peer := r.peers[targetNodeID]
	if peer == nil {
		delete(r.streams, streamID)
		return nil, attachRoute{}, fmt.Errorf("attach target node %q is not connected", targetNodeID)
	}
	return peer, route, nil
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
