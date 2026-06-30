package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func cloneImportInstances(instances []*session.Instance) []*session.Instance {
	out := make([]*session.Instance, 0, len(instances))
	for _, inst := range instances {
		cp := *inst
		out = append(out, &cp)
	}
	return out
}

func TestImportClaudeSession_DefaultsAndPersistsStopped(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "from-transcript")
	var saved [][]*session.Instance

	deps := importClaudeSessionDeps{
		load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
			return nil, nil, nil, nil
		},
		save: func(_ *session.Storage, instances []*session.Instance, _ []*session.GroupData) error {
			saved = append(saved, cloneImportInstances(instances))
			return nil
		},
		resolve: func(_ string, target string) (*session.ClaudeImportCandidate, error) {
			if target != "Alpha plan" {
				t.Fatalf("resolve target = %q, want Alpha plan", target)
			}
			return &session.ClaudeImportCandidate{
				SessionID: "11111111-2222-3333-4444-555555555555",
				Name:      "Alpha plan",
				CWD:       cwd,
			}, nil
		},
		cwd: func() (string, error) {
			return "/fallback/cwd", nil
		},
		start: func(*session.Instance) error {
			t.Fatal("start should not be called without --start")
			return nil
		},
	}

	result, err := importClaudeSession("", importClaudeSessionOptions{Target: "Alpha plan"}, deps)
	if err != nil {
		t.Fatalf("importClaudeSession: %v", err)
	}
	if result.Instance == nil {
		t.Fatal("result.Instance is nil")
	}
	if len(saved) != 1 {
		t.Fatalf("save count = %d, want 1", len(saved))
	}
	got := saved[0][0]
	if got.Status != session.StatusStopped {
		t.Errorf("Status = %q, want stopped", got.Status)
	}
	if got.Tool != "claude" {
		t.Errorf("Tool = %q, want claude", got.Tool)
	}
	if got.Command != "claude" {
		t.Errorf("Command = %q, want claude", got.Command)
	}
	if got.ClaudeSessionID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("ClaudeSessionID = %q", got.ClaudeSessionID)
	}
	if got.Title != "Alpha plan" {
		t.Errorf("Title = %q, want Claude session name", got.Title)
	}
	if got.ProjectPath != cwd {
		t.Errorf("ProjectPath = %q, want transcript cwd %q", got.ProjectPath, cwd)
	}
	if got.GroupPath != session.DefaultGroupPath {
		t.Errorf("GroupPath = %q, want %q", got.GroupPath, session.DefaultGroupPath)
	}
	if got.TitleLocked {
		t.Errorf("TitleLocked = true, want false for normal Claude import defaults")
	}
}

func TestImportClaudeSession_ExplicitOverridesAndShortUUIDFallback(t *testing.T) {
	var saved [][]*session.Instance
	deps := importClaudeSessionDeps{
		load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
			return nil, nil, nil, nil
		},
		save: func(_ *session.Storage, instances []*session.Instance, _ []*session.GroupData) error {
			saved = append(saved, cloneImportInstances(instances))
			return nil
		},
		resolve: func(_ string, _ string) (*session.ClaudeImportCandidate, error) {
			return &session.ClaudeImportCandidate{
				SessionID: "22222222-3333-4444-5555-666666666666",
			}, nil
		},
		cwd: func() (string, error) {
			return "/fallback/cwd", nil
		},
	}

	_, err := importClaudeSession("", importClaudeSessionOptions{
		Target:      "22222222-3333-4444-5555-666666666666",
		Title:       "Explicit title",
		GroupPath:   "work/imports",
		ProjectPath: "/explicit/path",
	}, deps)
	if err != nil {
		t.Fatalf("importClaudeSession: %v", err)
	}
	got := saved[0][0]
	if got.Title != "Explicit title" {
		t.Errorf("Title = %q, want explicit title", got.Title)
	}
	if got.ProjectPath != "/explicit/path" {
		t.Errorf("ProjectPath = %q, want explicit path", got.ProjectPath)
	}
	if got.GroupPath != "work/imports" {
		t.Errorf("GroupPath = %q, want explicit group", got.GroupPath)
	}
	if got.TitleLocked {
		t.Errorf("TitleLocked = true, want false for explicit --title import")
	}

	saved = nil
	deps.resolve = func(_ string, _ string) (*session.ClaudeImportCandidate, error) {
		return &session.ClaudeImportCandidate{SessionID: "33333333-4444-5555-6666-777777777777"}, nil
	}
	_, err = importClaudeSession("", importClaudeSessionOptions{Target: "33333333-4444-5555-6666-777777777777"}, deps)
	if err != nil {
		t.Fatalf("importClaudeSession fallback title: %v", err)
	}
	if got := saved[0][0].Title; got != "33333333" {
		t.Errorf("fallback Title = %q, want short UUID", got)
	}
	if got := saved[0][0].ProjectPath; got != "/fallback/cwd" {
		t.Errorf("fallback ProjectPath = %q, want current directory", got)
	}
}

