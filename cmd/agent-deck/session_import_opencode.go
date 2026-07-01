package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// handleSessionImportOpenCode imports an existing saved OpenCode session into Agent Deck.
func handleSessionImportOpenCode(profile string, args []string) {
	fs := flag.NewFlagSet("session import-opencode", flag.ExitOnError)
	title := fs.String("title", "", "Agent Deck session title (defaults to OpenCode title)")
	titleShort := fs.String("t", "", "Agent Deck session title (short)")
	group := fs.String("group", "", "Group path (defaults to My Sessions)")
	groupShort := fs.String("g", "", "Group path (short)")
	pathFlag := fs.String("path", "", "Project path override (defaults to OpenCode metadata path, then current directory)")
	start := fs.Bool("start", false, "Start the imported session after saving it")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	quiet := fs.Bool("quiet", false, "Minimal output")
	quietShort := fs.Bool("q", false, "Minimal output (short)")

	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session import-opencode <session-id-or-title> [options]")
		fmt.Println()
		fmt.Println("Import an existing saved OpenCode session into Agent Deck.")
		fmt.Println()
		fmt.Println("Options:")
		fs.PrintDefaults()
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  agent-deck session import-opencode ses_abc123")
		fmt.Println("  agent-deck session import-opencode \"Existing title\" -t \"Work item\" -g work --path .")
		fmt.Println("  agent-deck session import-opencode ses_abc123 --start")
	}

	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		os.Exit(1)
	}

	quietMode := *quiet || *quietShort
	out := NewCLIOutput(*jsonOutput, quietMode)

	target := fs.Arg(0)
	if strings.TrimSpace(target) == "" {
		out.Error("usage: agent-deck session import-opencode <session-id-or-title>", ErrCodeInvalidOperation)
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		out.Error(fmt.Sprintf("failed to get current directory: %v", err), ErrCodeInvalidOperation)
		os.Exit(1)
	}

	projectPath := ""
	if strings.TrimSpace(*pathFlag) != "" {
		projectPath, err = resolveAddPath(strings.Trim(*pathFlag, "'\""))
		if err != nil {
			out.Error(fmt.Sprintf("failed to resolve path: %v", err), ErrCodeInvalidOperation)
			os.Exit(1)
		}
	}

	sessionTitle := mergeFlags(*title, *titleShort)
	sessionGroup := mergeFlags(*group, *groupShort)

	storage, instances, groupsData, err := loadSessionData(profile)
	if err != nil {
		out.Error(err.Error(), ErrCodeNotFound)
		os.Exit(1)
	}

	groupTree := session.NewGroupTreeWithGroups(instances, groupsData)
	if sessionGroup != "" {
		sessionGroup = resolveGroupPathForAdd(groupTree, sessionGroup)
	}

	imported, err := session.ImportOpenCodeSession(context.Background(), target, session.OpenCodeImportOptions{
		Title:               sessionTitle,
		GroupPath:           sessionGroup,
		ProjectPath:         projectPath,
		FallbackProjectPath: cwd,
		CommandDir:          cwd,
	})
	if err != nil {
		code := ErrCodeInvalidOperation
		if strings.Contains(err.Error(), "not found") {
			code = ErrCodeNotFound
		} else if strings.Contains(err.Error(), "ambiguous") {
			code = ErrCodeAmbiguous
		}
		out.Error(err.Error(), code)
		os.Exit(1)
	}

	if isDupe, existingInst := isDuplicateSession(instances, imported.Title, imported.ProjectPath); isDupe {
		out.Error(
			fmt.Sprintf("session already exists with same title and path: %s (%s)", existingInst.Title, existingInst.ID),
			ErrCodeAlreadyExists,
		)
		os.Exit(1)
	}
	if existingInst := findOpenCodeImportSessionIDConflict(instances, imported.OpenCodeSessionID); existingInst != nil {
		out.Error(
			fmt.Sprintf("OpenCode session %q is already imported by Agent Deck session %q (%s)", imported.OpenCodeSessionID, existingInst.Title, existingInst.ID),
			ErrCodeAlreadyExists,
		)
		os.Exit(1)
	}

	instances = append(instances, imported)
	groupTree = session.NewGroupTreeWithGroups(instances, groupsData)
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
			out.Error(fmt.Sprintf("failed to start imported session: %v", err), ErrCodeInvalidOperation)
			os.Exit(1)
		}
		imported.PostStartSync(3 * time.Second)

		postStartTree := session.NewGroupTreeWithGroups(instances, groupsData)
		if imported.GroupPath != "" {
			postStartTree.CreateGroupPath(imported.GroupPath)
		}
		if err := storage.InsertSessionAndVerify(imported, postStartTree); err != nil {
			out.Error(fmt.Sprintf("failed to save imported session state: %v", err), ErrCodeInvalidOperation)
			os.Exit(1)
		}
	}

	result := map[string]interface{}{
		"success":             true,
		"id":                  imported.ID,
		"title":               imported.Title,
		"status":              imported.Status,
		"tool":                imported.Tool,
		"command":             imported.Command,
		"group":               imported.GroupPath,
		"project_path":        imported.ProjectPath,
		"opencode_session_id": imported.OpenCodeSessionID,
		"started":             *start,
	}
	if tmuxSess := imported.GetTmuxSession(); tmuxSess != nil && tmuxSess.Name != "" {
		result["tmux"] = tmuxSess.Name
	}

	out.Success(fmt.Sprintf("Imported OpenCode session: %s", imported.Title), result)
}

func findOpenCodeImportSessionIDConflict(instances []*session.Instance, importedSessionID string) *session.Instance {
	importedSessionID = strings.TrimSpace(importedSessionID)
	if importedSessionID == "" {
		return nil
	}
	for _, inst := range instances {
		if strings.TrimSpace(inst.OpenCodeSessionID) == importedSessionID {
			return inst
		}
	}
	return nil
}
