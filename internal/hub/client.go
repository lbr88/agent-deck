package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
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
		err = c.connectOnce(ctx, cfg, wsURL, tlsConfig)
		if ctx.Err() != nil {
			return nil
		}
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
		Token:    cfg.Token,
		Version:  cfg.Version,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}); err != nil {
		return err
	}
	if err := c.publishSnapshot(ctx, conn, cfg.NodeID); err != nil {
		return err
	}

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
			if err := c.publishSnapshot(ctx, conn, cfg.NodeID); err != nil {
				return err
			}
		}
	}
}

func (c *Client) publishSnapshot(ctx context.Context, conn *websocket.Conn, nodeID string) error {
	snapshot, err := c.buildSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("build snapshot: %w", err)
	}
	if snapshot.SentAt.IsZero() {
		snapshot.SentAt = time.Now().UTC()
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
	case MsgWelcome, MsgHeartbeat, MsgCommand, MsgAttachOpen, MsgAttachReady, MsgAttachData, MsgAttachResize, MsgAttachClose, MsgAttachClosed, MsgCommandResult, MsgError:
		return
	default:
		return
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

	storage, err := session.NewStorageWithProfile(s.Profile)
	if err != nil {
		return SnapshotPayload{}, err
	}
	defer storage.Close()

	instances, _, err := storage.LoadLite()
	if err != nil {
		return SnapshotPayload{}, err
	}
	sessions := make([]SessionInfo, 0, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		sessions = append(sessions, sessionInfoFromInstanceData(inst))
	}
	return SnapshotPayload{SentAt: time.Now().UTC(), Sessions: sessions}, nil
}

func sessionInfoFromInstanceData(inst *session.InstanceData) SessionInfo {
	info := SessionInfo{
		ID:               inst.ID,
		Title:            inst.Title,
		Tool:             inst.Tool,
		Status:           string(inst.Status),
		GroupPath:        inst.GroupPath,
		ProjectPath:      inst.ProjectPath,
		DisplaySessionID: displaySessionID(inst),
		UpdatedAt:        sessionUpdatedAt(inst),
	}
	return info
}

func displaySessionID(inst *session.InstanceData) string {
	proxy := &session.Instance{
		Tool:              inst.Tool,
		ClaudeSessionID:   inst.ClaudeSessionID,
		GeminiSessionID:   inst.GeminiSessionID,
		OpenCodeSessionID: inst.OpenCodeSessionID,
		CodexSessionID:    inst.CodexSessionID,
		KiroSessionID:     inst.KiroSessionID,
	}
	return proxy.DisplaySessionID()
}

func sessionUpdatedAt(inst *session.InstanceData) *time.Time {
	switch {
	case !inst.LastAccessedAt.IsZero():
		t := inst.LastAccessedAt
		return &t
	case !inst.CreatedAt.IsZero():
		t := inst.CreatedAt
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
