package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type SessionSource interface {
	Snapshot(context.Context) (SnapshotPayload, error)
}

type ClientConfig struct {
	URL              string
	NodeID           string
	NodeName         string
	Token            string
	Version          string
	TLSSkipVerify    bool
	CAPemFile        string
	ServerName       string
	PinnedCertSHA256 string

	HeartbeatInterval  time.Duration
	SnapshotInterval   time.Duration
	ReconnectBaseDelay time.Duration
	ReconnectMaxDelay  time.Duration

	OnStatus       func(string)
	OnSnapshot     func(NodeSessions)
	OnTrustRequest func(TrustRequestPayload)

	AttachBackend AttachBackend
	ActionBackend ActionBackend
}

type Client struct {
	cfg    ClientConfig
	source SessionSource

	mu               sync.Mutex
	activeConn       *clientConn
	ownerStreams     map[string]*ownerAttachStream
	requesterStreams map[string]*requesterAttachStream
	commandWaiters   map[string]*commandWaiter
}

func NewClient(cfg ClientConfig, source SessionSource) *Client {
	return &Client{cfg: cfg, source: source}
}

type clientConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *clientConn) writeEnvelope(typ MessageType, nodeID string, payload any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("hub client is not connected")
	}
	env, err := MarshalEnvelope(typ, nodeID, payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := writeWebSocketJSON(c.conn, env); err != nil {
		return fmt.Errorf("write hub %s: %w", typ, err)
	}
	return nil
}

type ownerAttachStream struct {
	streamID string
	stream   AttachStream
	cancel   context.CancelFunc
	once     sync.Once
}

func (s *ownerAttachStream) close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.stream != nil {
			_ = s.stream.Close()
		}
	})
}

type requesterAttachStream struct {
	streamID string
	ready    chan AttachOpenPayload
	data     chan []byte
	closed   chan AttachClosePayload
}

type clientAttachStream struct {
	client *Client
	conn   *clientConn
	stream *requesterAttachStream
	closed chan struct{}
	once   sync.Once
	buf    []byte
}

type commandWaiter struct {
	commandID string
	result    chan CommandResultPayload
}

func newRequesterAttachStream(streamID string) *requesterAttachStream {
	return &requesterAttachStream{
		streamID: streamID,
		ready:    make(chan AttachOpenPayload, 1),
		data:     make(chan []byte, 128),
		closed:   make(chan AttachClosePayload, 1),
	}
}

