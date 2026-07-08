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

func TestStoreListsAndRevokesInvites(t *testing.T) {
	store := openTestStore(t)
	token, err := store.CreateInviteWithOptions(CreateInviteOptions{
		NodeName:        "desktop",
		TTL:             time.Hour,
		Admin:           true,
		CreatedByNodeID: "node_admin",
	})
	if err != nil {
		t.Fatalf("CreateInviteWithOptions: %v", err)
	}

	invites, err := store.Invites()
	if err != nil {
		t.Fatalf("Invites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("invites length = %d, want 1: %+v", len(invites), invites)
	}
	invite := invites[0]
	if invite.ID == "" || invite.TokenHash == "" || invite.NodeName != "desktop" || !invite.Admin || invite.CreatedByNodeID != "node_admin" {
		t.Fatalf("invite = %+v, want listed admin invite with id", invite)
	}
	if invite.Status(time.Now()) != "pending" {
		t.Fatalf("invite status = %q, want pending", invite.Status(time.Now()))
	}

	if err := store.RevokeInvite(invite.ID); err != nil {
		t.Fatalf("RevokeInvite by id: %v", err)
	}
	if _, err := store.ConsumeInvite(token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("ConsumeInvite after revoke = %v, want ErrInviteInvalid", err)
	}
	invites, err = store.Invites()
	if err != nil {
		t.Fatalf("Invites after revoke: %v", err)
	}
	if len(invites) != 1 || invites[0].RevokedAt == nil || invites[0].Status(time.Now()) != "revoked" {
		t.Fatalf("revoked invite = %+v, want revoked status", invites)
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

func TestStoreAdminInviteCreatesAdminNode(t *testing.T) {
	store := openTestStore(t)
	token, err := store.CreateInviteWithOptions(CreateInviteOptions{
		NodeName: "laptop",
		TTL:      time.Hour,
		Admin:    true,
	})
	if err != nil {
		t.Fatalf("CreateInviteWithOptions: %v", err)
	}
	invite, err := store.ConsumeInvite(token)
	if err != nil {
		t.Fatalf("ConsumeInvite: %v", err)
	}
	if !invite.Admin {
		t.Fatalf("Invite.Admin = false, want true")
	}

	node, err := store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64", invite.Admin)
	if err != nil {
		t.Fatalf("UpsertNodeWithAdmin: %v", err)
	}
	if !node.Admin {
		t.Fatalf("Node.Admin = false, want true")
	}
	got, err := store.AuthenticateNode(node.ID, "node_secret")
	if err != nil {
		t.Fatalf("AuthenticateNode: %v", err)
	}
	if !got.Admin {
		t.Fatalf("authenticated Node.Admin = false, want true")
	}
}

func TestStoreNodeCountAndAdminPromotion(t *testing.T) {
	store := openTestStore(t)
	count, err := store.NodeCount()
	if err != nil {
		t.Fatalf("NodeCount empty: %v", err)
	}
	if count != 0 {
		t.Fatalf("NodeCount empty = %d, want 0", count)
	}
	if _, err := store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	count, err = store.NodeCount()
	if err != nil {
		t.Fatalf("NodeCount populated: %v", err)
	}
	if count != 1 {
		t.Fatalf("NodeCount populated = %d, want 1", count)
	}
	if err := store.SetNodeAdmin("node_1", true); err != nil {
		t.Fatalf("SetNodeAdmin: %v", err)
	}
	got, err := store.AuthenticateNode("node_1", "node_secret")
	if err != nil {
		t.Fatalf("AuthenticateNode: %v", err)
	}
	if !got.Admin {
		t.Fatalf("Node.Admin = false, want true after promotion")
	}
}

func TestStoreRenamesAndRevokesNode(t *testing.T) {
	store := openTestStore(t)
	node, err := store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := store.ReplaceSnapshot(node.ID, SnapshotPayload{
		SentAt:   time.Now(),
		Sessions: []SessionInfo{{ID: "s1", Title: "session"}},
	}); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}

	renamed, err := store.RenameNode(node.ID, "desktop")
	if err != nil {
		t.Fatalf("RenameNode: %v", err)
	}
	if renamed.Name != "desktop" {
		t.Fatalf("renamed node name = %q, want desktop", renamed.Name)
	}
	if err := store.RevokeNode(node.ID); err != nil {
		t.Fatalf("RevokeNode: %v", err)
	}
	if _, err := store.AuthenticateNode(node.ID, "node_secret"); !errors.Is(err, ErrNodeNotAuthenticated) {
		t.Fatalf("AuthenticateNode after revoke = %v, want ErrNodeNotAuthenticated", err)
	}
	snapshots, err := store.LatestSessions()
	if err != nil {
		t.Fatalf("LatestSessions after revoke: %v", err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("snapshots after revoke = %+v, want none", snapshots)
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
	second := SnapshotPayload{
		SentAt:       time.Now(),
		WebAvailable: true,
		Sessions:     []SessionInfo{{ID: "s2", Title: "new", Status: "running"}},
		Groups:       []GroupInfo{{Name: "ops", Path: "ops", DefaultPath: "/srv/ops"}},
	}
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
	if !got[0].WebAvailable {
		t.Fatalf("WebAvailable = false, want true: %+v", got[0])
	}
	if len(got[0].Groups) != 1 || got[0].Groups[0].DefaultPath != "/srv/ops" {
		t.Fatalf("groups = %+v, want /srv/ops default path", got[0].Groups)
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

func TestStoreLatestSessionsIncludesOnlineNodeWithoutSnapshot(t *testing.T) {
	store := openTestStore(t)
	node, err := store.UpsertNode("node_empty", "empty-node", "secret_hash", "1.2.3", "linux", "amd64")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := store.MarkNodeOnline(node.ID); err != nil {
		t.Fatalf("MarkNodeOnline: %v", err)
	}

	got, err := store.LatestSessions()
	if err != nil {
		t.Fatalf("LatestSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("LatestSessions length = %d, want 1: %+v", len(got), got)
	}
	if got[0].Node.ID != "node_empty" || got[0].Node.Name != "empty-node" {
		t.Fatalf("node = %+v", got[0].Node)
	}
	if len(got[0].Sessions) != 0 {
		t.Fatalf("sessions length = %d, want 0", len(got[0].Sessions))
	}
	if got[0].SentAt.IsZero() {
		t.Fatal("SentAt is zero")
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

func TestStoreTrustRequestsGateRequesterAccessToOwner(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.UpsertNode("node_owner", "workstation", "owner_hash", "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode owner: %v", err)
	}
	if _, err := store.UpsertNode("node_requester", "laptop", "requester_hash", "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode requester: %v", err)
	}

	requests, err := store.CreatePendingTrustRequestsForNewNode("node_requester")
	if err != nil {
		t.Fatalf("CreatePendingTrustRequestsForNewNode: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("trust requests = %+v, want one owner approval request", requests)
	}
	if requests[0].Owner.ID != "node_owner" || requests[0].Requester.ID != "node_requester" || requests[0].Status != TrustStatusPending {
		t.Fatalf("trust request = %+v, want node_owner pending node_requester", requests[0])
	}

	allowed, err := store.CanAccessNode("node_owner", "node_requester")
	if err != nil {
		t.Fatalf("CanAccessNode pending: %v", err)
	}
	if allowed {
		t.Fatal("pending requester can access owner, want blocked")
	}
	allowed, err = store.CanAccessNode("node_requester", "node_owner")
	if err != nil {
		t.Fatalf("CanAccessNode reverse: %v", err)
	}
	if allowed {
		t.Fatal("existing owner can access new requester without requester approval, want blocked")
	}

	pending, err := store.PendingTrustRequests("node_owner")
	if err != nil {
		t.Fatalf("PendingTrustRequests: %v", err)
	}
	if len(pending) != 1 || pending[0].Requester.ID != "node_requester" {
		t.Fatalf("pending requests = %+v, want requester approval", pending)
	}
	reversePending, err := store.PendingTrustRequests("node_requester")
	if err != nil {
		t.Fatalf("PendingTrustRequests reverse: %v", err)
	}
	if len(reversePending) != 1 || reversePending[0].Requester.ID != "node_owner" {
		t.Fatalf("reverse pending requests = %+v, want owner approval from new node", reversePending)
	}

	if err := store.AllowTrust("node_owner", "node_requester"); err != nil {
		t.Fatalf("AllowTrust: %v", err)
	}
	allowed, err = store.CanAccessNode("node_owner", "node_requester")
	if err != nil {
		t.Fatalf("CanAccessNode allowed: %v", err)
	}
	if !allowed {
		t.Fatal("allowed requester cannot access owner")
	}

	if err := store.DenyTrust("node_owner", "node_requester"); err != nil {
		t.Fatalf("DenyTrust: %v", err)
	}
	allowed, err = store.CanAccessNode("node_owner", "node_requester")
	if err != nil {
		t.Fatalf("CanAccessNode denied: %v", err)
	}
	if allowed {
		t.Fatal("denied requester can access owner")
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
