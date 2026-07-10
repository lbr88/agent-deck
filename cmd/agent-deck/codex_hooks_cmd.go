package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

const codexNotifyMarkerBegin = "# BEGIN AGENTDECK CODEX NOTIFY"
const codexNotifyMarkerEnd = "# END AGENTDECK CODEX NOTIFY"
const codexNotifyLine = `notify = ["agent-deck", "codex-notify"]`
const codexContextMarkerBegin = "# BEGIN AGENTDECK CODEX HUB CONTEXT"
const codexContextMarkerEnd = "# END AGENTDECK CODEX HUB CONTEXT"
const codexContextHookCommand = "agent-deck agent-context --format hook-json"

var codexNotifyTableRe = regexp.MustCompile(`(?m)^\s*\[notify\]\s*$`)
var codexNotifyKeyRe = regexp.MustCompile(`(?m)^\s*notify\s*=`)
var codexNotifyExactRe = regexp.MustCompile(`(?m)^\s*notify\s*=\s*\[\s*["']agent-deck["']\s*,\s*["']codex-notify["']\s*\]\s*$`)
var codexNotifyExactLineRe = regexp.MustCompile(`^\s*notify\s*=\s*\[\s*["']agent-deck["']\s*,\s*["']codex-notify["']\s*\]\s*$`)
var codexLegacyNotifyProgramLineRe = regexp.MustCompile(`(?i)^\s*program\s*=\s*\[\s*["']agent-deck["']\s*,\s*["']codex-notify["']\s*\]\s*$`)

type codexNotifyPayload struct {
	Type         string `json:"type"`
	Event        string `json:"event"`
	Method       string `json:"method"`
	SessionID    string `json:"session_id"`
	ThreadID     string `json:"thread_id"`
	ThreadIDDash string `json:"thread-id"`
	Params       map[string]json.RawMessage
	Payload      map[string]json.RawMessage
}

func mapCodexNotifyToStatus(event string) string {
	e := strings.ToLower(strings.TrimSpace(event))
	if e == "" {
		return ""
	}

	switch e {
	case "thread.started", "thread/started", "thread-started",
		"session.configured", "session/configured", "session-configured":
		return "waiting"
	case "agent-turn-complete", "agent-turn-completed", "turn/completed", "turn-completed", "turn.completed",
		"turn/complete", "turn-complete", "turn.complete", "turn/failed", "turn-failed", "turn.failed",
		"turn/aborted", "turn-aborted", "turn.aborted", "turn/cancelled", "turn-cancelled", "turn.cancelled",
		"turn/canceled", "turn-canceled", "turn.canceled":
		return "waiting"
	case "agent-turn-start", "agent-turn-started", "turn/started", "turn-started", "turn.started":
		return "running"
	default:
		canon := strings.NewReplacer(".", "/", "-", "/", "_", "/").Replace(e)
		if strings.Contains(canon, "thread/started") || strings.Contains(canon, "session/configured") {
			return "waiting"
		}
		if strings.Contains(canon, "turn") && (strings.Contains(canon, "complete") ||
			strings.Contains(canon, "fail") ||
			strings.Contains(canon, "abort") ||
			strings.Contains(canon, "cancel")) {
			return "waiting"
		}
		if strings.Contains(e, "turn") && strings.Contains(e, "complete") {
			return "waiting"
		}
		if strings.Contains(canon, "turn") && strings.Contains(canon, "start") {
			return "running"
		}
		return ""
	}
}

func decodeStringField(raw map[string]json.RawMessage, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok || len(value) == 0 {
			continue
		}
		var str string
		if err := json.Unmarshal(value, &str); err == nil {
			str = strings.TrimSpace(str)
			if str != "" {
				return str
			}
		}
	}
	return ""
}

