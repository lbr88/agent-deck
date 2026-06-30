package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// seedClaudeSession writes ~/.claude/sessions/<pid>.json under home with the
// given sessionId/name so ClaudeSessionName can resolve it.
func seedClaudeSession(t *testing.T, home, sessionID, name string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	b, err := json.Marshal(map[string]any{"sessionId": sessionID, "name": name})
	if err != nil {
		t.Fatalf("marshal session fields: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1234.json"), b, 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
}

// TestReconcileTitleFromClaude_UpdatesAndWritesBadge: when Claude's name differs
// from the instance Title, reconcile updates Title, returns (name,true), and
// drops the badge-update file the attach-side watcher reads (#1114 on-attach).
func TestReconcileTitleFromClaude_UpdatesAndWritesBadge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	badgeDir := t.TempDir()
	t.Setenv("AGENTDECK_BADGE_UPDATES_DIR", badgeDir)

	seedClaudeSession(t, home, "sid-1", "Conduit Federation 2SP")

	inst := &Instance{ID: "i1", Title: "rustic-island", Tool: "claude"}
	inst.tmuxSession = &tmux.Session{Name: "agentdeck_rustic_abcd1234"}

	name, changed := inst.ReconcileTitleFromClaude("sid-1")
	if !changed || name != "Conduit Federation 2SP" {
		t.Fatalf("ReconcileTitleFromClaude = (%q,%v), want (%q,true)", name, changed, "Conduit Federation 2SP")
	}
	if inst.Title != "Conduit Federation 2SP" {
		t.Errorf("Title = %q, want %q", inst.Title, "Conduit Federation 2SP")
	}
	got, err := os.ReadFile(filepath.Join(badgeDir, "agentdeck_rustic_abcd1234"))
	if err != nil {
		t.Fatalf("badge-update file missing: %v", err)
	}
	if string(got) != "Conduit Federation 2SP" {
		t.Errorf("badge-update file = %q, want %q", got, "Conduit Federation 2SP")
	}
}

// TestReconcileTitleFromClaude_NoopWhenEqual: a matching name is a no-op, with
// no badge-update file written (avoids a redundant OSC on every attach).
func TestReconcileTitleFromClaude_NoopWhenEqual(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	badgeDir := t.TempDir()
	t.Setenv("AGENTDECK_BADGE_UPDATES_DIR", badgeDir)

	seedClaudeSession(t, home, "sid-2", "already-set")
	inst := &Instance{ID: "i2", Title: "already-set", Tool: "claude"}
	inst.tmuxSession = &tmux.Session{Name: "agentdeck_x"}

	if name, changed := inst.ReconcileTitleFromClaude("sid-2"); changed || name != "" {
		t.Errorf("got (%q,%v), want no-op", name, changed)
	}
	if _, err := os.Stat(filepath.Join(badgeDir, "agentdeck_x")); !os.IsNotExist(err) {
		t.Errorf("badge-update file written for unchanged title")
	}
}

// TestReconcileTitleFromClaude_NoopWhenLocked: TitleLocked blocks the sync (#697).
func TestReconcileTitleFromClaude_NoopWhenLocked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedClaudeSession(t, home, "sid-3", "auto-name")

	inst := &Instance{ID: "i3", Title: "SCRUM-351", TitleLocked: true, Tool: "claude"}
	if _, changed := inst.ReconcileTitleFromClaude("sid-3"); changed {
		t.Errorf("locked title changed")
	}
	if inst.Title != "SCRUM-351" {
		t.Errorf("Title = %q, want unchanged SCRUM-351", inst.Title)
	}
}

// TestReconcileTitleFromClaude_NoopWhenSyncDisabled: sync_title=false opts out.
func TestReconcileTitleFromClaude_NoopWhenSyncDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".agent-deck")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("sync_title = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedClaudeSession(t, home, "sid-4", "should-not-apply")

	inst := &Instance{ID: "i4", Title: "loupe", Tool: "claude"}
	if _, changed := inst.ReconcileTitleFromClaude("sid-4"); changed {
		t.Errorf("title changed despite sync_title=false")
	}
	if inst.Title != "loupe" {
		t.Errorf("Title = %q, want unchanged loupe", inst.Title)
	}
}

