package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type codexIndexLine struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

// AppendCodexSessionIndexName appends a Codex session_index.jsonl record that
// makes Codex's resume picker show title for sessionID.
func AppendCodexSessionIndexName(codexHome, sessionID, title string, now time.Time) error {
	sessionID = strings.TrimSpace(sessionID)
	title = strings.TrimSpace(title)
	if sessionID == "" || title == "" {
		return nil
	}
	if !codexSessionIDPattern.MatchString(sessionID) {
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
		ID:         strings.ToLower(sessionID),
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
