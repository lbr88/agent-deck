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
	if strings.Join(d.targetOptions, ",") != "claude,opencode,kiro,omp" {
		t.Fatalf("targetOptions = %v, want claude/opencode/kiro/omp without codex", d.targetOptions)
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

func TestHandoverDialog_OmpSourceOffersEveryOtherSupportedTarget(t *testing.T) {
	source := session.NewInstanceWithGroupAndTool("approval", "/repo", "ticm", "omp")

	d := NewHandoverDialog()
	d.Show(source)

	if strings.Join(d.targetOptions, ",") != "claude,codex,opencode,kiro" {
		t.Fatalf("targetOptions = %v, want every supported target except omp", d.targetOptions)
	}
	if d.sourceTool != "omp" {
		t.Fatalf("sourceTool = %q, want omp", d.sourceTool)
	}
	if d.sourceToolID != "" {
		t.Fatalf("sourceToolID = %q, want deferred OMP lookup", d.sourceToolID)
	}
}

func TestHandoverDialog_SetSourceToolIDRejectsStaleResult(t *testing.T) {
	first := session.NewInstanceWithGroupAndTool("first", "/repo", "ticm", "omp")
	second := session.NewInstanceWithGroupAndTool("second", "/repo", "ticm", "omp")

	d := NewHandoverDialog()
	d.Show(first)
	d.Show(second)

	d.SetSourceToolID(first.ID, "stale-provider-id")
	if d.sourceToolID != "" {
		t.Fatalf("sourceToolID = %q after stale result, want empty", d.sourceToolID)
	}

	d.SetSourceToolID(second.ID, "current-provider-id")
	if d.sourceToolID != "current-provider-id" {
		t.Fatalf("sourceToolID = %q, want current-provider-id", d.sourceToolID)
	}

	d.Hide()
	d.SetSourceToolID(second.ID, "late-provider-id")
	if d.sourceToolID != "" {
		t.Fatalf("sourceToolID = %q after hidden result, want empty", d.sourceToolID)
	}
}

func TestHomeHandoverActionOpensDialogDirectly(t *testing.T) {
	h := NewHome()
	source := session.NewInstanceWithGroupAndTool("source", "/repo", "grp", "claude")
	h.instances = []*session.Instance{source}
	h.instanceByID = map[string]*session.Instance{source.ID: source}
	h.groupTree = session.NewGroupTree(h.instances)
	h.rebuildFlatItems()
	h.moveCursorToSession(source.ID)

	model, _ := h.dispatchAction(ActionHandover)
	h = model.(*Home)
	if h.handoverDialog == nil || !h.handoverDialog.IsVisible() {
		t.Fatal("handover action should open the handover dialog")
	}
	if h.editSessionDialog.IsVisible() {
		t.Fatal("handover action should not open the edit session dialog")
	}
}

func TestHomeHandoverActionDefersOmpSourceIdentityLookup(t *testing.T) {
	old := resolveHandoverSourceToolID
	t.Cleanup(func() { resolveHandoverSourceToolID = old })
	calls := 0
	resolveHandoverSourceToolID = func(source *session.Instance) string {
		calls++
		if source.Title != "source" {
			t.Fatalf("resolver source title = %q, want source", source.Title)
		}
		return "omp-provider-id"
	}

	h := NewHome()
	source := session.NewInstanceWithGroupAndTool("source", "/repo", "grp", "omp")
	h.instances = []*session.Instance{source}
	h.instanceByID = map[string]*session.Instance{source.ID: source}
	h.groupTree = session.NewGroupTree(h.instances)
	h.rebuildFlatItems()
	h.moveCursorToSession(source.ID)

	model, cmd := h.dispatchAction(ActionHandover)
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("OMP handover action should return deferred identity lookup command")
	}
	if calls != 0 {
		t.Fatalf("resolver calls before command execution = %d, want 0", calls)
	}
	if h.handoverDialog.sourceToolID != "" {
		t.Fatalf("sourceToolID before command execution = %q, want empty", h.handoverDialog.sourceToolID)
	}

	msg := cmd()
	if calls != 1 {
		t.Fatalf("resolver calls after command execution = %d, want 1", calls)
	}
	model, _ = h.Update(msg)
	h = model.(*Home)
	if h.handoverDialog.sourceToolID != "omp-provider-id" {
		t.Fatalf("sourceToolID after result = %q, want omp-provider-id", h.handoverDialog.sourceToolID)
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

	model, _ := h.dispatchAction(ActionHandover)
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

func TestActionCatalogIncludesHandover(t *testing.T) {
	for _, definition := range actionDefinitions() {
		if definition.ID == ActionHandover && strings.Contains(strings.ToLower(definition.Label), "hand over") {
			return
		}
	}
	t.Fatal("action catalog missing handover action")
}
