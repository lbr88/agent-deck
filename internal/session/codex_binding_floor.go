package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type codexBindingFloorState struct {
	SessionID  string    `json:"session_id,omitempty"`
	DetectedAt time.Time `json:"detected_at"`
	Revision   int64     `json:"revision,omitempty"`
}

func codexBindingFloorStatePath(instanceID string) string {
	return filepath.Join(GetHooksDir(), strings.TrimSpace(instanceID)+".codex-binding")
}

func writeCodexBindingFloorState(instanceID, sessionID string, detectedAt time.Time, revision int64) {
	instanceID = strings.TrimSpace(instanceID)
	if !validHookSessionAnchorInstanceID(instanceID) || detectedAt.IsZero() {
		return
	}
	state := codexBindingFloorState{
		SessionID:  strings.ToLower(strings.TrimSpace(sessionID)),
		DetectedAt: detectedAt,
		Revision:   max(revision, int64(0)),
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = writeInternalStateFileAtomic(codexBindingFloorStatePath(instanceID), data, 0o600)
}

func ensureCodexBindingFloorState(instanceID, sessionID string, detectedAt time.Time, revision int64) {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	if current, ok := readCodexBindingFloorState(instanceID); ok && current.SessionID == sessionID {
		requestedRevision := max(revision, int64(0))
		if current.Revision > requestedRevision ||
			(current.Revision == requestedRevision && !detectedAt.After(current.DetectedAt)) {
			return
		}
		if current.DetectedAt.After(detectedAt) {
			detectedAt = current.DetectedAt
		}
	}
	writeCodexBindingFloorState(instanceID, sessionID, detectedAt, revision)
}

func readCodexBindingFloorState(instanceID string) (codexBindingFloorState, bool) {
	instanceID = strings.TrimSpace(instanceID)
	if !validHookSessionAnchorInstanceID(instanceID) {
		return codexBindingFloorState{}, false
	}
	data, err := readStatusFileNoFollow(codexBindingFloorStatePath(instanceID))
	if err != nil || len(data) > 16*1024 {
		return codexBindingFloorState{}, false
	}
	var state codexBindingFloorState
	if json.Unmarshal(data, &state) != nil || state.DetectedAt.IsZero() {
		return codexBindingFloorState{}, false
	}
	state.SessionID = strings.ToLower(strings.TrimSpace(state.SessionID))
	return state, true
}

func clearCodexBindingFloorState(instanceID string) {
	instanceID = strings.TrimSpace(instanceID)
	if !validHookSessionAnchorInstanceID(instanceID) {
		return
	}
	_ = os.Remove(codexBindingFloorStatePath(instanceID))
}

// CodexHookCandidateRejectedByBindingFloor lets the notify subprocess apply
// the same immutable creation-time floor as the runtime before it updates the
// sticky anchor/status files. It is called while the shared hook lock is held.
func CodexHookCandidateRejectedByBindingFloor(instanceID, candidateID, codexHome string) bool {
	state, ok := readCodexBindingFloorState(instanceID)
	if !ok {
		return false
	}
	candidateID = strings.ToLower(strings.TrimSpace(candidateID))
	if candidateID == "" || candidateID == state.SessionID {
		return false
	}
	if CodexSessionRolloutExists(codexHome, candidateID) &&
		!IsCodexTopLevelSession(codexHome, candidateID) {
		return true
	}
	createdAt := codexRolloutCreatedAt(codexHome, candidateID)
	return createdAt.IsZero() || createdAt.Before(state.DetectedAt)
}
