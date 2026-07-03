package statedb

import (
	"testing"
	"time"
)

func TestKiroSessionBindingToolDataRoundTrip(t *testing.T) {
	detectedAt := time.Unix(1783072800, 0).UTC()
	data := WriteKiroSessionBindingToToolData(nil, "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d", detectedAt)

	sessionID, gotDetectedAt := ReadKiroSessionBindingFromToolData(data)
	if sessionID != "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d" {
		t.Fatalf("sessionID = %q", sessionID)
	}
	if !gotDetectedAt.Equal(detectedAt) {
		t.Fatalf("detectedAt = %v, want %v", gotDetectedAt, detectedAt)
	}
}

func TestKiroSessionBindingToolDataKnownKeyClears(t *testing.T) {
	detectedAt := time.Unix(1783072800, 0).UTC()
	data := WriteKiroSessionBindingToToolData(nil, "75e59a16-9f76-433d-baa3-3cb8e5ef4c5d", detectedAt)
	data = WriteKiroSessionBindingToToolData(data, "", time.Time{})

	sessionID, gotDetectedAt := ReadKiroSessionBindingFromToolData(data)
	if sessionID != "" {
		t.Fatalf("sessionID after clear = %q, want empty", sessionID)
	}
	if !gotDetectedAt.IsZero() {
		t.Fatalf("detectedAt after clear = %v, want zero", gotDetectedAt)
	}
}
