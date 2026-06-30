package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendCodexSessionIndexNameCreatesJSONLRecord(t *testing.T) {
	home := t.TempDir()
	id := "55555555-5555-5555-5555-555555555555"
	now := time.Date(2026, 6, 30, 10, 0, 0, 123, time.UTC)

	if err := AppendCodexSessionIndexName(home, id, "renamed", now); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID         string `json:"id"`
		ThreadName string `json:"thread_name"`
		UpdatedAt  string `json:"updated_at"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.ThreadName != "renamed" || got.UpdatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("bad record: %#v", got)
	}
}
