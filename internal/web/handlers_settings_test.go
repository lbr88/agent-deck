package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestSettingsGET(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		Profile:      "work",
		ReadOnly:     true,
		WebMutations: false,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"profile"`) {
		t.Errorf("expected 'profile' key, got: %s", body)
	}
	if !strings.Contains(body, `"readOnly"`) {
		t.Errorf("expected 'readOnly' key, got: %s", body)
	}
	if !strings.Contains(body, `"webMutations"`) {
		t.Errorf("expected 'webMutations' key, got: %s", body)
	}
	if !strings.Contains(body, `"version"`) {
		t.Errorf("expected 'version' key, got: %s", body)
	}
	if !strings.Contains(body, `"work"`) {
		t.Errorf("expected profile value 'work', got: %s", body)
	}
}

func TestSettingsMethodNotAllowed(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d: %s", http.StatusMethodNotAllowed, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), ErrCodeMethodNotAllowed) {
		t.Errorf("expected METHOD_NOT_ALLOWED error, got: %s", rr.Body.String())
	}
}

func TestSettingsUnauthorized(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d: %s", http.StatusUnauthorized, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), ErrCodeUnauthorized) {
		t.Errorf("expected UNAUTHORIZED error, got: %s", rr.Body.String())
	}
}

func TestSettingsWebMutationsTrue(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: true,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"webMutations":true`) {
		t.Errorf("expected webMutations:true, got: %s", rr.Body.String())
	}
}

func TestSettingsPickerToolsPreserveDefaultCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write claude stub: %v", err)
	}
	t.Setenv("PATH", binDir)
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)
	if err := session.SaveUserConfig(&session.UserConfig{
		DefaultTool: "codex",
		UI:          session.UISettings{ShowOnlyInstalledTools: true},
	}); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	session.ClearUserConfigCache()

	srv := NewServer(Config{ListenAddr: "127.0.0.1:0"})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	var got SettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if !slices.Contains(got.PickerTools, "codex") {
		t.Fatalf("pickerTools = %v, want configured default codex preserved even when web service PATH probe misses it", got.PickerTools)
	}
	if !slices.Contains(got.PickerTools, "claude") {
		t.Fatalf("pickerTools = %v, want installed claude retained", got.PickerTools)
	}
}

func TestSettingsReportsHubAdminCapability(t *testing.T) {
	home := t.TempDir()
	setWebHubConfigEnv(t, home)

	hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("node_id"); got != "node_admin" {
			t.Errorf("node_id = %q, want node_admin", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin_secret" {
			t.Errorf("Authorization = %q, want bearer admin token", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"node": map[string]any{
				"id":     "node_admin",
				"name":   "admin",
				"status": "online",
				"admin":  true,
			},
		})
	}))
	defer hubServer.Close()
	writeWebHubConfig(t, home, hubServer, "node_admin", "admin_secret")

	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	var got SettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if !got.HubConfigured || !got.HubAdmin {
		t.Fatalf("hub capability = configured:%v admin:%v, want true/true", got.HubConfigured, got.HubAdmin)
	}
}

func TestSettingsReportsConfiguredNonAdminHubCapability(t *testing.T) {
	home := t.TempDir()
	setWebHubConfigEnv(t, home)

	hubServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("node_id"); got != "node_user" {
			t.Errorf("node_id = %q, want node_user", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user_secret" {
			t.Errorf("Authorization = %q, want bearer user token", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"node": map[string]any{
				"id":     "node_user",
				"name":   "user",
				"status": "online",
				"admin":  false,
			},
		})
	}))
	defer hubServer.Close()
	writeWebHubConfig(t, home, hubServer, "node_user", "user_secret")

	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	var got SettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if !got.HubConfigured || got.HubAdmin {
		t.Fatalf("hub capability = configured:%v admin:%v, want true/false", got.HubConfigured, got.HubAdmin)
	}
}

func TestProfilesGET(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "work",
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}

	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"current"`) {
		t.Errorf("expected 'current' key, got: %s", body)
	}
	if !strings.Contains(body, `"profiles"`) {
		t.Errorf("expected 'profiles' key, got: %s", body)
	}
	if !strings.Contains(body, `"work"`) {
		t.Errorf("expected profile value 'work', got: %s", body)
	}
}

func TestProfilesMethodNotAllowed(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}

	req := httptest.NewRequest(http.MethodPost, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d: %s", http.StatusMethodNotAllowed, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), ErrCodeMethodNotAllowed) {
		t.Errorf("expected METHOD_NOT_ALLOWED error, got: %s", rr.Body.String())
	}
}

func TestProfilesUnauthorized(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}

	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d: %s", http.StatusUnauthorized, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), ErrCodeUnauthorized) {
		t.Errorf("expected UNAUTHORIZED error, got: %s", rr.Body.String())
	}
}
