package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestCommandDispatcherRejectsUnknownAction(t *testing.T) {
	dispatcher := CommandDispatcher{}
	_, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "unknown",
	})
	if err == nil {
		t.Fatal("unknown action succeeded")
	}
}

func TestCommandDispatcherSendUsesSessionIDAndMessage(t *testing.T) {
	fake := &fakeActionBackend{}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(map[string]string{"session_id": "s1", "message": "run tests"})
	_, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "send",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch send: %v", err)
	}
	if fake.sentSessionID != "s1" || fake.sentMessage != "run tests" {
		t.Fatalf("fake = %+v", fake)
	}
}

func TestCommandDispatcherCreateUsesCreateRequest(t *testing.T) {
	fake := &fakeActionBackend{createSessionID: "created_1"}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(CreateSessionRequest{
		Title:           "deploy",
		Tool:            "codex",
		ProjectPath:     "/srv/app",
		AdditionalPaths: []string{"/srv/lib"},
		GroupPath:       "ops",
		ModelID:         "gpt-5",
	})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "create",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch create: %v", err)
	}
	if fake.createReq.Title != "deploy" || fake.createReq.Tool != "codex" || fake.createReq.ProjectPath != "/srv/app" || fake.createReq.GroupPath != "ops" || fake.createReq.ModelID != "gpt-5" {
		t.Fatalf("create request = %+v", fake.createReq)
	}
	if len(fake.createReq.AdditionalPaths) != 1 || fake.createReq.AdditionalPaths[0] != "/srv/lib" {
		t.Fatalf("create additional paths = %+v, want [/srv/lib]", fake.createReq.AdditionalPaths)
	}
	var result actionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode create result: %v", err)
	}
	if result.SessionID != "created_1" {
		t.Fatalf("create result session_id = %q, want created_1", result.SessionID)
	}
}

func TestCommandDispatcherDeleteUsesSessionID(t *testing.T) {
	fake := &fakeActionBackend{}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(map[string]string{"session_id": "s1"})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "delete",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch delete: %v", err)
	}
	if fake.deletedSessionID != "s1" {
		t.Fatalf("deleted session id = %q, want s1", fake.deletedSessionID)
	}
	var result actionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode delete result: %v", err)
	}
	if result.SessionID != "s1" {
		t.Fatalf("delete result session_id = %q, want s1", result.SessionID)
	}
}

func TestCommandDispatcherUndoDeleteUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{undoDeletedSessionID: "s1"}
	dispatcher := CommandDispatcher{Backend: fake}
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "undo_delete",
	})
	if err != nil {
		t.Fatalf("Dispatch undo_delete: %v", err)
	}
	if fake.lastAction != "undo_delete" {
		t.Fatalf("lastAction = %q, want undo_delete", fake.lastAction)
	}
	var result actionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode undo_delete result: %v", err)
	}
	if result.SessionID != "s1" {
		t.Fatalf("undo_delete result session_id = %q, want s1", result.SessionID)
	}
}

func TestCommandDispatcherForkReturnsNewSessionID(t *testing.T) {
	fake := &fakeActionBackend{forkedSessionID: "forked_1"}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(map[string]string{"session_id": "s1"})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "fork",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch fork: %v", err)
	}
	if fake.forkedFromSessionID != "s1" {
		t.Fatalf("forked from session id = %q, want s1", fake.forkedFromSessionID)
	}
	var result actionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode fork result: %v", err)
	}
	if result.SessionID != "forked_1" {
		t.Fatalf("fork result session_id = %q, want forked_1", result.SessionID)
	}
}

func TestCommandDispatcherForkWithOptionsUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{forkedSessionID: "forked_options_1"}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(ForkSessionRequest{
		SessionID:   "s1",
		Title:       "custom fork",
		GroupPath:   "ops",
		Worktree:    true,
		Branch:      "fork/custom",
		WithState:   true,
		WithIgnored: true,
		Sandbox:     true,
	})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "fork",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch fork options: %v", err)
	}
	if fake.forkOptionsReq.SessionID != "s1" ||
		fake.forkOptionsReq.Title != "custom fork" ||
		fake.forkOptionsReq.GroupPath != "ops" ||
		fake.forkOptionsReq.Branch != "fork/custom" ||
		!fake.forkOptionsReq.Worktree ||
		!fake.forkOptionsReq.WithState ||
		!fake.forkOptionsReq.WithIgnored ||
		!fake.forkOptionsReq.Sandbox {
		t.Fatalf("fork options request = %+v", fake.forkOptionsReq)
	}
	var result actionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode fork options result: %v", err)
	}
	if result.SessionID != "forked_options_1" {
		t.Fatalf("fork options result session_id = %q, want forked_options_1", result.SessionID)
	}
}

func TestPrepareHubForkWorktreeCreatesGitWorktree(t *testing.T) {
	isolateHubActionConfig(t)
	repo := newHubActionGitRepo(t)
	inst := &session.Instance{ID: "s1", Title: "API", ProjectPath: repo, Tool: "codex"}

	worktree, err := prepareHubForkWorktree(inst, ForkSessionRequest{Worktree: true, Branch: "fork/hub"}, "API fork", nil)
	if err != nil {
		t.Fatalf("prepareHubForkWorktree: %v", err)
	}
	defer worktree.rollback()

	if worktree.opts == nil {
		t.Fatal("worktree opts are nil")
	}
	if worktree.opts.WorktreeBranch != "fork/hub" {
		t.Fatalf("WorktreeBranch = %q, want fork/hub", worktree.opts.WorktreeBranch)
	}
	if worktree.opts.WorktreeRepoRoot == "" || worktree.opts.WorktreePath == "" || worktree.opts.WorkDir != worktree.opts.WorktreePath {
		t.Fatalf("worktree opts not populated: %+v", worktree.opts)
	}
	if worktree.backendType != "git" {
		t.Fatalf("backendType = %q, want git", worktree.backendType)
	}
	if _, err := os.Stat(worktree.opts.WorktreePath); err != nil {
		t.Fatalf("worktree path stat: %v", err)
	}
	if worktree.opts.WorktreePath == repo {
		t.Fatal("worktree path must not reuse the source repo path")
	}
}

