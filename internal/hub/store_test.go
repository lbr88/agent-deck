package hub

import (
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

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
