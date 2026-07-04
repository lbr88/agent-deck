package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrInviteInvalid        = errors.New("hub: invite is invalid, expired, or consumed")
	ErrNodeNotAuthenticated = errors.New("hub: node authentication failed")
	ErrNodeNotFound         = errors.New("hub: node not found")
	ErrAdvertiseURLMissing  = errors.New("hub: advertise URL is not configured")
)

const hubAdvertiseURLKey = "advertise_url"

type Store struct {
	db *sql.DB
}

type Invite struct {
	TokenHash  string
	NodeName   string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

type Node struct {
	ID         string
	Name       string
	TokenHash  string
	Version    string
	OS         string
	Arch       string
	Status     string
	LastSeenAt *time.Time
}

type NodeSessions struct {
	Node     Node
	SentAt   time.Time
	Sessions []SessionInfo
}

func OpenStore(path string) (*Store, error) {
	if path != "" && path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create hub db dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open hub db: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) CreateInvite(nodeName string, ttl time.Duration) (plainToken string, err error) {
	token, err := newSecret("invite_")
	if err != nil {
		return "", err
	}
	now := time.Now()
	_, err = s.db.Exec(
		`INSERT INTO invites (token_hash, node_name, expires_at) VALUES (?, ?, ?)`,
		hashSecret(token), nodeName, now.Add(ttl).UnixNano(),
	)
	if err != nil {
		return "", fmt.Errorf("create invite: %w", err)
	}
	return token, nil
}

func (s *Store) SetAdvertiseURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("hub advertise URL is required")
	}
	_, err := s.db.Exec(
		`INSERT INTO hub_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		hubAdvertiseURLKey, rawURL,
	)
	if err != nil {
		return fmt.Errorf("set hub advertise URL: %w", err)
	}
	return nil
}

func (s *Store) AdvertiseURL() (string, error) {
	var rawURL string
	err := s.db.QueryRow(`SELECT value FROM hub_settings WHERE key = ?`, hubAdvertiseURLKey).Scan(&rawURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrAdvertiseURLMissing
		}
		return "", fmt.Errorf("load hub advertise URL: %w", err)
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", ErrAdvertiseURLMissing
	}
	return rawURL, nil
}

func (s *Store) ConsumeInvite(plainToken string) (Invite, error) {
	tokenHash := hashSecret(plainToken)
	now := time.Now().UnixNano()

	tx, err := s.db.Begin()
	if err != nil {
		return Invite{}, fmt.Errorf("begin consume invite: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`UPDATE invites
		 SET consumed_at = ?
		 WHERE token_hash = ? AND consumed_at IS NULL AND expires_at > ?`,
		now, tokenHash, now,
	)
	if err != nil {
		return Invite{}, fmt.Errorf("consume invite: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return Invite{}, fmt.Errorf("consume invite rows affected: %w", err)
	}
	if rows != 1 {
		return Invite{}, ErrInviteInvalid
	}

	invite, err := scanInvite(tx.QueryRow(
		`SELECT token_hash, node_name, expires_at, consumed_at FROM invites WHERE token_hash = ?`,
		tokenHash,
	))
	if err != nil {
		return Invite{}, fmt.Errorf("load consumed invite: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Invite{}, fmt.Errorf("commit consume invite: %w", err)
	}
	return invite, nil
}

func (s *Store) UpsertNode(id, name, tokenHash, version, osName, arch string) (Node, error) {
	_, err := s.db.Exec(
		`INSERT INTO nodes (id, name, token_hash, version, os, arch)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   token_hash = excluded.token_hash,
		   version = excluded.version,
		   os = excluded.os,
		   arch = excluded.arch`,
		id, name, tokenHash, version, osName, arch,
	)
	if err != nil {
		return Node{}, fmt.Errorf("upsert node: %w", err)
	}
	return s.nodeByID(id)
}

func (s *Store) AuthenticateNode(nodeID, plainToken string) (Node, error) {
	node, err := s.nodeByID(nodeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, ErrNodeNotAuthenticated
		}
		return Node{}, err
	}
	gotHash := hashSecret(plainToken)
	if subtle.ConstantTimeCompare([]byte(node.TokenHash), []byte(gotHash)) != 1 {
		return Node{}, ErrNodeNotAuthenticated
	}
	return node, nil
}

func (s *Store) MarkNodeOnline(nodeID string) error {
	return s.setNodeStatus(nodeID, "online", time.Now().UnixNano())
}

func (s *Store) MarkNodeOffline(nodeID string) error {
	return s.setNodeStatus(nodeID, "offline", 0)
}

func (s *Store) ReplaceSnapshot(nodeID string, snapshot SnapshotPayload) error {
	if _, err := s.nodeByID(nodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNodeNotFound
		}
		return fmt.Errorf("load snapshot node: %w", err)
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO snapshots (node_id, sent_at, payload_json)
		 VALUES (?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE SET
		   sent_at = excluded.sent_at,
		   payload_json = excluded.payload_json`,
		nodeID, snapshot.SentAt.UnixNano(), string(payload),
	)
	if err != nil {
		return fmt.Errorf("replace snapshot: %w", err)
	}
	return nil
}

