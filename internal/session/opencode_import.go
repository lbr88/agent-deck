package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// OpenCodeImportOptions controls how an existing OpenCode session is converted
// into an Agent Deck session row.
type OpenCodeImportOptions struct {
	Title               string
	GroupPath           string
	ProjectPath         string
	FallbackProjectPath string
	CommandDir          string
}

// ImportOpenCodeSession imports an existing saved OpenCode session using
// `opencode session list --format json` metadata only.
func ImportOpenCodeSession(ctx context.Context, target string, opts OpenCodeImportOptions) (*Instance, error) {
	sessions, err := listOpenCodeSessions(ctx, opts.CommandDir)
	if err != nil {
		return nil, err
	}
	match, err := resolveOpenCodeImportTarget(sessions, target)
	if err != nil {
		return nil, err
	}
	return newOpenCodeImportedInstance(match, opts)
}

func listOpenCodeSessions(ctx context.Context, commandDir string) ([]openCodeSessionMetadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "opencode", "session", "list", "--format", "json")
	if strings.TrimSpace(commandDir) != "" {
		cmd.Dir = commandDir
	}
	cmd.WaitDelay = 500 * time.Millisecond

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("opencode session list timed out")
		}
		return nil, fmt.Errorf("opencode session list --format json: %w", err)
	}

	var sessions []openCodeSessionMetadata
	if err := json.Unmarshal(output, &sessions); err != nil {
		return nil, fmt.Errorf("parse opencode session list JSON: %w", err)
	}
	return sessions, nil
}

func resolveOpenCodeImportTarget(sessions []openCodeSessionMetadata, target string) (openCodeSessionMetadata, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return openCodeSessionMetadata{}, fmt.Errorf("opencode session id or title is required")
	}

	for _, sess := range sessions {
		if sess.ID == target {
			return sess, nil
		}
	}

	var titleMatches []openCodeSessionMetadata
	for _, sess := range sessions {
		if sess.Title != "" && sess.Title == target {
			titleMatches = append(titleMatches, sess)
		}
	}
	switch len(titleMatches) {
	case 0:
		return openCodeSessionMetadata{}, fmt.Errorf("opencode session %q not found", target)
	case 1:
		return titleMatches[0], nil
	default:
		ids := make([]string, 0, len(titleMatches))
		for _, sess := range titleMatches {
			ids = append(ids, sess.ID)
		}
		sort.Strings(ids)
		return openCodeSessionMetadata{}, fmt.Errorf("ambiguous opencode session title %q; retry with one of these IDs: %s", target, strings.Join(ids, ", "))
	}
}

func newOpenCodeImportedInstance(meta openCodeSessionMetadata, opts OpenCodeImportOptions) (*Instance, error) {
	sessionID, err := normalizeToolSessionID(FieldOpenCodeSessionID, meta.ID)
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, fmt.Errorf("opencode session metadata missing id")
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = strings.TrimSpace(meta.Title)
	}
	if title == "" {
		title = shortOpenCodeSessionID(sessionID)
	}

	projectPath := strings.TrimSpace(opts.ProjectPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(meta.Directory)
	}
	if projectPath == "" {
		projectPath = strings.TrimSpace(meta.Path)
	}
	if projectPath == "" {
		projectPath = strings.TrimSpace(opts.FallbackProjectPath)
	}
	if projectPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve fallback project path: %w", err)
		}
		projectPath = cwd
	}

	groupPath := strings.TrimSpace(opts.GroupPath)
	if groupPath == "" {
		groupPath = DefaultGroupPath
	}

	inst := NewInstanceWithGroupAndTool(title, projectPath, groupPath, "opencode")
	inst.Command = "opencode"
	inst.Status = StatusStopped
	inst.OpenCodeSessionID = sessionID
	inst.OpenCodeDetectedAt = time.Now()
	return inst, nil
}

func shortOpenCodeSessionID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
