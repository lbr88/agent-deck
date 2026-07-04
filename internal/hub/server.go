package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ServerConfig struct {
	ListenAddr string
	DataDir    string
	CertFile   string
	KeyFile    string
}

type Server struct {
	cfg             ServerConfig
	store           *Store
	httpServer      *http.Server
	mu              sync.Mutex
	nodeConnections map[string]int
	peers           map[*hubPeer]struct{}
	attachRouter    *AttachRouter
	commandRoutes   map[string]commandRoute
}

type hubPeer struct {
	id     string
	nodeID string
	conn   *websocket.Conn
	mu     sync.Mutex
}

type commandRoute struct {
	requesterPeer   Peer
	ownerPeer       Peer
	requesterNodeID string
	ownerNodeID     string
}

func (p *hubPeer) NodeID() string {
	if p == nil {
		return ""
	}
	return p.nodeID
}

func (p *hubPeer) PeerID() string {
	if p == nil {
		return ""
	}
	return p.id
}

func (p *hubPeer) Send(env Envelope) error {
	if p == nil || p.conn == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return writeWebSocketJSON(p.conn, env)
}

func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:8421"
	}
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("hub data dir is required")
	}
	store, err := OpenStore(filepath.Join(cfg.DataDir, "hub.db"))
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, store: store, attachRouter: NewAttachRouter()}, nil
}

