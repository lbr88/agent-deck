package session

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// CodexIndexEntry is the latest known display metadata for a saved Codex session.
type CodexIndexEntry struct {
	ID         string
	ThreadName string
	Path       string
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
	ID          string `json:"id"`
	ThreadName  string `json:"thread_name"`
	CWD         string `json:"cwd"`
	Path        string `json:"path"`
	ProjectPath string `json:"project_path"`
	UpdatedAt   string `json:"updated_at"`
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
// record for each Codex session ID, newest first. Modern Codex stores resume
// picker metadata in state_5.sqlite, so this also merges that thread index when
// it is present.
func ListCodexIndex(codexHome string) ([]CodexIndexEntry, error) {
	latest := make(map[string]CodexIndexEntry)

	if err := readCodexJSONIndex(codexHome, latest); err != nil {
		return nil, err
	}
	stateEntries, err := listCodexStateThreads(codexHome)
	if err != nil {
		return nil, err
	}
	for _, entry := range stateEntries {
		mergeCodexIndexEntry(latest, entry)
	}

	entries := make([]CodexIndexEntry, 0, len(latest))
	for _, entry := range latest {
		entries = append(entries, entry)
	}
	sortCodexIndexEntries(entries)
	return entries, nil
}

func readCodexJSONIndex(codexHome string, latest map[string]CodexIndexEntry) error {
	path := filepath.Join(codexHome, "session_index.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

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
			return fmt.Errorf("parse %s line %d: %w", path, lineNo, err)
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, raw.UpdatedAt)
		if err != nil {
			return fmt.Errorf("parse %s line %d updated_at: %w", path, lineNo, err)
		}
		id := strings.ToLower(strings.TrimSpace(raw.ID))
		if !isCodexSessionUUID(id) {
			continue
		}
		entry := CodexIndexEntry{
			ID:         id,
			ThreadName: raw.ThreadName,
			Path:       firstNonEmpty(raw.CWD, raw.Path, raw.ProjectPath),
			UpdatedAt:  updatedAt,
		}
		mergeCodexIndexEntry(latest, entry)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func listCodexStateThreads(codexHome string) ([]CodexIndexEntry, error) {
	path := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", codexSQLiteReadOnlyDSN(path))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, title, preview, cwd, updated_at, updated_at_ms, recency_at, recency_at_ms
		FROM threads
		WHERE COALESCE(archived, 0) = 0
	`)
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var entries []CodexIndexEntry
	for rows.Next() {
		var id, title, preview, cwd sql.NullString
		var updatedAt, updatedAtMS, recencyAt, recencyAtMS sql.NullInt64
		if err := rows.Scan(&id, &title, &preview, &cwd, &updatedAt, &updatedAtMS, &recencyAt, &recencyAtMS); err != nil {
			return nil, err
		}
		sessionID := strings.ToLower(strings.TrimSpace(id.String))
		if !isCodexSessionUUID(sessionID) {
			continue
		}
		entries = append(entries, CodexIndexEntry{
			ID:         sessionID,
			ThreadName: firstNonEmpty(title.String, preview.String),
			Path:       cwd.String,
			UpdatedAt:  codexStateThreadTime(recencyAtMS, updatedAtMS, recencyAt, updatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func codexSQLiteReadOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return u.String()
}

func codexSQLiteReadWriteDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return u.String()
}

func isMissingCodexStateSchema(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "no such column")
}

func codexStateThreadTime(values ...sql.NullInt64) time.Time {
	for i, value := range values {
		if !value.Valid || value.Int64 <= 0 {
			continue
		}
		if i < 2 {
			return time.UnixMilli(value.Int64).UTC()
		}
		return time.Unix(value.Int64, 0).UTC()
	}
	return time.Time{}
}

func mergeCodexIndexEntry(latest map[string]CodexIndexEntry, entry CodexIndexEntry) {
	entry.ID = strings.ToLower(strings.TrimSpace(entry.ID))
	if !isCodexSessionUUID(entry.ID) {
		return
	}
	if prev, ok := latest[entry.ID]; ok {
		if entry.UpdatedAt.After(prev.UpdatedAt) {
			if strings.TrimSpace(entry.ThreadName) == "" {
				entry.ThreadName = prev.ThreadName
			}
			if strings.TrimSpace(entry.Path) == "" {
				entry.Path = prev.Path
			}
			latest[entry.ID] = entry
			return
		}
		if strings.TrimSpace(prev.ThreadName) == "" && strings.TrimSpace(entry.ThreadName) != "" {
			prev.ThreadName = entry.ThreadName
		}
		if strings.TrimSpace(prev.Path) == "" && strings.TrimSpace(entry.Path) != "" {
			prev.Path = entry.Path
		}
		latest[entry.ID] = prev
		return
	}
	latest[entry.ID] = entry
}

func sortCodexIndexEntries(entries []CodexIndexEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
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

// CodexRolloutCWD returns the project path recorded in the rollout session
// metadata. It scans only until the first session_meta record with a cwd.
func CodexRolloutCWD(codexHome, sessionID string) string {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	path := codexRolloutPathInHome(sessionID, codexHome)
	if path == "" {
		return ""
	}

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	type rolloutMetaLine struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
			CWD       string `json:"cwd"`
		} `json:"payload"`
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec rolloutMetaLine
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "session_meta" {
			continue
		}
		cwd := strings.TrimSpace(rec.Payload.CWD)
		if cwd == "" {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(firstNonEmpty(rec.Payload.ID, rec.Payload.SessionID)))
		if id == "" || id == sessionID {
			return cwd
		}
	}
	return ""
}

func codexRolloutPathInHome(sessionID, codexHome string) string {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	if !isCodexSessionUUID(sessionID) {
		return ""
	}
	pattern := filepath.Join(codexHome, "sessions", "*", "*", "*",
		"rollout-*-"+sessionID+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// AppendCodexSessionIndexName appends a Codex session_index.jsonl record that
// makes Codex's resume picker show title for sessionID.
func AppendCodexSessionIndexName(codexHome, sessionID, title string, now time.Time) error {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	title = strings.TrimSpace(title)
	if sessionID == "" || title == "" {
		return nil
	}
	if !isCodexSessionUUID(sessionID) {
		return fmt.Errorf("invalid codex session id %q", sessionID)
	}
	if codexHome == "" {
		return fmt.Errorf("codex home is empty")
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}

	path := filepath.Join(codexHome, "session_index.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	rec := codexIndexLine{
		ID:         sessionID,
		ThreadName: title,
		UpdatedAt:  now.UTC().Format(time.RFC3339Nano),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// SyncCodexSessionNameIn updates all Codex title indexes Agent Deck knows about
// for sessionID. Newer Codex builds read state_5.sqlite for the resume picker;
// older builds and Agent Deck's fallback importer read session_index.jsonl.
func SyncCodexSessionNameIn(codexHome, sessionID, title string, now time.Time) error {
	if err := AppendCodexSessionIndexName(codexHome, sessionID, title, now); err != nil {
		return err
	}
	return updateCodexStateThreadTitle(codexHome, sessionID, title)
}

// CodexSessionNameIn returns the title Codex currently stores for sessionID.
// The native SQLite thread row is authoritative for current Codex versions;
// session_index.jsonl is intentionally not consulted because Agent Deck's own
// older append records can otherwise mask a newer in-Codex /rename.
func CodexSessionNameIn(codexHome, sessionID string) (string, error) {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	if sessionID == "" {
		return "", nil
	}
	if !isCodexSessionUUID(sessionID) {
		return "", fmt.Errorf("invalid codex session id %q", sessionID)
	}
	if strings.TrimSpace(codexHome) == "" {
		return "", fmt.Errorf("codex home is empty")
	}

	path := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}

	db, err := sql.Open("sqlite", codexSQLiteReadOnlyDSN(path))
	if err != nil {
		return "", err
	}
	defer db.Close()

	var title sql.NullString
	err = db.QueryRow(`SELECT title FROM threads WHERE id = ?`, sessionID).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(title.String), nil
}

func updateCodexStateThreadTitle(codexHome, sessionID, title string) error {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	title = strings.TrimSpace(title)
	if sessionID == "" || title == "" {
		return nil
	}
	if !isCodexSessionUUID(sessionID) {
		return fmt.Errorf("invalid codex session id %q", sessionID)
	}
	if codexHome == "" {
		return fmt.Errorf("codex home is empty")
	}

	path := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	db, err := sql.Open("sqlite", codexSQLiteReadWriteDSN(path))
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`UPDATE threads SET title = ? WHERE id = ?`, title, sessionID)
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return nil
		}
		return err
	}
	return nil
}
