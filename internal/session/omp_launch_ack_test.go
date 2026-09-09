package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"al.essio.dev/pkg/shellescape"
	"github.com/asheshgoplani/agent-deck/internal/docker"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func newOmpLaunchAckInstance(t *testing.T, name string) *Instance {
	t.Helper()
	inst := NewInstanceWithTool(name, t.TempDir(), "omp")
	t.Cleanup(func() { _ = inst.KillAndWait() })
	return inst
}

func writeOmpLaunchAckProbe(t *testing.T, name, body string) string {
	t.Helper()
	probe := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(probe, []byte("#!/bin/bash\nset -eu\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return probe
}

func ompSuccessfulAckScript(delay string) string {
	return fmt.Sprintf(`sleep %s
session_file="$AGENTDECK_OMP_DIR/root.jsonl"
session_id="$AGENTDECK_INSTANCE_ID-session"
printf '{"type":"session","id":"%%s"}\n' "$session_id" > "$session_file"
printf '1\n%%s\n%%s\nsaved\n%%s\n' "$session_file" "$session_id" "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"%%s","session_file":"%%s","identity_ready":true,"error":""}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" "$session_id" "$session_file" > "$AGENTDECK_OMP_DIR/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
sleep 10`, delay)
}

func TestOmpStartWaitsForExactIdentityAcknowledgment(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-delayed-identity-ack")
	inst.Command = writeOmpLaunchAckProbe(t, "delayed-ack", ompSuccessfulAckScript("0.8"))
	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
	generation, err := os.ReadFile(filepath.Join(dir, ".agent-deck-launch-generation"))
	if err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(dir, ".agent-deck-omp-status."+strings.TrimSpace(string(generation))+".json")
	if _, err := os.Stat(status); err != nil {
		t.Fatalf("Start returned before the intended generation acknowledged identity: %v", err)
	}
}

func TestOmpStartReturnsDelayedProviderFailureWithPaneOutput(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-delayed-launch-failure")
	inst.Command = writeOmpLaunchAckProbe(t, "delayed-failure", "sleep 0.8\nprintf 'actual delayed provider failure\\n' >&2\nexit 23")
	err := inst.Start()
	if err == nil || !strings.Contains(err.Error(), "actual delayed provider failure") {
		t.Fatalf("slow provider failure was reported as launch success: %v", err)
	}
}

func TestOmpStartWithMessageDoesNotSendBeforeIdentityError(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-message-identity-error")
	received := filepath.Join(t.TempDir(), "received-message")
	inst.Command = writeOmpLaunchAckProbe(t, "identity-error", fmt.Sprintf(`
printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"","session_file":"","identity_ready":false,"error":"tracker exploded before identity publication"}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
printf '╭── π  > waiting for input\n'
if IFS= read -r -t 4 line; then printf '%%s\n' "$line" > %s; fi
sleep 1`, shellescape.Quote(received)))
	err := inst.StartWithMessage("must not reach an untracked OMP session")
	if err == nil || !strings.Contains(err.Error(), "tracker exploded before identity publication") {
		t.Fatalf("identity tracker failure was not returned: %v", err)
	}
	if _, err := os.Stat(received); !os.IsNotExist(err) {
		t.Fatalf("initial message reached an unacknowledged OMP session: %v", err)
	}
}

func TestOmpExitedNegativeAckReturnsTrackerError(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-exited-negative-ack")
	inst.Command = writeOmpLaunchAckProbe(t, "exited-negative-ack", `
printf '{"instance_id":"%s","launch_id":"%s","session_id":"","session_file":"","identity_ready":false,"error":"tracker rejected identity before exit"}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
exit 29`)
	if err := inst.Start(); err == nil || !strings.Contains(err.Error(), "tracker rejected identity before exit") {
		t.Fatalf("provider status was lost after immediate exit: %v", err)
	}
}

func TestOmpTitleWarningDoesNotInvalidateIdentityAcknowledgment(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-title-warning-ack")
	script := strings.Replace(ompSuccessfulAckScript("0"), `"error":""`, `"error":"title sync failed but identity is valid"`, 1)
	inst.Command = writeOmpLaunchAckProbe(t, "title-warning", script)
	if err := inst.Start(); err != nil {
		t.Fatalf("title-only warning rejected valid identity: %v", err)
	}
}

func TestOmpPrintModeDoesNotWaitForTUIIdentityAcknowledgment(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-print-mode")
	probe := writeOmpLaunchAckProbe(t, "print-mode", "sleep 0.8\nexit 0")
	inst.Command = probe + " --print"
	if err := inst.Start(); err != nil {
		t.Fatalf("intentional OMP print mode required a TUI-only identity ACK: %v", err)
	}
}

func TestOmpRetainedForkCommandRecoversGenerationTemplateAndAckPolicy(t *testing.T) {
	original := &Instance{ID: "persisted-fork-template", Tool: "omp", Command: "omp"}
	stored := original.buildOmpCommand("omp")
	reloaded := &Instance{Tool: "omp", Command: "omp", ForkStartCommand: stored, IsForkAwaitingStart: true}

	first, firstGeneration, firstAck, err := reloaded.prepareOmpLaunchGeneration(reloaded.consumeForkStartCommand())
	if err != nil {
		t.Fatal(err)
	}
	second, secondGeneration, secondAck, err := reloaded.prepareOmpLaunchGeneration(reloaded.consumeForkStartCommand())
	if err != nil {
		t.Fatal(err)
	}
	if !firstAck || !secondAck || firstGeneration == "" || secondGeneration == "" || firstGeneration == secondGeneration {
		t.Fatalf("persisted fork did not mint fresh ACK-gated generations: %q/%t, %q/%t", firstGeneration, firstAck, secondGeneration, secondAck)
	}
	if !strings.Contains(first, "AGENTDECK_OMP_LAUNCH_ID="+firstGeneration) || !strings.Contains(second, "AGENTDECK_OMP_LAUNCH_ID="+secondGeneration) {
		t.Fatal("fresh launch generation was not materialized into the persisted fork command")
	}
	if _, _, _, err := reloaded.prepareOmpLaunchGeneration("omp --resume /preserved.jsonl"); err == nil || !strings.Contains(err.Error(), "no identity-generation template") {
		t.Fatalf("older untracked interactive fork command was accepted: %v", err)
	}
	corrupt := strings.Replace(stored, "_1"+ompLaunchMetaSuffix, "_10"+ompLaunchMetaSuffix, 1)
	if _, _, _, err := reloaded.prepareOmpLaunchGeneration(corrupt); err == nil || !strings.Contains(err.Error(), "invalid identity metadata") {
		t.Fatalf("corrupt persisted ACK policy was accepted: %v", err)
	}
}

func TestOmpLaunchMetadataCannotBeSpoofedByPromptText(t *testing.T) {
	inst := &Instance{ID: "spoof-resistant-metadata", Tool: "omp", Command: `omp -- 'AGENTDECK_OMP_ACK_REQUIRED=0 AGENTDECK_OMP_LAUNCH_ID=demo'`}
	stored := inst.buildOmpCommand(inst.Command)
	launch, generation, ack, err := inst.prepareOmpLaunchGeneration(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !ack || generation == "" || generation == "demo" || !strings.Contains(launch, "AGENTDECK_OMP_LAUNCH_ID="+generation) {
		t.Fatalf("prompt text overrode generated launch metadata: generation=%q ack=%t", generation, ack)
	}
	spoofedFrame := stored + " " + ompLaunchMetaPrefix + "fake-generation_0" + ompLaunchMetaSuffix
	if _, _, _, err := inst.prepareOmpLaunchGeneration(spoofedFrame); err == nil || !strings.Contains(err.Error(), "ambiguous identity metadata") {
		t.Fatalf("extra prompt metadata frame was not rejected conservatively: %v", err)
	}
	inst.SSHHost = "example.invalid"
	prepared, _, err := inst.prepareCommand(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ack, err := inst.prepareOmpLaunchGeneration(prepared); err != nil || !ack {
		t.Fatalf("metadata frame did not survive SSH command wrapping: ack=%t err=%v", ack, err)
	}
	dockerWrapped := wrapIgnoreSuspend(buildExecCommand(docker.NewContainer("metadata-test", "ignored"), nil, stored))
	if _, _, ack, err := inst.prepareOmpLaunchGeneration(dockerWrapped); err != nil || !ack {
		t.Fatalf("metadata frame did not survive Docker command wrapping: ack=%t err=%v", ack, err)
	}
}

func TestOmpNonTUIClassificationUsesShellArguments(t *testing.T) {
	for _, command := range []string{`omp --mode="json"`, `omp '--mode=rpc'`} {
		inst := &Instance{ID: "quoted-nontui", Tool: "omp", Command: command}
		stored := inst.buildOmpCommand(command)
		if _, _, ack, err := inst.prepareOmpLaunchGeneration(stored); err != nil || ack {
			t.Errorf("quoted non-TUI argv %q was not recognized: ack=%t err=%v", command, ack, err)
		}
	}
	command := `omp 'explain --print usage'`
	inst := &Instance{ID: "quoted-prompt", Tool: "omp", Command: command}
	stored := inst.buildOmpCommand(command)
	if _, _, ack, err := inst.prepareOmpLaunchGeneration(stored); err != nil || !ack {
		t.Fatalf("quoted prompt was mistaken for a print flag: ack=%t err=%v", ack, err)
	}
	for _, command := range []string{`omp --system-prompt '--print'`, `omp --append-system-prompt --print`} {
		inst := &Instance{ID: "valued-flag-prompt", Tool: "omp", Command: command}
		stored := inst.buildOmpCommand(command)
		if _, _, ack, err := inst.prepareOmpLaunchGeneration(stored); err != nil || !ack {
			t.Errorf("value-taking flag in %q exposed its --print value: ack=%t err=%v", command, ack, err)
		}
	}
	for _, command := range []string{
		`omp > --print`,
		`omp # --print`,
		`omp "$OMP_ARGUMENT"`,
		`omp --system-prompt $EMPTY --print`,
		"omp `printf -- --print`",
		`omp --mode={json,text}`,
		`omp --mode=*`,
	} {
		if words, ok := ompShellWords(command); ok {
			t.Errorf("unsupported shell syntax in %q was accepted as argv: %q", command, words)
		}
		inst := &Instance{ID: "complex-shell-command", Tool: "omp", Command: command}
		stored := inst.buildOmpCommand(command)
		if _, _, ack, err := inst.prepareOmpLaunchGeneration(stored); err != nil || !ack {
			t.Errorf("unsupported shell syntax in %q bypassed TUI identity ACK: ack=%t err=%v", command, ack, err)
		}
	}
}

func TestOmpFreshPrimaryPaneProbeTargetsOnlyPaneZero(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	socket := "omp-primary-" + strings.ToLower(randomString(8))
	name := "omp-primary-pane"
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	if out, err := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", name, "sleep", "30").CombinedOutput(); err != nil {
		t.Fatalf("create isolated tmux session: %v: %s", err, out)
	}
	if out, err := exec.Command("tmux", "-L", socket, "split-window", "-t", name+":0", "sleep", "30").CombinedOutput(); err != nil {
		t.Fatalf("split primary window: %v: %s", err, out)
	}
	alive, err := (&tmux.Session{Name: name, SocketName: socket}).PrimaryPaneAliveFresh()
	if err != nil || !alive {
		t.Fatalf("live pane 0.0 was obscured by sibling panes: alive=%t err=%v", alive, err)
	}
}

func TestOmpNoSessionAndPinnedNonTUIModesSkipIdentityAck(t *testing.T) {
	for _, command := range []string{"omp --print", "omp -p", "omp --mode text", "omp --mode=json", "omp --mode rpc", "omp --mode=acp", "omp --mode rpc-ui"} {
		if _, generation, ack, err := (&Instance{Tool: "omp"}).prepareOmpLaunchGeneration(command); err != nil || generation != "" || ack {
			t.Errorf("non-TUI command %q was ACK-gated: generation=%q ack=%t err=%v", command, generation, ack, err)
		}
	}
	if _, _, _, err := (&Instance{Tool: "omp"}).prepareOmpLaunchGeneration("omp --mode plan"); err == nil {
		t.Fatal("unknown mode was assumed non-TUI and silently bypassed identity tracking")
	}
	if _, _, _, err := (&Instance{Tool: "omp"}).prepareOmpLaunchGeneration("omp -- --print"); err == nil {
		t.Fatal("flag-shaped prompt after -- was mistaken for non-TUI mode")
	}
}

func TestOmpNoSessionStartDoesNotRequireIdentityAck(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-no-session-no-ack")
	inst.Command = writeOmpLaunchAckProbe(t, "no-session", "sleep 10")
	if err := inst.SetOmpOptions(&OmpOptions{NoSession: true}); err != nil {
		t.Fatal(err)
	}
	if err := inst.Start(); err != nil {
		t.Fatalf("explicit no-session launch was blocked on persistent identity: %v", err)
	}
	if err := inst.ompDeduplicatedLaunch(false); err != nil {
		t.Fatalf("deduplicated no-session launch incorrectly required identity files: %v", err)
	}
	if err := inst.ompDeduplicatedLaunch(true); err == nil || !strings.Contains(err.Error(), "initial message was not sent") {
		t.Fatalf("deduplicated no-session message was silently dropped: %v", err)
	}
}

func TestOmpReadyAckCannotPassWithStaleAlivePaneCache(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-stale-pane-cache")
	inst.Command = writeOmpLaunchAckProbe(t, "ready-then-dead", strings.Replace(ompSuccessfulAckScript("0"), "sleep 10", "exit 0", 1))
	tmux.SeedPaneInfoCacheForTest(t, map[string]tmux.PaneInfo{inst.GetTmuxSession().Name: {Dead: false}})
	if err := inst.Start(); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("dead primary pane passed ACK through stale alive caches: %v", err)
	}
}

func TestOmpDeduplicatedReadyAckFailsClosedOnFreshPaneQueryError(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "omp-dedup-query-failure", Tool: "omp", Command: "omp", tmuxSession: &tmux.Session{Name: "missing-omp-pane", SocketName: "missing-omp-socket"}}
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	file := filepath.Join(dir, "root.jsonl")
	writeOmpValidationTranscript(t, file, "query-failure-session")
	writeOmpValidationBindingAt(t, dir, ompActiveBindingName+".query-generation", file, "query-failure-session", "saved", "query-generation")
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("query-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := fmt.Sprintf(`{"instance_id":%q,"launch_id":"query-generation","session_id":"query-failure-session","session_file":%q,"identity_ready":true,"error":""}`, inst.ID, file)
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-omp-status.query-generation.json"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := inst.ompDeduplicatedLaunch(false); err == nil {
		t.Fatal("fresh primary-pane query failure was treated as a live deduplicated OMP launch")
	}
}

func TestOmpLaunchSetupRejectsCorruptPriorGenerationBytes(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "nul", data: []byte("old\x00generation\n")},
		{name: "unterminated extra", data: []byte("old-generation\nextra")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := &Instance{ID: "corrupt-setup-" + strings.ReplaceAll(tc.name, " ", "-"), Tool: "omp", Command: "omp"}
			dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			generationFile := filepath.Join(dir, ".agent-deck-launch-generation")
			if err := os.WriteFile(generationFile, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			output, runErr := exec.Command("bash", "-c", inst.ompIdentityLaunchSetup()+" true").CombinedOutput()
			if runErr == nil || !strings.Contains(string(output), "Invalid OMP launch generation") {
				t.Fatalf("corrupt generation bytes reached launch setup: err=%v output=%q", runErr, output)
			}
			after, err := os.ReadFile(generationFile)
			if err != nil || string(after) != string(tc.data) {
				t.Fatalf("launch setup rewrote corrupt evidence: after=%q err=%v", after, err)
			}
		})
	}
}

