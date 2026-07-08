package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListKiroSavedSessionsSortsByUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	writeKiroSessionFile(t, dir, "old.json", `{"session_id":"37a7454c-f9d3-434a-bd7e-03318ef6b72a","cwd":"/repo/old","title":"old","created_at":"2026-05-08T05:27:02.624242907Z","updated_at":"2026-05-08T06:31:51.570257120Z","session_state":{"agent_name":"kiro_default"}}`)
	writeKiroSessionFile(t, dir, "new.json", `{"session_id":"75e59a16-9f76-433d-baa3-3cb8e5ef4c5d","cwd":"/repo/new","title":"new","created_at":"2026-05-09T05:27:02Z","updated_at":"2026-05-09T06:31:51Z","session_state":{"agent_name":"planner"}}`)

	entries, err := ListKiroSavedSessions(dir)
	if err != nil {
		t.Fatalf("ListKiroSavedSessions: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Title != "new" || entries[0].AgentName != "planner" || entries[0].CWD != "/repo/new" {
		t.Fatalf("newest entry not first or malformed: %+v", entries[0])
	}
}

func TestListKiroSavedSessionsMissingDirReturnsEmpty(t *testing.T) {
	entries, err := ListKiroSavedSessions(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("ListKiroSavedSessions missing dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0", len(entries))
	}
}

func TestListKiroSavedSessionsReportsMalformedPath(t *testing.T) {
	dir := t.TempDir()
	path := writeKiroSessionFile(t, dir, "bad.json", `{`)

	_, err := ListKiroSavedSessions(dir)
	if err == nil {
		t.Fatal("ListKiroSavedSessions malformed file error = nil")
	}
	if !containsAll(err.Error(), "parse", path) {
		t.Fatalf("error %q does not mention malformed path %q", err.Error(), path)
	}
}

func TestResolveKiroSavedSessionUUIDExactAndFoldedTitle(t *testing.T) {
	dir := t.TempDir()
	id := "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d"
	writeKiroSessionFile(t, dir, "session.json", `{"session_id":"`+id+`","cwd":"/repo","title":"Work Item","created_at":"2026-05-09T05:27:02Z","updated_at":"2026-05-09T06:31:51Z","session_state":{"agent_name":"kiro_default"}}`)

	byID, err := ResolveKiroSavedSession(dir, id)
	if err != nil {
		t.Fatalf("ResolveKiroSavedSession by ID: %v", err)
	}
	if byID.ID != id {
		t.Fatalf("byID.ID = %q, want %q", byID.ID, id)
	}

	byExact, err := ResolveKiroSavedSession(dir, "Work Item")
	if err != nil {
		t.Fatalf("ResolveKiroSavedSession by exact title: %v", err)
	}
	if byExact.ID != id {
		t.Fatalf("byExact.ID = %q, want %q", byExact.ID, id)
	}

	byFolded, err := ResolveKiroSavedSession(dir, "work item")
	if err != nil {
		t.Fatalf("ResolveKiroSavedSession by folded title: %v", err)
	}
	if byFolded.ID != id {
		t.Fatalf("byFolded.ID = %q, want %q", byFolded.ID, id)
	}
}

func TestResolveKiroSavedSessionAmbiguousTitle(t *testing.T) {
	dir := t.TempDir()
	writeKiroSessionFile(t, dir, "one.json", `{"session_id":"37a7454c-f9d3-434a-bd7e-03318ef6b72a","cwd":"/repo/a","title":"duplicate","created_at":"2026-05-08T05:27:02Z","updated_at":"2026-05-08T06:31:51Z","session_state":{"agent_name":"kiro_default"}}`)
	writeKiroSessionFile(t, dir, "two.json", `{"session_id":"75e59a16-9f76-433d-baa3-3cb8e5ef4c5d","cwd":"/repo/b","title":"duplicate","created_at":"2026-05-09T05:27:02Z","updated_at":"2026-05-09T06:31:51Z","session_state":{"agent_name":"kiro_default"}}`)

	_, err := ResolveKiroSavedSession(dir, "duplicate")
	if !errors.Is(err, ErrKiroSessionAmbiguous) {
		t.Fatalf("ResolveKiroSavedSession err = %v, want ErrKiroSessionAmbiguous", err)
	}
	var amb *KiroSessionAmbiguousError
	if !errors.As(err, &amb) || len(amb.Matches) != 2 {
		t.Fatalf("ambiguous error = %#v, want two matches", err)
	}
}

func TestSyncKiroSessionNameUpdatesSavedSessionTitle(t *testing.T) {
	dir := t.TempDir()
	id := "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d"
	now := time.Date(2026, 7, 8, 10, 0, 0, 123, time.UTC)
	body, err := json.Marshal(map[string]any{
		"session_id": id,
		"title":      "old title",
		"cwd":        "/repo",
		"created_at": "2026-07-08T09:00:00Z",
		"updated_at": "2026-07-08T09:00:00Z",
		"extra":      "preserved",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeKiroSessionFile(t, dir, "session.json", string(body))

	if err := SyncKiroSessionName(dir, id, "new title", now); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["title"] != "new title" {
		t.Fatalf("title = %v, want new title", got["title"])
	}
	if got["extra"] != "preserved" {
		t.Fatalf("extra = %v, want preserved", got["extra"])
	}
	if got["updated_at"] != now.Format(time.RFC3339Nano) {
		t.Fatalf("updated_at = %v, want %s", got["updated_at"], now.Format(time.RFC3339Nano))
	}
}

func TestNewKiroImportedInstanceUsesCWDPathAndGroup(t *testing.T) {
	updated := time.Date(2026, 7, 3, 10, 30, 0, 0, time.UTC)
	inst, err := NewKiroImportedInstance(KiroSavedSession{
		ID:        "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d",
		Title:     "github import",
		CWD:       "/home/lrasmussen/git/domutech/domutech-github",
		UpdatedAt: updated,
	}, KiroImportOptions{Command: "kiro-cli chat --tui"})
	if err != nil {
		t.Fatalf("NewKiroImportedInstance: %v", err)
	}
	if inst.Tool != "kiro" || inst.Command != "kiro-cli chat --tui" || inst.ProjectPath != "/home/lrasmussen/git/domutech/domutech-github" {
		t.Fatalf("unexpected imported instance: %+v", inst)
	}
	if inst.GroupPath != DefaultGroupPath {
		t.Fatalf("GroupPath = %q, want %q", inst.GroupPath, DefaultGroupPath)
	}
	if inst.KiroSessionID != "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d" || !inst.KiroDetectedAt.Equal(updated) {
		t.Fatalf("kiro binding not copied: %q %v", inst.KiroSessionID, inst.KiroDetectedAt)
	}
}

func writeKiroSessionFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func containsAll(s string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(s, needle) {
			return false
		}
	}
	return true
}
