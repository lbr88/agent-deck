package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const selfUpdateValidationTimeout = 15 * time.Second

// These seams keep failure handling deterministic in tests. Production callers
// always use the platform implementations below.
var (
	validateSelfUpdateCandidate = runSelfUpdateCandidateVersion
	replaceSelfUpdateFile       = atomicReplaceSelfUpdateFile
	syncSelfUpdateDir           = syncSelfUpdateDirectory
)

// selfUpdateMutexes complements the OS lock. In particular, it gives
// deterministic serialization to goroutines in one process on platforms whose
// advisory locks are scoped more broadly than an individual file descriptor.
var selfUpdateMutexes sync.Map // map[absolute executable path]*sync.Mutex

type selfUpdateLock struct {
	mu   *sync.Mutex
	file *os.File
}

func acquireSelfUpdateLock(execPath string) (*selfUpdateLock, error) {
	key, err := filepath.Abs(filepath.Clean(execPath))
	if err != nil {
		return nil, fmt.Errorf("resolve executable path for update lock: %w", err)
	}

	muValue, _ := selfUpdateMutexes.LoadOrStore(key, &sync.Mutex{})
	mu := muValue.(*sync.Mutex)
	mu.Lock()

	lockPath := key + ".update.lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		mu.Unlock()
		return nil, fmt.Errorf("open self-update lock %s: %w", lockPath, err)
	}
	if err := lockSelfUpdateFile(lockFile); err != nil {
		_ = lockFile.Close()
		mu.Unlock()
		return nil, fmt.Errorf("lock self-update target %s: %w", key, err)
	}

	return &selfUpdateLock{mu: mu, file: lockFile}, nil
}

func (l *selfUpdateLock) release() {
	if l == nil {
		return
	}
	if l.file != nil {
		_ = unlockSelfUpdateFile(l.file)
		_ = l.file.Close()
	}
	if l.mu != nil {
		l.mu.Unlock()
	}
}

// installSelfUpdateBinary stages a candidate beside the installed executable,
// proves that it can start, then atomically renames it over the current path.
// A stable advisory lock serializes concurrent agent-deck processes.
func installSelfUpdateBinary(execPath string, binaryData []byte) error {
	absExecPath, err := filepath.Abs(filepath.Clean(execPath))
	if err != nil {
		return fmt.Errorf("resolve installed binary path: %w", err)
	}

	lock, err := acquireSelfUpdateLock(absExecPath)
	if err != nil {
		return err
	}
	defer lock.release()

	dir := filepath.Dir(absExecPath)
	base := filepath.Base(absExecPath)
	candidatePath, err := writeSelfUpdateCandidate(dir, base, binaryData)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(candidatePath) }()

	// Do not touch the installed binary until the candidate has successfully
	// executed its cheapest supported command. This catches truncated archives,
	// wrong-architecture binaries, missing loaders, and non-executable files.
	if err := validateSelfUpdateCandidate(candidatePath); err != nil {
		return fmt.Errorf("validate new binary: %w", err)
	}

	backupPath, err := copySelfUpdateBackup(absExecPath, dir, base)
	if err != nil {
		return err
	}
	removeBackup := true
	defer func() {
		if removeBackup {
			_ = os.Remove(backupPath)
		}
	}()

	// Persist both staged directory entries before publishing the candidate.
	// syncSelfUpdateDirectory treats filesystems without directory-fsync support
	// as a best-effort success.
	if err := syncSelfUpdateDir(dir); err != nil {
		return fmt.Errorf("sync self-update staging directory: %w", err)
	}

	if err := replaceSelfUpdateFile(candidatePath, absExecPath); err != nil {
		failure, restored := selfUpdateInstallFailure(absExecPath, backupPath, dir, err)
		removeBackup = restored
		return failure
	}

	// If durability of the published rename cannot be established, restore the
	// old executable atomically rather than returning an error with an uncertain
	// on-disk version.
	if err := syncSelfUpdateDir(dir); err != nil {
		failure, restored := selfUpdateInstallFailure(absExecPath, backupPath, dir,
			fmt.Errorf("sync installed binary directory: %w", err))
		removeBackup = restored
		return failure
	}

	// The update is committed. Backup cleanup is intentionally best-effort: a
	// cleanup failure must not report that a successfully installed binary needs
	// to be retried (which could install an older concurrent candidate later).
	_ = os.Remove(backupPath)
	_ = syncSelfUpdateDir(dir)

	// Invalidate update cache so the banner dismisses in any running TUI.
	InvalidateCache()

	fmt.Println("✓ Update complete!")
	return nil
}

