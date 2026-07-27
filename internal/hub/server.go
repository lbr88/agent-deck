package hub

import (
	"context"
	"database/sql"
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
	ListenAddr   string
	DataDir      string
	CertFile     string
	KeyFile      string
	AdvertiseURL string
}

type Server struct {
	cfg               ServerConfig
	store             *Store
	httpServer        *http.Server
	shutdownRequested bool
	mu                sync.Mutex
	nodeConnections   map[string]int
	peers             map[*hubPeer]struct{}
	attachRouter      *AttachRouter
	commandRoutes     map[string]commandRoute
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
	cfg.AdvertiseURL = strings.TrimSpace(cfg.AdvertiseURL)
	if cfg.AdvertiseURL != "" {
		if !strings.HasPrefix(strings.ToLower(cfg.AdvertiseURL), "wss://") {
			_ = store.Close()
			return nil, fmt.Errorf("hub advertise URL requires wss://; use TLS even for local deployments")
		}
		if err := store.SetAdvertiseURL(cfg.AdvertiseURL); err != nil {
			_ = store.Close()
			return nil, err
		}
	}
	return &Server{cfg: cfg, store: store, attachRouter: NewAttachRouter()}, nil
}

func (s *Store) Nodes() ([]Node, error) {
	rows, err := s.db.Query(
		`SELECT id, name, token_hash, version, os, arch, status, last_seen_at, admin
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

// Shutdown gracefully stops accepting hub traffic and closes connected node
// WebSockets so clients immediately enter their normal reconnect loop. It is
// used by the runtime update handoff before the process execs the replacement
// binary. The store remains open until Close, allowing Serve's caller and
// deferred cleanup to finish in their normal order.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	s.shutdownRequested = true
	httpServer := s.httpServer
	peers := make([]*hubPeer, 0, len(s.peers))
	for peer := range s.peers {
		peers = append(peers, peer)
	}
	s.mu.Unlock()

peerLoop:
	for _, peer := range peers {
		select {
		case <-ctx.Done():
			break peerLoop
		default:
		}
		if peer == nil || peer.conn == nil {
			continue
		}
		deadline := time.Now().Add(time.Second)
		if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
			deadline = contextDeadline
		}
		// Gorilla permits Close and WriteControl concurrently with data writes,
		// so do not wait on peer.mu: a stuck data writer must not make update
		// handoff exceed the caller's shutdown deadline.
		_ = peer.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseServiceRestart, "agent-deck update"),
			deadline,
		)
		_ = peer.conn.Close()
	}
	if httpServer == nil {
		return nil
	}
	if err := httpServer.Shutdown(ctx); err != nil {
		_ = httpServer.Close()
		return err
	}
	return nil
}

func (s *Server) Serve() error {
	if strings.TrimSpace(s.cfg.CertFile) == "" || strings.TrimSpace(s.cfg.KeyFile) == "" {
		return fmt.Errorf("agent-deck hub serve requires --tls-cert and --tls-key")
	}
	httpServer := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.mu.Lock()
	if s.shutdownRequested {
		s.mu.Unlock()
		return nil
	}
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
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/invites", s.handleInvites)
	mux.HandleFunc("/api/invites/revoke", s.handleRevokeInvite)
	mux.HandleFunc("/api/nodes", s.handleListNodes)
	mux.HandleFunc("/api/nodes/promote", s.handlePromoteNode)
	mux.HandleFunc("/api/nodes/demote", s.handleDemoteNode)
	mux.HandleFunc("/api/nodes/rename", s.handleRenameNode)
	mux.HandleFunc("/api/nodes/revoke", s.handleRevokeNode)
	mux.HandleFunc("/api/trust/pending", s.handlePendingTrust)
	mux.HandleFunc("/api/trust/allow", s.handleAllowTrust)
	mux.HandleFunc("/api/trust/deny", s.handleDenyTrust)
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
	node, err := s.store.UpsertNodeWithAdmin(nodeID, nodeName, hashSecret(nodeToken), req.Version, req.OS, req.Arch, invite.Admin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	trustRequests, err := s.store.CreatePendingTrustRequestsForNewNode(node.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, joinResponse{
		URL:       s.hubURLForRequest(r),
		NodeID:    node.ID,
		NodeName:  node.Name,
		NodeToken: nodeToken,
	})
	s.notifyTrustRequests(trustRequests)
}

type createInviteRequest struct {
	NodeName   string `json:"node_name"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
	Admin      bool   `json:"admin,omitempty"`
}

type createInviteResponse struct {
	URL         string    `json:"url"`
	InviteToken string    `json:"invite_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	node, err := s.authenticateNodeRequest(r)
	if err != nil {
		http.Error(w, ErrNodeNotAuthenticated.Error(), http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{
		URL:  s.hubURLForRequest(r),
		Node: nodeResponseFromNode(node),
	})
}

func (s *Server) handleInvites(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListInvites(w, r)
	case http.MethodPost:
		s.handleCreateInvite(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	node, err := s.authenticateNodeRequest(r)
	if err != nil {
		http.Error(w, ErrNodeNotAuthenticated.Error(), http.StatusUnauthorized)
		return
	}
	if !node.Admin {
		http.Error(w, "hub admin node is required", http.StatusForbidden)
		return
	}

	var req createInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid invite request JSON", http.StatusBadRequest)
		return
	}
	req.NodeName = strings.TrimSpace(req.NodeName)
	if req.NodeName == "" {
		http.Error(w, "node_name is required", http.StatusBadRequest)
		return
	}
	ttl := 24 * time.Hour
	if req.TTLSeconds != 0 {
		if req.TTLSeconds < 0 {
			http.Error(w, "ttl_seconds must be greater than zero", http.StatusBadRequest)
			return
		}
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	expiresAt := time.Now().Add(ttl)
	token, err := s.store.CreateInviteWithOptions(CreateInviteOptions{
		NodeName:        req.NodeName,
		TTL:             ttl,
		Admin:           req.Admin,
		CreatedByNodeID: node.ID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, createInviteResponse{
		URL:         s.hubURLForRequest(r),
		InviteToken: token,
		ExpiresAt:   expiresAt,
	})
}

type statusResponse struct {
	URL  string       `json:"url"`
	Node nodeResponse `json:"node"`
}

type listNodesResponse struct {
	Nodes []nodeResponse `json:"nodes"`
}

type listInvitesResponse struct {
	Invites []inviteResponse `json:"invites"`
}

type inviteResponse struct {
	ID              string     `json:"id,omitempty"`
	NodeName        string     `json:"node_name"`
	ExpiresAt       time.Time  `json:"expires_at"`
	ConsumedAt      *time.Time `json:"consumed_at,omitempty"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	Admin           bool       `json:"admin"`
	CreatedByNodeID string     `json:"created_by_node_id,omitempty"`
	Status          string     `json:"status"`
}

type promoteNodeRequest struct {
	NodeID string `json:"node_id"`
}

type renameNodeRequest struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
}

