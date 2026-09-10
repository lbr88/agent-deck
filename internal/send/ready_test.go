package send

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

type mockReadyChecker struct {
	statuses []string
	statusIx atomic.Int64
	pane     string
}

func (m *mockReadyChecker) GetStatus() (string, error) {
	i := int(m.statusIx.Add(1)) - 1
	if i >= len(m.statuses) {
		return m.statuses[len(m.statuses)-1], nil
	}
	return m.statuses[i], nil
}

func (m *mockReadyChecker) CapturePaneFresh() (string, error) {
	return m.pane, nil
}

// Cursor can be interactive while GetStatus still returns "starting" during
// the tmux startup window (no activity-timestamp change → no prompt re-check).
func TestWaitForAgentReady_StartingWithCursorPrompt(t *testing.T) {
	mock := &mockReadyChecker{
		statuses: []string{"starting"},
		pane: strings.Join([]string{
			"Cursor Agent",
			"How can I help you today?",
			"› implement the feature",
			"Plan mode · Switch modes",
		}, "\n"),
	}

	err := WaitForAgentReady(mock, "cursor", 2*time.Second, PromptGates{})
	if err != nil {
		t.Fatalf("expected ready via startup prompt probe, got: %v", err)
	}
}

func TestWaitForAgentReady_StartingWithoutPromptTimesOut(t *testing.T) {
	mock := &mockReadyChecker{
		statuses: []string{"starting"},
		pane:     "Loading...\n",
	}

	err := WaitForAgentReady(mock, "cursor", 400*time.Millisecond, PromptGates{})
	if err == nil {
		t.Fatal("expected timeout when starting with no prompt")
	}
}

func TestWaitForAgentReady_CursorPromptDetector(t *testing.T) {
	content := "› ask anything\nPlan mode"
	d := tmux.NewPromptDetector("cursor")
	if !d.HasPrompt(content) {
		t.Fatalf("cursor detector should match › prompt, content:\n%s", content)
	}
}

type neverReadyChecker struct {
	calls atomic.Int64
}

func (m *neverReadyChecker) GetStatus() (string, error) {
	m.calls.Add(1)
	return "active", nil
}

func (m *neverReadyChecker) CapturePaneFresh() (string, error) {
	return "", nil
}

func TestWaitForAgentReady_RespectsTimeout(t *testing.T) {
	mock := &neverReadyChecker{}
	requested := 1 * time.Second
	start := time.Now()
	err := WaitForAgentReady(mock, "shell", requested, PromptGates{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error, got nil (elapsed=%v)", elapsed)
	}
	if elapsed > 3*requested {
		t.Fatalf("timeout ignored: elapsed=%v requested=%v", elapsed, requested)
	}
	lower := requested / 2
	if elapsed < lower {
		t.Fatalf("returned too quickly: elapsed=%v requested=%v lower=%v", elapsed, requested, lower)
	}
	if mock.calls.Load() == 0 {
		t.Error("expected GetStatus to be polled")
	}
}

type deadReadyChecker struct{ mockReadyChecker }

func (*deadReadyChecker) Exists() bool     { return true }
func (*deadReadyChecker) IsPaneDead() bool { return true }

func TestWaitForAgentReadyReturnsProviderDeathNotLongTimeout(t *testing.T) {
	checker := &deadReadyChecker{mockReadyChecker{statuses: []string{"error"}, pane: "approval-history-invalid"}}
	start := time.Now()
	err := WaitForAgentReady(checker, "omp", 2*time.Second, PromptGates{})
	if err == nil || !strings.Contains(err.Error(), "exited") || !strings.Contains(err.Error(), "approval-history-invalid") {
		t.Fatalf("provider exit lost: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("waited for readiness after the provider died")
	}
}

type freshReadyChecker struct {
	mockReadyChecker
	probes   int
	failAt   int
	probeErr error
}

func (*freshReadyChecker) Exists() bool     { return true }
func (*freshReadyChecker) IsPaneDead() bool { return false }
func (m *freshReadyChecker) PrimaryPaneAliveFresh() (bool, error) {
	m.probes++
	return m.probes < m.failAt, m.probeErr
}

func TestOmpMessageReadinessRejectsStaleCacheAndSettlingDeath(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
		err    error
	}{
		{"dead despite positive cache", 1, nil},
		{"query failure despite positive cache", 1, errors.New("fresh pane query failed")},
		{"death during ready settling delay", 3, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := &freshReadyChecker{mockReadyChecker: mockReadyChecker{statuses: []string{"active", "waiting"}, pane: "provider failure"}, failAt: tc.failAt, probeErr: tc.err}
			err := WaitForAgentReady(checker, "omp", 2*time.Second, PromptGates{})
			if err == nil || !strings.Contains(err.Error(), "message was not sent") {
				t.Fatalf("OMP accepted stale liveness before sending: %v", err)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("lost fresh probe failure: %v", err)
			}
		})
	}
}
