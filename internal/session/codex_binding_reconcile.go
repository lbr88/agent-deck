package session

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// adoptPersistedCodexPromotion heals a stale long-running Instance after a
// peer Agent Deck process migrates its legacy guardian/subagent binding to a
// user-facing Codex fork.
//
// The profile database is authoritative only for this one-way promotion. We
// deliberately do not adopt the child's recorded old root: older builds could
// write that root while the hidden child contained newer work, and resuming it
// would strand that history. A promoted target must be a different, known
// top-level rollout. This method never writes the database; doing so from the
// stale snapshot would recreate the cross-process clobber it is meant to stop.
func (i *Instance) adoptPersistedCodexPromotion(syncTmuxEnv bool) bool {
	if i == nil {
		return false
	}
	release, err := AcquireHookSessionLock(i.ID)
	if err != nil {
		sessionLog.Warn("codex_persisted_promotion_lock_failed",
			slog.String("instance_id", i.ID),
			slog.String("error", err.Error()))
		return false
	}
	defer release()
	return i.adoptPersistedCodexPromotionLocked(syncTmuxEnv)
}

func (i *Instance) adoptPersistedCodexPromotionLocked(syncTmuxEnv bool) bool {
	if i == nil || !IsCodexCompatible(i.Tool) {
		return false
	}
	if i.codexSessionBindingOverrideIntent {
		return false
	}

	currentID := strings.ToLower(strings.TrimSpace(i.CodexSessionID))
	db := i.metadataStateDB()
	if db == nil {
		if currentID != "" && IsCodexTopLevelSession(i.getCodexHomeDir(), currentID) && ReadHookSessionAnchor(i.ID) != currentID {
			WriteHookSessionAnchor(i.ID, currentID)
		}
		return false
	}
	persistedID, detectedAt, persistedRevision, err := db.ReadCodexSessionBinding(i.ID)
	if err != nil {
		sessionLog.Warn("codex_persisted_promotion_read_failed",
			slog.String("instance_id", i.ID),
			slog.String("error", err.Error()))
		return false
	}
	persistedID = strings.ToLower(strings.TrimSpace(persistedID))
	if persistedID == currentID {
		revisionAdvanced := persistedRevision > i.CodexBindingRevision
		// One-time bootstrap for legacy/manual top-level bindings written before
		// durable generations existed. A targeted write closes the generation-zero
		// window without trusting a stale whole-row snapshot.
		if persistedRevision == 0 && currentID != "" && IsCodexTopLevelSession(i.getCodexHomeDir(), currentID) {
			if i.CodexDetectedAt.IsZero() {
				i.CodexDetectedAt = time.Now()
			}
			detectedAt = i.CodexDetectedAt
			revision, matched, writeErr := db.WriteCodexSessionBinding(i.ID, currentID, i.CodexDetectedAt, 0)
			if writeErr != nil || !matched {
				sessionLog.Warn("codex_binding_revision_bootstrap_failed",
					slog.String("instance_id", i.ID),
					slog.String("session_id", currentID),
					slog.Bool("row_matched", matched),
					slog.Any("error", writeErr))
			} else {
				persistedRevision = revision
				revisionAdvanced = true
			}
		}
		if persistedRevision > i.CodexBindingRevision {
			i.CodexBindingRevision = persistedRevision
			i.CodexDetectedAt = detectedAt
		}
		if persistedRevision > 0 && !i.CodexDetectedAt.IsZero() {
			ensureCodexBindingFloorState(i.ID, currentID, i.CodexDetectedAt, persistedRevision)
		}
		if currentID == "" && persistedRevision > 0 {
			ClearHookSessionAnchor(i.ID)
		} else if currentID != "" && IsCodexTopLevelSession(i.getCodexHomeDir(), currentID) && ReadHookSessionAnchor(i.ID) != currentID {
			WriteHookSessionAnchor(i.ID, currentID)
		}
		if syncTmuxEnv && revisionAdvanced && persistedRevision > 0 && i.tmuxSession != nil && i.tmuxSession.Exists() {
			envID := currentID
			if IsCodexSubagentSession(i.getCodexHomeDir(), currentID) {
				envID = ""
			}
			if err := i.tmuxSession.SetEnvironment("CODEX_SESSION_ID", envID); err != nil {
				sessionLog.Warn("codex_persisted_binding_env_sync_failed",
					slog.String("instance_id", i.ID),
					slog.String("session_id", currentID),
					slog.String("error", err.Error()))
			}
		}
		return false
	}

	// A non-zero binding generation orders every patched writer, including an
	// explicit clear. A newer (or equal-but-conflicting) committed generation is
	// authoritative even when its session ID is empty; this prevents a stale
	// long-running process from resurrecting a promotion after the user cleared
	// or replaced it.
	revisionAuthoritative := persistedRevision > i.CodexBindingRevision ||
		(persistedRevision > 0 && persistedRevision == i.CodexBindingRevision)
	if i.CodexBindingRevision > persistedRevision && i.CodexBindingRevision > 0 {
		return false
	}
	if persistedID != "" && !IsCodexTopLevelSession(i.getCodexHomeDir(), persistedID) {
		return false
	}

	// Prefer exact durable provenance. It covers every stale peer shape: the
	// guardian source, its abandoned old root, and an instance loaded before any
	// ID was detected. Legacy migrations that predate the completed marker may
	// still advance monotonically by immutable rollout creation time.
	state, hasState := readCodexSubagentMigrationState(i.ID)
	if !revisionAuthoritative {
		if persistedID == "" {
			return false
		}
		provenPromotion := hasState && !state.Completed.IsZero() && state.TargetID == persistedID &&
			(currentID == "" || currentID == state.SourceID || currentID == state.OldRoot)
		if !provenPromotion {
			if currentID == "" {
				return false
			}
			currentCreated := codexRolloutCreatedAt(i.getCodexHomeDir(), currentID)
			persistedCreated := codexRolloutCreatedAt(i.getCodexHomeDir(), persistedID)
			if currentCreated.IsZero() || persistedCreated.IsZero() || !persistedCreated.After(currentCreated) {
				return false
			}
		}
	}

	i.CodexSessionID = persistedID
	if detectedAt.IsZero() && persistedID != "" {
		detectedAt = time.Now()
	}
	i.CodexDetectedAt = detectedAt
	i.CodexBindingRevision = persistedRevision
	if persistedRevision == 0 {
		revision, matched, writeErr := db.WriteCodexSessionBinding(i.ID, persistedID, detectedAt, 0)
		if writeErr != nil || !matched {
			sessionLog.Warn("codex_adopted_binding_revision_bootstrap_failed",
				slog.String("instance_id", i.ID),
				slog.String("session_id", persistedID),
				slog.Bool("row_matched", matched),
				slog.Any("error", writeErr))
		} else {
			i.CodexBindingRevision = revision
		}
	}
	ensureCodexBindingFloorState(i.ID, persistedID, i.CodexDetectedAt, i.CodexBindingRevision)
	i.hookStatus = ""
	i.hookEvent = ""
	i.hookLastUpdate = time.Time{}
	i.hookSessionID = persistedID
	i.codexSessionBindingOverrideIntent = false
	if hasState && revisionAuthoritative && state.TargetID != "" && persistedID != state.TargetID {
		i.clearCodexSubagentMigration()
	} else if hasState && state.TargetID == currentID && persistedID != currentID {
		i.clearCodexSubagentMigration()
	} else {
		i.resetCodexSubagentMigrationMemory()
	}
	// Preserve a fresh hook already emitted by the promoted top-level process;
	// only quarantine a file that still identifies some other thread.
	if hs := readHookStatusFile(i.ID); hs != nil {
		hookID := strings.ToLower(strings.TrimSpace(hs.SessionID))
		if hookID != "" && hookID != persistedID {
			i.removePersistedHookStatusFile()
		}
	}
	if persistedID == "" {
		ClearHookSessionAnchor(i.ID)
	} else {
		WriteHookSessionAnchor(i.ID, persistedID)
	}

	if syncTmuxEnv && i.tmuxSession != nil && i.tmuxSession.Exists() {
		if err := i.tmuxSession.SetEnvironment("CODEX_SESSION_ID", persistedID); err != nil {
			sessionLog.Warn("codex_persisted_promotion_env_sync_failed",
				slog.String("instance_id", i.ID),
				slog.String("session_id", persistedID),
				slog.String("error", err.Error()))
		}
	}

	_ = WriteSessionIDLifecycleEvent(SessionIDLifecycleEvent{
		InstanceID: i.ID,
		Tool:       i.Tool,
		Action:     "rebind",
		Source:     "persisted_binding",
		OldID:      currentID,
		Candidate:  persistedID,
		Reason:     "peer_committed_newer_codex_binding",
	})
	sessionLog.Info("codex_persisted_promotion_adopted",
		slog.String("instance_id", i.ID),
		slog.String("old_id", currentID),
		slog.String("new_id", persistedID))
	return true
}

