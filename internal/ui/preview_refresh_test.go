package ui

import (
	"testing"
	"time"
)

func TestHomeSelectedPreviewCacheExpiredUsesConfiguredTTL(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	home := NewHome()
	home.previewRefreshTTL = 500 * time.Millisecond
	home.previewCacheTime["s1"] = now.Add(-750 * time.Millisecond)

	if !home.selectedPreviewCacheExpired("s1", now) {
		t.Fatal("expected selected local preview cache to expire after configured TTL")
	}
}

func TestHomeSelectedPreviewCacheExpiredSkipsFreshCache(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	home := NewHome()
	home.previewRefreshTTL = 500 * time.Millisecond
	home.previewCacheTime["s1"] = now.Add(-250 * time.Millisecond)

	if home.selectedPreviewCacheExpired("s1", now) {
		t.Fatal("expected selected local preview cache inside configured TTL to stay fresh")
	}
}
