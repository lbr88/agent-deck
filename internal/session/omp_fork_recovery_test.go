package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeOmpForkRecoveryFixture(t *testing.T) (*Instance, *Instance, string, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := &Instance{ID: "fork-parent", Tool: "omp", Command: "omp", ProjectPath: t.TempDir()}
	target := &Instance{ID: "fork-child", Title: "Independent child", Tool: "omp", Command: "omp", ProjectPath: parent.ProjectPath}
	parentDir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	targetDir := filepath.Join(home, ".omp", "agent-deck", target.ID)
	parentFile := filepath.Join(parentDir, "active-parent.jsonl")
	writeOmpValidationTranscript(t, parentFile, "parent-id")
	writeOmpValidationTranscript(t, filepath.Join(parentDir, "historical-parent.jsonl"), "historical-parent-id")
	writeOmpValidationBinding(t, parentDir, parentFile, "parent-id", "saved", "parent-generation")
	return parent, target, parentDir, targetDir, parentFile
}

func writeOmpForkRecoveryProbe(t *testing.T, home string) (string, string) {
	t.Helper()
	logFile := filepath.Join(home, "omp-invocations")
	probe := filepath.Join(home, "omp-recovery-probe")
	script := `#!/bin/bash
set -eu
mode=
source_file=
session_dir=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --fork|--resume) mode="$1"; shift; source_file="${1-}" ;;
    --session-dir) shift; session_dir="${1-}" ;;
    --extension) shift; test -s "${1-}" ;;
  esac
  shift
done
printf '%s|%s|%s\n' "$mode" "$source_file" "$session_dir" >> "$OMP_FORK_LOG"
case "$mode" in
  --fork)
    child_file="$session_dir/child-root.jsonl"
    printf '{"type":"session","id":"child-id"}\n' > "$child_file"
    printf '1\n%s\nchild-id\nsaved\n%s\n' "$child_file" "$AGENTDECK_OMP_LAUNCH_ID" > "$session_dir/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
    printf 'preserve across retry\n' > "$session_dir/provider-artifact"
    ;;
  --resume)
    test -f "$source_file"
    ;;
  *) exit 44 ;;
esac
`
	if err := os.WriteFile(probe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMP_FORK_LOG", logFile)
	return probe, logFile
}

func TestOmpForkRetryResumesAcknowledgedChildWithoutForkingAgain(t *testing.T) {
	parent, target, _, targetDir, parentFile := makeOmpForkRecoveryFixture(t)
	probe, logFile := writeOmpForkRecoveryProbe(t, os.Getenv("HOME"))
	command, err := parent.buildOmpForkCommandForTarget(target, probe)
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if output, err := exec.Command("bash", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("fork attempt %d failed: %v\n%s", attempt, err, output)
		}
	}

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "--fork|"+parentFile+"|") || !strings.HasPrefix(lines[1], "--resume|") {
		t.Fatalf("retry did not transition from native fork to exact child resume:\n%s", logBytes)
	}
	if strings.Contains(lines[1], parentFile) {
		t.Fatalf("retry resumed parent instead of child: %s", lines[1])
	}
	if data, err := os.ReadFile(filepath.Join(targetDir, "provider-artifact")); err != nil || string(data) != "preserve across retry\n" {
		t.Fatalf("retry destroyed target artifact: %q, %v", data, err)
	}
	for _, file := range []string{parentFile, filepath.Join(filepath.Dir(parentFile), "historical-parent.jsonl")} {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("fork removed parent history %s: %v", file, err)
		}
	}
}

func TestOmpForkResumesBoundTargetDespiteRetainedHistoricalRoots(t *testing.T) {
	parent, target, _, targetDir, _ := makeOmpForkRecoveryFixture(t)
	probe, logFile := writeOmpForkRecoveryProbe(t, os.Getenv("HOME"))
	activeChild := filepath.Join(targetDir, "active-child.jsonl")
	writeOmpValidationTranscript(t, activeChild, "child-id")
	writeOmpValidationTranscript(t, filepath.Join(targetDir, "historical-child.jsonl"), "old-child-id")
	writeOmpValidationBinding(t, targetDir, activeChild, "child-id", "saved", "child-generation")
	command, err := parent.buildOmpForkCommandForTarget(target, probe)
	if err != nil {
		t.Fatal(err)
	}

	if output, err := exec.Command("bash", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("bound fork retry failed: %v\n%s", err, output)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(logBytes), "--resume|"+activeChild+"|") {
		t.Fatalf("bound child was not resumed exactly: %s", logBytes)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "historical-child.jsonl")); err != nil {
		t.Fatalf("historical child root was not preserved: %v", err)
	}
}

