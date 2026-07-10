package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHubHealthz(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("GET /healthz body = %q, want ok", rec.Body.String())
	}
}

func TestServerJoinConsumesInviteAndReturnsNodeCredentials(t *testing.T) {
	server := newTestServer(t)
	inviteToken, err := server.store.CreateInvite("laptop", time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	body := bytes.NewBufferString(`{"invite_token":` + strconvQuote(inviteToken) + `,"node_name":"laptop","version":"1.0.0","os":"linux","arch":"amd64"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/join", body)
	req.Host = "hub.local:8421"
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/join status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		URL       string `json:"url"`
		NodeID    string `json:"node_id"`
		NodeName  string `json:"node_name"`
		NodeToken string `json:"node_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode join response: %v", err)
	}
	if got.URL != "wss://hub.local:8421" || got.NodeName != "laptop" || got.NodeID == "" || got.NodeToken == "" {
		t.Fatalf("join response = %+v", got)
	}
	if _, err := server.store.AuthenticateNode(got.NodeID, got.NodeToken); err != nil {
		t.Fatalf("AuthenticateNode returned credentials: %v", err)
	}

	reuseReq := httptest.NewRequest(http.MethodPost, "/api/join", bytes.NewBufferString(`{"invite_token":`+strconvQuote(inviteToken)+`}`))
	reuseRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(reuseRec, reuseReq)
	if reuseRec.Code == http.StatusOK {
		t.Fatal("reused invite succeeded, want failure")
	}
}

func TestServerJoinAdminInviteCreatesAdminNode(t *testing.T) {
	server := newTestServer(t)
	inviteToken, err := server.store.CreateInviteWithOptions(CreateInviteOptions{
		NodeName: "laptop",
		TTL:      time.Hour,
		Admin:    true,
	})
	if err != nil {
		t.Fatalf("CreateInviteWithOptions: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/join", bytes.NewBufferString(`{"invite_token":`+strconvQuote(inviteToken)+`}`))
	req.Host = "hub.local:8421"
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/join status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		NodeID    string `json:"node_id"`
		NodeToken string `json:"node_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode join response: %v", err)
	}
	node, err := server.store.AuthenticateNode(got.NodeID, got.NodeToken)
	if err != nil {
		t.Fatalf("AuthenticateNode returned credentials: %v", err)
	}
	if !node.Admin {
		t.Fatalf("joined node Admin = false, want true")
	}
}

func TestServerJoinUsesConfiguredAdvertiseURL(t *testing.T) {
	server, err := NewServer(ServerConfig{
		DataDir:      t.TempDir(),
		AdvertiseURL: "wss://public.example:443",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	inviteToken, err := server.store.CreateInvite("laptop", time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/join", bytes.NewBufferString(`{"invite_token":`+strconvQuote(inviteToken)+`}`))
	req.Host = "internal.invalid:8421"
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/join status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode join response: %v", err)
	}
	if got.URL != "wss://public.example:443" {
		t.Fatalf("join URL = %q, want advertised public URL", got.URL)
	}
}

func TestServerJoinRequiresInviteToken(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/join", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/join missing token status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestServerInviteAPIRequiresAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/invites?node_id=node_1", strings.NewReader(`{"node_name":"work-laptop"}`))
	req.Header.Set("Authorization", "Bearer node_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /api/invites status = %d, want %d; body=%q", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestServerTrustAPIAllowsOwnerDecision(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_owner", "workstation", hashSecret("owner_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode owner: %v", err)
	}
	if _, err := server.store.UpsertNode("node_requester", "laptop", hashSecret("requester_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode requester: %v", err)
	}
	if err := server.store.SetTrust("node_owner", "node_requester", TrustStatusPending); err != nil {
		t.Fatalf("SetTrust pending: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/trust/pending?node_id=node_owner", nil)
	req.Header.Set("Authorization", "Bearer owner_secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/trust/pending status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var pending trustRequestsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("decode pending trust response: %v", err)
	}
	if len(pending.Requests) != 1 || pending.Requests[0].NodeID != "node_requester" {
		t.Fatalf("pending trust response = %+v, want node_requester", pending)
	}

	allowReq := httptest.NewRequest(http.MethodPost, "/api/trust/allow?node_id=node_owner", strings.NewReader(`{"node_id":"node_requester"}`))
	allowReq.Header.Set("Authorization", "Bearer owner_secret")
	allowRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowRec, allowReq)
	if allowRec.Code != http.StatusOK {
		t.Fatalf("POST /api/trust/allow status = %d, want %d; body=%q", allowRec.Code, http.StatusOK, allowRec.Body.String())
	}
	allowed, err := server.store.CanAccessNode("node_owner", "node_requester")
	if err != nil {
		t.Fatalf("CanAccessNode: %v", err)
	}
	if !allowed {
		t.Fatal("requester cannot access owner after allow")
	}
}

func TestServerTrustAPIDenyClearsRequesterSnapshot(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_owner", "workstation", hashSecret("owner_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode owner: %v", err)
	}
	if _, err := server.store.UpsertNode("node_requester", "laptop", hashSecret("requester_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode requester: %v", err)
	}
	allowTestTrust(t, server, "node_owner", "node_requester")
	if err := server.store.ReplaceSnapshot("node_owner", SnapshotPayload{
		SentAt:   time.Unix(126, 0).UTC(),
		Sessions: []SessionInfo{{ID: "s1", Title: "worker", Status: "waiting"}},
	}); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}

	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	requester := dialTestNodeWebSocket(t, httpServer.URL, "node_requester", "requester_secret")
	defer requester.Close()
	readTestWelcome(t, requester)
	_ = readTestEnvelopeOf(t, requester, MsgSnapshot, "node_owner")

	denyReq := httptest.NewRequest(http.MethodPost, "/api/trust/deny?node_id=node_owner", strings.NewReader(`{"node_id":"node_requester"}`))
	denyReq.Header.Set("Authorization", "Bearer owner_secret")
	denyRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(denyRec, denyReq)
	if denyRec.Code != http.StatusOK {
		t.Fatalf("POST /api/trust/deny status = %d, want %d; body=%q", denyRec.Code, http.StatusOK, denyRec.Body.String())
	}

	cleared := readTestEnvelope(t, requester)
	if cleared.Type != MsgSnapshot || cleared.NodeID != "node_owner" {
		t.Fatalf("clear snapshot envelope = %+v", cleared)
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(cleared.Payload, &payload); err != nil {
		t.Fatalf("decode clear snapshot: %v", err)
	}
	if len(payload.Sessions) != 0 {
		t.Fatalf("clear snapshot sessions = %+v, want none", payload.Sessions)
	}
}

func TestServerInviteAPICreatesInviteForAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/invites?node_id=node_admin", strings.NewReader(`{"node_name":"work-laptop","ttl_seconds":3600,"admin":true}`))
	req.Host = "hub.local:8421"
	req.Header.Set("Authorization", "Bearer admin_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/invites status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		URL         string    `json:"url"`
		InviteToken string    `json:"invite_token"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode invite response: %v", err)
	}
	if got.URL != "wss://hub.local:8421" || !strings.HasPrefix(got.InviteToken, "invite_") || got.ExpiresAt.IsZero() {
		t.Fatalf("invite response = %+v", got)
	}
	invite, err := server.store.ConsumeInvite(got.InviteToken)
	if err != nil {
		t.Fatalf("ConsumeInvite returned token: %v", err)
	}
	if invite.NodeName != "work-laptop" || !invite.Admin {
		t.Fatalf("created invite = %+v, want admin invite for work-laptop", invite)
	}
}

func TestServerNodesAPIRequiresAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/nodes?node_id=node_1", nil)
	req.Header.Set("Authorization", "Bearer node_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /api/nodes status = %d, want %d; body=%q", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestServerNodesAPIListsNodesForAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin admin: %v", err)
	}
	if _, err := server.store.UpsertNode("node_2", "desktop", hashSecret("node_secret"), "1.0.0", "linux", "arm64"); err != nil {
		t.Fatalf("UpsertNode node_2: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/nodes?node_id=node_admin", nil)
	req.Header.Set("Authorization", "Bearer admin_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/nodes status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		Nodes []Node `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes length = %d, want 2: %+v", len(got.Nodes), got.Nodes)
	}
	for _, node := range got.Nodes {
		if node.TokenHash != "" {
			t.Fatalf("nodes response exposed token hash: %+v", node)
		}
		if node.ID == "node_admin" && !node.Admin {
			t.Fatalf("admin node response = %+v, want admin=true", node)
		}
	}
}

func TestServerNodesPromoteAPIRequiresAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode node_1: %v", err)
	}
	if _, err := server.store.UpsertNode("node_2", "desktop", hashSecret("target_secret"), "1.0.0", "linux", "arm64"); err != nil {
		t.Fatalf("UpsertNode node_2: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/promote?node_id=node_1", strings.NewReader(`{"node_id":"node_2"}`))
	req.Header.Set("Authorization", "Bearer node_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /api/nodes/promote status = %d, want %d; body=%q", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestServerNodesPromoteAPIUpdatesNodeForAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin admin: %v", err)
	}
	if _, err := server.store.UpsertNode("node_2", "desktop", hashSecret("node_secret"), "1.0.0", "linux", "arm64"); err != nil {
		t.Fatalf("UpsertNode node_2: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/promote?node_id=node_admin", strings.NewReader(`{"node_id":"node_2"}`))
	req.Header.Set("Authorization", "Bearer admin_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/nodes/promote status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got Node
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode promote response: %v", err)
	}
	if got.ID != "node_2" || !got.Admin || got.TokenHash != "" {
		t.Fatalf("promote response = %+v, want promoted node without token hash", got)
	}
	nodes, err := server.store.Nodes()
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	found := false
	for _, node := range nodes {
		if node.ID == "node_2" {
			found = true
			if !node.Admin {
				t.Fatalf("stored node_2 admin = false, want true")
			}
		}
	}
	if !found {
		t.Fatal("node_2 not found after promote")
	}
}

func TestServerStatusAPIReportsAuthenticatedNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin admin: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/status?node_id=node_admin", nil)
	req.Header.Set("Authorization", "Bearer admin_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/status status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		Node nodeResponse `json:"node"`
		URL  string       `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if got.Node.ID != "node_admin" || !got.Node.Admin {
		t.Fatalf("status response = %+v, want authenticated admin node", got)
	}
}

func TestServerNodesDemoteAPIRejectsLastAdmin(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin admin: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/demote?node_id=node_admin", strings.NewReader(`{"node_id":"node_admin"}`))
	req.Header.Set("Authorization", "Bearer admin_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /api/nodes/demote status = %d, want %d; body=%q", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestServerNodesDemoteAPIUpdatesNodeForAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin admin: %v", err)
	}
	if _, err := server.store.UpsertNodeWithAdmin("node_2", "desktop", hashSecret("node_secret"), "1.0.0", "linux", "arm64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin node_2: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/demote?node_id=node_admin", strings.NewReader(`{"node_id":"node_2"}`))
	req.Header.Set("Authorization", "Bearer admin_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/nodes/demote status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got nodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode demote response: %v", err)
	}
	if got.ID != "node_2" || got.Admin {
		t.Fatalf("demote response = %+v, want node_2 admin=false", got)
	}
}

func TestServerNodesRenameAPIUpdatesNodeForAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin admin: %v", err)
	}
	if _, err := server.store.UpsertNode("node_2", "old", hashSecret("node_secret"), "1.0.0", "linux", "arm64"); err != nil {
		t.Fatalf("UpsertNode node_2: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/rename?node_id=node_admin", strings.NewReader(`{"node_id":"node_2","name":"desktop"}`))
	req.Header.Set("Authorization", "Bearer admin_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/nodes/rename status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got nodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode rename response: %v", err)
	}
	if got.ID != "node_2" || got.Name != "desktop" {
		t.Fatalf("rename response = %+v, want desktop", got)
	}
}

func TestServerNodesRevokeAPIDeletesNodeForAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin admin: %v", err)
	}
	if _, err := server.store.UpsertNode("node_2", "desktop", hashSecret("node_secret"), "1.0.0", "linux", "arm64"); err != nil {
		t.Fatalf("UpsertNode node_2: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/revoke?node_id=node_admin", strings.NewReader(`{"node_id":"node_2"}`))
	req.Header.Set("Authorization", "Bearer admin_secret")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/nodes/revoke status = %d, want %d; body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if _, err := server.store.AuthenticateNode("node_2", "node_secret"); !errors.Is(err, ErrNodeNotAuthenticated) {
		t.Fatalf("AuthenticateNode revoked node = %v, want ErrNodeNotAuthenticated", err)
	}
}

func TestServerInvitesAPIListsAndRevokesInviteForAdminNode(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "laptop", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin admin: %v", err)
	}
	token, err := server.store.CreateInviteWithOptions(CreateInviteOptions{
		NodeName:        "desktop",
		TTL:             time.Hour,
		Admin:           true,
		CreatedByNodeID: "node_admin",
	})
	if err != nil {
		t.Fatalf("CreateInviteWithOptions: %v", err)
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/invites?node_id=node_admin", nil)
	listReq.Header.Set("Authorization", "Bearer admin_secret")
	listRec := httptest.NewRecorder()

	server.Handler().ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/invites status = %d, want %d; body=%q", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var listed struct {
		Invites []struct {
			ID       string `json:"id"`
			NodeName string `json:"node_name"`
			Admin    bool   `json:"admin"`
			Status   string `json:"status"`
		} `json:"invites"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode invites response: %v", err)
	}
	if len(listed.Invites) != 1 || listed.Invites[0].ID == "" || listed.Invites[0].NodeName != "desktop" || !listed.Invites[0].Admin || listed.Invites[0].Status != "pending" {
		t.Fatalf("listed invites = %+v, want pending admin invite", listed.Invites)
	}
	if strings.Contains(listRec.Body.String(), "token_hash") || strings.Contains(listRec.Body.String(), token) {
		t.Fatalf("invites response exposed secret material: %s", listRec.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodPost, "/api/invites/revoke?node_id=node_admin", strings.NewReader(`{"invite_id":`+strconvQuote(listed.Invites[0].ID)+`}`))
	revokeReq.Header.Set("Authorization", "Bearer admin_secret")
	revokeRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(revokeRec, revokeReq)

	if revokeRec.Code != http.StatusOK {
		t.Fatalf("POST /api/invites/revoke status = %d, want %d; body=%q", revokeRec.Code, http.StatusOK, revokeRec.Body.String())
	}
	if _, err := server.store.ConsumeInvite(token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("ConsumeInvite revoked token = %v, want ErrInviteInvalid", err)
	}
}

func TestHubNodeWebSocketRequiresAuthentication(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	unauthReq := httptest.NewRequest(http.MethodGet, "/ws/node?node_id=node_1", nil)
	unauthRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /ws/node status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}

	badReq := httptest.NewRequest(http.MethodGet, "/ws/node?node_id=node_1", nil)
	badReq.Header.Set("Authorization", "Bearer wrong")
	badRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token /ws/node status = %d, want %d", badRec.Code, http.StatusUnauthorized)
	}

	goodReq := httptest.NewRequest(http.MethodGet, "/ws/node?node_id=node_1", nil)
	goodReq.Header.Set("Authorization", "Bearer node_secret")
	goodRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(goodRec, goodReq)
	if goodRec.Code == http.StatusUnauthorized {
		t.Fatal("authenticated /ws/node was rejected before websocket upgrade")
	}
}

