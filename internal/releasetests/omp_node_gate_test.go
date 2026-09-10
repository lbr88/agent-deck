package releasetests

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOmpNodeGateReportsMissingNodeAndPropagatesTestFailures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "lefthook.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		PrePush struct {
			Commands map[string]struct {
				Run string `yaml:"run"`
			} `yaml:"commands"`
		} `yaml:"pre-push"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	command := config.PrePush.Commands["omp-identity"].Run
	if command == "" {
		t.Fatal("missing OMP identity pre-push command")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell required to execute the lefthook command")
	}
	for _, tc := range []struct {
		name     string
		hasNode  bool
		nodeExit int
		wantExit int
		wantText string
	}{
		{"missing Node", false, 0, 1, "node is required for bundled OMP tracker tests."},
		{"failing tests", true, 47, 47, "OMP fixture tests executed"},
		{"passing tests", true, 0, 0, "OMP fixture tests executed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			// Keep ordinary shell utilities available while excluding the real
			// Node binary. This models missing Node, not a broken shell PATH.
			for _, name := range []string{"sh", "env", "echo", "printf"} {
				utility, err := exec.LookPath(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(utility, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.hasNode {
				probe := fmt.Sprintf(`#!/bin/sh
[ "$#" -eq 2 ] && [ "$1" = --test ] && [ "$2" = internal/session/omp/identity.test.mjs ] || exit 92
printf 'OMP fixture tests executed\n'
exit %d
`, tc.nodeExit)
				if err := os.WriteFile(filepath.Join(bin, "node"), []byte(probe), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			output, err := exec.Command(shell, "-c", command).CombinedOutput()
			exitCode := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatal(err)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != tc.wantExit || !strings.Contains(string(output), tc.wantText) {
				t.Fatalf("OMP hook: exit=%d output=%q; want exit=%d and %q", exitCode, output, tc.wantExit, tc.wantText)
			}
		})
	}
}