func (s *clientAttachStream) Read(p []byte) (int, error) {
	if s == nil || s.stream == nil {
		return 0, io.ErrClosedPipe
	}
	for len(s.buf) == 0 {
		select {
		case data := <-s.stream.data:
			if len(data) == 0 {
				continue
			}
			s.buf = append(s.buf, data...)
		case closed := <-s.stream.closed:
			if err := attachClosedError(closed); err != nil && !errors.Is(err, errAttachClosedByOwner) {
				return 0, err
			}
			return 0, io.EOF
		case <-s.closed:
			return 0, io.EOF
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func (s *clientAttachStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s == nil || s.conn == nil || s.stream == nil || s.client == nil {
		return 0, io.ErrClosedPipe
	}
	select {
	case <-s.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	if err := s.conn.writeEnvelope(MsgAttachData, s.client.cfg.NodeID, NewAttachData(s.stream.streamID, p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *clientAttachStream) Resize(size TerminalSize) error {
	if s == nil || s.conn == nil || s.stream == nil || s.client == nil {
		return io.ErrClosedPipe
	}
	if size.Cols <= 0 || size.Rows <= 0 {
		return nil
	}
	select {
	case <-s.closed:
		return io.ErrClosedPipe
	default:
	}
	return s.conn.writeEnvelope(MsgAttachResize, s.client.cfg.NodeID, AttachResizePayload{
		StreamID: s.stream.streamID,
		Cols:     size.Cols,
		Rows:     size.Rows,
	})
}

func (s *clientAttachStream) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.once.Do(func() {
		close(s.closed)
		if s.client != nil && s.stream != nil {
			s.client.unregisterRequesterStream(s.stream.streamID)
		}
		if s.conn != nil && s.client != nil && s.stream != nil {
			err = s.conn.writeEnvelope(MsgAttachClose, s.client.cfg.NodeID, AttachClosePayload{
				StreamID: s.stream.streamID,
				Reason:   "detached",
			})
		}
	})
	return err
}

func (c *Client) setActiveConn(conn *clientConn) {
	c.mu.Lock()
	c.activeConn = conn
	c.mu.Unlock()
}

func (c *Client) clearActiveConn(conn *clientConn) {
	c.mu.Lock()
	if c.activeConn == conn {
		c.activeConn = nil
	}
	ownerStreams := c.ownerStreams
	c.ownerStreams = nil
	for _, stream := range c.requesterStreams {
		select {
		case stream.closed <- AttachClosePayload{StreamID: stream.streamID, Reason: "hub disconnected"}:
		default:
		}
	}
	c.requesterStreams = nil
	for _, waiter := range c.commandWaiters {
		select {
		case waiter.result <- CommandResultPayload{CommandID: waiter.commandID, OK: false, Error: "hub disconnected"}:
		default:
		}
	}
	c.commandWaiters = nil
	c.mu.Unlock()

	for _, stream := range ownerStreams {
		stream.close()
	}
}

func (c *Client) currentConn() *clientConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeConn
}

func (c *Client) buildSnapshot(ctx context.Context) (SnapshotPayload, error) {
	if c.source == nil {
		return SnapshotPayload{SentAt: time.Now().UTC()}, nil
	}
	return c.source.Snapshot(ctx)
}

func (c *Client) Connect(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := c.normalizedConfig()
	if err != nil {
		return err
	}
	wsURL, err := nodeWebSocketURL(cfg.URL, cfg.NodeID)
	if err != nil {
		return err
	}
	tlsConfig, err := clientTLSConfig(cfg)
	if err != nil {
		return err
	}

	backoff := cfg.reconnectBaseDelay()
	for {
		if ctx.Err() != nil {
			return nil
		}
		notifyClientStatus(cfg, "connecting")
		if err := c.connectOnce(ctx, cfg, wsURL, tlsConfig); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		notifyClientStatus(cfg, "offline")
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff *= 2
		if maxDelay := cfg.reconnectMaxDelay(); backoff > maxDelay {
			backoff = maxDelay
		}
	}
}

func (c *Client) connectOnce(ctx context.Context, cfg ClientConfig, wsURL string, tlsConfig *tls.Config) error {
	dialer := websocket.Dialer{TLSClientConfig: tlsConfig}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.Token)
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return fmt.Errorf("connect hub websocket: %w", err)
	}
	defer conn.Close()
	conn.SetReadLimit(maxHubEnvelopeBytes)
	hubConn := &clientConn{conn: conn}
	c.setActiveConn(hubConn)
	defer c.clearActiveConn(hubConn)

	closeOnCancel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closeOnCancel:
		}
	}()
	defer close(closeOnCancel)

	if err := hubConn.writeEnvelope(MsgHello, cfg.NodeID, NodeHelloPayload{
		NodeID:   cfg.NodeID,
		NodeName: cfg.NodeName,
		Version:  cfg.Version,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}); err != nil {
		return err
	}
	if err := c.publishSnapshot(ctx, hubConn, cfg.NodeID, cfg.NodeName); err != nil {
		return err
	}
	notifyClientStatus(cfg, "connected")

	readErr := make(chan error, 1)
	go func() {
		readErr <- c.readLoop(ctx, hubConn)
	}()

	heartbeatTicker := time.NewTicker(cfg.heartbeatInterval())
	defer heartbeatTicker.Stop()
	snapshotTicker := time.NewTicker(cfg.snapshotInterval())
	defer snapshotTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case <-heartbeatTicker.C:
			if err := hubConn.writeEnvelope(MsgHeartbeat, cfg.NodeID, nil); err != nil {
				return err
			}
		case <-snapshotTicker.C:
			if err := c.publishSnapshot(ctx, hubConn, cfg.NodeID, cfg.NodeName); err != nil {
				return err
			}
		}
	}
}

func (c *Client) publishSnapshot(ctx context.Context, conn *clientConn, nodeID, nodeName string) error {
	snapshot, err := c.buildSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("build snapshot: %w", err)
	}
	if snapshot.SentAt.IsZero() {
		snapshot.SentAt = time.Now().UTC()
	}
	if strings.TrimSpace(snapshot.NodeID) == "" {
		snapshot.NodeID = nodeID
	}
	if strings.TrimSpace(snapshot.NodeName) == "" {
		snapshot.NodeName = strings.TrimSpace(nodeName)
	}
	return conn.writeEnvelope(MsgSnapshot, nodeID, snapshot)
}

func (c *Client) readLoop(ctx context.Context, conn *clientConn) error {
	for {
		var env Envelope
		if err := conn.conn.ReadJSON(&env); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		c.dispatchWithConn(ctx, conn, env)
	}
}

func (c *Client) dispatch(env Envelope) {
	c.dispatchWithConn(context.Background(), nil, env)
}

