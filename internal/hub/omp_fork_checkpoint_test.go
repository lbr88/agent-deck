package hub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

func seedHubOmpForkParent(t *testing.T, profile string) *session.Instance {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := session.NewInstanceWithGroupAndTool("OMP parent", t.TempDir(), "forks", "omp")
	dir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	root := filepath.Join(dir, "parent.jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("{\"type\":\"session\",\"id\":\"hub-parent-id\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-active-session"), []byte(fmt.Sprintf("1\n%s\nhub-parent-id\nsaved\nlegacy-generation\n", root)), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := session.NewStorageWithProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Save([]*session.Instance{parent}); err != nil {
		t.Fatal(err)
	}
	_ = storage.Close()
	return parent
}

func TestHubOmpForkFinalizationPreservesConcurrentRename(t *testing.T) {
	const profile = "_test"
	parent := seedHubOmpForkParent(t, profile)
	oldStart := startForkedInstanceForHub
	startForkedInstanceForHub = func(child *session.Instance) error {
		writer, err := session.NewStorageWithProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		if err := writer.GetDB().WriteSessionTitle(child.ID, "renamed while provider loaded"); err != nil {
			t.Fatal(err)
		}
		child.IsForkAwaitingStart = false
		child.ForkStartCommand = ""
		child.SetStatusThreadSafe(session.StatusRunning)
		return nil
	}
	t.Cleanup(func() { startForkedInstanceForHub = oldStart })

	id, err := (LocalActionBackend{Profile: profile}).Fork(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	verify, err := session.NewStorageWithProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer verify.Close()
	row, err := verify.GetDB().LoadInstanceByID(id)
	if err != nil || row == nil {
		t.Fatalf("LoadInstanceByID: row=%+v err=%v", row, err)
	}
	if row.Title != "renamed while provider loaded" {
		t.Fatalf("post-ACK finalization clobbered concurrent rename: %q", row.Title)
	}
	loaded, err := verify.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range loaded {
		if candidate.ID == id && !candidate.IsForkAwaitingStart && candidate.ForkStartCommand == "" {
			return
		}
	}
	t.Fatalf("acknowledged child %s retained pending recipe", id)
}

func TestHubOmpForkFinalizationDoesNotResurrectDeletedCheckpoint(t *testing.T) {
	const profile = "_test"
	parent := seedHubOmpForkParent(t, profile)
	oldStart := startForkedInstanceForHub
	startForkedInstanceForHub = func(child *session.Instance) error {
		writer, err := session.NewStorageWithProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		if err := writer.GetDB().DeleteInstance(child.ID); err != nil {
			t.Fatal(err)
		}
		child.IsForkAwaitingStart = false
		child.ForkStartCommand = ""
		return nil
	}
	t.Cleanup(func() { startForkedInstanceForHub = oldStart })

	id, err := (LocalActionBackend{Profile: profile}).Fork(context.Background(), parent.ID)
	if id == "" || !errors.Is(err, statedb.ErrInstanceNotStored) {
		t.Fatalf("deleted checkpoint result = (%q, %v), want child ID + ErrInstanceNotStored", id, err)
	}
	verify, openErr := session.NewStorageWithProfile(profile)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer verify.Close()
	row, loadErr := verify.GetDB().LoadInstanceByID(id)
	if loadErr != nil || row != nil {
		t.Fatalf("finalization resurrected deleted row: row=%+v err=%v", row, loadErr)
	}
}

func TestHubForkEntryPointsCheckpointOmpBeforeProviderStart(t *testing.T) {
	for _, withOptions := range []bool{false, true} {
		t.Run(fmt.Sprintf("options=%t", withOptions), func(t *testing.T) {
			profile := "_test"
			parent := seedHubOmpForkParent(t, profile)
			probeErr := errors.New("provider probe stop")
			oldStart := startForkedInstanceForHub
			startForkedInstanceForHub = func(child *session.Instance) error {
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
						return probeErr
					}
				}
				t.Fatalf("hub provider-start boundary has no durable exact OMP recipe for %s", child.ID)
				return probeErr
			}
			t.Cleanup(func() { startForkedInstanceForHub = oldStart })
			backend := LocalActionBackend{Profile: profile}
			var id string
			var err error
			if withOptions {
				id, err = backend.ForkWithOptions(context.Background(), ForkSessionRequest{SessionID: parent.ID, Title: "child"})
			} else {
				id, err = backend.Fork(context.Background(), parent.ID)
			}
			if id == "" || !errors.Is(err, probeErr) {
				t.Fatalf("checkpointed probe result = (%q, %v), want child ID + probe error", id, err)
			}
		})
	}
}
