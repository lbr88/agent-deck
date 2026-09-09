package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
)

func TestBuildOmpCommand_UsesInstanceScopedSessionDir(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	inst := &Instance{ID: "test-instance-id", Tool: "omp"}
	got := inst.buildOmpCommand("omp")

	wantSessionDir := "${HOME}/.omp/agent-deck/test-instance-id"
	for _, want := range []string{
		"session_dir=" + wantSessionDir,
		"mkdir -p \"$session_dir\"",
		"AGENTDECK_INSTANCE_ID=test-instance-id",
		"omp --resume \"$source_file\" --session-dir \"$session_dir\"",
		"omp --session-dir \"$session_dir\"",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildOmpCommand output missing %q\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, "--continue") {
		t.Fatalf("buildOmpCommand must not use terminal-breadcrumb-based --continue: %s", got)
	}
}

func TestBuildOmpCommandResumesOnlyOwnedTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "owned-instance", Tool: "omp", Command: "omp"}
	sessionDir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ownedSession := filepath.Join(sessionDir, "owned-session.jsonl")
	if err := os.WriteFile(ownedSession, []byte("{\"type\":\"session\",\"id\":\"owned-omp-id\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// OMP stores task/subagent transcripts inside a companion directory named
	// after the owning root transcript. They are not resumable root sessions.
	nestedDir := filepath.Join(sessionDir, "owned-session")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "newer-subagent.jsonl"), []byte("{\"type\":\"session\",\"id\":\"subagent-id\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeOmp := `#!/bin/sh
set -eu
resume_file=
session_dir=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --resume)
      shift
      resume_file="${1-}"
      ;;
    --session-dir)
      shift
      session_dir="${1-}"
      ;;
    --continue)
      echo "unexpected --continue" >&2
      exit 20
      ;;
  esac
  shift
done
[ "$resume_file" = "$EXPECTED_OMP_RESUME" ] || { echo "wrong resume file: $resume_file" >&2; exit 21; }
[ "$session_dir" = "$EXPECTED_OMP_TARGET" ] || { echo "wrong session dir: $session_dir" >&2; exit 22; }
`
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte(fakeOmp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EXPECTED_OMP_RESUME", ownedSession)
	t.Setenv("EXPECTED_OMP_TARGET", sessionDir)

	run := exec.Command("bash", "-c", inst.buildOmpCommand("omp"))
	run.Env = os.Environ()
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("OMP resume launch failed: %v\n%s", err, output)
	}
}

func TestCanForkOmpRejectsNestedSubagentTranscriptWithoutRootTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "nested-only", Tool: "omp", Command: "omp"}
	nestedDir := filepath.Join(home, ".omp", "agent-deck", inst.ID, "root-session")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "subagent.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if inst.CanForkOmp() {
		t.Fatal("nested task transcript without a top-level OMP session must not be forkable")
	}
}

func TestBuildOmpCommandStopsWhenSessionDirCreationFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	blockedParent := filepath.Join(home, ".omp", "agent-deck")
	if err := os.MkdirAll(filepath.Dir(blockedParent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "omp-invoked")
	fakeOmp := `#!/bin/sh
