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
