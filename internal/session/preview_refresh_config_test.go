package session

import (
	"testing"
	"time"
)

func TestUISettings_GetPreviewRefreshDuration(t *testing.T) {
	cases := []struct {
		name string
		ui   UISettings
		want time.Duration
	}{
		{"unset uses default", UISettings{}, DefaultPreviewRefresh},
		{"explicit value", UISettings{PreviewRefreshMS: 750}, 750 * time.Millisecond},
		{"clamp below min", UISettings{PreviewRefreshMS: 1}, MinPreviewRefresh},
		{"negative uses default", UISettings{PreviewRefreshMS: -5}, DefaultPreviewRefresh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ui.GetPreviewRefreshDuration(); got != tc.want {
				t.Fatalf("GetPreviewRefreshDuration() on %+v = %s, want %s", tc.ui, got, tc.want)
			}
		})
	}
}