func TestPrepareHubForkWorktreeWithStateCopiesParentFiles(t *testing.T) {
	isolateHubActionConfig(t)
	repo := newHubActionGitRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), "ignored.txt\n")
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-m", "ignore")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "modified\n")
	writeFile(t, filepath.Join(repo, "untracked.txt"), "new\n")
	writeFile(t, filepath.Join(repo, "ignored.txt"), "secret\n")
	inst := &session.Instance{ID: "s1", Title: "API", ProjectPath: repo, Tool: "codex"}

	worktree, err := prepareHubForkWorktree(inst, ForkSessionRequest{WithState: true, WithIgnored: true, Branch: "fork/state"}, "API state fork", nil)
	if err != nil {
		t.Fatalf("prepareHubForkWorktree with state: %v", err)
	}
	defer worktree.rollback()

	assertFileContent(t, filepath.Join(worktree.opts.WorktreePath, "tracked.txt"), "modified\n")
	assertFileContent(t, filepath.Join(worktree.opts.WorktreePath, "untracked.txt"), "new\n")
	assertFileContent(t, filepath.Join(worktree.opts.WorktreePath, "ignored.txt"), "secret\n")
}

func TestCommandDispatcherParityActionsUseBackend(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{"restart_fresh", "restart_fresh:s1"},
		{"archive", "archive:s1"},
		{"unarchive", "unarchive:s1"},
		{"remove", "remove:s1"},
		{"toggle_yolo", "toggle_yolo:s1"},
		{"mark_unread", "mark_unread:s1"},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			fake := &fakeActionBackend{}
			dispatcher := CommandDispatcher{Backend: fake}
			payload, _ := json.Marshal(map[string]string{"session_id": "s1"})
			_, err := dispatcher.Dispatch(context.Background(), CommandPayload{
				CommandID: "cmd_1",
				Action:    tc.action,
				Payload:   payload,
			})
			if err != nil {
				t.Fatalf("Dispatch %s: %v", tc.action, err)
			}
			if fake.lastAction != tc.want {
				t.Fatalf("lastAction = %q, want %q", fake.lastAction, tc.want)
			}
		})
	}
}

func TestCommandDispatcherMoveAndUpdateUseBackend(t *testing.T) {
	fake := &fakeActionBackend{}
	dispatcher := CommandDispatcher{Backend: fake}
	movePayload, _ := json.Marshal(map[string]string{"session_id": "s1", "group_path": "ops"})
	if _, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "move", Payload: movePayload}); err != nil {
		t.Fatalf("Dispatch move: %v", err)
	}
	if fake.movedSessionID != "s1" || fake.movedGroupPath != "ops" {
		t.Fatalf("move = session %q group %q", fake.movedSessionID, fake.movedGroupPath)
	}

	updatePayload, _ := json.Marshal(UpdateSessionRequest{
		SessionID: "s1",
		Changes:   []SessionFieldChange{{Field: "title", Value: "renamed"}},
	})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_2", Action: "update", Payload: updatePayload})
	if err != nil {
		t.Fatalf("Dispatch update: %v", err)
	}
	if fake.updateReq.SessionID != "s1" || len(fake.updateReq.Changes) != 1 || fake.updateReq.Changes[0].Value != "renamed" {
		t.Fatalf("update req = %+v", fake.updateReq)
	}
	var result UpdateSessionResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode update result: %v", err)
	}
	if !result.Restarted {
		t.Fatal("update result Restarted = false, want true")
	}
}

func TestCommandDispatcherUpdatePathsUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(UpdateSessionPathsRequest{
		SessionID: "s1",
		Paths:     []string{"/repo/a", "/repo/b"},
	})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "update_paths", Payload: payload})
	if err != nil {
		t.Fatalf("Dispatch update_paths: %v", err)
	}
	if fake.updatePathsReq.SessionID != "s1" || len(fake.updatePathsReq.Paths) != 2 || fake.updatePathsReq.Paths[1] != "/repo/b" {
		t.Fatalf("update paths req = %+v", fake.updatePathsReq)
	}
	var result UpdateSessionPathsResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode update_paths result: %v", err)
	}
	if !result.Restarted {
		t.Fatal("update_paths result Restarted = false, want true")
	}
}

func TestCommandDispatcherWorktreeFinishUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{
		worktreeFinishResp: WorktreeFinishResponse{
			SessionID:     "s1",
			Branch:        "fork/api",
			MergedInto:    "main",
			Merged:        true,
			BranchDeleted: true,
		},
	}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(WorktreeFinishRequest{
		SessionID:  "s1",
		Into:       "main",
		NoMerge:    false,
		KeepBranch: true,
		Force:      true,
	})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "worktree_finish", Payload: payload})
	if err != nil {
		t.Fatalf("Dispatch worktree_finish: %v", err)
	}
	if fake.worktreeFinishReq.SessionID != "s1" || fake.worktreeFinishReq.Into != "main" || !fake.worktreeFinishReq.KeepBranch || !fake.worktreeFinishReq.Force {
		t.Fatalf("worktree finish req = %+v", fake.worktreeFinishReq)
	}
	var result WorktreeFinishResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode worktree finish result: %v", err)
	}
	if result.Branch != "fork/api" || result.MergedInto != "main" || !result.Merged || !result.BranchDeleted {
		t.Fatalf("worktree finish result = %+v", result)
	}
}

func TestCommandDispatcherWorktreeSetupUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{
		worktreeSetupResp: WorktreeSetupResponse{SessionID: "s1"},
	}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(WorktreeSetupRequest{SessionID: "s1"})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "worktree_setup", Payload: payload})
	if err != nil {
		t.Fatalf("Dispatch worktree_setup: %v", err)
	}
	if fake.worktreeSetupReq.SessionID != "s1" {
		t.Fatalf("worktree setup req = %+v", fake.worktreeSetupReq)
	}
	var result WorktreeSetupResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode worktree setup result: %v", err)
	}
	if result.SessionID != "s1" {
		t.Fatalf("worktree setup result = %+v", result)
	}
}

func TestCommandDispatcherSandboxShellUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{
		sandboxShellResp: SandboxShellResponse{SessionID: "s1", AttachSessionID: "tmuxattach-token"},
	}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(SandboxShellRequest{SessionID: "s1"})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "sandbox_shell", Payload: payload})
	if err != nil {
		t.Fatalf("Dispatch sandbox_shell: %v", err)
	}
	if fake.sandboxShellReq.SessionID != "s1" {
		t.Fatalf("sandbox shell req = %+v", fake.sandboxShellReq)
	}
	var result SandboxShellResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode sandbox shell result: %v", err)
	}
	if result.SessionID != "s1" || result.AttachSessionID != "tmuxattach-token" {
		t.Fatalf("sandbox shell result = %+v", result)
	}
}

func TestCommandDispatcherGroupActionsUseBackend(t *testing.T) {
	fake := &fakeActionBackend{
		groupCreateResp:   GroupCreateResponse{Path: "ops/api", Name: "api", DefaultPath: "/srv/api", MaxConcurrent: 2},
		groupRenameResp:   GroupRenameResponse{OldPath: "ops/api", Path: "ops/backend", Name: "backend"},
		groupUpdateResp:   GroupUpdateResponse{Path: "ops/backend", DefaultPath: "/srv/backend", MaxConcurrent: 4},
		groupDeleteResp:   GroupDeleteResponse{Path: "ops/backend", SessionsMoved: 3, MovedTo: session.DefaultGroupPath},
		groupReparentResp: GroupReparentResponse{OldPath: "ops/backend", Path: "platform/backend", DestParentPath: "platform"},
		groupReorderResp:  GroupReorderResponse{Path: "platform/backend", FromPosition: 2, ToPosition: 1},
	}
	dispatcher := CommandDispatcher{Backend: fake}
	maxCreate := 2
	createPayload, _ := json.Marshal(GroupCreateRequest{
		Name:          "api",
		ParentPath:    "ops",
		DefaultPath:   "/srv/api",
		MaxConcurrent: &maxCreate,
	})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "group_create", Payload: createPayload})
	if err != nil {
		t.Fatalf("Dispatch group_create: %v", err)
	}
	if fake.groupCreateReq.Name != "api" || fake.groupCreateReq.ParentPath != "ops" || fake.groupCreateReq.DefaultPath != "/srv/api" || fake.groupCreateReq.MaxConcurrent == nil || *fake.groupCreateReq.MaxConcurrent != 2 {
		t.Fatalf("group create req = %+v", fake.groupCreateReq)
	}
	var createResult GroupCreateResponse
	if err := json.Unmarshal(raw, &createResult); err != nil {
		t.Fatalf("decode group create result: %v", err)
	}
	if createResult.Path != "ops/api" || createResult.MaxConcurrent != 2 {
		t.Fatalf("group create result = %+v", createResult)
	}

	renamePayload, _ := json.Marshal(GroupRenameRequest{GroupPath: "ops/api", Name: "backend"})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_2", Action: "group_rename", Payload: renamePayload})
	if err != nil {
		t.Fatalf("Dispatch group_rename: %v", err)
	}
	if fake.groupRenameReq.GroupPath != "ops/api" || fake.groupRenameReq.Name != "backend" {
		t.Fatalf("group rename req = %+v", fake.groupRenameReq)
	}
	var renameResult GroupRenameResponse
	if err := json.Unmarshal(raw, &renameResult); err != nil {
		t.Fatalf("decode group rename result: %v", err)
	}
	if renameResult.OldPath != "ops/api" || renameResult.Path != "ops/backend" {
		t.Fatalf("group rename result = %+v", renameResult)
	}

	defaultPath := "/srv/backend"
	maxUpdate := 4
	updatePayload, _ := json.Marshal(GroupUpdateRequest{GroupPath: "ops/backend", DefaultPath: &defaultPath, MaxConcurrent: &maxUpdate})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_3", Action: "group_update", Payload: updatePayload})
	if err != nil {
		t.Fatalf("Dispatch group_update: %v", err)
	}
	if fake.groupUpdateReq.GroupPath != "ops/backend" || fake.groupUpdateReq.DefaultPath == nil || *fake.groupUpdateReq.DefaultPath != "/srv/backend" || fake.groupUpdateReq.MaxConcurrent == nil || *fake.groupUpdateReq.MaxConcurrent != 4 {
		t.Fatalf("group update req = %+v", fake.groupUpdateReq)
	}
	var updateResult GroupUpdateResponse
	if err := json.Unmarshal(raw, &updateResult); err != nil {
		t.Fatalf("decode group update result: %v", err)
	}
	if updateResult.Path != "ops/backend" || updateResult.MaxConcurrent != 4 {
		t.Fatalf("group update result = %+v", updateResult)
	}

	deletePayload, _ := json.Marshal(GroupDeleteRequest{GroupPath: "ops/backend", Force: true})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_4", Action: "group_delete", Payload: deletePayload})
	if err != nil {
		t.Fatalf("Dispatch group_delete: %v", err)
	}
	if fake.groupDeleteReq.GroupPath != "ops/backend" || !fake.groupDeleteReq.Force {
		t.Fatalf("group delete req = %+v", fake.groupDeleteReq)
	}
	var deleteResult GroupDeleteResponse
	if err := json.Unmarshal(raw, &deleteResult); err != nil {
		t.Fatalf("decode group delete result: %v", err)
	}
	if deleteResult.Path != "ops/backend" || deleteResult.SessionsMoved != 3 {
		t.Fatalf("group delete result = %+v", deleteResult)
	}

	reparentPayload, _ := json.Marshal(GroupReparentRequest{GroupPath: "ops/backend", DestParentPath: "platform"})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_5", Action: "group_reparent", Payload: reparentPayload})
	if err != nil {
		t.Fatalf("Dispatch group_reparent: %v", err)
	}
	if fake.groupReparentReq.GroupPath != "ops/backend" || fake.groupReparentReq.DestParentPath != "platform" {
		t.Fatalf("group reparent req = %+v", fake.groupReparentReq)
	}
	var reparentResult GroupReparentResponse
	if err := json.Unmarshal(raw, &reparentResult); err != nil {
		t.Fatalf("decode group reparent result: %v", err)
	}
	if reparentResult.OldPath != "ops/backend" || reparentResult.Path != "platform/backend" {
		t.Fatalf("group reparent result = %+v", reparentResult)
	}

	pos := 1
	reorderPayload, _ := json.Marshal(GroupReorderRequest{GroupPath: "platform/backend", Position: &pos})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_6", Action: "group_reorder", Payload: reorderPayload})
	if err != nil {
		t.Fatalf("Dispatch group_reorder: %v", err)
	}
	if fake.groupReorderReq.GroupPath != "platform/backend" || fake.groupReorderReq.Position == nil || *fake.groupReorderReq.Position != 1 {
		t.Fatalf("group reorder req = %+v", fake.groupReorderReq)
	}
	var reorderResult GroupReorderResponse
	if err := json.Unmarshal(raw, &reorderResult); err != nil {
		t.Fatalf("decode group reorder result: %v", err)
	}
	if reorderResult.Path != "platform/backend" || reorderResult.FromPosition != 2 || reorderResult.ToPosition != 1 {
		t.Fatalf("group reorder result = %+v", reorderResult)
	}
}

