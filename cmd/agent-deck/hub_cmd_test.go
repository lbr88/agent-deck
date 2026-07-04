package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/gorilla/websocket"
)

func TestHubCommandIsRoutedFromMain(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(data), `case "hub":`) || !strings.Contains(string(data), "handleHub(") {
		t.Fatalf("main.go must route agent-deck hub commands")
	}
}

func TestSaveHubJoinConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	cfg := &session.UserConfig{}
	tokenPath := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	if err := saveHubJoinConfig(cfg, hubJoinResult{
		URL:              "wss://hub.local:8421",
		NodeID:           "node_abc",
		NodeName:         "laptop",
		NodeToken:        "adhn_secret",
		TokenPath:        tokenPath,
		PinnedCertSHA256: "abc123",
	}); err != nil {
		t.Fatalf("saveHubJoinConfig: %v", err)
	}
	if cfg.Hub.URL != "wss://hub.local:8421" || cfg.Hub.NodeID != "node_abc" || cfg.Hub.NodeName != "laptop" || !cfg.Hub.AutoConnect {
		t.Fatalf("hub config = %+v", cfg.Hub)
	}
	if cfg.Hub.PinnedCertSHA256 != "abc123" {
		t.Fatalf("PinnedCertSHA256 = %q, want abc123", cfg.Hub.PinnedCertSHA256)
	}
	if cfg.Hub.TokenFile != tokenPath {
		t.Fatalf("TokenFile = %q, want %q", cfg.Hub.TokenFile, tokenPath)
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if strings.TrimSpace(string(data)) != "adhn_secret" {
		t.Fatalf("token file = %q", string(data))
	}
}

func TestSaveHubJoinConfigTightensExistingTokenFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not expose unix permission bits")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	tokenPath := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write existing token: %v", err)
	}

	if err := saveHubJoinConfig(&session.UserConfig{}, hubJoinResult{
		URL:              "wss://hub.local:8421",
		NodeID:           "node_abc",
		NodeName:         "laptop",
		NodeToken:        "adhn_secret",
		TokenPath:        tokenPath,
		PinnedCertSHA256: "abc123",
	}); err != nil {
		t.Fatalf("saveHubJoinConfig: %v", err)
	}
	st, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("token mode = %o, want 600", got)
	}
}

func TestHubJoinDoesNotOverwriteConfigWhenLoadFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	configPath := filepath.Join(home, ".config", "agent-deck", session.UserConfigFileName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	badConfig := []byte("[hub\n")
	if err := os.WriteFile(configPath, badConfig, 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	tokenPath := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	server := newJoinResponseServer(t, `{"url":"","node_id":"node_abc","node_name":"laptop","node_token":"adhn_secret"}`)

	err := handleHubJoin([]string{server.wssURL(), "--token", "invite_abc", "--tls-skip-verify", "--token-file", tokenPath})

	if err == nil {
		t.Fatal("handleHubJoin succeeded with unparseable config, want error")
	}
	gotConfig, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(gotConfig) != string(badConfig) {
		t.Fatalf("config was overwritten:\n%s", string(gotConfig))
	}
	if _, statErr := os.Stat(tokenPath); !os.IsNotExist(statErr) {
		t.Fatalf("token file stat error = %v, want not exist", statErr)
	}
}

func TestHubJoinRejectsPlaintextURL(t *testing.T) {
	for _, raw := range []string{"http://hub.local:8421", "ws://hub.local:8421", " hub.local:8421 "} {
		if err := validateHubJoinURL(raw); err == nil {
			t.Fatalf("validateHubJoinURL(%q) succeeded, want plaintext rejection", raw)
		}
	}
	if err := validateHubJoinURL("wss://hub.local:8421"); err != nil {
		t.Fatalf("validateHubJoinURL(wss) = %v", err)
	}
}

func TestHubServeDefaultsToSelfSignedCertificate(t *testing.T) {
	dataDir := t.TempDir()

	certFile, keyFile, generated, err := resolveHubServeTLSFiles(dataDir, "", "")
	if err != nil {
		t.Fatalf("resolveHubServeTLSFiles: %v", err)
	}
	if certFile != filepath.Join(dataDir, "hub-self-signed.crt") {
		t.Fatalf("certFile = %q, want default self-signed path", certFile)
	}
	if keyFile != filepath.Join(dataDir, "hub-self-signed.key") {
		t.Fatalf("keyFile = %q, want default self-signed key path", keyFile)
	}
	if !generated {
		t.Fatal("first default TLS resolution should report generated=true")
	}
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		t.Fatalf("generated certificate pair is not loadable: %v", err)
	}
	if st, err := os.Stat(keyFile); err != nil {
		t.Fatalf("stat key file: %v", err)
	} else if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o, want 600", st.Mode().Perm())
	}

	_, _, generated, err = resolveHubServeTLSFiles(dataDir, "", "")
	if err != nil {
		t.Fatalf("second resolveHubServeTLSFiles: %v", err)
	}
	if generated {
		t.Fatal("second default TLS resolution should reuse existing cert")
	}
}

