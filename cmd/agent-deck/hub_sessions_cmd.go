package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

type hubSessionOptions struct {
	Action          string
	NodeID          string
	NodeName        string
	SessionID       string
	SessionTitle    string
	Title           string
	Tool            string
	CWD             string
	Group           string
	ModelID         string
	AdditionalPaths []string
	Message         string
	Notes           string
	Attach          bool
	Worktree        bool
	Branch          string
	Into            string
	NoMerge         bool
	KeepBranch      bool
	Force           bool
	WithState       bool
	WithIgnored     bool
	Sandbox         bool
	SandboxImage    string
}

type hubSessionCommandResult struct {
	Action       string `json:"action"`
	NodeID       string `json:"node_id"`
	NodeName     string `json:"node_name,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	SessionTitle string `json:"session_title,omitempty"`
}

type hubSessionListResult struct {
	Sessions []hubSessionRow `json:"sessions"`
}

type hubSessionRow struct {
	NodeID           string `json:"node_id"`
	NodeName         string `json:"node_name,omitempty"`
	ID               string `json:"id"`
	Title            string `json:"title"`
	Tool             string `json:"tool,omitempty"`
	Status           string `json:"status,omitempty"`
	GroupPath        string `json:"group_path,omitempty"`
	ProjectPath      string `json:"project_path,omitempty"`
	DisplaySessionID string `json:"display_session_id,omitempty"`
}

var errHubSessionNotFound = errors.New("hub session not found")

func handleHubSessions(profile string, args []string) error {
	if len(args) == 0 || args[0] == "list" || args[0] == "ls" {
		return handleHubSessionsList(profile, args)
	}
	switch args[0] {
	case "create", "new":
		return handleHubSessionsCreate(profile, args[1:])
	case "attach":
		return handleHubSessionsSimple(profile, "attach", args[1:])
	case "sandbox-shell", "exec-shell", "shell":
		return handleHubSessionsSandboxShell(profile, args[1:])
	case "send":
		return handleHubSessionsSend(profile, args[1:])
	case "approve":
		return handleHubSessionsSimple(profile, "approve", args[1:])
	case "notes":
		return handleHubSessionsNotes(profile, args[1:])
	case "start":
		return handleHubSessionsSimple(profile, "start", args[1:])
	case "close", "stop":
		return handleHubSessionsSimple(profile, "stop", args[1:])
	case "restart":
		return handleHubSessionsSimple(profile, "restart", args[1:])
	case "restart-fresh":
		return handleHubSessionsSimple(profile, "restart_fresh", args[1:])
	case "fork":
		return handleHubSessionsFork(profile, args[1:])
	case "worktree-setup", "setup-worktree":
		return handleHubSessionsSimple(profile, "worktree_setup", args[1:])
	case "worktree-finish", "finish-worktree":
		return handleHubSessionsWorktreeFinish(profile, args[1:])
	case "delete":
		return handleHubSessionsSimple(profile, "delete", args[1:])
	case "undo-delete", "undelete":
		return handleHubSessionsUndoDelete(profile, args[1:])
	case "archive":
		return handleHubSessionsSimple(profile, "archive", args[1:])
	case "unarchive":
		return handleHubSessionsSimple(profile, "unarchive", args[1:])
	case "remove":
		return handleHubSessionsSimple(profile, "remove", args[1:])
	case "toggle-yolo":
		return handleHubSessionsSimple(profile, "toggle_yolo", args[1:])
	case "unread", "mark-unread":
		return handleHubSessionsSimple(profile, "mark_unread", args[1:])
	case "rename":
		return handleHubSessionsRename(profile, args[1:])
	case "move":
		return handleHubSessionsMove(profile, args[1:])
	case "preview", "output":
		return handleHubSessionsPreview(profile, args[1:])
	case "help", "--help", "-h":
		printHubSessionsUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown hub sessions command %q", args[0])
	}
}

func handleHubSessionsList(profile string, args []string) error {
	if len(args) > 0 && (args[0] == "list" || args[0] == "ls") {
		args = args[1:]
	}
	fs := flag.NewFlagSet("hub sessions list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output sessions as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 2*time.Second, "Maximum time to wait for session snapshots")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions [list] [node-id-or-name] [--json]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: agent-deck hub sessions [list] [node-id-or-name] [--json]")
	}
	nodeSelector := ""
	if fs.NArg() == 1 {
		nodeSelector = fs.Arg(0)
	}
	return withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(_ context.Context, _ hubShellClient, snapshots []hub.NodeSessions) error {
		rows, err := listHubSessionRows(snapshots, nodeSelector)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(hubSessionListResult{Sessions: rows})
		}
		printHubSessionRows(rows)
		return nil
	})
}

func handleHubSessionsCreate(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "Session title")
	tool := fs.String("tool", "", "Tool to run on the remote node")
	cwd := fs.String("cwd", "", "Working directory on the remote node")
	group := fs.String("group", "", "Group path on the remote node")
	model := fs.String("model", "", "Model id")
	var addDirs stringSliceFlag
	fs.Var(&addDirs, "add-dir", "Additional working directory for multi-repo mode on the remote node (repeatable)")
	attach := fs.Bool("attach", false, "Attach after creating")
	jsonOutput := fs.Bool("json", false, "Output created session as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node-name resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions create <node-id-or-name> [--title name] [--tool tool] [--cwd path] [--add-dir path ...] [--group group] [--model model] [--attach]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: agent-deck hub sessions create <node-id-or-name>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:          "create",
		NodeID:          fs.Arg(0),
		Title:           *title,
		Tool:            *tool,
		CWD:             *cwd,
		AdditionalPaths: append([]string(nil), addDirs...),
		Group:           *group,
		ModelID:         *model,
		Attach:          *attach && !*jsonOutput,
	})
}

func handleHubSessionsSimple(profile, action string, args []string) error {
	fs := flag.NewFlagSet("hub sessions "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: agent-deck hub sessions %s <node-id-or-name> <session-id-or-title>\n", action)
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub sessions %s <node-id-or-name> <session-id-or-title>", action)
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:    action,
		NodeID:    fs.Arg(0),
		SessionID: fs.Arg(1),
	})
}

func handleHubSessionsSandboxShell(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions sandbox-shell", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output result as JSON without printing the short-lived attach token")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions sandbox-shell <node-id-or-name> <session-id-or-title>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub sessions sandbox-shell <node-id-or-name> <session-id-or-title>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:    "sandbox_shell",
		NodeID:    fs.Arg(0),
		SessionID: fs.Arg(1),
		Attach:    !*jsonOutput,
	})
}

func handleHubSessionsUndoDelete(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions undo-delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions undo-delete <node-id-or-name>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: agent-deck hub sessions undo-delete <node-id-or-name>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action: "undo_delete",
		NodeID: fs.Arg(0),
	})
}

func handleHubSessionsSend(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions send <node-id-or-name> <session-id-or-title> <message>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() < 3 {
		return fmt.Errorf("usage: agent-deck hub sessions send <node-id-or-name> <session-id-or-title> <message>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:    "send",
		NodeID:    fs.Arg(0),
		SessionID: fs.Arg(1),
		Message:   strings.Join(fs.Args()[2:], " "),
	})
}

func handleHubSessionsNotes(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions notes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions notes <node-id-or-name> <session-id-or-title> <notes>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() < 3 {
		return fmt.Errorf("usage: agent-deck hub sessions notes <node-id-or-name> <session-id-or-title> <notes>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:    "notes",
		NodeID:    fs.Arg(0),
		SessionID: fs.Arg(1),
		Notes:     strings.Join(fs.Args()[2:], " "),
	})
}

func handleHubSessionsFork(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions fork", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "Title for the forked session")
	fs.StringVar(title, "t", "", "Title for the forked session")
	group := fs.String("group", "", "Group path for the forked session")
	fs.StringVar(group, "g", "", "Group path for the forked session")
	worktree := fs.Bool("worktree", false, "Create the fork in a new worktree/branch")
	fs.BoolVar(worktree, "w", false, "Create the fork in a new worktree/branch")
	branch := fs.String("branch", "", "Branch/bookmark name for the fork worktree")
	fs.StringVar(branch, "b", "", "Branch/bookmark name for the fork worktree")
	withState := fs.Bool("with-state", false, "Carry uncommitted worktree state into the fork")
	withIgnored := fs.Bool("with-state-and-gitignored", false, "Carry uncommitted and gitignored files into the fork")
	sandbox := fs.Bool("sandbox", false, "Run the forked session in a Docker sandbox")
	sandboxImage := fs.String("sandbox-image", "", "Sandbox image override")
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions fork <node-id-or-name> <session-id-or-title> [options]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub sessions fork <node-id-or-name> <session-id-or-title>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:       "fork",
		NodeID:       fs.Arg(0),
		SessionID:    fs.Arg(1),
		Title:        *title,
		Group:        *group,
		Worktree:     *worktree,
		Branch:       *branch,
		WithState:    *withState,
		WithIgnored:  *withIgnored,
		Sandbox:      *sandbox,
		SandboxImage: *sandboxImage,
	})
}

func handleHubSessionsWorktreeFinish(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions worktree-finish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	into := fs.String("into", "", "Target branch to merge into")
	noMerge := fs.Bool("no-merge", false, "Skip merge and only remove worktree/session metadata")
	keepBranch := fs.Bool("keep-branch", false, "Do not delete the source branch")
	force := fs.Bool("force", false, "Skip dirty-worktree check and force cleanup")
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions worktree-finish <node-id-or-name> <session-id-or-title> [options]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub sessions worktree-finish <node-id-or-name> <session-id-or-title>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:     "worktree_finish",
		NodeID:     fs.Arg(0),
		SessionID:  fs.Arg(1),
		Into:       *into,
		NoMerge:    *noMerge,
		KeepBranch: *keepBranch,
		Force:      *force,
	})
}

func handleHubSessionsRename(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions rename", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions rename <node-id-or-name> <session-id-or-title> <new-title>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() < 3 {
		return fmt.Errorf("usage: agent-deck hub sessions rename <node-id-or-name> <session-id-or-title> <new-title>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:    "rename",
		NodeID:    fs.Arg(0),
		SessionID: fs.Arg(1),
		Title:     strings.Join(fs.Args()[2:], " "),
	})
}

func handleHubSessionsMove(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions move", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions move <node-id-or-name> <session-id-or-title> <group-path>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 3 {
		return fmt.Errorf("usage: agent-deck hub sessions move <node-id-or-name> <session-id-or-title> <group-path>")
	}
	return runHubSessionCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubSessionOptions{
		Action:    "move",
		NodeID:    fs.Arg(0),
		SessionID: fs.Arg(1),
		Group:     fs.Arg(2),
	})
}

func handleHubSessionsPreview(profile string, args []string) error {
	fs := flag.NewFlagSet("hub sessions preview", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output preview as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub sessions preview <node-id-or-name> <session-id-or-title> [--json]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub sessions preview <node-id-or-name> <session-id-or-title>")
	}
	var preview string
	err := withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		resolved, err := resolveHubSessionTarget(snapshots, fs.Arg(0), fs.Arg(1))
		if err != nil {
			return err
		}
		raw, err := client.Command(ctx, resolved.NodeID, "preview", map[string]string{"session_id": resolved.SessionID})
		if err != nil {
			return err
		}
		var response hub.PreviewSessionResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("decode hub preview result: %w", err)
		}
		preview = response.Content
		return nil
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"content": preview})
	}
	fmt.Print(preview)
	if !strings.HasSuffix(preview, "\n") {
		fmt.Println()
	}
	return nil
}

func runHubSessionCLI(profile string, connectTimeout, resolveTimeout time.Duration, jsonOutput bool, opts hubSessionOptions) error {
	var result hubSessionCommandResult
	err := withConnectedHubSessionClient(profile, connectTimeout, resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubSessionWithClient(ctx, client, snapshots, opts)
		return err
	})
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printHubSessionCommandResult(result)
	return nil
}

func runHubSessionWithClient(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions, opts hubSessionOptions) (hubSessionCommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return hubSessionCommandResult{}, fmt.Errorf("hub session client is required")
	}
	action := strings.TrimSpace(opts.Action)
	if action == "" {
		return hubSessionCommandResult{}, fmt.Errorf("hub session action is required")
	}
	if action == "create" {
		node, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, opts.NodeID)
		if err != nil {
			return hubSessionCommandResult{}, err
		}
		req := hub.CreateSessionRequest{
			Title:           strings.TrimSpace(opts.Title),
			Tool:            strings.TrimSpace(opts.Tool),
			ProjectPath:     strings.TrimSpace(opts.CWD),
			AdditionalPaths: normalizeHubSessionAdditionalPaths(opts.AdditionalPaths),
			GroupPath:       strings.TrimSpace(opts.Group),
			ModelID:         strings.TrimSpace(opts.ModelID),
		}
		raw, err := client.Command(ctx, node.NodeID, "create", req)
		if err != nil {
			return hubSessionCommandResult{}, err
		}
		sessionID, err := decodeHubCreateSessionResult(raw)
		if err != nil {
			return hubSessionCommandResult{}, err
		}
		if opts.Attach {
			if err := client.Attach(ctx, node.NodeID, sessionID, hub.TerminalSize{}); err != nil {
				return hubSessionCommandResult{}, err
			}
		}
		return hubSessionCommandResult{Action: "create", NodeID: node.NodeID, NodeName: node.NodeName, SessionID: sessionID, SessionTitle: strings.TrimSpace(opts.Title)}, nil
	}
	if action == "undo_delete" {
		node, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, opts.NodeID)
		if err != nil {
			return hubSessionCommandResult{}, err
		}
		raw, err := client.Command(ctx, node.NodeID, "undo_delete", nil)
		if err != nil {
			return hubSessionCommandResult{}, err
		}
		sessionID, err := decodeHubCreateSessionResult(raw)
		if err != nil {
			return hubSessionCommandResult{}, err
		}
		return hubSessionCommandResult{Action: "undo_delete", NodeID: node.NodeID, NodeName: node.NodeName, SessionID: sessionID}, nil
	}

	resolved, err := resolveHubSessionTarget(snapshots, opts.NodeID, opts.SessionID)
	if err != nil {
		return hubSessionCommandResult{}, err
	}
	result := hubSessionCommandResult{
		Action:       action,
		NodeID:       resolved.NodeID,
		NodeName:     resolved.NodeName,
		SessionID:    resolved.SessionID,
		SessionTitle: resolved.SessionTitle,
	}
	switch action {
	case "attach":
		if err := client.Attach(ctx, resolved.NodeID, resolved.SessionID, hub.TerminalSize{}); err != nil {
			return hubSessionCommandResult{}, err
		}
		return result, nil
	case "sandbox_shell":
		raw, commandErr := client.Command(ctx, resolved.NodeID, "sandbox_shell", hub.SandboxShellRequest{SessionID: resolved.SessionID})
		err = commandErr
		if err == nil {
			var response hub.SandboxShellResponse
			if decodeErr := json.Unmarshal(raw, &response); decodeErr != nil {
				return hubSessionCommandResult{}, fmt.Errorf("decode hub sandbox shell result: %w", decodeErr)
			}
			attachSessionID := strings.TrimSpace(response.AttachSessionID)
			if opts.Attach {
				if attachSessionID == "" {
					return hubSessionCommandResult{}, fmt.Errorf("hub sandbox shell returned no attach session")
				}
				if attachErr := client.Attach(ctx, resolved.NodeID, attachSessionID, hub.TerminalSize{}); attachErr != nil {
					return hubSessionCommandResult{}, attachErr
				}
			}
		}
	case "send":
		message := strings.TrimSpace(opts.Message)
		if message == "" {
			return hubSessionCommandResult{}, fmt.Errorf("hub sessions send message is required")
		}
		_, err = client.Command(ctx, resolved.NodeID, "send", map[string]string{"session_id": resolved.SessionID, "message": message})
	case "approve":
		_, err = client.Command(ctx, resolved.NodeID, "send", map[string]string{"session_id": resolved.SessionID, "message": "1"})
	case "notes":
		_, err = client.Command(ctx, resolved.NodeID, "update", hub.UpdateSessionRequest{
			SessionID: resolved.SessionID,
			Changes:   []hub.SessionFieldChange{{Field: session.FieldNotes, Value: opts.Notes}},
		})
	case "rename":
		title := strings.TrimSpace(opts.Title)
		if title == "" {
			return hubSessionCommandResult{}, fmt.Errorf("hub sessions rename title is required")
		}
		result.SessionTitle = title
		_, err = client.Command(ctx, resolved.NodeID, "rename", map[string]string{"session_id": resolved.SessionID, "title": title})
	case "move":
		group := strings.TrimSpace(opts.Group)
		if group == "" {
			return hubSessionCommandResult{}, fmt.Errorf("hub sessions move group path is required")
		}
		_, err = client.Command(ctx, resolved.NodeID, "move", map[string]string{"session_id": resolved.SessionID, "group_path": group})
	case "fork":
		req := hub.ForkSessionRequest{
			SessionID:    resolved.SessionID,
			Title:        strings.TrimSpace(opts.Title),
			GroupPath:    strings.TrimSpace(opts.Group),
			Worktree:     opts.Worktree,
			Branch:       strings.TrimSpace(opts.Branch),
			WithState:    opts.WithState,
			WithIgnored:  opts.WithIgnored,
			Sandbox:      opts.Sandbox,
			SandboxImage: strings.TrimSpace(opts.SandboxImage),
		}
		raw, commandErr := client.Command(ctx, resolved.NodeID, action, req)
		err = commandErr
		if err == nil {
			sessionID, decodeErr := decodeHubCreateSessionResult(raw)
			if decodeErr != nil {
				return hubSessionCommandResult{}, decodeErr
			}
			result.SessionID = sessionID
			if req.Title != "" {
				result.SessionTitle = req.Title
			} else {
				result.SessionTitle = resolved.SessionTitle + " (fork)"
			}
		}
	case "worktree_finish":
		_, err = client.Command(ctx, resolved.NodeID, "worktree_finish", hub.WorktreeFinishRequest{
			SessionID:  resolved.SessionID,
			Into:       strings.TrimSpace(opts.Into),
			NoMerge:    opts.NoMerge,
			KeepBranch: opts.KeepBranch,
			Force:      opts.Force,
		})
	case "start", "stop", "restart", "restart_fresh", "delete", "archive", "unarchive", "remove", "toggle_yolo", "mark_unread":
		_, err = client.Command(ctx, resolved.NodeID, action, map[string]string{"session_id": resolved.SessionID})
	case "worktree_setup":
		_, err = client.Command(ctx, resolved.NodeID, "worktree_setup", hub.WorktreeSetupRequest{SessionID: resolved.SessionID})
	default:
		return hubSessionCommandResult{}, fmt.Errorf("unsupported hub sessions action %q", action)
	}
	if err != nil {
		return hubSessionCommandResult{}, err
	}
	return result, nil
}

func normalizeHubSessionAdditionalPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

type resolvedHubSessionTarget struct {
	NodeID       string
	NodeName     string
	SessionID    string
	SessionTitle string
}

func resolveHubSessionTarget(snapshots []hub.NodeSessions, nodeSelector, sessionSelector string) (resolvedHubSessionTarget, error) {
	node, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, nodeSelector)
	if err != nil {
		return resolvedHubSessionTarget{}, err
	}
	sessionSelector = strings.TrimSpace(sessionSelector)
	if sessionSelector == "" {
		return resolvedHubSessionTarget{}, fmt.Errorf("hub session id or title is required")
	}
	for _, snapshot := range snapshots {
		if strings.TrimSpace(snapshot.Node.ID) != node.NodeID {
			continue
		}
		for _, sess := range snapshot.Sessions {
			if strings.TrimSpace(sess.ID) == sessionSelector {
				return resolvedHubSessionTarget{NodeID: node.NodeID, NodeName: node.NodeName, SessionID: strings.TrimSpace(sess.ID), SessionTitle: strings.TrimSpace(sess.Title)}, nil
			}
		}
		var matches []hub.SessionInfo
		for _, sess := range snapshot.Sessions {
			if strings.TrimSpace(sess.Title) == sessionSelector {
				matches = append(matches, sess)
			}
		}
		switch len(matches) {
		case 1:
			return resolvedHubSessionTarget{NodeID: node.NodeID, NodeName: node.NodeName, SessionID: strings.TrimSpace(matches[0].ID), SessionTitle: strings.TrimSpace(matches[0].Title)}, nil
		case 0:
			if strings.HasPrefix(sessionSelector, "sess_") {
				return resolvedHubSessionTarget{NodeID: node.NodeID, NodeName: node.NodeName, SessionID: sessionSelector}, nil
			}
			return resolvedHubSessionTarget{}, fmt.Errorf("%w: %q on node %s", errHubSessionNotFound, sessionSelector, node.NodeID)
		default:
			ids := make([]string, 0, len(matches))
			for _, match := range matches {
				ids = append(ids, strings.TrimSpace(match.ID))
			}
			return resolvedHubSessionTarget{}, fmt.Errorf("multiple hub sessions named %q on node %s; use one of these session ids: %s", sessionSelector, node.NodeID, strings.Join(ids, ", "))
		}
	}
	if strings.HasPrefix(sessionSelector, "sess_") {
		return resolvedHubSessionTarget{NodeID: node.NodeID, NodeName: node.NodeName, SessionID: sessionSelector}, nil
	}
	return resolvedHubSessionTarget{}, fmt.Errorf("%w: %q on node %s", errHubSessionNotFound, sessionSelector, node.NodeID)
}

func listHubSessionRows(snapshots []hub.NodeSessions, nodeSelector string) ([]hubSessionRow, error) {
	nodeSelector = strings.TrimSpace(nodeSelector)
	var nodeIDFilter string
	if nodeSelector != "" {
		node, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, nodeSelector)
		if err != nil {
			return nil, err
		}
		nodeIDFilter = node.NodeID
	}
	var rows []hubSessionRow
	for _, snapshot := range snapshots {
		nodeID := strings.TrimSpace(snapshot.Node.ID)
		if nodeID == "" || (nodeIDFilter != "" && nodeID != nodeIDFilter) {
			continue
		}
		nodeName := strings.TrimSpace(snapshot.Node.Name)
		for _, sess := range snapshot.Sessions {
			rows = append(rows, hubSessionRow{
				NodeID:           nodeID,
				NodeName:         nodeName,
				ID:               strings.TrimSpace(sess.ID),
				Title:            strings.TrimSpace(sess.Title),
				Tool:             strings.TrimSpace(sess.Tool),
				Status:           strings.TrimSpace(sess.Status),
				GroupPath:        strings.TrimSpace(sess.GroupPath),
				ProjectPath:      strings.TrimSpace(sess.ProjectPath),
				DisplaySessionID: strings.TrimSpace(sess.DisplaySessionID),
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].NodeName != rows[j].NodeName {
			return rows[i].NodeName < rows[j].NodeName
		}
		if rows[i].GroupPath != rows[j].GroupPath {
			return rows[i].GroupPath < rows[j].GroupPath
		}
		return rows[i].Title < rows[j].Title
	})
	return rows, nil
}

func withConnectedHubSessionClient(profile string, connectTimeout, snapshotTimeout time.Duration, run func(context.Context, hubShellClient, []hub.NodeSessions) error) error {
	if run == nil {
		return fmt.Errorf("hub session callback is required")
	}
	config, err := session.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("load user config: %w", err)
	}
	snapshots := newHubShellSnapshotCache()
	connected := make(chan struct{})
	var connectedOnce sync.Once
	client, err := newConfiguredHubClient(profile, config.Hub, configuredHubClientCallbacks{
		OnStatus: func(status string) {
			if status == "connected" {
				connectedOnce.Do(func() { close(connected) })
			}
		},
		OnSnapshot: snapshots.update,
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	clientCtx, cancelClient := context.WithCancel(ctx)
	defer cancelClient()
	connectErr := make(chan error, 1)
	go func() {
		connectErr <- client.Connect(clientCtx)
	}()

	waitCtx, cancelWait := context.WithTimeout(ctx, connectTimeout)
	defer cancelWait()
	select {
	case <-connected:
	case err := <-connectErr:
		if err != nil {
			return err
		}
		return fmt.Errorf("hub connection ended before it became ready")
	case <-waitCtx.Done():
		return fmt.Errorf("hub connection timed out after %s", connectTimeout.String())
	}
	if snapshotTimeout > 0 && len(snapshots.list()) == 0 {
		timer := time.NewTimer(snapshotTimeout)
		select {
		case <-snapshots.changed:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	return run(ctx, client, snapshots.list())
}

func printHubSessionRows(rows []hubSessionRow) {
	if len(rows) == 0 {
		fmt.Println("No hub sessions visible.")
		return
	}
	for _, row := range rows {
		node := row.NodeName
		if node == "" {
			node = row.NodeID
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", node, row.ID, row.Status, row.Tool, row.Title)
	}
}

func printHubSessionCommandResult(result hubSessionCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	switch result.Action {
	case "create":
		fmt.Printf("Created hub session %s on %s\n", result.SessionID, node)
	case "attach":
		return
	default:
		label := result.SessionTitle
		if label == "" {
			label = result.SessionID
		}
		fmt.Printf("%s hub session %s on %s\n", strings.ReplaceAll(result.Action, "_", "-"), label, node)
	}
}

func printHubSessionsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck hub sessions <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list [node]                 List visible hub sessions")
	fmt.Fprintln(w, "  create <node>               Create a session on a hub node")
	fmt.Fprintln(w, "  attach <node> <session>     Attach to a hub session")
	fmt.Fprintln(w, "  sandbox-shell <node> <session> Open a sandbox container shell through the hub")
	fmt.Fprintln(w, "  send <node> <session> <msg> Send a prompt/message")
	fmt.Fprintln(w, "  approve <node> <session>    Quick approve by sending 1+Enter")
	fmt.Fprintln(w, "  notes <node> <session> <notes> Update hub session notes")
	fmt.Fprintln(w, "  start <node> <session>      Start/resume a hub session")
	fmt.Fprintln(w, "  close <node> <session>      Stop a hub session")
	fmt.Fprintln(w, "  restart <node> <session>    Restart a hub session")
	fmt.Fprintln(w, "  restart-fresh <node> <session> Restart without resume")
	fmt.Fprintln(w, "  fork <node> <session>       Fork a hub session (supports --title/--group/--worktree)")
	fmt.Fprintln(w, "  worktree-setup <node> <session> Re-run hub worktree setup script")
	fmt.Fprintln(w, "  worktree-finish <node> <session> Finish a hub worktree session")
	fmt.Fprintln(w, "  rename <node> <session> <title> Rename a hub session")
	fmt.Fprintln(w, "  move <node> <session> <group> Move a hub session to a group")
	fmt.Fprintln(w, "  delete <node> <session>     Delete a hub session")
	fmt.Fprintln(w, "  undo-delete <node>          Restore the latest deleted hub session")
	fmt.Fprintln(w, "  archive <node> <session>    Archive a hub session")
	fmt.Fprintln(w, "  unarchive <node> <session>  Unarchive a hub session")
	fmt.Fprintln(w, "  remove <node> <session>     Remove stopped/error hub session metadata")
	fmt.Fprintln(w, "  toggle-yolo <node> <session> Toggle YOLO/auto-approve on a hub session")
	fmt.Fprintln(w, "  unread <node> <session>     Mark a hub session as needing attention")
	fmt.Fprintln(w, "  preview <node> <session>    Print remote pane preview")
}
