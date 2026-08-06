package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

func seedCodexRollout(t *testing.T, codexHome, sid string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "06", "06")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	p := filepath.Join(dir, "rollout-20260606T000000-"+sid+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
}

func TestCanForkCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sid := "11111111-2222-3333-4444-555555555555"
	seedCodexRollout(t, home, sid)

	inst := NewInstanceWithTool("cx", "/tmp/p", "codex")
	inst.CodexSessionID = sid
	inst.CodexDetectedAt = time.Now()
	if !inst.CanForkCodex() {
		t.Fatal("codex session with an on-disk rollout must be forkable")
	}

	inst.CodexSessionID = "no-rollout-uuid"
	if inst.CanForkCodex() {
		t.Fatal("codex session without a rollout must NOT be forkable")
	}
}

func TestCreateForkedCodexInstance_UsesWorktreeAndForkCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sid := "11111111-2222-3333-4444-555555555555"
	seedCodexRollout(t, home, sid)

	parent := NewInstanceWithTool("cx parent", "/tmp/original", "codex")
	parent.CodexSessionID = sid
	parent.CodexDetectedAt = time.Now()

	opts := &ClaudeOptions{
		WorkDir:          "/tmp/original-wt",
		WorktreePath:     "/tmp/original-wt",
		WorktreeRepoRoot: "/tmp/original",
		WorktreeBranch:   "fork/cx-parent",
	}
	forked, cmd, err := parent.CreateForkedCodexInstanceWithOptions("cx parent (fork)", "", opts)
	if err != nil {
		t.Fatalf("CreateForkedCodexInstanceWithOptions: %v", err)
	}
	if forked.ProjectPath != "/tmp/original-wt" {
		t.Fatalf("forked ProjectPath = %q, want worktree dir", forked.ProjectPath)
	}
	if forked.WorktreePath != "/tmp/original-wt" || forked.WorktreeBranch != "fork/cx-parent" {
		t.Fatalf("worktree metadata not copied: %+v", forked)
	}
	if !forked.IsForkAwaitingStart || forked.ForkStartCommand == "" {
		t.Fatal("codex fork must defer launch via ForkStartCommand/IsForkAwaitingStart (Pi pattern)")
	}
	if !strings.Contains(cmd, "fork "+sid) {
		t.Fatalf("fork command must run `codex fork <parent-sid>`; got: %s", cmd)
	}
}

// TestCreateForkedCodexInstance_RejectsParentBindingDuringStartup reproduces
// the live rename collision from pr-review-and-merge 3. While `codex fork`
// initializes, the child process temporarily has the parent's rollout open.
// Process-FD discovery must not bind that parent ID to the new Agent Deck row,
// because an immediate explicit rename would then rename both the parent and
// the fork through the shared native thread ID.
func TestCreateForkedCodexInstance_RejectsParentBindingDuringStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	parentID := "12121212-2222-4333-8444-555555555555"
	seedCodexRollout(t, home, parentID)

	parent := NewInstanceWithTool("parent", t.TempDir(), "codex")
	parent.CodexSessionID = parentID
	parent.CodexDetectedAt = time.Now()
	forked, _, err := parent.CreateForkedCodexInstanceWithOptions("fork", "", nil)
	if err != nil {
		t.Fatalf("CreateForkedCodexInstanceWithOptions: %v", err)
	}

	if changed := forked.acceptCodexSessionID(parentID, false); changed {
		t.Fatal("fork startup accepted the parent's Codex session ID")
	}
	if forked.CodexSessionID != "" {
		t.Fatalf("fork CodexSessionID = %q, want empty until the new child thread exists", forked.CodexSessionID)
	}
}

