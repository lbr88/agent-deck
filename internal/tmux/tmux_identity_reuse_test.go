package tmux

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStart_ReusedPersistedIdentityNeverRegeneratesOnExistingTarget covers
// the race where systemd (or another restart owner) recreates the persisted
// tmux target between teardown and Start. A normal fresh-session collision may
// choose a new suffix; a persisted restart must replace the exact target or
// fail, because changing Name would split runtime identity from SQLite again.
func TestStart_ReusedPersistedIdentityNeverRegeneratesOnExistingTarget(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("no tmux binary available: %v", err)
	}

	workDir := t.TempDir()
	original := NewSession("persisted-reuse-"+randomServerSuffix(t), workDir)
	original.LaunchAs = "direct"
	require.NoError(t, original.Start(""))
	stableName := original.Name
	t.Cleanup(func() { _ = original.Kill() })

	replacement := NewSession("renamed display title", workDir)
	replacement.Name = stableName
	replacement.LaunchAs = "direct"
	replacement.ReusePersistedIdentity = true
	t.Cleanup(func() { _ = replacement.Kill() })

	require.NoError(t, replacement.Start(""))
	require.Equal(t, stableName, replacement.Name,
		"persisted restart must never generate a different tmux identity")
	require.True(t, replacement.Exists(), "replacement must own the persisted target")
}

func TestStart_FreshIdentityStillRegeneratesOnCollision(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("no tmux binary available: %v", err)
	}

	workDir := t.TempDir()
	original := NewSession("fresh-collision-"+randomServerSuffix(t), workDir)
	original.LaunchAs = "direct"
	require.NoError(t, original.Start(""))
	occupiedName := original.Name
	t.Cleanup(func() { _ = original.Kill() })

	fresh := NewSession("fresh display title", workDir)
	fresh.Name = occupiedName
	fresh.LaunchAs = "direct"
	t.Cleanup(func() { _ = fresh.Kill() })

	require.NoError(t, fresh.Start(""))
	require.NotEqual(t, occupiedName, fresh.Name,
		"a new unpersisted session must retain the collision-avoidance behavior")
	require.True(t, original.Exists(), "fresh collision handling must not replace the existing target")
	require.True(t, fresh.Exists(), "fresh collision handling must start the regenerated target")
}