func TestImportClaudeSession_ProjectPathFallbackOrder(t *testing.T) {
	tests := []struct {
		name        string
		explicit    string
		candidate   session.ClaudeImportCandidate
		cwd         string
		wantProject string
	}{
		{
			name:        "explicit path wins",
			explicit:    "/explicit/path",
			candidate:   session.ClaudeImportCandidate{SessionID: "11111111-2222-3333-4444-555555555555", CWD: "/metadata/cwd", Path: "/metadata/path"},
			cwd:         "/fallback/cwd",
			wantProject: "/explicit/path",
		},
		{
			name:        "metadata cwd beats metadata path",
			candidate:   session.ClaudeImportCandidate{SessionID: "11111111-2222-3333-4444-555555555555", CWD: "/metadata/cwd", Path: "/metadata/path"},
			cwd:         "/fallback/cwd",
			wantProject: "/metadata/cwd",
		},
		{
			name:        "metadata path beats current working directory",
			candidate:   session.ClaudeImportCandidate{SessionID: "11111111-2222-3333-4444-555555555555", Path: "/metadata/path"},
			cwd:         "/fallback/cwd",
			wantProject: "/metadata/path",
		},
		{
			name:        "cwd is final fallback",
			candidate:   session.ClaudeImportCandidate{SessionID: "11111111-2222-3333-4444-555555555555"},
			cwd:         "/fallback/cwd",
			wantProject: "/fallback/cwd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var saved [][]*session.Instance
			deps := importClaudeSessionDeps{
				load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
					return nil, nil, nil, nil
				},
				save: func(_ *session.Storage, instances []*session.Instance, _ []*session.GroupData) error {
					saved = append(saved, cloneImportInstances(instances))
					return nil
				},
				resolve: func(_ string, _ string) (*session.ClaudeImportCandidate, error) {
					candidate := tc.candidate
					return &candidate, nil
				},
				cwd: func() (string, error) {
					return tc.cwd, nil
				},
			}

			_, err := importClaudeSession("", importClaudeSessionOptions{
				Target:      tc.candidate.SessionID,
				ProjectPath: tc.explicit,
			}, deps)
			if err != nil {
				t.Fatalf("importClaudeSession: %v", err)
			}
			if got := saved[0][0].ProjectPath; got != tc.wantProject {
				t.Fatalf("ProjectPath = %q, want %q", got, tc.wantProject)
			}
		})
	}
}

