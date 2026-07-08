package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestBuildAgentContextNoHubReturnsEmpty(t *testing.T) {
	cfg := &session.UserConfig{}
	text := buildAgentHubContext(cfg)
	if text != "" {
		t.Fatalf("context = %q, want empty when hub is not configured", text)
	}
}

func TestBuildAgentContextPlainOmitsSecrets(t *testing.T) {
	cfg := &session.UserConfig{Hub: session.HubSettings{
		URL:              "wss://hub.example.test",
		NodeID:           "node_local",
		NodeName:         "laptop",
		TokenFile:        "/tmp/secret-token",
		PinnedCertSHA256: "deadbeef",
	}}
	text := buildAgentHubContext(cfg)
	if !strings.Contains(text, "Agent Deck hub is configured") ||
		!strings.Contains(text, "agent-deck hub nodes") ||
		!strings.Contains(text, "agent-deck hub sessions [node-name-or-id]") ||
		!strings.Contains(text, "agent-deck hub sessions create <node-name-or-id>") ||
		!strings.Contains(text, "agent-deck hub shell <node-name-or-id>") {
		t.Fatalf("context missing expected hub guidance:\n%s", text)
	}
	for _, action := range []string{
		"attach",
		"send",
		"close",
		"restart",
		"restart-fresh",
		"fork",
		"rename",
		"move",
		"delete",
		"archive",
		"unarchive",
		"remove",
		"toggle-yolo",
		"preview",
	} {
		if !strings.Contains(text, action) {
			t.Fatalf("context missing hub sessions action %q:\n%s", action, text)
		}
	}
	for _, forbidden := range []string{"secret-token", "invite_", "deadbeef", "token file", "PinnedCertSHA256"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("context exposed %q:\n%s", forbidden, text)
		}
	}
}

func TestEncodeAgentContextHookJSON(t *testing.T) {
	for _, format := range []string{"hook-json", "codex-json"} {
		t.Run(format, func(t *testing.T) {
			out, err := encodeAgentContext(format, "UserPromptSubmit", "hub context")
			if err != nil {
				t.Fatalf("encodeAgentContext: %v", err)
			}
			var got struct {
				HookSpecificOutput struct {
					HookEventName     string `json:"hookEventName"`
					AdditionalContext string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			if got.HookSpecificOutput.HookEventName != "UserPromptSubmit" ||
				got.HookSpecificOutput.AdditionalContext != "hub context" {
				t.Fatalf("encoded output = %+v", got)
			}
		})
	}
}

func TestEncodeAgentContextAdditionalContextJSON(t *testing.T) {
	out, err := encodeAgentContext("additional-context-json", "sessionStart", "hub context")
	if err != nil {
		t.Fatalf("encodeAgentContext: %v", err)
	}
	var got struct {
		AdditionalContext string `json:"additionalContext"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got.AdditionalContext != "hub context" {
		t.Fatalf("additionalContext = %q, want hub context", got.AdditionalContext)
	}
}

func TestEncodeAgentContextContextJSON(t *testing.T) {
	out, err := encodeAgentContext("context-json", "PreToolUse", "hub context")
	if err != nil {
		t.Fatalf("encodeAgentContext: %v", err)
	}
	var got struct {
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got.Context != "hub context" {
		t.Fatalf("context = %q, want hub context", got.Context)
	}
}
