package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func handleSessionImportCodex(profile string, args []string) {
	fs := flag.NewFlagSet("session import-codex", flag.ExitOnError)
	title := fs.String("title", "", "Agent Deck title")
	titleShort := fs.String("t", "", "Agent Deck title")
	group := fs.String("group", "", "Group path")
	groupShort := fs.String("g", "", "Group path")
	pathFlag := fs.String("path", "", "Project path")
	command := fs.String("command", "", "Codex command")
	commandShort := fs.String("c", "", "Codex command")
	start := fs.Bool("start", false, "Start after import")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	quiet := fs.Bool("quiet", false, "Minimal output")
	quietShort := fs.Bool("q", false, "Minimal output")

	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		os.Exit(1)
	}

	out := NewCLIOutput(*jsonOutput, *quiet || *quietShort)
	if fs.NArg() != 1 {
		out.Error("usage: agent-deck session import-codex <session-id-or-name>", ErrCodeInvalidOperation)
		os.Exit(1)
	}

	resolvedCommand := mergeFlags(*command, *commandShort)
	if strings.TrimSpace(resolvedCommand) == "" {
		resolvedCommand = "codex"
	}
	if !session.IsSupportedCodexLaunchCommand(resolvedCommand) {
		out.Error(fmt.Sprintf("unsupported Codex command %q: use optional env assignments followed by codex or codex-*", resolvedCommand), ErrCodeInvalidOperation)
		os.Exit(1)
	}

	target := fs.Arg(0)
	codexHome := session.GetCodexHomeDirForCommand(resolvedCommand)
	entry, err := session.ResolveCodexIndexTarget(codexHome, target)
	if err != nil {
		out.Error(formatCodexImportResolveError(err), ErrCodeInvalidOperation)
		os.Exit(1)
	}

	projectPath := strings.TrimSpace(*pathFlag)
	if projectPath == "" {
		projectPath, err = os.Getwd()
	} else {
		projectPath, err = resolveAddPath(projectPath)
	}
	if err != nil {
		out.Error(fmt.Sprintf("failed to resolve project path: %v", err), ErrCodeInvalidOperation)
		os.Exit(1)
	}

	resolvedTitle := mergeFlags(*title, *titleShort)
	if strings.TrimSpace(resolvedTitle) == "" {
		resolvedTitle = strings.TrimSpace(entry.ThreadName)
	}
	if resolvedTitle == "" {
		resolvedTitle = codexImportFallbackTitle(entry.ID)
	}

	resolvedGroup := mergeFlags(*group, *groupShort)
	if strings.TrimSpace(resolvedGroup) == "" {
		resolvedGroup = session.DefaultGroupPath
	}

	inst := session.NewInstanceWithGroupAndTool(resolvedTitle, projectPath, resolvedGroup, "codex")
	inst.Command = resolvedCommand
	inst.CodexSessionID = entry.ID
	inst.CodexDetectedAt = entry.UpdatedAt
	inst.Status = session.StatusStopped

	if *start {
		if err := inst.Start(); err != nil {
			out.Error(fmt.Sprintf("failed to start imported codex session: %v", err), ErrCodeInvalidOperation)
			os.Exit(1)
		}
	}

	storage, instances, groups, err := loadSessionData(profile)
	if err != nil {
		out.Error(err.Error(), ErrCodeNotFound)
		os.Exit(1)
	}
	instances = append(instances, inst)
	if err := saveSessionData(storage, instances, groups); err != nil {
		out.Error(fmt.Sprintf("failed to save imported session: %v", err), ErrCodeInvalidOperation)
		os.Exit(1)
	}

	out.Success("Imported Codex session: "+inst.Title, map[string]interface{}{
		"success":        true,
		"id":             inst.ID,
		"title":          inst.Title,
		"group":          inst.GroupPath,
		"path":           inst.ProjectPath,
		"codexSessionId": inst.CodexSessionID,
		"status":         inst.Status,
	})
}

func formatCodexImportResolveError(err error) string {
	var ambiguous *session.CodexSessionAmbiguousError
	if errors.As(err, &ambiguous) {
		var b strings.Builder
		fmt.Fprintf(&b, "failed to resolve codex session: %v; import by UUID:", err)
		for _, match := range ambiguous.Matches {
			fmt.Fprintf(&b, "\n  %s  %s  %s", match.ID, match.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"), match.ThreadName)
		}
		return b.String()
	}
	return fmt.Sprintf("failed to resolve codex session: %v", err)
}

func codexImportFallbackTitle(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
