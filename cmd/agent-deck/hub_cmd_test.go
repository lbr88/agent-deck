package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
