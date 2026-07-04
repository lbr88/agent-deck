package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
	"github.com/gorilla/websocket"
)

type SessionSource interface {
	Snapshot(context.Context) (SnapshotPayload, error)
}

type ClientConfig struct {
	URL           string
	NodeID        string
	NodeName      string
	Token         string
	Version       string
	TLSSkipVerify bool
	CAPemFile     string
	ServerName    string

	HeartbeatInterval  time.Duration
	SnapshotInterval   time.Duration
	ReconnectBaseDelay time.Duration
	ReconnectMaxDelay  time.Duration

	OnStatus   func(string)
	OnSnapshot func(NodeSessions)
}

type Client struct {
	cfg    ClientConfig
	source SessionSource
}

func NewClient(cfg ClientConfig, source SessionSource) *Client {
	return &Client{cfg: cfg, source: source}
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
		err = c.connectOnce(ctx, cfg, wsURL, tlsConfig)
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

	closeOnCancel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closeOnCancel:
		}
	}()
	defer close(closeOnCancel)

	if err := writeEnvelope(conn, MsgHello, cfg.NodeID, NodeHelloPayload{
		NodeID:   cfg.NodeID,
		NodeName: cfg.NodeName,
		Version:  cfg.Version,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}); err != nil {
		return err
	}
	if err := c.publishSnapshot(ctx, conn, cfg.NodeID, cfg.NodeName); err != nil {
		return err
	}
	notifyClientStatus(cfg, "connected")

	readErr := make(chan error, 1)
	go func() {
		readErr <- c.readLoop(ctx, conn)
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
			if err := writeEnvelope(conn, MsgHeartbeat, cfg.NodeID, nil); err != nil {
				return err
			}
		case <-snapshotTicker.C:
			if err := c.publishSnapshot(ctx, conn, cfg.NodeID, cfg.NodeName); err != nil {
				return err
			}
		}
	}
}

func (c *Client) publishSnapshot(ctx context.Context, conn *websocket.Conn, nodeID, nodeName string) error {
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
	return writeEnvelope(conn, MsgSnapshot, nodeID, snapshot)
}

func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		c.dispatch(env)
	}
}

func (c *Client) dispatch(env Envelope) {
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
		})
	case MsgWelcome, MsgHeartbeat, MsgCommand, MsgAttachOpen, MsgAttachReady, MsgAttachData, MsgAttachResize, MsgAttachClose, MsgAttachClosed, MsgCommandResult, MsgError:
		return
	default:
		return
	}
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
	sessions := make([]SessionInfo, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		sessions = append(sessions, sessionInfoFromRow(row))
	}
	return SnapshotPayload{SentAt: time.Now().UTC(), Sessions: sessions}, nil
}

func sessionInfoFromRow(row *statedb.InstanceRow) SessionInfo {
	info := SessionInfo{
		ID:               row.ID,
		Title:            row.Title,
		Tool:             row.Tool,
		Status:           row.Status,
		GroupPath:        row.GroupPath,
		ProjectPath:      row.ProjectPath,
		DisplaySessionID: displaySessionIDFromRow(row),
		UpdatedAt:        rowUpdatedAt(row),
	}
	return info
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

func writeEnvelope(conn *websocket.Conn, typ MessageType, nodeID string, payload any) error {
	env, err := MarshalEnvelope(typ, nodeID, payload)
	if err != nil {
		return err
	}
	if err := conn.WriteJSON(env); err != nil {
		return fmt.Errorf("write hub %s: %w", typ, err)
	}
	return nil
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
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.TLSSkipVerify,
		ServerName:         cfg.ServerName,
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
