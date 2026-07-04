package hub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestServerJoinRequiresInviteToken(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/join", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/join missing token status = %d, want %d", rec.Code, http.StatusBadRequest)
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
