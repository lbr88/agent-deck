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

type hubPluginOptions struct {
	Action        string
	NodeID        string
	SessionID     string
	Name          string
	Restart       bool
	NoChannelLink bool
	JSONOutput    bool
}

type hubPluginCommandResult struct {
	Action                    string                   `json:"action"`
	NodeID                    string                   `json:"node_id"`
	NodeName                  string                   `json:"node_name,omitempty"`
	SessionID                 string                   `json:"session_id"`
	SessionTitle              string                   `json:"session_title,omitempty"`
	Name                      string                   `json:"name,omitempty"`
	Restarted                 bool                     `json:"restarted,omitempty"`
	Catalog                   []hub.PluginCatalogEntry `json:"catalog,omitempty"`
	Plugins                   []string                 `json:"plugins,omitempty"`
	Channels                  []string                 `json:"channels,omitempty"`
	PluginChannelLinkDisabled bool                     `json:"plugin_channel_link_disabled,omitempty"`
}

func handleHubPlugins(profile string, args []string) error {
	if len(args) == 0 || args[0] == "attached" || args[0] == "list" || args[0] == "ls" || args[0] == "catalog" {
		return handleHubPluginsList(profile, args)
	}
	switch args[0] {
	case "attach":
		return handleHubPluginsMutate(profile, "plugin_attach", args[1:])
	case "detach":
		return handleHubPluginsMutate(profile, "plugin_detach", args[1:])
	case "help", "--help", "-h":
		printHubPluginsUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown hub plugins command %q", args[0])
	}
}

func handleHubPluginsList(profile string, args []string) error {
	showCatalogOnly := false
	if len(args) > 0 {
		switch args[0] {
		case "attached", "list", "ls":
			args = args[1:]
		case "catalog":
			showCatalogOnly = true
			args = args[1:]
		}
	}
	fs := flag.NewFlagSet("hub plugins list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub plugins [attached|catalog] <node-id-or-name> <session-id-or-title> [--json]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub plugins [attached|catalog] <node-id-or-name> <session-id-or-title>")
	}
	var result hubPluginCommandResult
	err := withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubPluginWithClient(ctx, client, snapshots, hubPluginOptions{Action: "plugin_list", NodeID: fs.Arg(0), SessionID: fs.Arg(1)})
		return err
	})
	if err != nil {
		return err
	}
	if showCatalogOnly {
		result.Plugins = nil
		result.Channels = nil
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if showCatalogOnly {
		printHubPluginCatalogResult(result)
		return nil
	}
	printHubPluginListResult(result)
	return nil
}