// seedClaudeSessionFile writes ~/.claude/sessions/<file> with explicit fields,
// for tests that need several per-PID entries for the same sessionId.
func seedClaudeSessionFile(t *testing.T, home, file string, fields map[string]any) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal session fields: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), b, 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
}

// TestClaudeSessionNameIn_FreshestEntryWins: a resumed session leaves one
// per-PID file per run, all sharing the sessionId. The entry with the highest
// updatedAt is authoritative — not whichever sorts first in the directory.
func TestClaudeSessionNameIn_FreshestEntryWins(t *testing.T) {
	home := t.TempDir()
	// "1111.json" sorts before "2222.json"; the old behavior returned its name.
	seedClaudeSessionFile(t, home, "1111.json", map[string]any{
		"sessionId": "sid-x", "name": "stale plan title", "updatedAt": int64(1000),
	})
	seedClaudeSessionFile(t, home, "2222.json", map[string]any{
		"sessionId": "sid-x", "name": "current name", "updatedAt": int64(2000),
	})

	got := ClaudeSessionNameIn(filepath.Join(home, ".claude"), "sid-x")
	if got != "current name" {
		t.Errorf("ClaudeSessionNameIn = %q, want %q", got, "current name")
	}
}

// TestClaudeSessionNameIn_FreshestUnnamedSuppressesStaleName: when the live
// (freshest) process has no name, a stale named entry must not resurrect the
// old name.
func TestClaudeSessionNameIn_FreshestUnnamedSuppressesStaleName(t *testing.T) {
	home := t.TempDir()
	seedClaudeSessionFile(t, home, "1111.json", map[string]any{
		"sessionId": "sid-y", "name": "old name", "updatedAt": int64(1000),
	})
	seedClaudeSessionFile(t, home, "2222.json", map[string]any{
		"sessionId": "sid-y", "updatedAt": int64(2000),
	})

	if got := ClaudeSessionNameIn(filepath.Join(home, ".claude"), "sid-y"); got != "" {
		t.Errorf("ClaudeSessionNameIn = %q, want empty (freshest entry has no name)", got)
	}
}

// TestClaudeSessionNameIn_MtimeFallbackWhenNoUpdatedAt: entries without
// updatedAt (older Claude versions) fall back to file mtime for ordering.
func TestClaudeSessionNameIn_MtimeFallbackWhenNoUpdatedAt(t *testing.T) {
	home := t.TempDir()
	seedClaudeSessionFile(t, home, "1111.json", map[string]any{
		"sessionId": "sid-z", "name": "older",
	})
	seedClaudeSessionFile(t, home, "2222.json", map[string]any{
		"sessionId": "sid-z", "name": "newer",
	})
	dir := filepath.Join(home, ".claude", "sessions")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "1111.json"), old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if got := ClaudeSessionNameIn(filepath.Join(home, ".claude"), "sid-z"); got != "newer" {
		t.Errorf("ClaudeSessionNameIn = %q, want %q", got, "newer")
	}
}

