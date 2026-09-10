package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/docker"
)

func waitForOmpNonTUILogLines(t *testing.T, path string, count int) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(strings.Split(strings.TrimSpace(string(data)), "\n")) >= count {
			return string(data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	t.Fatalf("timed out waiting for %d provider invocations: %q, %v", count, data, err)
	return ""
}

func ompNonTUIPrimaryPanePID(t *testing.T, inst *Instance) string {
	t.Helper()
	sess := inst.GetTmuxSession()
	args := []string{}
	if sess.SocketName != "" {
		args = append(args, "-L", sess.SocketName)
	}
	args = append(args, "display-message", "-p", "-t", sess.Name+":0.0", "#{pane_pid}")
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("read primary pane PID: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeOmpNonTUILifecycleProbe(t *testing.T, root, id, logFile string) string {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "omp-lifecycle-probe")
	body := fmt.Sprintf(`#!/bin/bash
set -eu
root_file=%s
session_id=%s
log_file=%s
mode=fresh
source_file=
session_dir=
extension=0
print_mode=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --resume) mode=resume; shift; source_file=${1-} ;;
    --session-dir) shift; session_dir=${1-} ;;
    --extension) extension=1; shift ;;
    --print) print_mode=1 ;;
  esac
  shift
done
if [ "$mode" = fresh ]; then
  mkdir -p "$session_dir"
  printf '{"type":"session","id":"%%s"}\n' "$session_id" > "$root_file"
else
  test "$source_file" = "$root_file"
fi
printf '%%s|%%s|%%s\n' "$mode" "$source_file" "$extension" >> "$log_file"
if [ "$extension" -eq 1 ]; then
  printf '1\n%%s\n%%s\nsaved\n%%s\n' "$root_file" "$session_id" "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
  printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"%%s","session_file":"%%s","identity_ready":true,"error":""}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" "$session_id" "$root_file" > "$AGENTDECK_OMP_DIR/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
fi
sleep 20
`, shellQuote(root), shellQuote(id), shellQuote(logFile))
	if err := os.WriteFile(probe, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return probe
}

func TestOmpNonTUIRepeatedLaunchPreservesTrackedIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "nontui-preserves-tracked", Tool: "omp"}
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	root := filepath.Join(dir, "tracked-root.jsonl")
	writeOmpValidationTranscript(t, root, "tracked-root-id")
	const generation = "tracked-generation"
	writeOmpValidationBindingAt(t, dir, ompActiveBindingName+"."+generation, root, "tracked-root-id", "saved", generation)
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte(generation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generationBefore, err := os.ReadFile(filepath.Join(dir, ".agent-deck-launch-generation"))
	if err != nil {
		t.Fatal(err)
	}
	bindingPath := filepath.Join(dir, ompActiveBindingName+"."+generation)
	bindingBefore, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(t.TempDir(), "invocations")
	probe := filepath.Join(t.TempDir(), "omp-print-probe")
	probeBody := fmt.Sprintf(`#!/bin/bash
set -eu
mode=fresh
source_file=
extension=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --resume) mode=resume; shift; source_file=${1-} ;;
    --extension) extension=1; shift ;;
  esac
  shift
done
printf '%%s|%%s|%%s|%%s\n' "$mode" "$source_file" "$extension" "${AGENTDECK_OMP_LAUNCH_ID-}" >> %s
`, shellQuote(logFile))
	if err := os.WriteFile(probe, []byte(probeBody), 0o700); err != nil {
		t.Fatal(err)
	}
	inst.Command = probe + " --print"

	for attempt := 0; attempt < 2; attempt++ {
		output, runErr := exec.Command("bash", "-c", inst.buildOmpCommand(inst.Command)).CombinedOutput()
		if runErr != nil {
			t.Fatalf("non-TUI launch %d failed: %v\n%s", attempt+1, runErr, output)
		}
	}

	if got, err := os.ReadFile(filepath.Join(dir, ".agent-deck-launch-generation")); err != nil || string(got) != string(generationBefore) {
		t.Fatalf("non-TUI launch changed tracked generation: %q, %v", got, err)
	}
	if got, err := os.ReadFile(bindingPath); err != nil || string(got) != string(bindingBefore) {
		t.Fatalf("non-TUI launch changed tracked binding: %q, %v", got, err)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	wantLine := "resume|" + root + "|0|\n"
	if string(logBytes) != strings.Repeat(wantLine, 2) {
		t.Fatalf("non-TUI launch installed tracker or resumed another root: %q, want %q", logBytes, strings.Repeat(wantLine, 2))
	}
}

func TestOmpNonTUIRealLifecyclePreservesTrackedIdentityAndSwitchesToTUI(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	inst := newOmpLaunchAckInstance(t, "nontui-real-tracked")
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root := filepath.Join(dir, "tracked-root.jsonl")
	writeOmpValidationTranscript(t, root, "tracked-real-id")
	const generation = "tracked-real-generation"
	writeOmpValidationBindingAt(t, dir, ompActiveBindingName+"."+generation, root, "tracked-real-id", "saved", generation)
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte(generation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bindingPath := filepath.Join(dir, ompActiveBindingName+"."+generation)
	bindingBefore, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(t.TempDir(), "invocations")
	probe := writeOmpNonTUILifecycleProbe(t, root, "tracked-real-id", logFile)
	inst.Command = probe + " --print"

	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	waitForOmpNonTUILogLines(t, logFile, 1)
	if err := inst.KillAndWait(); err != nil {
		t.Fatal(err)
	}
	if err := inst.Restart(); err != nil {
		t.Fatal(err)
	}
	waitForOmpNonTUILogLines(t, logFile, 2)
	if got, err := os.ReadFile(filepath.Join(dir, ".agent-deck-launch-generation")); err != nil || string(got) != generation+"\n" {
		t.Fatalf("real non-TUI lifecycle changed generation: %q, %v", got, err)
	}
	if got, err := os.ReadFile(bindingPath); err != nil || string(got) != string(bindingBefore) {
		t.Fatalf("real non-TUI lifecycle changed binding: %q, %v", got, err)
	}
	nameBefore := inst.GetTmuxSession().Name
	if err := inst.SetOmpOptions(&OmpOptions{FromClaude: true}); err != nil {
		t.Fatal(err)
	}
	if err := inst.Restart(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive") {
		t.Fatalf("non-TUI import did not fail before killing the pane: %v", err)
	}
	if !inst.Exists() || inst.GetTmuxSession().Name != nameBefore || inst.GetOmpOptions() == nil || !inst.GetOmpOptions().FromClaude {
		t.Fatal("refused non-TUI import killed/replaced the process or consumed import intent")
	}
	if err := inst.SetOmpOptions(&OmpOptions{}); err != nil {
		t.Fatal(err)
	}
	pidBefore := ompNonTUIPrimaryPanePID(t, inst)
	if err := inst.RestartFresh(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive") {
		t.Fatalf("non-TUI RestartFresh did not fail before killing the pane: %v", err)
	}
	alive, aliveErr := inst.GetTmuxSession().PrimaryPaneAliveFresh()
	pidAfter := ompNonTUIPrimaryPanePID(t, inst)
	if aliveErr != nil || !alive || inst.GetTmuxSession().Name != nameBefore || pidAfter != pidBefore || inst.ompFreshStart {
		t.Fatal("refused non-TUI RestartFresh killed/replaced the process or consumed fresh intent")
	}

	inst.Command = probe
	if err := inst.Restart(); err != nil {
		t.Fatalf("switch back to interactive OMP failed: %v", err)
	}
	waitForOmpNonTUILogLines(t, logFile, 3)
	newGeneration, err := os.ReadFile(filepath.Join(dir, ".agent-deck-launch-generation"))
	if err != nil || strings.TrimSpace(string(newGeneration)) == generation {
		t.Fatalf("interactive launch did not publish a fresh generation: %q, %v", newGeneration, err)
	}
	if got, err := os.ReadFile(bindingPath); err != nil || string(got) != string(bindingBefore) {
		t.Fatalf("interactive switch destroyed prior binding evidence: %q, %v", got, err)
	}
}

func TestOmpNonTUIPendingRecipeRequiresExplicitInteractiveFreshRecovery(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	inst := newOmpLaunchAckInstance(t, "nontui-pending-recovery")
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	oldRoot := filepath.Join(dir, "unacknowledged-child.jsonl")
	oldBytes := []byte("{\"type\":\"session\",\"id\":\"unacknowledged-child\"}\nold child history\n")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldRoot, oldBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	replayed := filepath.Join(t.TempDir(), "old-recipe-ran")
	oldProbe := filepath.Join(t.TempDir(), "old-print-fork-probe")
	if err := os.WriteFile(oldProbe, []byte("#!/bin/bash\ntouch "+shellQuote(replayed)+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	builder := &Instance{ID: inst.ID, Tool: "omp", Command: oldProbe + " --print"}
	recipe := builder.buildOmpCommand(builder.Command)
	inst.ForkStartCommand = recipe
	inst.IsForkAwaitingStart = true
	newRoot := filepath.Join(dir, "interactive-recovery.jsonl")
	logFile := filepath.Join(t.TempDir(), "interactive-recovery-log")
	inst.Command = writeOmpNonTUILifecycleProbe(t, newRoot, "interactive-recovery-id", logFile)

	if err := inst.Start(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "restart fresh") {
		t.Fatalf("ordinary start did not preserve and refuse old non-TUI recipe: %v", err)
	}
	if inst.Exists() || !inst.IsForkAwaitingStart || inst.ForkStartCommand != recipe {
		t.Fatal("ordinary refusal launched or consumed old non-TUI fork recipe")
	}
	if _, err := os.Stat(replayed); !os.IsNotExist(err) {
		t.Fatalf("ordinary start replayed old non-TUI fork recipe: %v", err)
	}
	if err := inst.RestartFresh(); err != nil {
		t.Fatalf("explicit interactive fresh recovery failed: %v", err)
	}
	if inst.IsForkAwaitingStart || inst.ForkStartCommand != "" {
		t.Fatal("real interactive ACK did not clear recovered pending intent")
	}
	if got, err := os.ReadFile(oldRoot); err != nil || string(got) != string(oldBytes) {
		t.Fatalf("interactive fresh recovery changed old child history: %q, %v", got, err)
	}
}

func TestOmpNonTUIStartWithMessageRefusesBeforeLaunch(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "nontui-initial-message")
	providerRan := filepath.Join(t.TempDir(), "provider-ran")
	probe := filepath.Join(t.TempDir(), "omp-print-probe")
	if err := os.WriteFile(probe, []byte("#!/bin/bash\ntouch "+shellQuote(providerRan)+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	inst.Command = probe + " --print"
	if err := inst.StartWithMessage("must not be silently dropped"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "terminal prompt") {
		t.Fatalf("non-TUI initial message was not refused clearly: %v", err)
	}
	if failure := inst.SpawnFailure(); failure == nil || !strings.Contains(strings.ToLower(failure.DyingOutput), "terminal prompt") {
		t.Fatalf("non-TUI initial-message refusal was not recorded durably: %+v", failure)
	}
	if inst.Exists() {
		t.Fatal("non-TUI initial-message refusal left a tmux session running")
	}
	if _, err := os.Stat(providerRan); !os.IsNotExist(err) {
		t.Fatalf("non-TUI provider ran before initial-message refusal: %v", err)
	}
}

func TestOmpNonTUIRealLifecycleLeavesUniqueRootUntrackedUntilTUI(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	inst := newOmpLaunchAckInstance(t, "nontui-real-untracked")
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root := filepath.Join(dir, "unique-root.jsonl")
	logFile := filepath.Join(t.TempDir(), "invocations")
	probe := writeOmpNonTUILifecycleProbe(t, root, "unique-real-id", logFile)
	inst.Command = probe + " --print"

	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	waitForOmpNonTUILogLines(t, logFile, 1)
	if err := inst.KillAndWait(); err != nil {
		t.Fatal(err)
	}
	if err := inst.Restart(); err != nil {
		t.Fatal(err)
	}
	waitForOmpNonTUILogLines(t, logFile, 2)
	for _, name := range []string{".agent-deck-launch-generation", ompActiveBindingName} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("non-TUI lifecycle wrote identity state %s: %v", name, err)
		}
	}
	if err := inst.KillAndWait(); err != nil {
		t.Fatal(err)
	}
	inst.Command = probe
	if err := inst.Restart(); err != nil {
		t.Fatalf("interactive bootstrap of unique root failed: %v", err)
	}
	waitForOmpNonTUILogLines(t, logFile, 3)
	binding, err := resolveOmpActiveBinding(dir)
	if err != nil || binding == nil || binding.File != root || binding.SessionID != "unique-real-id" || binding.Generation == "legacy" {
		t.Fatalf("interactive launch did not bootstrap exact unique root: %+v, %v", binding, err)
	}
}

func TestOmpNonTUIIdentityChangingOperationsRequireInteractiveCommand(t *testing.T) {
	for _, imported := range []string{"claude", "codex"} {
		for _, route := range []string{"local", "ssh", "docker"} {
			t.Run("import-"+imported+"-"+route, func(t *testing.T) {
				inst := &Instance{ID: "nontui-import-" + imported + "-" + route, Tool: "omp", Command: "omp --print"}
				if route == "ssh" {
					inst.SSHHost = "must-not-connect"
				} else if route == "docker" {
					inst.Sandbox = &SandboxConfig{Enabled: true, Image: "must-not-start"}
				}
				opts := &OmpOptions{}
				if imported == "claude" {
					opts.FromClaude = true
				} else {
					opts.FromCodex = true
				}
				if err := inst.SetOmpOptions(opts); err != nil {
					t.Fatal(err)
				}
				err := inst.prepareOmpIdentity()
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive") {
					t.Fatalf("non-TUI import was not refused clearly: %v", err)
				}
				if failure := inst.SpawnFailure(); failure == nil || !strings.Contains(strings.ToLower(failure.DyingOutput), "interactive") {
					t.Fatalf("non-TUI import refusal was not recorded durably: %+v", failure)
				}
				preserved := inst.GetOmpOptions()
				if preserved == nil || preserved.FromClaude != opts.FromClaude || preserved.FromCodex != opts.FromCodex {
					t.Fatalf("refused import mutated its one-shot options: %+v", preserved)
				}
			})
		}
	}

	t.Run("fresh", func(t *testing.T) {
		inst := &Instance{ID: "nontui-fresh", Tool: "omp", Command: "omp --mode json", ompFreshStart: true}
		err := inst.prepareOmpIdentity()
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive") {
			t.Fatalf("non-TUI fresh launch was not refused clearly: %v", err)
		}
		if !inst.ompFreshStart {
			t.Fatal("refused fresh launch consumed its retry intent")
		}
		if failure := inst.SpawnFailure(); failure == nil || !strings.Contains(strings.ToLower(failure.DyingOutput), "interactive") {
			t.Fatalf("non-TUI fresh refusal was not recorded durably: %+v", failure)
		}
	})

	t.Run("persisted non-ACK fork recipe", func(t *testing.T) {
		builder := &Instance{ID: "old-nontui-fork", Tool: "omp", Command: "omp --print"}
		recipe := builder.buildOmpCommand(builder.Command)
		inst := &Instance{
			ID:                  builder.ID,
			Tool:                "omp",
			Command:             "omp",
			ForkStartCommand:    recipe,
			IsForkAwaitingStart: true,
		}
		err := inst.prepareOmpIdentity()
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive") {
			t.Fatalf("stored non-ACK fork recipe was not refused clearly: %v", err)
		}
		if !inst.IsForkAwaitingStart || inst.ForkStartCommand != recipe {
			t.Fatal("refused stored fork recipe was consumed or changed")
		}
		if failure := inst.SpawnFailure(); failure == nil || !strings.Contains(strings.ToLower(failure.DyingOutput), "interactive") {
			t.Fatalf("stored fork refusal was not recorded durably: %+v", failure)
		}
	})
}

func TestOmpNonTUIRefusesStatesThatNeedInteractiveIdentityTransition(t *testing.T) {
	for _, state := range []string{"pending missing transcript", "legacy migration", "copied legacy identity"} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			inst := &Instance{ID: "nontui-transition-" + strings.ReplaceAll(state, " ", "-"), Tool: "omp"}
			dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
			root := filepath.Join(dir, "root_shared-id.jsonl")
			providerRan := filepath.Join(t.TempDir(), "provider-ran")
			probe := filepath.Join(t.TempDir(), "omp-must-not-run")
			if err := os.WriteFile(probe, []byte("#!/bin/bash\ntouch "+shellQuote(providerRan)+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			inst.Command = probe + " --print"
			switch state {
			case "pending missing transcript":
				missing := filepath.Join(dir, "pending-root.jsonl")
				writeOmpValidationBinding(t, dir, missing, "pending-id", "pending", "legacy")
				if err := inst.prepareOmpIdentity(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive") {
					t.Fatalf("pending missing identity was not refused proactively: %v", err)
				}
			case "legacy migration":
				writeOmpValidationTranscript(t, root, "shared-id")
				if err := os.WriteFile(filepath.Join(dir, ".agent-deck-legacy-migration"), []byte(filepath.Base(root)+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "copied legacy identity":
				writeOmpValidationTranscript(t, root, "shared-id")
				sibling := filepath.Join(filepath.Dir(dir), "sibling", "other_shared-id.jsonl")
				writeOmpValidationTranscript(t, sibling, "shared-id")
			}
			if state != "pending missing transcript" {
				output, err := exec.Command("bash", "-c", inst.buildOmpCommand(inst.Command)).CombinedOutput()
				if err == nil || !strings.Contains(strings.ToLower(string(output)), "interactive") {
					t.Fatalf("%s was not refused before non-TUI provider launch: %v\n%s", state, err, output)
				}
			}
			if _, err := os.Stat(providerRan); !os.IsNotExist(err) {
				t.Fatalf("provider ran for unsupported %s: %v", state, err)
			}
			if state != "pending missing transcript" {
				if data, err := os.ReadFile(root); err != nil || !strings.Contains(string(data), "shared-id") {
					t.Fatalf("refusal changed source history: %q, %v", data, err)
				}
			}
		})
	}
}

func TestOmpNonTUIForkIsRejectedBeforeChildConstruction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := &Instance{ID: "nontui-fork-parent", Tool: "omp", Command: "omp --print", ProjectPath: t.TempDir()}
	dir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	root := filepath.Join(dir, "parent-root.jsonl")
	writeOmpValidationTranscript(t, root, "parent-root-id")
	writeOmpValidationBinding(t, dir, root, "parent-root-id", "saved", "legacy")
	bindingBefore, err := os.ReadFile(filepath.Join(dir, ompActiveBindingName))
	if err != nil {
		t.Fatal(err)
	}
	if parent.CanForkOmp() {
		t.Fatal("non-TUI OMP session was advertised as forkable")
	}
	child, command, err := parent.CreateForkedOmpInstanceWithOptions("must-not-exist", "", nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive") {
		t.Fatalf("non-TUI native fork was not refused clearly: child=%+v command=%q err=%v", child, command, err)
	}
	if child != nil || command != "" {
		t.Fatalf("refused native fork returned a constructed child: child=%+v command=%q", child, command)
	}
	if got, err := os.ReadFile(filepath.Join(dir, ompActiveBindingName)); err != nil || string(got) != string(bindingBefore) {
		t.Fatalf("refused native fork changed parent binding: %q, %v", got, err)
	}
}

func TestOmpNonTUIPromptDeliveryIsRejectedWithoutChangingOtherModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		inst    *Instance
		wantErr bool
	}{
		{name: "print", inst: &Instance{Tool: "omp", Command: "omp --print"}, wantErr: true},
		{name: "rpc", inst: &Instance{Tool: "omp", Command: "omp --mode=rpc"}, wantErr: true},
		{name: "interactive", inst: &Instance{Tool: "omp", Command: "omp"}},
		{name: "print-thoughts", inst: &Instance{Tool: "omp", Command: "omp --print-thoughts"}},
		{name: "other provider", inst: &Instance{Tool: "claude", Command: "claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.inst.PromptDeliveryError()
			if (err != nil) != tc.wantErr {
				t.Fatalf("PromptDeliveryError() = %v, wantErr=%t", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(strings.ToLower(err.Error()), "terminal prompt") {
				t.Fatalf("non-TUI prompt refusal is not actionable: %v", err)
			}
		})
	}
	noSession := &Instance{Tool: "omp", Command: "omp"}
	if err := noSession.SetOmpOptions(&OmpOptions{NoSession: true}); err != nil {
		t.Fatal(err)
	}
	if err := noSession.PromptDeliveryError(); err != nil {
		t.Fatalf("interactive --no-session lost pane prompt support: %v", err)
	}
}

func runOmpNonTUITargetRoute(t *testing.T, route, targetHome string, inst *Instance, raw string) ([]byte, error) {
	t.Helper()
	wrapped := raw
	switch route {
	case "local":
	case "ssh":
		inst.SSHHost = "isolated-nontui-target"
		wrapped = inst.wrapForSSH(raw)
	case "docker":
		wrapped = buildExecCommand(docker.NewContainer("isolated-nontui-container", "ignored"), nil, raw)
	default:
		t.Fatalf("unknown route %q", route)
	}
	cmd := exec.Command("bash", "-c", wrapped)
	cmd.Env = append(os.Environ(),
		"HOME="+targetHome,
		"AGENTDECK_OMP_DIR=/foreign/row",
		"AGENTDECK_OMP_LAUNCH_ID=foreign-generation",
		"AGENTDECK_OMP_SOURCE_BINDING=/foreign/source-binding",
		"AGENTDECK_OMP_SOURCE_ERROR=foreign source error",
	)
	return cmd.CombinedOutput()
}

func TestOmpNonTUIExecutionHostsStayUntrackedThenBootstrapTUI(t *testing.T) {
	for _, route := range []string{"local", "ssh", "docker"} {
		t.Run(route, func(t *testing.T) {
			targetHome := t.TempDir()
			transportBin := t.TempDir()
			transport := "#!/bin/bash\nset -eu\nexec env HOME=" + shellQuote(targetHome) + " bash -c \"${!#}\"\n"
			for _, name := range []string{"ssh", "docker"} {
				if err := os.WriteFile(filepath.Join(transportBin, name), []byte(transport), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", transportBin+string(os.PathListSeparator)+os.Getenv("PATH"))
			inst := &Instance{ID: "nontui-route-" + route, Tool: "omp"}
			dir := filepath.Join(targetHome, ".omp", "agent-deck", inst.ID)
			root := filepath.Join(dir, "created-root.jsonl")
			logFile := filepath.Join(t.TempDir(), "invocations")
			probe := filepath.Join(t.TempDir(), "omp-target-probe")
			probeBody := fmt.Sprintf(`#!/bin/bash
set -eu
mode=fresh
source_file=
session_dir=
extension=0
print_mode=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --resume) mode=resume; shift; source_file=${1-} ;;
    --session-dir) shift; session_dir=${1-} ;;
    --extension) extension=1; shift ;;
    --print) print_mode=1 ;;
  esac
  shift
done
if [ "$print_mode" -eq 1 ]; then
  if [ "$extension" -ne 0 ] || [ -n "${AGENTDECK_OMP_DIR+x}" ] || [ -n "${AGENTDECK_OMP_LAUNCH_ID+x}" ] || [ -n "${AGENTDECK_OMP_SOURCE_BINDING+x}" ] || [ -n "${AGENTDECK_OMP_SOURCE_ERROR+x}" ]; then
    echo 'non-TUI launch inherited or installed identity tracking' >&2
    exit 31
  fi
fi
if [ "$mode" = fresh ]; then
  mkdir -p "$session_dir"
  printf '{"type":"session","id":"created-root-id"}\n' > %s
else
  test "$source_file" = %s
fi
printf '%%s|%%s|%%s\n' "$mode" "$source_file" "$extension" >> %s
if [ "$extension" -eq 1 ]; then
  printf '1\n%%s\ncreated-root-id\nsaved\n%%s\n' %s "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
  printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"created-root-id","session_file":"%%s","identity_ready":true,"error":""}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" %s > "$AGENTDECK_OMP_DIR/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
fi
`, shellQuote(root), shellQuote(root), shellQuote(logFile), shellQuote(root), shellQuote(root))
			if err := os.WriteFile(probe, []byte(probeBody), 0o700); err != nil {
				t.Fatal(err)
			}
			inst.Command = probe + " --print"
			for attempt := 0; attempt < 2; attempt++ {
				output, err := runOmpNonTUITargetRoute(t, route, targetHome, inst, inst.buildOmpCommand(inst.Command))
				if err != nil {
					t.Fatalf("non-TUI target launch %d failed: %v\n%s", attempt+1, err, output)
				}
			}
			for _, name := range []string{".agent-deck-launch-generation", ".agent-deck-identity.mjs", ".agent-deck-title.json", ompActiveBindingName} {
				if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatalf("non-TUI target launch wrote identity state %s: %v", name, err)
				}
			}
			inst.Command = probe
			output, err := runOmpNonTUITargetRoute(t, route, targetHome, inst, inst.buildOmpCommand(inst.Command))
			if err != nil {
				t.Fatalf("later TUI bootstrap failed: %v\n%s", err, output)
			}
			binding, err := resolveOmpActiveBinding(dir)
			if err != nil || binding == nil || binding.File != root || binding.SessionID != "created-root-id" || binding.Generation == "legacy" {
				t.Fatalf("later TUI launch did not bind the unique non-TUI root: %+v, %v", binding, err)
			}
			logBytes, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			want := "fresh||0\nresume|" + root + "|0\nresume|" + root + "|1\n"
			if string(logBytes) != want {
				t.Fatalf("target route lost exact resume/bootstrap behavior: %q, want %q", logBytes, want)
			}
		})
	}
}