func (c *Client) dispatchWithConn(ctx context.Context, conn *clientConn, env Envelope) {
	switch env.Type {
	case MsgSnapshot:
		if c.cfg.OnSnapshot == nil {
			return
		}
		var snapshot SnapshotPayload
		if err := json.Unmarshal(env.Payload, &snapshot); err != nil {
			return
		}
		nodeID := strings.TrimSpace(snapshot.NodeID)
		if nodeID == "" {
			nodeID = strings.TrimSpace(env.NodeID)
		}
		if nodeID == "" {
			return
		}
		nodeName := strings.TrimSpace(snapshot.NodeName)
		if nodeName == "" {
			nodeName = nodeID
		}
		c.cfg.OnSnapshot(NodeSessions{
			Node:     Node{ID: nodeID, Name: nodeName},
			SentAt:   snapshot.SentAt,
			Sessions: append([]SessionInfo(nil), snapshot.Sessions...),
			Groups:   append([]GroupInfo(nil), snapshot.Groups...),
		})
	case MsgAttachOpen:
		c.handleAttachOpen(ctx, conn, env)
	case MsgAttachReady:
		c.handleAttachReady(env)
	case MsgAttachData:
		c.handleAttachData(conn, env)
	case MsgAttachResize:
		c.handleAttachResize(conn, env)
	case MsgAttachClose:
		c.handleAttachClose(conn, env)
	case MsgAttachClosed:
		c.handleAttachClosed(env)
	case MsgCommand:
		c.handleCommand(ctx, conn, env)
	case MsgCommandResult:
		c.handleCommandResult(env)
	case MsgTrustRequest:
		c.handleTrustRequest(env)
	case MsgWelcome, MsgHeartbeat, MsgError:
		return
	default:
		return
	}
}

func (c *Client) handleTrustRequest(env Envelope) {
	if c.cfg.OnTrustRequest == nil {
		return
	}
	var payload TrustRequestPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	payload.NodeID = strings.TrimSpace(payload.NodeID)
	if payload.NodeID == "" {
		return
	}
	if strings.TrimSpace(payload.NodeName) == "" {
		payload.NodeName = payload.NodeID
	}
	c.cfg.OnTrustRequest(payload)
}

func (c *Client) handleCommandResult(env Envelope) {
	var payload CommandResultPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	waiter := c.unregisterCommandWaiter(payload.CommandID)
	if waiter == nil {
		return
	}
	select {
	case waiter.result <- payload:
	default:
	}
}

func (c *Client) handleCommand(ctx context.Context, conn *clientConn, env Envelope) {
	if conn == nil {
		return
	}
	var payload CommandPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	result := CommandResultPayload{CommandID: payload.CommandID, OK: true}
	raw, err := (CommandDispatcher{Backend: c.cfg.ActionBackend}).Dispatch(ctx, payload)
	if err != nil {
		result.OK = false
		result.Error = err.Error()
	} else {
		result.Result = raw
	}
	_ = conn.writeEnvelope(MsgCommandResult, c.cfg.NodeID, result)
}

func (c *Client) handleAttachOpen(ctx context.Context, conn *clientConn, env Envelope) {
	if conn == nil {
		return
	}
	var payload AttachOpenPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	streamID := strings.TrimSpace(payload.StreamID)
	sessionID := strings.TrimSpace(payload.SessionID)
	if streamID == "" || sessionID == "" {
		return
	}
	if !c.reserveOwnerStream(streamID) {
		_ = conn.writeEnvelope(MsgAttachClosed, c.cfg.NodeID, AttachClosePayload{
			StreamID: streamID,
			Reason:   fmt.Sprintf("attach stream %q already exists", streamID),
		})
		return
	}
	backend := c.cfg.AttachBackend
	if backend == nil {
		backend = NewTmuxAttachBackend("")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := backend.Open(streamCtx, sessionID, TerminalSize{Cols: payload.Cols, Rows: payload.Rows})
	if err != nil {
		cancel()
		c.unregisterOwnerStream(streamID)
		_ = conn.writeEnvelope(MsgAttachClosed, c.cfg.NodeID, AttachClosePayload{StreamID: streamID, Reason: err.Error()})
		return
	}
	ownerStream := &ownerAttachStream{streamID: streamID, stream: stream, cancel: cancel}
	c.setReservedOwnerStream(ownerStream)
	if err := conn.writeEnvelope(MsgAttachReady, c.cfg.NodeID, AttachOpenPayload{
		StreamID:  streamID,
		NodeID:    c.cfg.NodeID,
		SessionID: sessionID,
		Cols:      payload.Cols,
		Rows:      payload.Rows,
	}); err != nil {
		c.unregisterOwnerStream(streamID)
		ownerStream.close()
		return
	}
	go c.pumpOwnerAttachOutput(streamCtx, conn, ownerStream)
}

func (c *Client) pumpOwnerAttachOutput(ctx context.Context, conn *clientConn, ownerStream *ownerAttachStream) {
	defer ownerStream.close()

	buf := make([]byte, 32*1024)
	for {
		n, err := ownerStream.stream.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			if writeErr := conn.writeEnvelope(MsgAttachData, c.cfg.NodeID, NewAttachData(ownerStream.streamID, data)); writeErr != nil {
				c.unregisterOwnerStream(ownerStream.streamID)
				return
			}
		}
		if err != nil {
			reason := ""
			if ctx.Err() == nil && err != io.EOF {
				reason = err.Error()
			}
			if c.unregisterOwnerStream(ownerStream.streamID) == ownerStream {
				_ = conn.writeEnvelope(MsgAttachClosed, c.cfg.NodeID, AttachClosePayload{StreamID: ownerStream.streamID, Reason: reason})
			}
			return
		}
	}
}

