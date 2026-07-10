package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/atomicfile"
)

type codexSubagentMigrationState struct {
	SourceID string    `json:"source_id"`
	OldRoot  string    `json:"old_root_id,omitempty"`
	Started  time.Time `json:"started_at"`
}

func codexSubagentMigrationStatePath(instanceID string) string {
	return filepath.Join(GetHooksDir(), strings.TrimSpace(instanceID)+".codex-migration")
}

func writeCodexSubagentMigrationState(instanceID string, state codexSubagentMigrationState) error {
	if strings.TrimSpace(instanceID) == "" || !isCodexSessionUUID(state.SourceID) || state.Started.IsZero() {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(codexSubagentMigrationStatePath(instanceID), data, 0o600)
}

func readCodexSubagentMigrationState(instanceID string) (codexSubagentMigrationState, bool) {
	if strings.TrimSpace(instanceID) == "" {
		return codexSubagentMigrationState{}, false
	}
	data, err := os.ReadFile(codexSubagentMigrationStatePath(instanceID))
	if err != nil || len(data) > 16*1024 {
		return codexSubagentMigrationState{}, false
	}
	var state codexSubagentMigrationState
	if json.Unmarshal(data, &state) != nil || !isCodexSessionUUID(state.SourceID) || state.Started.IsZero() {
		return codexSubagentMigrationState{}, false
	}
	state.SourceID = strings.ToLower(strings.TrimSpace(state.SourceID))
	state.OldRoot = strings.ToLower(strings.TrimSpace(state.OldRoot))
	return state, true
}

func clearCodexSubagentMigrationState(instanceID string) {
	if strings.TrimSpace(instanceID) == "" {
		return
	}
	_ = os.Remove(codexSubagentMigrationStatePath(instanceID))
}
