package ui

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func newCodexResumeReadinessHome(t *testing.T) (*Home, *session.Instance) {
	t.Helper()

	home := NewHome()
	inst := &session.Instance{
		ID:     "codex-resume-readiness",
		Title:  "ticmanager and oraa",
		Tool:   "codex",
		Status: session.StatusWaiting,
	}

	home.instancesMu.Lock()
	home.instances = []*session.Instance{inst}
	home.instanceByID[inst.ID] = inst
	home.instancesMu.Unlock()

	return home, inst
}

// Regression: Restart() marks a newly spawned Codex pane waiting before Codex
// has rendered its first frame. Clearing the resume guard on that synthetic
// status lets Enter attach to a completely blank pane for minutes.
func TestCodexResumeEmptyPaneIsNotReadyFromWaitingStatus(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	home.resumingSessions[inst.ID] = time.Now().Add(-10 * time.Second)
	home.previewCache[inst.ID] = ""

	if !home.hasActiveAnimation(inst.ID) {
		t.Fatal("Codex resume with an empty pane must remain guarded even when Restart marks it waiting")
	}
}

// Regression: while Restart() is still preparing the replacement process, the
// preview cache can contain a nonblank frame from the old Codex pane. That old
// content must not release the restart guard.
func TestCodexResumeOldCachedPaneCannotReleaseGuard(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	home.resumingSessions[inst.ID] = time.Now().Add(-10 * time.Second)
	home.previewCache[inst.ID] = "old Codex prompt"

	if !home.hasActiveAnimation(inst.ID) {
		t.Fatal("old cached pane content released the in-flight Codex restart guard")
	}
}

// Regression: a successful Restart() means tmux accepted the spawn command,
// not that the resumed Codex TUI is ready. The guard must survive the async
// restart result and be cleared only by observed pane output.
func TestSuccessfulCodexRestartKeepsResumeGuardUntilPaneReady(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	started := time.Now()
	home.resumingSessions[inst.ID] = started
	home.resumeGenerations[inst.ID] = started

	model, _ := home.Update(sessionRestartedMsg{sessionID: inst.ID, startedAt: started})
	updated := model.(*Home)

	got, ok := updated.resumingSessions[inst.ID]
	if !ok {
		t.Fatal("successful Codex restart cleared the resume guard before the first pane frame")
	}
	if !got.Equal(started) {
		t.Fatalf("resume generation changed after restart: got %v, want %v", got, started)
	}
	if _, ok := updated.resumeGenerations[inst.ID]; ok {
		t.Fatal("restart completion did not consume its ownership generation")
	}
}

// Regression: pressing Enter while Restart() is still in flight must only
// remember the user's intent. Probing at this point can read the old live pane
// before respawn-pane replaces it.
func TestCodexResumeEnterDuringRestartDoesNotProbeOldPane(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	started := time.Now()
	home.resumingSessions[inst.ID] = started
	home.resumeSessionExists = func(*session.Instance) bool { return true }

	probeCalled := false
	home.resumeReadinessProbe = func(*session.Instance) (string, error) {
		probeCalled = true
		return "old Codex prompt", nil
	}

	cmd := home.activateLocalSession(inst)

	if cmd != nil {
		t.Fatal("Enter during an in-flight restart launched a readiness probe")
	}
	if probeCalled {
		t.Fatal("Enter during an in-flight restart captured the old pane")
	}
	if got := home.resumeAttachRequests[inst.ID]; !got.Equal(started) {
		t.Fatalf("Enter did not retain attach intent for the active generation: got %v, want %v", got, started)
	}
}

// Regression: completion from an older restart must not start a readiness
// probe or clear state belonging to a newer restart.
func TestCodexResumeStaleRestartResultCannotOwnNewGeneration(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	oldStart := time.Now().Add(-time.Second)
	currentStart := time.Now()
	home.resumingSessions[inst.ID] = currentStart
	home.resumeGenerations[inst.ID] = currentStart
	home.resumeAttachRequests[inst.ID] = currentStart

	model, cmd := home.Update(sessionRestartedMsg{
		sessionID: inst.ID,
		startedAt: oldStart,
	})
	updated := model.(*Home)

	if cmd != nil {
		t.Fatal("stale restart result scheduled work for the newer generation")
	}
	if got := updated.resumingSessions[inst.ID]; !got.Equal(currentStart) {
		t.Fatalf("stale restart result changed resume generation: got %v, want %v", got, currentStart)
	}
	if got := updated.resumeAttachRequests[inst.ID]; !got.Equal(currentStart) {
		t.Fatalf("stale restart result changed attach generation: got %v, want %v", got, currentStart)
	}
}

// Regression: animation cleanup is visual bookkeeping, not restart ownership.
// A slow matching restart result must still report failure even after its
// resumingSessions entry has expired.
func TestRestartResultAfterAnimationExpiryStillReportsFailure(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	started := home.newSessionResumeGeneration(inst, false)
	delete(home.resumingSessions, inst.ID)

	model, cmd := home.Update(sessionRestartedMsg{
		sessionID: inst.ID,
		startedAt: started,
		err:       errors.New("restart failed after animation cleanup"),
	})
	updated := model.(*Home)

	if cmd != nil {
		t.Fatal("failed slow restart returned unexpected command")
	}
	if updated.err == nil {
		t.Fatal("matching restart failure was silently discarded after animation cleanup")
	}
	if _, ok := updated.resumeGenerations[inst.ID]; ok {
		t.Fatal("slow restart completion did not consume its ownership generation")
	}
}

