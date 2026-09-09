package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/git"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

func seedCLIOmpForkParent(t *testing.T, profile string) *session.Instance {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)
	parent := session.NewInstanceWithGroupAndTool("OMP parent", t.TempDir(), "forks", "omp")
	dir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	root := filepath.Join(dir, "parent.jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("{\"type\":\"session\",\"id\":\"cli-parent-id\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binding := fmt.Sprintf("1\n%s\ncli-parent-id\nsaved\nlegacy-generation\n", root)
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-active-session"), []byte(binding), 0o600); err != nil {
		t.Fatal(err)
	}
	if !parent.CanForkOmp() {
		t.Fatal("seed OMP parent binding is not forkable")
	}
	storage, err := session.NewStorageWithProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if err := storage.Save([]*session.Instance{parent}); err != nil {
		t.Fatal(err)
	}
	return parent
}

func TestSessionForkCheckpointsOmpRecipeBeforeProviderStart(t *testing.T) {
	const profile = "_test"
	parent := seedCLIOmpForkParent(t, profile)
	oldHook := sessionForkBeforeStartHook
	seen := false
	sessionForkBeforeStartHook = func(_ *session.Instance, child *session.Instance, _ git.WorktreeStateOptions) {
		seen = true
		verify, err := session.NewStorageWithProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		defer verify.Close()
		loaded, err := verify.Load()
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range loaded {
			if candidate.ID == child.ID && candidate.IsForkAwaitingStart && candidate.ForkStartCommand == child.ForkStartCommand {
				return
			}
		}
		t.Fatalf("CLI provider-start boundary has no durable exact OMP recipe for %s", child.ID)
	}
	t.Cleanup(func() { sessionForkBeforeStartHook = oldHook })

	handleSessionFork(profile, []string{parent.ID})
	if !seen {
		t.Fatal("CLI fork returned before reaching the provider-start boundary")
	}
}