func TestOmpConcurrentSlowIdentityFailureNeverDeduplicatesAsSuccess(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-concurrent-identity-failure")
	invocations := filepath.Join(t.TempDir(), "invocations")
	inst.Command = writeOmpLaunchAckProbe(t, "concurrent-failure", fmt.Sprintf(`
printf 'attempt\n' >> %s
sleep 0.8
printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"","session_file":"","identity_ready":false,"error":"concurrent tracker failure"}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
sleep 1`, shellescape.Quote(invocations)))

	start := make(chan struct{})
	errors := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			errors <- inst.StartWithMessage("must not be dropped")
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errors; err == nil {
			t.Fatal("concurrent unacknowledged OMP start was deduplicated as success")
		}
	}
	data, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "attempt\n") != 1 {
		t.Fatalf("concurrent identity failure launched OMP more than once: %q", data)
	}
}

func TestOmpFailedForkRestartRetainsCommandAndRetriesBoundChild(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	parent := &Instance{ID: "fork-parent-ack-" + strings.ReplaceAll(t.Name(), "/", "-"), Tool: "omp", Command: "omp", ProjectPath: t.TempDir()}
	parentDir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	parentFile := filepath.Join(parentDir, "parent-root.jsonl")
	writeOmpValidationTranscript(t, parentFile, "parent-ack-id")
	writeOmpValidationBinding(t, parentDir, parentFile, "parent-ack-id", "saved", "parent-generation")
	t.Cleanup(func() { _ = os.RemoveAll(parentDir) })
	inst := newOmpLaunchAckInstance(t, "omp-fork-ack-retry")
	generations := filepath.Join(t.TempDir(), "generations")
	probe := writeOmpLaunchAckProbe(t, "fork-retry", fmt.Sprintf(`
mode=
source_file=
session_dir=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --fork|--resume) mode=$1; shift; source_file=${1-} ;;
    --session-dir) shift; session_dir=${1-} ;;
  esac
  shift
done
printf '%%s|%%s\n' "$mode" "$AGENTDECK_OMP_LAUNCH_ID" >> %s
session_file="$session_dir/child-root.jsonl"
session_id="$AGENTDECK_INSTANCE_ID-child"
if [ "$mode" = --fork ]; then
  test -f "$source_file"
  printf '{"type":"session","id":"%%s"}\n' "$session_id" > "$session_file"
  printf '1\n%%s\n%%s\nsaved\n%%s\n' "$session_file" "$session_id" "$AGENTDECK_OMP_LAUNCH_ID" > "$session_dir/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
  printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"%%s","session_file":"%%s","identity_ready":false,"error":"post-fork tracker failure"}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" "$session_id" "$session_file" > "$session_dir/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
  sleep 0.8
  exit 31
fi
test "$mode" = --resume
test "$source_file" = "$session_file"
printf '1\n%%s\n%%s\nsaved\n%%s\n' "$session_file" "$session_id" "$AGENTDECK_OMP_LAUNCH_ID" > "$session_dir/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"%%s","session_file":"%%s","identity_ready":true,"error":""}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" "$session_id" "$session_file" > "$session_dir/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
sleep 10`, shellescape.Quote(generations)))
	inst.Command = probe
	command, err := parent.buildOmpForkCommandForTarget(inst, probe)
	if err != nil {
		t.Fatal(err)
	}
	inst.ForkStartCommand = command
	inst.IsForkAwaitingStart = true
	if err := inst.Restart(); err == nil || !strings.Contains(err.Error(), "post-fork tracker failure") {
		t.Fatalf("first failed fork launch was not reported: %v", err)
	}
	if !inst.IsForkAwaitingStart || inst.ForkStartCommand == "" {
		t.Fatal("failed fork consumed its only safe retry command before identity ACK")
	}
	if err := inst.Restart(); err != nil {
		t.Fatalf("fork retry failed: %v", err)
	}
	if inst.IsForkAwaitingStart || inst.ForkStartCommand != "" {
		t.Fatal("acknowledged fork did not consume its one-shot command")
	}
	data, err := os.ReadFile(generations)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "--fork|") || !strings.HasPrefix(lines[1], "--resume|") || strings.TrimPrefix(lines[0], "--fork|") == strings.TrimPrefix(lines[1], "--resume|") {
		t.Fatalf("fork retry reused a launch generation: %q", data)
	}
}