touch "$OMP_INVOKED_MARKER"
`
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte(fakeOmp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OMP_INVOKED_MARKER", marker)

	inst := &Instance{ID: "blocked-instance", Tool: "omp", Command: "omp"}
	run := exec.Command("bash", "-c", inst.buildOmpCommand("omp"))
	run.Env = os.Environ()
	if output, err := run.CombinedOutput(); err == nil {
		t.Fatalf("OMP launch succeeded after session directory creation failed:\n%s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("OMP was invoked after session directory creation failed: %v", err)
	}
}

func TestBuildOmpCommand_QuotesInstanceIDPathComponent(t *testing.T) {
	inst := &Instance{ID: "test instance'id", Tool: "omp"}
	got := inst.buildOmpCommand("omp")

	wantSessionDir := `${HOME}/.omp/agent-deck/` + shellescape.Quote(inst.ID)
	if !strings.Contains(got, "session_dir="+wantSessionDir) {
		t.Errorf("buildOmpCommand() should quote instance ID path component %q, got %q", wantSessionDir, got)
	}
}

func TestBuildOmpCommand_WrongTool(t *testing.T) {
	inst := &Instance{Tool: "claude"}
	got := inst.buildOmpCommand("some-command")
	if got != "some-command" {
		t.Errorf("buildOmpCommand with wrong tool = %q, want %q", got, "some-command")
	}
}

func TestBuildOmpCommand_DefaultsBinary(t *testing.T) {
	inst := &Instance{ID: "tid", Tool: "omp"}
	got := inst.buildOmpCommand("")
	if !strings.Contains(got, " omp --resume") || !strings.Contains(got, " omp --session-dir") {
		t.Errorf("empty command must default to omp binary, got %q", got)
	}
}

func TestResolveDynamicToolPreservesOmp(t *testing.T) {
	// omp drives other agent CLIs (codex, claude) as subprocesses; child
	// detection must never rewrite its identity.
	for _, detected := range []string{"codex", "claude", "gemini", "opencode", "kiro", "shell"} {
		if got := resolveDynamicTool("omp", detected, false); got != "omp" {
			t.Errorf("resolveDynamicTool(omp, %q) = %q, want omp", detected, got)
		}
	}
}

func TestResolveDynamicToolUpstreamBehaviorUnchanged(t *testing.T) {
	cases := []struct {
		current, detected string
		preserveCustom    bool
		want              string
	}{
		{"shell", "codex", false, "codex"},
		{"claude", "codex", false, "codex"},
		{"codex", "shell", false, "shell"},
		{"shell", "kiro", false, "kiro"},
		{"kiro", "claude", false, "kiro"},
		{"pi", "shell", false, "pi"},
		{"my-codex", "codex", true, "my-codex"},
		{"", "claude", false, "claude"},
	}
	for _, c := range cases {
		if got := resolveDynamicTool(c.current, c.detected, c.preserveCustom); got != c.want {
			t.Errorf("resolveDynamicTool(%q, %q, %v) = %q, want %q",
				c.current, c.detected, c.preserveCustom, got, c.want)
		}
	}
}

func TestResolveDynamicToolUpgradesShellToOmp(t *testing.T) {
	// A shell session that starts omp inside it should be re-typed as omp
	// (same wrapped-tool upgrade the other builtin CLIs get).
	if got := resolveDynamicTool("shell", "omp", false); got != "omp" {
		t.Errorf("resolveDynamicTool(shell, omp) = %q, want omp", got)
	}
}

func TestBuildOmpCommand_EmitsConfiguredOptions(t *testing.T) {
	inst := &Instance{ID: "omp-options", Tool: "omp"}
	err := inst.SetOmpOptions(&OmpOptions{
		Model: "opus", Models: []string{"opus", "gpt-5.5"},
		SmolModel: "flash", SlowModel: "opus", PlanModel: "gpt-5.5",
		ApprovalMode: "write", Profile: "work", MaxTime: "1h",
		AutoApprove: true, PrintThoughts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := inst.buildOmpCommand("omp")
	for _, want := range []string{
		"--model opus", "--models opus,gpt-5.5", "--smol flash",
		"--slow opus", "--plan gpt-5.5", "--approval-mode write",
		"--profile work", "--max-time 1h", "--auto-approve", "--print-thoughts",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildOmpCommand() missing %q: %s", want, got)
		}
	}
}

func TestBuildOmpCommand_NoSessionIsEphemeral(t *testing.T) {
	inst := &Instance{ID: "ephemeral", Tool: "omp"}
	if err := inst.SetOmpOptions(&OmpOptions{NoSession: true}); err != nil {
		t.Fatal(err)
	}
	got := inst.buildOmpCommand("omp")
	if strings.Contains(got, "--continue") || strings.Contains(got, "--session-dir") {
		t.Fatalf("ephemeral OMP command persisted a session: %s", got)
	}
	if !strings.Contains(got, "--no-session") {
		t.Fatalf("ephemeral OMP command missing --no-session: %s", got)
	}
}

func TestBuildOmpCommand_ImportIsOneShot(t *testing.T) {
	inst := &Instance{ID: "import", Tool: "omp"}
	if err := inst.SetOmpOptions(&OmpOptions{FromClaude: true}); err != nil {
		t.Fatal(err)
	}
	first := inst.buildOmpCommand("omp")
	second := inst.buildOmpCommand("omp")
	if !strings.Contains(first, "--from-claude") || strings.Contains(first, "--continue") {
		t.Fatalf("first import command = %s", first)
	}
	if strings.Contains(second, "--from-claude") || !strings.Contains(second, "--resume") {
		t.Fatalf("restart replayed import instead of resuming: %s", second)
	}
}

func TestBuildOmpCommand_FreshRestartSkipsResume(t *testing.T) {
	inst := &Instance{ID: "fresh", Tool: "omp", ompFreshStart: true}
	got := inst.buildOmpCommand("omp")
	if strings.Contains(got, "--resume") || strings.Contains(got, "--continue") {
		t.Fatalf("fresh OMP command = %s", got)
	}
	if inst.ompFreshStart {
		t.Fatal("fresh marker was not consumed")
	}
}

func TestOmpForkLaunchesNativeForkWithoutCloningParentIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := &Instance{ID: "parent", Tool: "omp", Command: "omp", ProjectPath: t.TempDir()}
	parentDir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(parentDir, "session.jsonl")
	if err := os.WriteFile(sourceFile, []byte("{\"type\":\"session\",\"id\":\"parent-omp-id\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nestedDir := filepath.Join(parentDir, "session")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "newer-subagent.jsonl"), []byte("{\"type\":\"session\",\"id\":\"subagent-id\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !parent.CanForkOmp() {
		t.Fatal("OMP parent with JSONL should be forkable")
	}
	child, cmd, err := parent.CreateForkedOmpInstanceWithOptions("child", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if child.Tool != "omp" || !child.IsForkAwaitingStart || child.ForkStartCommand != cmd {
		t.Fatalf("invalid OMP fork child: %+v", child)
	}

	childDir := filepath.Join(home, ".omp", "agent-deck", child.ID)
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeOmp := `#!/bin/sh
set -eu
source_file=
session_dir=
native_fork=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --fork)
      native_fork=true
      shift
      source_file="${1-}"
      ;;
    --session-dir)
      shift
      session_dir="${1-}"
      ;;
    --continue)
      echo "unexpected --continue" >&2
      exit 20
      ;;
  esac
  shift
