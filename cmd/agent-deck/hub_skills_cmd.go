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
	"github.com/asheshgoplani/agent-deck/internal/session"
)

type hubSkillOptions struct {
	Action     string
	NodeID     string
	SessionID  string
	Name       string
	Source     string
	Restart    bool
	JSONOutput bool
}

type hubSkillCommandResult struct {
	Action       string                           `json:"action"`
	NodeID       string                           `json:"node_id"`
	NodeName     string                           `json:"node_name,omitempty"`
	SessionID    string                           `json:"session_id"`
	SessionTitle string                           `json:"session_title,omitempty"`
	Name         string                           `json:"name,omitempty"`
	Source       string                           `json:"source,omitempty"`
	Restarted    bool                             `json:"restarted,omitempty"`
	Catalog      []session.SkillCandidate         `json:"catalog,omitempty"`
	Attached     []session.ProjectSkillAttachment `json:"attached,omitempty"`
	Skill        *session.ProjectSkillAttachment  `json:"skill,omitempty"`
}

func handleHubSkills(profile string, args []string) error {
	if len(args) == 0 || args[0] == "attached" || args[0] == "list" || args[0] == "ls" || args[0] == "catalog" {
		return handleHubSkillsList(profile, args)
	}
	switch args[0] {
	case "attach":
		return handleHubSkillsMutate(profile, "skill_attach", args[1:])
	case "detach":
		return handleHubSkillsMutate(profile, "skill_detach", args[1:])
	case "help", "--help", "-h":
		printHubSkillsUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown hub skills command %q", args[0])
	}
}

func handleHubSkillsList(profile string, args []string) error {
	action := "skill_list"
	showCatalogOnly := false
	if len(args) > 0 {
		switch args[0] {
		case "attached", "list", "ls":
			args = args[1:]
		case "catalog":
			args = args[1:]
			showCatalogOnly = true
		}
	}
	fs := flag.NewFlagSet("hub skills list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub skills [attached|catalog] <node-id-or-name> <session-id-or-title> [--json]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: agent-deck hub skills [attached|catalog] <node-id-or-name> <session-id-or-title>")
	}
	var result hubSkillCommandResult
	err := withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubSkillWithClient(ctx, client, snapshots, hubSkillOptions{
			Action:    action,
			NodeID:    fs.Arg(0),
			SessionID: fs.Arg(1),
		})
		return err
	})
	if err != nil {
		return err
	}
	if showCatalogOnly {
		result.Attached = nil
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if showCatalogOnly {
		printHubSkillCatalogResult(result)
		return nil
	}
	printHubSkillListResult(result)
	return nil
}