func (c *Client) handleAttachReady(env Envelope) {
	var payload AttachOpenPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	stream := c.requesterStream(payload.StreamID)
	if stream == nil {
		return
	}
	select {
	case stream.ready <- payload:
	default:
	}
}

func (c *Client) handleAttachData(conn *clientConn, env Envelope) {
	var payload AttachDataPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	data, err := payload.Bytes()
	if err != nil {
		return
	}
	if ownerStream := c.ownerStream(payload.StreamID); ownerStream != nil {
		if _, err := ownerStream.stream.Write(data); err != nil {
			c.closeOwnerAttachWithReason(conn, payload.StreamID, err.Error())
		}
		return
	}
	if requesterStream := c.requesterStream(payload.StreamID); requesterStream != nil {
		requesterStream.data <- data
	}
}

func (c *Client) handleAttachResize(conn *clientConn, env Envelope) {
	var payload AttachResizePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	if ownerStream := c.ownerStream(payload.StreamID); ownerStream != nil {
		if err := ownerStream.stream.Resize(TerminalSize{Cols: payload.Cols, Rows: payload.Rows}); err != nil {
			c.closeOwnerAttachWithReason(conn, payload.StreamID, err.Error())
		}
	}
}

func (c *Client) handleAttachClose(conn *clientConn, env Envelope) {
	var payload AttachClosePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	ownerStream := c.unregisterOwnerStream(payload.StreamID)
	if ownerStream == nil {
		return
	}
	ownerStream.close()
	if conn != nil {
		_ = conn.writeEnvelope(MsgAttachClosed, c.cfg.NodeID, AttachClosePayload{StreamID: payload.StreamID})
	}
}

func (c *Client) handleAttachClosed(env Envelope) {
	var payload AttachClosePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	stream := c.unregisterRequesterStream(payload.StreamID)
	if stream == nil {
		return
	}
	select {
	case stream.closed <- payload:
	default:
	}
}

func (c *Client) closeOwnerAttachWithReason(conn *clientConn, streamID, reason string) {
	ownerStream := c.unregisterOwnerStream(streamID)
	if ownerStream == nil {
		return
	}
	ownerStream.close()
	if conn != nil {
		_ = conn.writeEnvelope(MsgAttachClosed, c.cfg.NodeID, AttachClosePayload{StreamID: streamID, Reason: reason})
	}
}

func (c *Client) reserveOwnerStream(streamID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return false
	}
	if c.ownerStreams == nil {
		c.ownerStreams = make(map[string]*ownerAttachStream)
	}
	if _, exists := c.ownerStreams[streamID]; exists {
		return false
	}
	c.ownerStreams[streamID] = nil
	return true
}

func (c *Client) setReservedOwnerStream(stream *ownerAttachStream) {
	if stream == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ownerStreams == nil {
		c.ownerStreams = make(map[string]*ownerAttachStream)
	}
	c.ownerStreams[stream.streamID] = stream
}

func (c *Client) ownerStream(streamID string) *ownerAttachStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ownerStreams[strings.TrimSpace(streamID)]
}

func (c *Client) unregisterOwnerStream(streamID string) *ownerAttachStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	streamID = strings.TrimSpace(streamID)
	stream := c.ownerStreams[streamID]
	delete(c.ownerStreams, streamID)
	return stream
}

func (c *Client) registerRequesterStream(stream *requesterAttachStream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requesterStreams == nil {
		c.requesterStreams = make(map[string]*requesterAttachStream)
	}
	c.requesterStreams[stream.streamID] = stream
}

func (c *Client) requesterStream(streamID string) *requesterAttachStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requesterStreams[strings.TrimSpace(streamID)]
}

func (c *Client) unregisterRequesterStream(streamID string) *requesterAttachStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	streamID = strings.TrimSpace(streamID)
	stream := c.requesterStreams[streamID]
	delete(c.requesterStreams, streamID)
	return stream
}

