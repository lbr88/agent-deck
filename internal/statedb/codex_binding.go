package statedb

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	codexBindingRevisionKey    = "codex_binding_revision"
	codexPromotionSourceKey    = "codex_promotion_source_id"
	codexPromotionOldRootKey   = "codex_promotion_old_root_id"
	codexPromotionTargetKey    = "codex_promotion_target_id"
	codexPromotionStartedKey   = "codex_promotion_started_at"
	codexPromotionCompletedKey = "codex_promotion_completed_at"
)

var codexPromotionKeys = []string{
	codexPromotionSourceKey,
	codexPromotionOldRootKey,
	codexPromotionTargetKey,
	codexPromotionStartedKey,
	codexPromotionCompletedKey,
}

func toolDataString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.ToLower(strings.TrimSpace(value))
}

func toolDataPositiveInt64(values map[string]any, key string) int64 {
	value, ok := values[key].(float64)
	if !ok || value <= 0 || value != float64(int64(value)) {
		return 0
	}
	return int64(value)
}

// ReadCodexBindingRevisionFromToolData extracts the durable generation used to
// order concurrent Codex binding writers. Missing, malformed, and non-positive
// revisions are treated as the legacy generation zero.
func ReadCodexBindingRevisionFromToolData(data json.RawMessage) int64 {
	var values map[string]any
	if json.Unmarshal(data, &values) != nil {
		return 0
	}
	return toolDataPositiveInt64(values, codexBindingRevisionKey)
}

// WriteCodexBindingRevisionToToolData returns data with the supplied durable
// Codex binding generation. A non-positive revision removes the key so legacy
// and newly-created rows remain compact generation-zero records.
func WriteCodexBindingRevisionToToolData(data json.RawMessage, revision int64) json.RawMessage {
	var values map[string]json.RawMessage
	if len(data) == 0 {
		values = make(map[string]json.RawMessage)
	} else if json.Unmarshal(data, &values) != nil {
		return data
	}
	if values == nil {
		values = make(map[string]json.RawMessage)
	}
	if revision > 0 {
		revisionJSON, err := json.Marshal(revision)
		if err != nil {
			return data
		}
		values[codexBindingRevisionKey] = revisionJSON
	} else {
		delete(values, codexBindingRevisionKey)
	}
	out, err := json.Marshal(values)
	if err != nil {
		return data
	}
	return out
}

// WriteCodexSessionBindingToToolData overwrites the serialized Codex tuple with
// a caller-captured snapshot. Storage uses this after instanceToRow so a
// concurrent in-memory edit cannot leak partially into an older save payload.
func WriteCodexSessionBindingToToolData(data json.RawMessage, sessionID string, detectedAt time.Time, revision int64) json.RawMessage {
	var values map[string]any
	if len(data) == 0 {
		values = make(map[string]any)
	} else if json.Unmarshal(data, &values) != nil {
		return data
	}
	if values == nil {
		values = make(map[string]any)
	}
	values["codex_session_id"] = strings.ToLower(strings.TrimSpace(sessionID))
	if detectedAt.IsZero() {
		delete(values, "codex_detected_at")
	} else {
		values["codex_detected_at"] = detectedAt.Unix()
	}
	if revision > 0 {
		values[codexBindingRevisionKey] = revision
	} else {
		delete(values, codexBindingRevisionKey)
	}
	out, err := json.Marshal(values)
	if err != nil {
		return data
	}
	return out
}

// ReadCodexSessionBindingFromToolData extracts the complete durable Codex
// binding tuple used by Storage after a whole-row save has been reconciled at
// SQLite's write boundary.
func ReadCodexSessionBindingFromToolData(data json.RawMessage) (string, time.Time, int64) {
	var values map[string]any
	if json.Unmarshal(data, &values) != nil {
		return "", time.Time{}, 0
	}

	sessionID := toolDataString(values, "codex_session_id")
	detectedAt := time.Time{}
	if detectedAtUnix := toolDataPositiveInt64(values, "codex_detected_at"); detectedAtUnix > 0 {
		detectedAt = time.Unix(detectedAtUnix, 0)
	}
	return sessionID, detectedAt, toolDataPositiveInt64(values, codexBindingRevisionKey)
}