func TestImportClaudeSession_StartPersistsStoppedRowBeforeStarting(t *testing.T) {
	var events []string
	deps := importClaudeSessionDeps{
		load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
			return nil, nil, nil, nil
		},
		save: func(_ *session.Storage, instances []*session.Instance, _ []*session.GroupData) error {
			if len(instances) != 1 {
				t.Fatalf("save saw %d instances, want 1", len(instances))
			}
			events = append(events, "save:"+string(instances[0].Status))
			return nil
		},
		resolve: func(_ string, _ string) (*session.ClaudeImportCandidate, error) {
			return &session.ClaudeImportCandidate{
				SessionID: "44444444-5555-6666-7777-888888888888",
				Name:      "Start me",
				CWD:       "/tmp/start-me",
			}, nil
		},
		cwd: func() (string, error) {
			return "/fallback/cwd", nil
		},
		start: func(inst *session.Instance) error {
			events = append(events, "start")
			inst.Status = session.StatusStarting
			return nil
		},
	}

	_, err := importClaudeSession("", importClaudeSessionOptions{Target: "Start me", Start: true}, deps)
	if err != nil {
		t.Fatalf("importClaudeSession --start: %v", err)
	}
	want := []string{"save:stopped", "start", "save:starting"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestSessionImportClaudeCLI_PersistsImportedStoppedSession(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess CLI test skipped in short mode")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(home, ".claude")
	sessionID := "55555555-6666-7777-8888-999999999999"
	projectDir := filepath.Join(claudeDir, "projects", session.ConvertToClaudeDirName(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, sessionID+".jsonl"),
		[]byte(`{"sessionId":"`+sessionID+`","cwd":"`+cwd+`","timestamp":"2026-06-30T10:00:00Z"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(claudeDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(claudeDir, "sessions", "1234.json"),
		[]byte(`{"sessionId":"`+sessionID+`","name":"Imported Claude","updatedAt":2000}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runAgentDeck(t, home, "session", "import-claude", "Imported Claude", "--json")
	if code != 0 {
		t.Fatalf("session import-claude failed (%d)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var resp struct {
		Success         bool   `json:"success"`
		ID              string `json:"id"`
		Title           string `json:"title"`
		Status          string `json:"status"`
		Tool            string `json:"tool"`
		Command         string `json:"command"`
		ClaudeSessionID string `json:"claude_session_id"`
		ProjectPath     string `json:"project_path"`
		GroupPath       string `json:"group_path"`
		Started         bool   `json:"started"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal import response: %v\nstdout: %s", err, stdout)
	}
	if !resp.Success || resp.ID == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Title != "Imported Claude" || resp.Status != string(session.StatusStopped) || resp.Tool != "claude" ||
		resp.Command != "claude" || resp.ClaudeSessionID != sessionID || resp.ProjectPath != cwd ||
		resp.GroupPath != session.DefaultGroupPath || resp.Started {
		t.Fatalf("unexpected import response fields: %+v", resp)
	}

	stdout, stderr, code = runAgentDeck(t, home, "session", "show", resp.ID, "--json")
	if code != 0 {
		t.Fatalf("session show after import failed (%d)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var shown struct {
		ID              string `json:"id"`
		Title           string `json:"title"`
		Status          string `json:"status"`
		Tool            string `json:"tool"`
		Command         string `json:"command"`
		ClaudeSessionID string `json:"claude_session_id"`
		Path            string `json:"path"`
		Group           string `json:"group"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("unmarshal show response: %v\nstdout: %s", err, stdout)
	}
	if shown.ID != resp.ID || shown.Title != "Imported Claude" || shown.Status != string(session.StatusStopped) ||
		shown.Tool != "claude" || shown.Command != "claude" || shown.ClaudeSessionID != sessionID ||
		shown.Path != cwd || shown.Group != session.DefaultGroupPath {
		t.Fatalf("persisted session mismatch: %+v", shown)
	}
}

func TestSessionImportClaudeCLI_AmbiguousNameReturnsCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess CLI test skipped in short mode")
	}
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, sessionID := range []string{
		"66666666-7777-8888-9999-000000000000",
		"77777777-8888-9999-0000-111111111111",
	} {
		projectDir := filepath.Join(claudeDir, "projects", "project-"+strconv.Itoa(i))
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(projectDir, sessionID+".jsonl"),
			[]byte(`{"sessionId":"`+sessionID+`","cwd":"/tmp/project-`+strconv.Itoa(i)+`","timestamp":"2026-06-30T10:00:00Z"}`),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(claudeDir, "sessions", strconv.Itoa(i)+".json"),
			[]byte(`{"sessionId":"`+sessionID+`","name":"Duplicate Claude","updatedAt":2000}`),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, code := runAgentDeck(t, home, "session", "import-claude", "Duplicate Claude", "--json")
	if code != 1 {
		t.Fatalf("expected ambiguous import to fail with exit 1, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var resp struct {
		Success    bool   `json:"success"`
		Error      string `json:"error"`
		Code       string `json:"code"`
		Candidates []struct {
			SessionID string `json:"session_id"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal ambiguous response: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if resp.Success {
		t.Fatalf("ambiguous response marked success: %+v", resp)
	}
	if resp.Code != ErrCodeAmbiguous {
		t.Fatalf("code = %q, want %q", resp.Code, ErrCodeAmbiguous)
	}
	if len(resp.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(resp.Candidates))
	}
	if resp.Candidates[0].SessionID == "" || resp.Candidates[1].SessionID == "" {
		t.Fatalf("candidate UUIDs missing: %+v", resp.Candidates)
	}
}

func TestImportClaudeSession_AmbiguousErrorListsCandidateUUIDs(t *testing.T) {
	candidates := []session.ClaudeImportCandidate{
		{SessionID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Name: "same"},
		{SessionID: "ffffffff-1111-2222-3333-444444444444", Name: "same"},
	}
	deps := importClaudeSessionDeps{
		load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
			return nil, nil, nil, nil
		},
		resolve: func(_ string, target string) (*session.ClaudeImportCandidate, error) {
			return nil, &session.ClaudeImportResolveError{
				Target:     target,
				Kind:       session.ClaudeImportResolveAmbiguous,
				Candidates: candidates,
			}
		},
		cwd: func() (string, error) { return "/fallback/cwd", nil },
	}

	_, err := importClaudeSession("", importClaudeSessionOptions{Target: "same"}, deps)
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee") ||
		!strings.Contains(err.Error(), "ffffffff-1111-2222-3333-444444444444") {
		t.Fatalf("ambiguous error should include retry UUIDs, got: %v", err)
	}
}