func TestHubServeRejectsPartialCustomCertificateFlags(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, _, err := resolveHubServeTLSFiles(dataDir, "cert.pem", ""); err == nil {
		t.Fatal("resolveHubServeTLSFiles accepted cert without key")
	}
	if _, _, _, err := resolveHubServeTLSFiles(dataDir, "", "key.pem"); err == nil {
		t.Fatal("resolveHubServeTLSFiles accepted key without cert")
	}
}

func TestHubServeKeepsExplicitCertificatePair(t *testing.T) {
	certFile, keyFile, generated, err := resolveHubServeTLSFiles(t.TempDir(), " cert.pem ", " key.pem ")
	if err != nil {
		t.Fatalf("resolveHubServeTLSFiles: %v", err)
	}
	if certFile != "cert.pem" || keyFile != "key.pem" {
		t.Fatalf("cert/key = %q/%q, want trimmed explicit paths", certFile, keyFile)
	}
	if generated {
		t.Fatal("explicit certificate pair should not report generated=true")
	}
}

func TestHubServePersistsAdvertiseURL(t *testing.T) {
	dataDir := t.TempDir()

	url, err := configureHubAdvertiseURL(dataDir, "wss://hub.example:8421", ":8421")
	if err != nil {
		t.Fatalf("configureHubAdvertiseURL: %v", err)
	}
	if url != "wss://hub.example:8421" {
		t.Fatalf("advertise URL = %q, want configured URL", url)
	}

	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	got, err := store.AdvertiseURL()
	if err != nil {
		t.Fatalf("AdvertiseURL: %v", err)
	}
	if got != "wss://hub.example:8421" {
		t.Fatalf("stored advertise URL = %q, want configured URL", got)
	}
}

func TestHubServeAdvertiseURLFlagBeatsEnv(t *testing.T) {
	t.Setenv("AGENT_DECK_HUB_URL", "wss://env.example:8421")

	got := hubServeAdvertiseURL(" wss://flag.example:8421 ")

	if got != "wss://flag.example:8421" {
		t.Fatalf("hubServeAdvertiseURL = %q, want flag value", got)
	}
}

func TestHubServeAdvertiseURLFallsBackToEnv(t *testing.T) {
	t.Setenv("AGENT_DECK_HUB_URL", " wss://env.example:8421 ")

	got := hubServeAdvertiseURL("")

	if got != "wss://env.example:8421" {
		t.Fatalf("hubServeAdvertiseURL = %q, want env value", got)
	}
}

func TestHubServeDerivesAdvertiseURLFromListen(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_DECK_HUB_URL", "")

	url, err := configureHubAdvertiseURL(dataDir, "", "127.0.0.1:8421")
	if err != nil {
		t.Fatalf("configureHubAdvertiseURL: %v", err)
	}
	if url != "wss://127.0.0.1:8421" {
		t.Fatalf("advertise URL = %q, want URL derived from listen address", url)
	}

	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	got, err := store.AdvertiseURL()
	if err != nil {
		t.Fatalf("AdvertiseURL: %v", err)
	}
	if got != "wss://127.0.0.1:8421" {
		t.Fatalf("stored advertise URL = %q, want derived URL", got)
	}
}

func TestHubServeRejectsPlaintextAdvertiseURL(t *testing.T) {
	if _, err := configureHubAdvertiseURL(t.TempDir(), "http://hub.example:8421", ":8421"); err == nil {
		t.Fatal("configureHubAdvertiseURL accepted plaintext URL, want error")
	}
}

