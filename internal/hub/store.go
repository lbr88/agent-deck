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
	ErrInviteNotFound       = errors.New("hub: invite not found")
	ErrNodeNotAuthenticated = errors.New("hub: node authentication failed")
	ErrNodeNotFound         = errors.New("hub: node not found")
	ErrAdvertiseURLMissing  = errors.New("hub: advertise URL is not configured")
)

const (
	hubAdvertiseURLKey   = "advertise_url"
	hubTrustBackfillKey  = "trust_backfill_v1"
	hubSettingTrueString = "true"
)

type TrustStatus string

const (
	TrustStatusPending TrustStatus = "pending"
	TrustStatusAllowed TrustStatus = "allowed"
	TrustStatusDenied  TrustStatus = "denied"
)

type Store struct {
	db *sql.DB
}

type Invite struct {
	ID              string
	TokenHash       string
	NodeName        string
	ExpiresAt       time.Time
	ConsumedAt      *time.Time
	RevokedAt       *time.Time
	Admin           bool
	CreatedByNodeID string
}

func (i Invite) Status(now time.Time) string {
	switch {
	case i.RevokedAt != nil:
		return "revoked"
	case i.ConsumedAt != nil:
		return "consumed"
	case !i.ExpiresAt.IsZero() && !i.ExpiresAt.After(now):
		return "expired"
	default:
		return "pending"
	}
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
	Admin      bool
}

type NodeSessions struct {
	Node     Node
	SentAt   time.Time
	Sessions []SessionInfo
}

type TrustRequest struct {
	Owner     Node
	Requester Node
	Status    TrustStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CreateInviteOptions struct {
	NodeName        string
	TTL             time.Duration
	Admin           bool
	CreatedByNodeID string
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
	return s.CreateInviteWithOptions(CreateInviteOptions{NodeName: nodeName, TTL: ttl})
}

func (s *Store) CreateInviteWithOptions(opts CreateInviteOptions) (plainToken string, err error) {
	token, err := newSecret("invite_")
	if err != nil {
		return "", err
	}
	inviteID, err := newSecret("inv_")
	if err != nil {
		return "", err
	}
	now := time.Now()
	_, err = s.db.Exec(
		`INSERT INTO invites (id, token_hash, node_name, expires_at, admin, created_by_node_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		inviteID, hashSecret(token), opts.NodeName, now.Add(opts.TTL).UnixNano(), boolInt(opts.Admin), nullString(opts.CreatedByNodeID),
	)
	if err != nil {
		return "", fmt.Errorf("create invite: %w", err)
	}
	return token, nil
}

func (s *Store) Invites() ([]Invite, error) {
	rows, err := s.db.Query(
		`SELECT id, token_hash, node_name, expires_at, consumed_at, revoked_at, admin, created_by_node_id
		 FROM invites
		 ORDER BY expires_at DESC, node_name, token_hash`,
	)
	if err != nil {
		return nil, fmt.Errorf("query invites: %w", err)
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		invite, err := scanInviteValues(rows)
		if err != nil {
			return nil, fmt.Errorf("scan invite: %w", err)
		}
		invites = append(invites, invite)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invites: %w", err)
	}
	return invites, nil
}

func (s *Store) RevokeInvite(identifier string) error {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return ErrInviteNotFound
	}
	tokenHash := ""
	if strings.HasPrefix(identifier, "invite_") {
		tokenHash = hashSecret(identifier)
	}
	res, err := s.db.Exec(
		`UPDATE invites
		 SET revoked_at = ?
		 WHERE consumed_at IS NULL
		   AND revoked_at IS NULL
		   AND (id = ? OR token_hash = ?)`,
		time.Now().UnixNano(), identifier, tokenHash,
	)
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke invite rows affected: %w", err)
	}
	if rows != 1 {
		return ErrInviteNotFound
	}
	return nil
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
		 WHERE token_hash = ? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > ?`,
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
		`SELECT id, token_hash, node_name, expires_at, consumed_at, revoked_at, admin, created_by_node_id FROM invites WHERE token_hash = ?`,
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
	return s.upsertNode(id, name, tokenHash, version, osName, arch, false, false)
}

func (s *Store) UpsertNodeWithAdmin(id, name, tokenHash, version, osName, arch string, admin bool) (Node, error) {
	return s.upsertNode(id, name, tokenHash, version, osName, arch, admin, true)
}

func (s *Store) upsertNode(id, name, tokenHash, version, osName, arch string, admin, setAdmin bool) (Node, error) {
	if setAdmin {
		_, err := s.db.Exec(
			`INSERT INTO nodes (id, name, token_hash, version, os, arch, admin)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			   name = excluded.name,
			   token_hash = excluded.token_hash,
			   version = excluded.version,
			   os = excluded.os,
			   arch = excluded.arch,
			   admin = excluded.admin`,
			id, name, tokenHash, version, osName, arch, boolInt(admin),
		)
		if err != nil {
			return Node{}, fmt.Errorf("upsert node: %w", err)
		}
		return s.nodeByID(id)
	}
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

func (s *Store) NodeCount() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count nodes: %w", err)
	}
	return count, nil
}

func (s *Store) AdminNodeCount() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE admin = 1`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count admin nodes: %w", err)
	}
	return count, nil
}

func (s *Store) SetNodeAdmin(nodeID string, admin bool) error {
	res, err := s.db.Exec(`UPDATE nodes SET admin = ? WHERE id = ?`, boolInt(admin), nodeID)
	if err != nil {
		return fmt.Errorf("set node admin: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set node admin rows affected: %w", err)
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RenameNode(nodeID, name string) (Node, error) {
	nodeID = strings.TrimSpace(nodeID)
	name = strings.TrimSpace(name)
	if nodeID == "" || name == "" {
		return Node{}, sql.ErrNoRows
	}
	res, err := s.db.Exec(`UPDATE nodes SET name = ? WHERE id = ?`, name, nodeID)
	if err != nil {
		return Node{}, fmt.Errorf("rename node: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return Node{}, fmt.Errorf("rename node rows affected: %w", err)
	}
	if rows != 1 {
		return Node{}, sql.ErrNoRows
	}
	return s.nodeByID(nodeID)
}

func (s *Store) RevokeNode(nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return sql.ErrNoRows
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin revoke node: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM snapshots WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("delete node snapshots: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM node_trust_edges WHERE owner_node_id = ? OR requester_node_id = ?`, nodeID, nodeID); err != nil {
		return fmt.Errorf("delete node trust edges: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, nodeID)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete node rows affected: %w", err)
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revoke node: %w", err)
	}
	return nil
}

