package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateForkedInstanceForTool_OpenCodeUsesWorktreeDir(t *testing.T) {
	parent := NewInstanceWithTool("oc parent", "/tmp/original", "opencode")
	parent.OpenCodeSessionID = "ses_parent"
	parent.OpenCodeDetectedAt = time.Now()

	opts := &ClaudeOptions{
		WorkDir:          "/tmp/original-wt",
		WorktreePath:     "/tmp/original-wt",
		WorktreeRepoRoot: "/tmp/original",
		WorktreeBranch:   "fork/oc-parent",
	}

	forked, _, err := parent.CreateForkedInstanceForTool("oc fork", "", opts)
	if err != nil {
		t.Fatalf("CreateForkedInstanceForTool: %v", err)
	}
	if forked.Tool != "opencode" {
		t.Fatalf("forked tool = %q, want opencode", forked.Tool)
	}
	if forked.ProjectPath != "/tmp/original-wt" {
		t.Fatalf("ProjectPath = %q, want worktree dir", forked.ProjectPath)
	}
	if forked.WorktreePath != "/tmp/original-wt" ||
		forked.WorktreeRepoRoot != "/tmp/original" ||
		forked.WorktreeBranch != "fork/oc-parent" {
		t.Fatalf("worktree metadata not copied: %+v", forked)
	}
}

func TestCreateForkedInstanceForTool_CodexCompatibleUsesCodexFork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sid := "11111111-2222-3333-4444-555555555555"
	dir := filepath.Join(home, "sessions", "2026", "06", "07")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-20260607T000000-"+sid+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	parent := NewInstanceWithTool("cx parent", "/tmp/original", "codex")
	parent.CodexSessionID = sid
	parent.CodexDetectedAt = time.Now()

	forked, cmd, err := parent.CreateForkedInstanceForTool("cx fork", "", nil)
	if err != nil {
		t.Fatalf("CreateForkedInstanceForTool: %v", err)
	}
	if forked.Tool != "codex" {
		t.Fatalf("forked tool = %q, want codex", forked.Tool)
	}
	if !forked.IsForkAwaitingStart || forked.ForkStartCommand == "" {
		t.Fatal("codex fork must use deferred ForkStartCommand")
	}
	if !strings.Contains(cmd, " fork "+sid) {
		t.Fatalf("codex fork command missing parent sid: %s", cmd)
	}
}

func TestCreateForkedInstanceForTool_LocksForkTitleAcrossProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	claudeParent := NewInstanceWithTool("claude parent", t.TempDir(), "claude")
	claudeParent.ClaudeSessionID = "11111111-1111-4111-8111-111111111111"
	claudeParent.ClaudeDetectedAt = time.Now()
	MarkClaudeSessionIDVerified(claudeParent)

	codexParent := NewInstanceWithTool("codex parent", t.TempDir(), "codex")
	codexParent.CodexSessionID = "22222222-2222-4222-8222-222222222222"
	codexParent.CodexDetectedAt = time.Now()
	seedCodexRollout(t, GetCodexHomeDir(), codexParent.CodexSessionID)

	openCodeParent := NewInstanceWithTool("opencode parent", t.TempDir(), "opencode")
	openCodeParent.OpenCodeSessionID = "ses_parent_lock_test"
	openCodeParent.OpenCodeDetectedAt = time.Now()

	piParent := NewInstanceWithTool("pi parent", t.TempDir(), "pi")
	piParent.Command = "pi"
	piSessionDir := filepath.Join(home, ".pi", "agent-deck", piParent.ID)
	if err := os.MkdirAll(piSessionDir, 0o755); err != nil {
		t.Fatalf("mkdir Pi session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(piSessionDir, "session.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write Pi session: %v", err)
	}

	for _, tc := range []struct {
		name   string
		parent *Instance
	}{
		{name: "claude", parent: claudeParent},
		{name: "codex", parent: codexParent},
		{name: "opencode", parent: openCodeParent},
		{name: "pi", parent: piParent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const forkTitle = "independent child (fork)"
			forked, _, err := tc.parent.CreateForkedInstanceForTool(forkTitle, "forks", nil)
			if err != nil {
				t.Fatalf("CreateForkedInstanceForTool: %v", err)
			}
			if forked.Title != forkTitle {
				t.Fatalf("fork title = %q, want %q", forked.Title, forkTitle)
			}
			if !forked.TitleLocked {
				t.Fatal("fork title is unlocked; provider title sync can overwrite the child with its parent's title")
			}
		})
	}
}

func TestUpdateStatus_SyncsGeneratedForkTitleWithoutAcceptingParentName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	const (
		parentID   = "33333333-3333-4333-8333-333333333333"
		childID    = "44444444-4444-4444-8444-444444444444"
		parentName = "GEN-8525 centralized-docs"
		forkTitle  = "GEN-8525 centralized-docs (fork)"
	)
	seedCodexRollout(t, GetCodexHomeDir(), parentID)
	parent := NewInstanceWithTool(parentName, t.TempDir(), "codex")
	parent.CodexSessionID = parentID
	parent.CodexDetectedAt = time.Now()

	forked, _, err := parent.CreateForkedInstanceForTool(forkTitle, "forks", nil)
	if err != nil {
		t.Fatalf("CreateForkedInstanceForTool: %v", err)
	}
	forked.CodexSessionID = childID
	writeCodexStateThread(t, GetCodexHomeDir(), childID, parentName, "inherited fork preview", forked.ProjectPath, time.Now().UnixMilli())
	addCodexStateThreadNameColumn(t, GetCodexHomeDir(), childID, "")
	if err := AppendCodexSessionIndexName(GetCodexHomeDir(), childID, parentName, time.Now()); err != nil {
		t.Fatalf("seed inherited Codex name: %v", err)
	}

	if err := forked.UpdateStatus(); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if forked.Title != forkTitle {
		t.Fatalf("fork title = %q after inherited provider name, want %q", forked.Title, forkTitle)
	}
	nativeName, rowFound, hasExplicitName, err := codexStateSessionNameIn(GetCodexHomeDir(), childID)
	if err != nil {
		t.Fatalf("read native Codex fork name: %v", err)
	}
	if !rowFound || !hasExplicitName || nativeName != forkTitle {
		t.Fatalf("native Codex fork name = %q (row=%v explicit-column=%v), want %q", nativeName, rowFound, hasExplicitName, forkTitle)
	}
}