// TestCodexBootstrapExcludesPersistedBindings reproduces the ordinary-session
// form of the live collision: a new Codex process starts in the same cwd as an
// already-running session whose rollout is still being written. The older
// session's durable binding must be excluded even when its tmux environment is
// missing CODEX_SESSION_ID, otherwise the new row temporarily adopts the old
// thread and title synchronization renames the wrong session.
func TestCodexBootstrapExcludesPersistedBindings(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	db := withTempGlobalStateDB(t)

	oldID := "13131313-2222-4333-8444-555555555555"
	newID := "14141414-2222-4333-8444-555555555555"
	projectPath := t.TempDir()
	seedCodexRollout(t, codexHome, oldID)
	seedCodexRollout(t, codexHome, newID)

	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "06")
	oldPath := filepath.Join(rolloutDir, "rollout-20260606T000000-"+oldID+".jsonl")
	newPath := filepath.Join(rolloutDir, "rollout-20260606T000000-"+newID+".jsonl")
	now := time.Now()
	if err := os.Chtimes(newPath, now.Add(-time.Second), now.Add(-time.Second)); err != nil {
		t.Fatalf("age new rollout: %v", err)
	}
	if err := os.Chtimes(oldPath, now, now); err != nil {
		t.Fatalf("refresh old rollout: %v", err)
	}

	if err := db.SaveInstance(&statedb.InstanceRow{
		ID:          "existing-agent-deck-row",
		Title:       "existing",
		ProjectPath: projectPath,
		GroupPath:   "test",
		Tool:        "codex",
		Status:      "running",
		CreatedAt:   now.Add(-time.Hour),
		ToolData:    json.RawMessage(`{"codex_session_id":"` + oldID + `"}`),
	}); err != nil {
		t.Fatalf("persist existing Codex binding: %v", err)
	}

	created := NewInstanceWithTool("new", projectPath, "codex")
	created.CodexStartedAt = now.Add(-time.Minute).UnixMilli()
	if changed := created.acceptCodexSessionID(oldID, false); changed {
		t.Fatal("central binding gate accepted a Codex thread already owned by another Agent Deck row")
	}
	if created.CodexSessionID != "" {
		t.Fatalf("CodexSessionID = %q after owned candidate, want empty", created.CodexSessionID)
	}
	excluded := created.collectOtherCodexSessionIDs()
	if !excluded[oldID] {
		t.Fatalf("persisted Codex binding %q was not excluded: %#v", oldID, excluded)
	}
	if got := created.queryCodexSession(excluded, true); got != newID {
		t.Fatalf("bootstrap candidate = %q, want unowned new thread %q", got, newID)
	}
}

func TestCreateForkedCodexInstance_PreservesReasoningEffort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sid := "11111111-2222-3333-4444-555555555555"
	seedCodexRollout(t, home, sid)

	parent := NewInstanceWithTool("cx parent", "/tmp/original", "codex")
	parent.CodexSessionID = sid
	parent.CodexDetectedAt = time.Now()
	if err := parent.ApplyLaunchReasoningEffort("high"); err != nil {
		t.Fatalf("ApplyLaunchReasoningEffort: %v", err)
	}

	forked, cmd, err := parent.CreateForkedCodexInstanceWithOptions("cx fork", "", nil)
	if err != nil {
		t.Fatalf("CreateForkedCodexInstanceWithOptions: %v", err)
	}
	if got := forked.LaunchReasoningEffort(); got != "high" {
		t.Fatalf("forked LaunchReasoningEffort() = %q, want high", got)
	}
	if !strings.Contains(cmd, "--config model_reasoning_effort=high") {
		t.Fatalf("fork command missing reasoning effort: %s", cmd)
	}
}

func TestCreateForkedCodexInstance_UsesConfiguredCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", "")
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	codexHome := filepath.Join(home, "codex work")
	cfg := &UserConfig{Codex: CodexSettings{Command: `CODEX_HOME="` + codexHome + `" codex`}}
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	ClearUserConfigCache()

	sid := "aaaaaaaa-2222-3333-4444-555555555555"
	seedCodexRollout(t, codexHome, sid)

	parent := NewInstanceWithTool("cx parent", "/tmp/original", "codex")
	parent.CodexSessionID = sid
	parent.CodexDetectedAt = time.Now()

	_, cmd, err := parent.CreateForkedCodexInstanceWithOptions("cx parent (fork)", "", nil)
	if err != nil {
		t.Fatalf("CreateForkedCodexInstanceWithOptions: %v", err)
	}
	want := `CODEX_HOME="` + codexHome + `" codex fork ` + sid
	if !strings.Contains(cmd, want) {
		t.Fatalf("configured CODEX_HOME command must be preserved for fork; want %q in %q", want, cmd)
	}
}

