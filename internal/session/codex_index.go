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
	"sync"
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

type codexThreadKind uint8

const (
	codexThreadUnknown codexThreadKind = iota
	codexThreadTopLevel
	codexThreadSubagent
)

var (
	ErrCodexSessionNotFound  = errors.New("codex session not found")
	ErrCodexSessionAmbiguous = errors.New("codex session name is ambiguous")
	ErrCodexRolloutMissing   = errors.New("codex rollout file missing")

	codexIndexUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	// session_meta is immutable once Codex creates a rollout. Cache only
	// successfully classified records; an unknown result may simply mean the
	// first line has not flushed yet and must remain retryable.
	codexRolloutThreadMetadataCache sync.Map // map[string]codexRolloutThreadMetadata
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
	explicitJSONNames := make(map[string]string, len(latest))
	for id, entry := range latest {
		if name := strings.TrimSpace(entry.ThreadName); name != "" {
			explicitJSONNames[id] = name
		}
	}
	stateEntries, stateExplicitNames, err := listCodexStateThreads(codexHome)
	if err != nil {
		return nil, err
	}
	for _, entry := range stateEntries {
		mergeCodexIndexEntry(latest, entry)
	}
	// Modern Codex separates the auto-generated first-prompt title from the
	// explicit thread name. A newer state-row timestamp must not let the prompt
	// replace an older explicit /rename from session_index.jsonl.
	for id, name := range explicitJSONNames {
		if stateExplicitNames[id] {
			continue
		}
		entry := latest[id]
		entry.ThreadName = name
		latest[id] = entry
	}
	// Codex's SQLite/index files also contain internal workers and approval
	// guardians. They are not user-facing resumable threads and must never be
	// offered by Agent Deck's import picker.
	for id := range latest {
		if IsCodexSubagentSession(codexHome, id) {
			delete(latest, id)
		}
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

func listCodexStateThreads(codexHome string) ([]CodexIndexEntry, map[string]bool, error) {
	path := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}

	db, err := sql.Open("sqlite", codexSQLiteReadOnlyDSN(path))
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	hasName, err := codexStateHasThreadNameColumn(db)
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	nameColumn := "NULL"
	if hasName {
		nameColumn = "name"
	}
	rows, err := db.Query(fmt.Sprintf(`
		SELECT id, title, %s, preview, cwd, updated_at, updated_at_ms, recency_at, recency_at_ms
		FROM threads
		WHERE COALESCE(archived, 0) = 0
	`, nameColumn))
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	defer rows.Close()

	var entries []CodexIndexEntry
	explicitNames := make(map[string]bool)
	for rows.Next() {
		var id, title, name, preview, cwd sql.NullString
		var updatedAt, updatedAtMS, recencyAt, recencyAtMS sql.NullInt64
		if err := rows.Scan(&id, &title, &name, &preview, &cwd, &updatedAt, &updatedAtMS, &recencyAt, &recencyAtMS); err != nil {
			return nil, nil, err
		}
		sessionID := strings.ToLower(strings.TrimSpace(id.String))
		if !isCodexSessionUUID(sessionID) {
			continue
		}
		explicitName := strings.TrimSpace(name.String)
		if explicitName != "" || !hasName {
			explicitNames[sessionID] = true
		}
		entries = append(entries, CodexIndexEntry{
			ID:         sessionID,
			ThreadName: firstNonEmpty(explicitName, title.String, preview.String),
			Path:       cwd.String,
			UpdatedAt:  codexStateThreadTime(recencyAtMS, updatedAtMS, recencyAt, updatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return entries, explicitNames, nil
}

func codexStateHasThreadNameColumn(db *sql.DB) (bool, error) {
	rows, err := db.Query(`SELECT name FROM threads LIMIT 0`)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such column") {
			return false, nil
		}
		return false, err
	}
	return true, rows.Close()
}

func codexStateUsesExplicitThreadName(codexHome string) (bool, error) {
	path := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}

	db, err := sql.Open("sqlite", codexSQLiteReadOnlyDSN(path))
	if err != nil {
		return false, err
	}
	defer db.Close()

	hasName, err := codexStateHasThreadNameColumn(db)
	if err != nil && isMissingCodexStateSchema(err) {
		return false, nil
	}
	return hasName, err
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

// IsCodexSubagentSession reports whether sessionID belongs to an internal
// Codex subagent (including approval-review guardians) rather than a
// user-facing top-level thread. Agent Deck must never persist or resume these
// IDs as the owning interactive session: Codex Desktop intentionally omits
// subagent threads from its normal thread list.
func IsCodexSubagentSession(codexHome, sessionID string) bool {
	path := codexRolloutPathInHome(sessionID, codexHome)
	return path != "" && codexRolloutThreadKind(path) == codexThreadSubagent
}

// IsCodexTopLevelSession reports whether rollout metadata positively identifies
// sessionID as a user-facing thread. Unknown/missing metadata is deliberately
// false so a new foreign hook candidate cannot win a binding race before its
// immutable session_meta record is available.
func IsCodexTopLevelSession(codexHome, sessionID string) bool {
	path := codexRolloutPathInHome(sessionID, codexHome)
	return path != "" && codexRolloutThreadKind(path) == codexThreadTopLevel
}

// CodexSessionRolloutExists reports whether the session has flushed a rollout
// into codexHome. Hook compatibility paths use this to distinguish legacy
// opaque IDs from a present-but-unclassifiable modern rollout.
func CodexSessionRolloutExists(codexHome, sessionID string) bool {
	return codexRolloutPathInHome(sessionID, codexHome) != ""
}

// CodexTopLevelSessionID resolves sessionID to the user-facing root thread
// recorded in its rollout metadata. The boolean is true only when sessionID
// itself is an internal subagent. Current Codex session_meta records carry the
// root ID in payload.session_id even for nested workers and guardians.
func CodexTopLevelSessionID(codexHome, sessionID string) (string, bool) {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	path := codexRolloutPathInHome(sessionID, codexHome)
	if path == "" {
		return sessionID, false
	}
	meta := readCodexRolloutThreadMetadata(path)
	if meta.kind != codexThreadSubagent {
		return sessionID, false
	}
	rootID := strings.ToLower(strings.TrimSpace(meta.rootID))
	if !isCodexSessionUUID(rootID) {
		return "", true
	}
	return rootID, true
}

type codexRolloutThreadMetadata struct {
	kind      codexThreadKind
	rootID    string
	createdAt time.Time
}

// codexRolloutThreadKind reads only the session_meta record near the start of
// a rollout. Current Codex represents top-level sources as strings such as
// "cli"/"exec", while internal workers and approval reviewers use an object
// with a "subagent" key. Unknown/missing metadata stays permissive for older
// Codex versions rather than making otherwise valid sessions unresumable.
func codexRolloutThreadKind(path string) codexThreadKind {
	return readCodexRolloutThreadMetadata(path).kind
}

func readCodexRolloutThreadMetadata(path string) codexRolloutThreadMetadata {
	if cached, ok := codexRolloutThreadMetadataCache.Load(path); ok {
		return cached.(codexRolloutThreadMetadata)
	}

	f, err := os.Open(path)
	if err != nil {
		return codexRolloutThreadMetadata{kind: codexThreadUnknown}
	}
	defer f.Close()

	type rolloutMetaLine struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			SessionID string          `json:"session_id"`
			Source    json.RawMessage `json:"source"`
		} `json:"payload"`
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for lineNo := 0; lineNo < 32 && scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec rolloutMetaLine
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Type != "session_meta" {
			continue
		}
		meta := codexRolloutThreadMetadata{
			kind:      codexSourceThreadKind(rec.Payload.Source),
			rootID:    rec.Payload.SessionID,
			createdAt: parseCodexRolloutTimestamp(rec.Timestamp),
		}
		if meta.kind != codexThreadUnknown {
			codexRolloutThreadMetadataCache.Store(path, meta)
		}
		return meta
	}
	return codexRolloutThreadMetadata{kind: codexThreadUnknown}
}

func parseCodexRolloutTimestamp(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func codexRolloutCreatedAt(codexHome, sessionID string) time.Time {
	path := codexRolloutPathInHome(sessionID, codexHome)
	if path == "" {
		return time.Time{}
	}
	return readCodexRolloutThreadMetadata(path).createdAt
}

// CodexTopLevelSessionPredates reports whether candidate and current are known
// top-level rollouts and candidate's immutable session_meta timestamp is older.
// Zero/missing timestamps return false for backward compatibility with older
// Codex rollouts; callers then fall back to their existing ownership guards.
func CodexTopLevelSessionPredates(codexHome, candidate, current string) bool {
	if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(current)) ||
		!IsCodexTopLevelSession(codexHome, candidate) || !IsCodexTopLevelSession(codexHome, current) {
		return false
	}
	candidateAt := codexRolloutCreatedAt(codexHome, candidate)
	currentAt := codexRolloutCreatedAt(codexHome, current)
	return !candidateAt.IsZero() && !currentAt.IsZero() && candidateAt.Before(currentAt)
}

func codexSourceThreadKind(raw json.RawMessage) codexThreadKind {
	if len(raw) == 0 || string(raw) == "null" {
		return codexThreadUnknown
	}

	var sourceName string
	if err := json.Unmarshal(raw, &sourceName); err == nil {
		if strings.EqualFold(strings.TrimSpace(sourceName), "subagent") {
			return codexThreadSubagent
		}
		if strings.TrimSpace(sourceName) != "" {
			return codexThreadTopLevel
		}
	}

	var sourceObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sourceObject); err != nil {
		return codexThreadUnknown
	}
	for key := range sourceObject {
		if strings.EqualFold(strings.TrimSpace(key), "subagent") {
			return codexThreadSubagent
		}
	}
	if len(sourceObject) > 0 {
		return codexThreadTopLevel
	}
	return codexThreadUnknown
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

// CodexSessionNameIn returns the explicit name Codex currently stores for
// sessionID. Modern Codex keeps the auto-generated first-prompt title separate
// from the optional user name; the prompt must never replace an Agent Deck
// rename. Legacy schemas used title for the explicit name.
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
		return codexJSONSessionName(codexHome, sessionID)
	} else if err != nil {
		return "", err
	}

	db, err := sql.Open("sqlite", codexSQLiteReadOnlyDSN(path))
	if err != nil {
		return "", err
	}
	defer db.Close()

	hasName, err := codexStateHasThreadNameColumn(db)
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return codexJSONSessionName(codexHome, sessionID)
		}
		return "", err
	}
	column := "title"
	if hasName {
		column = "name"
	}
	var name sql.NullString
	err = db.QueryRow(`SELECT `+column+` FROM threads WHERE id = ?`, sessionID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return codexJSONSessionName(codexHome, sessionID)
	}
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return codexJSONSessionName(codexHome, sessionID)
		}
		return "", err
	}
	if name := strings.TrimSpace(name.String); name != "" {
		return name, nil
	}
	if hasName {
		return codexJSONSessionName(codexHome, sessionID)
	}
	return "", nil
}

func codexJSONSessionName(codexHome, sessionID string) (string, error) {
	latest := make(map[string]CodexIndexEntry)
	if err := readCodexJSONIndex(codexHome, latest); err != nil {
		return "", err
	}
	return strings.TrimSpace(latest[sessionID].ThreadName), nil
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

	hasName, err := codexStateHasThreadNameColumn(db)
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return nil
		}
		return err
	}
	column := "title"
	if hasName {
		column = "name"
	}
	_, err = db.Exec(`UPDATE threads SET `+column+` = ? WHERE id = ?`, title, sessionID)
	if err != nil {
		if isMissingCodexStateSchema(err) {
			return nil
		}
		return err
	}
	return nil
}