done
[ "$native_fork" = true ] || { echo "missing --fork" >&2; exit 21; }
[ "$source_file" = "$EXPECTED_OMP_SOURCE" ] || { echo "wrong fork source: $source_file" >&2; exit 22; }
[ "$session_dir" = "$EXPECTED_OMP_TARGET" ] || { echo "wrong session dir: $session_dir" >&2; exit 23; }
[ ! -e "$session_dir/$(basename "$source_file")" ] || { echo "parent transcript was copied before native fork" >&2; exit 24; }
`
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte(fakeOmp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EXPECTED_OMP_SOURCE", sourceFile)
	t.Setenv("EXPECTED_OMP_TARGET", childDir)

	run := exec.Command("bash", "-c", cmd)
	run.Env = os.Environ()
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("OMP fork launch failed: %v\n%s", err, output)
	}
	if matches, err := filepath.Glob(filepath.Join(childDir, "*.jsonl")); err != nil {
		t.Fatal(err)
	} else if len(matches) != 0 {
		t.Fatalf("Agent Deck cloned the parent's OMP identity into the child: %v", matches)
	}
}

func TestOmpForkRefusesAmbiguousParentTranscripts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := &Instance{ID: "parent", Tool: "omp", Command: "omp", ProjectPath: t.TempDir()}
	parentDir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.jsonl", "second.jsonl"} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if parent.CanForkOmp() {
		t.Fatal("OMP parent with multiple root transcripts must not be forkable")
	}
	target := &Instance{ID: "child", Tool: "omp", Command: "omp", ProjectPath: parent.ProjectPath}
	if _, err := parent.buildOmpForkCommandForTarget(target, "omp"); err == nil || !strings.Contains(err.Error(), "cannot fork") {
		t.Fatalf("ambiguous local OMP parent did not fail closed: %v", err)
	}

	// Remote and sandbox parents cannot be inspected until their generated
	// command runs on the target, so that command must enforce the same rule.
	parent.SSHHost = "remote-test-host"
	cmd, err := parent.buildOmpForkCommandForTarget(target, "omp")
	if err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(home, ".omp", "agent-deck", target.ID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "omp-invoked")
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte("#!/bin/sh\ntouch \"$OMP_INVOKED_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OMP_INVOKED_MARKER", marker)
	run := exec.Command("bash", "-c", cmd)
	run.Env = os.Environ()
	output, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("remote OMP fork silently chose an ambiguous transcript:\n%s", output)
	}
	if !strings.Contains(string(output), "multiple OMP root transcripts") {
		t.Fatalf("ambiguous remote fork error did not identify the problem:\n%s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("OMP was invoked for an ambiguous remote parent: %v", err)
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty target changed before ambiguous parent refusal: entries=%v err=%v", entries, err)
	}
}

func TestOmpForkStopsWhenTargetSessionDirCreationFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := &Instance{ID: "parent", Tool: "omp", Command: "omp", ProjectPath: t.TempDir()}
	parentDir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "session.jsonl"), []byte("{\"type\":\"session\",\"id\":\"parent-session\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, cmd, err := parent.CreateForkedOmpInstanceWithOptions("child", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "mkdir"), []byte("#!/bin/sh\nexit 73\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "omp-invoked")
	fakeOmp := `#!/bin/sh
