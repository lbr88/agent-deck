package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/hub"
)

type hubGroupOptions struct {
	Action           string
	NodeID           string
	GroupPath        string
	Name             string
	ParentPath       string
	DestParentPath   string
	DefaultPath      string
	ClearDefaultPath bool
	MaxConcurrent    *int
	Force            bool
	Direction        string
	Position         *int
}

type hubGroupCommandResult struct {
	Action        string `json:"action"`
	NodeID        string `json:"node_id"`
	NodeName      string `json:"node_name,omitempty"`
	Path          string `json:"path,omitempty"`
	Name          string `json:"name,omitempty"`
	DefaultPath   string `json:"default_path,omitempty"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"`
	SessionsMoved int    `json:"sessions_moved,omitempty"`
	MovedTo       string `json:"moved_to,omitempty"`
	OldPath       string `json:"old_path,omitempty"`
	FromPosition  int    `json:"from_position,omitempty"`
	ToPosition    int    `json:"to_position,omitempty"`
}

func handleHubGroups(profile string, args []string) error {
	if len(args) == 0 || args[0] == "list" || args[0] == "ls" {
		return handleHubGroupsList(profile, args)
	}
	switch args[0] {
	case "create", "new":
		return handleHubGroupsCreate(profile, args[1:])
	case "rename", "mv":
		return handleHubGroupsRename(profile, args[1:])
	case "update", "set":
		return handleHubGroupsUpdate(profile, args[1:])
	case "delete", "rm", "remove":
		return handleHubGroupsDelete(profile, args[1:])
	case "change", "reparent":
		return handleHubGroupsChange(profile, args[1:])
	case "reorder", "sort":
		return handleHubGroupsReorder(profile, args[1:])
	case "help", "--help", "-h":
		printHubGroupsUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown hub groups command %q", args[0])
	}
}

func handleHubGroupsList(profile string, args []string) error {
	if len(args) > 0 && (args[0] == "list" || args[0] == "ls") {
		args = args[1:]
	}
	fs := flag.NewFlagSet("hub groups list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 10*time.Second, "Time to connect to the hub")
	resolveTimeout := fs.Duration("timeout", 5*time.Second, "Time to wait for hub snapshots")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub groups [list] [node-id-or-name] [--json]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	nodeSelector := strings.TrimSpace(fs.Arg(0))
	return withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(_ context.Context, _ hubShellClient, snapshots []hub.NodeSessions) error {
		return printHubGroupsFromSnapshots(snapshots, nodeSelector, *jsonOutput)
	})
}

func handleHubGroupsCreate(profile string, args []string) error {
	fs := flag.NewFlagSet("hub groups create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	parent := fs.String("parent", "", "Create as subgroup under this remote group path")
	defaultPath := fs.String("default-path", "", "Default working directory for new sessions in this remote group")
	maxConcurrent := fs.Int("max-concurrent", -1, "Cap simultaneous running sessions in this remote group (0=unlimited, 1=serial, N=cap)")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 10*time.Second, "Time to connect to the hub")
	resolveTimeout := fs.Duration("timeout", 5*time.Second, "Time to wait for hub snapshots")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub groups create <node-id-or-name> <name> [--parent group] [--default-path path] [--max-concurrent N]")
		fs.PrintDefaults()
	}
	args = reorderGroupArgs(args)
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: agent-deck hub groups create <node-id-or-name> <name>")
	}
	var maxPtr *int
	if *maxConcurrent >= 0 {
		maxPtr = maxConcurrent
	}
	return runHubGroupCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubGroupOptions{
		Action:        "create",
		NodeID:        fs.Arg(0),
		Name:          fs.Arg(1),
		ParentPath:    *parent,
		DefaultPath:   *defaultPath,
		MaxConcurrent: maxPtr,
	})
}

func handleHubGroupsRename(profile string, args []string) error {
	fs := flag.NewFlagSet("hub groups rename", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 10*time.Second, "Time to connect to the hub")
	resolveTimeout := fs.Duration("timeout", 5*time.Second, "Time to wait for hub snapshots")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub groups rename <node-id-or-name> <group-path> <new-name>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() < 3 {
		return fmt.Errorf("usage: agent-deck hub groups rename <node-id-or-name> <group-path> <new-name>")
	}
	return runHubGroupCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubGroupOptions{
		Action:    "rename",
		NodeID:    fs.Arg(0),
		GroupPath: fs.Arg(1),
		Name:      strings.Join(fs.Args()[2:], " "),
	})
}