func TestCreateForkedCodexInstance_QuotesConfiguredCodexConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", "")
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	codexHome := filepath.Join(home, "codex config dir")
	cfg := &UserConfig{Codex: CodexSettings{ConfigDir: codexHome}}
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	ClearUserConfigCache()

	sid := "bbbbbbbb-2222-3333-4444-555555555555"
	seedCodexRollout(t, codexHome, sid)

	parent := NewInstanceWithTool("cx parent", "/tmp/original", "codex")
	parent.CodexSessionID = sid
	parent.CodexDetectedAt = time.Now()

	_, cmd, err := parent.CreateForkedCodexInstanceWithOptions("cx parent (fork)", "", nil)
	if err != nil {
		t.Fatalf("CreateForkedCodexInstanceWithOptions: %v", err)
	}
	want := "CODEX_HOME=" + shellescape.Quote(codexHome) + " "
	if !strings.Contains(cmd, want) {
		t.Fatalf("configured [codex].config_dir must be shell-quoted for fork; want %q in %q", want, cmd)
	}
}

func TestCreateForkedCodexInstance_PreservesCompatibleToolIdentity(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", codexHome)
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	cfg := &UserConfig{
		Tools: map[string]ToolDef{
			"my-codex": {
				Command:        "codex-wrapper",
				CompatibleWith: "codex",
			},
		},
	}
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	ClearUserConfigCache()

	sid := "cccccccc-2222-3333-4444-555555555555"
	seedCodexRollout(t, codexHome, sid)

	parent := NewInstanceWithTool("cx parent", "/tmp/original", "my-codex")
	parent.Command = "codex-wrapper"
	parent.CodexSessionID = sid
	parent.CodexDetectedAt = time.Now()

	forked, cmd, err := parent.CreateForkedCodexInstanceWithOptions("cx parent (fork)", "", nil)
	if err != nil {
		t.Fatalf("CreateForkedCodexInstanceWithOptions: %v", err)
	}
	if forked.Tool != "my-codex" {
		t.Fatalf("forked Tool = %q, want custom Codex-compatible tool identity", forked.Tool)
	}
	if !strings.Contains(cmd, "AGENTDECK_TOOL=my-codex") {
		t.Fatalf("fork command must preserve AGENTDECK_TOOL identity; got %q", cmd)
	}
	if !strings.Contains(cmd, "codex-wrapper fork "+sid) {
		t.Fatalf("fork command must use the compatible tool command; got %q", cmd)
	}
}

// TestCreateForkedCodexInstance_ShellQuotesEnvPrefix guards the PR #1299 review
// finding: a user-editable session title containing shell metacharacters must be
// shell-quoted in the generated fork launch command, not Go-%q-quoted (which would
// still allow $(...) / backtick expansion under a shell).
func TestCreateForkedCodexInstance_ShellQuotesEnvPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sid := "dddddddd-2222-3333-4444-555555555555"
	seedCodexRollout(t, home, sid)

	parent := NewInstanceWithTool("cx parent", "/tmp/original", "codex")
	parent.CodexSessionID = sid
	parent.CodexDetectedAt = time.Now()

	evil := "pwn $(touch /tmp/agentdeck-pwn)"
	_, cmd, err := parent.CreateForkedCodexInstanceWithOptions(evil, "", nil)
	if err != nil {
		t.Fatalf("CreateForkedCodexInstanceWithOptions: %v", err)
	}
	if !strings.Contains(cmd, "AGENTDECK_TITLE="+shellescape.Quote(evil)) {
		t.Fatalf("AGENTDECK_TITLE must be shell-quoted via shellescape.Quote; got: %s", cmd)
	}
}
