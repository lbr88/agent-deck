package main

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/update"
)

func TestResolveUpdateRepoPrefersFlagOverConfig(t *testing.T) {
	got, err := resolveUpdateRepo(session.UpdateSettings{Repo: "asheshgoplani/agent-deck"}, "lbr88/agent-deck")
	if err != nil {
		t.Fatalf("resolveUpdateRepo returned error: %v", err)
	}
	if got != "lbr88/agent-deck" {
		t.Fatalf("repo = %q, want lbr88/agent-deck", got)
	}
}

func TestResolveUpdateRepoDefaultsToUpstream(t *testing.T) {
	got, err := resolveUpdateRepo(session.UpdateSettings{}, "")
	if err != nil {
		t.Fatalf("resolveUpdateRepo returned error: %v", err)
	}
	if got != update.GitHubRepo {
		t.Fatalf("repo = %q, want %s", got, update.GitHubRepo)
	}
}

func TestResolveUpdateRepoRejectsInvalidRepo(t *testing.T) {
	if _, err := resolveUpdateRepo(session.UpdateSettings{}, "lbr88"); err == nil {
		t.Fatal("resolveUpdateRepo should reject invalid repo")
	}
}

func TestResolveUpdateChannelDefaultsToRelease(t *testing.T) {
	got, err := resolveUpdateChannel(session.UpdateSettings{}, "")
	if err != nil {
		t.Fatalf("resolveUpdateChannel returned error: %v", err)
	}
	if got != update.UpdateChannelRelease {
		t.Fatalf("channel = %q, want %s", got, update.UpdateChannelRelease)
	}
}

func TestResolveUpdateChannelPrefersFlagOverConfig(t *testing.T) {
	got, err := resolveUpdateChannel(session.UpdateSettings{Channel: update.UpdateChannelRelease}, update.UpdateChannelBranch)
	if err != nil {
		t.Fatalf("resolveUpdateChannel returned error: %v", err)
	}
	if got != update.UpdateChannelBranch {
		t.Fatalf("channel = %q, want %s", got, update.UpdateChannelBranch)
	}
}

func TestResolveUpdateChannelRejectsInvalidChannel(t *testing.T) {
	if _, err := resolveUpdateChannel(session.UpdateSettings{}, "nightly"); err == nil {
		t.Fatal("resolveUpdateChannel should reject invalid channel")
	}
}

func TestResolveUpdateBranchDefaultsToMain(t *testing.T) {
	got := resolveUpdateBranch(session.UpdateSettings{}, "")
	if got != update.DefaultUpdateBranch {
		t.Fatalf("branch = %q, want %s", got, update.DefaultUpdateBranch)
	}
}

func TestResolveUpdateBranchPrefersFlagOverConfig(t *testing.T) {
	got := resolveUpdateBranch(session.UpdateSettings{Branch: "main"}, "feature/source-update")
	if got != "feature/source-update" {
		t.Fatalf("branch = %q, want feature/source-update", got)
	}
}
