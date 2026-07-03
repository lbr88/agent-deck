package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type HandoverTarget string

const (
	HandoverTargetClaude   HandoverTarget = "claude"
	HandoverTargetCodex    HandoverTarget = "codex"
	HandoverTargetOpenCode HandoverTarget = "opencode"
	HandoverTargetKiro     HandoverTarget = "kiro"
)

type HandoverOptions struct {
	Target      HandoverTarget
	Title       string
	GroupPath   string
	ProjectPath string
	Message     string
	Start       bool
	Peers       []*Instance
}

type HandoverResult struct {
	Source         *Instance
	Target         *Instance
	HandoverPrompt string
	Started        bool
	Warning        string
}

const handoverLatestOutputMaxChars = 10000

var handoverLastResponse = func(inst *Instance, peers []*Instance) (*ResponseOutput, error) {
	return inst.GetLastResponseBestEffortChecked(peers)
}

// HandoverSession creates a new target-tool session and deterministic context
// packet from an existing Agent Deck session. It intentionally does not persist
// or start the target; callers own storage and lifecycle side effects.
func HandoverSession(source *Instance, opts HandoverOptions) (*HandoverResult, error) {
	if source == nil {
		return nil, fmt.Errorf("source session is required")
	}

	targetTool, err := normalizeHandoverTarget(opts.Target)
	if err != nil {
		return nil, err
	}
	sourceTool, err := handoverSourceTool(source)
	if err != nil {
		return nil, err
	}
	if targetTool == sourceTool {
		return nil, fmt.Errorf("cannot hand over to the same tool %q; use session fork for same-tool continuity when available", sourceTool)
	}

	warning := ""
	projectPath := strings.TrimSpace(opts.ProjectPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(source.ProjectPath)
	}
	if projectPath == "" {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			projectPath = cwd
			warning = joinHandoverWarnings(warning, "source project path was empty; used current working directory")
		} else {
			return nil, fmt.Errorf("source project path is empty and current directory is unavailable: %w", cwdErr)
		}
	}

	groupPath := strings.TrimSpace(opts.GroupPath)
	if groupPath == "" {
		groupPath = strings.TrimSpace(source.GroupPath)
	}
	if groupPath == "" {
		groupPath = DefaultGroupPath
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = uniqueHandoverDefaultTitle(source.Title, targetTool, opts.Peers)
	}

	target := NewInstanceWithGroupAndTool(title, projectPath, groupPath, targetTool)
	target.Command = targetTool
	target.Status = StatusStopped
	copyHandoverMetadata(source, target)

	latestOutput, latestWarning := handoverLatestOutput(source, opts.Peers)
	warning = joinHandoverWarnings(warning, latestWarning)

	prompt := buildHandoverPrompt(source, target, sourceTool, targetTool, latestOutput, strings.TrimSpace(opts.Message))
	return &HandoverResult{
		Source:         source,
		Target:         target,
		HandoverPrompt: prompt,
		Started:        false,
		Warning:        warning,
	}, nil
}

func normalizeHandoverTarget(target HandoverTarget) (string, error) {
	switch strings.ToLower(strings.TrimSpace(string(target))) {
	case string(HandoverTargetClaude):
		return string(HandoverTargetClaude), nil
	case string(HandoverTargetCodex):
		return string(HandoverTargetCodex), nil
	case string(HandoverTargetOpenCode):
		return string(HandoverTargetOpenCode), nil
	case string(HandoverTargetKiro):
		return string(HandoverTargetKiro), nil
	default:
		return "", fmt.Errorf("unsupported handover target %q: allowed targets are claude, codex, opencode, kiro", target)
	}
}

func handoverSourceTool(source *Instance) (string, error) {
	switch {
	case IsClaudeCompatible(source.Tool):
		return string(HandoverTargetClaude), nil
	case IsCodexCompatible(source.Tool):
		return string(HandoverTargetCodex), nil
	case source.Tool == string(HandoverTargetOpenCode):
		return string(HandoverTargetOpenCode), nil
	case source.Tool == string(HandoverTargetKiro):
		return string(HandoverTargetKiro), nil
	default:
		return "", fmt.Errorf("unsupported handover source tool %q: supported source tools are claude, codex, opencode, kiro", source.Tool)
	}
}

func uniqueHandoverDefaultTitle(sourceTitle, targetTool string, peers []*Instance) string {
	sourceTitle = strings.TrimSpace(sourceTitle)
	if sourceTitle == "" {
		sourceTitle = "session"
	}
	base := fmt.Sprintf("%s (%s)", sourceTitle, targetTool)
	used := make(map[string]bool, len(peers))
	for _, peer := range peers {
		if peer == nil {
			continue
		}
		used[peer.Title] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%s %d)", sourceTitle, targetTool, i)
		if !used[candidate] {
			return candidate
		}
	}
	return fmt.Sprintf("%s (%s %d)", sourceTitle, targetTool, time.Now().Unix())
}

