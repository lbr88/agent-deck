package session

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
	_ "modernc.org/sqlite"
)

func TestListCodexIndexLatestRecordWins(t *testing.T) {
	home := t.TempDir()
	writeCodexIndex(t, home,
		`{"id":"11111111-1111-1111-1111-111111111111","thread_name":"old","updated_at":"2026-06-30T09:00:00Z"}`,
		`{"id":"11111111-1111-1111-1111-111111111111","thread_name":"new","updated_at":"2026-06-30T10:00:00Z"}`,
	)

	got, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ThreadName != "new" {
		t.Fatalf("latest record not selected: %#v", got)
	}
}

func TestListCodexIndexSortsLatestFirst(t *testing.T) {
	home := t.TempDir()
	writeCodexIndex(t, home,
		`{"id":"11111111-1111-1111-1111-111111111111","thread_name":"older","updated_at":"2026-06-30T09:00:00Z"}`,
		`{"id":"22222222-2222-2222-2222-222222222222","thread_name":"newer","updated_at":"2026-06-30T10:00:00Z"}`,
	)

	got, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("entries not sorted newest first: %#v", got)
	}
}

func TestListCodexIndexSkipsMalformedSessionIDs(t *testing.T) {
	home := t.TempDir()
	validID := "22222222-2222-2222-2222-222222222222"
	writeCodexIndex(t, home,
		`{"id":"not-a-uuid","thread_name":"bad plain","updated_at":"2026-06-30T10:00:00Z"}`,
		`{"id":"bad*id","thread_name":"bad glob","updated_at":"2026-06-30T11:00:00Z"}`,
		`{"id":"bad?[id]","thread_name":"bad glob chars","updated_at":"2026-06-30T12:00:00Z"}`,
		`{"id":"`+validID+`","thread_name":"valid","updated_at":"2026-06-30T13:00:00Z"}`,
	)

	got, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != validID {
		t.Fatalf("entries = %#v, want only valid ID %s", got, validID)
	}
}

func TestListCodexIndexMissingFileReturnsEmpty(t *testing.T) {
	got, err := ListCodexIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("entries = %#v, want empty", got)
	}
}

func TestListCodexIndexMergesModernCodexStateThreads(t *testing.T) {
	home := t.TempDir()
	id := "33333333-3333-3333-3333-333333333333"
	projectPath := "/home/user/git/domutech/domain-monitor"
	writeCodexStateThread(t, home, id, "domain-monitor health checks", "staging lambda details", projectPath, 1783330000000)

	got, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %#v, want one modern state thread", got)
	}
	if got[0].ID != id || got[0].ThreadName != "domain-monitor health checks" || got[0].Path != projectPath {
		t.Fatalf("entry = %#v, want id/title/path from state thread", got[0])
	}
	if got[0].UpdatedAt.UnixMilli() != 1783330000000 {
		t.Fatalf("UpdatedAt = %s, want unix ms 1783330000000", got[0].UpdatedAt)
	}
}

func TestListCodexIndexModernStateFillsLegacyPath(t *testing.T) {
	home := t.TempDir()
	id := "44444444-4444-4444-4444-444444444444"
	projectPath := "/home/user/git/domutech/domain-monitor"
	writeCodexIndex(t, home,
		`{"id":"`+id+`","thread_name":"legacy title","updated_at":"2026-06-30T10:00:00Z"}`,
	)
	writeCodexStateThread(t, home, id, "", "state preview", projectPath, time.Date(2026, 6, 30, 9, 0, 0, 0, time.UTC).UnixMilli())

	got, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %#v, want one merged entry", got)
	}
	if got[0].ThreadName != "legacy title" {
		t.Fatalf("ThreadName = %q, want legacy title", got[0].ThreadName)
	}
	if got[0].Path != projectPath {
		t.Fatalf("Path = %q, want state cwd %q", got[0].Path, projectPath)
	}
}

func TestListCodexIndexMalformedJSONReportsLine(t *testing.T) {
	home := t.TempDir()
	writeCodexIndex(t, home,
		`{"id":"11111111-1111-1111-1111-111111111111","thread_name":"ok","updated_at":"2026-06-30T09:00:00Z"}`,
		`not-json`,
	)

	_, err := ListCodexIndex(home)
	if err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error %q does not mention line 2", err)
	}
}

func TestResolveCodexIndexTargetUUIDAndName(t *testing.T) {
	home := t.TempDir()
	id := "22222222-2222-2222-2222-222222222222"
	writeCodexIndex(t, home, `{"id":"`+id+`","thread_name":"work item","updated_at":"2026-06-30T10:00:00Z"}`)
	writeCodexRollout(t, home, id)

	byID, err := ResolveCodexIndexTarget(home, id)
	if err != nil {
		t.Fatal(err)
	}
	byName, err := ResolveCodexIndexTarget(home, "work item")
	if err != nil {
		t.Fatal(err)
	}
	if byID.ID != id || byName.ID != id {
		t.Fatalf("resolution mismatch byID=%#v byName=%#v", byID, byName)
	}
}