touch "$OMP_INVOKED_MARKER"
`
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte(fakeOmp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OMP_INVOKED_MARKER", marker)

	run := exec.Command("bash", "-c", cmd)
	run.Env = os.Environ()
	if output, err := run.CombinedOutput(); err == nil {
		t.Fatalf("OMP fork succeeded after target directory creation failed:\n%s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("OMP fork was invoked after target directory creation failed: %v", err)
	}
}

func TestBuildOmpCommandRekeysCopiedLegacyIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	inst := &Instance{ID: "current-instance", Tool: "omp", Command: "omp"}
	agentDeckDir := filepath.Join(home, ".omp", "agent-deck")
	sessionDir := filepath.Join(agentDeckDir, inst.ID)
	otherDir := filepath.Join(agentDeckDir, "copied-instance")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}

	const legacyID = "01a03221-99f5-7639-9bb7-2537f954aec7"
	sourceName := "2026-08-24T04-57-38-037Z_" + legacyID + ".jsonl"
	sourceFile := filepath.Join(sessionDir, sourceName)
	otherFile := filepath.Join(otherDir, "2026-08-27T07-10-34-000Z_"+legacyID+".jsonl")
	legacyBody := []byte("{\"type\":\"session\",\"id\":\"" + legacyID + "\"}\n")
	if err := os.WriteFile(sourceFile, legacyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherFile, legacyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	companionDir := strings.TrimSuffix(sourceFile, ".jsonl")
	if err := os.MkdirAll(companionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(companionDir, "tool.log"), []byte("preserve me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "omp-invocation")
	rootMarker := filepath.Join(home, "omp-root-created")
	copyMarker := filepath.Join(home, "omp-allow-artifact-copy")
	readyMarker := filepath.Join(home, "omp-ready")
	releaseMarker := filepath.Join(home, "omp-release")
	fakeOmp := `#!/bin/sh