func TestCommandDispatcherMCPActionsUseBackend(t *testing.T) {
	fake := &fakeActionBackend{
		mcpListResp:   MCPListResponse{SessionID: "s1", Local: []string{"exa"}, Global: []string{"memory"}, User: []string{"github"}},
		mcpAttachResp: MCPMutateResponse{SessionID: "s1", Name: "exa", Scope: "local"},
		mcpDetachResp: MCPMutateResponse{SessionID: "s1", Name: "exa", Scope: "global"},
		mcpMoveResp:   MCPMoveResponse{SessionID: "s1", Name: "exa", FromScope: "local", ToScope: "global"},
	}
	dispatcher := CommandDispatcher{Backend: fake}

	listPayload, _ := json.Marshal(MCPListRequest{SessionID: "s1"})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "mcp_list", Payload: listPayload})
	if err != nil {
		t.Fatalf("Dispatch mcp_list: %v", err)
	}
	if fake.mcpListReq.SessionID != "s1" {
		t.Fatalf("mcp list req = %+v", fake.mcpListReq)
	}
	var listResult MCPListResponse
	if err := json.Unmarshal(raw, &listResult); err != nil {
		t.Fatalf("decode mcp list result: %v", err)
	}
	if len(listResult.Local) != 1 || listResult.Local[0] != "exa" {
		t.Fatalf("mcp list result = %+v", listResult)
	}

	attachPayload, _ := json.Marshal(MCPMutateRequest{SessionID: "s1", Name: "exa", Scope: "local"})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_2", Action: "mcp_attach", Payload: attachPayload})
	if err != nil {
		t.Fatalf("Dispatch mcp_attach: %v", err)
	}
	if fake.mcpAttachReq.SessionID != "s1" || fake.mcpAttachReq.Name != "exa" || fake.mcpAttachReq.Scope != "local" {
		t.Fatalf("mcp attach req = %+v", fake.mcpAttachReq)
	}
	var attachResult MCPMutateResponse
	if err := json.Unmarshal(raw, &attachResult); err != nil {
		t.Fatalf("decode mcp attach result: %v", err)
	}
	if attachResult.Name != "exa" || attachResult.Scope != "local" {
		t.Fatalf("mcp attach result = %+v", attachResult)
	}

	detachPayload, _ := json.Marshal(MCPMutateRequest{SessionID: "s1", Name: "exa", Scope: "global"})
	if _, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_3", Action: "mcp_detach", Payload: detachPayload}); err != nil {
		t.Fatalf("Dispatch mcp_detach: %v", err)
	}
	if fake.mcpDetachReq.SessionID != "s1" || fake.mcpDetachReq.Name != "exa" || fake.mcpDetachReq.Scope != "global" {
		t.Fatalf("mcp detach req = %+v", fake.mcpDetachReq)
	}

	movePayload, _ := json.Marshal(MCPMoveRequest{SessionID: "s1", Name: "exa", FromScope: "local", ToScope: "global"})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_4", Action: "mcp_move", Payload: movePayload})
	if err != nil {
		t.Fatalf("Dispatch mcp_move: %v", err)
	}
	if fake.mcpMoveReq.SessionID != "s1" || fake.mcpMoveReq.Name != "exa" || fake.mcpMoveReq.FromScope != "local" || fake.mcpMoveReq.ToScope != "global" {
		t.Fatalf("mcp move req = %+v", fake.mcpMoveReq)
	}
	var moveResult MCPMoveResponse
	if err := json.Unmarshal(raw, &moveResult); err != nil {
		t.Fatalf("decode mcp move result: %v", err)
	}
	if moveResult.FromScope != "local" || moveResult.ToScope != "global" {
		t.Fatalf("mcp move result = %+v", moveResult)
	}
}