type revokeInviteRequest struct {
	InviteID string `json:"invite_id"`
	Token    string `json:"token,omitempty"`
}

type nodeResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	OS         string     `json:"os,omitempty"`
	Arch       string     `json:"arch,omitempty"`
	Status     string     `json:"status"`
	Admin      bool       `json:"admin"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

type trustRequestsResponse struct {
	Requests []TrustRequestPayload `json:"requests"`
}

type trustDecisionRequest struct {
	NodeID string `json:"node_id"`
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateAdminNodeRequest(w, r) {
		return
	}
	invites, err := s.store.Invites()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, listInvitesResponse{Invites: inviteResponses(invites)})
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	if !s.authenticateAdminNodeRequest(w, r) {
		return
	}
	var req revokeInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid invite revoke request JSON", http.StatusBadRequest)
		return
	}
	identifier := strings.TrimSpace(req.InviteID)
	if identifier == "" {
		identifier = strings.TrimSpace(req.Token)
	}
	if identifier == "" {
		http.Error(w, "invite_id or token is required", http.StatusBadRequest)
		return
	}
	if err := s.store.RevokeInvite(identifier); err != nil {
		if errors.Is(err, ErrInviteNotFound) {
			http.Error(w, "hub invite not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.authenticateAdminNodeRequest(w, r) {
		return
	}
	nodes, err := s.store.Nodes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, listNodesResponse{Nodes: nodeResponses(nodes)})
}

func (s *Server) handlePromoteNode(w http.ResponseWriter, r *http.Request) {
	s.handleSetNodeAdmin(w, r, true)
}

func (s *Server) handleDemoteNode(w http.ResponseWriter, r *http.Request) {
	s.handleSetNodeAdmin(w, r, false)
}

func (s *Server) handleSetNodeAdmin(w http.ResponseWriter, r *http.Request, admin bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	if !s.authenticateAdminNodeRequest(w, r) {
		return
	}
	var req promoteNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid node admin request JSON", http.StatusBadRequest)
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	if req.NodeID == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}
	if !admin {
		if err := s.ensureCanRemoveAdmin(req.NodeID); err != nil {
			writeAdminRemovalError(w, err)
			return
		}
	}
	if err := s.store.SetNodeAdmin(req.NodeID, admin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "hub node not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	node, err := s.store.nodeByID(req.NodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyNodeMetadataChanged(node)
	writeJSON(w, http.StatusOK, nodeResponseFromNode(node))
}

func (s *Server) handleRenameNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	if !s.authenticateAdminNodeRequest(w, r) {
		return
	}
	var req renameNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid node rename request JSON", http.StatusBadRequest)
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	req.Name = strings.TrimSpace(req.Name)
	if req.NodeID == "" || req.Name == "" {
		http.Error(w, "node_id and name are required", http.StatusBadRequest)
		return
	}
	node, err := s.store.RenameNode(req.NodeID, req.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "hub node not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyNodeMetadataChanged(node)
	writeJSON(w, http.StatusOK, nodeResponseFromNode(node))
}

func (s *Server) handleRevokeNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	if !s.authenticateAdminNodeRequest(w, r) {
		return
	}
	var req promoteNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid node revoke request JSON", http.StatusBadRequest)
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	if req.NodeID == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}
	if err := s.ensureCanRemoveAdmin(req.NodeID); err != nil {
		writeAdminRemovalError(w, err)
		return
	}
	if err := s.store.RevokeNode(req.NodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "hub node not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	s.disconnectNode(req.NodeID)
}

var errLastAdmin = errors.New("cannot remove the last hub admin")

func (s *Server) ensureCanRemoveAdmin(nodeID string) error {
	node, err := s.store.nodeByID(nodeID)
	if err != nil {
		return err
	}
	if !node.Admin {
		return nil
	}
	count, err := s.store.AdminNodeCount()
	if err != nil {
		return err
	}
	if count <= 1 {
		return errLastAdmin
	}
	return nil
}

func writeAdminRemovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errLastAdmin):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "hub node not found", http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) authenticateAdminNodeRequest(w http.ResponseWriter, r *http.Request) bool {
	node, err := s.authenticateNodeRequest(r)
	if err != nil {
		http.Error(w, ErrNodeNotAuthenticated.Error(), http.StatusUnauthorized)
		return false
	}
	if !node.Admin {
		http.Error(w, "hub admin node is required", http.StatusForbidden)
		return false
	}
	return true
}

func inviteResponses(invites []Invite) []inviteResponse {
	out := make([]inviteResponse, 0, len(invites))
	now := time.Now()
	for _, invite := range invites {
		out = append(out, inviteResponse{
			ID:              invite.ID,
			NodeName:        invite.NodeName,
			ExpiresAt:       invite.ExpiresAt,
			ConsumedAt:      invite.ConsumedAt,
			RevokedAt:       invite.RevokedAt,
			Admin:           invite.Admin,
			CreatedByNodeID: invite.CreatedByNodeID,
			Status:          invite.Status(now),
		})
	}
	return out
}

func nodeResponses(nodes []Node) []nodeResponse {
	out := make([]nodeResponse, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, nodeResponseFromNode(node))
	}
	return out
}

func nodeResponseFromNode(node Node) nodeResponse {
	return nodeResponse{
		ID:         node.ID,
		Name:       node.Name,
		Version:    node.Version,
		OS:         node.OS,
		Arch:       node.Arch,
		Status:     node.Status,
		Admin:      node.Admin,
		LastSeenAt: node.LastSeenAt,
	}
}

func (s *Server) handlePendingTrust(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	node, err := s.authenticateNodeRequest(r)
	if err != nil {
		http.Error(w, ErrNodeNotAuthenticated.Error(), http.StatusUnauthorized)
		return
	}
	requests, err := s.store.PendingTrustRequests(node.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, trustRequestsResponse{Requests: trustRequestPayloads(requests)})
}

func (s *Server) handleAllowTrust(w http.ResponseWriter, r *http.Request) {
	s.handleSetTrustDecision(w, r, true)
}

func (s *Server) handleDenyTrust(w http.ResponseWriter, r *http.Request) {
	s.handleSetTrustDecision(w, r, false)
}

func (s *Server) handleSetTrustDecision(w http.ResponseWriter, r *http.Request, allow bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	owner, err := s.authenticateNodeRequest(r)
	if err != nil {
		http.Error(w, ErrNodeNotAuthenticated.Error(), http.StatusUnauthorized)
		return
	}
	var req trustDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid trust decision request JSON", http.StatusBadRequest)
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	if req.NodeID == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}
	if err := s.setTrustDecision(owner.ID, req.NodeID, allow); err != nil {
		if errors.Is(err, ErrNodeNotFound) {
			http.Error(w, "hub node not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func trustRequestPayloads(requests []TrustRequest) []TrustRequestPayload {
	out := make([]TrustRequestPayload, 0, len(requests))
	for _, request := range requests {
		out = append(out, trustRequestPayload(request))
	}
	return out
}

func trustRequestPayload(request TrustRequest) TrustRequestPayload {
	return TrustRequestPayload{
		NodeID:   request.Requester.ID,
		NodeName: request.Requester.Name,
		Version:  request.Requester.Version,
		OS:       request.Requester.OS,
		Arch:     request.Requester.Arch,
		Status:   string(request.Status),
	}
}

func (s *Server) handleNodeWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	node, err := s.authenticateNodeRequest(r)
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
		Admin:    node.Admin,
	})
	if err == nil {
		_ = s.writePeerJSON(peer, welcome)
	}
	if err := s.sendPendingTrustRequests(peer, node.ID); err != nil {
		return
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

func (s *Server) authenticateNodeRequest(r *http.Request) (Node, error) {
	nodeID := strings.TrimSpace(r.URL.Query().Get("node_id"))
	token := bearerToken(r.Header.Get("Authorization"))
	if nodeID == "" || token == "" {
		return Node{}, ErrNodeNotAuthenticated
	}
	return s.store.AuthenticateNode(nodeID, token)
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
		currentNode, err := s.store.nodeByID(node.ID)
		if err != nil {
			return fmt.Errorf("load current snapshot node: %w", err)
		}
		snapshot.NodeID = currentNode.ID
		snapshot.NodeName = currentNode.Name
		snapshot.Admin = currentNode.Admin
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
		allowed, err := s.canPeerAccessNode(peer, payload.NodeID)
		if err != nil {
			return err
		}
		if !allowed {
			return s.sendAttachTrustDenied(peer, payload)
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
	case MsgTrustDecision:
		var payload TrustDecisionPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return fmt.Errorf("decode trust decision: %w", err)
		}
		return s.handleTrustDecisionFromPeer(peer, payload)
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

func (s *Server) disconnectNode(nodeID string) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return
	}
	s.mu.Lock()
	peers := make([]*hubPeer, 0)
	for peer := range s.peers {
		if peer.nodeID == nodeID {
			peers = append(peers, peer)
		}
	}
	s.mu.Unlock()
	for _, peer := range peers {
		if peer != nil && peer.conn != nil {
			_ = peer.conn.Close()
		}
	}
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
	allowed, err := s.canRequesterAccessNode(requester.NodeID(), targetNodeID)
	if err != nil {
		return err
	}
	if !allowed {
		err := fmt.Errorf("hub trust is required for node %q", targetNodeID)
		s.sendCommandFailure(requester, targetNodeID, commandID, err)
		return err
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

func (s *Server) canPeerAccessNode(peer *hubPeer, ownerNodeID string) (bool, error) {
	if peer == nil {
		return false, nil
	}
	return s.canRequesterAccessNode(peer.nodeID, ownerNodeID)
}

func (s *Server) canRequesterAccessNode(requesterNodeID, ownerNodeID string) (bool, error) {
	requesterNodeID = strings.TrimSpace(requesterNodeID)
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	if requesterNodeID == "" || ownerNodeID == "" {
		return false, nil
	}
	if s.store == nil {
		return true, nil
	}
	return s.store.CanAccessNode(ownerNodeID, requesterNodeID)
}

func (s *Server) sendAttachTrustDenied(peer *hubPeer, payload AttachOpenPayload) error {
	reason := fmt.Sprintf("hub trust is required for node %q", strings.TrimSpace(payload.NodeID))
	env, err := MarshalEnvelope(MsgAttachClosed, payload.NodeID, AttachClosePayload{
		StreamID: payload.StreamID,
		Reason:   reason,
	})
	if err != nil {
		return err
	}
	_ = s.writePeerJSON(peer, env)
	return nil
}

func (s *Server) sendPendingTrustRequests(peer *hubPeer, ownerNodeID string) error {
	if s.store == nil {
		return nil
	}
	requests, err := s.store.PendingTrustRequests(ownerNodeID)
	if err != nil {
		return err
	}
	for _, request := range requests {
		env, err := MarshalEnvelope(MsgTrustRequest, ownerNodeID, trustRequestPayload(request))
		if err != nil {
			return err
		}
		if err := s.writePeerJSON(peer, env); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) notifyTrustRequests(requests []TrustRequest) {
	for _, request := range requests {
		env, err := MarshalEnvelope(MsgTrustRequest, request.Owner.ID, trustRequestPayload(request))
		if err != nil {
			continue
		}
		for _, peer := range s.connectedPeersForNode(request.Owner.ID) {
			_ = s.writePeerJSON(peer, env)
		}
	}
}

func (s *Server) handleTrustDecisionFromPeer(peer *hubPeer, payload TrustDecisionPayload) error {
	if peer == nil {
		return fmt.Errorf("trust decision peer is required")
	}
	requesterNodeID := strings.TrimSpace(payload.NodeID)
	if requesterNodeID == "" {
		return fmt.Errorf("trust decision node_id is required")
	}
	return s.setTrustDecision(peer.nodeID, requesterNodeID, payload.Allow)
}

func (s *Server) setTrustDecision(ownerNodeID, requesterNodeID string, allow bool) error {
	var err error
	if allow {
		err = s.store.AllowTrust(ownerNodeID, requesterNodeID)
	} else {
		err = s.store.DenyTrust(ownerNodeID, requesterNodeID)
	}
	if err != nil {
		return err
	}
	if allow {
		s.sendLatestSnapshotToNode(ownerNodeID, requesterNodeID)
	} else {
		s.sendClearSnapshotToNode(ownerNodeID, requesterNodeID)
	}
	return nil
}

func (s *Server) sendLatestSnapshotToNode(ownerNodeID, requesterNodeID string) {
	if s.store == nil {
		return
	}
	snapshots, err := s.store.LatestSessions()
	if err != nil {
		return
	}
	for _, latest := range snapshots {
		if latest.Node.ID != ownerNodeID {
			continue
		}
		env, err := s.snapshotEnvelope(latest, true)
		if err != nil {
			return
		}
		for _, peer := range s.connectedPeersForNode(requesterNodeID) {
			_ = s.writePeerJSON(peer, env)
		}
		return
	}
}

func (s *Server) sendClearSnapshotToNode(ownerNodeID, requesterNodeID string) {
	if s.store == nil {
		return
	}
	snapshots, err := s.store.LatestSessions()
	if err != nil {
		return
	}
	for _, latest := range snapshots {
		if latest.Node.ID != ownerNodeID {
			continue
		}
		env, err := s.snapshotEnvelope(latest, false)
		if err != nil {
			return
		}
		for _, peer := range s.connectedPeersForNode(requesterNodeID) {
			_ = s.writePeerJSON(peer, env)
		}
		return
	}
}

func (s *Server) connectedPeersForNode(nodeID string) []*hubPeer {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	peers := make([]*hubPeer, 0)
	for peer := range s.peers {
		if peer.nodeID == nodeID {
			peers = append(peers, peer)
		}
	}
	return peers
}

func (s *Server) sendLatestSnapshots(peer *hubPeer) error {
	snapshots, err := s.store.LatestSessions()
	if err != nil {
		return err
	}
	for _, latest := range snapshots {
		allowed, err := s.canRequesterAccessNode(peer.nodeID, latest.Node.ID)
		if err != nil {
			return err
		}
		env, err := s.snapshotEnvelope(latest, allowed)
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
	allowedEnv, err := MarshalEnvelope(MsgSnapshot, originNodeID, snapshot)
	if err != nil {
		return
	}
	clearEnv, err := MarshalEnvelope(MsgSnapshot, originNodeID, SnapshotPayload{
		NodeID:   snapshot.NodeID,
		NodeName: snapshot.NodeName,
		Admin:    snapshot.Admin,
		SentAt:   snapshot.SentAt,
		Sessions: []SessionInfo{},
	})
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
		allowed, err := s.canRequesterAccessNode(peer.nodeID, originNodeID)
		if err != nil {
			continue
		}
		if allowed {
			_ = s.writePeerJSON(peer, allowedEnv)
			continue
		}
		_ = s.writePeerJSON(peer, clearEnv)
	}
}

func (s *Server) snapshotEnvelope(latest NodeSessions, includeSessions bool) (Envelope, error) {
	payload := SnapshotPayload{
		NodeID:       latest.Node.ID,
		NodeName:     latest.Node.Name,
		Admin:        latest.Node.Admin,
		SentAt:       latest.SentAt,
		WebAvailable: latest.WebAvailable,
		Sessions:     []SessionInfo{},
	}
	if includeSessions {
		payload.Sessions = append([]SessionInfo(nil), latest.Sessions...)
		payload.Groups = append([]GroupInfo(nil), latest.Groups...)
	}
	return MarshalEnvelope(MsgSnapshot, latest.Node.ID, payload)
}

// notifyNodeMetadataChanged pushes the hub registry's authoritative short name
// and role immediately. Connected clients keep their join-time NodeName in
// memory, so waiting for their next self-reported snapshot would otherwise
// reintroduce the old name after a rename.
func (s *Server) notifyNodeMetadataChanged(node Node) {
	node.ID = strings.TrimSpace(node.ID)
	node.Name = strings.TrimSpace(node.Name)
	if node.ID == "" {
		return
	}
	welcome, err := MarshalEnvelope(MsgWelcome, node.ID, WelcomePayload{
		NodeID:   node.ID,
		NodeName: node.Name,
		Admin:    node.Admin,
	})
	if err == nil {
		for _, peer := range s.connectedPeersForNode(node.ID) {
			_ = s.writePeerJSON(peer, welcome)
		}
	}

	snapshots, err := s.store.LatestSessions()
	if err != nil {
		return
	}
	for _, latest := range snapshots {
		if latest.Node.ID != node.ID {
			continue
		}
		s.broadcastSnapshot(node.ID, SnapshotPayload{
			NodeID:       node.ID,
			NodeName:     node.Name,
			Admin:        node.Admin,
			SentAt:       latest.SentAt,
			WebAvailable: latest.WebAvailable,
			Sessions:     append([]SessionInfo(nil), latest.Sessions...),
			Groups:       append([]GroupInfo(nil), latest.Groups...),
		})
		return
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

func (s *Server) hubURLForRequest(r *http.Request) string {
	if s != nil {
		if rawURL := strings.TrimSpace(s.cfg.AdvertiseURL); rawURL != "" {
			return rawURL
		}
		if s.store != nil {
			if rawURL, err := s.store.AdvertiseURL(); err == nil && strings.TrimSpace(rawURL) != "" {
				return strings.TrimSpace(rawURL)
			}
		}
	}
	return hubURLForRequest(r, s.cfg.ListenAddr)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
