package statedb

import (
	"encoding/json"
	"time"
)

// WriteKiroSessionBindingToToolData writes Kiro's native session binding into
// the typed tool_data schema while preserving every other known key.
func WriteKiroSessionBindingToToolData(data json.RawMessage, sessionID string, detectedAt time.Time) json.RawMessage {
	var td toolDataBlob
	if len(data) > 0 {
		_ = json.Unmarshal(data, &td)
	}
	td.KiroSessionID = sessionID
	td.KiroDetectedAt = 0
	if !detectedAt.IsZero() {
		td.KiroDetectedAt = detectedAt.Unix()
	}
	out, _ := json.Marshal(td)
	return out
}

// ReadKiroSessionBindingFromToolData extracts Kiro's native session binding
// from the typed tool_data schema.
func ReadKiroSessionBindingFromToolData(data json.RawMessage) (sessionID string, detectedAt time.Time) {
	if len(data) == 0 {
		return "", time.Time{}
	}
	var td toolDataBlob
	if err := json.Unmarshal(data, &td); err != nil {
		return "", time.Time{}
	}
	if td.KiroDetectedAt > 0 {
		detectedAt = time.Unix(td.KiroDetectedAt, 0)
	}
	return td.KiroSessionID, detectedAt
}