func TestCommandDispatcherSkillActionsUseBackend(t *testing.T) {
	fake := &fakeActionBackend{
		skillListResp: SkillListResponse{
			SessionID: "s1",
			Catalog:   []session.SkillCandidate{{ID: "pool/alpha", Name: "alpha", Source: "pool", Kind: "dir"}},
			Attached:  []session.ProjectSkillAttachment{{ID: "pool/beta", Name: "beta", Source: "pool"}},
		},
		skillAttachResp: SkillMutateResponse{
			SessionID: "s1",
			Skill:     &session.ProjectSkillAttachment{ID: "pool/alpha", Name: "alpha", Source: "pool"},
		},
		skillDetachResp: SkillMutateResponse{
			SessionID: "s1",
			Skill:     &session.ProjectSkillAttachment{ID: "pool/beta", Name: "beta", Source: "pool"},
		},
	}
	dispatcher := CommandDispatcher{Backend: fake}

	listPayload, _ := json.Marshal(SkillListRequest{SessionID: "s1"})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "skill_list", Payload: listPayload})
	if err != nil {
		t.Fatalf("Dispatch skill_list: %v", err)
	}
	if fake.skillListReq.SessionID != "s1" {
		t.Fatalf("skill list req = %+v", fake.skillListReq)
	}
	var listResult SkillListResponse
	if err := json.Unmarshal(raw, &listResult); err != nil {
		t.Fatalf("decode skill list result: %v", err)
	}
	if len(listResult.Catalog) != 1 || listResult.Catalog[0].Name != "alpha" || len(listResult.Attached) != 1 || listResult.Attached[0].Name != "beta" {
		t.Fatalf("skill list result = %+v", listResult)
	}

	attachPayload, _ := json.Marshal(SkillMutateRequest{SessionID: "s1", Name: "alpha", Source: "pool"})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_2", Action: "skill_attach", Payload: attachPayload})
	if err != nil {
		t.Fatalf("Dispatch skill_attach: %v", err)
	}
	if fake.skillAttachReq.SessionID != "s1" || fake.skillAttachReq.Name != "alpha" || fake.skillAttachReq.Source != "pool" {
		t.Fatalf("skill attach req = %+v", fake.skillAttachReq)
	}
	var attachResult SkillMutateResponse
	if err := json.Unmarshal(raw, &attachResult); err != nil {
		t.Fatalf("decode skill attach result: %v", err)
	}
	if attachResult.Skill == nil || attachResult.Skill.Name != "alpha" {
		t.Fatalf("skill attach result = %+v", attachResult)
	}

	detachPayload, _ := json.Marshal(SkillMutateRequest{SessionID: "s1", Name: "beta", Source: "pool"})
	if _, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_3", Action: "skill_detach", Payload: detachPayload}); err != nil {
		t.Fatalf("Dispatch skill_detach: %v", err)
	}
	if fake.skillDetachReq.SessionID != "s1" || fake.skillDetachReq.Name != "beta" || fake.skillDetachReq.Source != "pool" {
		t.Fatalf("skill detach req = %+v", fake.skillDetachReq)
	}
}

func TestCommandDispatcherPluginActionsUseBackend(t *testing.T) {
	fake := &fakeActionBackend{
		pluginListResp: PluginListResponse{
			SessionID: "s1",
			Catalog:   []PluginCatalogEntry{{Name: "octopus", ID: "octopus@local"}},
			Plugins:   []string{"discord"},
			Channels:  []string{"plugin:discord"},
		},
		pluginAttachResp: PluginMutateResponse{SessionID: "s1", Plugins: []string{"discord", "octopus"}},
		pluginDetachResp: PluginMutateResponse{SessionID: "s1", Plugins: []string{"discord"}},
	}
	dispatcher := CommandDispatcher{Backend: fake}

	listPayload, _ := json.Marshal(PluginListRequest{SessionID: "s1"})
	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_1", Action: "plugin_list", Payload: listPayload})
	if err != nil {
		t.Fatalf("Dispatch plugin_list: %v", err)
	}
	if fake.pluginListReq.SessionID != "s1" {
		t.Fatalf("plugin list req = %+v", fake.pluginListReq)
	}
	var listResult PluginListResponse
	if err := json.Unmarshal(raw, &listResult); err != nil {
		t.Fatalf("decode plugin list result: %v", err)
	}
	if len(listResult.Catalog) != 1 || listResult.Catalog[0].Name != "octopus" || len(listResult.Plugins) != 1 || listResult.Plugins[0] != "discord" {
		t.Fatalf("plugin list result = %+v", listResult)
	}

	attachPayload, _ := json.Marshal(PluginMutateRequest{SessionID: "s1", Name: "octopus", NoChannelLink: true})
	raw, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_2", Action: "plugin_attach", Payload: attachPayload})
	if err != nil {
		t.Fatalf("Dispatch plugin_attach: %v", err)
	}
	if fake.pluginAttachReq.SessionID != "s1" || fake.pluginAttachReq.Name != "octopus" || !fake.pluginAttachReq.NoChannelLink {
		t.Fatalf("plugin attach req = %+v", fake.pluginAttachReq)
	}
	var attachResult PluginMutateResponse
	if err := json.Unmarshal(raw, &attachResult); err != nil {
		t.Fatalf("decode plugin attach result: %v", err)
	}
	if len(attachResult.Plugins) != 2 || attachResult.Plugins[1] != "octopus" {
		t.Fatalf("plugin attach result = %+v", attachResult)
	}

	detachPayload, _ := json.Marshal(PluginMutateRequest{SessionID: "s1", Name: "octopus"})
	if _, err = dispatcher.Dispatch(context.Background(), CommandPayload{CommandID: "cmd_3", Action: "plugin_detach", Payload: detachPayload}); err != nil {
		t.Fatalf("Dispatch plugin_detach: %v", err)
	}
	if fake.pluginDetachReq.SessionID != "s1" || fake.pluginDetachReq.Name != "octopus" {
		t.Fatalf("plugin detach req = %+v", fake.pluginDetachReq)
	}
}