func TestHubNodeWebSocketPersistsSnapshot(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	conn := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret")
	defer conn.Close()
	readTestWelcome(t, conn)

	snapshot, err := MarshalEnvelope(MsgSnapshot, "node_1", SnapshotPayload{
		SentAt: time.Unix(123, 0).UTC(),
		Sessions: []SessionInfo{{
			ID:        "s1",
			Title:     "worker",
			GroupPath: "default",
			Status:    "waiting",
		}},
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	if err := conn.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON snapshot: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("snapshot was not persisted")
		default:
		}
		got, err := server.store.LatestSessions()
		if err != nil {
			t.Fatalf("LatestSessions: %v", err)
		}
		if len(got) == 1 && len(got[0].Sessions) == 1 && got[0].Sessions[0].ID == "s1" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHubNodeWebSocketFansOutSnapshots(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret_1"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode node_1: %v", err)
	}
	if _, err := server.store.UpsertNode("node_2", "server1", hashSecret("node_secret_2"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode node_2: %v", err)
	}
	allowTestTrust(t, server, "node_1", "node_2")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	first := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret_1")
	defer first.Close()
	readTestWelcome(t, first)
	second := dialTestNodeWebSocket(t, httpServer.URL, "node_2", "node_secret_2")
	defer second.Close()
	readTestWelcome(t, second)

	snapshot, err := MarshalEnvelope(MsgSnapshot, "node_1", SnapshotPayload{
		SentAt: time.Unix(125, 0).UTC(),
		Sessions: []SessionInfo{{
			ID:        "s1",
			Title:     "worker",
			GroupPath: "ops",
			Status:    "waiting",
		}},
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope snapshot: %v", err)
	}
	if err := first.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON snapshot: %v", err)
	}

	got := readTestEnvelopeMatching(t, second, "snapshot from node_1 with sessions", func(env Envelope) bool {
		if env.Type != MsgSnapshot || env.NodeID != "node_1" {
			return false
		}
		var payload SnapshotPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return false
		}
		return len(payload.Sessions) == 1
	})
	var payload SnapshotPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("fanout payload: %v", err)
	}
	if payload.NodeName != "laptop" || len(payload.Sessions) != 1 || payload.Sessions[0].Title != "worker" {
		t.Fatalf("fanout payload = %+v", payload)
	}
}