func (c *Client) registerCommandWaiter(waiter *commandWaiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.commandWaiters == nil {
		c.commandWaiters = make(map[string]*commandWaiter)
	}
	c.commandWaiters[waiter.commandID] = waiter
}

func (c *Client) unregisterCommandWaiter(commandID string) *commandWaiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	commandID = strings.TrimSpace(commandID)
	waiter := c.commandWaiters[commandID]
	delete(c.commandWaiters, commandID)
	return waiter
}

func (c *Client) Command(ctx context.Context, nodeID, action string, payload any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	nodeID = strings.TrimSpace(nodeID)
	action = strings.TrimSpace(action)
	if nodeID == "" {
		return nil, fmt.Errorf("hub command node id is required")
	}
	if action == "" {
		return nil, fmt.Errorf("hub command action is required")
	}
	conn := c.currentConn()
	if conn == nil {
		return nil, fmt.Errorf("hub client is not connected")
	}
	commandID, err := newSecret("cmd_")
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal command payload: %w", err)
	}
	waiter := &commandWaiter{commandID: commandID, result: make(chan CommandResultPayload, 1)}
	c.registerCommandWaiter(waiter)
	defer c.unregisterCommandWaiter(commandID)

	if err := conn.writeEnvelope(MsgCommand, c.cfg.NodeID, CommandPayload{
		CommandID: commandID,
		NodeID:    nodeID,
		Action:    action,
		Payload:   raw,
	}); err != nil {
		return nil, err
	}

	select {
	case result := <-waiter.result:
		if !result.OK {
			if strings.TrimSpace(result.Error) == "" {
				return nil, fmt.Errorf("hub command %s failed", action)
			}
			return nil, fmt.Errorf("hub command %s failed: %s", action, result.Error)
		}
		return result.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) TrustDecision(ctx context.Context, nodeID string, allow bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return fmt.Errorf("hub trust node id is required")
	}
	conn := c.currentConn()
	if conn == nil {
		return fmt.Errorf("hub client is not connected")
	}
	done := make(chan error, 1)
	go func() {
		done <- conn.writeEnvelope(MsgTrustDecision, c.cfg.NodeID, TrustDecisionPayload{NodeID: nodeID, Allow: allow})
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) OpenAttach(ctx context.Context, nodeID, sessionID string, size TerminalSize) (AttachStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	nodeID = strings.TrimSpace(nodeID)
	sessionID = strings.TrimSpace(sessionID)
	if nodeID == "" {
		return nil, fmt.Errorf("hub attach node id is required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("hub attach session id is required")
	}
	conn := c.currentConn()
	if conn == nil {
		return nil, fmt.Errorf("hub client is not connected")
	}
	if size.Cols <= 0 || size.Rows <= 0 {
		size = TerminalSize{Cols: 80, Rows: 24}
	}
	streamID, err := newSecret("attach_")
	if err != nil {
		return nil, err
	}
	stream := newRequesterAttachStream(streamID)
	c.registerRequesterStream(stream)

	if err := conn.writeEnvelope(MsgAttachOpen, c.cfg.NodeID, AttachOpenPayload{
		StreamID:  streamID,
		NodeID:    nodeID,
		SessionID: sessionID,
		Cols:      size.Cols,
		Rows:      size.Rows,
	}); err != nil {
		c.unregisterRequesterStream(streamID)
		return nil, err
	}

	select {
	case <-stream.ready:
		return &clientAttachStream{
			client: c,
			conn:   conn,
			stream: stream,
			closed: make(chan struct{}),
		}, nil
	case closed := <-stream.closed:
		c.unregisterRequesterStream(streamID)
		err := attachClosedError(closed)
		if errors.Is(err, errAttachClosedByOwner) || err == nil {
			return nil, fmt.Errorf("hub attach closed before ready")
		}
		return nil, err
	case <-ctx.Done():
		c.unregisterRequesterStream(streamID)
		_ = conn.writeEnvelope(MsgAttachClose, c.cfg.NodeID, AttachClosePayload{StreamID: streamID, Reason: ctx.Err().Error()})
		return nil, ctx.Err()
	}
}

func (c *Client) Attach(ctx context.Context, nodeID, sessionID string, size TerminalSize) error {
	if ctx == nil {
		ctx = context.Background()
	}
	nodeID = strings.TrimSpace(nodeID)
	sessionID = strings.TrimSpace(sessionID)
	if nodeID == "" {
		return fmt.Errorf("hub attach node id is required")
	}
	if sessionID == "" {
		return fmt.Errorf("hub attach session id is required")
	}
	conn := c.currentConn()
	if conn == nil {
		return fmt.Errorf("hub client is not connected")
	}
	if size.Cols <= 0 || size.Rows <= 0 {
		size = currentTerminalSize()
	}
	streamID, err := newSecret("attach_")
	if err != nil {
		return err
	}
	stream := newRequesterAttachStream(streamID)
	c.registerRequesterStream(stream)
	defer c.unregisterRequesterStream(streamID)

	if err := conn.writeEnvelope(MsgAttachOpen, c.cfg.NodeID, AttachOpenPayload{
		StreamID:  streamID,
		NodeID:    nodeID,
		SessionID: sessionID,
		Cols:      size.Cols,
		Rows:      size.Rows,
	}); err != nil {
		return err
	}

	select {
	case <-stream.ready:
	case closed := <-stream.closed:
		err := attachClosedError(closed)
		if errors.Is(err, errAttachClosedByOwner) {
			return nil
		}
		return err
	case <-ctx.Done():
		_ = conn.writeEnvelope(MsgAttachClose, c.cfg.NodeID, AttachClosePayload{StreamID: streamID, Reason: ctx.Err().Error()})
		return ctx.Err()
	}

	return c.runRequesterAttachTerminal(ctx, conn, stream)
}

func (c *Client) runRequesterAttachTerminal(ctx context.Context, conn *clientConn, stream *requesterAttachStream) error {
	stdinFD := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(stdinFD)
	if err != nil {
		_ = conn.writeEnvelope(MsgAttachClose, c.cfg.NodeID, AttachClosePayload{StreamID: stream.streamID, Reason: err.Error()})
		return fmt.Errorf("set terminal raw mode: %w", err)
	}
	var restoreOnce sync.Once
	restoreTerminal := func() {
		restoreOnce.Do(func() { _ = term.Restore(stdinFD, oldState) })
	}
	defer restoreTerminal()

	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 4)
	sendErr := func(err error) {
		select {
		case errCh <- err:
		case <-attachCtx.Done():
		}
	}

	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	defer signal.Stop(sigwinch)

	go func() {
		for {
			select {
			case <-attachCtx.Done():
				return
			case <-sigwinch:
				size := currentTerminalSize()
				if size.Cols > 0 && size.Rows > 0 {
					_ = conn.writeEnvelope(MsgAttachResize, c.cfg.NodeID, AttachResizePayload{
						StreamID: stream.streamID,
						Cols:     size.Cols,
						Rows:     size.Rows,
					})
				}
			}
		}
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := readTerminalInput(attachCtx, stdinFD, buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				if idx := tmux.IndexCtrlQ(chunk); idx >= 0 {
					if idx > 0 {
						if writeErr := conn.writeEnvelope(MsgAttachData, c.cfg.NodeID, NewAttachData(stream.streamID, chunk[:idx])); writeErr != nil {
							sendErr(writeErr)
							return
						}
					}
					sendErr(nil)
					return
				}
				if writeErr := conn.writeEnvelope(MsgAttachData, c.cfg.NodeID, NewAttachData(stream.streamID, chunk)); writeErr != nil {
					sendErr(writeErr)
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					sendErr(nil)
				} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					sendErr(attachCtx.Err())
				} else {
					sendErr(err)
				}
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case data := <-stream.data:
				if len(data) == 0 {
					continue
				}
				if _, err := os.Stdout.Write(data); err != nil {
					sendErr(err)
					return
				}
			case closed := <-stream.closed:
				sendErr(attachClosedError(closed))
				return
			case <-attachCtx.Done():
				sendErr(attachCtx.Err())
				return
			}
		}
	}()

	err = <-errCh
	cancel()
	restoreTerminal()
	if errors.Is(err, errAttachClosedByOwner) {
		return nil
	}
	reason := "detached"
	switch {
	case ctx.Err() != nil:
		reason = ctx.Err().Error()
	case err != nil:
		reason = err.Error()
	}
	_ = conn.writeEnvelope(MsgAttachClose, c.cfg.NodeID, AttachClosePayload{StreamID: stream.streamID, Reason: reason})
	return err
}

