package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/hub"
)

type hubMCPOptions struct {
	Action     string
	NodeID     string
	SessionID  string
	Name       string
	Scope      string
	FromScope  string
	ToScope    string
	Restart    bool
	JSONOutput bool
}

type hubMCPCommandResult struct {
	Action       string                `json:"action"`
	NodeID       string                `json:"node_id"`
	NodeName     string                `json:"node_name,omitempty"`
	SessionID    string                `json:"session_id"`
	SessionTitle string                `json:"session_title,omitempty"`
	Name         string                `json:"name,omitempty"`
	Scope        string                `json:"scope,omitempty"`
	FromScope    string                `json:"from_scope,omitempty"`
	ToScope      string                `json:"to_scope,omitempty"`
	Restarted    bool                  `json:"restarted,omitempty"`
	Local        []string              `json:"local,omitempty"`
	Global       []string              `json:"global,omitempty"`
	User         []string              `json:"user,omitempty"`
	Catalog      []hub.MCPCatalogEntry `json:"catalog,omitempty"`
}

func handleHubMCPs(profile string, args []string) error {
	if len(args) == 0 || args[0] == "attached" || args[0] == "list" || args[0] == "ls" {
		return handleHubMCPsAttached(profile, args)
	}
	switch args[0] {
	case "catalog":
		return handleHubMCPsCatalog(profile, args[1:])
	case "attach":
		return handleHubMCPsMutate(profile, "mcp_attach", args[1:])
	case "detach":
		return handleHubMCPsMutate(profile, "mcp_detach", args[1:])
	case "move", "toggle":
		return handleHubMCPsMove(profile, args[1:])
	case "help", "--help", "-h":
		printHubMCPsUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown hub mcps command %q", args[0])
	}
}

func handleHubMCPsAttached(profile string, args []string) error {
	if len(args) > 0 && (args[0] == "attached" || args[0] == "list" || args[0] == "ls") {
		args = args[1:]
	}
	fs := flag.NewFlagSet("hub mcps attached", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub mcps attached <node-id-or-name> <session-id-or-title> [--json]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub mcps attached <node-id-or-name> <session-id-or-title>")
	}

	var result hubMCPCommandResult
	err := withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubMCPWithClient(ctx, client, snapshots, hubMCPOptions{
			Action:    "mcp_list",
			NodeID:    fs.Arg(0),
			SessionID: fs.Arg(1),
		})
		return err
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printHubMCPAttachedResult(result)
	return nil
}

func handleHubMCPsCatalog(profile string, args []string) error {
	fs := flag.NewFlagSet("hub mcps catalog", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub mcps catalog <node-id-or-name> <session-id-or-title> [--json]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub mcps catalog <node-id-or-name> <session-id-or-title>")
	}

	var result hubMCPCommandResult
	err := withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubMCPWithClient(ctx, client, snapshots, hubMCPOptions{
			Action:    "mcp_list",
			NodeID:    fs.Arg(0),
			SessionID: fs.Arg(1),
		})
		return err
	})
	if err != nil {
		return err
	}
	result.Action = "mcp_catalog"
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printHubMCPCatalogResult(result)
	return nil
}