func writeSelfUpdateCandidate(dir, base string, binaryData []byte) (string, error) {
	f, err := os.CreateTemp(dir, "."+base+".update-*")
	if err != nil {
		return "", fmt.Errorf("create staged binary beside %s: %w", base, err)
	}
	path := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()

	if err := f.Chmod(0o755); err != nil {
		return "", fmt.Errorf("make staged binary executable: %w", err)
	}
	if _, err := f.Write(binaryData); err != nil {
		return "", fmt.Errorf("write staged binary: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync staged binary: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close staged binary: %w", err)
	}
	ok = true
	return path, nil
}

func copySelfUpdateBackup(execPath, dir, base string) (string, error) {
	source, err := os.Open(execPath)
	if err != nil {
		return "", fmt.Errorf("open installed binary for rollback backup: %w", err)
	}
	defer source.Close()

	info, err := source.Stat()
	if err != nil {
		return "", fmt.Errorf("stat installed binary for rollback backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("installed binary is not a regular file: %s", execPath)
	}

	backup, err := os.CreateTemp(dir, "."+base+".rollback-*")
	if err != nil {
		return "", fmt.Errorf("create rollback backup: %w", err)
	}
	backupPath := backup.Name()
	ok := false
	defer func() {
		if !ok {
			_ = backup.Close()
			_ = os.Remove(backupPath)
		}
	}()

	backupMode := info.Mode().Perm() | (info.Mode() & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky))
	if _, err := io.Copy(backup, source); err != nil {
		return "", fmt.Errorf("copy rollback backup: %w", err)
	}
	if err := backup.Chmod(backupMode); err != nil {
		return "", fmt.Errorf("set rollback backup permissions: %w", err)
	}
	if err := backup.Sync(); err != nil {
		return "", fmt.Errorf("sync rollback backup: %w", err)
	}
	if err := backup.Close(); err != nil {
		return "", fmt.Errorf("close rollback backup: %w", err)
	}
	ok = true
	return backupPath, nil
}

func selfUpdateInstallFailure(execPath, backupPath, dir string, installErr error) (error, bool) {
	if rollbackErr := replaceSelfUpdateFile(backupPath, execPath); rollbackErr != nil {
		return fmt.Errorf("failed to install new binary: %w; rollback failed: %v; previous binary preserved at %s", installErr, rollbackErr, backupPath), false
	}
	if syncErr := syncSelfUpdateDir(dir); syncErr != nil {
		return fmt.Errorf("failed to install new binary: %w; rollback directory sync failed: %v", installErr, syncErr), true
	}
	return fmt.Errorf("failed to install new binary (previous binary restored): %w", installErr), true
}

func runSelfUpdateCandidateVersion(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateValidationTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Env = append(os.Environ(), SkipUpdateCheckEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return fmt.Errorf("candidate `version` timed out after %s: %w", selfUpdateValidationTimeout, ctx.Err())
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 4096 {
			detail = detail[:4096] + "..."
		}
		if detail != "" {
			return fmt.Errorf("candidate `version` failed: %w: %s", err, detail)
		}
		return fmt.Errorf("candidate `version` failed: %w", err)
	}
	if err := validateAgentDeckVersionOutput(output); err != nil {
		return fmt.Errorf("candidate `version` output is not Agent Deck: %w", err)
	}
	return nil
}
