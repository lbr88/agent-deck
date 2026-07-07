package releasetests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGoReleaserPublishesToCurrentRepository(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yml: %v", err)
	}

	release, _ := cfg["release"].(map[string]any)
	if _, hardcoded := release["github"]; hardcoded {
		t.Fatalf(".goreleaser.yml must not hardcode release.github; GoReleaser should infer the current repository from origin")
	}
}

func TestGoReleaserDoesNotRequireHomebrewTapForForkReleases(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}

	text := string(raw)
	if strings.Contains(text, "\nbrews:") {
		t.Fatalf(".goreleaser.yml must not require the deprecated brews publisher for fork releases")
	}
	if strings.Contains(text, "HOMEBREW_TAP_GITHUB_TOKEN") {
		t.Fatalf(".goreleaser.yml must not require HOMEBREW_TAP_GITHUB_TOKEN for fork releases")
	}
}