func TestLocalActionBackendGroupActionsPersist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	backend := LocalActionBackend{Profile: "hub-groups-test"}
	maxCreate := 2
	created, err := backend.CreateGroup(context.Background(), GroupCreateRequest{
		Name:          "api",
		ParentPath:    "",
		DefaultPath:   "/srv/api",
		MaxConcurrent: &maxCreate,
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if created.Path != "api" || created.DefaultPath != "/srv/api" || created.MaxConcurrent != 2 {
		t.Fatalf("created group = %+v", created)
	}

	renamed, err := backend.RenameGroup(context.Background(), GroupRenameRequest{GroupPath: "api", Name: "backend"})
	if err != nil {
		t.Fatalf("RenameGroup: %v", err)
	}
	if renamed.OldPath != "api" || renamed.Path != "backend" {
		t.Fatalf("renamed group = %+v", renamed)
	}

	defaultPath := "/srv/backend"
	maxUpdate := 4
	updated, err := backend.UpdateGroup(context.Background(), GroupUpdateRequest{
		GroupPath:     "backend",
		DefaultPath:   &defaultPath,
		MaxConcurrent: &maxUpdate,
	})
	if err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}
	if updated.DefaultPath != "/srv/backend" || updated.MaxConcurrent != 4 {
		t.Fatalf("updated group = %+v", updated)
	}

	if _, err := backend.CreateGroup(context.Background(), GroupCreateRequest{Name: "platform"}); err != nil {
		t.Fatalf("CreateGroup platform: %v", err)
	}
	reparented, err := backend.ReparentGroup(context.Background(), GroupReparentRequest{GroupPath: "backend", DestParentPath: "platform"})
	if err != nil {
		t.Fatalf("ReparentGroup: %v", err)
	}
	if reparented.OldPath != "backend" || reparented.Path != "platform/backend" || reparented.DestParentPath != "platform" {
		t.Fatalf("reparented group = %+v", reparented)
	}
	if _, err := backend.CreateGroup(context.Background(), GroupCreateRequest{Name: "worker", ParentPath: "platform"}); err != nil {
		t.Fatalf("CreateGroup platform/worker: %v", err)
	}
	reordered, err := backend.ReorderGroup(context.Background(), GroupReorderRequest{GroupPath: "platform/worker", Direction: "up"})
	if err != nil {
		t.Fatalf("ReorderGroup: %v", err)
	}
	if reordered.Path != "platform/worker" || reordered.FromPosition <= reordered.ToPosition {
		t.Fatalf("reordered group = %+v", reordered)
	}

	deleted, err := backend.DeleteGroup(context.Background(), GroupDeleteRequest{GroupPath: "platform/backend", Force: true})
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if deleted.Path != "platform/backend" {
		t.Fatalf("deleted group = %+v", deleted)
	}

	storage, err := session.NewStorageWithProfile("hub-groups-test")
	if err != nil {
		t.Fatalf("NewStorageWithProfile: %v", err)
	}
	defer storage.Close()
	_, groups, err := storage.LoadWithGroups()
	if err != nil {
		t.Fatalf("LoadWithGroups: %v", err)
	}
	for _, group := range groups {
		if group.Path == "platform/backend" {
			t.Fatalf("deleted group still persisted: %+v", group)
		}
	}
	if pos, _ := persistedHubGroupPosition(groups, "platform/worker"); pos != 0 {
		t.Fatalf("platform/worker persisted position = %d, want 0", pos)
	}
}

func TestLocalActionBackendRenameGroupRejectsCollision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	backend := LocalActionBackend{Profile: "hub-group-rename-collision-test"}
	if _, err := backend.CreateGroup(context.Background(), GroupCreateRequest{Name: "source"}); err != nil {
		t.Fatalf("CreateGroup source: %v", err)
	}
	if _, err := backend.CreateGroup(context.Background(), GroupCreateRequest{Name: "target"}); err != nil {
		t.Fatalf("CreateGroup target: %v", err)
	}

	if _, err := backend.RenameGroup(context.Background(), GroupRenameRequest{GroupPath: "source", Name: "target"}); !errors.Is(err, session.ErrGroupAlreadyExists) {
		t.Fatalf("RenameGroup collision error = %v, want ErrGroupAlreadyExists", err)
	}
}

func TestCommandDispatcherPreviewReturnsContent(t *testing.T) {
	fake := &fakeActionBackend{previewContent: "remote pane content"}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(map[string]string{"session_id": "s1"})

	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "preview",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch preview: %v", err)
	}
	if fake.previewSessionID != "s1" {
		t.Fatalf("preview session id = %q, want s1", fake.previewSessionID)
	}
	var result PreviewSessionResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode preview result: %v", err)
	}
	if result.Content != "remote pane content" {
		t.Fatalf("preview content = %q, want remote pane content", result.Content)
	}
}

func TestCommandDispatcherImportTmuxUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{importTmuxCount: 2}
	dispatcher := CommandDispatcher{Backend: fake}

	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "import_tmux",
	})
	if err != nil {
		t.Fatalf("Dispatch import_tmux: %v", err)
	}
	if !fake.importTmuxCalled {
		t.Fatal("ImportTmux was not called")
	}
	var result ImportTmuxSessionsResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode import_tmux result: %v", err)
	}
	if result.Imported != 2 {
		t.Fatalf("imported count = %d, want 2", result.Imported)
	}
}

func TestCommandDispatcherWebProxyUsesBackend(t *testing.T) {
	fake := &fakeActionBackend{
		webProxyResp: WebProxyResponse{
			StatusCode: 200,
			Header:     map[string][]string{"Content-Type": {"text/html"}},
			BodyB64:    base64.StdEncoding.EncodeToString([]byte("<html>remote</html>")),
		},
	}
	dispatcher := CommandDispatcher{Backend: fake}
	payload, _ := json.Marshal(WebProxyRequest{Method: "GET", Path: "/api/menu"})

	raw, err := dispatcher.Dispatch(context.Background(), CommandPayload{
		CommandID: "cmd_1",
		Action:    "web_proxy",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Dispatch web_proxy: %v", err)
	}
	if fake.webProxyReq.Path != "/api/menu" || fake.webProxyReq.Method != "GET" {
		t.Fatalf("web proxy req = %+v", fake.webProxyReq)
	}
	var result WebProxyResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode web proxy result: %v", err)
	}
	body, err := base64.StdEncoding.DecodeString(result.BodyB64)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(body) != "<html>remote</html>" {
		t.Fatalf("body = %q", body)
	}
}

