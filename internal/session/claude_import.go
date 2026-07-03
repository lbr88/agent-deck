package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ClaudeImportCandidate is the metadata Agent Deck needs to import an
// existing Claude Code session. It intentionally excludes transcript content.
type ClaudeImportCandidate struct {
	SessionID string    `json:"session_id"`
	Name      string    `json:"name,omitempty"`
	Title     string    `json:"title,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
	Path      string    `json:"path,omitempty"`
	FilePath  string    `json:"file_path"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DisplayTitle returns the best human-readable title for importing this Claude
// session. Name is Claude's explicit user-controlled session name; Title may be
// derived from bounded transcript metadata when no name exists.
func (c ClaudeImportCandidate) DisplayTitle() string {
	if title := strings.TrimSpace(c.Title); title != "" {
		return title
	}
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}
	sessionID := strings.TrimSpace(c.SessionID)
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8]
}

// ClaudeImportResolveKind identifies why an import target could not be
// resolved to exactly one Claude session.
type ClaudeImportResolveKind string

const (
	ClaudeImportResolveNotFound  ClaudeImportResolveKind = "not_found"
	ClaudeImportResolveAmbiguous ClaudeImportResolveKind = "ambiguous"
)

// ClaudeImportResolveError carries machine-readable resolution details while
// keeping diagnostics limited to metadata fields and retry UUIDs.
type ClaudeImportResolveError struct {
	Target     string
	Kind       ClaudeImportResolveKind
	Candidates []ClaudeImportCandidate
}

func (e *ClaudeImportResolveError) Error() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ClaudeImportResolveAmbiguous:
		ids := make([]string, 0, len(e.Candidates))
		for _, c := range e.Candidates {
			if c.SessionID != "" {
				ids = append(ids, c.SessionID)
			}
		}
		return fmt.Sprintf("Claude session name %q is ambiguous; retry by UUID: %s", e.Target, strings.Join(ids, ", "))
	default:
		return fmt.Sprintf("Claude session %q not found", e.Target)
	}
}

type claudeImportJSONLRecord struct {
	Type          string          `json:"type"`
	SessionID     string          `json:"sessionId"`
	CWD           string          `json:"cwd"`
	Path          string          `json:"path"`
	Summary       string          `json:"summary"`
	Message       json.RawMessage `json:"message"`
	FallbackTitle string          `json:"-"`
}

// ListClaudeImportCandidates scans configDir/projects for UUID-named Claude
// JSONL transcripts and returns only import-safe metadata.
func ListClaudeImportCandidates(configDir string) ([]ClaudeImportCandidate, error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		configDir = GetClaudeConfigDir()
	}
	projectsDir := filepath.Join(configDir, "projects")
	if _, err := os.Stat(projectsDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	byID := make(map[string]ClaudeImportCandidate)
	err := filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "tool-results", "subagents":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !isUUIDFileName(filepath.Base(path)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		meta, err := parseClaudeImportJSONLMetadata(path)
		if err != nil {
			return nil
		}
		sessionID := strings.TrimSpace(meta.SessionID)
		if sessionID == "" {
			sessionID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		}
		if sessionID == "" {
			return nil
		}
		name := ClaudeSessionNameIn(configDir, sessionID)
		title := strings.TrimSpace(name)
		if title == "" {
			title = strings.TrimSpace(meta.FallbackTitle)
		}
		candidate := ClaudeImportCandidate{
			SessionID: sessionID,
			Name:      name,
			Title:     title,
			CWD:       strings.TrimSpace(meta.CWD),
			Path:      strings.TrimSpace(meta.Path),
			FilePath:  path,
			UpdatedAt: info.ModTime(),
		}
		if prev, ok := byID[sessionID]; ok && !candidate.UpdatedAt.After(prev.UpdatedAt) {
			return nil
		}
		byID[sessionID] = candidate
		return nil
	})
	if err != nil {
		return nil, err
	}

	candidates := make([]ClaudeImportCandidate, 0, len(byID))
	for _, c := range byID {
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
		}
		return candidates[i].SessionID < candidates[j].SessionID
	})
	return candidates, nil
}

func parseClaudeImportJSONLMetadata(filePath string) (claudeImportJSONLRecord, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return claudeImportJSONLRecord{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(io.LimitReader(f, 256*1024))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 256*1024)

	var meta claudeImportJSONLRecord
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record claudeImportJSONLRecord
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if meta.SessionID == "" && record.SessionID != "" {
			meta.SessionID = record.SessionID
		}
		if meta.CWD == "" && record.CWD != "" {
			meta.CWD = record.CWD
		}
		if meta.Path == "" && record.Path != "" {
			meta.Path = record.Path
		}
		if meta.FallbackTitle == "" {
			meta.FallbackTitle = claudeImportFallbackTitle(record)
		}
		if meta.SessionID != "" && (meta.CWD != "" || meta.Path != "") && meta.FallbackTitle != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil && meta.SessionID == "" && meta.CWD == "" && meta.Path == "" {
		return meta, err
	}
	return meta, nil
}

func claudeImportFallbackTitle(record claudeImportJSONLRecord) string {
	if summary := cleanClaudeImportFallbackTitle(record.Summary); summary != "" {
		return summary
	}
	if record.Type != "user" || len(record.Message) == 0 {
		return ""
	}
	var msg claudeMessage
	if err := json.Unmarshal(record.Message, &msg); err != nil {
		return ""
	}
	if msg.Role != "" && msg.Role != "user" {
		return ""
	}
	return cleanClaudeImportFallbackTitle(extractContentText(msg.Content))
}

func cleanClaudeImportFallbackTitle(title string) string {
	title = strings.Join(strings.Fields(strings.TrimSpace(title)), " ")
	if title == "" {
		return ""
	}
	const maxRunes = 80
	runes := []rune(title)
	if len(runes) <= maxRunes {
		return title
	}
	return string(runes[:maxRunes-3]) + "..."
}

// ResolveClaudeImportTarget resolves target by UUID first, then exact Claude
// session name. Ambiguous names return candidate UUIDs for retry.
func ResolveClaudeImportTarget(configDir, target string) (*ClaudeImportCandidate, error) {
	target = strings.TrimSpace(target)
	candidates, err := ListClaudeImportCandidates(configDir)
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		if c.SessionID == target {
			match := c
			return &match, nil
		}
	}
	var nameMatches []ClaudeImportCandidate
	for _, c := range candidates {
		if c.Name == target {
			nameMatches = append(nameMatches, c)
		}
	}
	switch len(nameMatches) {
	case 1:
		match := nameMatches[0]
		return &match, nil
	case 0:
		return nil, &ClaudeImportResolveError{Target: target, Kind: ClaudeImportResolveNotFound}
	default:
		return nil, &ClaudeImportResolveError{
			Target:     target,
			Kind:       ClaudeImportResolveAmbiguous,
			Candidates: nameMatches,
		}
	}
}