// Regression: pressing Enter on a stopped session expresses "open this
// session", not merely "spawn its tmux process". An empty readiness probe must
// retain that intent and poll again instead of attaching to the blank pane.
func TestCodexResumeEmptyProbeKeepsAttachIntent(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	started := time.Now()
	home.resumingSessions[inst.ID] = started
	home.resumeAttachRequests[inst.ID] = started

	model, cmd := home.Update(sessionResumeReadinessMsg{
		sessionID: inst.ID,
		startedAt: started,
	})
	updated := model.(*Home)

	if _, ok := updated.resumingSessions[inst.ID]; !ok {
		t.Fatal("empty pane probe cleared the Codex resume guard")
	}
	if _, ok := updated.resumeAttachRequests[inst.ID]; !ok {
		t.Fatal("empty pane probe discarded the user's attach intent")
	}
	if cmd == nil {
		t.Fatal("empty pane probe did not schedule another readiness check")
	}
}

// Regression: the first nonblank pane frame is the release point. If the user
// still has that session selected, Agent Deck must consume the queued attach
// intent and open it exactly once.
func TestCodexResumeFirstPaneFrameAutoAttachesSelectedSession(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	started := time.Now()
	home.resumingSessions[inst.ID] = started
	home.resumeAttachRequests[inst.ID] = started
	home.flatItems = []session.Item{{
		Type:    session.ItemTypeSession,
		Session: inst,
	}}

	var attachedID string
	home.resumeAttachSink = func(got *session.Instance) tea.Cmd {
		attachedID = got.ID
		return func() tea.Msg { return nil }
	}

	model, cmd := home.Update(sessionResumeReadinessMsg{
		sessionID: inst.ID,
		startedAt: started,
		ready:     true,
	})
	updated := model.(*Home)

	if attachedID != inst.ID {
		t.Fatalf("ready Codex session attached %q, want %q", attachedID, inst.ID)
	}
	if cmd == nil {
		t.Fatal("ready Codex session did not return the attach command")
	}
	if _, ok := updated.resumingSessions[inst.ID]; ok {
		t.Fatal("first pane frame did not clear the resume guard")
	}
	if _, ok := updated.resumeAttachRequests[inst.ID]; ok {
		t.Fatal("first pane frame did not consume the attach intent")
	}
}

// Regression: readiness can arrive minutes after Enter. If navigation moved to
// another session in the meantime, the delayed event must make the resumed
// session available without stealing the user's terminal.
func TestCodexResumeReadyPaneDoesNotAttachAfterSelectionMoves(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	started := time.Now()
	home.resumingSessions[inst.ID] = started
	home.resumeAttachRequests[inst.ID] = started
	home.flatItems = []session.Item{{
		Type: session.ItemTypeSession,
		Session: &session.Instance{
			ID:    "another-session",
			Title: "another session",
			Tool:  "codex",
		},
	}}

	attached := false
	home.resumeAttachSink = func(*session.Instance) tea.Cmd {
		attached = true
		return nil
	}

	model, cmd := home.Update(sessionResumeReadinessMsg{
		sessionID: inst.ID,
		startedAt: started,
		content:   "Codex is ready",
		ready:     true,
	})
	updated := model.(*Home)

	if attached {
		t.Fatal("delayed readiness event attached after selection moved")
	}
	if _, ok := updated.resumingSessions[inst.ID]; ok {
		t.Fatal("ready pane did not clear the resume guard")
	}
	if _, ok := updated.resumeAttachRequests[inst.ID]; ok {
		t.Fatal("ready pane did not consume the stale attach intent")
	}
	if cmd == nil {
		t.Fatal("ready pane did not resume normal preview refresh")
	}
}

// Regression: a delayed probe from an older restart must not consume the
// attach intent belonging to a newer restart generation.
func TestCodexResumeStaleProbeCannotAttachNewGeneration(t *testing.T) {
	home, inst := newCodexResumeReadinessHome(t)
	oldStart := time.Now().Add(-time.Second)
	currentStart := time.Now()
	home.resumingSessions[inst.ID] = currentStart
	home.resumeAttachRequests[inst.ID] = currentStart
	home.flatItems = []session.Item{{
		Type:    session.ItemTypeSession,
		Session: inst,
	}}

	attached := false
	home.resumeAttachSink = func(*session.Instance) tea.Cmd {
		attached = true
		return nil
	}

	model, cmd := home.Update(sessionResumeReadinessMsg{
		sessionID: inst.ID,
		startedAt: oldStart,
		ready:     true,
	})
	updated := model.(*Home)

	if attached {
		t.Fatal("stale readiness probe attached the newer resume generation")
	}
	if cmd != nil {
		t.Fatal("stale readiness probe returned a command")
	}
	if got := updated.resumingSessions[inst.ID]; !got.Equal(currentStart) {
		t.Fatalf("stale probe changed resume generation: got %v, want %v", got, currentStart)
	}
	if got := updated.resumeAttachRequests[inst.ID]; !got.Equal(currentStart) {
		t.Fatalf("stale probe changed attach generation: got %v, want %v", got, currentStart)
	}
}
