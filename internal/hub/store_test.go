package hub

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreInviteConsumeIsSingleUse(t *testing.T) {
	store := openTestStore(t)
	token, err := store.CreateInvite("laptop", time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	invite, err := store.ConsumeInvite(token)
	if err != nil {
		t.Fatalf("ConsumeInvite first: %v", err)
	}
	if invite.NodeName != "laptop" {
		t.Fatalf("NodeName = %q", invite.NodeName)
	}
	if _, err := store.ConsumeInvite(token); err == nil {
		t.Fatal("second ConsumeInvite succeeded, want single-use failure")
	}
}

func TestStoreInviteConsumeRejectsExpiredInvite(t *testing.T) {
	store := openTestStore(t)
	token, err := store.CreateInvite("laptop", -time.Second)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := store.ConsumeInvite(token); err == nil {
		t.Fatal("ConsumeInvite expired token succeeded, want failure")
	}
}

func TestStoreInviteConsumeAllowsSubSecondTTL(t *testing.T) {
	store := openTestStore(t)
	token, err := store.CreateInvite("laptop", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := store.ConsumeInvite(token); err != nil {
		t.Fatalf("ConsumeInvite before sub-second expiry: %v", err)
	}
}

func TestStoreAdvertiseURLPersists(t *testing.T) {
	store := openTestStore(t)

	if err := store.SetAdvertiseURL(" wss://hub.example:8421 "); err != nil {
		t.Fatalf("SetAdvertiseURL: %v", err)
	}
	got, err := store.AdvertiseURL()
	if err != nil {
		t.Fatalf("AdvertiseURL: %v", err)
	}
	if got != "wss://hub.example:8421" {
		t.Fatalf("AdvertiseURL = %q, want trimmed URL", got)
	}
}

func TestStoreAuthenticateNodeComparesTokenHash(t *testing.T) {
	store := openTestStore(t)
	token := "node_secret"
	node, err := store.UpsertNode("node_1", "laptop", hashSecret(token), "1.0.0", "linux", "amd64")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	got, err := store.AuthenticateNode(node.ID, token)
	if err != nil {
		t.Fatalf("AuthenticateNode: %v", err)
	}
	if got.ID != node.ID || got.Name != "laptop" {
		t.Fatalf("node = %+v", got)
	}
	if _, err := store.AuthenticateNode(node.ID, "wrong"); err == nil {
		t.Fatal("AuthenticateNode with wrong token succeeded, want failure")
	}
}

func TestStoreRejectsSnapshotForUnknownNode(t *testing.T) {
	store := openTestStore(t)
	err := store.ReplaceSnapshot("missing", SnapshotPayload{
		SentAt:   time.Now(),
		Sessions: []SessionInfo{{ID: "s1", Title: "orphan", Status: "running"}},
	})
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("ReplaceSnapshot error = %v, want ErrNodeNotFound", err)
	}
}

func TestStoreSnapshotReplacesLatest(t *testing.T) {
	store := openTestStore(t)
	node, err := store.UpsertNode("node_1", "laptop", "secret_hash", "1.0.0", "linux", "amd64")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	first := SnapshotPayload{SentAt: time.Now(), Sessions: []SessionInfo{{ID: "s1", Title: "old", Status: "waiting"}}}
	second := SnapshotPayload{SentAt: time.Now(), Sessions: []SessionInfo{{ID: "s2", Title: "new", Status: "running"}}}
	if err := store.ReplaceSnapshot(node.ID, first); err != nil {
		t.Fatalf("ReplaceSnapshot first: %v", err)
	}
	if err := store.ReplaceSnapshot(node.ID, second); err != nil {
		t.Fatalf("ReplaceSnapshot second: %v", err)
	}
	got, err := store.LatestSessions()
	if err != nil {
		t.Fatalf("LatestSessions: %v", err)
	}
	if len(got) != 1 || len(got[0].Sessions) != 1 || got[0].Sessions[0].ID != "s2" {
		t.Fatalf("sessions = %+v", got)
	}
}

func TestStoreSnapshotPreservesNanosecondSentAt(t *testing.T) {
	store := openTestStore(t)
	node, err := store.UpsertNode("node_1", "laptop", "secret_hash", "1.0.0", "linux", "amd64")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	sentAt := time.Unix(123, 456789123)
	if err := store.ReplaceSnapshot(node.ID, SnapshotPayload{SentAt: sentAt}); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}
	got, err := store.LatestSessions()
	if err != nil {
		t.Fatalf("LatestSessions: %v", err)
	}
	if len(got) != 1 || !got[0].SentAt.Equal(sentAt) {
		t.Fatalf("SentAt = %+v, want %v", got, sentAt)
	}
}

func TestStoreLatestSessionsIncludesNodeMetadataAndStatus(t *testing.T) {
	store := openTestStore(t)
	node, err := store.UpsertNode("node_1", "laptop", "secret_hash", "1.2.3", "linux", "amd64")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := store.ReplaceSnapshot(node.ID, SnapshotPayload{
		SentAt:   time.Now(),
		Sessions: []SessionInfo{{ID: "s1", Title: "session", Status: "running"}},
	}); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}
	if err := store.MarkNodeOnline(node.ID); err != nil {
		t.Fatalf("MarkNodeOnline: %v", err)
	}
	online, err := store.LatestSessions()
	if err != nil {
		t.Fatalf("LatestSessions online: %v", err)
	}
	if len(online) != 1 {
		t.Fatalf("online sessions = %+v", online)
	}
	gotNode := online[0].Node
	if gotNode.ID != "node_1" || gotNode.Name != "laptop" || gotNode.Version != "1.2.3" || gotNode.OS != "linux" || gotNode.Arch != "amd64" {
		t.Fatalf("node metadata = %+v", gotNode)
	}
	if gotNode.Status != "online" || gotNode.LastSeenAt == nil || gotNode.LastSeenAt.IsZero() {
		t.Fatalf("online node status = %+v", gotNode)
	}
	if err := store.MarkNodeOffline(node.ID); err != nil {
		t.Fatalf("MarkNodeOffline: %v", err)
	}
	offline, err := store.LatestSessions()
	if err != nil {
		t.Fatalf("LatestSessions offline: %v", err)
	}
	if len(offline) != 1 || offline[0].Node.Status != "offline" {
		t.Fatalf("offline sessions = %+v", offline)
	}
}

func TestStoreLatestSessionsReturnsErrorForMalformedSnapshotJSON(t *testing.T) {
	store := openTestStore(t)
	node, err := store.UpsertNode("node_1", "laptop", "secret_hash", "1.0.0", "linux", "amd64")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if _, err := store.db.Exec(
		`INSERT INTO snapshots (node_id, sent_at, payload_json) VALUES (?, ?, ?)`,
		node.ID, time.Now().UnixNano(), `{bad json`,
	); err != nil {
		t.Fatalf("insert malformed snapshot: %v", err)
	}
	if _, err := store.LatestSessions(); err == nil {
		t.Fatal("LatestSessions succeeded with malformed snapshot JSON, want error")
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