func TestOmpRestartFreshSupersedesPendingForkIntent(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	parent := &Instance{ID: "fresh-fork-parent-" + randomString(8), Tool: "omp", Command: "omp", ProjectPath: t.TempDir()}
	parentDir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	parentFile := filepath.Join(parentDir, "active-parent.jsonl")
	historicalFile := filepath.Join(parentDir, "historical-parent.jsonl")
	writeOmpValidationTranscript(t, parentFile, "fresh-parent-id")
	writeOmpValidationTranscript(t, historicalFile, "fresh-parent-history")
	writeOmpValidationBinding(t, parentDir, parentFile, "fresh-parent-id", "saved", "fresh-parent-generation")
	t.Cleanup(func() { _ = os.RemoveAll(parentDir) })

	inst := newOmpLaunchAckInstance(t, "omp-fresh-supersedes-fork")
	invocations := filepath.Join(t.TempDir(), "fresh-invocations")
	probe := writeOmpLaunchAckProbe(t, "fresh-supersedes-fork", fmt.Sprintf(`
mode=fresh
session_dir=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --fork|--resume) mode=$1; shift ;;
    --session-dir) shift; session_dir=${1-} ;;
  esac
  shift
done
printf '%%s\n' "$mode" >> %s
session_file="$session_dir/fresh-root.jsonl"
session_id="$AGENTDECK_INSTANCE_ID-fresh"
printf '{"type":"session","id":"%%s"}\n' "$session_id" > "$session_file"
printf '1\n%%s\n%%s\nsaved\n%%s\n' "$session_file" "$session_id" "$AGENTDECK_OMP_LAUNCH_ID" > "$session_dir/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
rm -f "$session_dir/.agent-deck-fresh-pending.$AGENTDECK_OMP_LAUNCH_ID"
printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"%%s","session_file":"%%s","identity_ready":true,"error":""}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" "$session_id" "$session_file" > "$session_dir/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
sleep 10`, shellescape.Quote(invocations)))
	inst.Command = probe
	command, err := parent.buildOmpForkCommandForTarget(inst, probe)
	if err != nil {
		t.Fatal(err)
	}
	inst.ForkStartCommand = command
	inst.IsForkAwaitingStart = true
	if err := inst.RestartFresh(); err != nil {
		t.Fatalf("explicit fresh restart did not supersede pending fork: %v", err)
	}
	data, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fresh\n" {
		t.Fatalf("explicit fresh restart replayed fork/resume intent: %q", data)
	}
	if inst.IsForkAwaitingStart || inst.ForkStartCommand != "" {
		t.Fatal("acknowledged fresh replacement retained superseded fork intent")
	}
	for _, file := range []string{parentFile, historicalFile} {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("explicit fresh restart removed parent history %s: %v", file, err)
		}
	}
}

func TestOmpLaunchWaitRejectsStaleGenerationAcknowledgment(t *testing.T) {
	inst := newOmpLaunchAckInstance(t, "omp-stale-launch-ack")
	inst.Command = writeOmpLaunchAckProbe(t, "stale-ack", `
printf 'stale-generation\n' > "$AGENTDECK_OMP_DIR/.agent-deck-launch-generation"
printf '{"instance_id":"%s","launch_id":"stale-generation","session_id":"stale","session_file":"/stale.jsonl","identity_ready":true,"error":""}\n' "$AGENTDECK_INSTANCE_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-omp-status.stale-generation.json"
sleep 0.8
printf 'stale generation process exited\n' >&2
exit 32`)
	if err := inst.Start(); err == nil {
		t.Fatalf("stale generation ACK satisfied current launch: %v", err)
	}
}
