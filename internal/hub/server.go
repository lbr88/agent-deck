package hub

import (
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
	return &Server{cfg: cfg, store: store}, nil
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

	s.retainNodeConnection(node.ID)
	defer s.releaseNodeConnection(node.ID)

	welcome, err := MarshalEnvelope(MsgWelcome, node.ID, WelcomePayload{
		NodeID:   node.ID,
		NodeName: node.Name,
	})
	if err == nil {
		_ = conn.WriteJSON(welcome)
	}
	for {
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return
		}
		if err := s.handleNodeEnvelope(node.ID, env); err != nil {
			errEnv, marshalErr := MarshalEnvelope(MsgError, node.ID, ErrorPayload{Message: err.Error()})
			if marshalErr == nil {
				_ = conn.WriteJSON(errEnv)
			}
		}
	}
}

func (s *Server) handleNodeEnvelope(nodeID string, env Envelope) error {
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
		return s.store.ReplaceSnapshot(nodeID, snapshot)
	case MsgCommand, MsgCommandResult, MsgAttachOpen, MsgAttachReady, MsgAttachData, MsgAttachResize, MsgAttachClose, MsgAttachClosed:
		return nil
	default:
		return nil
	}
}

func (s *Server) retainNodeConnection(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodeConnections == nil {
		s.nodeConnections = make(map[string]int)
	}
	if s.nodeConnections[nodeID] == 0 {
		_ = s.store.MarkNodeOnline(nodeID)
	}
	s.nodeConnections[nodeID]++
}

func (s *Server) releaseNodeConnection(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := s.nodeConnections[nodeID]
	if count <= 1 {
		delete(s.nodeConnections, nodeID)
		_ = s.store.MarkNodeOffline(nodeID)
		return
	}
	s.nodeConnections[nodeID] = count - 1
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
