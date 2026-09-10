package releasetests

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGovulncheckCIAndLocalGateUseSamePinnedScanner(t *testing.T) {
	repo := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "govulncheck.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	install := ""
	for _, step := range workflow.Jobs["govulncheck"].Steps {
		if step.Name == "Install govulncheck" {
			install = step.Run
		}
	}
	if install == "" {
		t.Fatal("CI has no govulncheck installation step")
	}

	// Execute both real shell artifacts. Stub only heavyweight external tools
	// and Git's staged-file inventory; no lint, network install, or full suite
	// should run inside this guard. An outdated scanner deliberately exists on
	// PATH, reproducing the local gate's former presence-only version check.
	fixture := t.TempDir()
	bin := filepath.Join(fixture, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go": `printf 'go %s\n' "$*" >> "$GOVULNCHECK_TEST_CALLS"`,
		"git": `case "$1" in
  rev-parse) printf '%s\n' "$GOVULNCHECK_TEST_ROOT" ;;
  diff) printf '.github/workflows/govulncheck.yml\n' ;;
  ls-files) ;;
  *) exit 91 ;;
esac`,
		"govulncheck": `if [ "${1-}" = -version ]; then
  printf 'Scanner: govulncheck@v1.6.0\n'
else
  printf 'outdated govulncheck %s\n' "$*" >> "$GOVULNCHECK_TEST_CALLS"
fi`,
		"golangci-lint": "exit 0",
		"gotestsum":     "exit 0",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", fixture)
	t.Setenv("GOPATH", filepath.Join(fixture, "go"))
	t.Setenv("AGENT_DECK_SKIP_PRECOMMIT_CI", "")
	t.Setenv("AGENT_DECK_PRECOMMIT_CACHE_DIR", filepath.Join(fixture, "cache"))
	t.Setenv("GOVULNCHECK_TEST_ROOT", fixture)
	ciCalls := filepath.Join(fixture, "ci-calls")
	t.Setenv("GOVULNCHECK_TEST_CALLS", ciCalls)
	if output, err := exec.Command("bash", "-eu", "-c", install).CombinedOutput(); err != nil {
		t.Fatalf("CI install step: %v\n%s", err, output)
	}
	ci, err := os.ReadFile(ciCalls)
	if err != nil {
		t.Fatal(err)
	}
	selected := strings.TrimPrefix(strings.TrimSpace(string(ci)), "go install ")
	if !regexp.MustCompile(`^golang\.org/x/vuln/cmd/govulncheck@v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(selected) {
		t.Errorf("CI must select an exact scanner release, got %q", ci)
	}
	localCalls := filepath.Join(fixture, "local-calls")
	t.Setenv("GOVULNCHECK_TEST_CALLS", localCalls)
	gate := exec.Command("bash", filepath.Join(repo, "scripts", "precommit-ci-gate.sh"))
	gate.Dir = fixture
	if output, err := gate.CombinedOutput(); err != nil {
		t.Fatalf("local gate: %v\n%s", err, output)
	}
	local, err := os.ReadFile(localCalls)
	if err != nil {
		t.Fatal(err)
	}
	if want := "go run " + selected + " ./...\n"; string(local) != want {
		t.Fatalf("local gate must run CI's exact scanner instead of trusting an installed old binary: got %q want %q", local, want)
	}
}
