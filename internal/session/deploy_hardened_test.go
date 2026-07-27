package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// localShellRemoteExec runs the generated remote POSIX-shell transaction in a
// temp directory. Tests exercise the real lock/stage/verify/publish/rollback
// behavior without SSH or a real remote host.
func localShellRemoteExec(ctx context.Context, remoteCmd string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", remoteCmd)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func assertNoDeployArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".agent-deck.update.lock" ||
			strings.HasPrefix(name, ".agent-deck.update.") ||
			strings.HasPrefix(name, ".agent-deck.backup.") {
			t.Errorf("deployment artifact was not cleaned up: %s", filepath.Join(dir, name))
		}
	}
}

func TestDeployBinary_HardenedTransactionHappyPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dir with ' quote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "agent-deck")
	if err := os.WriteFile(target, []byte("old-binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte("#!/bin/sh\nprintf 'non-fatal startup warning\\n' >&2\nprintf 'Agent Deck v2.4.6\\n'\n")

	var gotCmd string
	var gotStdin []byte
	r := &SSHRunner{remoteExecFn: func(ctx context.Context, remoteCmd string, stdin []byte) ([]byte, error) {
		gotCmd = remoteCmd
		gotStdin = append([]byte(nil), stdin...)
		return localShellRemoteExec(ctx, remoteCmd, stdin)
	}}
	if err := r.DeployBinary(context.Background(), newBinary, target, "v2.4.6"); err != nil {
		t.Fatalf("DeployBinary: %v", err)
	}
	gotBinary, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBinary, newBinary) || !bytes.Equal(gotStdin, newBinary) {
		t.Fatalf("published/stdin bytes differ from streamed candidate")
	}
	if strings.Contains(gotCmd, target+".new") {
		t.Fatalf("deploy still uses fixed .new staging path:\n%s", gotCmd)
	}
	stage := `mktemp "$ad_dir/.agent-deck.update.XXXXXX"`
	if !strings.Contains(gotCmd, stage) {
		t.Fatalf("deploy lacks unique sibling mktemp %q:\n%s", stage, gotCmd)
	}
	candidateCheck := `"$ad_tmp" version > "$ad_lock/version-output" 2> "$ad_lock/version-error"`
	publish := `mv -f "$ad_tmp" "$ad_target"`
	checkAt, publishAt := strings.Index(gotCmd, candidateCheck), strings.Index(gotCmd, publish)
	if checkAt < 0 || publishAt < 0 || checkAt >= publishAt {
		t.Fatalf("candidate check must precede publish (check=%d publish=%d)", checkAt, publishAt)
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RejectsWrongCandidateBeforePublish(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("known-good-old-binary\n")
	if err := os.WriteFile(target, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	wrong := []byte("#!/bin/sh\nprintf 'Agent Deck v9.9.9\\n'\n")
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	err := r.DeployBinary(context.Background(), wrong, target, "2.4.6")
	if err == nil || !strings.Contains(err.Error(), "staged candidate reports an unexpected version") {
		t.Fatalf("DeployBinary error = %v, want staged-version mismatch", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBinary) {
		t.Fatalf("wrong candidate changed live target")
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RefusesSymlinkWithoutFlatteningIt(t *testing.T) {
	dir := t.TempDir()
	realTarget := filepath.Join(dir, "agent-deck-real")
	target := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("known-good-package-managed-binary\n")
	if err := os.WriteFile(realTarget, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realTarget, target); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	candidate := []byte("#!/bin/sh\nprintf 'Agent Deck v2.4.6\\n'\n")
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	err := r.DeployBinary(context.Background(), candidate, target, "2.4.6")
	if err == nil || !strings.Contains(err.Error(), "symbolic-link install") {
		t.Fatalf("DeployBinary error = %v, want symbolic-link refusal", err)
	}
	info, statErr := os.Lstat(target)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("remote deployment flattened the install symlink")
	}
	got, readErr := os.ReadFile(realTarget)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBinary) {
		t.Fatal("remote deployment changed the symlink target")
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RefusesDirectHomebrewCellarTarget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Cellar", "agent-deck", "2.4.5", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("known-good-homebrew-binary\n")
	if err := os.WriteFile(target, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := []byte("#!/bin/sh\nprintf 'Agent Deck v2.4.6\\n'\n")
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	err := r.DeployBinary(context.Background(), candidate, target, "2.4.6")
	if err == nil || !strings.Contains(err.Error(), "Homebrew-managed install") {
		t.Fatalf("DeployBinary error = %v, want Homebrew refusal", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBinary) {
		t.Fatal("remote deployment changed the Homebrew Cellar binary")
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RollsBackFailedPostPublishVerification(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("previous-known-good-binary\n")
	if err := os.WriteFile(target, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := []byte(`#!/bin/sh
case "$0" in
	*/.agent-deck.update.*) printf 'Agent Deck v3.1.4\n' ;;
	*) printf 'broken after publish\n' >&2; exit 42 ;;
esac
`)
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	err := r.DeployBinary(context.Background(), candidate, target, "3.1.4")
	if err == nil || !strings.Contains(err.Error(), "published binary failed verification") {
		t.Fatalf("DeployBinary error = %v, want post-publish verification failure", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBinary) {
		t.Fatalf("failed publish was not rolled back")
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RollsBackWhenTerminatedAfterPublish(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("previous-known-good-binary\n")
	if err := os.WriteFile(target, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := []byte(`#!/bin/sh
case "$0" in
	*/.agent-deck.update.*) printf 'Agent Deck v3.1.5\n' ;;
	*) kill -TERM "$PPID"; sleep 1; printf 'Agent Deck v3.1.5\n' ;;
esac
`)
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	err := r.DeployBinary(context.Background(), candidate, target, "3.1.5")
	if err == nil {
		t.Fatal("DeployBinary unexpectedly succeeded after transaction termination")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBinary) {
		t.Fatalf("terminated provisional publish was not rolled back")
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_UsesUniqueStagingPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	invocations := filepath.Join(dir, "invocations")
	candidate := []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$0\" >> %s\nprintf 'Agent Deck v4.5.6\\n'\n", shellQuote(invocations)))
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	for i := 0; i < 2; i++ {
		if err := r.DeployBinary(context.Background(), candidate, target, "4.5.6"); err != nil {
			t.Fatalf("DeployBinary attempt %d: %v", i+1, err)
		}
	}
	data, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatal(err)
	}
	var staged []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, "/.agent-deck.update.") {
			staged = append(staged, line)
		}
	}
	if len(staged) != 2 || staged[0] == staged[1] {
		t.Fatalf("staged paths = %q, want two unique paths", staged)
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RecoversOrphanedRemoteLock(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	lockDir := filepath.Join(dir, ".agent-deck.update.lock")
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "pid"), []byte("999999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := []byte("#!/bin/sh\nprintf 'Agent Deck v4.5.7\\n'\n")
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	if err := r.DeployBinary(context.Background(), candidate, target, "4.5.7"); err != nil {
		t.Fatalf("DeployBinary did not recover orphaned lock: %v", err)
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RecoversNonemptyPrepublishRemoteLock(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	lockDir := filepath.Join(dir, ".agent-deck.update.lock")
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "pid"), []byte("999999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanCandidate := filepath.Join(dir, ".agent-deck.update.orphan")
	orphanBackup := filepath.Join(dir, ".agent-deck.backup.orphan")
	for _, item := range []struct {
		path string
		data []byte
	}{
		{orphanCandidate, []byte("partial-candidate")},
		{orphanBackup, []byte("partial-backup")},
		{filepath.Join(lockDir, "tmp-path"), []byte(orphanCandidate + "\n")},
		{filepath.Join(lockDir, "backup-path"), []byte(orphanBackup + "\n")},
		{filepath.Join(lockDir, "version-output"), []byte("partial-output")},
		{filepath.Join(lockDir, "version-error"), []byte("partial-error")},
	} {
		if err := os.WriteFile(item.path, item.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	candidate := []byte("#!/bin/sh\nprintf 'Agent Deck v4.5.8\\n'\n")
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	if err := r.DeployBinary(context.Background(), candidate, target, "4.5.8"); err != nil {
		t.Fatalf("DeployBinary did not recover nonempty prepublish lock: %v", err)
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RecoversJournalAfterSIGKILLDuringPublish(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("previous-known-good-binary\n")
	if err := os.WriteFile(target, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	killer := []byte(`#!/bin/sh
case "$0" in
	*/.agent-deck.update.*) printf 'Agent Deck v6.1.0\n' ;;
	*) kill -KILL "$PPID"; sleep 0.1; exit 99 ;;
esac
`)
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	if err := r.DeployBinary(context.Background(), killer, target, "6.1.0"); err == nil {
		t.Fatal("DeployBinary unexpectedly survived SIGKILL during provisional publication")
	}
	lockDir := filepath.Join(dir, ".agent-deck.update.lock")
	for _, name := range []string{"publishing", "had-target", "backup-path"} {
		if _, err := os.Stat(filepath.Join(lockDir, name)); err != nil {
			t.Fatalf("crash journal missing %s: %v", name, err)
		}
	}

	// The next deployment first recovers the stale journal. Make its own
	// candidate fail preflight so the restored old target remains observable.
	wrong := []byte("#!/bin/sh\nprintf 'Agent Deck v9.9.9\\n'\n")
	err := r.DeployBinary(context.Background(), wrong, target, "6.1.1")
	if err == nil || !strings.Contains(err.Error(), "staged candidate reports an unexpected version") {
		t.Fatalf("recovery probe error = %v, want staged-version mismatch", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBinary) {
		t.Fatal("stale provisional publication was not rolled back from its journal")
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RecoversSIGKILLJournalWithoutPreviousTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	killer := []byte(`#!/bin/sh
case "$0" in
	*/.agent-deck.update.*) printf 'Agent Deck v6.1.2\n' ;;
	*) kill -KILL "$PPID"; sleep 0.1; exit 99 ;;
esac
`)
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	if err := r.DeployBinary(context.Background(), killer, target, "6.1.2"); err == nil {
		t.Fatal("DeployBinary unexpectedly survived SIGKILL during first publication")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("provisional target was not published before crash: %v", err)
	}

	wrong := []byte("#!/bin/sh\nprintf 'Agent Deck v9.9.9\\n'\n")
	err := r.DeployBinary(context.Background(), wrong, target, "6.1.3")
	if err == nil || !strings.Contains(err.Error(), "staged candidate reports an unexpected version") {
		t.Fatalf("recovery probe error = %v, want staged-version mismatch", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("recovery did not remove provisional first-install target: %v", err)
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RecoversCrashAfterCommitWithoutRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("previous-known-good-binary\n")
	if err := os.WriteFile(target, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := []byte("#!/bin/sh\nprintf 'Agent Deck v6.1.4\\n'\n")
	invocation := 0
	r := &SSHRunner{remoteExecFn: func(ctx context.Context, remoteCmd string, stdin []byte) ([]byte, error) {
		if invocation == 0 {
			remoteCmd = strings.Replace(remoteCmd, "ad_committed=1\n", "ad_committed=1\nkill -KILL \"$$\"\n", 1)
		}
		invocation++
		return localShellRemoteExec(ctx, remoteCmd, stdin)
	}}
	if err := r.DeployBinary(context.Background(), candidate, target, "6.1.4"); err == nil {
		t.Fatal("DeployBinary unexpectedly survived injected crash after commit")
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-deck.update.lock", "committed")); err != nil {
		t.Fatalf("committed crash journal missing: %v", err)
	}

	wrong := []byte("#!/bin/sh\nprintf 'Agent Deck v9.9.9\\n'\n")
	err := r.DeployBinary(context.Background(), wrong, target, "6.1.5")
	if err == nil || !strings.Contains(err.Error(), "staged candidate reports an unexpected version") {
		t.Fatalf("recovery probe error = %v, want staged-version mismatch", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, candidate) {
		t.Fatal("committed target was rolled back during stale-journal cleanup")
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_PreservesJournalWhenRecoveryTargetBecomesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	if err := os.WriteFile(target, []byte("old-binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	killer := []byte(`#!/bin/sh
case "$0" in
	*/.agent-deck.update.*) printf 'Agent Deck v6.1.6\n' ;;
	*) kill -KILL "$PPID"; sleep 0.1; exit 99 ;;
esac
`)
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	if err := r.DeployBinary(context.Background(), killer, target, "6.1.6"); err == nil {
		t.Fatal("DeployBinary unexpectedly survived SIGKILL during publication")
	}
	lockDir := filepath.Join(dir, ".agent-deck.update.lock")
	backupPathBytes, err := os.ReadFile(filepath.Join(lockDir, "backup-path"))
	if err != nil {
		t.Fatal(err)
	}
	backupPath := strings.TrimSpace(string(backupPathBytes))
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("external\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err = r.DeployBinary(context.Background(), []byte("candidate"), target, "6.1.7")
	if err == nil || !strings.Contains(err.Error(), backupPath) || !strings.Contains(err.Error(), "refusing unsafe rollback") {
		t.Fatalf("recovery error = %v, want preserved backup %s", err, backupPath)
	}
	if info, statErr := os.Lstat(target); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("recovery flattened replacement symlink: info=%v err=%v", info, statErr)
	}
	if got, readErr := os.ReadFile(outside); readErr != nil || string(got) != "external\n" {
		t.Fatalf("recovery changed symlink referent: data=%q err=%v", got, readErr)
	}
	if _, statErr := os.Stat(backupPath); statErr != nil {
		t.Fatalf("recovery did not preserve rollback backup: %v", statErr)
	}
	if _, statErr := os.Stat(lockDir); statErr != nil {
		t.Fatalf("recovery did not preserve journal: %v", statErr)
	}
}

func TestDeployBinary_PreservesJournalWhenFirstInstallRollbackRemovalFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	candidate := []byte(`#!/bin/sh
case "$0" in
	*/.agent-deck.update.*) printf 'Agent Deck v6.1.8\n' ;;
	*) printf 'broken after publish\n' >&2; exit 42 ;;
esac
`)
	r := &SSHRunner{remoteExecFn: func(ctx context.Context, remoteCmd string, stdin []byte) ([]byte, error) {
		remoteCmd = strings.Replace(remoteCmd, `if rm -f "$ad_target" 2>/dev/null; then`, `if false; then`, 1)
		return localShellRemoteExec(ctx, remoteCmd, stdin)
	}}
	err := r.DeployBinary(context.Background(), candidate, target, "6.1.8")
	if err == nil || !strings.Contains(err.Error(), "preserving recovery journal") {
		t.Fatalf("DeployBinary error = %v, want preserved-journal rollback failure", err)
	}
	lockDir := filepath.Join(dir, ".agent-deck.update.lock")
	for _, path := range []string{target, lockDir, filepath.Join(lockDir, "publishing")} {
		if _, statErr := os.Lstat(path); statErr != nil {
			t.Fatalf("rollback failure did not preserve %s: %v", path, statErr)
		}
	}
}

func TestDeployBinary_RecoversJournalAgainstItsPersistedTarget(t *testing.T) {
	dir := t.TempDir()
	targetA := filepath.Join(dir, "agent-deck-a")
	targetB := filepath.Join(dir, "agent-deck-b")
	oldA := []byte("previous-a\n")
	oldB := []byte("previous-b\n")
	if err := os.WriteFile(targetA, oldA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetB, oldB, 0o755); err != nil {
		t.Fatal(err)
	}
	killer := []byte(`#!/bin/sh
case "$0" in
	*/.agent-deck.update.*) printf 'Agent Deck v6.2.0\n' ;;
	*) kill -KILL "$PPID"; sleep 0.1; exit 99 ;;
esac
`)
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	if err := r.DeployBinary(context.Background(), killer, targetA, "6.2.0"); err == nil {
		t.Fatal("DeployBinary unexpectedly survived SIGKILL during target A publication")
	}

	// A later transaction for another filename shares the directory lock. It
	// must recover A from the journal, never apply A's backup to B.
	wrong := []byte("#!/bin/sh\nprintf 'Agent Deck v9.9.9\\n'\n")
	err := r.DeployBinary(context.Background(), wrong, targetB, "6.2.1")
	if err == nil || !strings.Contains(err.Error(), "staged candidate reports an unexpected version") {
		t.Fatalf("recovery probe error = %v, want staged-version mismatch", err)
	}
	for path, want := range map[string][]byte{targetA: oldA, targetB: oldB} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("persisted-target recovery changed %s: got %q want %q", path, got, want)
		}
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_DoesNotStealOldLockFromLiveOwner(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("live-owner-binary\n")
	if err := os.WriteFile(target, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	lockDir := filepath.Join(dir, ".agent-deck.update.lock")
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	candidate := []byte("#!/bin/sh\nprintf 'Agent Deck v6.3.0\\n'\n")
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	if err := r.DeployBinary(ctx, candidate, target, "6.3.0"); err == nil {
		t.Fatal("DeployBinary stole an old lock whose owner PID is still live")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBinary) {
		t.Fatal("old-but-live lock did not protect the target")
	}
	if _, err := os.Stat(lockDir); err != nil {
		t.Fatalf("live owner's lock was removed: %v", err)
	}
}

func TestDeployBinary_RefusesSymlinkLockDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	outside := t.TempDir()
	marker := filepath.Join(outside, "do-not-touch")
	if err := os.WriteFile(marker, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".agent-deck.update.lock")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	candidate := []byte("#!/bin/sh\nprintf 'Agent Deck v6.3.1\\n'\n")
	r := &SSHRunner{remoteExecFn: localShellRemoteExec}
	err := r.DeployBinary(context.Background(), candidate, target, "6.3.1")
	if err == nil || !strings.Contains(err.Error(), "symbolic-link update lock") {
		t.Fatalf("DeployBinary error = %v, want symlink-lock refusal", err)
	}
	if got, readErr := os.ReadFile(marker); readErr != nil || string(got) != "safe" {
		t.Fatalf("symlink lock target changed: data=%q err=%v", got, readErr)
	}
}

func TestDeployBinary_SerializesConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-deck")
	logPath := filepath.Join(dir, "candidate.log")
	debugPath := filepath.Join(dir, "deploy.debug")
	makeCandidate := func(label string) []byte {
		return []byte(fmt.Sprintf(`#!/bin/sh
printf '%s-start\n' >> %s
sleep 0.2
printf '%s-end\n' >> %s
printf 'Agent Deck v5.0.0\n'
`, label, shellQuote(logPath), label, shellQuote(logPath)))
	}

	var active atomic.Int32
	var maxActive atomic.Int32
	r := &SSHRunner{remoteExecFn: func(ctx context.Context, remoteCmd string, stdin []byte) ([]byte, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		debug := shellQuote(debugPath)
		remoteCmd = strings.Replace(remoteCmd, "ad_lock_held=0\n", "ad_lock_held=0\nad_me=$$\nprintf 'start %s\\n' \"$ad_me\" >> "+debug+"\n", 1)
		remoteCmd = strings.Replace(remoteCmd, "ad_cleanup() {\n", "ad_cleanup() {\nprintf 'cleanup %s held=%s tmp=%s\\n' \"$ad_me\" \"$ad_lock_held\" \"$ad_tmp\" >> "+debug+"\n", 1)
		remoteCmd = strings.Replace(remoteCmd, "while ! mkdir \"$ad_lock\" 2>/dev/null; do\n", "while ! mkdir \"$ad_lock\" 2>/dev/null; do\nprintf 'wait %s\\n' \"$ad_me\" >> "+debug+"\n", 1)
		remoteCmd = strings.Replace(remoteCmd, "ad_lock_held=1\n", "ad_lock_held=1\nprintf 'acquired %s\\n' \"$ad_me\" >> "+debug+"\n", 1)
		remoteCmd = strings.Replace(remoteCmd, "ad_candidate_status=0\n", "printf 'candidate %s tmp=%s\\n' \"$ad_me\" \"$ad_tmp\" >> "+debug+"\nad_candidate_status=0\n", 1)
		remoteCmd = strings.Replace(remoteCmd, "if ! mv -f \"$ad_tmp\" \"$ad_target\"; then\n", "printf 'publish %s tmp=%s\\n' \"$ad_me\" \"$ad_tmp\" >> "+debug+"\nif ! mv -f \"$ad_tmp\" \"$ad_target\"; then\n", 1)
		return localShellRemoteExec(ctx, remoteCmd, stdin)
	}}
	errs := make(chan error, 2)
	for _, label := range []string{"A", "B"} {
		candidate := makeCandidate(label)
		go func(data []byte) { errs <- r.DeployBinary(context.Background(), data, target, "5.0.0") }(candidate)
	}
	var deployErrs []error
	for range 2 {
		if err := <-errs; err != nil {
			deployErrs = append(deployErrs, err)
		}
	}
	if debug, debugErr := os.ReadFile(debugPath); debugErr == nil {
		t.Logf("deploy debug:\n%s", debug)
	}
	if len(deployErrs) > 0 {
		t.Fatalf("concurrent DeployBinary errors: %v", deployErrs)
	}
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("same-process remote deploys ran concurrently: max active = %d", got)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 8 {
		t.Fatalf("candidate log = %q, want eight entries", lines)
	}
	labels := make([]string, len(lines))
	for i, line := range lines {
		labels[i] = strings.SplitN(line, "-", 2)[0]
	}
	got := strings.Join(labels, "")
	if got != "AAAABBBB" && got != "BBBBAAAA" {
		t.Fatalf("concurrent transactions interleaved: labels=%s lines=%q", got, lines)
	}
	assertNoDeployArtifacts(t, dir)

	cmd := buildRemoteDeployCommand(target, "5.0.0")
	for _, want := range []string{`umask 077`, `mkdir "$ad_lock"`, `mkdir "$ad_recovering"`, `"$ad_lock/target-path"`, `kill -0 "$ad_holder"`, `ps -p "$ad_holder"`, `-mmin +3`, `ad_attempt=$((ad_attempt + 1))`, `"$ad_attempt" -ge 30`, `trap 'ad_cleanup' 0`} {
		if !strings.Contains(cmd, want) {
			t.Errorf("portable bounded lock command missing %q", want)
		}
	}
	if strings.Contains(cmd, "flock") {
		t.Errorf("deploy command must not rely on flock")
	}
}

func TestDeployBinary_RejectsInvalidExpectedVersionBeforeRemoteWrite(t *testing.T) {
	called := false
	r := &SSHRunner{remoteExecFn: func(context.Context, string, []byte) ([]byte, error) {
		called = true
		return nil, nil
	}}
	err := r.DeployBinary(context.Background(), []byte("candidate"), "/tmp/agent-deck", "2.0.0; touch /tmp/pwned")
	if err == nil || !strings.Contains(err.Error(), "invalid expected") {
		t.Fatalf("DeployBinary error = %v, want invalid-version error", err)
	}
	if called {
		t.Fatal("invalid expected version reached remoteExecFn")
	}
}

func TestDeployBinary_SupportsBareRelativeTarget(t *testing.T) {
	dir := t.TempDir()
	candidate := []byte("#!/bin/sh\nprintf 'Agent Deck v7.0.0\\n'\n")
	r := &SSHRunner{remoteExecFn: func(ctx context.Context, remoteCmd string, stdin []byte) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", remoteCmd)
		cmd.Dir = dir
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return out, nil
	}}
	if err := r.DeployBinary(context.Background(), candidate, "agent-deck", "7.0.0"); err != nil {
		t.Fatalf("DeployBinary bare relative path: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "agent-deck"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, candidate) {
		t.Fatal("bare relative target did not receive candidate")
	}
	assertNoDeployArtifacts(t, dir)
}

func TestDeployBinary_RejectsNewlineInRemotePathBeforeRemoteWrite(t *testing.T) {
	called := false
	r := &SSHRunner{remoteExecFn: func(context.Context, string, []byte) ([]byte, error) {
		called = true
		return nil, nil
	}}
	err := r.DeployBinary(context.Background(), []byte("candidate"), "/tmp/agent-deck\nother", "7.0.0")
	if err == nil || !strings.Contains(err.Error(), "invalid remote agent-deck path") {
		t.Fatalf("DeployBinary error = %v, want invalid-path error", err)
	}
	if called {
		t.Fatal("invalid remote path reached remoteExecFn")
	}
}

func TestNormalizeExpectedRemoteVersionAcceptsSemverMetadata(t *testing.T) {
	got, err := normalizeExpectedRemoteVersion("V2.0.0-rc.1+linux.amd64")
	if err != nil {
		t.Fatalf("normalizeExpectedRemoteVersion: %v", err)
	}
	if got != "2.0.0-rc.1+linux.amd64" {
		t.Fatalf("normalized version = %q", got)
	}
}