func (s *Store) CreatePendingTrustRequestsForNewNode(requesterNodeID string) ([]TrustRequest, error) {
	requesterNodeID = strings.TrimSpace(requesterNodeID)
	if requesterNodeID == "" {
		return nil, ErrNodeNotFound
	}
	if _, err := s.nodeByID(requesterNodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNodeNotFound
		}
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin trust requests: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id FROM nodes WHERE id <> ? ORDER BY name, id`, requesterNodeID)
	if err != nil {
		return nil, fmt.Errorf("query existing trust owners: %w", err)
	}
	var ownerIDs []string
	for rows.Next() {
		var ownerID string
		if err := rows.Scan(&ownerID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan existing trust owner: %w", err)
		}
		ownerIDs = append(ownerIDs, ownerID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate existing trust owners: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close existing trust owners: %w", err)
	}

	now := time.Now().UnixNano()
	for _, ownerID := range ownerIDs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO node_trust_edges (owner_node_id, requester_node_id, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)`,
			ownerID, requesterNodeID, string(TrustStatusPending), now, now,
		); err != nil {
			return nil, fmt.Errorf("create pending trust request: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO node_trust_edges (owner_node_id, requester_node_id, status, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?)`,
			requesterNodeID, ownerID, string(TrustStatusPending), now, now,
		); err != nil {
			return nil, fmt.Errorf("create reverse trust edge: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit trust requests: %w", err)
	}
	return s.trustRequests(`e.requester_node_id = ? AND e.status = ?`, requesterNodeID, string(TrustStatusPending))
}

func (s *Store) PendingTrustRequests(ownerNodeID string) ([]TrustRequest, error) {
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	if ownerNodeID == "" {
		return nil, nil
	}
	return s.trustRequests(`e.owner_node_id = ? AND e.status = ?`, ownerNodeID, string(TrustStatusPending))
}

func (s *Store) AllowTrust(ownerNodeID, requesterNodeID string) error {
	return s.SetTrust(ownerNodeID, requesterNodeID, TrustStatusAllowed)
}

func (s *Store) DenyTrust(ownerNodeID, requesterNodeID string) error {
	return s.SetTrust(ownerNodeID, requesterNodeID, TrustStatusDenied)
}

