package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSession_Exists_ProbeTimeoutIsNotTreatedAsAbsent is the regression for the
// "all sessions X out at once" cascade: when the tmux server is briefly busy
// (e.g. another session is being torn down), a `has-session` probe can hang.
// The old code ran the probe with no timeout and treated any non-success as
// "session gone", flipping live sessions to StatusError. A probe that does not
// answer in time is indeterminate — we must assume the session still exists and
// let a later poll resolve it, NOT declare it dead.
//
// Contrast with #755 (exists_socket_test.go): a probe that COMPLETES against an
// absent server must still report false. This test only covers the timeout
// (no answer) case, which is the one that must change.
func TestSession_Exists_ProbeTimeoutIsNotTreatedAsAbsent(t *testing.T) {
	// Stub `tmux` with a binary that hangs past the probe timeout, simulating a
	// server too busy to answer. It would eventually exit non-zero, but the
	// probe must give up first and treat the timeout as indeterminate.
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 1\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Short probe timeout so the test is fast; the fake hangs longer than this.
	restore := hasSessionProbeTimeout
	hasSessionProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { hasSessionProbeTimeout = restore })

	// Non-default socket skips the session cache; a unique name guarantees no
	// live pipe connection — so Exists() reaches the subprocess probe.
	s := &Session{Name: "busy-server-session", SocketName: "agent-deck-timeout-test"}

	if !s.Exists() {
		t.Fatalf("Exists() returned false when the has-session probe timed out; " +
			"a probe that never answers is indeterminate and must not be treated as a dead session")
	}
}

func TestSession_ProbeExistsDirect_TimeoutIsIndeterminate(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 1\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	restore := hasSessionProbeTimeout
	hasSessionProbeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { hasSessionProbeTimeout = restore })

	s := &Session{Name: "busy-server-session", SocketName: "agent-deck-timeout-test"}
	exists, err := s.probeExistsDirect()
	if exists {
		t.Fatal("timed-out direct probe must not claim the target exists")
	}
	if !errors.Is(err, errTmuxTimeout) {
		t.Fatalf("timed-out direct probe must report indeterminate timeout, got %v", err)
	}
}

func TestSession_WaitForPersistedIdentityAbsence_RetriesIndeterminateProbe(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "first-probe")
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ ! -e '" + marker + "' ]; then touch '" + marker + "'; sleep 1; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	restore := hasSessionProbeTimeout
	hasSessionProbeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { hasSessionProbeTimeout = restore })

	s := &Session{Name: "released-after-timeout", SocketName: "agent-deck-timeout-test"}
	if err := s.waitForPersistedIdentityAbsence(); err != nil {
		t.Fatalf("an indeterminate first probe followed by definitive absence must recover: %v", err)
	}
}

func TestSession_WaitForPersistedIdentityAbsence_ReportsIndeterminate(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 1\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	restoreTimeout := hasSessionProbeTimeout
	restoreDelay := persistedIdentityProbeDelay
	hasSessionProbeTimeout = 25 * time.Millisecond
	persistedIdentityProbeDelay = time.Millisecond
	t.Cleanup(func() {
		hasSessionProbeTimeout = restoreTimeout
		persistedIdentityProbeDelay = restoreDelay
	})

	s := &Session{Name: "always-busy", SocketName: "agent-deck-timeout-test"}
	err := s.waitForPersistedIdentityAbsence()
	if !errors.Is(err, errPersistedIdentityIndeterminate) {
		t.Fatalf("all timed-out probes must report indeterminate verification, got %v", err)
	}
	if !errors.Is(err, errTmuxTimeout) {
		t.Fatalf("indeterminate verification must preserve the timeout cause, got %v", err)
	}
}
