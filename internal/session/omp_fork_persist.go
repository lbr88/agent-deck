package session

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

const toolDataOmpPendingForkCommandKey = "omp_pending_fork_command"

var ErrOmpForkCheckpointSuperseded = errors.New("OMP fork checkpoint was superseded")

// OMP keeps its native-fork recipe until the child acknowledges its identity.
// Persist that intent in the tool_data extras zone, without changing any other
// provider's transient fork handling or the positional SQLite tool-data schema.
func writeOmpPendingForkToToolData(td json.RawMessage, inst *Instance) json.RawMessage {
	inst.mu.RLock()
	if inst.Tool != "omp" {
		inst.mu.RUnlock()
		return td
	}
	command := ""
	if inst.IsForkAwaitingStart {
		command = inst.ForkStartCommand
	}
	inst.mu.RUnlock()

	var fields map[string]json.RawMessage
	_ = json.Unmarshal(td, &fields)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	// Explicit empty is required: omitting an acknowledged recipe would let
	// MergeToolDataExtras restore the old pending value on the next save.
	fields[toolDataOmpPendingForkCommandKey], _ = json.Marshal(command)
	out, _ := json.Marshal(fields)
	return out
}

func readOmpPendingForkFromToolData(td json.RawMessage, tool string) string {
	if tool != "omp" {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(td, &fields); err != nil {
		return ""
	}
	var command string
	if err := json.Unmarshal(fields[toolDataOmpPendingForkCommandKey], &command); err != nil {
		return ""
	}
	return command
}

// CheckpointOmpForkBeforeStart persists an OMP child's exact pending native-
// fork recipe before any provider process may acknowledge and clear it. The
// returned preserveChild is true when the row is durable or a failed write is
// ambiguous; callers must then retain its worktree/identity for safe recovery.
// Other providers retain their existing transient first-start behavior.
func (s *Storage) CheckpointOmpForkBeforeStart(inst *Instance) (preserveChild bool, err error) {
	if inst == nil {
		return false, fmt.Errorf("cannot checkpoint nil OMP fork child")
	}
	inst.mu.RLock()
	tool := inst.Tool
	pending := inst.IsForkAwaitingStart
	recipe := inst.ForkStartCommand
	id := inst.ID
	inst.mu.RUnlock()
	if tool != "omp" {
		return false, nil
	}
	if !pending || recipe == "" {
		return false, fmt.Errorf("cannot checkpoint OMP fork child %s: pending native-fork recipe is unavailable; provider was not started and parent history is preserved", id)
	}

	if saveErr := s.InsertSessionAndVerify(inst, nil); saveErr != nil {
		row, inspectErr := s.loadOmpForkCheckpointRow(id)
		preserve := inspectErr != nil || row != nil
		return preserve, fmt.Errorf("cannot durably checkpoint OMP fork child %s before provider launch: %w; provider was not started and parent history is preserved; if child %s is listed, start it to resume safely, otherwise retry the fork", id, saveErr, id)
	}
	row, inspectErr := s.loadOmpForkCheckpointRow(id)
	if inspectErr != nil {
		return true, fmt.Errorf("cannot verify durable OMP fork checkpoint for child %s: %w; provider was not started and the possibly persisted child/worktree must be preserved for retry", id, inspectErr)
	}
	if row == nil {
		return false, fmt.Errorf("cannot verify durable OMP fork checkpoint for child %s: saved row disappeared; provider was not started and parent history is preserved", id)
	}
	if row.Tool != "omp" || readOmpPendingForkFromToolData(row.ToolData, row.Tool) != recipe {
		return true, fmt.Errorf("cannot verify durable OMP fork checkpoint for child %s: persisted recipe does not match; provider was not started and the child/worktree is preserved for inspection", id)
	}
	return true, nil
}

func (s *Storage) loadOmpForkCheckpointRow(id string) (*statedb.InstanceRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("storage database not initialized")
	}
	return s.db.LoadInstanceByID(id)
}

// FinalizeOmpForkLaunch durably clears the checkpointed recipe and records the
// live runtime handle after an exact identity ACK. It is a targeted update so
// a slow launch cannot replay stale title/group metadata over concurrent user
// edits. ErrInstanceNotStored remains discoverable with errors.Is: callers
// must not reinsert a child the user deleted while it was launching.
func (s *Storage) FinalizeOmpForkLaunch(inst *Instance) error {
	if inst == nil {
		return fmt.Errorf("cannot finalize nil OMP fork child")
	}
	inst.mu.RLock()
	if inst.Tool != "omp" {
		inst.mu.RUnlock()
		return nil
	}
	id := inst.ID
	launchTitle := inst.Title
	status := string(inst.Status)
	startedAt := inst.LastStartedAt
	sandboxContainer := inst.SandboxContainer
	tmuxName := ""
	if inst.tmuxSession != nil {
		tmuxName = inst.tmuxSession.Name
	}
	inst.mu.RUnlock()
	if tmuxName == "" {
		return fmt.Errorf("cannot finalize OMP fork child %s: provider ACK left no tmux session handle", id)
	}

	s.mu.Lock()
	if s.db == nil {
		s.mu.Unlock()
		return fmt.Errorf("cannot finalize OMP fork child %s: storage database not initialized", id)
	}
	_, err := s.db.WriteOmpForkLaunchOutcome(id, tmuxName, status, startedAt, sandboxContainer)
	if err != nil {
		s.mu.Unlock()
		if errors.Is(err, statedb.ErrInstanceNotStored) {
			return fmt.Errorf("OMP fork child %s was removed while launching: %w", id, err)
		}
		return fmt.Errorf("record acknowledged OMP fork child %s: %w", id, err)
	}
	row, err := s.db.LoadInstanceByID(id)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("reload acknowledged OMP fork child %s: %w", id, err)
	}
	if row == nil {
		return fmt.Errorf("OMP fork child %s was removed after launch finalization: %w", id, statedb.ErrInstanceNotStored)
	}
	if err := applyOmpForkPersistedMetadata(inst, row); err != nil {
		return fmt.Errorf("apply persisted metadata for acknowledged OMP fork child %s: %w", id, err)
	}
	if row.Title != launchTitle {
		if err := inst.syncOmpTitle(row.Title); err != nil {
			return fmt.Errorf("reapply newer title for acknowledged OMP fork child %s: %w", id, err)
		}
	}
	return nil
}