func (s *Store) SetTrust(ownerNodeID, requesterNodeID string, status TrustStatus) error {
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	requesterNodeID = strings.TrimSpace(requesterNodeID)
	if ownerNodeID == "" || requesterNodeID == "" {
		return ErrNodeNotFound
	}
	if ownerNodeID == requesterNodeID {
		return nil
	}
	switch status {
	case TrustStatusPending, TrustStatusAllowed, TrustStatusDenied:
	default:
		return fmt.Errorf("invalid trust status %q", status)
	}
	if _, err := s.nodeByID(ownerNodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNodeNotFound
		}
		return err
	}
	if _, err := s.nodeByID(requesterNodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNodeNotFound
		}
		return err
	}

	now := time.Now().UnixNano()
	_, err := s.db.Exec(
		`INSERT INTO node_trust_edges (owner_node_id, requester_node_id, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(owner_node_id, requester_node_id) DO UPDATE SET
		   status = excluded.status,
		   updated_at = excluded.updated_at`,
		ownerNodeID, requesterNodeID, string(status), now, now,
	)
	if err != nil {
		return fmt.Errorf("set node trust: %w", err)
	}
	return nil
}

func (s *Store) CanAccessNode(ownerNodeID, requesterNodeID string) (bool, error) {
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	requesterNodeID = strings.TrimSpace(requesterNodeID)
	if ownerNodeID == "" || requesterNodeID == "" {
		return false, nil
	}
	if ownerNodeID == requesterNodeID {
		return true, nil
	}
	var status string
	err := s.db.QueryRow(
		`SELECT status FROM node_trust_edges WHERE owner_node_id = ? AND requester_node_id = ?`,
		ownerNodeID, requesterNodeID,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("load node trust: %w", err)
	}
	return TrustStatus(status) == TrustStatusAllowed, nil
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
		`SELECT n.id, n.name, n.token_hash, n.version, n.os, n.arch, n.status, n.last_seen_at, n.admin,
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
  id TEXT,
  token_hash TEXT PRIMARY KEY,
  node_name TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER,
  revoked_at INTEGER,
  admin INTEGER NOT NULL DEFAULT 0,
  created_by_node_id TEXT
);
CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  version TEXT,
  os TEXT,
  arch TEXT,
  status TEXT NOT NULL DEFAULT 'offline',
  last_seen_at INTEGER,
  admin INTEGER NOT NULL DEFAULT 0
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
);
CREATE TABLE IF NOT EXISTS node_trust_edges (
  owner_node_id TEXT NOT NULL,
  requester_node_id TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (owner_node_id, requester_node_id)
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate hub db: %w", err)
	}
	if err := s.ensureColumn("invites", "id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("invites", "revoked_at", "INTEGER"); err != nil {
		return err
	}
	if err := s.ensureColumn("invites", "admin", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("invites", "created_by_node_id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("nodes", "admin", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_invites_id ON invites(id) WHERE id IS NOT NULL`); err != nil {
		return fmt.Errorf("create invite id index: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_node_trust_owner_status ON node_trust_edges(owner_node_id, status)`); err != nil {
		return fmt.Errorf("create node trust owner index: %w", err)
	}
	if err := s.backfillLegacyTrustEdges(); err != nil {
		return err
	}
	return nil
}

func (s *Store) backfillLegacyTrustEdges() error {
	var done string
	err := s.db.QueryRow(`SELECT value FROM hub_settings WHERE key = ?`, hubTrustBackfillKey).Scan(&done)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load trust backfill setting: %w", err)
	}
	if strings.TrimSpace(done) == hubSettingTrueString {
		return nil
	}

	var edgeCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM node_trust_edges`).Scan(&edgeCount); err != nil {
		return fmt.Errorf("count trust edges: %w", err)
	}
	if edgeCount != 0 {
		return s.markTrustBackfillComplete()
	}
	var nodeCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&nodeCount); err != nil {
		return fmt.Errorf("count trust backfill nodes: %w", err)
	}
	if nodeCount < 2 {
		return s.markTrustBackfillComplete()
	}
	now := time.Now().UnixNano()
	_, err = s.db.Exec(
		`INSERT OR IGNORE INTO node_trust_edges (owner_node_id, requester_node_id, status, created_at, updated_at)
		 SELECT owner.id, requester.id, ?, ?, ?
		 FROM nodes owner
		 CROSS JOIN nodes requester
		 WHERE owner.id <> requester.id`,
		string(TrustStatusAllowed), now, now,
	)
	if err != nil {
		return fmt.Errorf("backfill legacy trust edges: %w", err)
	}
	return s.markTrustBackfillComplete()
}

func (s *Store) markTrustBackfillComplete() error {
	_, err := s.db.Exec(
		`INSERT INTO hub_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		hubTrustBackfillKey, hubSettingTrueString,
	)
	if err != nil {
		return fmt.Errorf("mark trust backfill complete: %w", err)
	}
	return nil
}