func TestOmpForkRejectsUnboundOrPendingNonemptyTargetAndPreservesEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prepare    func(t *testing.T, dir string)
		wantReason string
	}{
		{
			name: "unbound transcript",
			prepare: func(t *testing.T, dir string) {
				writeOmpValidationTranscript(t, filepath.Join(dir, "orphan.jsonl"), "orphan-id")
				if err := os.WriteFile(filepath.Join(dir, "irreplaceable-note"), []byte("keep me\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "history preserved",
		},
		{
			name: "control files without binding",
			prepare: func(t *testing.T, dir string) {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("interrupted\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "never acknowledged",
		},
		{
			name: "pending missing child",
			prepare: func(t *testing.T, dir string) {
				writeOmpValidationBinding(t, dir, filepath.Join(dir, "missing.jsonl"), "pending-child", "pending", "child-generation")
			},
			wantReason: "pending",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, target, _, targetDir, _ := makeOmpForkRecoveryFixture(t)
			probe, logFile := writeOmpForkRecoveryProbe(t, os.Getenv("HOME"))
			tc.prepare(t, targetDir)
			before := map[string]string{}
			_ = filepath.Walk(targetDir, func(file string, info os.FileInfo, err error) error {
				if err == nil && info.Mode().IsRegular() {
					data, readErr := os.ReadFile(file)
					if readErr == nil {
						before[file] = string(data)
					}
				}
				return nil
			})
			command, err := parent.buildOmpForkCommandForTarget(target, probe)
			if err != nil {
				t.Fatal(err)
			}
			output, runErr := exec.Command("bash", "-c", command).CombinedOutput()
			if runErr == nil || !strings.Contains(strings.ToLower(string(output)), tc.wantReason) {
				t.Fatalf("ambiguous target was not rejected actionably: %v\n%s", runErr, output)
			}
			if _, err := os.Stat(logFile); !os.IsNotExist(err) {
				t.Fatalf("OMP was invoked for unsafe target: %v", err)
			}
			for file, want := range before {
				data, err := os.ReadFile(file)
				if err != nil || string(data) != want {
					t.Errorf("target evidence changed at %s: %q, %v", file, data, err)
				}
			}
		})
	}
}

func TestOmpForkAllowsExistingTrulyEmptyTarget(t *testing.T) {
	parent, target, _, targetDir, parentFile := makeOmpForkRecoveryFixture(t)
	probe, logFile := writeOmpForkRecoveryProbe(t, os.Getenv("HOME"))
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command, err := parent.buildOmpForkCommandForTarget(target, probe)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("bash", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("empty target native fork failed: %v\n%s", err, output)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(logBytes), fmt.Sprintf("--fork|%s|%s", parentFile, targetDir)) {
		t.Fatalf("empty target did not native-fork parent: %s", logBytes)
	}
}

func TestOmpForkRejectsUnreadableExistingTargetAndPreservesIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory read permissions")
	}
	parent, target, _, targetDir, _ := makeOmpForkRecoveryFixture(t)
	probe, logFile := writeOmpForkRecoveryProbe(t, os.Getenv("HOME"))
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(targetDir, "preserve-me")
	if err := os.WriteFile(evidence, []byte("irreplaceable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(targetDir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(targetDir, 0o700) })
	command, err := parent.buildOmpForkCommandForTarget(target, probe)
	if err != nil {
		t.Fatal(err)
	}
	output, runErr := exec.Command("bash", "-c", command).CombinedOutput()
	if runErr == nil || !strings.Contains(strings.ToLower(string(output)), "inspect") || !strings.Contains(strings.ToLower(string(output)), "history preserved") {
		t.Fatalf("unreadable target was not rejected actionably: %v\n%s", runErr, output)
	}
	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Fatalf("OMP was invoked for unreadable target: %v", err)
	}
	if err := os.Chmod(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(evidence); err != nil || string(data) != "irreplaceable\n" {
		t.Fatalf("unreadable target evidence changed: %q, %v", data, err)
	}
}