func TestSyncClaudeSessionNameIn_UpdatesFreshestMatchPreservingUnknownFields(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	seedClaudeSessionFile(t, home, "1111.json", map[string]any{
		"sessionId": "sid-sync",
		"name":      "old stale",
		"updatedAt": int64(1000),
		"nested": map[string]any{
			"keep": true,
		},
	})
	seedClaudeSessionFile(t, home, "2222.json", map[string]any{
		"sessionId": "sid-sync",
		"name":      "old fresh",
		"updatedAt": int64(2000),
		"nested": map[string]any{
			"keep": "yes",
		},
		"futureClaudeField": []any{"preserve", float64(42)},
	})
	seedClaudeSessionFile(t, home, "3333.json", map[string]any{
		"sessionId": "other",
		"name":      "do not touch",
		"updatedAt": int64(3000),
	})

	if err := SyncClaudeSessionNameIn(claudeDir, "sid-sync", "Agent Deck Rename"); err != nil {
		t.Fatalf("SyncClaudeSessionNameIn: %v", err)
	}

	if got := ClaudeSessionNameIn(claudeDir, "sid-sync"); got != "Agent Deck Rename" {
		t.Fatalf("ClaudeSessionNameIn after sync = %q, want %q", got, "Agent Deck Rename")
	}

	var stale map[string]any
	readClaudeSessionMetaFile(t, home, "1111.json", &stale)
	if stale["name"] != "old stale" {
		t.Errorf("stale matching metadata name = %q, want old stale", stale["name"])
	}

	var fresh map[string]any
	readClaudeSessionMetaFile(t, home, "2222.json", &fresh)
	if fresh["name"] != "Agent Deck Rename" {
		t.Errorf("fresh matching metadata name = %q, want Agent Deck Rename", fresh["name"])
	}
	nested, ok := fresh["nested"].(map[string]any)
	if !ok || nested["keep"] != "yes" {
		t.Errorf("unknown nested field not preserved: %#v", fresh["nested"])
	}
	if got, ok := fresh["futureClaudeField"].([]any); !ok || len(got) != 2 || got[0] != "preserve" || got[1] != float64(42) {
		t.Errorf("unknown array field not preserved: %#v", fresh["futureClaudeField"])
	}

	var other map[string]any
	readClaudeSessionMetaFile(t, home, "3333.json", &other)
	if other["name"] != "do not touch" {
		t.Errorf("nonmatching metadata name = %q, want do not touch", other["name"])
	}
}

func TestSyncClaudeSessionNameIn_MissingMetadataReturnsWarningError(t *testing.T) {
	home := t.TempDir()
	err := SyncClaudeSessionNameIn(filepath.Join(home, ".claude"), "missing-sid", "new name")
	if !errors.Is(err, ErrClaudeSessionMetadataNotFound) {
		t.Fatalf("SyncClaudeSessionNameIn error = %v, want ErrClaudeSessionMetadataNotFound", err)
	}
}

func TestClaudeSessionNameIn_MalformedFreshestMatchDoesNotFallBackToStaleName(t *testing.T) {
	home := t.TempDir()
	seedClaudeSessionFile(t, home, "1111.json", map[string]any{
		"sessionId": "sid-bad-read", "name": "stale old name", "updatedAt": int64(1000),
	})
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.WriteFile(filepath.Join(dir, "2222.json"), []byte(`{"sessionId":"sid-bad-read","name":`), 0o644); err != nil {
		t.Fatalf("write malformed session file: %v", err)
	}

	if got := ClaudeSessionNameIn(filepath.Join(home, ".claude"), "sid-bad-read"); got != "" {
		t.Fatalf("ClaudeSessionNameIn = %q, want empty when freshest matching metadata is malformed", got)
	}

	best, err := freshestClaudeSessionMetaIn(filepath.Join(home, ".claude"), "sid-bad-read")
	if err == nil {
		t.Fatalf("freshestClaudeSessionMetaIn unexpectedly returned %+v for malformed freshest match", best)
	}
	if errors.Is(err, ErrClaudeSessionMetadataNotFound) {
		t.Fatalf("freshestClaudeSessionMetaIn error = %v, want actionable malformed/read error", err)
	}
}

func TestSyncClaudeSessionNameIn_IgnoresNewerMalformedUnrelatedMetadata(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	seedClaudeSessionFile(t, home, "1111.json", map[string]any{
		"sessionId": "sid-target",
		"name":      "old target name",
		"updatedAt": int64(1000),
	})
	dir := filepath.Join(claudeDir, "sessions")
	if err := os.WriteFile(filepath.Join(dir, "2222.json"), []byte(`{"sessionId":"sid-other","name":`), 0o644); err != nil {
		t.Fatalf("write malformed unrelated session file: %v", err)
	}

	if err := SyncClaudeSessionNameIn(claudeDir, "sid-target", "Agent Deck Rename"); err != nil {
		t.Fatalf("SyncClaudeSessionNameIn error = %v, want successful update despite unrelated malformed metadata", err)
	}

	if got := ClaudeSessionNameIn(claudeDir, "sid-target"); got != "Agent Deck Rename" {
		t.Fatalf("ClaudeSessionNameIn = %q, want Agent Deck Rename", got)
	}
}

