package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

func TestBuildOmpCommand_FreshRestartRemovesScopedHistory(t *testing.T) {
	inst := &Instance{ID: "fresh", Tool: "omp", ompFreshStart: true}
	got := inst.buildOmpCommand("omp")
	if !strings.Contains(got, `rm -rf -- "$session_dir"`) || strings.Contains(got, "--continue") {
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
