package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestListOpenCode(t *testing.T) {
	projectPath := t.TempDir()
	fakeDir := installFakeOpenCodeSessionList(t, `[
		{"id":"ses_cli123","title":"CLI saved","directory":"`+projectPath+`","updated":1768982200000}
	]`)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	entries, err := ListOpenCodeImportEntries(context.Background(), "")
	if err != nil {
		t.Fatalf("ListOpenCodeImportEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].ID != "ses_cli123" {
		t.Fatalf("entries[0].ID = %q, want ses_cli123", entries[0].ID)
	}
	if entries[0].Directory != projectPath {
		t.Fatalf("entries[0].Directory = %q, want %q", entries[0].Directory, projectPath)
	}
}

func installFakeOpenCodeSessionList(t *testing.T, payload string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"list\" ] && [ \"$3\" = \"--format\" ] && [ \"$4\" = \"json\" ]; then\n" +
		"cat <<'JSON'\n" +
		payload + "\n" +
		"JSON\n" +
		"exit 0\n" +
		"fi\n" +
		"exit 64\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake opencode: %v", err)
	}
	return dir
}
