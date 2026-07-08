package session

import (
	"strings"
	"testing"
)

func TestBuildKiroCommandFresh(t *testing.T) {
	t.Setenv("KIRO_HOME", t.TempDir())

	inst := &Instance{Tool: "kiro", Command: "kiro-cli chat --tui"}
	got := inst.buildKiroCommand(inst.Command)
	for _, want := range []string{"kiro-cli chat", "--tui"} {
		if !strings.Contains(got, want) {
			t.Fatalf("buildKiroCommand missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "--resume-id") {
		t.Fatalf("fresh command unexpectedly resumes: %q", got)
	}
}

func TestBuildKiroCommandResumeWithOptions(t *testing.T) {
	t.Setenv("KIRO_HOME", t.TempDir())

	inst := &Instance{
		Tool:          "kiro",
		Command:       "kiro-cli chat --tui",
		KiroSessionID: "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d",
	}
	if err := inst.SetKiroOptions(&KiroOptions{
		Agent:         "planner",
		Model:         "sonnet",
		TrustAllTools: true,
		TrustTools:    []string{"shell"},
	}); err != nil {
		t.Fatal(err)
	}

	got := inst.buildKiroCommand(inst.Command)
	for _, want := range []string{
		"kiro-cli chat",
		"--resume-id 75e59a16-9f76-433d-baa3-3cb8e5ef4c5d",
		"--tui",
		"--agent planner",
		"--model sonnet",
		"--trust-all-tools",
		"--trust-tools shell",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("buildKiroCommand missing %q in %q", want, got)
		}
	}
}

func TestBuildKiroCommandPreservesCustomSupportedFlags(t *testing.T) {
	t.Setenv("KIRO_HOME", t.TempDir())

	inst := &Instance{Tool: "kiro", Command: "kiro-cli chat --tui --trust-all-tools"}
	got := inst.buildKiroCommand(inst.Command)
	if !strings.Contains(got, "kiro-cli chat") || !strings.Contains(got, "--trust-all-tools") || !strings.Contains(got, "--tui") {
		t.Fatalf("buildKiroCommand = %q, want custom supported flags preserved", got)
	}
}

func TestBuildKiroCommandUsesAgentDeckAgentWhenHooksInstalled(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("KIRO_HOME", configDir)
	if _, err := InjectKiroHooks(configDir); err != nil {
		t.Fatalf("InjectKiroHooks: %v", err)
	}

	inst := &Instance{Tool: "kiro", Command: "kiro-cli chat --tui"}
	got := inst.buildKiroCommand(inst.Command)
	if !strings.Contains(got, "--agent agent-deck") {
		t.Fatalf("buildKiroCommand = %q, want Agent Deck Kiro agent", got)
	}
}

func TestBuildKiroCommandKeepsConfiguredAgentWhenHooksInstalled(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("KIRO_HOME", configDir)
	if _, err := InjectKiroHooks(configDir); err != nil {
		t.Fatalf("InjectKiroHooks: %v", err)
	}

	inst := &Instance{Tool: "kiro", Command: "kiro-cli chat --tui"}
	if err := inst.SetKiroOptions(&KiroOptions{Agent: "planner"}); err != nil {
		t.Fatal(err)
	}

	got := inst.buildKiroCommand(inst.Command)
	if !strings.Contains(got, "--agent planner") || strings.Contains(got, "--agent agent-deck") {
		t.Fatalf("buildKiroCommand = %q, want configured agent only", got)
	}
}
