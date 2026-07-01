package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHandleRenameWarnsWhenCodexNameSyncFails(t *testing.T) {
	profile := "_test_rename_postcommit_warning"
	storage, err := session.NewStorageWithProfile(profile)
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	inst := session.NewInstanceWithTool("old-title", t.TempDir(), "codex")
	inst.ID = "rename-postcommit-warning"
	inst.CodexSessionID = "66666666-6666-6666-6666-666666666666"
	if err := storage.SaveWithGroups([]*session.Instance{inst}, session.NewGroupTree([]*session.Instance{inst})); err != nil {
		t.Fatalf("seed SaveWithGroups: %v", err)
	}

	homeFile := filepath.Join(t.TempDir(), "codex-home-file")
	if err := os.WriteFile(homeFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write codex home file: %v", err)
	}
	t.Setenv("CODEX_HOME", homeFile)

	stderr := captureStderr(t, func() {
		handleRename(profile, []string{inst.ID, "new-title"})
	})

	if !strings.Contains(stderr, "Warning:") {
		t.Fatalf("stderr missing warning: %q", stderr)
	}
	if !strings.Contains(stderr, "Codex session name") {
		t.Fatalf("stderr missing Codex sync context: %q", stderr)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stderr pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}
	return string(out)
}