func TestResolveCodexIndexTargetUUIDWithRolloutOnly(t *testing.T) {
	home := t.TempDir()
	id := "33333333-3333-3333-3333-333333333333"
	writeCodexRollout(t, home, id)

	got, err := ResolveCodexIndexTarget(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id {
		t.Fatalf("resolved ID = %q, want %q", got.ID, id)
	}
}

func TestResolveCodexIndexTargetAmbiguousName(t *testing.T) {
	home := t.TempDir()
	idA := "44444444-4444-4444-4444-444444444444"
	idB := "55555555-5555-5555-5555-555555555555"
	writeCodexIndex(t, home,
		`{"id":"`+idA+`","thread_name":"same","updated_at":"2026-06-30T10:00:00Z"}`,
		`{"id":"`+idB+`","thread_name":"same","updated_at":"2026-06-30T11:00:00Z"}`,
	)
	writeCodexRollout(t, home, idA)
	writeCodexRollout(t, home, idB)

	_, err := ResolveCodexIndexTarget(home, "same")
	if !errors.Is(err, ErrCodexSessionAmbiguous) {
		t.Fatalf("err = %v, want ErrCodexSessionAmbiguous", err)
	}
}

func TestResolveCodexIndexTargetMissingRollout(t *testing.T) {
	home := t.TempDir()
	id := "66666666-6666-6666-6666-666666666666"
	writeCodexIndex(t, home, `{"id":"`+id+`","thread_name":"no rollout","updated_at":"2026-06-30T10:00:00Z"}`)

	_, err := ResolveCodexIndexTarget(home, "no rollout")
	if !errors.Is(err, ErrCodexRolloutMissing) {
		t.Fatalf("err = %v, want ErrCodexRolloutMissing", err)
	}
}

func TestResolveCodexIndexTargetSkipsMalformedIDsBeforeNameResolution(t *testing.T) {
	home := t.TempDir()
	poisonedID := "bad*id"
	writeCodexIndex(t, home, `{"id":"`+poisonedID+`","thread_name":"poisoned name","updated_at":"2026-06-30T10:00:00Z"}`)
	writeCodexRollout(t, home, poisonedID)

	_, err := ResolveCodexIndexTarget(home, "poisoned name")
	if !errors.Is(err, ErrCodexSessionNotFound) {
		t.Fatalf("err = %v, want ErrCodexSessionNotFound", err)
	}
}

func TestResolveCodexIndexTargetUnknown(t *testing.T) {
	_, err := ResolveCodexIndexTarget(t.TempDir(), "missing")
	if !errors.Is(err, ErrCodexSessionNotFound) {
		t.Fatalf("err = %v, want ErrCodexSessionNotFound", err)
	}
}

func TestCodexRolloutExists(t *testing.T) {
	home := t.TempDir()
	id := "77777777-7777-7777-7777-777777777777"
	if CodexRolloutExists(home, id) {
		t.Fatal("rollout should not exist before helper writes it")
	}
	writeCodexRollout(t, home, id)
	if !CodexRolloutExists(home, id) {
		t.Fatal("rollout should exist")
	}
}

func TestCodexRolloutExistsRejectsMalformedSessionID(t *testing.T) {
	home := t.TempDir()
	poisonedID := "bad*id"
	writeCodexRollout(t, home, poisonedID)

	if CodexRolloutExists(home, poisonedID) {
		t.Fatal("malformed Codex session ID with glob metacharacters must not match rollout files")
	}
}

func TestCodexRolloutCWDReadsSessionMetadata(t *testing.T) {
	home := t.TempDir()
	sessionID := "44444444-4444-4444-4444-444444444444"
	projectPath := filepath.Join(home, "project")
	writeCodexRolloutBody(t, home, sessionID,
		`{"type":"session_meta","payload":{"id":"`+sessionID+`","cwd":"`+projectPath+`"}}`+"\n",
	)

	got := CodexRolloutCWD(home, sessionID)
	if got != projectPath {
		t.Fatalf("CodexRolloutCWD = %q, want %q", got, projectPath)
	}
}

func TestAppendCodexSessionIndexNameCreatesJSONLRecord(t *testing.T) {
	home := t.TempDir()
	id := "55555555-5555-5555-5555-555555555555"
	now := time.Date(2026, 6, 30, 10, 0, 0, 123, time.UTC)

	if err := AppendCodexSessionIndexName(home, id, "renamed", now); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID         string `json:"id"`
		ThreadName string `json:"thread_name"`
		UpdatedAt  string `json:"updated_at"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.ThreadName != "renamed" || got.UpdatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("bad record: %#v", got)
	}
}

func TestSyncCodexSessionNameInUpdatesStateDBThreadTitleAndJSONIndex(t *testing.T) {
	home := t.TempDir()
	id := "55555555-5555-5555-5555-555555555555"
	now := time.Date(2026, 6, 30, 10, 0, 0, 123, time.UTC)
	writeCodexStateThread(t, home, id, "old title", "old preview", "/repo", now.Add(-time.Hour).UnixMilli())

	if err := SyncCodexSessionNameIn(home, id, "renamed", now); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var title string
	if err := db.QueryRow(`SELECT title FROM threads WHERE id = ?`, id).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "renamed" {
		t.Fatalf("state thread title = %q, want renamed", title)
	}

	data, err := os.ReadFile(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"thread_name":"renamed"`) {
		t.Fatalf("session_index.jsonl missing renamed title:\n%s", string(data))
	}
}