func handleHubGroupsUpdate(profile string, args []string) error {
	fs := flag.NewFlagSet("hub groups update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	defaultPath := fs.String("default-path", "", "Default working directory for new sessions in this remote group")
	clearDefaultPath := fs.Bool("clear-default-path", false, "Clear remote group default working directory")
	maxConcurrent := fs.Int("max-concurrent", -1, "Cap simultaneous running sessions in this remote group (0=unlimited, 1=serial, N=cap)")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 10*time.Second, "Time to connect to the hub")
	resolveTimeout := fs.Duration("timeout", 5*time.Second, "Time to wait for hub snapshots")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub groups update <node-id-or-name> <group-path> [--default-path path|--clear-default-path|--max-concurrent N]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: agent-deck hub groups update <node-id-or-name> <group-path>")
	}
	if *defaultPath != "" && *clearDefaultPath {
		return fmt.Errorf("--default-path and --clear-default-path are mutually exclusive")
	}
	var maxPtr *int
	if *maxConcurrent >= 0 {
		maxPtr = maxConcurrent
	}
	if *defaultPath == "" && !*clearDefaultPath && maxPtr == nil {
		return fmt.Errorf("specify at least one of --default-path, --clear-default-path, or --max-concurrent")
	}
	return runHubGroupCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubGroupOptions{
		Action:           "update",
		NodeID:           fs.Arg(0),
		GroupPath:        fs.Arg(1),
		DefaultPath:      *defaultPath,
		ClearDefaultPath: *clearDefaultPath,
		MaxConcurrent:    maxPtr,
	})
}

func handleHubGroupsDelete(profile string, args []string) error {
	fs := flag.NewFlagSet("hub groups delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "Move sessions to the remote default group and delete")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 10*time.Second, "Time to connect to the hub")
	resolveTimeout := fs.Duration("timeout", 5*time.Second, "Time to wait for hub snapshots")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub groups delete <node-id-or-name> <group-path> [--force]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: agent-deck hub groups delete <node-id-or-name> <group-path>")
	}
	return runHubGroupCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubGroupOptions{
		Action:    "delete",
		NodeID:    fs.Arg(0),
		GroupPath: fs.Arg(1),
		Force:     *force,
	})
}

func handleHubGroupsChange(profile string, args []string) error {
	fs := flag.NewFlagSet("hub groups change", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 10*time.Second, "Time to connect to the hub")
	resolveTimeout := fs.Duration("timeout", 5*time.Second, "Time to wait for hub snapshots")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub groups change <node-id-or-name> <group-path> [dest-parent-path]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: agent-deck hub groups change <node-id-or-name> <group-path> [dest-parent-path]")
	}
	dest := ""
	if fs.NArg() > 2 {
		dest = fs.Arg(2)
	}
	return runHubGroupCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubGroupOptions{
		Action:         "change",
		NodeID:         fs.Arg(0),
		GroupPath:      fs.Arg(1),
		DestParentPath: dest,
	})
}

func handleHubGroupsReorder(profile string, args []string) error {
	fs := flag.NewFlagSet("hub groups reorder", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	up := fs.Bool("up", false, "Move the remote group up among siblings")
	down := fs.Bool("down", false, "Move the remote group down among siblings")
	position := fs.Int("position", -1, "Move the remote group to a zero-based sibling position")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 10*time.Second, "Time to connect to the hub")
	resolveTimeout := fs.Duration("timeout", 5*time.Second, "Time to wait for hub snapshots")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub groups reorder <node-id-or-name> <group-path> [--up|--down|--position N]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: agent-deck hub groups reorder <node-id-or-name> <group-path> [--up|--down|--position N]")
	}
	modeCount := 0
	direction := ""
	if *up {
		modeCount++
		direction = "up"
	}
	if *down {
		modeCount++
		direction = "down"
	}
	var posPtr *int
	if *position >= 0 {
		modeCount++
		pos := *position
		posPtr = &pos
	}
	if modeCount != 1 {
		return fmt.Errorf("specify exactly one of --up, --down, or --position")
	}
	return runHubGroupCLI(profile, *connectTimeout, *resolveTimeout, *jsonOutput, hubGroupOptions{
		Action:    "reorder",
		NodeID:    fs.Arg(0),
		GroupPath: fs.Arg(1),
		Direction: direction,
		Position:  posPtr,
	})
}

func runHubGroupCLI(profile string, connectTimeout, resolveTimeout time.Duration, jsonOutput bool, opts hubGroupOptions) error {
	var result hubGroupCommandResult
	err := withConnectedHubSessionClient(profile, connectTimeout, resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubGroupWithClient(ctx, client, snapshots, opts)
		return err
	})
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printHubGroupCommandResult(result)
	return nil
}