func copyHandoverMetadata(source, target *Instance) {
	if source.Sandbox != nil {
		target.Sandbox = cloneSandboxConfig(source.Sandbox)
	}
	if IsClaudeCompatible(target.Tool) {
		target.Account = source.Account
		target.Channels = append([]string(nil), source.Channels...)
		target.Plugins = append([]string(nil), source.Plugins...)
		target.PluginChannelLinkDisabled = source.PluginChannelLinkDisabled
		target.AutoLinkedChannels = append([]string(nil), source.AutoLinkedChannels...)
		target.InheritTelegramEnv = source.InheritTelegramEnv
	}
	if strings.TrimSpace(target.ProjectPath) == strings.TrimSpace(source.ProjectPath) {
		target.WorktreePath = source.WorktreePath
		target.WorktreeRepoRoot = source.WorktreeRepoRoot
		target.WorktreeBranch = source.WorktreeBranch
		target.WorktreeType = source.WorktreeType
	}
}

func cloneSandboxConfig(in *SandboxConfig) *SandboxConfig {
	if in == nil {
		return nil
	}
	out := *in
	if in.CPULimit != nil {
		v := *in.CPULimit
		out.CPULimit = &v
	}
	if in.MemoryLimit != nil {
		v := *in.MemoryLimit
		out.MemoryLimit = &v
	}
	if in.ExtraVolumes != nil {
		out.ExtraVolumes = make(map[string]string, len(in.ExtraVolumes))
		for k, v := range in.ExtraVolumes {
			out.ExtraVolumes[k] = v
		}
	}
	return &out
}

func handoverLatestOutput(source *Instance, peers []*Instance) (string, string) {
	resp, err := handoverLastResponse(source, peers)
	if err != nil {
		return "No latest output was available.", fmt.Sprintf("latest output unavailable: %v", err)
	}
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "No latest output was available.", ""
	}
	return capHandoverText(resp.Content, handoverLatestOutputMaxChars), ""
}

func capHandoverText(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return strings.TrimSpace(s[:limit]) + fmt.Sprintf("\n[truncated to %d characters]", limit)
}

func buildHandoverPrompt(source, target *Instance, sourceTool, targetTool, latestOutput, message string) string {
	sourceToolID := sourceHandoverToolSessionID(source, sourceTool)
	if sourceToolID == "" {
		sourceToolID = "unknown"
	}
	if message == "" {
		message = "Continue the task from the context above."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are continuing work from an Agent Deck session handed over from %s to %s.\n\n", sourceTool, targetTool)
	b.WriteString("Source session:\n")
	fmt.Fprintf(&b, "- Agent Deck title: %s\n", source.Title)
	fmt.Fprintf(&b, "- Agent Deck id: %s\n", source.ID)
	fmt.Fprintf(&b, "- Source tool: %s\n", sourceTool)
	fmt.Fprintf(&b, "- Source tool session id: %s\n", sourceToolID)
	fmt.Fprintf(&b, "- Project path: %s\n", target.ProjectPath)
	fmt.Fprintf(&b, "- Group: %s\n\n", target.GroupPath)
	b.WriteString("Git context:\n")
	b.WriteString(handoverGitContext(target.ProjectPath))
	b.WriteString("\n\n")
	b.WriteString("Latest useful source output:\n")
	b.WriteString(latestOutput)
	b.WriteString("\n\n")
	b.WriteString("Operator instruction:\n")
	b.WriteString(message)
	b.WriteString("\n\n")
	b.WriteString("Important:\n")
	b.WriteString("- Native transcript history was not migrated.\n")
	b.WriteString("- Treat this handover as the context to continue from.\n")
	b.WriteString("- Inspect the repository before making changes.\n")
	return b.String()
}

func sourceHandoverToolSessionID(source *Instance, sourceTool string) string {
	switch sourceTool {
	case string(HandoverTargetClaude):
		return strings.TrimSpace(source.ClaudeSessionID)
	case string(HandoverTargetCodex):
		return strings.TrimSpace(source.CodexSessionID)
	case string(HandoverTargetOpenCode):
		return strings.TrimSpace(source.OpenCodeSessionID)
	case string(HandoverTargetKiro):
		return strings.TrimSpace(source.KiroSessionID)
	default:
		return ""
	}
}

func handoverGitContext(projectPath string) string {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return "git context unavailable: project path is empty"
	}

	if out, err := runHandoverGit(projectPath, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return "not a git repository / unavailable"
	}

	branch, branchErr := runHandoverGit(projectPath, "branch", "--show-current")
	if branchErr != nil || strings.TrimSpace(branch) == "" {
		branch = "detached"
	}
	head, headErr := runHandoverGit(projectPath, "rev-parse", "--short", "HEAD")
	if headErr != nil {
		head = "unavailable"
	}
	status, statusErr := runHandoverGit(projectPath, "status", "--short")
	if statusErr != nil {
		status = "unavailable"
	} else if strings.TrimSpace(status) == "" {
		status = "clean"
	}

	return fmt.Sprintf("Branch: %s\nHEAD: %s\nStatus:\n%s",
		strings.TrimSpace(branch),
		strings.TrimSpace(head),
		strings.TrimSpace(status),
	)
}

func runHandoverGit(projectPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmdArgs := append([]string{"-C", projectPath}, args...)
	out, err := exec.CommandContext(ctx, "git", cmdArgs...).CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func joinHandoverWarnings(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}