set -eu
mode=
source_file=
session_dir=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --fork|--resume)
      mode="$1"
      shift
      source_file="${1-}"
      ;;
    --session-dir)
      shift
      session_dir="${1-}"
      ;;
  esac
  shift
done
[ "$mode" = "--fork" ] || { echo "legacy identity was resumed instead of re-keyed: $mode" >&2; exit 31; }
[ "$source_file" = "$EXPECTED_OMP_SOURCE" ] || { echo "wrong fork source: $source_file" >&2; exit 32; }
[ "$session_dir" = "$EXPECTED_OMP_TARGET" ] || { echo "wrong session dir: $session_dir" >&2; exit 33; }
printf '%s\n' "$mode $source_file" > "$OMP_INVOKED_MARKER"
printf '%s\n' '{"type":"session","id":"new-unique-id"}' > "$session_dir/2026-09-07T05-00-00-000Z_new-unique-id.jsonl"
touch "$OMP_ROOT_MARKER"
while [ ! -e "$OMP_ALLOW_COPY_MARKER" ]; do
  sleep 0.01
done
[ -f "$EXPECTED_OMP_COMPANION/tool.log" ] || { echo "source companion disappeared during native fork initialization" >&2; exit 34; }
mkdir -p "$session_dir/2026-09-07T05-00-00-000Z_new-unique-id"
cp "$EXPECTED_OMP_COMPANION/tool.log" "$session_dir/2026-09-07T05-00-00-000Z_new-unique-id/tool.log"
touch "$OMP_READY_MARKER"
while [ ! -e "$OMP_RELEASE_MARKER" ]; do
  sleep 0.01
