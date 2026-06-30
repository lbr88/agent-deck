package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestListCodexIndexMissingFileReturnsEmpty(t *testing.T) {
	got, err := ListCodexIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("entries = %#v, want empty", got)
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
	dir := filepath.Join(home, "sessions", "2026", "06", "30")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir codex rollout dir: %v", err)
	}
	path := filepath.Join(dir, "rollout-2026-06-30T10-00-00-"+sessionID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write codex rollout: %v", err)
	}
}
