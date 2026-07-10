package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetCodexThreadNameNativeUsesSlashRenameRPC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TELEGRAM_BOT_TOKEN", "must-not-leak")
	t.Setenv("CLAUDE_CONFIG_DIR", "/conductor/config")
	capture := filepath.Join(t.TempDir(), "requests.jsonl")
	oldCommand := codexAppServerCommandContext
	codexAppServerCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCodexAppServerRenameHelperProcess")
		cmd.Env = append(os.Environ(),
			"GO_WANT_CODEX_APP_SERVER_RENAME_HELPER=1",
			"CODEX_RENAME_CAPTURE="+capture,
			"CODEX_RENAME_EXPECTED_HOME="+home)
		return cmd
	}
	t.Cleanup(func() { codexAppServerCommandContext = oldCommand })

	id := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	if err := setCodexThreadNameNative(`CODEX_HOME="`+home+`" codex-nightly`, home, id, "renamed natively"); err != nil {
		t.Fatalf("setCodexThreadNameNative: %v", err)
	}

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read captured requests: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("captured %d requests, want initialize/initialized/name-set:\n%s", len(lines), string(data))
	}
	var rename struct {
		Method string `json:"method"`
		Params struct {
			ThreadID string `json:"threadId"`
			Name     string `json:"name"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &rename); err != nil {
		t.Fatalf("decode rename request: %v", err)
	}
	if rename.Method != "thread/name/set" || rename.Params.ThreadID != id || rename.Params.Name != "renamed natively" {
		t.Fatalf("rename request = %+v", rename)
	}
}

func TestCodexAppServerRenameHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_APP_SERVER_RENAME_HELPER") != "1" {
		return
	}
	if os.Getenv("TELEGRAM_BOT_TOKEN") != "" || os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		os.Exit(4)
	}
	if os.Getenv("CODEX_HOME") != os.Getenv("CODEX_RENAME_EXPECTED_HOME") {
		os.Exit(5)
	}
	capture := os.Getenv("CODEX_RENAME_CAPTURE")
	f, err := os.OpenFile(capture, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(2)
	}
	defer f.Close()

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		_, _ = f.Write(append(line, '\n'))
		var request struct {
			ID int `json:"id"`
		}
		_ = json.Unmarshal(line, &request)
		if request.ID != 0 {
			fmt.Printf(`{"id":%d,"result":{}}`+"\n", request.ID)
		}
		if request.ID == 2 {
			os.Exit(0)
		}
	}
	os.Exit(3)
}
