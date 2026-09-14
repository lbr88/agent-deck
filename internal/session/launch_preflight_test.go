package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLaunchExecutableReportsMissingBareCommand(t *testing.T) {
	err := validateLaunchExecutable("omp", func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil {
		t.Fatal("missing executable passed launch preflight")
	}
	if !strings.Contains(err.Error(), `command "omp" was not found`) {
		t.Fatalf("error = %q, want the exact missing command", err)
	}
}

func TestValidateLaunchExecutableChecksFirstTokenWithArguments(t *testing.T) {
	var lookedUp string
	err := validateLaunchExecutable("codex --dangerously-bypass-approvals-and-sandbox", func(name string) (string, error) {
		lookedUp = name
		return "/opt/tools/" + name, nil
	})
	if err != nil {
		t.Fatalf("available executable rejected: %v", err)
	}
	if lookedUp != "codex" {
		t.Fatalf("looked up %q, want codex", lookedUp)
	}
}

func TestValidateLaunchExecutableLeavesShellExpressionsToShell(t *testing.T) {
	called := false
	err := validateLaunchExecutable(`env PROFILE=work wrapper-command --flag`, func(string) (string, error) {
		called = true
		return "", errors.New("must not be called")
	})
	if err != nil {
		t.Fatalf("shell expression should be diagnosed by its shell: %v", err)
	}
	if called {
		t.Fatal("shell expression was incorrectly treated as one executable")
	}
}

func TestInstanceValidateLaunchExecutableSkipsInteractiveShellSentinel(t *testing.T) {
	inst := &Instance{Tool: "shell", Command: "shell"}
	if err := inst.validateLaunchExecutable(); err != nil {
		t.Fatalf("interactive shell sentinel must not be resolved as an executable: %v", err)
	}
}

func TestInstanceValidateLaunchExecutableDefersTargetScopedLookup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	tests := []struct {
		name string
		inst *Instance
	}{
		{
			name: "ssh",
			inst: &Instance{Tool: "omp", Command: "remote-only-omp", SSHHost: "worker.example"},
		},
		{
			name: "sandbox",
			inst: &Instance{
				Tool:    "omp",
				Command: "container-only-omp",
				Sandbox: &SandboxConfig{Enabled: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.inst.validateLaunchExecutable(); err != nil {
				t.Fatalf("host PATH rejected command resolved inside the execution target: %v", err)
			}
		})
	}
}

func TestResolvedLaunchCommandForPreflightUsesConfiguredClaudeCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	configDir := filepath.Join(home, ".config", "agent-deck")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(home, "bin", "claude-wrapper")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[claude]\ncommand = \""+wrapper+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	inst := &Instance{Tool: "claude", Command: "claude"}
	if got := inst.resolvedLaunchCommandForPreflight(); got != wrapper {
		t.Fatalf("preflight command = %q, want configured wrapper %q", got, wrapper)
	}
	if err := inst.validateLaunchExecutable(); err != nil {
		t.Fatalf("configured executable wrapper was rejected: %v", err)
	}
}

func TestResolvedLaunchCommandForPreflightDefaultsEmptyScopedToolCommands(t *testing.T) {
	for _, tool := range []string{"pi", "omp"} {
		t.Run(tool, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			inst := &Instance{Tool: tool}
			if got := inst.resolvedLaunchCommandForPreflight(); got != tool {
				t.Fatalf("preflight command = %q, want default %q", got, tool)
			}
			if err := inst.validateLaunchExecutable(); err == nil || !strings.Contains(err.Error(), "was not found on PATH") {
				t.Fatalf("empty %s command bypassed executable validation: %v", tool, err)
			}
		})
	}
}
