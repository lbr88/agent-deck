package tmux

import (
	"testing"
	"time"
)

func TestIsPaneDeadRejectsCacheFromPreviousProcessGeneration(t *testing.T) {
	s := &Session{Name: "missing-replacement-pane", startupAt: time.Now()}

	paneCacheMu.Lock()
	paneCacheData = map[string]PaneInfo{s.Name: {Dead: true}}
	paneCacheTime = s.startupAt.Add(-time.Second)
	paneCacheMu.Unlock()
	t.Cleanup(func() {
		paneCacheMu.Lock()
		paneCacheData = nil
		paneCacheTime = time.Time{}
		paneCacheMu.Unlock()
	})

	if s.IsPaneDead() {
		t.Fatal("dead-pane cache from the previous process generation was trusted")
	}
}
