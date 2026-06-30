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
	CWD       string    `json:"cwd,omitempty"`
	FilePath  string    `json:"file_path"`
	UpdatedAt time.Time `json:"updated_at"`
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
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
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
		candidate := ClaudeImportCandidate{
			SessionID: sessionID,
			Name:      ClaudeSessionNameIn(configDir, sessionID),
			CWD:       strings.TrimSpace(meta.CWD),
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

	scanner := bufio.NewScanner(io.LimitReader(f, 32*1024))
	buf := make([]byte, 0, 32*1024)
	scanner.Buffer(buf, 32*1024)

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
		if meta.SessionID != "" && meta.CWD != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil && meta.SessionID == "" && meta.CWD == "" {
		return meta, err
	}
	return meta, nil
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