// adoptCompletedCodexMigrationTarget recovers the narrow crash window after a
// fresh top-level fork was durably recorded in the migration sidecar but before
// the matching profile-DB promotion committed. A stale process may still hold
// either the guardian source or its abandoned old root. buildCodexCommand must
// reuse the completed target before its normal legacy path clears the sidecar
// and launches another fork.
//
// The completed sidecar is retained as provenance for delayed source/root
// events. Promotion persistence is retried best-effort; even if SQLite is still
// unavailable, resuming the already-created target is safer than duplicating
// the fork.
func (i *Instance) adoptCompletedCodexMigrationTarget(syncTmuxEnv bool) bool {
	if i == nil {
		return false
	}
	release, err := AcquireHookSessionLock(i.ID)
	if err != nil {
		sessionLog.Warn("codex_completed_migration_lock_failed",
			slog.String("instance_id", i.ID),
			slog.String("error", err.Error()))
		return false
	}
	defer release()
	return i.adoptCompletedCodexMigrationTargetLocked(syncTmuxEnv)
}

func (i *Instance) adoptCompletedCodexMigrationTargetLocked(syncTmuxEnv bool) bool {
	if i == nil || !IsCodexCompatible(i.Tool) || i.codexSessionBindingOverrideIntent {
		return false
	}

	state, ok := readCodexSubagentMigrationState(i.ID)
	if !ok || state.TargetID == "" || state.Completed.IsZero() ||
		state.Completed.Before(state.Started) {
		return false
	}
	currentID := strings.ToLower(strings.TrimSpace(i.CodexSessionID))
	if currentID != state.SourceID && currentID != state.OldRoot {
		return false
	}

	codexHome := i.getCodexHomeDir()
	if !IsCodexTopLevelSession(codexHome, state.TargetID) {
		return false
	}
	rolloutPath := codexRolloutPathInHome(state.TargetID, codexHome)
	info, err := os.Stat(rolloutPath)
	if err != nil || info.ModTime().Before(state.Started.Add(-2*time.Second)) {
		return false
	}

	i.CodexSessionID = state.TargetID
	i.CodexDetectedAt = state.Completed
	writeCodexBindingFloorState(i.ID, state.TargetID, state.Completed, i.CodexBindingRevision)
	i.hookStatus = ""
	i.hookEvent = ""
	i.hookLastUpdate = time.Time{}
	i.hookSessionID = state.TargetID
	i.codexSessionBindingOverrideIntent = false
	i.resetCodexSubagentMigrationMemory()
	WriteHookSessionAnchor(i.ID, state.TargetID)

	if syncTmuxEnv && i.tmuxSession != nil && i.tmuxSession.Exists() {
		if err := i.tmuxSession.SetEnvironment("CODEX_SESSION_ID", state.TargetID); err != nil {
			sessionLog.Warn("codex_completed_migration_target_env_sync_failed",
				slog.String("instance_id", i.ID),
				slog.String("session_id", state.TargetID),
				slog.String("error", err.Error()))
		}
	}

	// Retry the DB promotion that may have lost the race/crashed after the
	// completed sidecar landed. This method deliberately leaves the sidecar in
	// place whether the retry succeeds or fails.
	if i.persistCodexSessionPromotion(state, "codex_completed_migration_promotion_retry_failed") {
		ensureCodexBindingFloorState(i.ID, state.TargetID, i.CodexDetectedAt, i.CodexBindingRevision)
	} else {
		i.adoptPersistedCodexPromotionLocked(syncTmuxEnv)
	}
	sessionLog.Info("codex_completed_migration_target_adopted",
		slog.String("instance_id", i.ID),
		slog.String("old_id", currentID),
		slog.String("new_id", state.TargetID))
	return true
}