func handleHubSkillsMutate(profile, action string, args []string) error {
	fs := flag.NewFlagSet("hub skills "+strings.TrimPrefix(action, "skill_"), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	source := fs.String("source", "", "Skill source qualifier, e.g. pool")
	restart := fs.Bool("restart", false, "Restart the remote session after changing skills")
	connectTimeout := fs.Duration("connect-timeout", 15*time.Second, "Maximum time to wait for the hub websocket connection")
	resolveTimeout := fs.Duration("resolve-timeout", 5*time.Second, "Maximum time to wait for node/session resolution")
	fs.Usage = func() {
		name := strings.TrimPrefix(action, "skill_")
		fmt.Fprintf(os.Stderr, "Usage: agent-deck hub skills %s <node-id-or-name> <session-id-or-title> <skill-name> [--source pool] [--restart]\n", name)
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 3 {
		return fmt.Errorf("usage: agent-deck hub skills %s <node-id-or-name> <session-id-or-title> <skill-name>", strings.TrimPrefix(action, "skill_"))
	}
	var result hubSkillCommandResult
	err := withConnectedHubSessionClient(profile, *connectTimeout, *resolveTimeout, func(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions) error {
		var err error
		result, err = runHubSkillWithClient(ctx, client, snapshots, hubSkillOptions{
			Action:    action,
			NodeID:    fs.Arg(0),
			SessionID: fs.Arg(1),
			Name:      fs.Arg(2),
			Source:    *source,
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
	printHubSkillCommandResult(result)
	return nil
}

func runHubSkillWithClient(ctx context.Context, client hubShellClient, snapshots []hub.NodeSessions, opts hubSkillOptions) (hubSkillCommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return hubSkillCommandResult{}, fmt.Errorf("hub skill client is required")
	}
	resolved, err := resolveHubSessionTarget(snapshots, opts.NodeID, opts.SessionID)
	if err != nil {
		return hubSkillCommandResult{}, err
	}
	result := hubSkillCommandResult{
		Action:       strings.TrimSpace(opts.Action),
		NodeID:       resolved.NodeID,
		NodeName:     resolved.NodeName,
		SessionID:    resolved.SessionID,
		SessionTitle: resolved.SessionTitle,
		Name:         strings.TrimSpace(opts.Name),
		Source:       strings.TrimSpace(opts.Source),
	}
	switch result.Action {
	case "skill_list":
		raw, err := client.Command(ctx, resolved.NodeID, "skill_list", hub.SkillListRequest{SessionID: resolved.SessionID})
		if err != nil {
			return hubSkillCommandResult{}, err
		}
		var resp hub.SkillListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return hubSkillCommandResult{}, fmt.Errorf("decode hub skill list result: %w", err)
		}
		result.Catalog = sortedSkillCandidates(resp.Catalog)
		result.Attached = sortedSkillAttachments(resp.Attached)
		return result, nil
	case "skill_attach", "skill_detach":
		if result.Name == "" {
			return hubSkillCommandResult{}, fmt.Errorf("hub skill name is required")
		}
		raw, err := client.Command(ctx, resolved.NodeID, result.Action, hub.SkillMutateRequest{
			SessionID: resolved.SessionID,
			Name:      result.Name,
			Source:    result.Source,
		})
		if err != nil {
			return hubSkillCommandResult{}, err
		}
		if len(raw) > 0 {
			var resp hub.SkillMutateResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return hubSkillCommandResult{}, fmt.Errorf("decode hub skill result: %w", err)
			}
			result.Skill = resp.Skill
			if resp.Skill != nil {
				result.Name = strings.TrimSpace(resp.Skill.Name)
				result.Source = strings.TrimSpace(resp.Skill.Source)
			}
		}
	default:
		return hubSkillCommandResult{}, fmt.Errorf("unsupported hub skill action %q", result.Action)
	}
	if opts.Restart {
		if _, err := client.Command(ctx, resolved.NodeID, "restart", map[string]string{"session_id": resolved.SessionID}); err != nil {
			return hubSkillCommandResult{}, err
		}
		result.Restarted = true
	}
	return result, nil
}

func sortedSkillCandidates(in []session.SkillCandidate) []session.SkillCandidate {
	out := append([]session.SkillCandidate(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		leftName := strings.ToLower(out[i].Name)
		rightName := strings.ToLower(out[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func sortedSkillAttachments(in []session.ProjectSkillAttachment) []session.ProjectSkillAttachment {
	out := append([]session.ProjectSkillAttachment(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		leftName := strings.ToLower(out[i].Name)
		rightName := strings.ToLower(out[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func printHubSkillListResult(result hubSkillCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	fmt.Printf("Skills for %s on %s:\n", result.SessionTitleOrID(), node)
	fmt.Println("ATTACHED:")
	printHubSkillAttachments(result.Attached)
	fmt.Println("CATALOG:")
	printHubSkillCandidates(result.Catalog)
}

func printHubSkillCatalogResult(result hubSkillCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	fmt.Printf("Skill catalog for %s on %s:\n", result.SessionTitleOrID(), node)
	printHubSkillCandidates(result.Catalog)
}

func printHubSkillCandidates(skills []session.SkillCandidate) {
	if len(skills) == 0 {
		fmt.Println("  none")
		return
	}
	for _, skill := range skills {
		source := strings.TrimSpace(skill.Source)
		if source != "" {
			fmt.Printf("  %s [%s]\n", skill.Name, source)
		} else {
			fmt.Printf("  %s\n", skill.Name)
		}
	}
}

func printHubSkillAttachments(skills []session.ProjectSkillAttachment) {
	if len(skills) == 0 {
		fmt.Println("  none")
		return
	}
	for _, skill := range skills {
		source := strings.TrimSpace(skill.Source)
		if source != "" {
			fmt.Printf("  %s [%s]\n", skill.Name, source)
		} else {
			fmt.Printf("  %s\n", skill.Name)
		}
	}
}

func printHubSkillCommandResult(result hubSkillCommandResult) {
	node := result.NodeName
	if node == "" {
		node = result.NodeID
	}
	action := strings.TrimPrefix(strings.ReplaceAll(result.Action, "_", "-"), "skill-")
	target := result.Name
	if result.Source != "" {
		target += " [" + result.Source + "]"
	}
	fmt.Printf("%s skill %s on %s / %s\n", action, target, node, result.SessionTitleOrID())
	if result.Restarted {
		fmt.Println("Restarted remote session.")
	}
}

func (r hubSkillCommandResult) SessionTitleOrID() string {
	if strings.TrimSpace(r.SessionTitle) != "" {
		return strings.TrimSpace(r.SessionTitle)
	}
	return strings.TrimSpace(r.SessionID)
}

func printHubSkillsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck hub skills <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  attached <node> <session>          List attached skills and remote catalog")
	fmt.Fprintln(w, "  catalog <node> <session>           List the remote node skill catalog")
	fmt.Fprintln(w, "  attach <node> <session> <skill>    Attach a skill to a hub session")
	fmt.Fprintln(w, "  detach <node> <session> <skill>    Detach a skill from a hub session")
}
