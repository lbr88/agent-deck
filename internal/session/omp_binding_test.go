package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The binding, not directory order or modification time, is the active
// conversation. Native OMP branches legitimately leave the parent on disk.
func TestOmpRecordedBindingSurvivesHistoricalRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "approval", Tool: "omp", Command: "omp"}
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(dir, "2026-08-28T02-56-01-192Z_01a0464b-b2a8-716b-a5ac-5a31f14a4033.jsonl")
	parent := filepath.Join(dir, "2026-08-27T11-32-37-443Z_01a042fe-4dc3-7486-bc27-6838d285b356.jsonl")
	for file, id := range map[string]string{
		active: "01a0464b-b2a8-716b-a5ac-5a31f14a4033",
		parent: "01a042fe-4dc3-7486-bc27-6838d285b356",
	} {
		if err := os.WriteFile(file, []byte(fmt.Sprintf("{\"type\":\"title\",\"title\":\"kept history\"}\n{\"type\":\"session\",\"id\":%q}\n", id)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Fixed-line format is readable by the target shell without jq/node.
	binding := "1\n" + active + "\n01a0464b-b2a8-716b-a5ac-5a31f14a4033\nsaved\nlegacy\n"
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-active-session"), []byte(binding), 0o600); err != nil {
		t.Fatal(err)
	}
	if !inst.CanForkOmp() {
		t.Error("a bound OMP conversation must remain forkable when its historical parent is retained")
	}
	bin := filepath.Join(home, "omp-probe")
	if err := os.WriteFile(bin, []byte(`#!/bin/sh
if [ "$1" != "$EXPECTED_MODE" ] || [ "$2" != "$EXPECTED_ACTIVE" ]; then echo 'wrong OMP conversation' >&2; exit 21; fi
if [ "$1" = --resume ]; then
  printf '1\n%s\n01a0464b-b2a8-716b-a5ac-5a31f14a4033\nsaved\n%s\n' "$2" "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
fi
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPECTED_ACTIVE", active)
	t.Setenv("EXPECTED_MODE", "--resume")
	run := exec.Command("bash", "-c", inst.buildOmpCommand(bin))
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("resume must use recorded binding despite retained history: %v\n%s", err, output)
	}
	t.Setenv("EXPECTED_MODE", "--fork")
	forkCommand, err := inst.buildOmpForkCommandForTarget(&Instance{ID: "approval-fork", Tool: "omp"}, bin)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("bash", "-c", forkCommand).CombinedOutput(); err != nil {
		t.Fatalf("fork must use the same active binding as resume: %v\n%s", err, output)
	}
	for _, file := range []string{parent, active} {
		if data, err := os.ReadFile(file); err != nil || !strings.Contains(string(data), "kept history") {
			t.Errorf("history changed or removed: %s: %v", file, err)
		}
	}
}

func TestOmpLifecycleRejectsUnresolvedIdentityWithDurableReason(t *testing.T) {
	for _, action := range []string{"start", "restart", "start-with-message"} {
		t.Run(action, func(t *testing.T) {
			withTempHome(t)
			inst := NewInstanceWithTool("omp-unresolved-"+action, t.TempDir(), "omp")
			dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"first", "second"} {
				if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", name)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			defer func() { _ = inst.Kill() }()
			var err error
			if action == "start" {
				err = inst.Start()
			} else if action == "start-with-message" {
				err = inst.StartWithMessage("Do not lose this request")
			} else {
				err = inst.Restart()
			}
			if err == nil || !strings.Contains(err.Error(), "first.jsonl") || !strings.Contains(err.Error(), "second.jsonl") {
				t.Errorf("%s must return actionable identity error before launching: %v", action, err)
			}
			rec := inst.SpawnFailure()
			if rec == nil || !strings.Contains(rec.DyingOutput, "first.jsonl") || !strings.Contains(rec.DyingOutput, "second.jsonl") {
				t.Errorf("%s must preserve actual candidate names for TUI/hub preview: %+v", action, rec)
			}
		})
	}
}

func TestOmpImmediateProviderFailureIsReturnedByLifecycle(t *testing.T) {
	for _, action := range []string{"start", "restart", "start-with-message"} {
		t.Run(action, func(t *testing.T) {
			withTempHome(t)
			probe := filepath.Join(t.TempDir(), "broken-omp")
			if err := os.WriteFile(probe, []byte("#!/bin/sh\nprintf '\\033[31momp-failed-before-input\\033[0m\\n' >&2\nexit 23\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			inst := NewInstanceWithTool("omp-immediate-"+action, t.TempDir(), "omp")
			inst.Command = probe
			t.Cleanup(func() { _ = inst.Kill() })
			var err error
			switch action {
			case "start":
				err = inst.Start()
			case "restart":
				err = inst.Restart()
			default:
				err = inst.StartWithMessage("must not be submitted to another session")
			}
			if err == nil || !strings.Contains(err.Error(), "omp-failed-before-input") {
				t.Fatalf("%s hid provider death: %v", action, err)
			}
			if strings.ContainsRune(err.Error(), '\x1b') {
				t.Fatalf("%s returned terminal control sequences to the TUI error footer", action)
			}
			rec := inst.SpawnFailure()
			if rec == nil || !strings.Contains(rec.DyingOutput, "omp-failed-before-input") {
				t.Fatalf("no durable error: %+v", rec)
			}
		})
	}
}

func TestOmpFreshRestartPreservesPreviousHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "fresh-history", Tool: "omp", ompFreshStart: true}
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(filepath.Join(dir, "previous"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"previous.jsonl", "previous/tool-output.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("irreplaceable history\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	probe := filepath.Join(home, "fresh-probe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nfor arg; do case \"$arg\" in --resume|--fork|--continue) exit 22;; esac; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-c", inst.buildOmpCommand(probe)).CombinedOutput(); err != nil {
		t.Fatalf("fresh launch failed: %v\n%s", err, out)
	}
	for _, name := range []string{"previous.jsonl", "previous/tool-output.log"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != "irreplaceable history\n" {
			t.Errorf("fresh restart destroyed prior history %s: %v", name, err)
		}
	}
}

func TestOmpUnacknowledgedFreshBoundaryCannotResumeOldHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "fresh-failed", Tool: "omp"}
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(oldFile, []byte("{\"type\":\"session\",\"id\":\"old\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ompActiveBindingName), []byte("1\n"+oldFile+"\nold\nsaved\nprevious-launch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exercise the real fresh launcher, not an invented mixture of legacy
	// common markers and generation-specific records. The provider dies before
	// its extension can acknowledge a replacement conversation.
	inst.ompFreshStart = true
	if out, err := exec.Command("bash", "-c", inst.buildOmpCommand("false")).CombinedOutput(); err == nil {
		t.Fatalf("failure probe unexpectedly succeeded: %s", out)
	}
	if err := inst.prepareOmpIdentity(); err == nil {
		t.Error("local preflight resurrects old conversation after unacknowledged fresh start")
	}
	if out, err := exec.Command("bash", "-c", inst.buildOmpCommand("true")).CombinedOutput(); err == nil {
		t.Errorf("execution-host resolver resurrects old conversation after failed fresh start: %s", out)
	}
}

func TestOmpLaunchInstallsIdentityTrackerOnExecutionHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "tracked-launch", Title: "Approval's exact title", Tool: "omp", ompFreshStart: true}
	probe := filepath.Join(home, "tracker-probe")
	script := `#!/bin/bash
set -eu
test "$AGENTDECK_INSTANCE_ID" = tracked-launch
test "$AGENTDECK_OMP_DIR" = "$HOME/.omp/agent-deck/tracked-launch"
test -n "$AGENTDECK_OMP_LAUNCH_ID"
test "$(cat "$AGENTDECK_OMP_DIR/.agent-deck-launch-generation")" = "$AGENTDECK_OMP_LAUNCH_ID"
test "$(cat "$AGENTDECK_OMP_DIR/.agent-deck-fresh-pending.$AGENTDECK_OMP_LAUNCH_ID")" = "$AGENTDECK_OMP_LAUNCH_ID"
found=0
while [ "$#" -gt 0 ]; do
 if [ "$1" = --extension ]; then shift; test -s "$1"; found=1; fi
 shift
done
test "$found" = 1
`
	if err := os.WriteFile(probe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-c", inst.buildOmpCommand(probe)).CombinedOutput(); err != nil {
		t.Fatalf("launch did not install/authorize execution-host identity tracker: %v\n%s", err, out)
	}
	title, err := os.ReadFile(filepath.Join(home, ".omp", "agent-deck", inst.ID, ".agent-deck-title.json"))
	if err != nil || !strings.Contains(string(title), "Approval's exact title") {
		t.Fatalf("authoritative title missing: %s (%v)", title, err)
	}
}

func TestOmpLaunchAndRenameNeverWriteThroughAnotherRowSymlink(t *testing.T) {
	for _, action := range []string{"launch", "rename"} {
		t.Run(action, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			inst := &Instance{ID: "owned", Tool: "omp", Title: "owned title"}
			root := filepath.Join(home, ".omp", "agent-deck")
			other := filepath.Join(root, "other")
			if err := os.MkdirAll(other, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(root, inst.ID)); err != nil {
				t.Fatal(err)
			}
			var err error
			if action == "launch" {
				_, err = exec.Command("bash", "-c", inst.buildOmpCommand("true")).CombinedOutput()
			} else {
				err = inst.syncOmpTitle("Wrong owner title")
			}
			if err == nil {
				t.Errorf("%s accepted another row's symlink", action)
			}
			entries, err := os.ReadDir(other)
			if err != nil || len(entries) != 0 {
				t.Fatalf("%s wrote into another row before validating ownership: %v %v", action, entries, err)
			}
		})
	}
}

func TestOmpExplicitRenameQueuesOnlyThisEntryTitle(t *testing.T) {
	withTempHome(t)
	parent := &Instance{ID: "parent", Tool: "omp", Title: "Docs"}
	child := &Instance{ID: "child", Tool: "omp", Title: "Docs (fork)"}
	for _, inst := range []*Instance{parent, child} {
		_, sync, err := SetField(inst, FieldTitle, inst.Title, nil)
		if err != nil || sync == nil {
			t.Fatalf("OMP title has no downstream sync: %v", err)
		}
		if err := sync(); err != nil {
			t.Fatal(err)
		}
	}
	_, sync, err := SetField(child, FieldTitle, "Docs new work", nil)
	if err != nil || sync == nil {
		t.Fatalf("rename missing sync: %v", err)
	}
	if err := sync(); err != nil {
		t.Fatal(err)
	}
	for _, inst := range []*Instance{parent, child} {
		data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID, ".agent-deck-title.json"))
		if err != nil || !strings.Contains(string(data), inst.Title) {
			t.Fatalf("wrong title intent for %s: %s (%v)", inst.ID, data, err)
		}
	}
}