func (s *Store) LatestSessions() ([]NodeSessions, error) {
	rows, err := s.db.Query(
		`SELECT n.id, n.name, n.token_hash, n.version, n.os, n.arch, n.status, n.last_seen_at,
		        s.sent_at, s.payload_json
		 FROM snapshots s
		 JOIN nodes n ON n.id = s.node_id
		 ORDER BY n.name, n.id`,
	)
	if err != nil {
		return nil, fmt.Errorf("query latest sessions: %w", err)
	}
	defer rows.Close()

	var out []NodeSessions
	for rows.Next() {
		var node Node
		var sentAtUnix int64
		var payloadJSON string
		if err := scanNodeFields(rows, &node, &sentAtUnix, &payloadJSON); err != nil {
			return nil, fmt.Errorf("scan latest sessions: %w", err)
		}
		var snapshot SnapshotPayload
		if err := json.Unmarshal([]byte(payloadJSON), &snapshot); err != nil {
			return nil, fmt.Errorf("decode snapshot for node %s: %w", node.ID, err)
		}
		out = append(out, NodeSessions{
			Node:     node,
			SentAt:   time.Unix(0, sentAtUnix),
			Sessions: snapshot.Sessions,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest sessions: %w", err)
	}
	return out, nil
}

func (s *Store) migrate() error {
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
	}
	for _, pragma := range pragmas {
		if _, err := s.db.Exec(pragma); err != nil {
			return fmt.Errorf("set sqlite pragma: %w", err)
		}
	}

	const schema = `
CREATE TABLE IF NOT EXISTS invites (
  token_hash TEXT PRIMARY KEY,
  node_name TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER
);
CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  version TEXT,
  os TEXT,
  arch TEXT,
  status TEXT NOT NULL DEFAULT 'offline',
  last_seen_at INTEGER
);
CREATE TABLE IF NOT EXISTS snapshots (
  node_id TEXT PRIMARY KEY,
  sent_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  metadata_json TEXT
);
CREATE TABLE IF NOT EXISTS hub_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate hub db: %w", err)
	}
	return nil
}

func (s *Store) nodeByID(id string) (Node, error) {
	return scanNode(s.db.QueryRow(
		`SELECT id, name, token_hash, version, os, arch, status, last_seen_at
		 FROM nodes WHERE id = ?`,
		id,
	))
}

func (s *Store) setNodeStatus(nodeID, status string, lastSeenAt int64) error {
	var res sql.Result
	var err error
	if lastSeenAt == 0 {
		res, err = s.db.Exec(`UPDATE nodes SET status = ? WHERE id = ?`, status, nodeID)
	} else {
		res, err = s.db.Exec(`UPDATE nodes SET status = ?, last_seen_at = ? WHERE id = ?`, status, lastSeenAt, nodeID)
	}
	if err != nil {
		return fmt.Errorf("mark node %s: %w", status, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark node %s rows affected: %w", status, err)
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func scanInvite(row *sql.Row) (Invite, error) {
	var invite Invite
	var expiresAt int64
	var consumedAt sql.NullInt64
	if err := row.Scan(&invite.TokenHash, &invite.NodeName, &expiresAt, &consumedAt); err != nil {
		return Invite{}, err
	}
	invite.ExpiresAt = time.Unix(0, expiresAt)
	if consumedAt.Valid {
		t := time.Unix(0, consumedAt.Int64)
		invite.ConsumedAt = &t
	}
	return invite, nil
}

func scanNode(row *sql.Row) (Node, error) {
	var node Node
	if err := scanNodeValues(row, &node); err != nil {
		return Node{}, err
	}
	return node, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNodeValues(row rowScanner, node *Node) error {
	var version, osName, arch sql.NullString
	var lastSeenAt sql.NullInt64
	if err := row.Scan(
		&node.ID,
		&node.Name,
		&node.TokenHash,
		&version,
		&osName,
		&arch,
		&node.Status,
		&lastSeenAt,
	); err != nil {
		return err
	}
	node.Version = version.String
	node.OS = osName.String
	node.Arch = arch.String
	if lastSeenAt.Valid {
		t := time.Unix(0, lastSeenAt.Int64)
		node.LastSeenAt = &t
	}
	return nil
}

func scanNodeFields(rows *sql.Rows, node *Node, extra ...any) error {
	var version, osName, arch sql.NullString
	var lastSeenAt sql.NullInt64
	dest := []any{
		&node.ID,
		&node.Name,
		&node.TokenHash,
		&version,
		&osName,
		&arch,
		&node.Status,
		&lastSeenAt,
	}
	dest = append(dest, extra...)
	if err := rows.Scan(dest...); err != nil {
		return err
	}
	node.Version = version.String
	node.OS = osName.String
	node.Arch = arch.String
	if lastSeenAt.Valid {
		t := time.Unix(0, lastSeenAt.Int64)
		node.LastSeenAt = &t
	}
	return nil
}

func newSecret(prefix string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