func readTerminalInput(ctx context.Context, fd int, buf []byte) (int, error) {
	pollFD, err := pollFDFromInt(fd)
	if err != nil {
		return 0, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		events := []unix.PollFd{{Fd: pollFD, Events: unix.POLLIN}}
		n, err := unix.Poll(events, 100)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return 0, err
		}
		if n == 0 {
			continue
		}
		if events[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0 {
			return os.Stdin.Read(buf)
		}
	}
}

func pollFDFromInt(fd int) (int32, error) {
	const maxInt32 = int(^uint32(0) >> 1)
	if fd < 0 || fd > maxInt32 {
		return 0, fmt.Errorf("file descriptor %d out of poll range", fd)
	}
	return int32(fd), nil
}

func attachClosedError(payload AttachClosePayload) error {
	if strings.TrimSpace(payload.Reason) == "" {
		return errAttachClosedByOwner
	}
	return fmt.Errorf("hub attach closed: %s", payload.Reason)
}

var errAttachClosedByOwner = errors.New("hub attach closed by owner")

func currentTerminalSize() TerminalSize {
	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return TerminalSize{}
	}
	return TerminalSize{Cols: cols, Rows: rows}
}

func notifyClientStatus(cfg ClientConfig, status string) {
	if cfg.OnStatus != nil {
		cfg.OnStatus(status)
	}
}

