package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type fakePluginManager struct {
	catalog []PluginCatalogEntry
	state   SessionPluginsResponse

	attachSessionID string
	attachName      string
	attachNoLink    bool
	detachSessionID string
	detachName      string
}

func (f *fakePluginManager) ListPluginCatalog() []PluginCatalogEntry {
	return append([]PluginCatalogEntry(nil), f.catalog...)
}

func (f *fakePluginManager) ListSessionPlugins(sessionID string, _ *MenuSession) (SessionPluginsResponse, error) {
	state := f.state
	state.SessionID = sessionID
	return state, nil
}

func (f *fakePluginManager) AttachPlugin(sessionID string, _ *MenuSession, name string, noChannelLink bool) (PluginMutateResponse, error) {
	f.attachSessionID = sessionID
	f.attachName = name
	f.attachNoLink = noChannelLink
	return PluginMutateResponse{SessionID: sessionID, Plugins: []string{name}, RestartRequired: true}, nil
}

func (f *fakePluginManager) DetachPlugin(sessionID string, _ *MenuSession, name string) (PluginMutateResponse, error) {
	f.detachSessionID = sessionID
	f.detachName = name
	return PluginMutateResponse{SessionID: sessionID, Plugins: []string{}, RestartRequired: true}, nil
}

func newPluginTestServer(mgr PluginManager, mutationsAllowed bool) *Server {
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: mutationsAllowed})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{Items: []MenuItem{{
		Type: MenuItemTypeSession,
		Session: &MenuSession{
			ID:          "sess-001",
			Title:       "alpha",
			Tool:        "claude",
			Status:      session.StatusRunning,
			ProjectPath: "/srv/alpha",
			Plugins:     []string{"octopus"},
			Channels:    []string{"plugin:octopus"},
		},
	}}}}
	srv.SetPluginManager(mgr)
	return srv
}

func TestPluginsCatalogGET_Happy(t *testing.T) {
	mgr := &fakePluginManager{catalog: []PluginCatalogEntry{{Name: "octopus", PluginName: "octopus", Source: "nyldn/claude-octopus"}}}
	srv := newPluginTestServer(mgr, true)

	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp PluginsCatalogResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Plugins) != 1 || resp.Plugins[0].Name != "octopus" {
		t.Fatalf("plugins = %+v", resp.Plugins)
	}
}

func TestSessionPluginsGET_Happy(t *testing.T) {
	mgr := &fakePluginManager{state: SessionPluginsResponse{
		Catalog:  []PluginCatalogEntry{{Name: "discord"}},
		Plugins:  []string{"octopus"},
		Channels: []string{"plugin:octopus"},
	}}
	srv := newPluginTestServer(mgr, true)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-001/plugins", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"octopus"`) || !strings.Contains(rr.Body.String(), `"discord"`) {
		t.Fatalf("response missing plugin state/catalog: %s", rr.Body.String())
	}
}

func TestSessionPluginsAttach_Happy(t *testing.T) {
	mgr := &fakePluginManager{}
	srv := newPluginTestServer(mgr, true)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-001/plugins/discord", strings.NewReader(`{"noChannelLink":true}`))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if mgr.attachSessionID != "sess-001" || mgr.attachName != "discord" || !mgr.attachNoLink {
		t.Fatalf("attach call = session:%q name:%q noLink:%v", mgr.attachSessionID, mgr.attachName, mgr.attachNoLink)
	}
}

func TestSessionPluginsDetach_Happy(t *testing.T) {
	mgr := &fakePluginManager{}
	srv := newPluginTestServer(mgr, true)

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/sess-001/plugins/octopus", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if mgr.detachSessionID != "sess-001" || mgr.detachName != "octopus" {
		t.Fatalf("detach call = session:%q name:%q", mgr.detachSessionID, mgr.detachName)
	}
}

func TestSessionPluginsAttach_ReadOnly(t *testing.T) {
	srv := newPluginTestServer(&fakePluginManager{}, false)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-001/plugins/discord", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rr.Code, rr.Body.String())
	}
}