// completedCodexMigrationRejects identifies a delayed lifecycle/env candidate
// from the exact guardian source or abandoned old root after the promoted fork
// is already authoritative. The completed sidecar intentionally survives
// process restarts until a genuinely newer top-level rotation supersedes it.
func (i *Instance) completedCodexMigrationRejects(candidateID string) bool {
	if i == nil {
		return false
	}
	state, ok := readCodexSubagentMigrationState(i.ID)
	if !ok || state.Completed.IsZero() || state.TargetID == "" ||
		!strings.EqualFold(strings.TrimSpace(i.CodexSessionID), state.TargetID) {
		return false
	}
	candidateID = strings.ToLower(strings.TrimSpace(candidateID))
	return candidateID != "" && (candidateID == state.SourceID || candidateID == state.OldRoot)
}

// codexBindingFloorRejects protects an explicit binding edit while it is
// waiting for a whole-row save, and protects a committed empty binding as a
// tombstone afterward. An empty binding records its mutation time in
// CodexDetectedAt; only a top-level rollout created at or after that floor can
// become the fresh replacement. Delayed hooks/env values from older roots are
// therefore unable to turn themselves into a newer binding revision.
func (i *Instance) codexBindingFloorRejects(candidateID string) bool {
	if i == nil {
		return false
	}
	candidateID = strings.ToLower(strings.TrimSpace(candidateID))
	if candidateID == "" {
		return false
	}
	currentID := strings.ToLower(strings.TrimSpace(i.CodexSessionID))
	if i.codexSessionBindingOverrideIntent && currentID != "" {
		return candidateID != currentID
	}
	if candidateID == currentID {
		return false
	}
	if i.CodexBindingRevision <= 0 && !i.codexSessionBindingOverrideIntent {
		return false
	}
	// Once an empty binding becomes a tombstone, only a verified top-level
	// rollout may consume it. Fresh guardians/subagents are internal too; their
	// creation time being after RestartFresh does not make them user threads.
	if currentID == "" && CodexSessionRolloutExists(i.getCodexHomeDir(), candidateID) &&
		!IsCodexTopLevelSession(i.getCodexHomeDir(), candidateID) {
		return true
	}
	floorAt := i.CodexDetectedAt
	if persistedFloor, ok := readCodexBindingFloorState(i.ID); ok && persistedFloor.SessionID == currentID &&
		persistedFloor.DetectedAt.After(floorAt) {
		floorAt = persistedFloor.DetectedAt
	}
	if floorAt.IsZero() {
		return true
	}
	createdAt := codexRolloutCreatedAt(i.getCodexHomeDir(), candidateID)
	return createdAt.IsZero() || createdAt.Before(floorAt)
}

