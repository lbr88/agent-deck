package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type attachFailureProgram struct {
	home *Home
	cmd  tea.Cmd
}

func (m *attachFailureProgram) Init() tea.Cmd { return m.cmd }
func (m *attachFailureProgram) View() string  { return "" }
func (m *attachFailureProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(statusUpdateMsg); ok {
		_, _ = m.home.Update(msg)
		return m, tea.Quit
	}
	return m, nil
}

// Run the actual tea.Exec callback. A source-string assertion would miss the
// observed behavior: failed attach returns to the list with no visible reason.
func TestOmpFailedAttachReportsErrorToTUI(t *testing.T) {
	inst := session.NewInstanceWithTool("missing-omp-pane", t.TempDir(), "omp")
	h := newTestHomeWithItems(120, 30, []session.Item{{Type: session.ItemTypeSession, Session: inst}})
	h.instances = []*session.Instance{inst}
	h.instanceByID = map[string]*session.Instance{inst.ID: inst}
	h.groupTree = session.NewGroupTree(h.instances)
	model := &attachFailureProgram{home: h, cmd: h.attachSession(inst)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(strings.NewReader("")), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	if _, err := program.Run(); err != nil {
		t.Fatalf("failed to exercise attach callback: %v", err)
	}
	if h.err == nil {
		t.Fatal("failed OMP attach returned to the TUI without an error, leaving only a red X")
	}
	if h.isAttaching.Load() {
		t.Fatal("failed attach left the TUI in attaching state")
	}
}

func TestOmpRestartWaitsForActualPaneInsteadOfSyntheticReady(t *testing.T) {
	h, inst := newCodexResumeReadinessHome(t)
	inst.Tool = "omp"
	started := h.newSessionResumeGeneration(inst, true)
	probed := false
	h.resumeReadinessProbe = func(*session.Instance) (string, error) { probed = true; return "", nil }
	_, cmd := h.Update(sessionRestartedMsg{sessionID: inst.ID, startedAt: started})
	var readiness *sessionResumeReadinessMsg
	var execute func(tea.Cmd)
	execute = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, child := range msg {
				execute(child)
			}
		case sessionResumeReadinessMsg:
			readiness = &msg
		}
	}
	execute(cmd)
	if !probed || readiness == nil || readiness.ready {
		t.Fatalf("OMP restart treated tmux spawn as readiness: probed=%v result=%+v", probed, readiness)
	}
}

func TestOmpResumeSurvivesAnimationCleanupUntilReadinessDeadline(t *testing.T) {
	h, inst := newCodexResumeReadinessHome(t)
	inst.Tool = "omp"
	h.resumingSessions[inst.ID] = time.Now().Add(-6 * time.Second)
	expired := h.cleanupExpiredAnimations(h.resumingSessions, 20*time.Second, 5*time.Second, codexResumeReadyTimeout)
	if len(expired) != 0 {
		t.Fatal("animation cleanup abandoned OMP resume after five seconds, before its readiness deadline")
	}
}

func TestOmpDeathDuringResumeReportsImmediately(t *testing.T) {
	inst := session.NewInstanceWithTool("omp-dies-during-resume", t.TempDir(), "omp")
	ts := inst.GetTmuxSession()
	ts.RunCommandAsInitialProcess = true
	ts.OptionOverrides = map[string]string{"remain-on-exit": "on"}
	t.Cleanup(func() { _ = ts.Kill() })
	if err := ts.Start("printf 'omp-runtime-failure\\n'; exit 23"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !ts.IsPaneDead() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	h := newTestHomeWithItems(120, 30, []session.Item{{Type: session.ItemTypeSession, Session: inst}})
	h.instances = []*session.Instance{inst}
	h.instanceByID = map[string]*session.Instance{inst.ID: inst}
	started := h.newSessionResumeGeneration(inst, true)
	msg := h.probeSessionResumeReadiness(inst.ID, started, 0)()
	_, _ = h.Update(msg)
	if h.err == nil || !strings.Contains(h.err.Error(), "omp-runtime-failure") {
		t.Fatalf("proven OMP death hidden behind loading timeout: %v", h.err)
	}
	if _, exists := h.resumingSessions[inst.ID]; exists {
		t.Fatal("dead provider remains in resuming state")
	}
}

func TestOmpUnobservablePaneDoesNotOpenOldPreview(t *testing.T) {
	inst := session.NewInstanceWithTool("omp-missing-during-resume", t.TempDir(), "omp")
	_, err := captureResumePane(inst)
	if !errors.Is(err, errResumeObservationFailed) {
		t.Fatalf("missing pane did not produce an explicit observation failure: %v", err)
	}
	h := newTestHomeWithItems(120, 30, []session.Item{{Type: session.ItemTypeSession, Session: inst}})
	h.instances = []*session.Instance{inst}
	h.instanceByID = map[string]*session.Instance{inst.ID: inst}
	started := h.newSessionResumeGeneration(inst, true)
	msg := h.probeSessionResumeReadiness(inst.ID, started, 0)()
	_, _ = h.Update(msg)
	if h.err == nil || !errors.Is(h.err, errResumeObservationFailed) {
		t.Fatalf("TUI hid failed live observation: %v", h.err)
	}
	if _, exists := h.resumingSessions[inst.ID]; exists {
		t.Fatal("unobservable provider remains in resuming state")
	}
}

func TestOmpEnterShowsActualIdentityErrorBeforeLaunching(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	inst := session.NewInstanceWithTool("approval-candidates", t.TempDir(), "omp")
	dir := filepath.Join(homeDir, ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		data := `{"type":"session","id":"` + name + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := newTestHomeWithItems(120, 30, []session.Item{{Type: session.ItemTypeSession, Session: inst}})
	h.instances = []*session.Instance{inst}
	h.instanceByID = map[string]*session.Instance{inst.ID: inst}
	h.resumeSessionExists = func(*session.Instance) bool { return false }
	cmd := h.activateLocalSession(inst)
	if cmd == nil {
		t.Fatal("Enter did not attempt this session")
	}
	msg := cmd()
	if _, ok := msg.(sessionRestartedMsg); !ok {
		t.Fatalf("unexpected Enter result: %T", msg)
	}
	_, _ = h.Update(msg)
	if h.err == nil || !strings.Contains(h.err.Error(), "first.jsonl") || !strings.Contains(h.err.Error(), "second.jsonl") {
		t.Fatalf("Enter hid actual identity error: %v", h.err)
	}
	if inst.Exists() {
		t.Fatal("ambiguous session launched despite error")
	}
}

func TestOmpIdentityWarningSurvivesRemotePreviewTailLimit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := session.NewInstanceWithTool("omp-long-preview", t.TempDir(), "omp")
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"original", "branch"} {
		if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(`{"type":"session","id":"`+name+`"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ts := inst.GetTmuxSession()
	ts.RunCommandAsInitialProcess = true
	t.Cleanup(func() { _ = ts.Kill() })
	if err := ts.Start("printf 'history-line\\n%.0s' {1..250}; sleep 30"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		content, _ := ts.CaptureFullHistory()
		if strings.Count(content, "history-line") >= 250 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test pane did not emit long history")
		}
		time.Sleep(10 * time.Millisecond)
	}
	preview, err := inst.PreviewFull()
	if err != nil {
		t.Fatal(err)
	}
	visible := truncateRemotePreviewContent(preview)
	if !strings.Contains(visible, "original.jsonl") || !strings.Contains(visible, "branch.jsonl") {
		t.Fatal("remote preview tail truncation discarded the actionable OMP warning")
	}
}