func handleHubMCPsMutate(profile, action string, args []string) error {
	fs := flag.NewFlagSet("hub mcps "+strings.TrimPrefix(action, "mcp_"), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	global := fs.Bool("global", false, "Use global MCP scope")
	user := fs.Bool("user", false, "Use user MCP scope")
	scope := fs.String("scope", "", "MCP scope: local, global, or user")
	restart := fs.Bool("restart", false, "Restart the remote session after changing MCPs")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		name := strings.TrimPrefix(action, "mcp_")
		fmt.Fprintf(os.Stderr, "Usage: agent-deck hub mcps %s <node-id-or-name> <session-id-or-title> <mcp-name> [--scope local|global|user] [--restart]\n", name)
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 3 {
		return fmt.Errorf("usage: agent-deck hub mcps %s <node-id-or-name> <session-id-or-title> <mcp-name>", strings.TrimPrefix(action, "mcp_"))
	}
	resolvedScope, err := hubMCPScopeFromFlags(*scope, *global, *user)
	if err != nil {
		return err
	}
	var result hubMCPCommandResult
	err = withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubMCPWithClient(ctx, client, snapshots, hubMCPOptions{
			Action:    action,
			NodeID:    fs.Arg(0),
			SessionID: fs.Arg(1),
			Name:      fs.Arg(2),
			Scope:     resolvedScope,
			Restart:   *restart,
		})
		return err
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printHubMCPCommandResult(result)
	return nil
}

func handleHubMCPsMove(profile string, args []string) error {
	fs := flag.NewFlagSet("hub mcps move", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	fromScope := fs.String("from", "", "Current MCP scope: local, global, or user; auto-detected when omitted")
	toScope := fs.String("scope", "", "Destination MCP scope: local, global, or user")
	restart := fs.Bool("restart", false, "Restart the remote session after changing MCPs")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub mcps move <node-id-or-name> <session-id-or-title> <mcp-name> <scope> [--from scope] [--restart]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() < 3 || fs.NArg() > 4 {
		return fmt.Errorf("usage: agent-deck hub mcps move <node-id-or-name> <session-id-or-title> <mcp-name> <scope>")
	}
	destScope := strings.TrimSpace(*toScope)
	if destScope == "" && fs.NArg() == 4 {
		destScope = fs.Arg(3)
	}
	destScope, err := normalizeHubMCPScope(destScope)
	if err != nil {
		return err
	}
	srcScope := strings.TrimSpace(*fromScope)
	if srcScope != "" {
		srcScope, err = normalizeHubMCPScope(srcScope)
		if err != nil {
			return err
		}
	}
	var result hubMCPCommandResult
	err = withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubMCPWithClient(ctx, client, snapshots, hubMCPOptions{
			Action:    "mcp_move",
			NodeID:    fs.Arg(0),
			SessionID: fs.Arg(1),
			Name:      fs.Arg(2),
			FromScope: srcScope,
			ToScope:   destScope,
			Restart:   *restart,
		})
		return err
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printHubMCPCommandResult(result)
	return nil
}

func runHubMCPWithClient(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions, opts hubMCPOptions) (hubMCPCommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return hubMCPCommandResult{}, fmt.Errorf("hub mcp client is required")
	}
	resolved, err := resolveHubSessionTarget(snapshots, opts.NodeID, opts.SessionID)
	if err != nil {
		return hubMCPCommandResult{}, err
	}
	result := hubMCPCommandResult{
		Action:       strings.TrimSpace(opts.Action),
		NodeID:       resolved.NodeID,
		NodeName:     resolved.NodeName,
		SessionID:    resolved.SessionID,
		SessionTitle: resolved.SessionTitle,
		Name:         strings.TrimSpace(opts.Name),
		Scope:        strings.TrimSpace(opts.Scope),
		FromScope:    strings.TrimSpace(opts.FromScope),
		ToScope:      strings.TrimSpace(opts.ToScope),
	}
	switch result.Action {
	case "mcp_list":
		raw, err := client.Command(ctx, resolved.NodeID, "mcp_list", hub.MCPListRequest{SessionID: resolved.SessionID})
		if err != nil {
			return hubMCPCommandResult{}, err
		}
		var resp hub.MCPListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubMCPCommandResult{}, fmt.Errorf("decode hub mcp list result: %w", err)
		}
		result.Local = appendSortedStrings(resp.Local)
		result.Global = appendSortedStrings(resp.Global)
		result.User = appendSortedStrings(resp.User)
		result.Catalog = appendSortedMCPCatalog(resp.Catalog)
		return result, nil
	case "mcp_attach", "mcp_detach":
		if result.Name == "" {
			return hubMCPCommandResult{}, fmt.Errorf("hub mcp name is required")
		}
		scope, err := normalizeHubMCPScope(result.Scope)
		if err != nil {
			return hubMCPCommandResult{}, err
		}
		result.Scope = scope
		raw, err := client.Command(ctx, resolved.NodeID, result.Action, hub.MCPMutateRequest{
			SessionID: resolved.SessionID,
			Name:      result.Name,
			Scope:     scope,
		})
		if err != nil {
			return hubMCPCommandResult{}, err
		}
		var resp hub.MCPMutateResponse
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &resp); err != nil {
				return hubMCPCommandResult{}, fmt.Errorf("decode hub mcp result: %w", err)
			}
			if strings.TrimSpace(resp.Scope) != "" {
				result.Scope = strings.TrimSpace(resp.Scope)
			}
		}
	case "mcp_move":
		if result.Name == "" {
			return hubMCPCommandResult{}, fmt.Errorf("hub mcp name is required")
		}
		toScope, err := normalizeHubMCPScope(result.ToScope)
		if err != nil {
			return hubMCPCommandResult{}, err
		}
		fromScope := strings.TrimSpace(result.FromScope)
		if fromScope == "" {
			fromScope, err = detectHubMCPScope(ctx, client, resolved, result.Name)
			if err != nil {
				return hubMCPCommandResult{}, err
			}
		} else {
			fromScope, err = normalizeHubMCPScope(fromScope)
			if err != nil {
				return hubMCPCommandResult{}, err
			}
		}
		result.FromScope = fromScope
		result.ToScope = toScope
		raw, err := client.Command(ctx, resolved.NodeID, "mcp_move", hub.MCPMoveRequest{
			SessionID: resolved.SessionID,
			Name:      result.Name,
			FromScope: fromScope,
			ToScope:   toScope,
		})
		if err != nil {
			return hubMCPCommandResult{}, err
		}
		var resp hub.MCPMoveResponse
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &resp); err != nil {
				return hubMCPCommandResult{}, fmt.Errorf("decode hub mcp move result: %w", err)
			}
			if strings.TrimSpace(resp.FromScope) != "" {
				result.FromScope = strings.TrimSpace(resp.FromScope)
			}
			if strings.TrimSpace(resp.ToScope) != "" {
				result.ToScope = strings.TrimSpace(resp.ToScope)
			}
		}
	default:
		return hubMCPCommandResult{}, fmt.Errorf("unsupported hub mcp action %q", result.Action)
	}
	if opts.Restart {
		if _, err := client.Command(ctx, resolved.NodeID, "restart", map[string]string{"session_id": resolved.SessionID}); err != nil {
			return hubMCPCommandResult{}, err
		}
		result.Restarted = true
	}
	return result, nil
}