done
`
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte(fakeOmp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EXPECTED_OMP_SOURCE", sourceFile)
	t.Setenv("EXPECTED_OMP_TARGET", sessionDir)
	t.Setenv("EXPECTED_OMP_COMPANION", companionDir)
	t.Setenv("OMP_INVOKED_MARKER", marker)
	t.Setenv("OMP_ROOT_MARKER", rootMarker)
	t.Setenv("OMP_ALLOW_COPY_MARKER", copyMarker)
	t.Setenv("OMP_READY_MARKER", readyMarker)
	t.Setenv("OMP_RELEASE_MARKER", releaseMarker)

	run := exec.Command("bash", "-c", inst.buildOmpCommand("omp"))
	run.Env = os.Environ()
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile := func(path string) bool {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); err == nil {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}
	if !waitForFile(rootMarker) {
		_ = os.WriteFile(releaseMarker, nil, 0o600)
		_ = run.Wait()
		t.Fatal("fake OMP did not create its replacement transcript")
	}
	archiveDir := filepath.Join(sessionDir, ".agent-deck-legacy-collisions", legacyID)
	archivedSource := filepath.Join(archiveDir, sourceName)
	archiveDeadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(archiveDeadline) {
		if _, err := os.Stat(archivedSource); err == nil {
			_ = os.WriteFile(copyMarker, nil, 0o600)
			_ = os.WriteFile(releaseMarker, nil, 0o600)
			_ = run.Wait()
			t.Fatal("legacy transcript was archived before OMP finished copying artifacts")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(sourceFile); err != nil {
		_ = os.WriteFile(releaseMarker, nil, 0o600)
		_ = run.Wait()
		t.Fatalf("legacy transcript was archived before OMP finished copying artifacts: %v", err)
	}
	migrationMarker := filepath.Join(sessionDir, ".agent-deck-legacy-migration")
	if _, err := os.Stat(migrationMarker); err != nil {
		_ = os.WriteFile(releaseMarker, nil, 0o600)
		_ = run.Wait()
		t.Fatalf("durable migration marker was cleared before OMP finished copying artifacts: %v", err)
	}
	if err := os.WriteFile(copyMarker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !waitForFile(readyMarker) {
		_ = os.WriteFile(releaseMarker, nil, 0o600)
		_ = run.Wait()
		t.Fatal("fake OMP did not finish its artifact copy")
	}

	if !waitForFile(archivedSource) {
		_ = os.WriteFile(releaseMarker, nil, 0o600)
		_ = run.Wait()
		t.Fatal("legacy transcript was not archived while OMP remained running")
	}
	forkedCompanionDir := filepath.Join(sessionDir, "2026-09-07T05-00-00-000Z_new-unique-id")
	if got, err := os.ReadFile(filepath.Join(forkedCompanionDir, "tool.log")); err != nil {
		_ = os.WriteFile(releaseMarker, nil, 0o600)
		_ = run.Wait()
		t.Fatalf("legacy artifacts were not copied before the source was archived: %v", err)
	} else if string(got) != "preserve me\n" {
		_ = os.WriteFile(releaseMarker, nil, 0o600)
		_ = run.Wait()
		t.Fatalf("forked legacy artifact changed: %q", got)
	}
	if err := os.WriteFile(releaseMarker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("OMP legacy identity migration failed: %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil {
		t.Fatalf("native OMP fork was not invoked: %v", err)
	} else if !strings.HasPrefix(string(got), "--fork ") {
		t.Fatalf("unexpected OMP invocation: %s", got)
	}

	if got, err := os.ReadFile(archivedSource); err != nil {
		t.Fatalf("legacy transcript was not preserved: %v", err)
	} else if string(got) != string(legacyBody) {
		t.Fatalf("archived legacy transcript changed: %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(companionDir, "tool.log")); err != nil {
		t.Fatalf("legacy companion directory was not preserved in place: %v", err)
	} else if string(got) != "preserve me\n" {
		t.Fatalf("legacy companion data changed: %q", got)
	}
	if _, err := os.Stat(sourceFile); !os.IsNotExist(err) {
		t.Fatalf("copied legacy root remained resumable after re-key: %v", err)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Fatalf("other Agent Deck instance was modified: %v", err)
	}
}

func TestBuildOmpCommandPreservesCopiedLegacyIdentityWithoutReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	inst := &Instance{ID: "current-instance", Tool: "omp", Command: "omp"}
	agentDeckDir := filepath.Join(home, ".omp", "agent-deck")
	sessionDir := filepath.Join(agentDeckDir, inst.ID)
	otherDir := filepath.Join(agentDeckDir, "copied-instance")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}

	const legacyID = "01a03221-99f5-7639-9bb7-2537f954aec7"
	sourceName := "2026-08-24T04-57-38-037Z_" + legacyID + ".jsonl"
	sourceFile := filepath.Join(sessionDir, sourceName)
	otherFile := filepath.Join(otherDir, "2026-08-27T07-10-34-000Z_"+legacyID+".jsonl")
	legacyBody := []byte("{\"type\":\"session\",\"id\":\"" + legacyID + "\"}\n")
	if err := os.WriteFile(sourceFile, legacyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherFile, legacyBody, 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	run := exec.Command("bash", "-c", inst.buildOmpCommand("omp"))
	run.Env = os.Environ()
	output, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("legacy identity migration succeeded without a replacement transcript:\n%s", output)
	}
	if !strings.Contains(string(output), "preserved "+sourceFile) {
		t.Fatalf("migration failure did not report preservation of the source transcript:\n%s", output)
	}
	if got, err := os.ReadFile(sourceFile); err != nil {
		t.Fatalf("legacy transcript was not preserved in place: %v", err)
	} else if string(got) != string(legacyBody) {
		t.Fatalf("preserved legacy transcript changed: %q", got)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Fatalf("other Agent Deck instance was modified: %v", err)
	}
	archiveDir := filepath.Join(sessionDir, ".agent-deck-legacy-collisions", legacyID)
	if _, err := os.Stat(archiveDir); !os.IsNotExist(err) {
		t.Fatalf("legacy transcript was archived without a replacement: %v", err)
	}
}

func TestBuildOmpCommandRecoversInterruptedLegacyMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	inst := &Instance{ID: "current-instance", Tool: "omp", Command: "omp"}
	agentDeckDir := filepath.Join(home, ".omp", "agent-deck")
	sessionDir := filepath.Join(agentDeckDir, inst.ID)
	otherDir := filepath.Join(agentDeckDir, "copied-instance")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}

	const legacyID = "01a03221-99f5-7639-9bb7-2537f954aec7"
	legacyName := "2026-08-24T04-57-38-037Z_" + legacyID + ".jsonl"
	legacyFile := filepath.Join(sessionDir, legacyName)
	newFile := filepath.Join(sessionDir, "2026-09-07T05-00-00-000Z_new-unique-id.jsonl")
	otherFile := filepath.Join(otherDir, "2026-08-27T07-10-34-000Z_"+legacyID+".jsonl")
	for path, body := range map[string]string{
		legacyFile: "{\"type\":\"session\",\"id\":\"" + legacyID + "\"}\n",
		newFile:    "{\"type\":\"session\",\"id\":\"new-unique-id\"}\n",
		otherFile:  "{\"type\":\"session\",\"id\":\"" + legacyID + "\"}\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	migrationMarker := filepath.Join(sessionDir, ".agent-deck-legacy-migration")
	if err := os.WriteFile(migrationMarker, []byte(legacyName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A previous launcher recorded the copied root before its native re-key
	// was interrupted. Recovery must not keep selecting that archived source.
	if err := writeOmpActiveBinding(sessionDir, &ompActiveBinding{File: legacyFile, SessionID: legacyID, State: "saved", Generation: "legacy"}); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "omp-invocation")
	fakeOmp := `#!/bin/sh
