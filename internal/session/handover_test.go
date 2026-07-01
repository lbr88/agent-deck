package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandoverSession_ClaudeToCodexBuildsTargetAndPrompt(t *testing.T) {
	projectPath := t.TempDir()
	source := NewInstanceWithGroupAndTool("api migration", projectPath, "domutech/infra", "claude")
	source.Command = "claude"
	source.ClaudeSessionID = "claude-session-123"
	source.Status = StatusWaiting

	result, err := HandoverSession(source, HandoverOptions{
		Target: HandoverTargetCodex,
		Peers:  []*Instance{source},
	})
	if err != nil {
		t.Fatalf("HandoverSession: %v", err)
	}
	if result.Source != source {
		t.Fatal("result.Source should be the original source instance")
	}
	if result.Target == nil {
		t.Fatal("result.Target is nil")
	}
	if result.Target.Tool != "codex" {
		t.Fatalf("target Tool = %q, want codex", result.Target.Tool)
	}
	if result.Target.Command != "codex" {
		t.Fatalf("target Command = %q, want codex", result.Target.Command)
	}
	if result.Target.Status != StatusStopped {
		t.Fatalf("target Status = %q, want stopped", result.Target.Status)
	}
	if result.Target.Title != "api migration (codex)" {
		t.Fatalf("target Title = %q", result.Target.Title)
	}
	if result.Target.ProjectPath != projectPath {
		t.Fatalf("target ProjectPath = %q, want %q", result.Target.ProjectPath, projectPath)
	}
	if result.Target.GroupPath != "domutech/infra" {
		t.Fatalf("target GroupPath = %q", result.Target.GroupPath)
	}

	prompt := result.HandoverPrompt
	for _, want := range []string{
		"handed over from claude to codex",
		"- Agent Deck title: api migration",
		"- Agent Deck id: " + source.ID,
		"- Source tool: claude",
		"- Source tool session id: claude-session-123",
		"- Project path: " + projectPath,
		"- Group: domutech/infra",
		"Native transcript history was not migrated.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestHandoverSession_RefusesSameTool(t *testing.T) {
	source := NewInstanceWithGroupAndTool("same", t.TempDir(), "grp", "codex")

	_, err := HandoverSession(source, HandoverOptions{Target: HandoverTargetCodex})
	if err == nil {
		t.Fatal("expected same-tool handover error")
	}
	if !strings.Contains(err.Error(), "same tool") || !strings.Contains(err.Error(), "fork") {
		t.Fatalf("error = %q, want same-tool fork guidance", err.Error())
	}
}

func TestHandoverSession_OpenCodeToClaudeIncludesSourceSessionID(t *testing.T) {
	source := NewInstanceWithGroupAndTool("investigate", t.TempDir(), "grp", "opencode")
	source.OpenCodeSessionID = "ses_opencode_123"

	result, err := HandoverSession(source, HandoverOptions{Target: HandoverTargetClaude})
	if err != nil {
		t.Fatalf("HandoverSession: %v", err)
	}
	if result.Target.Tool != "claude" || result.Target.Command != "claude" {
		t.Fatalf("target = tool %q command %q, want claude/claude", result.Target.Tool, result.Target.Command)
	}
	if !strings.Contains(result.HandoverPrompt, "- Source tool session id: ses_opencode_123") {
		t.Fatalf("prompt missing OpenCode session id:\n%s", result.HandoverPrompt)
	}
}

func TestHandoverSession_MissingLatestOutputUsesFallback(t *testing.T) {
	source := NewInstanceWithGroupAndTool("codex source", t.TempDir(), "grp", "codex")

	result, err := HandoverSession(source, HandoverOptions{Target: HandoverTargetClaude})
	if err != nil {
		t.Fatalf("HandoverSession: %v", err)
	}
	if result.Warning == "" {
		t.Fatal("expected warning for unavailable latest output")
	}
	if !strings.Contains(result.HandoverPrompt, "No latest output was available.") {
		t.Fatalf("prompt missing fallback latest-output text:\n%s", result.HandoverPrompt)
	}
}

func TestHandoverSession_DefaultTitleSuffixesAroundPeers(t *testing.T) {
	source := NewInstanceWithGroupAndTool("deploy", t.TempDir(), "grp", "claude")
	existing1 := NewInstanceWithGroupAndTool("deploy (codex)", source.ProjectPath, "grp", "codex")
	existing2 := NewInstanceWithGroupAndTool("deploy (codex 2)", source.ProjectPath, "grp", "codex")

	result, err := HandoverSession(source, HandoverOptions{
		Target: HandoverTargetCodex,
		Peers:  []*Instance{source, existing1, existing2},
	})
	if err != nil {
		t.Fatalf("HandoverSession: %v", err)
	}
	if result.Target.Title != "deploy (codex 3)" {
		t.Fatalf("target Title = %q, want deploy (codex 3)", result.Target.Title)
	}
}

func TestHandoverSession_CapsLongLatestOutput(t *testing.T) {
	old := handoverLastResponse
	t.Cleanup(func() { handoverLastResponse = old })
	handoverLastResponse = func(*Instance, []*Instance) (*ResponseOutput, error) {
		return &ResponseOutput{Tool: "claude", Role: "assistant", Content: strings.Repeat("0123456789\n", 4000)}, nil
	}

	source := NewInstanceWithGroupAndTool("large", t.TempDir(), "grp", "claude")
	result, err := HandoverSession(source, HandoverOptions{Target: HandoverTargetCodex})
	if err != nil {
		t.Fatalf("HandoverSession: %v", err)
	}
	if len(result.HandoverPrompt) > 20000 {
		t.Fatalf("prompt length = %d, want capped prompt", len(result.HandoverPrompt))
	}
	if !strings.Contains(result.HandoverPrompt, "[truncated") {
		t.Fatalf("prompt missing truncation marker:\n%s", result.HandoverPrompt)
	}
}

func TestHandoverGitContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := handoverGitContext(repo)
	for _, want := range []string{"Branch:", "HEAD:", "Status:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("git context missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "dirty.txt") {
		t.Fatalf("git context missing short status output:\n%s", got)
	}

	outside := handoverGitContext(t.TempDir())
	if !strings.Contains(outside, "not a git repository") {
		t.Fatalf("outside git context = %q", outside)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
