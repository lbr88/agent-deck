package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// KiroSavedSession is the metadata Agent Deck exposes for importing an
// existing saved Kiro CLI session without reading transcript content.
type KiroSavedSession struct {
	ID        string
	Title     string
	CWD       string
	AgentName string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// KiroImportOptions controls how a saved Kiro session becomes an Agent Deck row.
type KiroImportOptions struct {
	Title               string
	GroupPath           string
	ProjectPath         string
	FallbackProjectPath string
	Command             string
}

var (
	ErrKiroSessionNotFound  = errors.New("kiro session not found")
	ErrKiroSessionAmbiguous = errors.New("kiro session title is ambiguous")

	kiroSessionUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// KiroSessionAmbiguousError includes matching saved sessions so callers can
// prompt the user to retry by session ID.
type KiroSessionAmbiguousError struct {
	Target  string
	Matches []KiroSavedSession
}

func (e *KiroSessionAmbiguousError) Error() string {
	return fmt.Sprintf("%v: %q matches %d sessions", ErrKiroSessionAmbiguous, e.Target, len(e.Matches))
}

func (e *KiroSessionAmbiguousError) Unwrap() error {
	return ErrKiroSessionAmbiguous
}

type kiroSessionFile struct {
	SessionID    string `json:"session_id"`
	CWD          string `json:"cwd"`
	Title        string `json:"title"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	SessionState struct {
		AgentName string `json:"agent_name"`
	} `json:"session_state"`
}

// KiroSessionsDir returns the default Kiro CLI saved-session metadata directory.
func KiroSessionsDir() string {
	if home := strings.TrimSpace(os.Getenv("KIRO_HOME")); home != "" {
		return filepath.Join(ExpandPath(home), "sessions", "cli")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".kiro", "sessions", "cli")
	}
	return filepath.Join(home, ".kiro", "sessions", "cli")
}

// ListKiroSavedSessions reads Kiro CLI session metadata JSON files and returns
// entries newest first.
func ListKiroSavedSessions(dir string) ([]KiroSavedSession, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = KiroSessionsDir()
	}
	if info, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("kiro sessions path is not a directory: %s", dir)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	entries := make([]KiroSavedSession, 0, len(matches))
	for _, path := range matches {
		entry, err := readKiroSavedSessionFile(path)
		if err != nil {
			return nil, err
		}
		if entry.ID == "" {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
	return entries, nil
}

func readKiroSavedSessionFile(path string) (KiroSavedSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return KiroSavedSession{}, err
	}
	var raw kiroSessionFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return KiroSavedSession{}, fmt.Errorf("parse %s: %w", path, err)
	}
	id := strings.ToLower(strings.TrimSpace(raw.SessionID))
	if !kiroSessionUUIDPattern.MatchString(id) {
		return KiroSavedSession{}, nil
	}
	createdAt, err := parseOptionalKiroTime(raw.CreatedAt)
	if err != nil {
		return KiroSavedSession{}, fmt.Errorf("parse %s created_at: %w", path, err)
	}
	updatedAt, err := parseOptionalKiroTime(raw.UpdatedAt)
	if err != nil {
		return KiroSavedSession{}, fmt.Errorf("parse %s updated_at: %w", path, err)
	}
	return KiroSavedSession{
		ID:        id,
		Title:     strings.TrimSpace(raw.Title),
		CWD:       strings.TrimSpace(raw.CWD),
		AgentName: strings.TrimSpace(raw.SessionState.AgentName),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func parseOptionalKiroTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

// ResolveKiroSavedSession resolves target as UUID first, then exact title, then
// unambiguous case-insensitive title.
func ResolveKiroSavedSession(dir, target string) (KiroSavedSession, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return KiroSavedSession{}, ErrKiroSessionNotFound
	}
	entries, err := ListKiroSavedSessions(dir)
	if err != nil {
		return KiroSavedSession{}, err
	}
	if kiroSessionUUIDPattern.MatchString(target) {
		targetID := strings.ToLower(target)
		for _, entry := range entries {
			if entry.ID == targetID {
				return entry, nil
			}
		}
		return KiroSavedSession{}, ErrKiroSessionNotFound
	}

	var exact []KiroSavedSession
	for _, entry := range entries {
		if entry.Title == target {
			exact = append(exact, entry)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return KiroSavedSession{}, &KiroSessionAmbiguousError{Target: target, Matches: exact}
	}

	var folded []KiroSavedSession
	for _, entry := range entries {
		if entry.Title != "" && strings.EqualFold(entry.Title, target) {
			folded = append(folded, entry)
		}
	}
	switch len(folded) {
	case 0:
		return KiroSavedSession{}, ErrKiroSessionNotFound
	case 1:
		return folded[0], nil
	default:
		return KiroSavedSession{}, &KiroSessionAmbiguousError{Target: target, Matches: folded}
	}
}

// NewKiroImportedInstance builds a stopped Agent Deck session from saved Kiro
// metadata.
func NewKiroImportedInstance(entry KiroSavedSession, opts KiroImportOptions) (*Instance, error) {
	sessionID := strings.ToLower(strings.TrimSpace(entry.ID))
	if !kiroSessionUUIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("kiro session metadata missing valid id")
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = strings.TrimSpace(entry.Title)
	}
	if title == "" {
		title = shortKiroSessionID(sessionID)
	}

	projectPath := strings.TrimSpace(opts.ProjectPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(entry.CWD)
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

	command := strings.TrimSpace(opts.Command)
	if command == "" {
		command = "kiro-cli chat --tui"
	}

	inst := NewInstanceWithGroupAndTool(title, projectPath, groupPath, "kiro")
	inst.Command = command
	inst.Status = StatusStopped
	inst.KiroSessionID = sessionID
	inst.KiroDetectedAt = entry.UpdatedAt
	return inst, nil
}

func shortKiroSessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