func (s *Store) trustRequests(where string, args ...any) ([]TrustRequest, error) {
	query := `SELECT owner.id, owner.name, owner.token_hash, owner.version, owner.os, owner.arch, owner.status, owner.last_seen_at, owner.admin,
	                requester.id, requester.name, requester.token_hash, requester.version, requester.os, requester.arch, requester.status, requester.last_seen_at, requester.admin,
	                e.status, e.created_at, e.updated_at
	         FROM node_trust_edges e
	         JOIN nodes owner ON owner.id = e.owner_node_id
	         JOIN nodes requester ON requester.id = e.requester_node_id
	         WHERE ` + where + `
	         ORDER BY requester.name, requester.id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query trust requests: %w", err)
	}
	defer rows.Close()

	var out []TrustRequest
	for rows.Next() {
		request, err := scanTrustRequestValues(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trust request: %w", err)
		}
		out = append(out, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trust requests: %w", err)
	}
	return out, nil
}

func (s *Store) nodeByID(id string) (Node, error) {
	return scanNode(s.db.QueryRow(
		`SELECT id, name, token_hash, version, os, arch, status, last_seen_at, admin
		 FROM nodes WHERE id = ?`,
		id,
	))
}

func (s *Store) ensureColumn(table, column, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan %s column info: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s column info: %w", table, err)
	}
	if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("add %s.%s column: %w", table, column, err)
	}
	return nil
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
	return scanInviteValues(row)
}

func scanInviteValues(row rowScanner) (Invite, error) {
	var invite Invite
	var id sql.NullString
	var expiresAt int64
	var consumedAt sql.NullInt64
	var revokedAt sql.NullInt64
	var admin int
	var createdBy sql.NullString
	if err := row.Scan(&id, &invite.TokenHash, &invite.NodeName, &expiresAt, &consumedAt, &revokedAt, &admin, &createdBy); err != nil {
		return Invite{}, err
	}
	invite.ID = id.String
	invite.ExpiresAt = time.Unix(0, expiresAt)
	if consumedAt.Valid {
		t := time.Unix(0, consumedAt.Int64)
		invite.ConsumedAt = &t
	}
	if revokedAt.Valid {
		t := time.Unix(0, revokedAt.Int64)
		invite.RevokedAt = &t
	}
	invite.Admin = admin != 0
	invite.CreatedByNodeID = createdBy.String
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
	var admin int
	if err := row.Scan(
		&node.ID,
		&node.Name,
		&node.TokenHash,
		&version,
		&osName,
		&arch,
		&node.Status,
		&lastSeenAt,
		&admin,
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
	node.Admin = admin != 0
	return nil
}

func scanTrustRequestValues(row rowScanner) (TrustRequest, error) {
	var owner, requester Node
	var ownerVersion, ownerOS, ownerArch sql.NullString
	var ownerLastSeenAt sql.NullInt64
	var ownerAdmin int
	var requesterVersion, requesterOS, requesterArch sql.NullString
	var requesterLastSeenAt sql.NullInt64
	var requesterAdmin int
	var status string
	var createdAt, updatedAt int64
	if err := row.Scan(
		&owner.ID,
		&owner.Name,
		&owner.TokenHash,
		&ownerVersion,
		&ownerOS,
		&ownerArch,
		&owner.Status,
		&ownerLastSeenAt,
		&ownerAdmin,
		&requester.ID,
		&requester.Name,
		&requester.TokenHash,
		&requesterVersion,
		&requesterOS,
		&requesterArch,
		&requester.Status,
		&requesterLastSeenAt,
		&requesterAdmin,
		&status,
		&createdAt,
		&updatedAt,
	); err != nil {
		return TrustRequest{}, err
	}
	owner.Version = ownerVersion.String
	owner.OS = ownerOS.String
	owner.Arch = ownerArch.String
	owner.Admin = ownerAdmin != 0
	if ownerLastSeenAt.Valid {
		t := time.Unix(0, ownerLastSeenAt.Int64)
		owner.LastSeenAt = &t
	}
	requester.Version = requesterVersion.String
	requester.OS = requesterOS.String
	requester.Arch = requesterArch.String
	requester.Admin = requesterAdmin != 0
	if requesterLastSeenAt.Valid {
		t := time.Unix(0, requesterLastSeenAt.Int64)
		requester.LastSeenAt = &t
	}
	return TrustRequest{
		Owner:     owner,
		Requester: requester,
		Status:    TrustStatus(status),
		CreatedAt: time.Unix(0, createdAt),
		UpdatedAt: time.Unix(0, updatedAt),
	}, nil
}

func scanNodeFields(rows *sql.Rows, node *Node, extra ...any) error {
	var version, osName, arch sql.NullString
	var lastSeenAt sql.NullInt64
	var admin int
	dest := []any{
		&node.ID,
		&node.Name,
		&node.TokenHash,
		&version,
		&osName,
		&arch,
		&node.Status,
		&lastSeenAt,
		&admin,
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
	node.Admin = admin != 0
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

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullString(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}