type LocalSessionSource struct {
	Profile string
}

func (s LocalSessionSource) Snapshot(ctx context.Context) (SnapshotPayload, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return SnapshotPayload{}, ctx.Err()
	default:
	}

	effectiveProfile := session.GetEffectiveProfile(s.Profile)
	profileDir, err := session.GetProfileDir(effectiveProfile)
	if err != nil {
		return SnapshotPayload{}, err
	}
	dbPath := filepath.Join(profileDir, "state.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return SnapshotPayload{SentAt: time.Now().UTC()}, nil
		}
		return SnapshotPayload{}, fmt.Errorf("stat local session state db: %w", err)
	}

	db, err := statedb.OpenReadOnly(dbPath)
	if err != nil {
		return SnapshotPayload{}, err
	}
	defer db.Close()

	rows, err := db.LoadInstances()
	if err != nil {
		return SnapshotPayload{}, fmt.Errorf("load local sessions: %w", err)
	}
	groupRows, err := db.LoadGroups()
	if err != nil {
		return SnapshotPayload{}, fmt.Errorf("load local groups: %w", err)
	}
	sessions := make([]SessionInfo, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		sessions = append(sessions, sessionInfoFromRow(row))
	}
	groups := make([]GroupInfo, 0, len(groupRows))
	for _, row := range groupRows {
		if row == nil {
			continue
		}
		groups = append(groups, groupInfoFromRow(row))
	}
	return SnapshotPayload{SentAt: time.Now().UTC(), Sessions: sessions, Groups: groups}, nil
}

func groupInfoFromRow(row *statedb.GroupRow) GroupInfo {
	return GroupInfo{
		Name:          row.Name,
		Path:          row.Path,
		Expanded:      row.Expanded,
		Order:         row.Order,
		DefaultPath:   row.DefaultPath,
		MaxConcurrent: row.MaxConcurrent,
	}
}

func sessionInfoFromRow(row *statedb.InstanceRow) SessionInfo {
	info := SessionInfo{
		ID:               row.ID,
		Title:            row.Title,
		Tool:             row.Tool,
		Status:           row.Status,
		GroupPath:        row.GroupPath,
		ProjectPath:      row.ProjectPath,
		Notes:            notesFromRow(row),
		DisplaySessionID: displaySessionIDFromRow(row),
		CanFork:          canForkFromRow(row),
		UpdatedAt:        rowUpdatedAt(row),
	}
	if !row.ArchivedAt.IsZero() {
		archivedAt := row.ArchivedAt
		info.ArchivedAt = &archivedAt
	}
	return info
}

func notesFromRow(row *statedb.InstanceRow) string {
	if row == nil {
		return ""
	}
	_, _, _, _, _, _, _, _, _, _, _, notes, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := statedb.UnmarshalToolData(row.ToolData)
	return notes
}

func canForkFromRow(row *statedb.InstanceRow) bool {
	if row == nil {
		return false
	}
	claudeSessionID, claudeDetectedAt,
		_, _, _, _,
		openCodeSessionID, openCodeDetectedAt,
		codexSessionID, codexDetectedAt,
		_, _, _, _,
		sandboxJSON, _,
		sshHost, _,
		_, _, _, _, _, _, _, _, _, _ := statedb.UnmarshalToolData(row.ToolData)
	proxy := &session.Instance{
		ID:                 row.ID,
		Tool:               row.Tool,
		ProjectPath:        row.ProjectPath,
		ClaudeSessionID:    claudeSessionID,
		ClaudeDetectedAt:   claudeDetectedAt,
		OpenCodeSessionID:  openCodeSessionID,
		OpenCodeDetectedAt: openCodeDetectedAt,
		CodexSessionID:     codexSessionID,
		CodexDetectedAt:    codexDetectedAt,
		SSHHost:            sshHost,
	}
	if len(sandboxJSON) > 0 {
		_ = json.Unmarshal(sandboxJSON, &proxy.Sandbox)
	}
	return proxy.CanFork()
}

func displaySessionIDFromRow(row *statedb.InstanceRow) string {
	claudeSessionID, _,
		geminiSessionID, _, _, _,
		openCodeSessionID, _,
		codexSessionID, _,
		_, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := statedb.UnmarshalToolData(row.ToolData)
	kiroSessionID, _ := statedb.ReadKiroSessionBindingFromToolData(row.ToolData)
	return displaySessionIDFromParts(row.Tool, claudeSessionID, geminiSessionID, openCodeSessionID, codexSessionID, kiroSessionID)
}