func runHubGroupWithClient(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions, opts hubGroupOptions) (hubGroupCommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return hubGroupCommandResult{}, fmt.Errorf("hub group client is required")
	}
	node, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, opts.NodeID)
	if err != nil {
		return hubGroupCommandResult{}, err
	}
	result := hubGroupCommandResult{Action: opts.Action, NodeID: node.NodeID, NodeName: node.NodeName}
	switch strings.TrimSpace(opts.Action) {
	case "create":
		req := hub.GroupCreateRequest{
			Name:          strings.TrimSpace(opts.Name),
			ParentPath:    strings.Trim(strings.TrimSpace(opts.ParentPath), "/"),
			DefaultPath:   strings.TrimSpace(opts.DefaultPath),
			MaxConcurrent: opts.MaxConcurrent,
		}
		if req.Name == "" {
			return hubGroupCommandResult{}, fmt.Errorf("hub groups create name is required")
		}
		raw, err := client.Command(ctx, node.NodeID, "group_create", req)
		if err != nil {
			return hubGroupCommandResult{}, err
		}
		var resp hub.GroupCreateResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubGroupCommandResult{}, fmt.Errorf("decode hub group create result: %w", err)
		}
		result.Path, result.Name, result.DefaultPath, result.MaxConcurrent = resp.Path, resp.Name, resp.DefaultPath, resp.MaxConcurrent
	case "rename":
		req := hub.GroupRenameRequest{GroupPath: strings.Trim(strings.TrimSpace(opts.GroupPath), "/"), Name: strings.TrimSpace(opts.Name)}
		if req.GroupPath == "" || req.Name == "" {
			return hubGroupCommandResult{}, fmt.Errorf("hub groups rename group path and name are required")
		}
		raw, err := client.Command(ctx, node.NodeID, "group_rename", req)
		if err != nil {
			return hubGroupCommandResult{}, err
		}
		var resp hub.GroupRenameResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubGroupCommandResult{}, fmt.Errorf("decode hub group rename result: %w", err)
		}
		result.Path, result.Name = resp.Path, resp.Name
	case "update":
		req := hub.GroupUpdateRequest{
			GroupPath:        strings.Trim(strings.TrimSpace(opts.GroupPath), "/"),
			ClearDefaultPath: opts.ClearDefaultPath,
			MaxConcurrent:    opts.MaxConcurrent,
		}
		if opts.DefaultPath != "" {
			defaultPath := strings.TrimSpace(opts.DefaultPath)
			req.DefaultPath = &defaultPath
		}
		if req.GroupPath == "" {
			return hubGroupCommandResult{}, fmt.Errorf("hub groups update group path is required")
		}
		if req.DefaultPath == nil && !req.ClearDefaultPath && req.MaxConcurrent == nil {
			return hubGroupCommandResult{}, fmt.Errorf("hub groups update requires a setting change")
		}
		raw, err := client.Command(ctx, node.NodeID, "group_update", req)
		if err != nil {
			return hubGroupCommandResult{}, err
		}
		var resp hub.GroupUpdateResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubGroupCommandResult{}, fmt.Errorf("decode hub group update result: %w", err)
		}
		result.Path, result.DefaultPath, result.MaxConcurrent = resp.Path, resp.DefaultPath, resp.MaxConcurrent
	case "delete":
		req := hub.GroupDeleteRequest{GroupPath: strings.Trim(strings.TrimSpace(opts.GroupPath), "/"), Force: opts.Force}
		if req.GroupPath == "" {
			return hubGroupCommandResult{}, fmt.Errorf("hub groups delete group path is required")
		}
		raw, err := client.Command(ctx, node.NodeID, "group_delete", req)
		if err != nil {
			return hubGroupCommandResult{}, err
		}
		var resp hub.GroupDeleteResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubGroupCommandResult{}, fmt.Errorf("decode hub group delete result: %w", err)
		}
		result.Path, result.SessionsMoved, result.MovedTo = resp.Path, resp.SessionsMoved, resp.MovedTo
	case "change":
		req := hub.GroupReparentRequest{
			GroupPath:      strings.Trim(strings.TrimSpace(opts.GroupPath), "/"),
			DestParentPath: strings.Trim(strings.TrimSpace(opts.DestParentPath), "/"),
		}
		if req.GroupPath == "" {
			return hubGroupCommandResult{}, fmt.Errorf("hub groups change group path is required")
		}
		raw, err := client.Command(ctx, node.NodeID, "group_reparent", req)
		if err != nil {
			return hubGroupCommandResult{}, err
		}
		var resp hub.GroupReparentResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubGroupCommandResult{}, fmt.Errorf("decode hub group reparent result: %w", err)
		}
		result.OldPath, result.Path = resp.OldPath, resp.Path
	case "reorder":
		req := hub.GroupReorderRequest{
			GroupPath: strings.Trim(strings.TrimSpace(opts.GroupPath), "/"),
			Direction: strings.TrimSpace(opts.Direction),
			Position:  opts.Position,
		}
		if req.GroupPath == "" {
			return hubGroupCommandResult{}, fmt.Errorf("hub groups reorder group path is required")
		}
		if req.Direction == "" && req.Position == nil {
			return hubGroupCommandResult{}, fmt.Errorf("hub groups reorder requires a direction or position")
		}
		raw, err := client.Command(ctx, node.NodeID, "group_reorder", req)
		if err != nil {
			return hubGroupCommandResult{}, err
		}
		var resp hub.GroupReorderResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubGroupCommandResult{}, fmt.Errorf("decode hub group reorder result: %w", err)
		}
		result.Path, result.FromPosition, result.ToPosition = resp.Path, resp.FromPosition, resp.ToPosition
	default:
		return hubGroupCommandResult{}, fmt.Errorf("unsupported hub groups action %q", opts.Action)
	}
	return result, nil
}

