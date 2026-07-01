package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestWebMutatorUpdateSessionReturnsNonFatalCodexSyncWarning(t *testing.T) {
	h, storage := newHeadlessHomeForTest(t, "_test_web_postcommit_warning")

	homeFile := filepath.Join(t.TempDir(), "codex-home-file")
	if err := os.WriteFile(homeFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write codex home file: %v", err)
	}
	t.Setenv("CODEX_HOME", homeFile)

	inst := session.NewInstanceWithTool("old-title", t.TempDir(), "codex")
	inst.ID = "web-postcommit-warning"
	inst.CodexSessionID = "66666666-6666-6666-6666-666666666666"
	if err := storage.SaveWithGroups([]*session.Instance{inst}, session.NewGroupTree([]*session.Instance{inst})); err != nil {
		t.Fatalf("seed SaveWithGroups: %v", err)
	}

	changed, restartRequired, err := NewWebMutator(h).UpdateSession(inst.ID, map[string]string{
		session.FieldTitle: "new-title",
	})

	if len(changed) != 1 || changed[0] != session.FieldTitle {
		t.Fatalf("changed = %#v, want title", changed)
	}
	if restartRequired {
		t.Fatal("title rename should not require restart")
	}
	var warning *session.NonFatalWarning
	if !errors.As(err, &warning) {
		t.Fatalf("err = %v, want NonFatalWarning", err)
	}
	if !strings.Contains(warning.Error(), "Codex session name") {
		t.Fatalf("warning = %v, want Codex session name context", warning)
	}

	reloaded, _, err := storage.LoadWithGroups()
	if err != nil {
		t.Fatalf("LoadWithGroups: %v", err)
	}
	if len(reloaded) != 1 || reloaded[0].Title != "new-title" {
		t.Fatalf("rename was not persisted despite warning: %#v", reloaded)
	}
}
