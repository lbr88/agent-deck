package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type codexSubagentMigrationState struct {
	SourceID  string    `json:"source_id"`
	OldRoot   string    `json:"old_root_id,omitempty"`
	Started   time.Time `json:"started_at"`
	TargetID  string    `json:"target_id,omitempty"`
	Completed time.Time `json:"completed_at,omitempty"`
}

func codexSubagentMigrationStatePath(instanceID string) string {
	return filepath.Join(GetHooksDir(), strings.TrimSpace(instanceID)+".codex-migration")
}

func writeCodexSubagentMigrationState(instanceID string, state codexSubagentMigrationState) error {
	instanceID = strings.TrimSpace(instanceID)
	if !validHookSessionAnchorInstanceID(instanceID) || !isCodexSessionUUID(state.SourceID) || state.Started.IsZero() {
		return nil
	}
	state.SourceID = strings.ToLower(strings.TrimSpace(state.SourceID))
	state.OldRoot = strings.ToLower(strings.TrimSpace(state.OldRoot))
	state.TargetID = strings.ToLower(strings.TrimSpace(state.TargetID))
	if (state.TargetID == "") != state.Completed.IsZero() {
		return nil
	}
	if state.TargetID != "" && (!isCodexSessionUUID(state.TargetID) || state.TargetID == state.SourceID || state.TargetID == state.OldRoot) {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeInternalStateFileAtomic(codexSubagentMigrationStatePath(instanceID), data, 0o600)
}

func readCodexSubagentMigrationState(instanceID string) (codexSubagentMigrationState, bool) {
	instanceID = strings.TrimSpace(instanceID)
	if !validHookSessionAnchorInstanceID(instanceID) {
		return codexSubagentMigrationState{}, false
	}
	data, err := readStatusFileNoFollow(codexSubagentMigrationStatePath(instanceID))
	if err != nil || len(data) > 16*1024 {
		return codexSubagentMigrationState{}, false
	}
	var state codexSubagentMigrationState
	if json.Unmarshal(data, &state) != nil || !isCodexSessionUUID(state.SourceID) || state.Started.IsZero() {
		return codexSubagentMigrationState{}, false
	}
	state.SourceID = strings.ToLower(strings.TrimSpace(state.SourceID))
	state.OldRoot = strings.ToLower(strings.TrimSpace(state.OldRoot))
	state.TargetID = strings.ToLower(strings.TrimSpace(state.TargetID))
	if (state.TargetID == "") != state.Completed.IsZero() {
		return codexSubagentMigrationState{}, false
	}
	if state.TargetID != "" && (!isCodexSessionUUID(state.TargetID) || state.TargetID == state.SourceID || state.TargetID == state.OldRoot) {
		return codexSubagentMigrationState{}, false
	}
	return state, true
}

func clearCodexSubagentMigrationState(instanceID string) {
	instanceID = strings.TrimSpace(instanceID)
	if !validHookSessionAnchorInstanceID(instanceID) {
		return
	}
	_ = os.Remove(codexSubagentMigrationStatePath(instanceID))
}