func parseCodexNotifyPayload(data []byte) (event, sessionID string) {
	var payload codexNotifyPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", ""
	}

	event = strings.TrimSpace(payload.Type)
	if event == "" {
		event = strings.TrimSpace(payload.Event)
	}
	if event == "" {
		event = strings.TrimSpace(payload.Method)
	}
	if event == "" {
		event = decodeStringField(payload.Params, "type", "event", "method")
	}
	if event == "" {
		event = decodeStringField(payload.Payload, "type", "event", "method")
	}

	sessionID = strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(payload.ThreadID)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(payload.ThreadIDDash)
	}
	if sessionID == "" {
		sessionID = decodeStringField(payload.Params, "session_id", "thread_id", "thread-id", "id")
	}
	if sessionID == "" {
		sessionID = decodeStringField(payload.Payload, "session_id", "thread_id", "thread-id", "id")
	}

	return event, sessionID
}

// handleCodexNotify processes Codex notify payloads.
func handleCodexNotify() {
	instanceID := os.Getenv("AGENTDECK_INSTANCE_ID")
	if instanceID == "" {
		return
	}

	eventArg := ""
	var data []byte
	// Codex notify may pass payload in argv and/or stdin.
	if len(os.Args) > 2 {
		for _, arg := range os.Args[2:] {
			arg = strings.TrimSpace(arg)
			if arg == "" {
				continue
			}
			if strings.HasPrefix(arg, "{") && strings.HasSuffix(arg, "}") {
				data = []byte(arg)
				break
			}
			if eventArg == "" {
				eventArg = arg
			}
		}
	}

	if len(data) == 0 {
		readData, err := io.ReadAll(os.Stdin)
		if err != nil || len(readData) == 0 {
			readData = nil
		}
		if len(readData) > 0 {
			data = readData
		}
	}

	event := ""
	sessionID := ""
	if len(data) > 0 {
		event, sessionID = parseCodexNotifyPayload(data)
		if event == "" {
			trimmed := strings.TrimSpace(string(data))
			if !strings.HasPrefix(trimmed, "{") {
				event = trimmed
			}
		}
	}
	if event == "" {
		event = eventArg
	}
	status := mapCodexNotifyToStatus(event)
	if status == "" {
		return
	}

	if sessionID == "" {
		sessionID = strings.TrimSpace(os.Getenv("CODEX_SESSION_ID"))
	}
	if rootID, isSubagent := session.CodexTopLevelSessionID(session.GetCodexHomeDir(), sessionID); isSubagent {
		// Codex invokes the global notify command for internal workers and
		// approval-review guardians as well as the owning interactive thread.
		// They inherit AGENTDECK_INSTANCE_ID, but their thread IDs are not
		// user-facing/resumable in Codex Desktop. Keep the parent's sticky root
		// mapping and discard the child's lifecycle event entirely.
		// A late child from an older root can finish after the interactive
		// thread has rotated. Never replace an established anchor with a
		// child's ancestry; only use it to bootstrap an otherwise empty one.
		if rootID != "" && session.ReadHookSessionAnchor(instanceID) == "" {
			session.WriteHookSessionAnchor(instanceID, rootID)
		}
		return
	}
	if anchorID := session.ReadHookSessionAnchor(instanceID); sessionID != "" && anchorID != "" && sessionID != anchorID &&
		session.CodexSessionRolloutExists(session.GetCodexHomeDir(), sessionID) &&
		!session.IsCodexTopLevelSession(session.GetCodexHomeDir(), sessionID) {
		// Do not let an unclassified foreign candidate win the short window
		// before Codex flushes session_meta. A later hook or the process probe
		// will bind it once ownership is positively known.
		return
	}

	writeHookStatus(instanceID, status, sessionID, event)
}

func handleCodexHooks(args []string) {
	if len(args) == 0 {
		printCodexHooksUsage(os.Stderr)
		os.Exit(1)
	}

	switch args[0] {
	case "help", "--help", "-h":
		printCodexHooksUsage(os.Stdout)
	case "install":
		handleCodexHooksInstall()
	case "uninstall":
		handleCodexHooksUninstall()
	case "status":
		handleCodexHooksStatus()
	default:
		fmt.Fprintf(os.Stderr, "Unknown codex-hooks subcommand: %s\n", args[0])
		printCodexHooksUsage(os.Stderr)
		os.Exit(1)
	}
}

func printCodexHooksUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck codex-hooks <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Manage Codex notify and Agent Deck hub context hook integration.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  install      Install or upgrade agent-deck Codex hooks")
	fmt.Fprintln(w, "  uninstall    Remove agent-deck Codex hooks")
	fmt.Fprintln(w, "  status       Show current hook install status")
}