func handleHubPluginsMutate(profile, action string, args []string) error {
	fs := flag.NewFlagSet("hub plugins "+strings.TrimPrefix(action, "plugin_"), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	restart := fs.Bool("restart", false, "Restart the remote session after changing plugins")
	noChannelLink := fs.Bool("no-channel-link", false, "For channel-emitting plugins, do not auto-add channels")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		name := strings.TrimPrefix(action, "plugin_")
		fmt.Fprintf(os.Stderr, "Usage: agent-deck hub plugins %s <node-id-or-name> <session-id-or-title> <plugin-name> [--restart] [--no-channel-link]\n", name)
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 3 {
		return fmt.Errorf("usage: agent-deck hub plugins %s <node-id-or-name> <session-id-or-title> <plugin-name>", strings.TrimPrefix(action, "plugin_"))
	}
	var result hubPluginCommandResult
	err := withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubPluginWithClient(ctx, client, snapshots, hubPluginOptions{
			Action:        action,
			NodeID:        fs.Arg(0),
			SessionID:     fs.Arg(1),
			Name:          fs.Arg(2),
			Restart:       *restart,
			NoChannelLink: *noChannelLink,
		})
		return err
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printHubPluginCommandResult(result)
	return nil
}

func runHubPluginWithClient(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions, opts hubPluginOptions) (hubPluginCommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return hubPluginCommandResult{}, fmt.Errorf("hub plugin client is required")
	}
	resolved, err := resolveHubSessionTarget(snapshots, opts.NodeID, opts.SessionID)
	if err != nil {
		return hubPluginCommandResult{}, err
	}
	result := hubPluginCommandResult{
		Action:       strings.TrimSpace(opts.Action),
		NodeID:       resolved.NodeID,
		NodeName:     resolved.NodeName,
		SessionID:    resolved.SessionID,
		SessionTitle: resolved.SessionTitle,
		Name:         strings.TrimSpace(opts.Name),
	}
	switch result.Action {
	case "plugin_list":
		raw, err := client.Command(ctx, resolved.NodeID, "plugin_list", hub.PluginListRequest{SessionID: resolved.SessionID})
		if err != nil {
			return hubPluginCommandResult{}, err
		}
		var resp hub.PluginListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubPluginCommandResult{}, fmt.Errorf("decode hub plugin list result: %w", err)
		}
		result.Catalog = sortedHubPluginCatalog(resp.Catalog)
		result.Plugins = sortedStrings(resp.Plugins)
		result.Channels = sortedStrings(resp.Channels)
		result.PluginChannelLinkDisabled = resp.PluginChannelLinkDisabled
		return result, nil
	case "plugin_attach", "plugin_detach":
		if result.Name == "" {
			return hubPluginCommandResult{}, fmt.Errorf("hub plugin name is required")
		}
		raw, err := client.Command(ctx, resolved.NodeID, result.Action, hub.PluginMutateRequest{SessionID: resolved.SessionID, Name: result.Name, NoChannelLink: opts.NoChannelLink})
		if err != nil {
			return hubPluginCommandResult{}, err
		}
		if len(raw) > 0 {
			var resp hub.PluginMutateResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return hubPluginCommandResult{}, fmt.Errorf("decode hub plugin result: %w", err)
			}
			result.Plugins = sortedStrings(resp.Plugins)
			result.Channels = sortedStrings(resp.Channels)
			result.PluginChannelLinkDisabled = resp.PluginChannelLinkDisabled
		}
	default:
		return hubPluginCommandResult{}, fmt.Errorf("unsupported hub plugin action %q", result.Action)
	}
	if opts.Restart {
		if _, err := client.Command(ctx, resolved.NodeID, "restart", map[string]string{"session_id": resolved.SessionID}); err != nil {
			return hubPluginCommandResult{}, err
		}
		result.Restarted = true
	}
	return result, nil
}

func sortedHubPluginCatalog(in []hub.PluginCatalogEntry) []hub.PluginCatalogEntry {
	out := append([]hub.PluginCatalogEntry(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func printHubPluginListResult(result hubPluginCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	fmt.Printf("Plugins for %s on %s:\n", result.SessionTitleOrID(), node)
	fmt.Println("ATTACHED:")
	printHubPluginNames(result.Plugins)
	fmt.Println("CATALOG:")
	printHubPluginCatalog(result.Catalog)
	if result.PluginChannelLinkDisabled {
		fmt.Println("auto-channel-link disabled")
	}
}

func printHubPluginCatalogResult(result hubPluginCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	fmt.Printf("Plugin catalog for %s on %s:\n", result.SessionTitleOrID(), node)
	printHubPluginCatalog(result.Catalog)
}

func printHubPluginCatalog(entries []hub.PluginCatalogEntry) {
	if len(entries) == 0 {
		fmt.Println("  none")
		return
	}
	for _, entry := range entries {
		fmt.Printf("  %s", entry.Name)
		if entry.ID != "" {
			fmt.Printf(" (%s)", entry.ID)
		}
		fmt.Println()
	}
}

func printHubPluginNames(names []string) {
	if len(names) == 0 {
		fmt.Println("  none")
		return
	}
	for _, name := range names {
		fmt.Printf("  %s\n", name)
	}
}

func printHubPluginCommandResult(result hubPluginCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	action := strings.TrimPrefix(strings.ReplaceAll(result.Action, "_", "-"), "plugin-")
	fmt.Printf("%s plugin %s on %s / %s\n", action, result.Name, node, result.SessionTitleOrID())
	if result.Restarted {
		fmt.Println("Restarted remote session.")
	}
}

func (r hubPluginCommandResult) SessionTitleOrID() string {
	if strings.TrimSpace(r.SessionTitle) != "" {
		return strings.TrimSpace(r.SessionTitle)
	}
	return strings.TrimSpace(r.SessionID)
}

func printHubPluginsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck hub plugins <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  attached <node> <session>            List attached plugins and remote catalog")
	fmt.Fprintln(w, "  catalog <node> <session>             List the remote node plugin catalog")
	fmt.Fprintln(w, "  attach <node> <session> <plugin>     Attach a plugin to a hub session")
	fmt.Fprintln(w, "  detach <node> <session> <plugin>     Detach a plugin from a hub session")
}
