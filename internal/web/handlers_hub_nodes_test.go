package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestWebHubNodesAdminListsConfiguredHubNodes(t *testing.T) {
	home := t.TempDir()
	setWebHubConfigEnv(t, home)

	seen := make(chan struct{}, 1)
	hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.URL.Query().Get("node_id"); got != "node_admin" {
			t.Errorf("node_id = %q, want node_admin", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
			t.Errorf("Authorization = %q, want bearer admin token", got)
		}
		seen <- struct{}{}
		writeJSON(w, http.StatusOK, HubNodesAdminResponse{Nodes: []HubNodeAdmin{{
			ID:     "node_remote",
			Name:   "desktop",
			Status: "online",
			Admin:  true,
		}}})
	}))
	defer hubServer.Close()
	writeWebHubConfig(t, home, hubServer, "node_admin", "admin_secret")

	server := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})
	req := httptest.NewRequest(http.MethodGet, "/api/hub/nodes", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/hub/nodes status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	var got HubNodesAdminResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].ID != "node_remote" || got.Nodes[0].Name != "desktop" || !got.Nodes[0].Admin {
		t.Fatalf("nodes response = %+v", got)
	}
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("web hub nodes endpoint did not call configured hub")
	}
}

func TestWebHubNodeRenameUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	setWebHubConfigEnv(t, home)

	seen := make(chan map[string]string, 1)
	hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes/rename" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.URL.Query().Get("node_id"); got != "node_admin" {
			t.Errorf("node_id = %q, want node_admin", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
			t.Errorf("Authorization = %q, want bearer admin token", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		seen <- body
		writeJSON(w, http.StatusOK, HubNodeAdmin{ID: "node_remote", Name: "desktop", Status: "online"})
	}))
	defer hubServer.Close()
	writeWebHubConfig(t, home, hubServer, "node_admin", "admin_secret")

	menu := NewMemoryMenuData(nil)
	menu.SetSnapshot(&MenuSnapshot{
		Profile:     "test",
		GeneratedAt: time.Unix(1, 0).UTC(),
		HubNodes:    []HubNode{{ID: "node_remote", Name: "old-name"}},
		Items: []MenuItem{
			{Type: MenuItemTypeGroup, Group: &MenuGroup{Path: "node_remote/default", HubNodeID: "node_remote", HubNodeName: "old-name"}},
			{Type: MenuItemTypeSession, Session: &MenuSession{ID: "s1", Title: "remote", HubNodeID: "node_remote", HubNodeName: "old-name"}},
		},
	})

	server := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: menu})
	req := httptest.NewRequest(http.MethodPatch, "/api/hub/nodes/node_remote", strings.NewReader(`{"name":"desktop"}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/hub/nodes status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	var renamed HubNodeAdmin
	if err := json.Unmarshal(rec.Body.Bytes(), &renamed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if renamed.ID != "node_remote" || renamed.Name != "desktop" {
		t.Fatalf("rename response = %+v", renamed)
	}
	select {
	case got := <-seen:
		if got["node_id"] != "node_remote" || got["name"] != "desktop" {
			t.Fatalf("rename request = %+v, want node_remote desktop", got)
		}
	case <-time.After(time.Second):
		t.Fatal("web hub node rename endpoint did not call configured hub")
	}

	snapshot, err := menu.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot: %v", err)
	}
	if snapshot.HubNodes[0].Name != "desktop" {
		t.Fatalf("cached hub node name = %q, want desktop", snapshot.HubNodes[0].Name)
	}
	if snapshot.Items[0].Group.HubNodeName != "desktop" || snapshot.Items[1].Session.HubNodeName != "desktop" {
		t.Fatalf("cached hub item node names not updated: %+v %+v", snapshot.Items[0].Group, snapshot.Items[1].Session)
	}
}

func TestWebHubNodeAdminActionsUseConfiguredAdminNode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		method     string
		webPath    string
		remotePath string
		wantAdmin  bool
	}{
		{name: "promote", method: http.MethodPost, webPath: "/api/hub/nodes/node_remote/promote", remotePath: "/api/nodes/promote", wantAdmin: true},
		{name: "demote", method: http.MethodPost, webPath: "/api/hub/nodes/node_remote/demote", remotePath: "/api/nodes/demote", wantAdmin: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setWebHubConfigEnv(t, home)

			seen := make(chan map[string]string, 1)
			hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.remotePath {
					http.NotFound(w, r)
					return
				}
				if got := r.URL.Query().Get("node_id"); got != "node_admin" {
					t.Errorf("node_id = %q, want node_admin", got)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
					t.Errorf("Authorization = %q, want bearer admin token", got)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode body: %v", err)
				}
				seen <- body
				writeJSON(w, http.StatusOK, HubNodeAdmin{ID: "node_remote", Name: "desktop", Status: "online", Admin: tc.wantAdmin})
			}))
			defer hubServer.Close()
			writeWebHubConfig(t, home, hubServer, "node_admin", "admin_secret")

			server := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})
			req := httptest.NewRequest(tc.method, tc.webPath, nil)
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s status = %d, want 200; body=%q", tc.method, tc.webPath, rec.Code, rec.Body.String())
			}
			var got HubNodeAdmin
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.ID != "node_remote" || got.Admin != tc.wantAdmin {
				t.Fatalf("node response = %+v, want admin=%v", got, tc.wantAdmin)
			}
			select {
			case body := <-seen:
				if body["node_id"] != "node_remote" {
					t.Fatalf("admin request = %+v, want node_remote", body)
				}
			case <-time.After(time.Second):
				t.Fatalf("%s did not call configured hub", tc.name)
			}
		})
	}
}

func TestWebHubNodeRevokeUsesConfiguredAdminNodeAndRemovesMenuProjection(t *testing.T) {
	home := t.TempDir()
	setWebHubConfigEnv(t, home)

	seen := make(chan map[string]string, 1)
	hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes/revoke" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("node_id"); got != "node_admin" {
			t.Errorf("node_id = %q, want node_admin", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
			t.Errorf("Authorization = %q, want bearer admin token", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		seen <- body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer hubServer.Close()
	writeWebHubConfig(t, home, hubServer, "node_admin", "admin_secret")

	menu := NewMemoryMenuData(nil)
	menu.SetSnapshot(&MenuSnapshot{
		Profile:       "test",
		GeneratedAt:   time.Unix(1, 0).UTC(),
		TotalGroups:   1,
		TotalSessions: 1,
		HubNodes:      []HubNode{{ID: "node_remote", Name: "desktop"}},
		Items: []MenuItem{
			{Index: 0, Type: MenuItemTypeGroup, Group: &MenuGroup{Path: "node_remote/default", HubNodeID: "node_remote", HubNodeName: "desktop"}},
			{Index: 1, Type: MenuItemTypeSession, Session: &MenuSession{ID: "hub/node_remote/s1", Title: "remote", HubNodeID: "node_remote", HubNodeName: "desktop"}},
		},
	})

	server := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: menu})
	req := httptest.NewRequest(http.MethodDelete, "/api/hub/nodes/node_remote", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/hub/nodes status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	select {
	case body := <-seen:
		if body["node_id"] != "node_remote" {
			t.Fatalf("revoke request = %+v, want node_remote", body)
		}
	case <-time.After(time.Second):
		t.Fatal("web hub node revoke endpoint did not call configured hub")
	}
	snapshot, err := menu.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot: %v", err)
	}
	if len(snapshot.HubNodes) != 0 || len(snapshot.Items) != 0 || snapshot.TotalGroups != 0 || snapshot.TotalSessions != 0 {
		t.Fatalf("snapshot after revoke = nodes:%+v items:%+v groups:%d sessions:%d", snapshot.HubNodes, snapshot.Items, snapshot.TotalGroups, snapshot.TotalSessions)
	}
}

func TestWebHubInvitesAdminUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	setWebHubConfigEnv(t, home)

	seenCreate := make(chan map[string]any, 1)
	hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("node_id"); got != "node_admin" {
			t.Errorf("node_id = %q, want node_admin", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
			t.Errorf("Authorization = %q, want bearer admin token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/invites":
			writeJSON(w, http.StatusOK, map[string]any{"invites": []map[string]any{{
				"id":                 "inv_remote",
				"node_name":          "gpu",
				"expires_at":         time.Unix(123, 0).UTC(),
				"admin":              true,
				"created_by_node_id": "node_admin",
				"status":             "pending",
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/invites":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create invite body: %v", err)
			}
			seenCreate <- body
			writeJSON(w, http.StatusOK, map[string]any{
				"url":          "wss://hub.example",
				"invite_token": "invite_remote",
				"expires_at":   time.Unix(456, 0).UTC(),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer hubServer.Close()
	writeWebHubConfig(t, home, hubServer, "node_admin", "admin_secret")

	server := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})
	listReq := httptest.NewRequest(http.MethodGet, "/api/hub/invites", nil)
	listRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/hub/invites status = %d, want 200; body=%q", listRec.Code, listRec.Body.String())
	}
	var listed HubInvitesAdminResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Invites) != 1 || listed.Invites[0].ID != "inv_remote" || listed.Invites[0].NodeName != "gpu" || !listed.Invites[0].Admin {
		t.Fatalf("listed invites = %+v", listed.Invites)
	}
	if strings.Contains(listRec.Body.String(), "invite_") {
		t.Fatalf("invite list leaked token material: %s", listRec.Body.String())
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/hub/invites", strings.NewReader(`{"nodeName":"gpu","ttlSeconds":3600,"admin":true}`))
	createRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("POST /api/hub/invites status = %d, want 200; body=%q", createRec.Code, createRec.Body.String())
	}
	var created CreateHubInviteResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.InviteToken != "invite_remote" || created.JoinCommand != "agent-deck hub join wss://hub.example --token invite_remote" {
		t.Fatalf("create response = %+v", created)
	}
	select {
	case body := <-seenCreate:
		if body["node_name"] != "gpu" || body["admin"] != true || body["ttl_seconds"].(float64) != 3600 {
			t.Fatalf("create invite request = %+v", body)
		}
	case <-time.After(time.Second):
		t.Fatal("web hub invite create endpoint did not call configured hub")
	}
}

func TestWebHubInviteRevokeUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	setWebHubConfigEnv(t, home)

	seen := make(chan map[string]string, 1)
	hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/invites/revoke" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("node_id"); got != "node_admin" {
			t.Errorf("node_id = %q, want node_admin", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
			t.Errorf("Authorization = %q, want bearer admin token", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode revoke invite body: %v", err)
		}
		seen <- body
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	defer hubServer.Close()
	writeWebHubConfig(t, home, hubServer, "node_admin", "admin_secret")

	server := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})
	req := httptest.NewRequest(http.MethodDelete, "/api/hub/invites/inv_remote", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/hub/invites status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	select {
	case body := <-seen:
		if body["invite_id"] != "inv_remote" {
			t.Fatalf("revoke invite request = %+v, want inv_remote", body)
		}
	case <-time.After(time.Second):
		t.Fatal("web hub invite revoke endpoint did not call configured hub")
	}
}

func TestWebHubTrustAdminUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	setWebHubConfigEnv(t, home)

	seenDecision := make(chan map[string]string, 1)
	hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("node_id"); got != "node_admin" {
			t.Errorf("node_id = %q, want node_admin", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
			t.Errorf("Authorization = %q, want bearer admin token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/trust/pending":
			writeJSON(w, http.StatusOK, map[string]any{"requests": []map[string]any{{
				"node_id":   "node_requester",
				"node_name": "gpu",
				"version":   "1.2.3",
				"os":        "linux",
				"arch":      "amd64",
				"status":    "pending",
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/trust/allow":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode trust decision body: %v", err)
			}
			seenDecision <- body
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer hubServer.Close()
	writeWebHubConfig(t, home, hubServer, "node_admin", "admin_secret")

	server := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})
	listReq := httptest.NewRequest(http.MethodGet, "/api/hub/trust/pending", nil)
	listRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/hub/trust/pending status = %d, want 200; body=%q", listRec.Code, listRec.Body.String())
	}
	var listed HubTrustRequestsAdminResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode trust list response: %v", err)
	}
	if len(listed.Requests) != 1 || listed.Requests[0].NodeID != "node_requester" || listed.Requests[0].NodeName != "gpu" {
		t.Fatalf("trust requests = %+v", listed.Requests)
	}

	allowReq := httptest.NewRequest(http.MethodPost, "/api/hub/trust/node_requester/allow", nil)
	allowRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowRec, allowReq)
	if allowRec.Code != http.StatusOK {
		t.Fatalf("POST /api/hub/trust allow status = %d, want 200; body=%q", allowRec.Code, allowRec.Body.String())
	}
	select {
	case body := <-seenDecision:
		if body["node_id"] != "node_requester" {
			t.Fatalf("trust decision request = %+v, want node_requester", body)
		}
	case <-time.After(time.Second):
		t.Fatal("web hub trust allow endpoint did not call configured hub")
	}
}

func setWebHubConfigEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)
}

func writeWebHubConfig(t *testing.T, home string, server *httptest.Server, nodeID, token string) {
	t.Helper()
	tokenFile := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenFile, []byte(" "+token+" \n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if err := session.SaveUserConfig(&session.UserConfig{Hub: session.HubSettings{
		URL:           "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:        nodeID,
		NodeName:      "admin",
		TokenFile:     tokenFile,
		TLSSkipVerify: true,
	}}); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	session.ClearUserConfigCache()
}
