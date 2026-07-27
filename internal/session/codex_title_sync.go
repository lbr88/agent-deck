package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/childenv"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

const (
	codexTitleReconcileInterval = 2 * time.Second
	codexNativeRenameTimeout    = 5 * time.Second
)

var codexAppServerCommandContext = exec.CommandContext

// ReconcileTitleFromCodex pulls a native Codex title into Agent Deck when
// inbound title sync is enabled and the session title is not explicitly locked.
func (i *Instance) ReconcileTitleFromCodex() (string, bool, error) {
	if i == nil {
		return "", false, nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.reconcileTitleFromCodexLocked()
}

func (i *Instance) reconcileTitleFromCodexLocked() (string, bool, error) {
	if i.TitleLocked || !IsCodexCompatible(i.Tool) || strings.TrimSpace(i.CodexSessionID) == "" {
		return "", false, nil
	}
	if cfg, err := LoadUserConfig(); err == nil && cfg != nil && !cfg.GetSyncTitle() {
		return "", false, nil
	}

	codexHome := i.getCodexHomeDir()
	if IsCodexSubagentSession(codexHome, i.CodexSessionID) {
		return "", false, nil
	}
	name, err := CodexSessionNameIn(codexHome, i.CodexSessionID)
	if err != nil {
		return "", false, err
	}
	if name == "" || name == i.Title {
		return "", false, nil
	}

	i.Title = name
	i.AutoName = false
	i.SyncTmuxDisplayName()
	if tmuxSess := i.GetTmuxSession(); tmuxSess != nil {
		_ = tmuxSess.SetEnvironment("AGENTDECK_TITLE", name)
		if tmuxSess.Name != "" {
			_ = tmux.WriteBadgeUpdate(tmuxSess.Name, name)
		}
	}
	if db := i.metadataStateDB(); db != nil {
		if err := db.WriteSessionTitle(i.ID, name); err != nil {
			return "", false, err
		}
	}
	sessionLog.Info("codex_title_reconciled",
		slog.String("instance_id", i.ID),
		slog.String("session_id", i.CodexSessionID),
		slog.String("title", name))
	return name, true, nil
}

// refreshCodexMetadataLocked is called from UpdateStatus before its many early
// returns. This is what lets /rename propagate even for detached/stopped rows
// and what lets a late-created Codex thread repair a missing cached binding.
func (i *Instance) refreshCodexMetadataLocked() {
	if !IsCodexCompatible(i.Tool) {
		return
	}
	// This must precede the metadata throttle: peer-process guardian migrations
	// are correctness state, not optional title-refresh work.
	i.adoptPersistedCodexPromotion(true)
	now := time.Now()
	if !i.lastCodexTitleSync.IsZero() && now.Sub(i.lastCodexTitleSync) < codexTitleReconcileInterval {
		return
	}
	i.lastCodexTitleSync = now

	if i.CodexSessionID == "" && i.tmuxSession != nil && i.tmuxSession.Exists() {
		i.updateCodexSession(i.collectOtherCodexSessionIDs(), true)
	}
	if i.CodexSessionID == "" {
		return
	}
	if _, _, err := i.reconcileTitleFromCodexLocked(); err != nil {
		sessionLog.Warn("codex_title_reconcile_failed",
			slog.String("instance_id", i.ID),
			slog.String("session_id", i.CodexSessionID),
			slog.String("error", err.Error()))
	}
}

// EnsureCodexSessionIDForRename resolves a late/missing Codex binding from the
// live tmux process or the normal scoped disk fallback. Explicit Agent Deck
// renames call this after persistence so they can still reach Codex when the
// launch process exited before its asynchronous ID detector completed.
func (i *Instance) EnsureCodexSessionIDForRename() string {
	if i == nil || !IsCodexCompatible(i.Tool) {
		return ""
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.CodexSessionID == "" {
		i.updateCodexSession(i.collectOtherCodexSessionIDs(), true)
	}
	if IsCodexSubagentSession(i.getCodexHomeDir(), i.CodexSessionID) {
		return ""
	}
	return i.CodexSessionID
}

// SyncCodexSessionNameForCommand performs the supported native Codex
// thread/name/set operation (the same operation behind /rename), then appends
// the legacy JSON index record for older pickers. Unsupported/older Codex
// commands fall back to the direct SQLite update used by prior Agent Deck
// releases.
func SyncCodexSessionNameForCommand(command, codexHome, sessionID, title string, now time.Time) error {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	title = strings.TrimSpace(title)
	if sessionID == "" || title == "" {
		return nil
	}
	if !isCodexSessionUUID(sessionID) {
		return fmt.Errorf("invalid codex session id %q", sessionID)
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return fmt.Errorf("codex home is empty")
	}
	if IsCodexSubagentSession(codexHome, sessionID) {
		return fmt.Errorf("refusing to rename internal Codex subagent thread %q", sessionID)
	}

	nativeErr := setCodexThreadNameNative(command, codexHome, sessionID, title)
	if nativeErr != nil {
		if fallbackErr := updateCodexStateThreadTitle(codexHome, sessionID, title); fallbackErr != nil {
			return errors.Join(nativeErr, fallbackErr)
		}
		sessionLog.Warn("codex_native_title_sync_fallback",
			slog.String("session_id", sessionID),
			slog.String("error", nativeErr.Error()))
	}
	return AppendCodexSessionIndexName(codexHome, sessionID, title, now)
}

func setCodexThreadNameNative(command, codexHome, sessionID, title string) error {
	executable, ok := codexExecutableFromCommand(command)
	if !ok {
		return fmt.Errorf("Codex command %q cannot be used for native rename", command)
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexNativeRenameTimeout)
	defer cancel()

	cmd := codexAppServerCommandContext(ctx, executable, "app-server", "--stdio")
	if strings.TrimSpace(codexHome) != "" {
		if info, err := os.Stat(codexHome); err == nil && info.IsDir() {
			cmd.Dir = codexHome
		}
	}
	baseEnv := cmd.Env
	if len(baseEnv) == 0 {
		baseEnv = childenv.ForLaunch("")
	} else {
		baseEnv = childenv.FilterEnv(baseEnv, "")
	}
	cmd.Env = replaceEnv(baseEnv, "CODEX_HOME", codexHome)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	waited := false
	defer func() {
		_ = stdin.Close()
		if !waited && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	abort := func(cause error) error {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		waitErr := cmd.Wait()
		waited = true
		if cause == nil {
			cause = waitErr
		}
		return codexRPCError(cause, stderr.String())
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	if err := writeCodexRPC(stdin, 1, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "agent-deck", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return abort(err)
	}
	if err := readCodexRPCResponse(scanner, 1); err != nil {
		return abort(err)
	}
	if _, err := fmt.Fprintln(stdin, `{"method":"initialized","params":{}}`); err != nil {
		return abort(err)
	}
	if err := writeCodexRPC(stdin, 2, "thread/name/set", map[string]string{
		"threadId": sessionID,
		"name":     title,
	}); err != nil {
		return abort(err)
	}
	if err := readCodexRPCResponse(scanner, 2); err != nil {
		return abort(err)
	}

	_ = stdin.Close()
	err = cmd.Wait()
	waited = true
	if err != nil {
		return codexRPCError(err, stderr.String())
	}
	return nil
}

func writeCodexRPC(stdin interface{ Write([]byte) (int, error) }, id int, method string, params any) error {
	line, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = stdin.Write(line)
	return err
}

func readCodexRPCResponse(scanner *bufio.Scanner, id int) error {
	wantID := fmt.Sprintf("%d", id)
	for scanner.Scan() {
		var msg struct {
			ID    json.RawMessage `json:"id"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if strings.TrimSpace(string(msg.ID)) != wantID {
			continue
		}
		if len(msg.Error) > 0 && string(msg.Error) != "null" {
			return fmt.Errorf("Codex app-server response: %s", string(msg.Error))
		}
		return nil
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("Codex app-server closed before response %d", id)
}

func codexRPCError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	if len(stderr) > 512 {
		stderr = stderr[:512]
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

func codexExecutableFromCommand(command string) (string, bool) {
	if !IsSupportedCodexLaunchCommand(command) {
		return "", false
	}
	rest := strings.TrimSpace(command)
	for rest != "" {
		token, remainder, ok := nextShellWord(rest)
		if !ok {
			return "", false
		}
		if isShellEnvAssignment(token) {
			rest = strings.TrimSpace(remainder)
			continue
		}
		return token, true
	}
	return "", false
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return append(out, prefix+filepath.Clean(strings.TrimSpace(value)))
}
