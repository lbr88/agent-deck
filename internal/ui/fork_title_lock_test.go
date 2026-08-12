package ui

// A forked session inherits the parent's provider name, so the title must stay
// locked throughout the UI fork pipeline.

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func forkTitleLockDeps(fake *session.Instance) forkInstanceDeps {
	return forkInstanceDeps{
		createInstance: func(_ *session.Instance, _, _ string, _ *session.ClaudeOptions) (*session.Instance, error) {
			return fake, nil
		},
		createMultiRepoDir: func(_, _ *session.Instance) error { return nil },
		startInstance:      func(_ *session.Instance) error { return nil },
		rollback:           func(_, _, _ string) {},
	}
}

func TestCompleteFork_LockTitleSetsTitleLocked(t *testing.T) {
	fake := &session.Instance{}
	inst, err := completeFork(&session.Instance{}, "my fork", "group", forkToggles{LockTitle: true}, nil, "", "", false, forkTitleLockDeps(fake))
	if err != nil {
		t.Fatalf("completeFork: %v", err)
	}
	if !inst.TitleLocked {
		t.Error("TitleLocked = false for dialog fork, want true")
	}
}