func displaySessionIDFromParts(tool, claudeSessionID, geminiSessionID, openCodeSessionID, codexSessionID, kiroSessionID string) string {
	proxy := &session.Instance{
		Tool:              tool,
		ClaudeSessionID:   claudeSessionID,
		GeminiSessionID:   geminiSessionID,
		OpenCodeSessionID: openCodeSessionID,
		CodexSessionID:    codexSessionID,
		KiroSessionID:     kiroSessionID,
	}
	return proxy.DisplaySessionID()
}

func rowUpdatedAt(row *statedb.InstanceRow) *time.Time {
	switch {
	case !row.LastAccessed.IsZero():
		t := row.LastAccessed
		return &t
	case !row.CreatedAt.IsZero():
		t := row.CreatedAt
		return &t
	default:
		return nil
	}
}

func (c *Client) normalizedConfig() (ClientConfig, error) {
	cfg := c.cfg
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.NodeID = strings.TrimSpace(cfg.NodeID)
	cfg.NodeName = strings.TrimSpace(cfg.NodeName)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.Version = strings.TrimSpace(cfg.Version)
	cfg.CAPemFile = strings.TrimSpace(cfg.CAPemFile)
	cfg.ServerName = strings.TrimSpace(cfg.ServerName)
	cfg.PinnedCertSHA256 = strings.TrimSpace(cfg.PinnedCertSHA256)
	if cfg.URL == "" {
		return ClientConfig{}, fmt.Errorf("hub URL is required")
	}
	if !strings.HasPrefix(strings.ToLower(cfg.URL), "wss://") {
		return ClientConfig{}, fmt.Errorf("hub client requires wss://; use TLS even for local deployments")
	}
	if cfg.NodeID == "" {
		return ClientConfig{}, fmt.Errorf("hub node id is required")
	}
	if cfg.Token == "" {
		return ClientConfig{}, fmt.Errorf("hub node token is required")
	}
	return cfg, nil
}

func nodeWebSocketURL(rawURL, nodeID string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse hub URL: %w", err)
	}
	if strings.ToLower(u.Scheme) != "wss" {
		return "", fmt.Errorf("hub client requires wss://; use TLS even for local deployments")
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("hub URL host is required")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws/node"
	}
	q := u.Query()
	q.Set("node_id", strings.TrimSpace(nodeID))
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String(), nil
}

func clientTLSConfig(cfg ClientConfig) (*tls.Config, error) {
	if strings.TrimSpace(cfg.PinnedCertSHA256) != "" {
		pinned := strings.TrimSpace(cfg.PinnedCertSHA256)
		tlsConfig := &tls.Config{}
		// #nosec G402 -- certificate chain validation is replaced by exact SHA-256 pin validation below.
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			rawCerts := make([][]byte, 0, len(state.PeerCertificates))
			for _, cert := range state.PeerCertificates {
				rawCerts = append(rawCerts, cert.Raw)
			}
			return VerifyPinnedCertificate(rawCerts, pinned)
		}
		return tlsConfig, nil
	}
	tlsConfig := &tls.Config{
		ServerName: cfg.ServerName,
	}
	if cfg.TLSSkipVerify {
		// #nosec G402 -- explicit user-configured option for private/self-managed hubs.
		tlsConfig.InsecureSkipVerify = true
	}
	if cfg.CAPemFile == "" {
		return tlsConfig, nil
	}
	pemData, err := os.ReadFile(cfg.CAPemFile)
	if err != nil {
		return nil, fmt.Errorf("read hub CA PEM file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("no certificates found in hub CA PEM file")
	}
	tlsConfig.RootCAs = pool
	return tlsConfig, nil
}

func (cfg ClientConfig) heartbeatInterval() time.Duration {
	if cfg.HeartbeatInterval > 0 {
		return cfg.HeartbeatInterval
	}
	return 30 * time.Second
}

func (cfg ClientConfig) snapshotInterval() time.Duration {
	if cfg.SnapshotInterval > 0 {
		return cfg.SnapshotInterval
	}
	return 15 * time.Second
}

func (cfg ClientConfig) reconnectBaseDelay() time.Duration {
	if cfg.ReconnectBaseDelay > 0 {
		return cfg.ReconnectBaseDelay
	}
	return time.Second
}

func (cfg ClientConfig) reconnectMaxDelay() time.Duration {
	if cfg.ReconnectMaxDelay > 0 {
		return cfg.ReconnectMaxDelay
	}
	return 30 * time.Second
}