func TestHubServeBootstrapAdminCreatesFirstInvite(t *testing.T) {
	dataDir := t.TempDir()

	result, err := createBootstrapAdminInviteIfNeeded(dataDir, "wss://hub.example:8421", "lbr-lap", time.Hour)
	if err != nil {
		t.Fatalf("createBootstrapAdminInviteIfNeeded: %v", err)
	}
	if !result.Created {
		t.Fatal("bootstrap invite Created = false, want true for empty hub")
	}
	if !strings.HasPrefix(result.JoinCommand, "agent-deck hub join wss://hub.example:8421 --token invite_") {
		t.Fatalf("bootstrap join command = %q", result.JoinCommand)
	}

	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	invite, err := store.ConsumeInvite(result.InviteToken)
	if err != nil {
		t.Fatalf("ConsumeInvite bootstrap token: %v", err)
	}
	if invite.NodeName != "lbr-lap" || !invite.Admin {
		t.Fatalf("bootstrap invite = %+v, want admin invite for lbr-lap", invite)
	}
}

func TestHubServeBootstrapAdminSkipsWhenNodesExist(t *testing.T) {
	dataDir := t.TempDir()
	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := store.UpsertNode("node_1", "existing", "secret_hash", "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := createBootstrapAdminInviteIfNeeded(dataDir, "wss://hub.example:8421", "lbr-lap", time.Hour)
	if err != nil {
		t.Fatalf("createBootstrapAdminInviteIfNeeded: %v", err)
	}
	if result.Created || result.JoinCommand != "" || result.InviteToken != "" {
		t.Fatalf("bootstrap result = %+v, want skipped when nodes exist", result)
	}
}

func TestHubInvitePrintsJoinCommandFromHubMetadata(t *testing.T) {
	dataDir := t.TempDir()
	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := store.SetAdvertiseURL("wss://hub.example:8421"); err != nil {
		t.Fatalf("SetAdvertiseURL: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := captureStdout(t, func() {
		if err := handleHubInvite([]string{"--data", dataDir, "laptop"}); err != nil {
			t.Fatalf("handleHubInvite: %v", err)
		}
	})
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, "agent-deck hub join wss://hub.example:8421 --token invite_") {
		t.Fatalf("invite output = %q, want full join command", out)
	}
	if strings.Contains(out, "\n") {
		t.Fatalf("invite output should be one command, got %q", out)
	}
}

func TestHubInviteUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	type remoteInviteRequest struct {
		NodeName   string `json:"node_name"`
		TTLSeconds int64  `json:"ttl_seconds"`
		Admin      bool   `json:"admin"`
	}
	seen := make(chan remoteInviteRequest, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/invites" {
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
		var req remoteInviteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		seen <- req
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"url":          "wss://hub.example:8421",
			"invite_token": "invite_remote",
			"expires_at":   time.Now().Add(time.Hour),
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	tokenFile := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenFile, []byte(" admin_secret \n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if err := session.SaveUserConfig(&session.UserConfig{Hub: session.HubSettings{
		URL:           "wss://" + strings.TrimPrefix(server.URL, "https://"),
		NodeID:        "node_admin",
		NodeName:      "laptop",
		TokenFile:     tokenFile,
		TLSSkipVerify: true,
	}}); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	session.ClearUserConfigCache()

	out := captureStdout(t, func() {
		if err := handleHubInvite([]string{"--admin", "work-laptop"}); err != nil {
			t.Fatalf("handleHubInvite: %v", err)
		}
	})
	if strings.TrimSpace(out) != "agent-deck hub join wss://hub.example:8421 --token invite_remote" {
		t.Fatalf("invite output = %q, want full remote join command", out)
	}
	select {
	case req := <-seen:
		if req.NodeName != "work-laptop" || req.TTLSeconds <= 0 || !req.Admin {
			t.Fatalf("remote invite request = %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("hub invite did not call configured hub")
	}
}

func TestHubNodesUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	seen := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"nodes": []map[string]any{{
				"id":      "node_remote",
				"name":    "desktop",
				"version": "1.0.0",
				"os":      "linux",
				"arch":    "amd64",
				"status":  "online",
				"admin":   true,
			}},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	writeTestHubConfig(t, home, server, "node_admin", "admin_secret")

	out := captureStdout(t, func() {
		if err := handleHubNodes([]string{"--json"}); err != nil {
			t.Fatalf("handleHubNodes: %v", err)
		}
	})
	if !strings.Contains(out, "node_remote") || !strings.Contains(out, "desktop") || !strings.Contains(out, `"admin":true`) {
		t.Fatalf("remote nodes output = %s, want remote admin node data", out)
	}
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("hub nodes did not call configured hub")
	}
}

func TestHubNodesPromoteUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	seen := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes/promote" {
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
		var req struct {
			NodeID string `json:"node_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		seen <- req.NodeID
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(hubNodeOutput{
			ID:     req.NodeID,
			Name:   "desktop",
			Status: "offline",
			Admin:  true,
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	writeTestHubConfig(t, home, server, "node_admin", "admin_secret")

	out := captureStdout(t, func() {
		if err := handleHubNodes([]string{"promote", "node_remote"}); err != nil {
			t.Fatalf("handleHubNodes promote: %v", err)
		}
	})
	if !strings.Contains(out, "node_remote") || !strings.Contains(strings.ToLower(out), "admin") {
		t.Fatalf("remote promote output = %q, want node/admin confirmation", out)
	}
	select {
	case got := <-seen:
		if got != "node_remote" {
			t.Fatalf("promote request node_id = %q, want node_remote", got)
		}
	case <-time.After(time.Second):
		t.Fatal("hub nodes promote did not call configured hub")
	}
}

func TestHubStatusUsesConfiguredNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	seen := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
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
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"url": "wss://hub.example:8421",
			"node": map[string]any{
				"id":     "node_admin",
				"name":   "admin",
				"status": "online",
				"admin":  true,
			},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	writeTestHubConfig(t, home, server, "node_admin", "admin_secret")

	out := captureStdout(t, func() {
		if err := handleHubStatus([]string{}); err != nil {
			t.Fatalf("handleHubStatus: %v", err)
		}
	})
	if !strings.Contains(out, "wss://hub.example:8421") || !strings.Contains(out, "node_admin") || !strings.Contains(strings.ToLower(out), "admin") {
		t.Fatalf("status output = %q, want hub URL and admin node", out)
	}
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("hub status did not call configured hub")
	}
}

func TestHubNodesAdminSubcommandsUseConfiguredAdminNode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		path     string
		wantBody map[string]string
	}{
		{
			name:     "demote",
			args:     []string{"demote", "node_remote"},
			path:     "/api/nodes/demote",
			wantBody: map[string]string{"node_id": "node_remote"},
		},
		{
			name:     "rename",
			args:     []string{"rename", "node_remote", "desktop"},
			path:     "/api/nodes/rename",
			wantBody: map[string]string{"node_id": "node_remote", "name": "desktop"},
		},
		{
			name:     "revoke",
			args:     []string{"revoke", "node_remote"},
			path:     "/api/nodes/revoke",
			wantBody: map[string]string{"node_id": "node_remote"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			session.ClearUserConfigCache()
			t.Cleanup(session.ClearUserConfigCache)

			seen := make(chan map[string]string, 1)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					http.NotFound(w, r)
					return
				}
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if got := r.URL.Query().Get("node_id"); got != "node_admin" {
					t.Errorf("node_id query = %q, want node_admin", got)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
					t.Errorf("Authorization = %q, want bearer admin token", got)
				}
				var req map[string]string
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
				}
				seen <- req
				w.Header().Set("Content-Type", "application/json")
				if tc.name == "revoke" {
					if err := json.NewEncoder(w).Encode(map[string]bool{"ok": true}); err != nil {
						t.Errorf("encode revoke response: %v", err)
					}
					return
				}
				if err := json.NewEncoder(w).Encode(hubNodeOutput{
					ID:     "node_remote",
					Name:   "desktop",
					Status: "offline",
					Admin:  tc.name != "demote",
				}); err != nil {
					t.Errorf("encode node response: %v", err)
				}
			}))
			defer server.Close()
			writeTestHubConfig(t, home, server, "node_admin", "admin_secret")

			out := captureStdout(t, func() {
				if err := handleHubNodes(tc.args); err != nil {
					t.Fatalf("handleHubNodes %s: %v", tc.name, err)
				}
			})
			if !strings.Contains(out, "node_remote") {
				t.Fatalf("%s output = %q, want target node", tc.name, out)
			}
			select {
			case got := <-seen:
				for key, want := range tc.wantBody {
					if got[key] != want {
						t.Fatalf("%s request[%s] = %q, want %q; request=%+v", tc.name, key, got[key], want, got)
					}
				}
			case <-time.After(time.Second):
				t.Fatalf("hub nodes %s did not call configured hub", tc.name)
			}
		})
	}
}

func TestHubInvitesUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	seen := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/invites" {
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
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"invites": []map[string]any{{
				"id":         "inv_remote",
				"node_name":  "desktop",
				"status":     "pending",
				"admin":      true,
				"expires_at": time.Now().Add(time.Hour),
			}},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	writeTestHubConfig(t, home, server, "node_admin", "admin_secret")

	out := captureStdout(t, func() {
		if err := handleHubInvites([]string{"--json"}); err != nil {
			t.Fatalf("handleHubInvites: %v", err)
		}
	})
	if !strings.Contains(out, "inv_remote") || !strings.Contains(out, "desktop") || !strings.Contains(out, "pending") {
		t.Fatalf("invites output = %s, want remote invite data", out)
	}
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("hub invites did not call configured hub")
	}
}

func TestHubInvitesRevokeUsesConfiguredAdminNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	seen := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/invites/revoke" {
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
		var req struct {
			InviteID string `json:"invite_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		seen <- req.InviteID
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]bool{"ok": true}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	writeTestHubConfig(t, home, server, "node_admin", "admin_secret")

	out := captureStdout(t, func() {
		if err := handleHubInvites([]string{"revoke", "inv_remote"}); err != nil {
			t.Fatalf("handleHubInvites revoke: %v", err)
		}
	})
	if !strings.Contains(out, "inv_remote") {
		t.Fatalf("revoke invite output = %q, want invite id", out)
	}
	select {
	case got := <-seen:
		if got != "inv_remote" {
			t.Fatalf("revoke invite id = %q, want inv_remote", got)
		}
	case <-time.After(time.Second):
		t.Fatal("hub invites revoke did not call configured hub")
	}
}

func TestHubTrustPendingUsesConfiguredNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	seen := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/trust/pending" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.URL.Query().Get("node_id"); got != "node_owner" {
			t.Errorf("node_id = %q, want node_owner", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer owner_secret" {
			t.Errorf("Authorization = %q, want bearer owner token", got)
		}
		seen <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"requests": []map[string]any{{
				"node_id":   "node_joining",
				"node_name": "new laptop",
				"status":    "pending",
				"os":        "linux",
				"arch":      "amd64",
			}},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	writeTestHubConfig(t, home, server, "node_owner", "owner_secret")

	out := captureStdout(t, func() {
		if err := handleHubTrust([]string{"pending"}); err != nil {
			t.Fatalf("handleHubTrust pending: %v", err)
		}
	})
	if !strings.Contains(out, "node_joining") || !strings.Contains(out, "new laptop") || !strings.Contains(out, "pending") {
		t.Fatalf("trust pending output = %q, want request", out)
	}
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("hub trust pending did not call configured hub")
	}
}

func TestHubTrustAllowUsesConfiguredNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	seen := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/trust/allow" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.URL.Query().Get("node_id"); got != "node_owner" {
			t.Errorf("node_id = %q, want node_owner", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer owner_secret" {
			t.Errorf("Authorization = %q, want bearer owner token", got)
		}
		var req struct {
			NodeID string `json:"node_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		seen <- req.NodeID
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]bool{"ok": true}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	writeTestHubConfig(t, home, server, "node_owner", "owner_secret")

	out := captureStdout(t, func() {
		if err := handleHubTrust([]string{"allow", "node_joining"}); err != nil {
			t.Fatalf("handleHubTrust allow: %v", err)
		}
	})
	if !strings.Contains(out, "Allowed") || !strings.Contains(out, "node_joining") {
		t.Fatalf("trust allow output = %q, want allow confirmation", out)
	}
	select {
	case got := <-seen:
		if got != "node_joining" {
			t.Fatalf("allow request node_id = %q, want node_joining", got)
		}
	case <-time.After(time.Second):
		t.Fatal("hub trust allow did not call configured hub")
	}
}

func TestHubInviteErrorsWhenHubURLIsNotConfigured(t *testing.T) {
	err := handleHubInvite([]string{"--data", t.TempDir(), "laptop"})
	if err == nil {
		t.Fatal("handleHubInvite succeeded without advertised hub URL, want setup guidance")
	}
	if !strings.Contains(err.Error(), "hub URL") || !strings.Contains(err.Error(), "hub serve") {
		t.Fatalf("error = %q, want hub serve guidance", err.Error())
	}
}

func TestExchangeHubInviteTrustsAndReturnsPinnedFingerprint(t *testing.T) {
	server := newJoinResponseServer(t, `{"url":"","node_id":"node_abc","node_name":"laptop","node_token":"adhn_secret"}`)
	var prompt hubServerCertInfo

	result, err := exchangeHubInvite(server.wssURL(), hubJoinRequest{InviteToken: "invite_abc"}, hubJoinTLSOptions{
		TrustServerCert: func(info hubServerCertInfo) (bool, error) {
			prompt = info
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("exchangeHubInvite: %v", err)
	}
	if prompt.SHA256 == "" || result.PinnedCertSHA256 == "" {
		t.Fatalf("prompt/result fingerprints = %q/%q, want populated", prompt.SHA256, result.PinnedCertSHA256)
	}
	if result.PinnedCertSHA256 != prompt.SHA256 {
		t.Fatalf("result pin = %q, want prompt fingerprint %q", result.PinnedCertSHA256, prompt.SHA256)
	}
}

func TestExchangeHubInviteRejectsUntrustedFingerprint(t *testing.T) {
	server := newJoinResponseServer(t, `{"url":"","node_id":"node_abc","node_name":"laptop","node_token":"adhn_secret"}`)

	_, err := exchangeHubInvite(server.wssURL(), hubJoinRequest{InviteToken: "invite_abc"}, hubJoinTLSOptions{
		TrustServerCert: func(info hubServerCertInfo) (bool, error) {
			return false, nil
		},
	})
	if err == nil {
		t.Fatal("exchangeHubInvite succeeded after trust callback rejected fingerprint")
	}
}

func TestHubJoinRejectsMalformedJoinResponse(t *testing.T) {
	server := newJoinResponseServer(t, `{"url":"wss://hub.local:8421","node_id":"node_abc","node_name":"laptop","node_token":"  "}`)

	_, err := exchangeHubInvite(server.wssURL(), hubJoinRequest{InviteToken: "invite_abc"}, hubJoinTLSOptions{TLSSkipVerify: true})

	if err == nil {
		t.Fatal("exchangeHubInvite succeeded with blank node token, want error")
	}
}

func TestHubJoinRejectsPlaintextResponseURLBeforePersisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	tokenPath := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	server := newJoinResponseServer(t, `{"url":"http://hub.local:8421","node_id":"node_abc","node_name":"laptop","node_token":"adhn_secret"}`)

	err := handleHubJoin([]string{server.wssURL(), "--token", "invite_abc", "--tls-skip-verify", "--token-file", tokenPath})

	if err == nil {
		t.Fatal("handleHubJoin succeeded with plaintext response URL, want error")
	}
	if _, statErr := os.Stat(tokenPath); !os.IsNotExist(statErr) {
		t.Fatalf("token file stat error = %v, want not exist", statErr)
	}
	configPath, pathErr := session.GetUserConfigPath()
	if pathErr != nil {
		t.Fatalf("GetUserConfigPath: %v", pathErr)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("config stat error = %v, want not exist", statErr)
	}
}

func TestHubNodesJSONDoesNotExposeTokenHash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	dataDir := t.TempDir()
	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := store.UpsertNodeWithAdmin("node_1", "laptop", "secret_hash", "1.0.0", "linux", "amd64", true); err != nil {
		t.Fatalf("UpsertNodeWithAdmin: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := captureStdout(t, func() {
		if err := handleHubNodes([]string{"--data", dataDir, "--json"}); err != nil {
			t.Fatalf("handleHubNodes: %v", err)
		}
	})

	if strings.Contains(out, "TokenHash") || strings.Contains(out, "token_hash") || strings.Contains(out, "secret_hash") {
		t.Fatalf("nodes JSON exposed token hash: %s", out)
	}
	if !strings.Contains(out, "node_1") || !strings.Contains(out, "laptop") {
		t.Fatalf("nodes JSON missing expected node data: %s", out)
	}
	if !strings.Contains(out, `"admin":true`) {
		t.Fatalf("nodes JSON missing admin=true: %s", out)
	}
}

func TestHubNodesPromoteMarksNodeAdmin(t *testing.T) {
	dataDir := t.TempDir()
	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := store.UpsertNode("node_1", "laptop", "secret_hash", "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := captureStdout(t, func() {
		if err := handleHubNodes([]string{"promote", "--data", dataDir, "node_1"}); err != nil {
			t.Fatalf("handleHubNodes promote: %v", err)
		}
	})
	if !strings.Contains(out, "node_1") || !strings.Contains(strings.ToLower(out), "admin") {
		t.Fatalf("promote output = %q, want node/admin confirmation", out)
	}

	store, err = hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore after promote: %v", err)
	}
	defer store.Close()
	nodes, err := store.Nodes()
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 1 || !nodes[0].Admin {
		t.Fatalf("Node.Admin = false, want true after promote")
	}
}

func TestHubSettingsEnabledTrimsWhitespace(t *testing.T) {
	enabled := session.HubSettings{
		URL:    " wss://hub.local:8421 ",
		NodeID: " node_abc ",
	}.Enabled()
	if !enabled {
		t.Fatal("HubSettings.Enabled() = false, want true for trimmed URL and node id")
	}
	disabled := session.HubSettings{
		URL:    " wss://hub.local:8421 ",
		NodeID: "   ",
	}.Enabled()
	if disabled {
		t.Fatal("HubSettings.Enabled() = true, want false for blank node id")
	}
}

func TestHubConnectRequiresConfiguredHub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	err := handleHubConnectWithContext(context.Background(), "default", nil)

	if err == nil {
		t.Fatal("handleHubConnectWithContext succeeded without hub config, want error")
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "hub") || !strings.Contains(lower, "join") {
		t.Fatalf("error = %q, want hub join guidance", err.Error())
	}
}

func TestHubConnectReadsTokenFileAndConnects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	connected := make(chan struct{})
	authHeader := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader <- r.Header.Get("Authorization")
		conn, err := hubTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var env hub.Envelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			if env.Type == hub.MsgSnapshot {
				close(connected)
				return
			}
		}
	}))
	defer server.Close()

	tokenFile := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenFile, []byte(" node_secret \n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if err := session.SaveUserConfig(&session.UserConfig{Hub: session.HubSettings{
		URL:           " wss://" + strings.TrimPrefix(server.URL, "https://") + " ",
		NodeID:        " node_1 ",
		NodeName:      " laptop ",
		TokenFile:     " " + tokenFile + " ",
		TLSSkipVerify: true,
	}}); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	session.ClearUserConfigCache()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	out := captureStdout(t, func() {
		go func() {
			errCh <- handleHubConnectWithContext(ctx, "default", nil)
		}()
		select {
		case <-connected:
			cancel()
		case <-time.After(time.Second):
			t.Fatal("hub connect did not publish snapshot")
		}
		if err := waitConnectErr(t, errCh); err != nil {
			t.Fatalf("handleHubConnectWithContext: %v", err)
		}
	})

	if got := waitConnectString(t, authHeader); got != "Bearer node_secret" {
		t.Fatalf("Authorization = %q, want trimmed bearer token", got)
	}
	if strings.Contains(out, "node_secret") {
		t.Fatalf("hub connect printed node token: %s", out)
	}
}

type joinResponseServer struct {
	server *httptest.Server
}

func newJoinResponseServer(t *testing.T, responseBody string) joinResponseServer {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/join" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, responseBody)
	}))
	t.Cleanup(server.Close)
	return joinResponseServer{server: server}
}

func (s joinResponseServer) wssURL() string {
	return "wss://" + strings.TrimPrefix(s.server.URL, "https://")
}

var hubTestUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func waitConnectErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connect error")
		return nil
	}
}

func waitConnectString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for string")
		return ""
	}
}

func writeTestHubConfig(t *testing.T, home string, server *httptest.Server, nodeID, token string) {
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