// stripCodexManagedExtras removes keys which MergeToolDataExtras would
// otherwise mistake for user-managed unknown fields. These values have their
// own concurrency semantics and are restored, when appropriate, only by
// reconcileCodexBindingToolData using the authoritative row in the active
// SQLite write transaction.
func stripCodexManagedExtras(data json.RawMessage) json.RawMessage {
	var values map[string]json.RawMessage
	if json.Unmarshal(data, &values) != nil || values == nil {
		return data
	}
	delete(values, codexBindingRevisionKey)
	for _, key := range codexPromotionKeys {
		delete(values, key)
	}
	out, err := json.Marshal(values)
	if err != nil {
		return data
	}
	return out
}

func mergeToolDataExtrasWithoutCodexManaged(oldToolData, newToolData json.RawMessage) json.RawMessage {
	return MergeToolDataExtras(stripCodexManagedExtras(oldToolData), newToolData)
}

func ensureExplicitCodexBindingKey(data json.RawMessage) json.RawMessage {
	var values map[string]json.RawMessage
	if json.Unmarshal(data, &values) != nil {
		return data
	}
	if values == nil {
		values = make(map[string]json.RawMessage)
	}
	if _, ok := values["codex_session_id"]; !ok {
		values["codex_session_id"] = json.RawMessage(`""`)
	}
	out, err := json.Marshal(values)
	if err != nil {
		return data
	}
	return out
}

func copyCodexBinding(authoritative, incoming map[string]any, revision int64) {
	for _, key := range []string{"codex_session_id", "codex_detected_at"} {
		if value, ok := authoritative[key]; ok {
			incoming[key] = value
		} else {
			delete(incoming, key)
		}
	}
	if revision > 0 {
		incoming[codexBindingRevisionKey] = revision
	} else {
		delete(incoming, codexBindingRevisionKey)
	}
	for _, key := range codexPromotionKeys {
		if value, ok := authoritative[key]; ok {
			incoming[key] = value
		}
	}
}

// reconcileCodexBindingToolData runs while a full-row save holds SQLite's
// write transaction. Targeted writers advance codex_binding_revision
// atomically, allowing this boundary to reject stale snapshots even after a
// completed promotion marker has been rotated away. The marker remains a
// compatibility defense for generation-zero promotions.
//
// Explicit user overrides are the one permitted way for a stale snapshot to
// supersede the authoritative binding: they receive a new generation greater
// than both versions and remove all migration provenance.
func reconcileCodexBindingToolData(existing, incoming json.RawMessage, explicitOverride bool) json.RawMessage {
	var oldValues, newValues map[string]any
	if json.Unmarshal(existing, &oldValues) != nil || json.Unmarshal(incoming, &newValues) != nil {
		return incoming
	}
	if oldValues == nil || newValues == nil {
		return incoming
	}

	// Promotion provenance is never accepted from a serialized process
	// snapshot. Only the authoritative row may contribute these managed keys.
	for _, key := range codexPromotionKeys {
		delete(newValues, key)
	}

	existingID := toolDataString(oldValues, "codex_session_id")
	incomingID := toolDataString(newValues, "codex_session_id")
	existingRevision := toolDataPositiveInt64(oldValues, codexBindingRevisionKey)
	incomingRevision := toolDataPositiveInt64(newValues, codexBindingRevisionKey)

	if explicitOverride {
		if incomingRevision < existingRevision {
			incomingRevision = existingRevision
		}
		newValues[codexBindingRevisionKey] = incomingRevision + 1
	} else if existingRevision > incomingRevision ||
		(existingRevision > 0 && existingRevision == incomingRevision && existingID != incomingID) {
		// A lower generation is stale. Equal non-zero generations must describe
		// the same binding; if they do not, SQLite's currently committed row wins.
		copyCodexBinding(oldValues, newValues, existingRevision)
	} else {
		sourceID := toolDataString(oldValues, codexPromotionSourceKey)
		oldRootID := toolDataString(oldValues, codexPromotionOldRootKey)
		targetID := toolDataString(oldValues, codexPromotionTargetKey)
		validPromotion := sourceID != "" && targetID != "" && existingID == targetID
		promotionPeer := incomingID == sourceID || incomingID == targetID || incomingID == "" ||
			(oldRootID != "" && incomingID == oldRootID)
		if validPromotion && promotionPeer {
			copyCodexBinding(oldValues, newValues, existingRevision)
		}
		// Otherwise the incoming generation/binding is newer (or this is a
		// generation-zero ordinary save) and intentionally rotates provenance.
	}

	merged, err := json.Marshal(newValues)
	if err != nil {
		return incoming
	}
	return merged
}