func printHubGroupsFromSnapshots(snapshots []hub.NodeSessions, nodeSelector string, jsonOutput bool) error {
	type row struct {
		NodeID        string `json:"node_id"`
		NodeName      string `json:"node_name,omitempty"`
		Path          string `json:"path"`
		Name          string `json:"name"`
		DefaultPath   string `json:"default_path,omitempty"`
		MaxConcurrent int    `json:"max_concurrent,omitempty"`
		SessionCount  int    `json:"session_count"`
	}
	var nodes []hub.NodeSessions
	if strings.TrimSpace(nodeSelector) != "" {
		node, err := resolveHubShellNodeSelectorFromSnapshots(snapshots, nodeSelector)
		if err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			if snapshot.Node.ID == node.NodeID {
				nodes = append(nodes, snapshot)
				break
			}
		}
	} else {
		nodes = snapshots
	}
	rows := make([]row, 0)
	for _, snapshot := range nodes {
		counts := make(map[string]int)
		for _, sess := range snapshot.Sessions {
			path := strings.Trim(strings.TrimSpace(sess.GroupPath), "/")
			if path == "" {
				path = "my-sessions"
			}
			counts[path]++
		}
		for _, group := range snapshot.Groups {
			path := strings.Trim(strings.TrimSpace(group.Path), "/")
			if path == "" {
				path = "my-sessions"
			}
			rows = append(rows, row{
				NodeID:        snapshot.Node.ID,
				NodeName:      snapshot.Node.Name,
				Path:          path,
				Name:          group.Name,
				DefaultPath:   group.DefaultPath,
				MaxConcurrent: group.MaxConcurrent,
				SessionCount:  counts[path],
			})
			delete(counts, path)
		}
		for path, count := range counts {
			rows = append(rows, row{NodeID: snapshot.Node.ID, NodeName: snapshot.Node.Name, Path: path, Name: path, SessionCount: count})
		}
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"groups": rows})
	}
	if len(rows) == 0 {
		fmt.Println("No hub groups visible.")
		return nil
	}
	for _, r := range rows {
		node := r.NodeName
		if node == "" {
			node = r.NodeID
		}
		fmt.Printf("%s\t%s\t%d sessions\n", node, r.Path, r.SessionCount)
	}
	return nil
}

func printHubGroupCommandResult(result hubGroupCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	switch result.Action {
	case "create":
		fmt.Printf("Created hub group %s on %s\n", result.Path, node)
	case "rename":
		fmt.Printf("Renamed hub group to %s on %s\n", result.Path, node)
	case "update":
		fmt.Printf("Updated hub group %s on %s\n", result.Path, node)
	case "delete":
		fmt.Printf("Deleted hub group %s on %s (%d sessions moved to %s)\n", result.Path, node, result.SessionsMoved, result.MovedTo)
	case "change":
		fmt.Printf("Moved hub group %s to %s on %s\n", result.OldPath, result.Path, node)
	case "reorder":
		fmt.Printf("Reordered hub group %s on %s (%d → %d)\n", result.Path, node, result.FromPosition, result.ToPosition)
	default:
		fmt.Printf("Hub group %s sent to %s\n", result.Action, node)
	}
}

func printHubGroupsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck hub groups <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list [node]                    List groups on trusted hub nodes")
	fmt.Fprintln(w, "  create <node> <name>           Create a group on a hub node")
	fmt.Fprintln(w, "  rename <node> <group> <name>   Rename a group on a hub node")
	fmt.Fprintln(w, "  update <node> <group>          Update hub group settings")
	fmt.Fprintln(w, "  delete <node> <group>          Delete a group on a hub node")
	fmt.Fprintln(w, "  change <node> <group> [parent] Reparent a group on a hub node")
	fmt.Fprintln(w, "  reorder <node> <group>         Reorder a group among siblings")
}
