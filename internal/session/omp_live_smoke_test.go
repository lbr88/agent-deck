package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
)

// Opt-in real harness test: isolated TestMain home/socket, no user auth or
// configuration, no model prompts. The fake API key is never used for a turn.
func TestOmpLiveLifecycleSmoke(t *testing.T) {
	bin := os.Getenv("AGENTDECK_OMP_SMOKE_BIN")
	if bin == "" {
		t.Skip("set AGENTDECK_OMP_SMOKE_BIN to run real OMP lifecycle smoke")
	}
	home := os.Getenv("HOME") // TestMain's isolated home, also owned by its tmux server.
	inst := NewInstanceWithTool("omp-live-identity", t.TempDir(), "omp")
	config := filepath.Join(t.TempDir(), "smoke.yml")
	if err := os.WriteFile(config, []byte("setupVersion: 2\nstartup:\n  setupWizard: false\n  checkUpdate: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "isolated-omp")
	script := "#!/bin/bash\nexec env -i " +
		"HOME=" + shellescape.Quote(home) + " PATH=" + shellescape.Quote(os.Getenv("PATH")) +
		" TMPDIR=" + shellescape.Quote(os.TempDir()) + " TERM=xterm-256color " +
		`AGENTDECK_INSTANCE_ID="$AGENTDECK_INSTANCE_ID" AGENTDECK_PROFILE="$AGENTDECK_PROFILE" AGENTDECK_OMP_DIR="$AGENTDECK_OMP_DIR" AGENTDECK_OMP_LAUNCH_ID="$AGENTDECK_OMP_LAUNCH_ID" ` +
		shellescape.Quote(bin) + " --config " + shellescape.Quote(config) + ` "$@"` + "\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	inst.Command = shellescape.Quote(wrapper) + " --no-extensions --no-skills --no-rules --no-tools --no-lsp --no-pty --no-title --provider openai --model gpt-4.1-mini --api-key unused-isolated-smoke-key"
	t.Cleanup(func() { _ = inst.KillAndWait() })
	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	waitBinding := func(target *Instance, previous string) *ompActiveBinding {
		t.Helper()
		targetDir := filepath.Join(home, ".omp", "agent-deck", target.ID)
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			binding, err := readOmpActiveBinding(targetDir)
			if err == nil && binding != nil && binding.SessionID != previous {
				return binding
			}
			if target.GetTmuxSession().IsPaneDead() {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		out, _ := target.PreviewFull()
		t.Fatalf("real OMP failed to publish changed identity (previous=%s): %s", previous, out)
		return nil
	}
	initial := waitBinding(inst, "")
	if initial.State != "pending" {
		t.Fatalf("unused conversation should be lazy, got %+v", initial)
	}
	if err := inst.GetTmuxSession().SendKeysAndEnter("/new"); err != nil {
		t.Fatal(err)
	}
	current := waitBinding(inst, initial.SessionID)
	_, sync, err := SetField(inst, FieldTitle, "Smoke explicit rename", nil)
	if err != nil || sync == nil {
		t.Fatalf("no title sync: %v", err)
	}
	if err := sync(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	renamed := false
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(current.File)
		if strings.Contains(string(data), "Smoke explicit rename") {
			renamed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !renamed {
		out, _ := inst.PreviewFull()
		t.Fatalf("real OMP did not persist explicit rename: %s", out)
	}
	for !inst.CanForkOmp() && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	child, _, err := inst.CreateForkedOmpInstanceWithOptions("Smoke independent fork", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.KillAndWait() })
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	childBinding := waitBinding(child, "")
	if childBinding.SessionID == current.SessionID || childBinding.File == current.File {
		t.Fatalf("native fork reused parent conversation: parent=%+v child=%+v", current, childBinding)
	}
	_, syncChild, err := SetField(child, FieldTitle, "Smoke child renamed alone", nil)
	if err != nil || syncChild == nil {
		t.Fatalf("no child title sync: %v", err)
	}
	if err := syncChild(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(childBinding.File)
		if strings.Contains(string(data), "Smoke child renamed alone") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child rename did not reach actual OMP transcript")
		}
		time.Sleep(100 * time.Millisecond)
	}
	parentData, err := os.ReadFile(current.File)
	if err != nil || strings.Contains(string(parentData), "Smoke child renamed alone") {
		t.Fatalf("child rename changed parent transcript: %v", err)
	}
	parentBinding, err := readOmpActiveBinding(dir)
	if err != nil || parentBinding.SessionID != current.SessionID {
		t.Fatalf("fork changed parent's active identity: %+v %v", parentBinding, err)
	}
	for _, target := range []*Instance{inst, child} {
		want := current
		if target == child {
			want = childBinding
		}
		if err := target.KillAndWait(); err != nil {
			t.Fatal(err)
		}
		if err := target.Restart(); err != nil {
			t.Fatal(err)
		}
		resumed := waitBinding(target, "")
		if resumed.SessionID != want.SessionID || resumed.File != want.File {
			t.Fatalf("restart opened wrong conversation: wanted %+v, got %+v", want, resumed)
		}
	}
}