func handleCodexHooksInstall() {
	configPath := getCodexConfigPath()
	content, _ := readFileOrEmpty(configPath)

	updated, notifyChanged, err := ensureCodexNotifyBlock(content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: existing notify setting found in %s\n", configPath)
		fmt.Fprintln(os.Stderr, "Please merge manually by setting:")
		fmt.Fprintln(os.Stderr, `  notify = ["agent-deck", "codex-notify"]`)
		os.Exit(1)
	}
	updated, contextChanged, err := ensureCodexContextBlock(updated)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: malformed agent-deck Codex context hook block in %s\n", configPath)
		os.Exit(1)
	}
	if !notifyChanged && !contextChanged {
		fmt.Println("Codex hooks are already installed.")
		fmt.Printf("Config: %s\n", configPath)
		return
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating codex config dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing codex config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Codex hooks installed successfully.")
	fmt.Printf("Config: %s\n", configPath)
}

func handleCodexHooksUninstall() {
	configPath := getCodexConfigPath()
	content, err := readFileOrEmpty(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading codex config: %v\n", err)
		os.Exit(1)
	}

	updated := content
	removed := false

	updated, removed, err = removeMarkedBlock(updated, codexNotifyMarkerBegin, codexNotifyMarkerEnd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: malformed agent-deck Codex hook block in config.")
		os.Exit(1)
	}

	var legacyRemoved bool
	updated, legacyRemoved = removeLegacyCodexNotifyTable(updated)
	removed = removed || legacyRemoved

	var exactRemoved bool
	updated, exactRemoved = removeExactCodexNotifyLine(updated)
	removed = removed || exactRemoved

	var contextRemoved bool
	updated, contextRemoved, err = removeMarkedBlock(updated, codexContextMarkerBegin, codexContextMarkerEnd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: malformed agent-deck Codex context hook block in config.")
		os.Exit(1)
	}
	removed = removed || contextRemoved

	if removed {
		if err := os.WriteFile(configPath, []byte(updated), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing codex config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Codex hooks removed successfully.")
		return
	}

	fmt.Println("No agent-deck Codex hooks found to remove.")
}

func handleCodexHooksStatus() {
	configPath := getCodexConfigPath()
	content, _ := readFileOrEmpty(configPath)

	notifyInstalled := codexNotifyInstalled(content)
	contextInstalled := codexContextHookInstalled(content)
	switch {
	case notifyInstalled && contextInstalled:
		fmt.Println("Status: INSTALLED")
	case hasLegacyCodexNotifyTable(content):
		fmt.Println("Status: LEGACY_NOTIFY_TABLE")
		fmt.Println("Run 'agent-deck codex-hooks install' to migrate to current Codex format.")
	case codexNotifyTableRe.MatchString(content):
		fmt.Println("Status: LEGACY_NOTIFY_TABLE")
		fmt.Println("Run 'agent-deck codex-hooks install' to migrate to current Codex format.")
	case codexNotifyKeyRe.MatchString(content) && !notifyInstalled:
		fmt.Println("Status: CUSTOM_NOTIFY")
	case notifyInstalled || contextInstalled:
		fmt.Println("Status: PARTIAL")
		fmt.Println("Run 'agent-deck codex-hooks install' to install missing agent-deck Codex hooks.")
	default:
		fmt.Println("Status: NOT INSTALLED")
		fmt.Println("Run 'agent-deck codex-hooks install' to install.")
	}
	fmt.Printf("Config: %s\n", configPath)
}

func getCodexConfigPath() string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".codex", "config.toml")
	}
	return filepath.Join(home, ".codex", "config.toml")
}

func readFileOrEmpty(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func prependCodexNotifyBlock(block, content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return block
	}
	return strings.TrimRight(block, "\n") + "\n\n" + trimmed + "\n"
}

func codexNotifyBlock() string {
	return codexNotifyMarkerBegin + "\n" +
		codexNotifyLine + "\n" +
		codexNotifyMarkerEnd + "\n"
}