func detectHubMCPScope(ctx context.Context, client hubShellClient, target resolvedHubSessionTarget, name string) (string, error) {
	raw, err := client.Command(ctx, target.NodeID, "mcp_list", hub.MCPListRequest{SessionID: target.SessionID})
	if err != nil {
		return "", err
	}
	var resp hub.MCPListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode hub mcp list result: %w", err)
	}
	var matches []string
	for _, scope := range []struct {
		name  string
		names []string
	}{
		{"local", resp.Local},
		{"global", resp.Global},
		{"user", resp.User},
	} {
		for _, candidate := range scope.names {
			if strings.TrimSpace(candidate) == name {
				matches = append(matches, scope.name)
				break
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("hub mcp %q is not attached to session %s", name, target.SessionID)
	default:
		return "", fmt.Errorf("hub mcp %q is attached in multiple scopes (%s); pass --from", name, strings.Join(matches, ", "))
	}
}

func hubMCPScopeFromFlags(scope string, global, user bool) (string, error) {
	count := 0
	if strings.TrimSpace(scope) != "" {
		count++
	}
	if global {
		count++
	}
	if user {
		count++
	}
	if count > 1 {
		return "", fmt.Errorf("choose only one of --scope, --global, or --user")
	}
	switch {
	case global:
		return "global", nil
	case user:
		return "user", nil
	default:
		return normalizeHubMCPScope(scope)
	}
}

func normalizeHubMCPScope(scope string) (string, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		return "local", nil
	}
	switch scope {
	case "local", "global", "user":
		return scope, nil
	default:
		return "", fmt.Errorf("unsupported MCP scope %q; use local, global, or user", scope)
	}
}

func appendSortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func appendSortedMCPCatalog(in []hub.MCPCatalogEntry) []hub.MCPCatalogEntry {
	out := append([]hub.MCPCatalogEntry(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func printHubMCPAttachedResult(result hubMCPCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	fmt.Printf("MCPs for %s on %s:\n", result.SessionTitleOrID(), node)
	printHubMCPScope("LOCAL", result.Local)
	printHubMCPScope("GLOBAL", result.Global)
	printHubMCPScope("USER", result.User)
}

func printHubMCPScope(label string, names []string) {
	fmt.Printf("%s:\n", label)
	if len(names) == 0 {
		fmt.Println("  none")
		return
	}
	for _, name := range names {
		fmt.Printf("  %s\n", name)
	}
}

func printHubMCPCatalogResult(result hubMCPCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	fmt.Printf("MCP catalog for %s on %s:\n", result.SessionTitleOrID(), node)
	if len(result.Catalog) == 0 {
		fmt.Println("  none")
		return
	}
	for _, entry := range result.Catalog {
		transport := strings.TrimSpace(entry.Transport)
		if transport == "" {
			transport = "stdio"
		}
		detail := transport
		if strings.TrimSpace(entry.Command) != "" {
			detail += " · " + strings.TrimSpace(entry.Command)
		} else if strings.TrimSpace(entry.URL) != "" {
			detail += " · " + strings.TrimSpace(entry.URL)
		}
		if strings.TrimSpace(entry.Description) != "" {
			detail += " — " + strings.TrimSpace(entry.Description)
		}
		fmt.Printf("  %s (%s)\n", entry.Name, detail)
	}
}

func printHubMCPCommandResult(result hubMCPCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	action := strings.TrimPrefix(strings.ReplaceAll(result.Action, "_", "-"), "mcp-")
	target := result.Name
	switch result.Action {
	case "mcp_attach", "mcp_detach":
		fmt.Printf("%s MCP %s (%s) on %s / %s\n", action, target, result.Scope, node, result.SessionTitleOrID())
	case "mcp_move":
		fmt.Printf("move MCP %s (%s -> %s) on %s / %s\n", target, result.FromScope, result.ToScope, node, result.SessionTitleOrID())
	default:
		fmt.Printf("%s MCP %s on %s / %s\n", action, target, node, result.SessionTitleOrID())
	}
	if result.Restarted {
		fmt.Println("Restarted remote session.")
	}
}

func (r hubMCPCommandResult) SessionTitleOrID() string {
	if strings.TrimSpace(r.SessionTitle) != "" {
		return strings.TrimSpace(r.SessionTitle)
	}
	return strings.TrimSpace(r.SessionID)
}

func printHubMCPsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck hub mcps <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  attached <node> <session>        List MCPs attached to a hub session")
	fmt.Fprintln(w, "  catalog <node> <session>         List MCPs available on a hub node")
	fmt.Fprintln(w, "  attach <node> <session> <mcp>    Attach an MCP to a hub session")
	fmt.Fprintln(w, "  detach <node> <session> <mcp>    Detach an MCP from a hub session")
	fmt.Fprintln(w, "  move <node> <session> <mcp> <scope> Move an MCP between local/global/user scopes")
}
