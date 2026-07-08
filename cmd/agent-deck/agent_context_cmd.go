package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

const defaultAgentContextHookEvent = "SessionStart"

type agentContextCodexOutput struct {
	HookSpecificOutput agentContextCodexSpecificOutput `json:"hookSpecificOutput"`
}

type agentContextCodexSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

type agentContextHookPayload struct {
	HookEventName      string `json:"hook_event_name"`
	HookEventNameCamel string `json:"hookEventName"`
	Event              string `json:"event"`
	Type               string `json:"type"`
}

func handleAgentContext(args []string) {
	fs := flag.NewFlagSet("agent-context", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "plain", "Output format: plain or hook-json")
	event := fs.String("event", "", "Hook event name for formats that require one")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck agent-context [--format plain|hook-json] [--event HookEventName]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "Error: unexpected arguments: %v\n", fs.Args())
		os.Exit(1)
	}

	config, err := session.LoadUserConfig()
	if err != nil {
		// Hooks should not break the parent agent because agent-deck config is
		// temporarily unreadable. Keep this command fail-open and quiet.
		return
	}
	contextText := buildAgentHubContext(config)
	if strings.TrimSpace(contextText) == "" {
		return
	}

	hookEvent := strings.TrimSpace(*event)
	if hookEvent == "" {
		hookEvent = readAgentContextHookEvent(defaultAgentContextHookEvent)
	}
	output, err := encodeAgentContext(*format, hookEvent, contextText)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(output) == "" {
		return
	}
	fmt.Println(output)
}

func buildAgentHubContext(config *session.UserConfig) string {
	if config == nil || !config.Hub.Enabled() {
		return ""
	}
	nodeLabel := strings.TrimSpace(config.Hub.NodeName)
	nodeID := strings.TrimSpace(config.Hub.NodeID)
	if nodeLabel == "" {
		nodeLabel = nodeID
	}
	if nodeID != "" && nodeLabel != nodeID {
		nodeLabel = fmt.Sprintf("%s (%s)", nodeLabel, nodeID)
	}

	var b strings.Builder
	b.WriteString("Agent Deck hub is configured for this agent-deck node.\n")
	if nodeLabel != "" {
		b.WriteString("Local hub node: ")
		b.WriteString(nodeLabel)
		b.WriteString(".\n")
	}
	b.WriteString("Remote Agent Deck access is available when the user's request clearly requires work on another connected node.\n")
	b.WriteString("Useful commands:\n")
	b.WriteString("- agent-deck hub nodes\n")
	b.WriteString("- agent-deck hub sessions [node-name-or-id]\n")
	b.WriteString("- agent-deck hub sessions create <node-name-or-id> --cwd <path> --title <title> --tool <tool>\n")
	b.WriteString("- agent-deck hub sessions attach|send|close|restart|rename <node-name-or-id> <session-id-or-title> [...]\n")
	b.WriteString("- agent-deck hub shell <node-name-or-id> --cwd <path> --title <title>\n")
	b.WriteString("Prefer node names from `agent-deck hub nodes`; use node ids only when names are ambiguous. Prefer session ids when titles are ambiguous.\n")
	b.WriteString("Do not expose hub secrets, invite codes, TLS fingerprints, or host-specific configuration unless the user explicitly asks to inspect local configuration.")
	return b.String()
}

func encodeAgentContext(format, event, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	switch strings.TrimSpace(format) {
	case "", "plain":
		return text, nil
	case "hook-json", "codex-json":
		hookEvent := strings.TrimSpace(event)
		if hookEvent == "" {
			hookEvent = defaultAgentContextHookEvent
		}
		out, err := json.Marshal(agentContextCodexOutput{
			HookSpecificOutput: agentContextCodexSpecificOutput{
				HookEventName:     hookEvent,
				AdditionalContext: text,
			},
		})
		if err != nil {
			return "", err
		}
		return string(out), nil
	case "additional-context-json":
		out, err := json.Marshal(struct {
			AdditionalContext string `json:"additionalContext"`
		}{AdditionalContext: text})
		if err != nil {
			return "", err
		}
		return string(out), nil
	case "context-json":
		out, err := json.Marshal(struct {
			Context string `json:"context"`
		}{Context: text})
		if err != nil {
			return "", err
		}
		return string(out), nil
	default:
		return "", fmt.Errorf("unsupported agent-context format %q", format)
	}
}

func readAgentContextHookEvent(fallback string) string {
	if !stdinLooksReadable() {
		return fallback
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxHookPayloadSize))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return fallback
	}
	var payload agentContextHookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fallback
	}
	for _, candidate := range []string{
		payload.HookEventName,
		payload.HookEventNameCamel,
		payload.Event,
		payload.Type,
	} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return fallback
}

func stdinLooksReadable() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}