func codexContextHookBlock() string {
	return codexContextMarkerBegin + "\n" +
		`[[hooks.SessionStart]]
matcher = "startup|resume|clear|compact"

[[hooks.SessionStart.hooks]]
type = "command"
command = "agent-deck agent-context --format hook-json"
timeout = 5
statusMessage = "Loading Agent Deck hub context"` + "\n" +
		codexContextMarkerEnd + "\n"
}

func ensureCodexNotifyBlock(content string) (string, bool, error) {
	if strings.Contains(content, codexNotifyMarkerBegin) {
		if codexNotifyInstalled(content) {
			return content, false, nil
		}
		updated, removed, err := removeMarkedBlock(content, codexNotifyMarkerBegin, codexNotifyMarkerEnd)
		if err != nil {
			return content, false, err
		}
		if removed {
			return prependCodexNotifyBlock(codexNotifyBlock(), updated), true, nil
		}
	}

	if updated, removed := removeLegacyCodexNotifyTable(content); removed {
		return prependCodexNotifyBlock(codexNotifyBlock(), updated), true, nil
	}

	if codexNotifyExactRe.MatchString(content) {
		return content, false, nil
	}

	if codexNotifyKeyRe.MatchString(content) || codexNotifyTableRe.MatchString(content) {
		return content, false, fmt.Errorf("custom notify setting")
	}

	return prependCodexNotifyBlock(codexNotifyBlock(), content), true, nil
}

func ensureCodexContextBlock(content string) (string, bool, error) {
	if codexContextHookInstalled(content) {
		return content, false, nil
	}
	updated, _, err := removeMarkedBlock(content, codexContextMarkerBegin, codexContextMarkerEnd)
	if err != nil {
		return content, false, err
	}
	return prependCodexNotifyBlock(codexContextHookBlock(), updated), true, nil
}

func codexNotifyInstalled(content string) bool {
	return codexNotifyExactRe.MatchString(content)
}

func codexContextHookInstalled(content string) bool {
	begin := strings.Index(content, codexContextMarkerBegin)
	if begin == -1 {
		return false
	}
	endRel := strings.Index(content[begin:], codexContextMarkerEnd)
	if endRel == -1 {
		return false
	}
	end := begin + endRel + len(codexContextMarkerEnd)
	block := strings.TrimSpace(content[begin:end])
	return block == strings.TrimSpace(codexContextHookBlock())
}

func removeMarkedBlock(content, beginMarker, endMarker string) (string, bool, error) {
	begin := strings.Index(content, beginMarker)
	if begin == -1 {
		return content, false, nil
	}
	endRel := strings.Index(content[begin:], endMarker)
	if endRel == -1 {
		return content, false, fmt.Errorf("missing end marker %q", endMarker)
	}
	end := begin + endRel + len(endMarker)
	updated := strings.TrimSpace(content[:begin] + content[end:])
	if updated != "" {
		updated += "\n"
	}
	return updated, true, nil
}

func removeLegacyCodexNotifyTable(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	removed := false

	for idx := 0; idx < len(lines); idx++ {
		line := lines[idx]
		if strings.TrimSpace(line) != "[notify]" {
			out = append(out, line)
			continue
		}

		progIdx := idx + 1
		for progIdx < len(lines) && strings.TrimSpace(lines[progIdx]) == "" {
			progIdx++
		}
		if progIdx < len(lines) && codexLegacyNotifyProgramLineRe.MatchString(lines[progIdx]) {
			removed = true
			idx = progIdx
			continue
		}

		out = append(out, line)
	}

	if !removed {
		return content, false
	}

	updated := strings.TrimSpace(strings.Join(out, "\n"))
	if updated != "" {
		updated += "\n"
	}
	return updated, true
}

func hasLegacyCodexNotifyTable(content string) bool {
	_, removed := removeLegacyCodexNotifyTable(content)
	return removed
}

func removeExactCodexNotifyLine(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	removed := false

	for _, line := range lines {
		if codexNotifyExactLineRe.MatchString(line) {
			removed = true
			continue
		}
		out = append(out, line)
	}

	if !removed {
		return content, false
	}

	updated := strings.TrimSpace(strings.Join(out, "\n"))
	if updated != "" {
		updated += "\n"
	}
	return updated, true
}
