package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireHookSessionLockSerializesSameInstance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	releaseFirst, err := AcquireHookSessionLock("lock-test")
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer releaseFirst()

	acquiredSecond := make(chan func(), 1)
	errSecond := make(chan error, 1)
	go func() {
		release, err := AcquireHookSessionLock("lock-test")
		if err != nil {
			errSecond <- err
			return
		}
		acquiredSecond <- release
	}()

	select {
	case err := <-errSecond:
		t.Fatalf("second acquire failed: %v", err)
	case release := <-acquiredSecond:
		release()
		t.Fatal("second acquire completed before first lock was released")
	case <-time.After(100 * time.Millisecond):
		// Expected: the second caller is blocked behind the first.
	}

	releaseFirst()
	select {
	case err := <-errSecond:
		t.Fatalf("second acquire failed after release: %v", err)
	case release := <-acquiredSecond:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire remained blocked after first lock was released")
	}
}

func TestAcquireHookSessionLockRejectsUnsafeInstanceID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if release, err := AcquireHookSessionLock("../other-instance"); err == nil {
		release()
		t.Fatal("expected unsafe instance ID to be rejected")
	}
}

func TestCodexInternalSidecarWritesReplaceSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)
	const instanceID = "sidecar-symlink-test"
	hooksDir := GetHooksDir()
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}

	target := filepath.Join(home, "sentinel")
	if err := os.WriteFile(target, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	assertSentinel := func(label string) {
		t.Helper()
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "do-not-touch" {
			t.Fatalf("%s followed sidecar symlink: data=%q err=%v", label, data, err)
		}
	}
	assertRegular := func(path, label string) {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("lstat %s: %v", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s remained a symlink", label)
		}
	}

	anchorPath := HookSessionAnchorPath(instanceID)
	if err := os.Symlink(target, anchorPath); err != nil {
		t.Fatalf("symlink anchor: %v", err)
	}
	if got := ReadHookSessionAnchor(instanceID); got != "" {
		t.Fatalf("anchor reader followed symlink: %q", got)
	}
	const sessionID = "11111111-aaaa-4aaa-8aaa-111111111111"
	WriteHookSessionAnchor(instanceID, sessionID)
	assertSentinel("anchor write")
	assertRegular(anchorPath, "anchor")

	migrationPath := codexSubagentMigrationStatePath(instanceID)
	if err := os.Symlink(target, migrationPath); err != nil {
		t.Fatalf("symlink migration: %v", err)
	}
	if _, ok := readCodexSubagentMigrationState(instanceID); ok {
		t.Fatal("migration reader followed symlink")
	}
	if err := writeCodexSubagentMigrationState(instanceID, codexSubagentMigrationState{
		SourceID: sessionID,
		Started:  time.Now(),
	}); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	assertSentinel("migration write")
	assertRegular(migrationPath, "migration")

	floorPath := codexBindingFloorStatePath(instanceID)
	if err := os.Symlink(target, floorPath); err != nil {
		t.Fatalf("symlink floor: %v", err)
	}
	if _, ok := readCodexBindingFloorState(instanceID); ok {
		t.Fatal("floor reader followed symlink")
	}
	writeCodexBindingFloorState(instanceID, sessionID, time.Now(), 1)
	assertSentinel("floor write")
	assertRegular(floorPath, "floor")
}
