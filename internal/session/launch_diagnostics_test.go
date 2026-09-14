package session

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func TestBuildTmuxOptionOverridesRetainsAgentExitDiagnostics(t *testing.T) {
	for _, tool := range []string{"codex", "omp", "claude", "opencode", "kiro"} {
		t.Run(tool, func(t *testing.T) {
			inst := &Instance{Tool: tool}
			if got := inst.buildTmuxOptionOverrides()["remain-on-exit"]; got != "on" {
				t.Fatalf("remain-on-exit = %q, want on for %s", got, tool)
			}
		})
	}

	if got := (&Instance{Tool: "shell"}).buildTmuxOptionOverrides()["remain-on-exit"]; got != "" {
		t.Fatalf("shell remain-on-exit = %q, want unset", got)
	}
}

func TestFormatTerminatedPanePreviewIncludesExitStatusAndOutput(t *testing.T) {
	got := formatTerminatedPanePreview("provider failed: invalid credentials\n", 17, 0, true)
	if !strings.Contains(got, "exit status 17") {
		t.Fatalf("preview hid exit status: %q", got)
	}
	if !strings.Contains(got, "provider failed: invalid credentials") {
		t.Fatalf("preview hid terminal output: %q", got)
	}
}

func TestFormatTerminatedPanePreviewNamesSignal(t *testing.T) {
	got := formatTerminatedPanePreview("last frame", 143, 15, true)
	if !strings.Contains(got, "signal 15") || !strings.Contains(got, "exit status 143") {
		t.Fatalf("preview hid signal termination: %q", got)
	}
}

func TestFormatTerminatedPanePreviewDoesNotInferSignalFromExitCode(t *testing.T) {
	got := formatTerminatedPanePreview("last frame", 143, 0, true)
	if strings.Contains(got, "signal") || !strings.Contains(got, "exit status 143") {
		t.Fatalf("ordinary exit 143 was misidentified as a signal: %q", got)
	}
}

func TestUpdateStatusDeadPaneOverridesFreshWaitingHook(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}

	pane := tmux.NewSession("dead-pane-overrides-hook", t.TempDir())
	pane.RunCommandAsInitialProcess = true
	pane.OptionOverrides = map[string]string{"remain-on-exit": "on"}
	t.Cleanup(func() { _ = pane.Kill() })
	if err := pane.Start("sleep 30"); err != nil {
		t.Fatal(err)
	}

	rawPID, err := exec.Command("tmux", "display-message", "-p", "-t", pane.Name+":0.0", "#{pane_pid}").Output()
	if err != nil {
		t.Fatalf("read pane pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil {
		t.Fatalf("parse pane pid %q: %v", rawPID, err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find pane process: %v", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM pane process: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !pane.IsPaneDead() {
		time.Sleep(10 * time.Millisecond)
	}
	if !pane.IsPaneDead() {
		t.Fatal("pane did not become retained/dead after SIGTERM")
	}

	inst := &Instance{
		ID:             "dead-pane-overrides-hook",
		Tool:           "codex",
		Status:         StatusWaiting,
		CreatedAt:      time.Now().Add(-time.Minute),
		tmuxSession:    pane,
		hookStatus:     "waiting",
		hookLastUpdate: time.Now(),
	}
	if err := inst.UpdateStatus(); err != nil {
		t.Fatal(err)
	}
	if got := inst.GetStatusThreadSafe(); got != StatusError {
		t.Fatalf("status = %q, want %q: a dead pane must outrank a fresh waiting hook", got, StatusError)
	}
}

func TestStartReplacesRetainedDeadPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}

	pane := tmux.NewSession("start-replaces-dead-pane", t.TempDir())
	inst := &Instance{
		ID:          "start-replaces-dead-pane",
		Title:       "start replaces dead pane",
		Tool:        "test-sleeper",
		Command:     "sleep 30",
		ProjectPath: t.TempDir(),
		CreatedAt:   time.Now().Add(-time.Minute),
		tmuxSession: pane,
	}
	t.Cleanup(func() { _ = pane.Kill() })
	if err := inst.Start(); err != nil {
		t.Fatalf("initial Start: %v", err)
	}

	rawPID, err := exec.Command("tmux", "display-message", "-p", "-t", pane.Name+":0.0", "#{pane_pid}").Output()
	if err != nil {
		t.Fatalf("read pane pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil {
		t.Fatalf("parse pane pid %q: %v", rawPID, err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find pane process: %v", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM pane process: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !pane.IsPaneDead() {
		time.Sleep(10 * time.Millisecond)
	}
	if !pane.IsPaneDead() {
		t.Fatal("pane did not become retained/dead after SIGTERM")
	}
	if inst.HasLivePane() {
		t.Fatal("retained dead pane must not be reported as a live agent process")
	}

	if err := inst.Start(); err != nil {
		t.Fatalf("Start should replace retained dead pane: %v", err)
	}
	if !pane.Exists() || pane.IsPaneDead() {
		t.Fatal("Start did not leave a live replacement pane")
	}
	if !inst.HasLivePane() {
		t.Fatal("replacement pane must be reported as live")
	}
}