func TestSyncClaudeSessionNameIn_MalformedFreshestMatchReturnsActionableErrorAndLeavesStaleFileUntouched(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	seedClaudeSessionFile(t, home, "1111.json", map[string]any{
		"sessionId": "sid-malformed-sync",
		"name":      "old stale",
		"updatedAt": int64(1000),
	})
	dir := filepath.Join(claudeDir, "sessions")
	freshPath := filepath.Join(dir, "2222.json")
	if err := os.WriteFile(freshPath, []byte(`{"sessionId":"sid-malformed-sync","name":`), 0o644); err != nil {
		t.Fatalf("write malformed session file: %v", err)
	}

	beforeFresh, err := os.ReadFile(freshPath)
	if err != nil {
		t.Fatalf("read malformed session file before sync: %v", err)
	}
	if err := SyncClaudeSessionNameIn(claudeDir, "sid-malformed-sync", "Agent Deck Rename"); err == nil {
		t.Fatal("SyncClaudeSessionNameIn unexpectedly succeeded with malformed freshest matching metadata")
	} else if errors.Is(err, ErrClaudeSessionMetadataNotFound) {
		t.Fatalf("SyncClaudeSessionNameIn error = %v, want actionable malformed/read error", err)
	}

	var stale map[string]any
	readClaudeSessionMetaFile(t, home, "1111.json", &stale)
	if stale["name"] != "old stale" {
		t.Fatalf("stale metadata name = %q, want unchanged old stale", stale["name"])
	}

	afterFresh, err := os.ReadFile(freshPath)
	if err != nil {
		t.Fatalf("read malformed session file after sync: %v", err)
	}
	if string(afterFresh) != string(beforeFresh) {
		t.Fatal("malformed freshest matching metadata should remain untouched on failed sync")
	}
}

func TestSyncClaudeSessionNameForInstance_GatesToClaudeCompatibleWithSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)
	seedClaudeSessionFile(t, home, "1234.json", map[string]any{
		"sessionId": "sid-inst",
		"name":      "before",
		"updatedAt": int64(1000),
	})

	shellInst := &Instance{Tool: "shell", ClaudeSessionID: "sid-inst", Title: "shell rename"}
	if err := SyncClaudeSessionNameForInstance(shellInst); err != nil {
		t.Fatalf("non-Claude-compatible sync returned error: %v", err)
	}
	if got := ClaudeSessionNameIn(filepath.Join(home, ".claude"), "sid-inst"); got != "before" {
		t.Fatalf("non-Claude-compatible sync changed name to %q", got)
	}

	emptyIDInst := &Instance{Tool: "claude", Title: "empty id rename"}
	if err := SyncClaudeSessionNameForInstance(emptyIDInst); err != nil {
		t.Fatalf("empty ClaudeSessionID sync returned error: %v", err)
	}
	if got := ClaudeSessionNameIn(filepath.Join(home, ".claude"), "sid-inst"); got != "before" {
		t.Fatalf("empty ClaudeSessionID sync changed name to %q", got)
	}

	claudeInst := &Instance{Tool: "claude", ClaudeSessionID: "sid-inst", Title: "locked user rename", TitleLocked: true}
	if err := SyncClaudeSessionNameForInstance(claudeInst); err != nil {
		t.Fatalf("Claude-compatible sync returned error: %v", err)
	}
	if got := ClaudeSessionNameIn(filepath.Join(home, ".claude"), "sid-inst"); got != "locked user rename" {
		t.Fatalf("Claude-compatible sync name = %q, want locked user rename", got)
	}
}

func readClaudeSessionMetaFile(t *testing.T, home, file string, out any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "sessions", file))
	if err != nil {
		t.Fatalf("read session file %s: %v", file, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal session file %s: %v", file, err)
	}
}

// TestReconcileTitleFromClaude_NoopWhenNoName: no Claude session file → no-op.
func TestReconcileTitleFromClaude_NoopWhenNoName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	inst := &Instance{ID: "i5", Title: "keep-me", Tool: "claude"}
	if _, changed := inst.ReconcileTitleFromClaude("no-such-sid"); changed {
		t.Errorf("title changed with no Claude name available")
	}
	if inst.Title != "keep-me" {
		t.Errorf("Title = %q, want unchanged keep-me", inst.Title)
	}
}
