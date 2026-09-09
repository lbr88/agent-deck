package statedb

import (
	"fmt"
	"time"
)

// WriteOmpForkLaunchOutcome completes one pre-start OMP fork checkpoint after
// the child has acknowledged its identity. It deliberately updates only the
// runtime handle/status and OMP-owned tool_data keys. A whole-row save here
// would replay the caller's pre-launch snapshot over title/group/order edits a
// user made while the provider was loading.
//
// The tool predicate fences an ID that was deleted and reused for another
// provider. A zero-row update is reported rather than inserting anything: an
// explicit deletion during a slow launch must never be undone by completion.
func (s *StateDB) WriteOmpForkLaunchOutcome(id, tmuxSession, status string, startedAt time.Time, sandboxContainer string) (WriteStamps, error) {
	startedUnix := int64(0)
	if !startedAt.IsZero() {
		startedUnix = startedAt.Unix()
	}
	var affected int64
	err := withBusyRetry(func() error {
		res, err := s.db.Exec(
			`UPDATE instances
			    SET tmux_session = ?, status = ?,
			        acknowledged = CASE WHEN ? = 'running' THEN 0 ELSE acknowledged END,
			        tool_data = json_set(
			            COALESCE(tool_data, '{}'),
			            '$.omp_pending_fork_command', '',
			            '$.last_started_at', ?,
			            '$.sandbox_container', ?)
			  WHERE id = ? AND tool = 'omp'`,
			tmuxSession, status, status, startedUnix, sandboxContainer, id,
		)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return WriteStamps{}, err
	}
	if affected == 0 {
		return WriteStamps{}, fmt.Errorf("record OMP fork launch outcome for %q: %w", id, ErrInstanceNotStored)
	}
	return s.touchStamp()
}

// WriteOmpForkUserTitle records a post-checkpoint user rename without touching
// runtime state or any other metadata. The tool predicate prevents a late
// launch result from overwriting a deleted/reused row; a title mismatch is a
// benign stale-command result rather than permission to overwrite newer intent.
func (s *StateDB) WriteOmpForkUserTitle(id, expectedTitle, title string, locked bool) (bool, error) {
	lockedInt := 0
	if locked {
		lockedInt = 1
	}
	var affected int64
	err := withBusyRetry(func() error {
		res, err := s.db.Exec(
			`UPDATE instances SET title = ?, title_locked = ?, auto_name = 0 WHERE id = ? AND tool = 'omp' AND title = ?`,
			title, lockedInt, id, expectedTitle,
		)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, err
	}
	if affected == 0 {
		row, err := s.LoadInstanceByID(id)
		if err != nil {
			return false, err
		}
		if row == nil || row.Tool != "omp" {
			return false, fmt.Errorf("record OMP fork title for %q: %w", id, ErrInstanceNotStored)
		}
		return false, nil
	}
	_, err = s.touchStamp()
	return true, err
}