func (i *Instance) healRejectedCodexCandidate(candidateID, reason string) {
	if i == nil {
		return
	}
	release, err := AcquireHookSessionLock(i.ID)
	if err != nil {
		sessionLog.Warn("codex_rejected_candidate_lock_failed",
			slog.String("instance_id", i.ID),
			slog.String("candidate", strings.ToLower(strings.TrimSpace(candidateID))),
			slog.String("error", err.Error()))
		return
	}
	defer release()
	i.healRejectedCodexCandidateLocked(candidateID, reason)
}

func (i *Instance) healRejectedCodexCandidateLocked(candidateID, reason string) {
	currentID := strings.ToLower(strings.TrimSpace(i.CodexSessionID))
	if currentID != "" && IsCodexTopLevelSession(i.getCodexHomeDir(), currentID) {
		WriteHookSessionAnchor(i.ID, currentID)
	} else if currentID == "" {
		ClearHookSessionAnchor(i.ID)
	}
	if i.tmuxSession != nil && i.tmuxSession.Exists() {
		_ = i.tmuxSession.SetEnvironment("CODEX_SESSION_ID", i.codexTmuxSessionID())
	}
	_ = WriteSessionIDLifecycleEvent(SessionIDLifecycleEvent{
		InstanceID: i.ID,
		Tool:       i.Tool,
		Action:     "reject",
		Source:     "codex_binding_guard",
		OldID:      currentID,
		Candidate:  strings.ToLower(strings.TrimSpace(candidateID)),
		Reason:     reason,
	})
	sessionLog.Warn("codex_session_candidate_rejected",
		slog.String("instance_id", i.ID),
		slog.String("current_id", currentID),
		slog.String("candidate", candidateID),
		slog.String("reason", reason))
}

func (i *Instance) codexTmuxSessionID() string {
	if i == nil {
		return ""
	}
	currentID := strings.ToLower(strings.TrimSpace(i.CodexSessionID))
	if IsCodexSubagentSession(i.getCodexHomeDir(), currentID) {
		return ""
	}
	return currentID
}