func TestSyncCodexSessionNameInMissingStateDBStillWritesJSONIndex(t *testing.T) {
	home := t.TempDir()
	id := "66666666-6666-6666-6666-666666666666"

	if err := SyncCodexSessionNameIn(home, id, "json-only", time.Now()); err != nil {
		t.Fatal(err)
	}

	entries, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != id || entries[0].ThreadName != "json-only" {
		t.Fatalf("entries = %#v, want json-only title", entries)
	}
}

func TestCodexSessionNameInReadsNativeThreadTitle(t *testing.T) {
	home := t.TempDir()
	id := "77777777-7777-7777-7777-777777777777"
	writeCodexStateThread(t, home, id, "renamed with slash command", "preview", "/repo", time.Now().UnixMilli())

	got, err := CodexSessionNameIn(home, id)
	if err != nil {
		t.Fatalf("CodexSessionNameIn: %v", err)
	}
	if got != "renamed with slash command" {
		t.Fatalf("CodexSessionNameIn = %q, want slash-command title", got)
	}
}

func TestReconcileTitleFromCodexUpdatesLockedTitleAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	db := withTempGlobalStateDB(t)
	id := "88888888-8888-8888-8888-888888888888"
	writeCodexStateThread(t, GetCodexHomeDir(), id, "renamed from codex", "preview", "/repo", time.Now().UnixMilli())

	inst := NewInstanceWithTool("agent deck title", "/repo", "codex")
	inst.CodexSessionID = id
	inst.TitleLocked = true
	if err := db.SaveInstance(&statedb.InstanceRow{
		ID:          inst.ID,
		Title:       inst.Title,
		ProjectPath: inst.ProjectPath,
		GroupPath:   inst.GroupPath,
		Tool:        inst.Tool,
		Status:      "idle",
		CreatedAt:   time.Now(),
		TitleLocked: true,
		ToolData:    json.RawMessage(`{"codex_session_id":"` + id + `"}`),
	}); err != nil {
		t.Fatalf("SaveInstance: %v", err)
	}

	name, changed, err := inst.ReconcileTitleFromCodex()
	if err != nil {
		t.Fatalf("ReconcileTitleFromCodex: %v", err)
	}
	if !changed || name != "renamed from codex" || inst.Title != name {
		t.Fatalf("reconcile = (%q, %v), instance title %q", name, changed, inst.Title)
	}
	rows, err := db.LoadInstances()
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	if len(rows) != 1 || rows[0].Title != "renamed from codex" {
		t.Fatalf("persisted rows = %#v, want renamed title", rows)
	}
	if !inst.TitleLocked {
		t.Fatal("Codex rename must not silently change the user's title-lock setting")
	}
}

func TestCodexSessionBindSyncsAgentDeckTitleToIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	inst := NewInstanceWithTool("github import", t.TempDir(), "codex")
	id := "66666666-6666-6666-6666-666666666666"

	inst.bindCodexSessionFromHook(id, "thread/started")

	entries, err := ListCodexIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("codex index entries = %#v, want one title sync record", entries)
	}
	if entries[0].ID != id || entries[0].ThreadName != "github import" {
		t.Fatalf("codex index entry = %#v, want id %q title %q", entries[0], id, "github import")
	}
}

func writeCodexIndex(t *testing.T, home string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write codex index: %v", err)
	}
}

func writeCodexRollout(t *testing.T, home, sessionID string) {
	t.Helper()
	writeCodexRolloutBody(t, home, sessionID, "{}\n")
}

func writeCodexRolloutBody(t *testing.T, home, sessionID, body string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "06", "30")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir codex rollout dir: %v", err)
	}
	path := filepath.Join(dir, "rollout-2026-06-30T10-00-00-"+sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write codex rollout: %v", err)
	}
}

func writeCodexStateThread(t *testing.T, home, id, title, preview, cwd string, updatedAtMS int64) {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open codex state sqlite: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			rollout_path TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT '',
			model_provider TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			sandbox_policy TEXT NOT NULL DEFAULT '',
			approval_mode TEXT NOT NULL DEFAULT '',
			tokens_used INTEGER NOT NULL DEFAULT 0,
			has_user_event INTEGER NOT NULL DEFAULT 0,
			archived INTEGER NOT NULL DEFAULT 0,
			preview TEXT NOT NULL DEFAULT '',
			recency_at INTEGER NOT NULL DEFAULT 0,
			recency_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER
		)
	`)
	if err != nil {
		t.Fatalf("create codex threads table: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO threads (id, title, preview, cwd, updated_at, updated_at_ms, recency_at, recency_at_ms, archived)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
	`, id, title, preview, cwd, updatedAtMS/1000, updatedAtMS, updatedAtMS/1000, updatedAtMS)
	if err != nil {
		t.Fatalf("insert codex thread: %v", err)
	}
}
