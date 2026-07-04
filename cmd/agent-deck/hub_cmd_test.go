package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
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
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	cfg := &session.UserConfig{}
	tokenPath := filepath.Join(home, ".config", "agent-deck", "hub-node-token")
	if err := saveHubJoinConfig(cfg, hubJoinResult{
		URL:       "wss://hub.local:8421",
		NodeID:    "node_abc",
		NodeName:  "laptop",
		NodeToken: "adhn_secret",
		TokenPath: tokenPath,
	}); err != nil {
		t.Fatalf("saveHubJoinConfig: %v", err)
	}
	if cfg.Hub.URL != "wss://hub.local:8421" || cfg.Hub.NodeID != "node_abc" || cfg.Hub.NodeName != "laptop" || !cfg.Hub.AutoConnect {
		t.Fatalf("hub config = %+v", cfg.Hub)
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
		URL:       "wss://hub.local:8421",
		NodeID:    "node_abc",
		NodeName:  "laptop",
		NodeToken: "adhn_secret",
		TokenPath: tokenPath,
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
	if _, err := store.UpsertNode("node_1", "laptop", "secret_hash", "1.0.0", "linux", "amd64"); err != nil {
		t.Fatalf("UpsertNode: %v", err)
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
