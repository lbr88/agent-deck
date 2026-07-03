package session

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	claudeImportIDAlpha = "11111111-2222-3333-4444-555555555555"
	claudeImportIDBeta  = "22222222-3333-4444-5555-666666666666"
	claudeImportIDGamma = "33333333-4444-5555-6666-777777777777"
)

func writeClaudeImportTranscript(t *testing.T, configDir, cwd, sessionID string, extraLines ...string) string {
	t.Helper()
	projectDir := filepath.Join(configDir, "projects", ConvertToClaudeDirName(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	lines := []string{`{"sessionId":"` + sessionID + `","cwd":"` + cwd + `","timestamp":"2026-06-30T10:00:00Z"}`}
	lines = append(lines, extraLines...)
	path := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func writeClaudeImportName(t *testing.T, configDir, file, sessionID, name string, updatedAt int64) {
	t.Helper()
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	payload := `{"sessionId":"` + sessionID + `","name":"` + name + `","updatedAt":` + strconv.FormatInt(updatedAt, 10) + `}`
	if err := os.WriteFile(filepath.Join(sessionsDir, file), []byte(payload), 0o644); err != nil {
		t.Fatalf("write name metadata: %v", err)
	}
}

func TestListClaudeImportCandidates_MetadataOnly(t *testing.T) {
	configDir := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "project")
	privateLine := `{"type":"user","message":{"role":"user","content":"PRIVATE_TRANSCRIPT_CONTENT` + strings.Repeat("x", 70*1024) + `"}}`
	transcriptPath := writeClaudeImportTranscript(t, configDir, cwd, claudeImportIDAlpha, privateLine)
	writeClaudeImportName(t, configDir, "1234.json", claudeImportIDAlpha, "Alpha plan", 2000)

	candidates, err := ListClaudeImportCandidates(configDir)
	if err != nil {
		t.Fatalf("ListClaudeImportCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1: %#v", len(candidates), candidates)
	}
	got := candidates[0]
	if got.SessionID != claudeImportIDAlpha {
		t.Errorf("SessionID = %q, want %q", got.SessionID, claudeImportIDAlpha)
	}
	if got.CWD != cwd {
		t.Errorf("CWD = %q, want %q", got.CWD, cwd)
	}
	if got.Path != "" {
		t.Errorf("Path = %q, want empty when metadata path absent", got.Path)
	}
	if got.Name != "Alpha plan" {
		t.Errorf("Name = %q, want Alpha plan", got.Name)
	}
	if got.Title != "Alpha plan" {
		t.Errorf("Title = %q, want Alpha plan", got.Title)
	}
	if got.FilePath != transcriptPath {
		t.Errorf("FilePath = %q, want %q", got.FilePath, transcriptPath)
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt should come from transcript file metadata")
	}
	if strings.Contains(strings.Join([]string{got.SessionID, got.Name, got.CWD, got.FilePath}, "\n"), "PRIVATE_TRANSCRIPT_CONTENT") {
		t.Fatalf("candidate exposed transcript content: %#v", got)
	}
}

func TestListClaudeImportCandidates_UsesTranscriptSummaryAsFallbackTitle(t *testing.T) {
	configDir := t.TempDir()
	writeClaudeImportTranscript(
		t,
		configDir,
		"/tmp/summary-project",
		claudeImportIDAlpha,
		`{"type":"summary","summary":"Review GitHub pull request #14","sessionId":"`+claudeImportIDAlpha+`"}`,
	)

	candidates, err := ListClaudeImportCandidates(configDir)
	if err != nil {
		t.Fatalf("ListClaudeImportCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].Name != "" {
		t.Fatalf("Name = %q, want empty when Claude has no explicit name metadata", candidates[0].Name)
	}
	if got := candidates[0].Title; got != "Review GitHub pull request #14" {
		t.Fatalf("Title = %q, want transcript summary fallback", got)
	}
}

func TestListClaudeImportCandidates_UsesFirstUserMessageAsFallbackTitle(t *testing.T) {
	configDir := t.TempDir()
	writeClaudeImportTranscript(
		t,
		configDir,
		"/tmp/user-message-project",
		claudeImportIDBeta,
		`{"type":"user","sessionId":"`+claudeImportIDBeta+`","message":{"role":"user","content":"Investigate why Claude imports show only short session ids"}}`,
	)

	candidates, err := ListClaudeImportCandidates(configDir)
	if err != nil {
		t.Fatalf("ListClaudeImportCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if got := candidates[0].Title; got != "Investigate why Claude imports show only short session ids" {
		t.Fatalf("Title = %q, want first user message fallback", got)
	}
}

func TestListClaudeImportCandidates_CapturesMetadataPathFallback(t *testing.T) {
	configDir := t.TempDir()
	projectDir := filepath.Join(configDir, "projects", "fallback-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	transcriptPath := filepath.Join(projectDir, claudeImportIDBeta+".jsonl")
	line := `{"sessionId":"` + claudeImportIDBeta + `","path":"/metadata/path","timestamp":"2026-06-30T10:00:00Z"}`
	if err := os.WriteFile(transcriptPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	candidates, err := ListClaudeImportCandidates(configDir)
	if err != nil {
		t.Fatalf("ListClaudeImportCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if got := candidates[0].Path; got != "/metadata/path" {
		t.Fatalf("Path = %q, want metadata path fallback", got)
	}
	if got := candidates[0].CWD; got != "" {
		t.Fatalf("CWD = %q, want empty when only metadata path exists", got)
	}
}

func TestResolveClaudeImportTarget_UUIDPreferredOverExactName(t *testing.T) {
	configDir := t.TempDir()
	writeClaudeImportTranscript(t, configDir, "/tmp/alpha", claudeImportIDAlpha)
	writeClaudeImportName(t, configDir, "alpha.json", claudeImportIDAlpha, "Alpha", 1000)
	writeClaudeImportTranscript(t, configDir, "/tmp/beta", claudeImportIDBeta)
	writeClaudeImportName(t, configDir, "beta.json", claudeImportIDBeta, claudeImportIDAlpha, 2000)

	got, err := ResolveClaudeImportTarget(configDir, claudeImportIDAlpha)
	if err != nil {
		t.Fatalf("ResolveClaudeImportTarget: %v", err)
	}
	if got.SessionID != claudeImportIDAlpha {
		t.Fatalf("resolved SessionID = %q, want UUID match %q", got.SessionID, claudeImportIDAlpha)
	}
}

func TestResolveClaudeImportTarget_NameAmbiguousAndMissing(t *testing.T) {
	configDir := t.TempDir()
	writeClaudeImportTranscript(t, configDir, "/tmp/alpha", claudeImportIDAlpha)
	writeClaudeImportName(t, configDir, "alpha.json", claudeImportIDAlpha, "Shared name", 1000)
	writeClaudeImportTranscript(t, configDir, "/tmp/beta", claudeImportIDBeta)
	writeClaudeImportName(t, configDir, "beta.json", claudeImportIDBeta, "Shared name", 2000)
	writeClaudeImportTranscript(t, configDir, "/tmp/gamma", claudeImportIDGamma)
	writeClaudeImportName(t, configDir, "gamma.json", claudeImportIDGamma, "Unique name", 3000)

	got, err := ResolveClaudeImportTarget(configDir, "Unique name")
	if err != nil {
		t.Fatalf("ResolveClaudeImportTarget unique name: %v", err)
	}
	if got.SessionID != claudeImportIDGamma {
		t.Fatalf("unique name resolved %q, want %q", got.SessionID, claudeImportIDGamma)
	}

	_, err = ResolveClaudeImportTarget(configDir, "Shared name")
	var resolveErr *ClaudeImportResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("ambiguous error type = %T, want *ClaudeImportResolveError", err)
	}
	if resolveErr.Kind != ClaudeImportResolveAmbiguous {
		t.Fatalf("ambiguous Kind = %q, want %q", resolveErr.Kind, ClaudeImportResolveAmbiguous)
	}
	if len(resolveErr.Candidates) != 2 {
		t.Fatalf("ambiguous candidate count = %d, want 2", len(resolveErr.Candidates))
	}
	if resolveErr.Candidates[0].SessionID == "" || resolveErr.Candidates[1].SessionID == "" {
		t.Fatalf("ambiguous candidates must include retry UUIDs: %#v", resolveErr.Candidates)
	}

	_, err = ResolveClaudeImportTarget(configDir, "missing")
	if !errors.As(err, &resolveErr) {
		t.Fatalf("missing error type = %T, want *ClaudeImportResolveError", err)
	}
	if resolveErr.Kind != ClaudeImportResolveNotFound {
		t.Fatalf("missing Kind = %q, want %q", resolveErr.Kind, ClaudeImportResolveNotFound)
	}
}

func TestUpdateStatus_PreservesStoppedDuringStartupGrace(t *testing.T) {
	inst := NewInstanceWithTool("imported-stopped", t.TempDir(), "claude")
	inst.Status = StatusStopped

	if err := inst.UpdateStatus(); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if inst.Status != StatusStopped {
		t.Fatalf("Status = %q, want %q", inst.Status, StatusStopped)
	}
}
