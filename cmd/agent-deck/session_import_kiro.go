package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// handleSessionImportKiro imports an existing saved Kiro CLI session into Agent Deck.
func handleSessionImportKiro(profile string, args []string) {
	fs := flag.NewFlagSet("session import-kiro", flag.ExitOnError)
	title := fs.String("title", "", "Agent Deck session title (defaults to Kiro title)")
	titleShort := fs.String("t", "", "Agent Deck session title (short)")
	group := fs.String("group", "", "Group path (defaults to project-derived group)")
	groupShort := fs.String("g", "", "Group path (short)")
	pathFlag := fs.String("path", "", "Project path override (defaults to Kiro cwd, then current directory)")
	command := fs.String("command", "", "Kiro command")
	commandShort := fs.String("c", "", "Kiro command (short)")
	start := fs.Bool("start", false, "Start the imported session after saving it")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	quiet := fs.Bool("quiet", false, "Minimal output")
	quietShort := fs.Bool("q", false, "Minimal output (short)")

	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session import-kiro <session-id-or-title> [options]")
		fmt.Println()
		fmt.Println("Import an existing saved Kiro CLI session into Agent Deck.")
		fmt.Println()
		fmt.Println("Options:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		os.Exit(1)
	}

	out := NewCLIOutput(*jsonOutput, *quiet || *quietShort)
	target := strings.TrimSpace(fs.Arg(0))
	if target == "" {
		out.Error("usage: agent-deck session import-kiro <session-id-or-title>", ErrCodeInvalidOperation)
		os.Exit(1)
	}

	entry, err := session.ResolveKiroSavedSession(session.KiroSessionsDir(), target)
	if err != nil {
		out.Error(formatKiroImportResolveError(err), ErrCodeInvalidOperation)
		os.Exit(1)
	}

	projectPath := strings.TrimSpace(*pathFlag)
	if projectPath != "" {
		projectPath, err = resolveAddPath(projectPath)
		if err != nil {
			out.Error(fmt.Sprintf("failed to resolve project path: %v", err), ErrCodeInvalidOperation)
			os.Exit(1)
		}
	}
	fallbackPath, _ := os.Getwd()

	resolvedCommand := mergeFlags(*command, *commandShort)
	if strings.TrimSpace(resolvedCommand) == "" {
		resolvedCommand = session.GetKiroCommand()
	}

	storage, instances, groupsData, err := loadSessionData(profile)
	if err != nil {
		out.Error(err.Error(), ErrCodeNotFound)
		os.Exit(1)
	}

	resolvedTitle := mergeFlags(*title, *titleShort)
	resolvedGroup := mergeFlags(*group, *groupShort)
	if strings.TrimSpace(resolvedGroup) != "" {
		groupTree := session.NewGroupTreeWithGroups(instances, groupsData)
		resolvedGroup = resolveGroupPathForAdd(groupTree, resolvedGroup)
	}

	imported, err := session.NewKiroImportedInstance(entry, session.KiroImportOptions{
		Title:               resolvedTitle,
		GroupPath:           resolvedGroup,
		ProjectPath:         projectPath,
		FallbackProjectPath: fallbackPath,
		Command:             resolvedCommand,
	})
	if err != nil {
		out.Error(err.Error(), ErrCodeInvalidOperation)
		os.Exit(1)
	}
	if strings.TrimSpace(resolvedGroup) == "" {
		imported.GroupPath = session.GroupPathForProject(imported.ProjectPath)
	}

	if isDupe, existingInst := isDuplicateSession(instances, imported.Title, imported.ProjectPath); isDupe {
		out.Error(
			fmt.Sprintf("session already exists with same title and path: %s (%s)", existingInst.Title, existingInst.ID),
			ErrCodeAlreadyExists,
		)
		os.Exit(1)
	}
	if existingInst := findKiroImportSessionIDConflict(instances, imported.KiroSessionID); existingInst != nil {
		out.Error(
			fmt.Sprintf("Kiro session %q is already imported by Agent Deck session %q (%s)", imported.KiroSessionID, existingInst.Title, existingInst.ID),
			ErrCodeAlreadyExists,
		)
		os.Exit(1)
	}

	instances = append(instances, imported)
	groupTree := session.NewGroupTreeWithGroups(instances, groupsData)
	if imported.GroupPath != "" {
		groupTree.CreateGroupPath(imported.GroupPath)
	}
	if err := storage.InsertSessionAndVerify(imported, groupTree); err != nil {
		out.Error(fmt.Sprintf("failed to save imported session: %v", err), ErrCodeInvalidOperation)
		os.Exit(1)
	}

	if *start {
		session.ScrubProcessEnvForChildLaunch(imported)
		if err := imported.Start(); err != nil {
			out.Error(fmt.Sprintf("failed to start imported Kiro session: %v", err), ErrCodeInvalidOperation)
			os.Exit(1)
		}
		imported.PostStartSync(3 * time.Second)
		if err := storage.InsertSessionAndVerify(imported, groupTree); err != nil {
			out.Error(fmt.Sprintf("failed to save imported session state: %v", err), ErrCodeInvalidOperation)
			os.Exit(1)
		}
	}

	result := map[string]interface{}{
		"success":         true,
		"id":              imported.ID,
		"title":           imported.Title,
		"status":          imported.Status,
		"tool":            imported.Tool,
		"command":         imported.Command,
		"group":           imported.GroupPath,
		"project_path":    imported.ProjectPath,
		"kiro_session_id": imported.KiroSessionID,
		"started":         *start,
	}
	out.Success(fmt.Sprintf("Imported Kiro session: %s", imported.Title), result)
}

func formatKiroImportResolveError(err error) string {
	var ambiguous *session.KiroSessionAmbiguousError
	if errors.As(err, &ambiguous) {
		var b strings.Builder
		fmt.Fprintf(&b, "failed to resolve kiro session: %v; import by session ID:", err)
		for _, match := range ambiguous.Matches {
			fmt.Fprintf(&b, "\n  %s  %s  %s", match.ID, match.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"), match.Title)
		}
		return b.String()
	}
	return fmt.Sprintf("failed to resolve kiro session: %v", err)
}

func findKiroImportSessionIDConflict(instances []*session.Instance, importedSessionID string) *session.Instance {
	importedSessionID = strings.TrimSpace(importedSessionID)
	if importedSessionID == "" {
		return nil
	}
	for _, inst := range instances {
		if strings.TrimSpace(inst.KiroSessionID) == importedSessionID {
			return inst
		}
	}
	return nil
}
