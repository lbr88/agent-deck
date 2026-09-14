package session

import (
	"fmt"
	"os/exec"
	"strings"
)

// validateLaunchExecutable reports a missing simple command before tmux is
// created. Complex shell expressions are left to the shell because reducing
// one to its first token would misdiagnose env prefixes, pipelines and user
// wrappers. Their stderr is preserved by the normal spawn-failure path.
func validateLaunchExecutable(command string, lookPath func(string) (string, error)) error {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "|&;<>()\n") {
		return nil
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil
	}
	executable := fields[0]
	if executable == "env" || executable == "export" || strings.ContainsAny(executable, `"'$\\=`) {
		return nil
	}
	if _, err := lookPath(executable); err != nil {
		return fmt.Errorf("command %q was not found on PATH; install it or configure this session with an executable command: %w", executable, err)
	}
	return nil
}

func (i *Instance) validateLaunchExecutable() error {
	// "shell" is the UI/CLI sentinel for an ordinary interactive shell
	// session, not an executable that Agent Deck launches. tmux supplies the
	// user's shell for this case.
	if i.Tool == "shell" && strings.TrimSpace(i.Command) == "shell" {
		return nil
	}
	// SSH and sandbox commands are resolved by the remote host or container,
	// not by Agent Deck's host process. A host-side LookPath would reject a
	// perfectly valid target-only install before ssh/docker can run it. Any
	// target-side command failure remains visible in the retained tmux pane and
	// durable launch diagnostics.
	if i.IsSSH() || i.IsSandboxed() {
		return nil
	}
	return validateLaunchExecutable(i.resolvedLaunchCommandForPreflight(), exec.LookPath)
}

// resolvedLaunchCommandForPreflight mirrors the default-command substitutions
// performed by each builder. Validating the persisted placeholder (for example
// "claude") would reject a perfectly valid configured absolute wrapper before
// the builder gets a chance to substitute it.
func (i *Instance) resolvedLaunchCommandForPreflight() string {
	command := strings.TrimSpace(i.Command)
	switch {
	case IsClaudeCompatible(i.Tool) && (command == "" || command == "claude"):
		return GetClaudeCommandForInstance(i)
	case IsCodexCompatible(i.Tool):
		return i.resolveCodexCommand(command)
	case i.Tool == "pi" && command == "":
		return GetToolCommand("pi")
	case i.Tool == "omp" && command == "":
		return GetToolCommand("omp")
	case i.Tool == "gemini" && command == "gemini":
		return GetToolCommand("gemini")
	case i.Tool == "opencode" && command == "opencode":
		return GetToolCommand("opencode")
	case i.Tool == "kiro":
		return i.resolveKiroCommand(command)
	case i.Tool == "cursor" && isDefaultCursorInvocation(command):
		return GetToolCommand("cursor")
	case i.Tool == "copilot" && command == "copilot":
		return GetToolCommand("copilot")
	case i.Tool == "hermes" && (command == "" || command == "hermes"):
		return GetToolCommand("hermes")
	case i.Tool == "crush" && (command == "" || command == "crush"):
		return GetCrushCommand()
	case i.Tool == "deepseek" && !i.deepSeekCommandIsPassthrough():
		return i.resolveDeepSeekCommand()
	default:
		return command
	}
}
