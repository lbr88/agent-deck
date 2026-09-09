package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
)

func TestOmpProactiveHealthDetectsAmbiguityBeforeRestart(t *testing.T) {
	withTempHome(t)
	inst := NewInstanceWithTool("health-before-enter", t.TempDir(), "omp")
	dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"original", "active"} {
		if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", name)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := inst.UpdateStatus(); err != nil {
		t.Fatal(err)
	}
	preview, err := inst.PreviewFull()
	if err != nil || !strings.Contains(preview, "active.jsonl") || !strings.Contains(preview, "original.jsonl") {
		t.Fatalf("status polling did not expose ambiguous histories before restart: %v %q", err, preview)
	}
}

func TestOmpTargetHealthUsesSSHAndSandboxFilesystemBeforeRestart(t *testing.T) {
	for _, route := range []string{"ssh", "docker"} {
		t.Run(route, func(t *testing.T) {
			withTempHome(t)
			targetHome := t.TempDir()
			inst := NewInstanceWithTool("target-health", t.TempDir(), "omp")
			if route == "ssh" {
				inst.SSHHost = "test-target"
			} else {
				inst.Sandbox = &SandboxConfig{Enabled: true}
				inst.SandboxContainer = "agent-deck-test-health"
			}
			dir := filepath.Join(targetHome, ".omp", "agent-deck", inst.ID)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"target-original", "target-branch"} {
				if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", name)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			binDir := t.TempDir()
			// Exercise the generated target script; replace only the transport,
			// so no SSH server, Docker daemon or user credentials are accessed.
			wrapper := "#!/bin/bash\nexec env HOME=" + shellescape.Quote(targetHome) + " bash -c \"${!#}\"\n"
			if err := os.WriteFile(filepath.Join(binDir, route), []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			preview, err := inst.PreviewFull()
			if err != nil || !strings.Contains(preview, "target-original.jsonl") || !strings.Contains(preview, "target-branch.jsonl") {
				t.Fatalf("%s target identity problem hidden before restart: %q %v", route, preview, err)
			}
		})
	}
}

func TestOmpProactiveHealthDetectsLiveTrackingFailure(t *testing.T) {
	withTempHome(t)
	inst := NewInstanceWithTool("health-runtime-error", t.TempDir(), "omp")
	dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("live-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := fmt.Sprintf(`{"instance_id":%q,"launch_id":"live-generation","identity_ready":false,"error":"refused other-entry conversation"}`, inst.ID)
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-omp-status.live-generation.json"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inst.UpdateStatus(); err != nil {
		t.Fatal(err)
	}
	preview, err := inst.PreviewFull()
	if err != nil || !strings.Contains(preview, "refused other-entry conversation") {
		t.Fatalf("runtime tracking error hidden: %v %q", err, preview)
	}
}

func TestOmpProactiveHealthUsesSameIdentityValidationAsLaunch(t *testing.T) {
	for _, fault := range []string{"wrong-id", "wrong-path", "control-bearing-error"} {
		t.Run(fault, func(t *testing.T) {
			withTempHome(t)
			inst := NewInstanceWithTool("live-health-consistency", t.TempDir(), "omp")
			dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
			file := filepath.Join(dir, "active.jsonl")
			const generation = "live-health-generation"
			writeOmpValidationTranscript(t, file, "active-id")
			writeOmpValidationBindingAt(t, dir, ompActiveBindingName+"."+generation, file, "active-id", "saved", generation)
			if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte(generation+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			status := ompIdentityStatus{InstanceID: inst.ID, LaunchID: generation, SessionID: "active-id", SessionFile: file, IdentityReady: true}
			switch fault {
			case "wrong-id":
				status.SessionID = "unrelated-id"
			case "wrong-path":
				status.SessionFile = filepath.Join(dir, "unrelated.jsonl")
			case "control-bearing-error":
				status.IdentityReady = false
				status.Error = "\x1b[2Jtracking failed"
			}
			data, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".agent-deck-omp-status."+generation+".json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			preview := inst.ompHealthPreview()
			if preview == "" || strings.Contains(preview, "\x1b") {
				t.Fatalf("proactive health missed/surfaced unsafe identity status: %q", preview)
			}
		})
	}
}

func TestOmpIntentionalNonTUIHealthDoesNotRequireInteractiveAck(t *testing.T) {
	withTempHome(t)
	inst := NewInstanceWithTool("print-health", t.TempDir(), "omp")
	inst.Command = "omp --print"
	dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("print-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if warning := inst.ompHealthPreview(); warning != "" {
		t.Fatalf("intentional non-TUI launch got a false interactive-ACK warning: %s", warning)
	}
}

func TestOmpHealthWarningDoesNotSurviveToolChange(t *testing.T) {
	withTempHome(t)
	inst := NewInstanceWithTool("health-tool-change", t.TempDir(), "omp")
	dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", name)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if warning := inst.ompHealthPreview(); warning == "" {
		t.Fatal("fixture did not establish an OMP identity warning")
	}
	if _, _, err := SetField(inst, FieldTool, "shell", nil); err != nil {
		t.Fatal(err)
	}
	if warning := inst.ompHealthPreview(); warning != "" {
		t.Fatalf("stale OMP warning leaked into shell preview: %q", warning)
	}
}

func TestOmpTargetHealthDiscardsCompletionAfterOwnerChange(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*Instance)
		restore func(*Instance)
	}{
		{
			name:    "route",
			change:  func(inst *Instance) { inst.SSHHost = "new-target" },
			restore: func(*Instance) {},
		},
		{
			name:    "tool",
			change:  func(inst *Instance) { inst.Tool = "shell" },
			restore: func(inst *Instance) { inst.Tool = "omp" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTempHome(t)
			inst := NewInstanceWithTool("health-owner-change", t.TempDir(), "omp")
			inst.SSHHost = "old-target"
			started := filepath.Join(t.TempDir(), "old-started")
			release := filepath.Join(t.TempDir(), "release-old")
			newStarted := filepath.Join(t.TempDir(), "new-started")
			binDir := t.TempDir()
			wrapper := "#!/bin/bash\n" +
				"if [ ! -f " + shellescape.Quote(started) + " ]; then touch " + shellescape.Quote(started) + "; while [ ! -f " + shellescape.Quote(release) + " ]; do sleep 0.01; done; echo 'stale target warning' >&2; exit 1; fi\n" +
				"touch " + shellescape.Quote(newStarted) + "\n" +
				"exec env HOME=" + shellescape.Quote(t.TempDir()) + " bash -c \"${!#}\"\n"
			if err := os.WriteFile(filepath.Join(binDir, "ssh"), []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			inst.mu.Lock()
			inst.refreshOmpMetadataLocked()
			pending := inst.ompMetadataPending
			inst.mu.Unlock()
			if pending == nil {
				t.Fatal("target health check did not start")
			}
			waitForHealthTestFile(t, started)
			inst.mu.Lock()
			tc.change(inst)
			inst.mu.Unlock()
			if err := os.WriteFile(release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case <-pending:
			case <-time.After(4 * time.Second):
				t.Fatal("discarded target health check did not signal completion")
			}
			inst.mu.Lock()
			tc.restore(inst)
			inst.mu.Unlock()

			if warning := inst.ompHealthPreview(); warning != "" {
				t.Fatalf("old target health completion leaked after owner change: %q", warning)
			}
			waitForHealthTestFile(t, newStarted)
		})
	}
}

func TestOmpUntrackedHealthStripsControlsFromCandidatePaths(t *testing.T) {
	withTempHome(t)
	inst := NewInstanceWithTool("health-control-path", t.TempDir(), "omp")
	dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"normal.jsonl", "unsafe-\x1b[2J-root.jsonl"} {
		data := fmt.Sprintf("{\"type\":\"session\",\"id\":\"root-%d\"}\n", index)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	warning := inst.ompHealthPreview()
	if warning == "" || !strings.Contains(warning, "Preserved candidates") {
		t.Fatalf("fixture did not expose ambiguous candidates: %q", warning)
	}
	if strings.Contains(warning, "\x1b") {
		t.Fatalf("untracked candidate path injected terminal controls: %q", warning)
	}
}

func waitForHealthTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
