package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestGlobalSearchFindsClaudeConversationContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	projectDir := filepath.Join(home, ".claude", "projects", "-Users-test-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := `{"sessionId":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","type":"user","message":{"role":"user","content":"implement MCP server for observability metrics"},"cwd":"/Users/test/project"}
{"sessionId":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","type":"assistant","message":{"role":"assistant","content":"I'll scaffold an MCP server that exposes Prometheus-style metrics."}}`
	if err := os.WriteFile(filepath.Join(projectDir, "a1b2c3d4-e5f6-7890-abcd-ef1234567890.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	other := `{"sessionId":"b2c3d4e5-f6a7-8901-bcde-f23456789012","type":"user","message":{"role":"user","content":"refactor the login widget styles"},"cwd":"/Users/test/project"}`
	if err := os.WriteFile(filepath.Join(projectDir, "b2c3d4e5-f6a7-8901-bcde-f23456789012.jsonl"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}

	server := NewServer(Config{Profile: "test"})
	req := httptest.NewRequest(http.MethodGet, "/api/search/global?q=observability&limit=5", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/search/global status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp GlobalSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	if resp.Query != "observability" || resp.Count != 1 {
		t.Fatalf("response query/count = %q/%d; body=%s", resp.Query, resp.Count, rec.Body.String())
	}
	got := resp.Results[0]
	if got.SessionID != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Fatalf("session id = %q", got.SessionID)
	}
	if !strings.Contains(strings.ToLower(got.Snippet), "observability") {
		t.Fatalf("snippet %q should contain query", got.Snippet)
	}
	if got.CWD != "/Users/test/project" {
		t.Fatalf("cwd = %q", got.CWD)
	}
}

func TestGlobalSearchRejectsWrongMethod(t *testing.T) {
	server := NewServer(Config{Profile: "test"})
	req := httptest.NewRequest(http.MethodPost, "/api/search/global?q=x", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/search/global status=%d body=%s", rec.Code, rec.Body.String())
	}
}
