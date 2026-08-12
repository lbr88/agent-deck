// rename_title_lock_test.go — CLI contract tests for title locking on explicit
// renames (PR #1355 review follow-up). Fork title locking is covered through
// the shared cross-provider constructor and CLI Pi fork behavior tests.
//
// An explicit rename is user intent: it must set TitleLocked so the #572
// Claude-name sync (e.g. an auto-assigned plan title) can't revert it on the
// next hook event. Direct `inst.Title = ...` assignments bypass the SetField
// mutator that applies the lock.
//
// Why structural assertions instead of end-to-end handler invocation:
// handleRename calls os.Exit on every error path, and there is no
// runMain/TestHelperProcess subprocess harness in this package. We follow the
// extractFuncBody precedent from session_remove_kill_test.go.

package main

import (
	"os"
	"strings"
	"testing"
)

func mustExtractFuncBody(t *testing.T, file, funcName string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := extractFuncBody(string(src), funcName)
	if body == "" {
		t.Fatalf("could not extract %s body from %s — file layout changed?", funcName, file)
	}
	return body
}

// TestHandleRename_RoutesThroughSetField: `agent-deck rename` must apply the
// title via session.SetField (which sets TitleLocked), not by assigning
// inst.Title directly.
func TestHandleRename_RoutesThroughSetField(t *testing.T) {
	body := foldSpaces(mustExtractFuncBody(t, "main.go", "handleRename"))

	if !strings.Contains(body, "session.SetField(inst, session.FieldTitle, newTitle, nil)") {
		t.Error("handleRename must route the rename through session.SetField(FieldTitle) so TitleLocked is set")
	}
	if strings.Contains(body, "inst.Title = newTitle") {
		t.Error("handleRename must not assign inst.Title directly — that bypasses the TitleLocked mutator")
	}
}

func TestHandleRename_SyncsClaudeNameAfterSuccessfulSave(t *testing.T) {
	body := foldSpaces(mustExtractFuncBody(t, "main.go", "handleRename"))

	saveIdx := strings.Index(body, "storage.SaveWithGroups(instances, groupTree)")
	syncIdx := strings.Index(body, "session.SyncClaudeSessionNameForInstance(inst)")
	successIdx := strings.Index(body, "out.Success(")
	if saveIdx == -1 {
		t.Fatal("handleRename must persist the Agent Deck rename with SaveWithGroups")
	}
	if syncIdx == -1 {
		t.Fatal("handleRename must attempt Claude name sync after persisting Agent Deck rename")
	}
	if successIdx == -1 {
		t.Fatal("handleRename must report success after persistence")
	}
	if !(saveIdx < syncIdx && syncIdx < successIdx) {
		t.Fatalf("Claude name sync must run after SaveWithGroups and before success output (save=%d sync=%d success=%d)", saveIdx, syncIdx, successIdx)
	}
	if !strings.Contains(body, `fmt.Fprintf(os.Stderr, "Warning: Claude name sync failed: %v\n", syncErr)`) {
		t.Error("handleRename must print a nonfatal stderr warning when Claude name sync fails")
	}
}

func TestHandleSessionSetTitle_SyncsClaudeNameAfterSuccessfulSave(t *testing.T) {
	body := foldSpaces(mustExtractFuncBody(t, "session_cmd.go", "handleSessionSet"))

	saveIdx := strings.Index(body, "storage.SaveWithGroups(instances, groupTree)")
	gateIdx := strings.Index(body, "field == session.FieldTitle")
	syncIdx := strings.Index(body, "session.SyncClaudeSessionNameForInstance(inst)")
	successIdx := strings.Index(body, "out.Success(")
	if saveIdx == -1 {
		t.Fatal("handleSessionSet must persist the Agent Deck update with SaveWithGroups")
	}
	if gateIdx == -1 {
		t.Fatal("handleSessionSet must gate Claude name sync to title updates")
	}
	if syncIdx == -1 {
		t.Fatal("handleSessionSet title updates must attempt Claude name sync after persistence")
	}
	if successIdx == -1 {
		t.Fatal("handleSessionSet must report success after persistence")
	}
	if !(saveIdx < gateIdx && gateIdx < syncIdx && syncIdx < successIdx) {
		t.Fatalf("Claude name sync must run after SaveWithGroups and before success output, gated to title (save=%d gate=%d sync=%d success=%d)", saveIdx, gateIdx, syncIdx, successIdx)
	}
}
