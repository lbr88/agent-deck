package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// hookSessionLockMu provides the in-process half of hook-session locking.
// flock serializes separate Agent Deck processes; the keyed mutex also makes
// the ordering explicit for goroutines that independently open the lock file.
var hookSessionLockMu sync.Map // map[string]*sync.Mutex

// AcquireHookSessionLock serializes hook anchor and status mutations for one
// Agent Deck instance. The returned release function must be called exactly
// once; it is safe to defer and tolerates duplicate calls.
//
// The stable lock file is intentionally retained. Codex's parent thread,
// approval guardians, delayed old-root notify processes, and Agent Deck's
// runtime can all inherit the same instance ID and must agree on one ordering
// before validating or updating the sticky session binding.
func AcquireHookSessionLock(instanceID string) (release func(), err error) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" || filepath.Base(instanceID) != instanceID || instanceID == "." || instanceID == ".." {
		return nil, fmt.Errorf("invalid hook session lock instance ID %q", instanceID)
	}

	hooksDir := GetHooksDir()
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return nil, fmt.Errorf("create hooks dir for session lock: %w", err)
	}
	lockPath := filepath.Join(hooksDir, "."+instanceID+".hook.lock")

	muValue, _ := hookSessionLockMu.LoadOrStore(lockPath, &sync.Mutex{})
	mu := muValue.(*sync.Mutex)
	mu.Lock()

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		mu.Unlock()
		return nil, fmt.Errorf("open hook session lock %q: %w", lockPath, err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		mu.Unlock()
		return nil, fmt.Errorf("flock hook session lock %q: %w", lockPath, err)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			_ = lockFile.Close()
			mu.Unlock()
		})
	}, nil
}