// ReconcileOmpForkMetadata refreshes user-owned metadata and editable launch
// configuration from the latest durable row. Completion/adoption calls it
// after a potentially long provider start so an old watcher snapshot cannot
// replay over a newer CLI/web edit. It performs database work only: remote
// title synchronization stays in the launch worker. exists=false means the
// user deleted the checkpoint and callers must not re-adopt it.
func (s *Storage) ReconcileOmpForkMetadata(inst *Instance) (exists bool, err error) {
	if inst == nil || inst.GetToolThreadSafe() != "omp" {
		return true, nil
	}
	s.mu.Lock()
	if s.db == nil {
		s.mu.Unlock()
		return false, fmt.Errorf("storage database not initialized")
	}
	row, err := s.db.LoadInstanceByID(inst.ID)
	s.mu.Unlock()
	if err != nil || row == nil {
		return row != nil, err
	}
	if row.Tool != "omp" {
		return false, fmt.Errorf("%w: row %s now belongs to tool %q", ErrOmpForkCheckpointSuperseded, inst.ID, row.Tool)
	}
	if err := applyOmpForkPersistedMetadata(inst, row); err != nil {
		return true, err
	}
	return true, nil
}

// ConvergeOmpForkUserTitle persists a pending user rename with a targeted row
// update, then updates the provider intent. Call it from a launch worker/tea.Cmd
// because remote and sandbox title synchronization may block.
func (s *Storage) ConvergeOmpForkUserTitle(inst *Instance, expectedTitle, title string, locked bool) (string, error) {
	if inst == nil || inst.GetToolThreadSafe() != "omp" {
		return "", nil
	}
	s.mu.Lock()
	if s.db == nil {
		s.mu.Unlock()
		return "", fmt.Errorf("storage database not initialized")
	}
	_, err := s.db.WriteOmpForkUserTitle(inst.ID, expectedTitle, title, locked)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	// Always re-read after the compare-and-set. A newer successful rename may
	// have cleared the UI pending map while this older command was queued; in
	// that case its durable title wins and the stale command must converge the
	// provider forward rather than overwrite the row backward.
	for attempt := 0; attempt < 3; attempt++ {
		s.mu.Lock()
		row, loadErr := s.db.LoadInstanceByID(inst.ID)
		s.mu.Unlock()
		if loadErr != nil {
			return "", loadErr
		}
		if row == nil || row.Tool != "omp" {
			return "", fmt.Errorf("OMP fork child %s was removed or superseded before title convergence: %w", inst.ID, statedb.ErrInstanceNotStored)
		}
		if err := inst.syncOmpTitle(row.Title); err != nil {
			return "", err
		}
		s.mu.Lock()
		latest, loadErr := s.db.LoadInstanceByID(inst.ID)
		s.mu.Unlock()
		if loadErr != nil {
			return "", loadErr
		}
		if latest == nil || latest.Tool != "omp" {
			return "", fmt.Errorf("OMP fork child %s was removed or superseded during title convergence: %w", inst.ID, statedb.ErrInstanceNotStored)
		}
		if latest.Title == row.Title {
			return latest.Title, nil
		}
	}
	return "", fmt.Errorf("OMP fork child %s title changed repeatedly during convergence; retry rename", inst.ID)
}

func applyOmpForkPersistedMetadata(inst *Instance, row *statedb.InstanceRow) error {
	if inst == nil || row == nil {
		return nil
	}
	if row.Tool != "omp" {
		return fmt.Errorf("row %s belongs to tool %q, not OMP", row.ID, row.Tool)
	}
	var extras struct {
		Notes           string          `json:"notes"`
		Color           string          `json:"color"`
		ToolOptions     json.RawMessage `json:"tool_options"`
		IdleTimeoutSecs int64           `json:"idle_timeout_secs"`
	}
	if len(row.ToolData) > 0 {
		if err := json.Unmarshal(row.ToolData, &extras); err != nil {
			return fmt.Errorf("decode OMP fork metadata: %w", err)
		}
	}
	inst.mu.Lock()
	inst.Title = row.Title
	inst.ProjectPath = row.ProjectPath
	inst.GroupPath = row.GroupPath
	inst.Order = row.Order
	inst.Command = row.Command
	inst.Wrapper = row.Wrapper
	inst.Account = row.Account
	inst.NoTransitionNotify = row.NoTransitionNotify
	inst.TitleLocked = row.TitleLocked
	inst.Pin = PinMode(row.Pin)
	inst.Notes = extras.Notes
	inst.Color = extras.Color
	inst.ToolOptionsJSON = append(json.RawMessage(nil), extras.ToolOptions...)
	inst.IdleTimeoutSecs = extras.IdleTimeoutSecs
	inst.ArchivedAt = row.ArchivedAt
	inst.mu.Unlock()
	return nil
}