set -eu
mode=
source_file=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --fork|--resume)
      mode="$1"
      shift
      source_file="${1-}"
      ;;
  esac
  shift
done
[ "$mode" = "--resume" ] || { echo "interrupted migration was forked again: $mode" >&2; exit 41; }
[ "$source_file" = "$EXPECTED_OMP_RESUME" ] || { echo "wrong recovered transcript: $source_file" >&2; exit 42; }
printf '%s\n' "$mode $source_file" > "$OMP_INVOKED_MARKER"
`
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte(fakeOmp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EXPECTED_OMP_RESUME", newFile)
	t.Setenv("OMP_INVOKED_MARKER", marker)

	run := exec.Command("bash", "-c", inst.buildOmpCommand("omp"))
	run.Env = os.Environ()
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("interrupted legacy migration was not recovered: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("recovered transcript was not resumed: %v", err)
	}
	archiveDir := filepath.Join(sessionDir, ".agent-deck-legacy-collisions", legacyID)
	if _, err := os.Stat(filepath.Join(archiveDir, legacyName)); err != nil {
		t.Fatalf("interrupted migration source was not archived: %v", err)
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatalf("replacement transcript was modified: %v", err)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Fatalf("other Agent Deck instance was modified: %v", err)
	}
	// This fake provider does not acknowledge tracking. Keep the durable
	// recovery checkpoint, even though archive/copy work has completed.
	if _, err := os.Stat(migrationMarker); err != nil {
		t.Fatalf("durable migration marker was cleared before provider ACK: %v", err)
	}
}

func TestBuildOmpCommandRepairsInterruptedLegacyArtifactCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	inst := &Instance{ID: "current-instance", Tool: "omp", Command: "omp"}
	agentDeckDir := filepath.Join(home, ".omp", "agent-deck")
	sessionDir := filepath.Join(agentDeckDir, inst.ID)
	otherDir := filepath.Join(agentDeckDir, "copied-instance")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}

	const legacyID = "01a03221-99f5-7639-9bb7-2537f954aec7"
	legacyName := "2026-08-24T04-57-38-037Z_" + legacyID + ".jsonl"
	newName := "2026-09-07T05-00-00-000Z_new-unique-id.jsonl"
	newFile := filepath.Join(sessionDir, newName)
	otherFile := filepath.Join(otherDir, "2026-08-27T07-10-34-000Z_"+legacyID+".jsonl")
	archiveDir := filepath.Join(sessionDir, ".agent-deck-legacy-collisions", legacyID)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(archiveDir, legacyName): "{\"type\":\"session\",\"id\":\"" + legacyID + "\"}\n",
		newFile:                               "{\"type\":\"session\",\"id\":\"new-unique-id\"}\n",
		otherFile:                             "{\"type\":\"session\",\"id\":\"" + legacyID + "\"}\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	legacyCompanionDir := filepath.Join(sessionDir, strings.TrimSuffix(legacyName, ".jsonl"))
	newCompanionDir := filepath.Join(sessionDir, strings.TrimSuffix(newName, ".jsonl"))
	if err := os.MkdirAll(legacyCompanionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newCompanionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyCompanionDir, "tool.log"), []byte("complete artifact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newCompanionDir, "tool.log"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newCompanionDir, "new.log"), []byte("preserve new artifact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	migrationMarker := filepath.Join(sessionDir, ".agent-deck-legacy-migration")
	if err := os.WriteFile(migrationMarker, []byte(legacyName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "omp-invocation")
	fakeOmp := `#!/bin/sh
