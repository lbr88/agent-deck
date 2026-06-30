package session

import "testing"

func TestIsSupportedCodexLaunchCommandAllowsOnlyBareExecutable(t *testing.T) {
	valid := []string{
		"codex",
		"codex-nightly",
		"codex_custom",
		"CODEX_HOME=/tmp/codex codex",
		`CODEX_HOME="/tmp/codex home" EXTRA_FLAG=1 codex-nightly`,
	}
	for _, command := range valid {
		t.Run("valid_"+command, func(t *testing.T) {
			if !IsSupportedCodexLaunchCommand(command) {
				t.Fatalf("IsSupportedCodexLaunchCommand(%q) = false, want true", command)
			}
		})
	}

	invalid := []string{
		"codex exec",
		"codex && echo",
		"codex extra",
		"codex-nightly resume",
		"CODEX_HOME=/tmp/codex codex --model gpt-5",
	}
	for _, command := range invalid {
		t.Run("invalid_"+command, func(t *testing.T) {
			if IsSupportedCodexLaunchCommand(command) {
				t.Fatalf("IsSupportedCodexLaunchCommand(%q) = true, want false", command)
			}
		})
	}
}
