package session

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestResolveOpenCodeImportTargetPrefersExactIDOverTitle(t *testing.T) {
	sessions := []openCodeSessionMetadata{
		{ID: "ses_target", Title: "Session by ID"},
		{ID: "ses_other", Title: "ses_target"},
	}

	got, err := resolveOpenCodeImportTarget(sessions, "ses_target")
	if err != nil {
		t.Fatalf("resolveOpenCodeImportTarget: %v", err)
	}
	if got.ID != "ses_target" {
		t.Fatalf("resolved ID = %q, want exact ID match", got.ID)
	}
}

func TestResolveOpenCodeImportTargetAmbiguousTitleListsCandidateIDs(t *testing.T) {
	sessions := []openCodeSessionMetadata{
		{ID: "ses_one", Title: "Shared title"},
		{ID: "ses_two", Title: "Shared title"},
	}

	_, err := resolveOpenCodeImportTarget(sessions, "Shared title")
	if err == nil {
		t.Fatal("resolveOpenCodeImportTarget should fail on ambiguous titles")
	}
	msg := err.Error()
	for _, want := range []string{"ambiguous", "ses_one", "ses_two"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

func TestNewOpenCodeImportedInstanceUsesMetadataDefaults(t *testing.T) {
	projectPath := t.TempDir()
	fallbackPath := t.TempDir()

	inst, err := newOpenCodeImportedInstance(openCodeSessionMetadata{
		ID:        "ses_default123",
		Title:     "Saved OpenCode title",
		Directory: projectPath,
	}, OpenCodeImportOptions{
		FallbackProjectPath: fallbackPath,
	})
	if err != nil {
		t.Fatalf("newOpenCodeImportedInstance: %v", err)
	}

	if inst.Title != "Saved OpenCode title" {
		t.Fatalf("Title = %q, want metadata title", inst.Title)
	}
	if inst.ProjectPath != projectPath {
		t.Fatalf("ProjectPath = %q, want metadata directory %q", inst.ProjectPath, projectPath)
	}
	if inst.GroupPath != DefaultGroupPath {
		t.Fatalf("GroupPath = %q, want default group %q", inst.GroupPath, DefaultGroupPath)
	}
	if inst.Tool != "opencode" {
		t.Fatalf("Tool = %q, want opencode", inst.Tool)
	}
	if inst.Command != "opencode" {
		t.Fatalf("Command = %q, want opencode", inst.Command)
	}
	if inst.Status != StatusStopped {
		t.Fatalf("Status = %q, want stopped", inst.Status)
	}
	if inst.OpenCodeSessionID != "ses_default123" {
		t.Fatalf("OpenCodeSessionID = %q, want ses_default123", inst.OpenCodeSessionID)
	}
	if inst.OpenCodeDetectedAt.IsZero() {
		t.Fatal("OpenCodeDetectedAt must be stamped")
	}
}

func TestNewOpenCodeImportedInstanceUsesExplicitOverrides(t *testing.T) {
	projectPath := t.TempDir()

	inst, err := newOpenCodeImportedInstance(openCodeSessionMetadata{
		ID:        "ses_override123",
		Title:     "Metadata title",
		Directory: t.TempDir(),
	}, OpenCodeImportOptions{
		Title:               "Explicit title",
		GroupPath:           "work/backend",
		ProjectPath:         projectPath,
		FallbackProjectPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("newOpenCodeImportedInstance: %v", err)
	}

	if inst.Title != "Explicit title" {
		t.Fatalf("Title = %q, want explicit title", inst.Title)
	}
	if inst.ProjectPath != projectPath {
		t.Fatalf("ProjectPath = %q, want explicit path %q", inst.ProjectPath, projectPath)
	}
	if inst.GroupPath != "work/backend" {
		t.Fatalf("GroupPath = %q, want explicit group", inst.GroupPath)
	}
}

func TestNewOpenCodeImportedInstanceRejectsUnsafeMetadataID(t *testing.T) {
	_, err := newOpenCodeImportedInstance(openCodeSessionMetadata{
		ID:        "ses_bad;touch",
		Title:     "Unsafe",
		Directory: t.TempDir(),
	}, OpenCodeImportOptions{FallbackProjectPath: t.TempDir()})
	if err == nil {
		t.Fatal("newOpenCodeImportedInstance should reject unsafe OpenCode IDs")
	}
	if !strings.Contains(err.Error(), "shell-safe identifier") {
		t.Fatalf("error = %q, want shell-safe identifier guidance", err.Error())
	}
}

func TestImportOpenCodeSessionQueriesFakeCLI(t *testing.T) {
	projectPath := t.TempDir()
	fakeDir := installFakeOpenCodeSessionList(t, `[
		{"id":"ses_cli123","title":"CLI saved","path":"`+projectPath+`","created":1768982195000,"updated":1768982200000}
	]`)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	inst, err := ImportOpenCodeSession(context.Background(), "CLI saved", OpenCodeImportOptions{
		FallbackProjectPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ImportOpenCodeSession: %v", err)
	}
	if inst.OpenCodeSessionID != "ses_cli123" {
		t.Fatalf("OpenCodeSessionID = %q, want ses_cli123", inst.OpenCodeSessionID)
	}
	if inst.ProjectPath != projectPath {
		t.Fatalf("ProjectPath = %q, want %q", inst.ProjectPath, projectPath)
	}
}