func TestHubNodeRenameImmediatelyPublishesAuthoritativeName(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNodeWithAdmin("node_admin", "admin", hashSecret("admin_secret"), "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNode admin: %v", err)
	}
	if _, err := server.store.UpsertNode("node_owner", "old-laptop", hashSecret("owner_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode owner: %v", err)
	}
	allowTestTrust(t, server, "node_owner", "node_admin")
	allowTestTrust(t, server, "node_admin", "node_owner")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	adminConn := dialTestNodeWebSocket(t, httpServer.URL, "node_admin", "admin_secret")
	defer adminConn.Close()
	readTestWelcome(t, adminConn)
	ownerConn := dialTestNodeWebSocket(t, httpServer.URL, "node_owner", "owner_secret")
	defer ownerConn.Close()
	readTestWelcome(t, ownerConn)

	snapshot, err := MarshalEnvelope(MsgSnapshot, "node_owner", SnapshotPayload{
		NodeName: "stale-client-name",
		SentAt:   time.Unix(127, 0).UTC(),
		Sessions: []SessionInfo{{ID: "s1", Title: "worker", Status: "waiting"}},
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope snapshot: %v", err)
	}
	if err := ownerConn.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON snapshot: %v", err)
	}
	_ = readTestEnvelopeMatching(t, adminConn, "initial authoritative owner snapshot", func(env Envelope) bool {
		if env.Type != MsgSnapshot || env.NodeID != "node_owner" {
			return false
		}
		var payload SnapshotPayload
		return json.Unmarshal(env.Payload, &payload) == nil && len(payload.Sessions) == 1 && payload.NodeName == "old-laptop"
	})

	renameReq := httptest.NewRequest(http.MethodPost, "/api/nodes/rename?node_id=node_admin", strings.NewReader(`{"node_id":"node_owner","name":"work-laptop"}`))
	renameReq.Header.Set("Authorization", "Bearer admin_secret")
	renameRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(renameRec, renameReq)
	if renameRec.Code != http.StatusOK {
		t.Fatalf("rename status = %d, want 200; body=%q", renameRec.Code, renameRec.Body.String())
	}

	ownerWelcome := readTestEnvelopeMatching(t, ownerConn, "renamed owner welcome", func(env Envelope) bool {
		if env.Type != MsgWelcome || env.NodeID != "node_owner" {
			return false
		}
		var payload WelcomePayload
		return json.Unmarshal(env.Payload, &payload) == nil && payload.NodeName == "work-laptop"
	})
	var welcome WelcomePayload
	if err := json.Unmarshal(ownerWelcome.Payload, &welcome); err != nil {
		t.Fatalf("decode renamed welcome: %v", err)
	}
	if welcome.NodeName != "work-laptop" {
		t.Fatalf("renamed welcome = %+v", welcome)
	}

	renamed := readTestEnvelopeMatching(t, adminConn, "renamed owner snapshot", func(env Envelope) bool {
		if env.Type != MsgSnapshot || env.NodeID != "node_owner" {
			return false
		}
		var payload SnapshotPayload
		return json.Unmarshal(env.Payload, &payload) == nil && payload.NodeName == "work-laptop" && len(payload.Sessions) == 1
	})
	var renamedPayload SnapshotPayload
	if err := json.Unmarshal(renamed.Payload, &renamedPayload); err != nil {
		t.Fatalf("decode renamed snapshot: %v", err)
	}
	if renamedPayload.NodeName != "work-laptop" || renamedPayload.Sessions[0].ID != "s1" {
		t.Fatalf("renamed snapshot = %+v", renamedPayload)
	}

	// A connected owner still has its pre-rename ClientConfig name. Future
	// self-reported snapshots must not overwrite the registry's short name.
	if err := ownerConn.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON stale snapshot after rename: %v", err)
	}
	after := readTestEnvelopeMatching(t, adminConn, "post-rename authoritative owner snapshot", func(env Envelope) bool {
		if env.Type != MsgSnapshot || env.NodeID != "node_owner" {
			return false
		}
		var payload SnapshotPayload
		return json.Unmarshal(env.Payload, &payload) == nil && len(payload.Sessions) == 1
	})
	var afterPayload SnapshotPayload
	if err := json.Unmarshal(after.Payload, &afterPayload); err != nil {
		t.Fatalf("decode post-rename snapshot: %v", err)
	}
	if afterPayload.NodeName != "work-laptop" {
		t.Fatalf("post-rename snapshot name = %q, want authoritative work-laptop", afterPayload.NodeName)
	}

	promoteReq := httptest.NewRequest(http.MethodPost, "/api/nodes/promote?node_id=node_admin", strings.NewReader(`{"node_id":"node_owner"}`))
	promoteReq.Header.Set("Authorization", "Bearer admin_secret")
	promoteRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(promoteRec, promoteReq)
	if promoteRec.Code != http.StatusOK {
		t.Fatalf("promote status = %d, want 200; body=%q", promoteRec.Code, promoteRec.Body.String())
	}
	roleWelcome := readTestEnvelopeMatching(t, ownerConn, "promoted owner welcome", func(env Envelope) bool {
		if env.Type != MsgWelcome || env.NodeID != "node_owner" {
			return false
		}
		var payload WelcomePayload
		return json.Unmarshal(env.Payload, &payload) == nil && payload.NodeName == "work-laptop" && payload.Admin
	})
	if err := json.Unmarshal(roleWelcome.Payload, &welcome); err != nil {
		t.Fatalf("decode promoted welcome: %v", err)
	}
	if !welcome.Admin {
		t.Fatalf("promoted welcome = %+v, want admin", welcome)
	}
	roleSnapshot := readTestEnvelopeMatching(t, adminConn, "promoted owner snapshot", func(env Envelope) bool {
		if env.Type != MsgSnapshot || env.NodeID != "node_owner" {
			return false
		}
		var payload SnapshotPayload
		return json.Unmarshal(env.Payload, &payload) == nil && payload.NodeName == "work-laptop" && payload.Admin
	})
	if err := json.Unmarshal(roleSnapshot.Payload, &afterPayload); err != nil {
		t.Fatalf("decode promoted snapshot: %v", err)
	}
	if !afterPayload.Admin {
		t.Fatalf("promoted snapshot = %+v, want admin", afterPayload)
	}
}