func (s *Store) Nodes() ([]Node, error) {
	rows, err := s.db.Query(
		`SELECT id, name, token_hash, version, os, arch, status, last_seen_at
		 FROM nodes
		 ORDER BY name, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		if err := scanNodeFields(rows, &node); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return nodes, nil
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	httpServer := s.httpServer
	s.httpServer = nil
	s.mu.Unlock()

	var closeErr error
	if httpServer != nil {
		closeErr = httpServer.Close()
	}
	if err := s.store.Close(); err != nil && closeErr == nil {
		closeErr = err
	}
	return closeErr
}

func (s *Server) Serve() error {
	if strings.TrimSpace(s.cfg.CertFile) == "" || strings.TrimSpace(s.cfg.KeyFile) == "" {
		return fmt.Errorf("agent-deck hub serve requires --tls-cert and --tls-key")
	}
	httpServer := &http.Server{
		Addr:    s.cfg.ListenAddr,
		Handler: s.Handler(),
	}
	s.mu.Lock()
	s.httpServer = httpServer
	s.mu.Unlock()

	err := httpServer.ListenAndServeTLS(s.cfg.CertFile, s.cfg.KeyFile)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/join", s.handleJoin)
	mux.HandleFunc("/ws/node", s.handleNodeWebSocket)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

type joinRequest struct {
	InviteToken string `json:"invite_token"`
	NodeName    string `json:"node_name,omitempty"`
	Version     string `json:"version,omitempty"`
	OS          string `json:"os,omitempty"`
	Arch        string `json:"arch,omitempty"`
}

type joinResponse struct {
	URL       string `json:"url"`
	NodeID    string `json:"node_id"`
	NodeName  string `json:"node_name"`
	NodeToken string `json:"node_token"`
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid join request JSON", http.StatusBadRequest)
		return
	}
	req.InviteToken = strings.TrimSpace(req.InviteToken)
	if req.InviteToken == "" {
		http.Error(w, "invite token is required", http.StatusBadRequest)
		return
	}

	invite, err := s.store.ConsumeInvite(req.InviteToken)
	if err != nil {
		status := http.StatusUnauthorized
		if !errors.Is(err, ErrInviteInvalid) {
			status = http.StatusInternalServerError
		}
		http.Error(w, err.Error(), status)
		return
	}

	nodeName := strings.TrimSpace(invite.NodeName)
	if nodeName == "" {
		nodeName = strings.TrimSpace(req.NodeName)
	}
	if nodeName == "" {
		nodeName = "node"
	}

	nodeID, err := newSecret("node_")
	if err != nil {
		http.Error(w, "failed to create node id", http.StatusInternalServerError)
		return
	}
	nodeToken, err := newSecret("node_token_")
	if err != nil {
		http.Error(w, "failed to create node token", http.StatusInternalServerError)
		return
	}
	node, err := s.store.UpsertNode(nodeID, nodeName, hashSecret(nodeToken), req.Version, req.OS, req.Arch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, joinResponse{
		URL:       hubURLForRequest(r, s.cfg.ListenAddr),
		NodeID:    node.ID,
		NodeName:  node.Name,
		NodeToken: nodeToken,
	})
}

func (s *Server) handleNodeWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodeID := strings.TrimSpace(r.URL.Query().Get("node_id"))
	token := bearerToken(r.Header.Get("Authorization"))
	if nodeID == "" || token == "" {
		http.Error(w, "node credentials are required", http.StatusUnauthorized)
		return
	}
	node, err := s.store.AuthenticateNode(nodeID, token)
	if err != nil {
		http.Error(w, ErrNodeNotAuthenticated.Error(), http.StatusUnauthorized)
		return
	}

	conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(maxHubEnvelopeBytes)

	peerID, err := newSecret("peer_")
	if err != nil {
		return
	}
	peer := &hubPeer{id: peerID, nodeID: node.ID, conn: conn}
	s.retainNodeConnection(node.ID, peer)
	defer s.releaseNodeConnection(node.ID, peer)

	welcome, err := MarshalEnvelope(MsgWelcome, node.ID, WelcomePayload{
		NodeID:   node.ID,
		NodeName: node.Name,
	})
	if err == nil {
		_ = s.writePeerJSON(peer, welcome)
	}
	if err := s.sendLatestSnapshots(peer); err != nil {
		return
	}
	for {
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return
		}
		if err := s.handleNodeEnvelope(r.Context(), node, peer, env); err != nil {
			errEnv, marshalErr := MarshalEnvelope(MsgError, node.ID, ErrorPayload{Message: err.Error()})
			if marshalErr == nil {
				_ = s.writePeerJSON(peer, errEnv)
			}
		}
	}
}

func (s *Server) handleNodeEnvelope(ctx context.Context, node Node, peer *hubPeer, env Envelope) error {
	if env.Version != 0 && env.Version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %d", env.Version)
	}
	switch env.Type {
	case MsgHello, MsgHeartbeat:
		return nil
	case MsgSnapshot:
		var snapshot SnapshotPayload
		if err := json.Unmarshal(env.Payload, &snapshot); err != nil {
			return fmt.Errorf("decode snapshot: %w", err)
		}
		if snapshot.SentAt.IsZero() {
			snapshot.SentAt = time.Now().UTC()
		}
		snapshot.NodeID = node.ID
		snapshot.NodeName = node.Name
		if err := s.store.ReplaceSnapshot(node.ID, snapshot); err != nil {
			return err
		}
		s.broadcastSnapshot(node.ID, snapshot)
		return nil
	case MsgAttachOpen:
		var payload AttachOpenPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode attach open: %w", err)
		}
		return s.attachRouter.OpenFromPeer(ctx, peer, payload.NodeID, payload)
	case MsgAttachReady:
		var payload AttachOpenPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode attach ready: %w", err)
		}
		return s.attachRouter.ForwardReadyFromOwnerPeer(peer, payload)
	case MsgAttachData:
		var payload AttachDataPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode attach data: %w", err)
		}
		return s.attachRouter.ForwardDataFromPeer(peer, payload)
	case MsgAttachResize:
		var payload AttachResizePayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode attach resize: %w", err)
		}
		return s.attachRouter.ForwardResizeFromRequesterPeer(peer, payload)
	case MsgAttachClose:
		var payload AttachClosePayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode attach close: %w", err)
		}
		return s.attachRouter.ForwardCloseFromRequesterPeer(peer, payload)
	case MsgAttachClosed:
		var payload AttachClosePayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode attach closed: %w", err)
		}
		return s.attachRouter.ForwardClosedFromOwnerPeer(peer, payload)
	case MsgCommand:
		var payload CommandPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode command: %w", err)
		}
		return s.routeCommandFromPeer(ctx, peer, payload)
	case MsgCommandResult:
		var payload CommandResultPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode command result: %w", err)
		}
		return s.routeCommandResultFromPeer(peer, payload)
	default:
		return nil
	}
}

func (s *Server) retainNodeConnection(nodeID string, peer *hubPeer) {
	s.mu.Lock()
	if s.nodeConnections == nil {
		s.nodeConnections = make(map[string]int)
	}
	if s.peers == nil {
		s.peers = make(map[*hubPeer]struct{})
	}
	s.peers[peer] = struct{}{}
	if s.nodeConnections[nodeID] == 0 {
		_ = s.store.MarkNodeOnline(nodeID)
	}
	s.nodeConnections[nodeID]++
	s.mu.Unlock()
	if s.attachRouter != nil {
		s.attachRouter.Register(peer)
	}
}

func (s *Server) releaseNodeConnection(nodeID string, peer *hubPeer) {
	s.mu.Lock()
	delete(s.peers, peer)
	count := s.nodeConnections[nodeID]
	if count <= 1 {
		delete(s.nodeConnections, nodeID)
		_ = s.store.MarkNodeOffline(nodeID)
		s.mu.Unlock()
		if s.attachRouter != nil {
			s.attachRouter.Unregister(nodeID)
		}
		s.releaseCommandRoutesForPeer(peer)
		return
	}
	s.nodeConnections[nodeID] = count - 1
	var replacement *hubPeer
	for candidate := range s.peers {
		if candidate.nodeID == nodeID {
			replacement = candidate
			break
		}
	}
	s.mu.Unlock()
	if replacement != nil && s.attachRouter != nil {
		s.attachRouter.UnregisterPeer(peer)
		s.attachRouter.Register(replacement)
	} else if s.attachRouter != nil {
		s.attachRouter.UnregisterPeer(peer)
	}
	s.releaseCommandRoutesForPeer(peer)
}

func (s *Server) routeCommandFromPeer(ctx context.Context, requester Peer, payload CommandPayload) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	commandID := strings.TrimSpace(payload.CommandID)
	targetNodeID := strings.TrimSpace(payload.NodeID)
	if commandID == "" {
		return fmt.Errorf("command_id is required")
	}
	if targetNodeID == "" {
		return fmt.Errorf("command node_id is required")
	}
	if requester == nil || strings.TrimSpace(requester.NodeID()) == "" || strings.TrimSpace(requester.PeerID()) == "" {
		return fmt.Errorf("command requester peer is required")
	}

	if s.attachRouter == nil {
		return fmt.Errorf("command router is not configured")
	}
	owner := s.attachRouter.peer(targetNodeID)
	if owner == nil {
		err := fmt.Errorf("command owner node %q is not connected", targetNodeID)
		s.sendCommandFailure(requester, targetNodeID, commandID, err)
		return err
	}

	s.mu.Lock()
	if s.commandRoutes == nil {
		s.commandRoutes = make(map[string]commandRoute)
	}
	if _, exists := s.commandRoutes[commandID]; exists {
		s.mu.Unlock()
		err := fmt.Errorf("command %q already exists", commandID)
		s.sendCommandFailure(requester, targetNodeID, commandID, err)
		return err
	}
	s.commandRoutes[commandID] = commandRoute{
		requesterPeer:   requester,
		ownerPeer:       owner,
		requesterNodeID: requester.NodeID(),
		ownerNodeID:     targetNodeID,
	}
	s.mu.Unlock()

	payload.NodeID = targetNodeID
	env, err := MarshalEnvelope(MsgCommand, requester.NodeID(), payload)
	if err != nil {
		s.removeCommandRoute(commandID)
		return err
	}
	if err := owner.Send(env); err != nil {
		s.removeCommandRoute(commandID)
		s.sendCommandFailure(requester, targetNodeID, commandID, err)
		return err
	}
	return nil
}

func (s *Server) routeCommandResultFromPeer(owner Peer, payload CommandResultPayload) error {
	commandID := strings.TrimSpace(payload.CommandID)
	if commandID == "" {
		return fmt.Errorf("command_id is required")
	}
	if owner == nil || strings.TrimSpace(owner.NodeID()) == "" || strings.TrimSpace(owner.PeerID()) == "" {
		return fmt.Errorf("command owner peer is required")
	}

	s.mu.Lock()
	route, ok := s.commandRoutes[commandID]
	if ok && samePeer(owner, route.ownerPeer) {
		delete(s.commandRoutes, commandID)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("command %q is not open", commandID)
	}
	if !samePeer(owner, route.ownerPeer) {
		return fmt.Errorf("command %q does not belong to peer %q", commandID, owner.PeerID())
	}

	env, err := MarshalEnvelope(MsgCommandResult, owner.NodeID(), payload)
	if err != nil {
		return err
	}
	return route.requesterPeer.Send(env)
}

func (s *Server) removeCommandRoute(commandID string) {
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.commandRoutes, commandID)
}

func (s *Server) releaseCommandRoutesForPeer(peer Peer) {
	if peer == nil || strings.TrimSpace(peer.NodeID()) == "" || strings.TrimSpace(peer.PeerID()) == "" {
		return
	}
	type notification struct {
		peer Peer
		env  Envelope
	}
	var notifications []notification

	s.mu.Lock()
	for commandID, route := range s.commandRoutes {
		switch {
		case samePeer(route.requesterPeer, peer):
			delete(s.commandRoutes, commandID)
		case samePeer(route.ownerPeer, peer):
			if route.requesterPeer != nil {
				payload := CommandResultPayload{CommandID: commandID, OK: false, Error: "command owner disconnected"}
				if env, err := MarshalEnvelope(MsgCommandResult, peer.NodeID(), payload); err == nil {
					notifications = append(notifications, notification{peer: route.requesterPeer, env: env})
				}
			}
			delete(s.commandRoutes, commandID)
		}
	}
	s.mu.Unlock()

	for _, msg := range notifications {
		_ = msg.peer.Send(msg.env)
	}
}

func (s *Server) sendCommandFailure(requester Peer, ownerNodeID, commandID string, err error) {
	if requester == nil || err == nil {
		return
	}
	env, marshalErr := MarshalEnvelope(MsgCommandResult, ownerNodeID, CommandResultPayload{
		CommandID: commandID,
		OK:        false,
		Error:     err.Error(),
	})
	if marshalErr == nil {
		_ = requester.Send(env)
	}
}

func (s *Server) sendLatestSnapshots(peer *hubPeer) error {
	snapshots, err := s.store.LatestSessions()
	if err != nil {
		return err
	}
	for _, latest := range snapshots {
		payload := SnapshotPayload{
			NodeID:   latest.Node.ID,
			NodeName: latest.Node.Name,
			SentAt:   latest.SentAt,
			Sessions: append([]SessionInfo(nil), latest.Sessions...),
		}
		env, err := MarshalEnvelope(MsgSnapshot, latest.Node.ID, payload)
		if err != nil {
			return err
		}
		if err := s.writePeerJSON(peer, env); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) broadcastSnapshot(originNodeID string, snapshot SnapshotPayload) {
	env, err := MarshalEnvelope(MsgSnapshot, originNodeID, snapshot)
	if err != nil {
		return
	}

	s.mu.Lock()
	peers := make([]*hubPeer, 0, len(s.peers))
	for peer := range s.peers {
		if peer.nodeID == originNodeID {
			continue
		}
		peers = append(peers, peer)
	}
	s.mu.Unlock()

	for _, peer := range peers {
		_ = s.writePeerJSON(peer, env)
	}
}

func (s *Server) writePeerJSON(peer *hubPeer, v any) error {
	env, ok := v.(Envelope)
	if ok {
		return peer.Send(env)
	}
	if peer == nil || peer.conn == nil {
		return nil
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return writeWebSocketJSON(peer.conn, v)
}

var nodeWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func hubURLForRequest(r *http.Request, listenAddr string) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = strings.TrimSpace(listenAddr)
	}
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return "wss://" + host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