set -eu
mode=
source_file=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --fork|--resume)
      mode="$1"
      shift
      source_file="${1-}"
      ;;
  esac
  shift
done
[ "$mode" = "--resume" ] || { echo "interrupted artifact repair was forked again: $mode" >&2; exit 51; }
[ "$source_file" = "$EXPECTED_OMP_RESUME" ] || { echo "wrong repaired transcript: $source_file" >&2; exit 52; }
printf '%s\n' "$mode $source_file" > "$OMP_INVOKED_MARKER"
`
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte(fakeOmp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EXPECTED_OMP_RESUME", newFile)
	t.Setenv("OMP_INVOKED_MARKER", marker)

	run := exec.Command("bash", "-c", inst.buildOmpCommand("omp"))
	run.Env = os.Environ()
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("interrupted legacy artifact copy was not repaired: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("repaired transcript was not resumed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(newCompanionDir, "tool.log")); err != nil {
		t.Fatalf("repaired artifact is missing: %v", err)
	} else if string(got) != "complete artifact\n" {
		t.Fatalf("partial artifact was not repaired: %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(newCompanionDir, "new.log")); err != nil {
		t.Fatalf("new fork artifact was removed during repair: %v", err)
	} else if string(got) != "preserve new artifact\n" {
		t.Fatalf("new fork artifact changed during repair: %q", got)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Fatalf("other Agent Deck instance was modified: %v", err)
	}
	if _, err := os.Stat(migrationMarker); err != nil {
		t.Fatalf("durable migration marker was cleared after repair without provider ACK: %v", err)
	}
}

func TestBuildOmpCommandRefusesAmbiguousRootTranscripts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "ambiguous-instance", Tool: "omp", Command: "omp"}
	sessionDir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"2026-09-01T00-00-00-000Z_first-unique-id.jsonl",
		"2026-09-02T00-00-00-000Z_second-unique-id.jsonl",
	} {
		if err := os.WriteFile(filepath.Join(sessionDir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "omp-invoked")
	if err := os.WriteFile(filepath.Join(fakeBin, "omp"), []byte("#!/bin/sh\ntouch \"$OMP_INVOKED_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OMP_INVOKED_MARKER", marker)

	run := exec.Command("bash", "-c", inst.buildOmpCommand("omp"))
	run.Env = os.Environ()
	output, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("ambiguous OMP history was silently resumed:\n%s", output)
	}
	if !strings.Contains(string(output), "multiple OMP root transcripts") {
		t.Fatalf("ambiguous-history error did not identify the problem:\n%s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("OMP was invoked despite ambiguous root transcripts: %v", err)
	}
}