func TestSanitizeWebProxyPathRejectsAbsoluteURL(t *testing.T) {
	if _, err := sanitizeWebProxyPath("http://example.com/api/menu"); err == nil {
		t.Fatal("absolute URL accepted")
	}
	if _, err := sanitizeWebProxyPath("//example.com/api/menu"); err == nil {
		t.Fatal("// URL accepted")
	}
}

func persistedHubGroupPosition(groups []*session.GroupData, groupPath string) (int, []string) {
	parent := hubParentGroupPath(groupPath)
	level := session.GetGroupLevel(groupPath)
	siblings := make([]*session.GroupData, 0)
	for _, group := range groups {
		if group == nil {
			continue
		}
		if hubParentGroupPath(group.Path) == parent && session.GetGroupLevel(group.Path) == level {
			siblings = append(siblings, group)
		}
	}
	sort.SliceStable(siblings, func(i, j int) bool {
		if siblings[i].Order != siblings[j].Order {
			return siblings[i].Order < siblings[j].Order
		}
		return siblings[i].Path < siblings[j].Path
	})
	paths := make([]string, len(siblings))
	for i, group := range siblings {
		paths[i] = group.Path
		if group.Path == groupPath {
			return i, paths
		}
	}
	return -1, paths
}

type fakeActionBackend struct {
	sentSessionID        string
	sentMessage          string
	createReq            CreateSessionRequest
	createSessionID      string
	deletedSessionID     string
	undoDeletedSessionID string
	forkedFromSessionID  string
	forkOptionsReq       ForkSessionRequest
	forkedSessionID      string
	previewSessionID     string
	previewContent       string
	importTmuxCalled     bool
	importTmuxCount      int
	lastAction           string
	movedSessionID       string
	movedGroupPath       string
	updateReq            UpdateSessionRequest
	updatePathsReq       UpdateSessionPathsRequest
	worktreeSetupReq     WorktreeSetupRequest
	worktreeSetupResp    WorktreeSetupResponse
	worktreeFinishReq    WorktreeFinishRequest
	worktreeFinishResp   WorktreeFinishResponse
	sandboxShellReq      SandboxShellRequest
	sandboxShellResp     SandboxShellResponse
	groupCreateReq       GroupCreateRequest
	groupCreateResp      GroupCreateResponse
	groupRenameReq       GroupRenameRequest
	groupRenameResp      GroupRenameResponse
	groupUpdateReq       GroupUpdateRequest
	groupUpdateResp      GroupUpdateResponse
	groupDeleteReq       GroupDeleteRequest
	groupDeleteResp      GroupDeleteResponse
	groupReparentReq     GroupReparentRequest
	groupReparentResp    GroupReparentResponse
	groupReorderReq      GroupReorderRequest
	groupReorderResp     GroupReorderResponse
	mcpListReq           MCPListRequest
	mcpListResp          MCPListResponse
	mcpAttachReq         MCPMutateRequest
	mcpAttachResp        MCPMutateResponse
	mcpDetachReq         MCPMutateRequest
	mcpDetachResp        MCPMutateResponse
	mcpMoveReq           MCPMoveRequest
	mcpMoveResp          MCPMoveResponse
	skillListReq         SkillListRequest
	skillListResp        SkillListResponse
	skillAttachReq       SkillMutateRequest
	skillAttachResp      SkillMutateResponse
	skillDetachReq       SkillMutateRequest
	skillDetachResp      SkillMutateResponse
	pluginListReq        PluginListRequest
	pluginListResp       PluginListResponse
	pluginAttachReq      PluginMutateRequest
	pluginAttachResp     PluginMutateResponse
	pluginDetachReq      PluginMutateRequest
	pluginDetachResp     PluginMutateResponse
	webProxyReq          WebProxyRequest
	webProxyResp         WebProxyResponse
}

func (b *fakeActionBackend) Send(_ context.Context, sessionID, message string) error {
	b.sentSessionID = sessionID
	b.sentMessage = message
	return nil
}

func (b *fakeActionBackend) Start(context.Context, string) error {
	return nil
}

func (b *fakeActionBackend) Stop(context.Context, string) error {
	return nil
}

func (b *fakeActionBackend) Restart(context.Context, string) error {
	return nil
}

func (b *fakeActionBackend) RestartFresh(_ context.Context, sessionID string) error {
	b.lastAction = "restart_fresh:" + sessionID
	return nil
}

func (b *fakeActionBackend) Fork(_ context.Context, sessionID string) (string, error) {
	b.forkedFromSessionID = sessionID
	return b.forkedSessionID, nil
}

func (b *fakeActionBackend) ForkWithOptions(_ context.Context, req ForkSessionRequest) (string, error) {
	b.forkOptionsReq = req
	return b.forkedSessionID, nil
}

func (b *fakeActionBackend) Rename(context.Context, string, string) error {
	return nil
}

func (b *fakeActionBackend) Create(_ context.Context, req CreateSessionRequest) (string, error) {
	b.createReq = req
	return b.createSessionID, nil
}

func (b *fakeActionBackend) Delete(_ context.Context, sessionID string) error {
	b.deletedSessionID = sessionID
	return nil
}

func (b *fakeActionBackend) UndoDelete(context.Context) (string, error) {
	b.lastAction = "undo_delete"
	return b.undoDeletedSessionID, nil
}

func (b *fakeActionBackend) Archive(_ context.Context, sessionID string) error {
	b.lastAction = "archive:" + sessionID
	return nil
}

func (b *fakeActionBackend) Unarchive(_ context.Context, sessionID string) error {
	b.lastAction = "unarchive:" + sessionID
	return nil
}