func TestServerSnapshotEnvelopeIncludesGroupsAndWebAvailability(t *testing.T) {
	server := newTestServer(t)
	env, err := server.snapshotEnvelope(NodeSessions{
		Node:         Node{ID: "node_1", Name: "laptop"},
		SentAt:       time.Unix(126, 0).UTC(),
		WebAvailable: true,
		Sessions:     []SessionInfo{{ID: "s1", Title: "worker", Status: "waiting"}},
		Groups:       []GroupInfo{{Name: "ops", Path: "ops", DefaultPath: "/srv/ops"}},
	}, true)
	if err != nil {
		t.Fatalf("snapshotEnvelope: %v", err)
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !payload.WebAvailable {
		t.Fatal("WebAvailable = false, want true")
	}
	if len(payload.Groups) != 1 || payload.Groups[0].DefaultPath != "/srv/ops" {
		t.Fatalf("groups = %+v, want /srv/ops default path", payload.Groups)
	}
}

func TestHubNodeWebSocketClearsUntrustedSnapshotFanout(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret_1"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode node_1: %v", err)
	}
	if _, err := server.store.UpsertNode("node_2", "server1", hashSecret("node_secret_2"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode node_2: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	first := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret_1")
	defer first.Close()
	readTestWelcome(t, first)
	second := dialTestNodeWebSocket(t, httpServer.URL, "node_2", "node_secret_2")
	defer second.Close()
	readTestWelcome(t, second)

	snapshot, err := MarshalEnvelope(MsgSnapshot, "node_1", SnapshotPayload{
		SentAt:   time.Unix(125, 0).UTC(),
		Sessions: []SessionInfo{{ID: "s1", Title: "worker", Status: "waiting"}},
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope snapshot: %v", err)
	}
	if err := first.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON snapshot: %v", err)
	}

	got := readTestEnvelope(t, second)
	if got.Type != MsgSnapshot || got.NodeID != "node_1" {
		t.Fatalf("clear fanout envelope = %+v", got)
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("clear fanout payload: %v", err)
	}
	if payload.NodeName != "laptop" || len(payload.Sessions) != 0 {
		t.Fatalf("clear fanout payload = %+v, want laptop with no sessions", payload)
	}
}

func TestHubNodeWebSocketRoutesAttachRelay(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_requester", "laptop", hashSecret("requester_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode requester: %v", err)
	}
	if _, err := server.store.UpsertNode("node_owner", "workstation", hashSecret("owner_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode owner: %v", err)
	}
	allowTestTrust(t, server, "node_owner", "node_requester")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	requester := dialTestNodeWebSocket(t, httpServer.URL, "node_requester", "requester_secret")
	defer requester.Close()
	readTestWelcome(t, requester)
	owner := dialTestNodeWebSocket(t, httpServer.URL, "node_owner", "owner_secret")
	defer owner.Close()
	readTestWelcome(t, owner)

	open, err := MarshalEnvelope(MsgAttachOpen, "node_requester", AttachOpenPayload{
		StreamID:  "stream_1",
		NodeID:    "node_owner",
		SessionID: "sess_1",
		Cols:      120,
		Rows:      40,
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope open: %v", err)
	}
	if err := requester.WriteJSON(open); err != nil {
		t.Fatalf("requester WriteJSON open: %v", err)
	}
	_ = readTestEnvelopeOf(t, owner, MsgAttachOpen, "node_requester")

	input, err := MarshalEnvelope(MsgAttachData, "node_requester", NewAttachData("stream_1", []byte("input")))
	if err != nil {
		t.Fatalf("MarshalEnvelope input: %v", err)
	}
	if err := requester.WriteJSON(input); err != nil {
		t.Fatalf("requester WriteJSON input: %v", err)
	}
	gotInput := readTestEnvelopeOf(t, owner, MsgAttachData, "node_requester")
	assertAttachDataBytes(t, gotInput, "input")

	ready, err := MarshalEnvelope(MsgAttachReady, "node_owner", AttachOpenPayload{StreamID: "stream_1", SessionID: "sess_1", Cols: 120, Rows: 40})
	if err != nil {
		t.Fatalf("MarshalEnvelope ready: %v", err)
	}
	if err := owner.WriteJSON(ready); err != nil {
		t.Fatalf("owner WriteJSON ready: %v", err)
	}
	_ = readTestEnvelopeOf(t, requester, MsgAttachReady, "node_owner")

	output, err := MarshalEnvelope(MsgAttachData, "node_owner", NewAttachData("stream_1", []byte("output")))
	if err != nil {
		t.Fatalf("MarshalEnvelope output: %v", err)
	}
	if err := owner.WriteJSON(output); err != nil {
		t.Fatalf("owner WriteJSON output: %v", err)
	}
	gotOutput := readTestEnvelopeOf(t, requester, MsgAttachData, "node_owner")
	assertAttachDataBytes(t, gotOutput, "output")

	closed, err := MarshalEnvelope(MsgAttachClosed, "node_owner", AttachClosePayload{StreamID: "stream_1", Reason: "done"})
	if err != nil {
		t.Fatalf("MarshalEnvelope closed: %v", err)
	}
	if err := owner.WriteJSON(closed); err != nil {
		t.Fatalf("owner WriteJSON closed: %v", err)
	}
	_ = readTestEnvelopeOf(t, requester, MsgAttachClosed, "node_owner")
}

func TestHubNodeWebSocketRoutesCommandRelay(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_requester", "laptop", hashSecret("requester_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode requester: %v", err)
	}
	if _, err := server.store.UpsertNode("node_owner", "workstation", hashSecret("owner_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode owner: %v", err)
	}
	allowTestTrust(t, server, "node_owner", "node_requester")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	requester := dialTestNodeWebSocket(t, httpServer.URL, "node_requester", "requester_secret")
	defer requester.Close()
	readTestWelcome(t, requester)
	owner := dialTestNodeWebSocket(t, httpServer.URL, "node_owner", "owner_secret")
	defer owner.Close()
	readTestWelcome(t, owner)

	payload, err := json.Marshal(map[string]string{"session_id": "sess_1", "message": "run tests"})
	if err != nil {
		t.Fatalf("marshal action payload: %v", err)
	}
	command, err := MarshalEnvelope(MsgCommand, "node_requester", CommandPayload{
		CommandID: "cmd_1",
		NodeID:    "node_owner",
		Action:    "send",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope command: %v", err)
	}
	if err := requester.WriteJSON(command); err != nil {
		t.Fatalf("requester WriteJSON command: %v", err)
	}
	gotCommand := readTestEnvelopeOf(t, owner, MsgCommand, "node_requester")
	var gotPayload CommandPayload
	if err := json.Unmarshal(gotCommand.Payload, &gotPayload); err != nil {
		t.Fatalf("decode command payload: %v", err)
	}
	if gotPayload.CommandID != "cmd_1" || gotPayload.Action != "send" {
		t.Fatalf("command payload = %+v", gotPayload)
	}

	result, err := MarshalEnvelope(MsgCommandResult, "node_owner", CommandResultPayload{CommandID: "cmd_1", OK: true})
	if err != nil {
		t.Fatalf("MarshalEnvelope result: %v", err)
	}
	if err := owner.WriteJSON(result); err != nil {
		t.Fatalf("owner WriteJSON result: %v", err)
	}
	_ = readTestEnvelopeOf(t, requester, MsgCommandResult, "node_owner")
}

func TestServerCommandResultFromWrongOwnerPeerDoesNotCloseRoute(t *testing.T) {
	server := &Server{attachRouter: NewAttachRouter()}
	requester := newFakePeerConn("node_requester", "requester-a")
	owner := newFakePeerConn("node_owner", "owner-a")
	wrongOwnerPeer := newFakePeerConn("node_owner", "owner-b")
	server.attachRouter.Register(owner)

	if err := server.routeCommandFromPeer(context.Background(), requester, CommandPayload{
		CommandID: "cmd_1",
		NodeID:    "node_owner",
		Action:    "send",
	}); err != nil {
		t.Fatalf("routeCommandFromPeer: %v", err)
	}
	if len(owner.messages) != 1 || owner.messages[0].Type != MsgCommand {
		t.Fatalf("owner messages = %+v, want command", owner.messages)
	}

	err := server.routeCommandResultFromPeer(wrongOwnerPeer, CommandResultPayload{CommandID: "cmd_1", OK: true})
	if err == nil {
		t.Fatal("wrong owner peer result succeeded, want error")
	}
	if len(requester.messages) != 0 {
		t.Fatalf("requester received %+v from wrong owner peer", requester.messages)
	}
	server.mu.Lock()
	_, stillOpen := server.commandRoutes["cmd_1"]
	server.mu.Unlock()
	if !stillOpen {
		t.Fatal("wrong owner peer closed the command route")
	}

	if err := server.routeCommandResultFromPeer(owner, CommandResultPayload{CommandID: "cmd_1", OK: true}); err != nil {
		t.Fatalf("correct owner result: %v", err)
	}
	got := requester.pop(t)
	if got.Type != MsgCommandResult || got.NodeID != "node_owner" {
		t.Fatalf("requester result = %+v, want command result from node_owner", got)
	}
}

func TestHubNodeWebSocketAcceptsHeartbeat(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	conn := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret")
	defer conn.Close()
	readTestWelcome(t, conn)

	heartbeat, err := MarshalEnvelope(MsgHeartbeat, "node_1", nil)
	if err != nil {
		t.Fatalf("MarshalEnvelope heartbeat: %v", err)
	}
	if err := conn.WriteJSON(heartbeat); err != nil {
		t.Fatalf("WriteJSON heartbeat: %v", err)
	}
	snapshot, err := MarshalEnvelope(MsgSnapshot, "node_1", SnapshotPayload{
		SentAt:   time.Unix(124, 0).UTC(),
		Sessions: []SessionInfo{{ID: "after-heartbeat", Title: "worker"}},
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope snapshot: %v", err)
	}
	if err := conn.WriteJSON(snapshot); err != nil {
		t.Fatalf("WriteJSON after heartbeat: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("connection did not accept snapshot after heartbeat")
		default:
		}
		got, err := server.store.LatestSessions()
		if err != nil {
			t.Fatalf("LatestSessions: %v", err)
		}
		if len(got) == 1 && len(got[0].Sessions) == 1 && got[0].Sessions[0].ID == "after-heartbeat" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHubNodeWebSocketOverlappingConnectionsKeepNodeOnline(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.store.UpsertNode("node_1", "laptop", hashSecret("node_secret"), "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	first := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret")
	readTestWelcome(t, first)
	second := dialTestNodeWebSocket(t, httpServer.URL, "node_1", "node_secret")
	readTestWelcome(t, second)
	waitNodeStatus(t, server, "node_1", "online")

	if err := first.Close(); err != nil {
		t.Fatalf("close first websocket: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	assertNodeStatus(t, server, "node_1", "online")

	if err := second.Close(); err != nil {
		t.Fatalf("close second websocket: %v", err)
	}
	waitNodeStatus(t, server, "node_1", "offline")
}

func TestServerServeRequiresTLSCertAndKey(t *testing.T) {
	server := newTestServer(t)
	err := server.Serve()
	if err == nil {
		t.Fatal("Serve without TLS cert/key succeeded, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--tls-cert") || !strings.Contains(msg, "--tls-key") {
		t.Fatalf("Serve error = %q, want --tls-cert and --tls-key guidance", msg)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func strconvQuote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func dialTestNodeWebSocket(t *testing.T, serverURL, nodeID, token string) *websocket.Conn {
	t.Helper()
	u := "ws://" + strings.TrimPrefix(serverURL, "http://") + "/ws/node?node_id=" + nodeID
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		t.Fatalf("Dial node websocket: %v", err)
	}
	return conn
}

func readTestWelcome(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	var env Envelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("ReadJSON welcome: %v", err)
	}
	if env.Type != MsgWelcome {
		t.Fatalf("welcome type = %q, want %q", env.Type, MsgWelcome)
	}
}

func readTestEnvelope(t *testing.T, conn *websocket.Conn) Envelope {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	var env Envelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("ReadJSON envelope: %v", err)
	}
	return env
}

func readTestEnvelopeOf(t *testing.T, conn *websocket.Conn, typ MessageType, nodeID string) Envelope {
	t.Helper()
	return readTestEnvelopeMatching(t, conn, string(typ)+" from "+nodeID, func(env Envelope) bool {
		return env.Type == typ && env.NodeID == nodeID
	})
}

func readTestEnvelopeMatching(t *testing.T, conn *websocket.Conn, desc string, match func(Envelope) bool) Envelope {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var last Envelope
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			t.Fatalf("ReadJSON waiting for %s: %v (last=%+v)", desc, err, last)
		}
		last = env
		if match(env) {
			return env
		}
	}
}

func allowTestTrust(t *testing.T, server *Server, ownerNodeID, requesterNodeID string) {
	t.Helper()
	if err := server.store.AllowTrust(ownerNodeID, requesterNodeID); err != nil {
		t.Fatalf("AllowTrust %s <- %s: %v", ownerNodeID, requesterNodeID, err)
	}
}

func waitNodeStatus(t *testing.T, server *Server, nodeID, want string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			assertNodeStatus(t, server, nodeID, want)
			return
		default:
		}
		if nodeStatus(t, server, nodeID) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertNodeStatus(t *testing.T, server *Server, nodeID, want string) {
	t.Helper()
	if got := nodeStatus(t, server, nodeID); got != want {
		t.Fatalf("node %s status = %q, want %q", nodeID, got, want)
	}
}

func nodeStatus(t *testing.T, server *Server, nodeID string) string {
	t.Helper()
	nodes, err := server.store.Nodes()
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	for _, node := range nodes {
		if node.ID == nodeID {
			return node.Status
		}
	}
	t.Fatalf("node %s not found", nodeID)
	return ""
}
