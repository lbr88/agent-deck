package session

import (
	"os"
	"path/filepath"
	"strings"
)

// Hook session anchor sidecar:
// Hook payloads can have empty session_id for some events, which may otherwise
// lose restart-critical session binding. We persist the last non-empty ID in a
// .sid sidecar file and only use it as a read-time fallback. Hook JSON semantics
// remain unchanged for backward compatibility.

// HookSessionAnchorPath returns the sidecar file path used to persist the
// last known non-empty hook session ID for one instance.
func HookSessionAnchorPath(instanceID string) string {
	return filepath.Join(GetHooksDir(), instanceID+".sid")
}

func validHookSessionAnchorInstanceID(instanceID string) bool {
	return instanceID != "" && filepath.Base(instanceID) == instanceID && instanceID != "." && instanceID != ".."
}

// writeInternalStateFileAtomic replaces path itself (including a destination
// symlink) with a regular file. Agent Deck owns these files; unlike user config,
// following a destination symlink would let the sidecar overwrite its target.
func writeInternalStateFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// ReadHookSessionAnchor reads the persisted hook session ID sidecar.
func ReadHookSessionAnchor(instanceID string) string {
	instanceID = strings.TrimSpace(instanceID)
	if !validHookSessionAnchorInstanceID(instanceID) {
		return ""
	}
	data, err := readStatusFileNoFollow(HookSessionAnchorPath(instanceID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteHookSessionAnchor persists the latest non-empty hook session ID.
func WriteHookSessionAnchor(instanceID, sessionID string) {
	instanceID = strings.TrimSpace(instanceID)
	sessionID = strings.TrimSpace(sessionID)
	if !validHookSessionAnchorInstanceID(instanceID) || sessionID == "" {
		return
	}
	hooksDir := GetHooksDir()
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return
	}
	// Use a unique sibling temp file and rename it onto the internal-state path.
	// Renaming the directory entry deliberately replaces a hostile destination
	// symlink instead of following it. Multiple writers also cannot steal a
	// shared ".tmp" file from each other.
	_ = writeInternalStateFileAtomic(HookSessionAnchorPath(instanceID), []byte(sessionID), 0o600)
}

// ClearHookSessionAnchor removes the persisted hook session ID sidecar.
func ClearHookSessionAnchor(instanceID string) {
	instanceID = strings.TrimSpace(instanceID)
	if !validHookSessionAnchorInstanceID(instanceID) {
		return
	}
	_ = os.Remove(HookSessionAnchorPath(instanceID))
}