func (b *fakeActionBackend) Remove(_ context.Context, sessionID string) error {
	b.lastAction = "remove:" + sessionID
	return nil
}

func (b *fakeActionBackend) Move(_ context.Context, sessionID, groupPath string) error {
	b.movedSessionID = sessionID
	b.movedGroupPath = groupPath
	return nil
}

func (b *fakeActionBackend) Update(_ context.Context, req UpdateSessionRequest) (UpdateSessionResponse, error) {
	b.updateReq = req
	return UpdateSessionResponse{Restarted: true}, nil
}

func (b *fakeActionBackend) UpdatePaths(_ context.Context, req UpdateSessionPathsRequest) (UpdateSessionPathsResponse, error) {
	b.updatePathsReq = req
	return UpdateSessionPathsResponse{Restarted: true}, nil
}

func (b *fakeActionBackend) SetupWorktree(_ context.Context, req WorktreeSetupRequest) (WorktreeSetupResponse, error) {
	b.worktreeSetupReq = req
	return b.worktreeSetupResp, nil
}

func (b *fakeActionBackend) FinishWorktree(_ context.Context, req WorktreeFinishRequest) (WorktreeFinishResponse, error) {
	b.worktreeFinishReq = req
	return b.worktreeFinishResp, nil
}

func (b *fakeActionBackend) OpenSandboxShell(_ context.Context, req SandboxShellRequest) (SandboxShellResponse, error) {
	b.sandboxShellReq = req
	return b.sandboxShellResp, nil
}

func (b *fakeActionBackend) CreateGroup(_ context.Context, req GroupCreateRequest) (GroupCreateResponse, error) {
	b.groupCreateReq = req
	return b.groupCreateResp, nil
}

func (b *fakeActionBackend) RenameGroup(_ context.Context, req GroupRenameRequest) (GroupRenameResponse, error) {
	b.groupRenameReq = req
	return b.groupRenameResp, nil
}

func (b *fakeActionBackend) UpdateGroup(_ context.Context, req GroupUpdateRequest) (GroupUpdateResponse, error) {
	b.groupUpdateReq = req
	return b.groupUpdateResp, nil
}

func (b *fakeActionBackend) DeleteGroup(_ context.Context, req GroupDeleteRequest) (GroupDeleteResponse, error) {
	b.groupDeleteReq = req
	return b.groupDeleteResp, nil
}

func (b *fakeActionBackend) ReparentGroup(_ context.Context, req GroupReparentRequest) (GroupReparentResponse, error) {
	b.groupReparentReq = req
	return b.groupReparentResp, nil
}

func (b *fakeActionBackend) ReorderGroup(_ context.Context, req GroupReorderRequest) (GroupReorderResponse, error) {
	b.groupReorderReq = req
	return b.groupReorderResp, nil
}

func (b *fakeActionBackend) ListMCPs(_ context.Context, req MCPListRequest) (MCPListResponse, error) {
	b.mcpListReq = req
	return b.mcpListResp, nil
}

func (b *fakeActionBackend) AttachMCP(_ context.Context, req MCPMutateRequest) (MCPMutateResponse, error) {
	b.mcpAttachReq = req
	return b.mcpAttachResp, nil
}

func (b *fakeActionBackend) DetachMCP(_ context.Context, req MCPMutateRequest) (MCPMutateResponse, error) {
	b.mcpDetachReq = req
	return b.mcpDetachResp, nil
}

func (b *fakeActionBackend) MoveMCP(_ context.Context, req MCPMoveRequest) (MCPMoveResponse, error) {
	b.mcpMoveReq = req
	return b.mcpMoveResp, nil
}

func (b *fakeActionBackend) ListSkills(_ context.Context, req SkillListRequest) (SkillListResponse, error) {
	b.skillListReq = req
	return b.skillListResp, nil
}

func (b *fakeActionBackend) AttachSkill(_ context.Context, req SkillMutateRequest) (SkillMutateResponse, error) {
	b.skillAttachReq = req
	return b.skillAttachResp, nil
}

func (b *fakeActionBackend) DetachSkill(_ context.Context, req SkillMutateRequest) (SkillMutateResponse, error) {
	b.skillDetachReq = req
	return b.skillDetachResp, nil
}

func (b *fakeActionBackend) ListPlugins(_ context.Context, req PluginListRequest) (PluginListResponse, error) {
	b.pluginListReq = req
	return b.pluginListResp, nil
}

func (b *fakeActionBackend) AttachPlugin(_ context.Context, req PluginMutateRequest) (PluginMutateResponse, error) {
	b.pluginAttachReq = req
	return b.pluginAttachResp, nil
}

func (b *fakeActionBackend) DetachPlugin(_ context.Context, req PluginMutateRequest) (PluginMutateResponse, error) {
	b.pluginDetachReq = req
	return b.pluginDetachResp, nil
}

func (b *fakeActionBackend) ToggleYolo(_ context.Context, sessionID string) error {
	b.lastAction = "toggle_yolo:" + sessionID
	return nil
}

func (b *fakeActionBackend) MarkUnread(_ context.Context, sessionID string) error {
	b.lastAction = "mark_unread:" + sessionID
	return nil
}

func (b *fakeActionBackend) Preview(_ context.Context, sessionID string) (string, error) {
	b.previewSessionID = sessionID
	return b.previewContent, nil
}

func (b *fakeActionBackend) ImportTmux(context.Context) (int, error) {
	b.importTmuxCalled = true
	return b.importTmuxCount, nil
}

func (b *fakeActionBackend) ProxyWeb(_ context.Context, req WebProxyRequest) (WebProxyResponse, error) {
	b.webProxyReq = req
	return b.webProxyResp, nil
}

func isolateHubActionConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)
}

func newHubActionGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Agent Deck Test")
	runGit(t, repo, "config", "user.email", "agent-deck@example.invalid")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "base")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
