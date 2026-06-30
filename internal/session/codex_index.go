package session

import (
	"bufio"
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

// CodexIndexEntry is the latest known display metadata for a saved Codex session.
type CodexIndexEntry struct {
	ID         string
	ThreadName string
	UpdatedAt  time.Time
}

var (
	ErrCodexSessionNotFound  = errors.New("codex session not found")
	ErrCodexSessionAmbiguous = errors.New("codex session name is ambiguous")
	ErrCodexRolloutMissing   = errors.New("codex rollout file missing")

	codexIndexUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// CodexSessionAmbiguousError includes the candidate sessions for an ambiguous
// name lookup so callers can prompt the user to retry by UUID.
type CodexSessionAmbiguousError struct {
	Target  string
	Matches []CodexIndexEntry
}

func (e *CodexSessionAmbiguousError) Error() string {
	return fmt.Sprintf("%v: %q matches %d sessions", ErrCodexSessionAmbiguous, e.Target, len(e.Matches))
}

func (e *CodexSessionAmbiguousError) Unwrap() error {
	return ErrCodexSessionAmbiguous
}

type codexIndexLine struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

// GetCodexHomeDir returns the effective Codex home using Agent Deck's normal
// CODEX_HOME/profile/default resolution rules.
func GetCodexHomeDir() string {
	return getCodexHomeDir()
}

// GetCodexHomeDirForCommand also honors an inline CODEX_HOME assignment in a
// Codex command override.
func GetCodexHomeDirForCommand(command string) string {
	return getCodexHomeDirForCommand(command)
}

// ListCodexIndex reads CODEX_HOME/session_index.jsonl and returns the latest
// record for each Codex session ID, newest first.
func ListCodexIndex(codexHome string) ([]CodexIndexEntry, error) {
	path := filepath.Join(codexHome, "session_index.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	latest := make(map[string]CodexIndexEntry)
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw codexIndexLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", path, lineNo, err)
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, raw.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse %s line %d updated_at: %w", path, lineNo, err)
		}
		id := strings.ToLower(strings.TrimSpace(raw.ID))
		if !isCodexSessionUUID(id) {
			continue
		}
		entry := CodexIndexEntry{
			ID:         id,
			ThreadName: raw.ThreadName,
			UpdatedAt:  updatedAt,
		}
		if prev, ok := latest[id]; !ok || entry.UpdatedAt.After(prev.UpdatedAt) {
			latest[id] = entry
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	entries := make([]CodexIndexEntry, 0, len(latest))
	for _, entry := range latest {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
	return entries, nil
}

func isCodexSessionUUID(id string) bool {
	return codexIndexUUIDPattern.MatchString(strings.TrimSpace(id))
}

// ResolveCodexIndexTarget resolves an import target as a Codex UUID first, then
// as an exact thread name. Resolved sessions must have a flushed rollout file.
func ResolveCodexIndexTarget(codexHome, target string) (CodexIndexEntry, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return CodexIndexEntry{}, ErrCodexSessionNotFound
	}

	entries, err := ListCodexIndex(codexHome)
	if err != nil {
		return CodexIndexEntry{}, err
	}

	if codexIndexUUIDPattern.MatchString(target) {
		targetID := strings.ToLower(target)
		for _, entry := range entries {
			if strings.EqualFold(entry.ID, targetID) {
				if !CodexRolloutExists(codexHome, entry.ID) {
					return CodexIndexEntry{}, ErrCodexRolloutMissing
				}
				return entry, nil
			}
		}
		if CodexRolloutExists(codexHome, targetID) {
			return CodexIndexEntry{ID: targetID}, nil
		}
		return CodexIndexEntry{}, ErrCodexSessionNotFound
	}

	var matches []CodexIndexEntry
	for _, entry := range entries {
		if entry.ThreadName == target {
			matches = append(matches, entry)
		}
	}
	switch len(matches) {
	case 0:
		return CodexIndexEntry{}, ErrCodexSessionNotFound
	case 1:
		if !CodexRolloutExists(codexHome, matches[0].ID) {
			return CodexIndexEntry{}, ErrCodexRolloutMissing
		}
		return matches[0], nil
	default:
		return CodexIndexEntry{}, &CodexSessionAmbiguousError{Target: target, Matches: matches}
	}
}

// CodexRolloutExists reports whether Codex has a rollout file for sessionID.
func CodexRolloutExists(codexHome, sessionID string) bool {
	return codexRolloutExistsInHome(strings.ToLower(strings.TrimSpace(sessionID)), codexHome)
}
