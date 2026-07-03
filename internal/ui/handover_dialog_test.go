package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHandoverDialog_ShowDefaultsAndExcludesSourceTool(t *testing.T) {
	source := session.NewInstanceWithGroupAndTool("SERV-220", "/repo", "domutech", "codex")
	source.CodexSessionID = "019f12ae-037a-7cd1-b49e-18808bf7f48d"

	d := NewHandoverDialog()
	d.Show(source)

	if !d.IsVisible() {
		t.Fatal("dialog should be visible")
	}
	if strings.Join(d.targetOptions, ",") != "claude,opencode,kiro" {
		t.Fatalf("targetOptions = %v, want claude/opencode/kiro without codex", d.targetOptions)
	}
	if d.titleInput.Value() != "SERV-220 (claude)" {
		t.Fatalf("title default = %q", d.titleInput.Value())
	}
	if d.pathInput.Value() != "/repo" || d.groupInput.Value() != "domutech" {
		t.Fatalf("path/group defaults = %q/%q", d.pathInput.Value(), d.groupInput.Value())
	}
	view := d.View()
	if !strings.Contains(view, "019f12ae") || strings.Contains(view, "037a-7cd1-b49e") {
		t.Fatalf("view should show shortened source tool id, got:\n%s", view)
	}
}

func TestHomeHandoverActionOpensDialogWithPrefix(t *testing.T) {
	h := NewHome()
	source := session.NewInstanceWithGroupAndTool("source", "/repo", "grp", "claude")
	h.instances = []*session.Instance{source}
	h.instanceByID = map[string]*session.Instance{source.ID: source}
	h.groupTree = session.NewGroupTree(h.instances)
	h.rebuildFlatItems()
	h.moveCursorToSession(source.ID)

	model, _ := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	h = model.(*Home)
	if !h.sessionActionPrefix {
		t.Fatal("P should arm the session action prefix")
	}
	model, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	h = model.(*Home)
	if h.handoverDialog == nil || !h.handoverDialog.IsVisible() {
		t.Fatal("P then h should open the handover dialog")
	}
	if h.editSessionDialog.IsVisible() {
		t.Fatal("P then h should not open the edit session dialog")
	}
}

func TestHomeHandoverActionRendersDialog(t *testing.T) {
	h := NewHome()
	h.initialLoading = false
	h.width = 100
	h.height = 40
	source := session.NewInstanceWithGroupAndTool("source", "/repo", "grp", "claude")
	h.instances = []*session.Instance{source}
	h.instanceByID = map[string]*session.Instance{source.ID: source}
	h.groupTree = session.NewGroupTree(h.instances)
	h.rebuildFlatItems()
	h.moveCursorToSession(source.ID)

	model, _ := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	h = model.(*Home)
	model, _ = h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	h = model.(*Home)

	if got := h.View(); !strings.Contains(got, "Hand Over Session") {
		t.Fatalf("handover dialog state was opened but not rendered:\n%s", got)
	}
}

func TestHomeHandoverDialogCountsAsModal(t *testing.T) {
	h := NewHome()
	source := session.NewInstanceWithGroupAndTool("source", "/repo", "grp", "claude")
	h.handoverDialog.Show(source)

	if !h.hasModalVisible() {
		t.Fatal("handover dialog should count as a visible modal")
	}
}

func TestHomeHandoverConfirmCreatesPersistedStoppedTarget(t *testing.T) {
	h := NewHome()
	source := session.NewInstanceWithGroupAndTool("source", t.TempDir(), "grp", "claude")
	h.instances = []*session.Instance{source}
	h.instanceByID = map[string]*session.Instance{source.ID: source}
	h.groupTree = session.NewGroupTree(h.instances)
	h.rebuildFlatItems()
	h.handoverDialog.Show(source)

	_, cmd := h.handleHandoverDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should return handover command")
	}
	msg := cmd()
	created, ok := msg.(sessionHandoverCreatedMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want sessionHandoverCreatedMsg", msg)
	}
	if created.instance == nil {
		t.Fatal("created instance is nil")
	}
	if created.instance.Tool != "codex" || created.instance.Status != session.StatusStopped {
		t.Fatalf("created target tool/status = %q/%q, want codex/stopped", created.instance.Tool, created.instance.Status)
	}

	model, _ := h.Update(created)
	h = model.(*Home)
	if len(h.instances) != 2 {
		t.Fatalf("instances = %d, want 2", len(h.instances))
	}
	if h.instances[1].ID != created.instance.ID {
		t.Fatalf("new instance not appended")
	}
}

func TestHomeHandoverStartErrorKeepsCreatedRow(t *testing.T) {
	old := startHandoverTarget
	t.Cleanup(func() { startHandoverTarget = old })
	startHandoverTarget = func(*session.Instance, string) error {
		return fmt.Errorf("boom")
	}

	h := NewHome()
	source := session.NewInstanceWithGroupAndTool("source", t.TempDir(), "grp", "claude")
	h.instances = []*session.Instance{source}
	h.instanceByID = map[string]*session.Instance{source.ID: source}
	h.groupTree = session.NewGroupTree(h.instances)
	h.rebuildFlatItems()
	h.handoverDialog.Show(source)
	h.handoverDialog.startNow = true

	_, cmd := h.handleHandoverDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
	created := cmd().(sessionHandoverCreatedMsg)
	if created.instance == nil || created.err == nil {
		t.Fatalf("created = %+v, want instance and start error", created)
	}

	model, _ := h.Update(created)
	h = model.(*Home)
	if len(h.instances) != 2 {
		t.Fatalf("instances = %d, want created row kept after start error", len(h.instances))
	}
	if h.err == nil || !strings.Contains(h.err.Error(), "boom") {
		t.Fatalf("h.err = %v, want start error surfaced", h.err)
	}
}

func TestHelpIncludesHandoverAction(t *testing.T) {
	h := NewHelpOverlay()
	h.SetSize(100, 100)
	h.Show()
	if got := h.View(); !strings.Contains(got, "P h") || !strings.Contains(strings.ToLower(got), "handover") {
		t.Fatalf("help missing P h handover action:\n%s", got)
	}
}
