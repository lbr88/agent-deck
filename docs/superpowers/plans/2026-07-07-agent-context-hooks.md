# Agent Context Hooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

## Goal

Give supported agent CLIs model-visible Agent Deck hub instructions through their normal hook installers. Do not create a separate hub-hook install workflow. The shared hook command must return no output when the local node is not joined to a hub.

## Architecture

- Add one generic `agent-deck agent-context` command.
- Render plain text for hook systems that add stdout to model context.
- Render `hook-json` for hook systems that expect `hookSpecificOutput.additionalContext`; keep `codex-json` as a compatibility alias.
- Wire the command into each supported CLI's existing/default hook installer when that CLI has a verified model-visible context hook.
- Leave unsupported tools unwired until their hook contract supports model-visible context injection.

## Supported Hook Targets

| Tool | Installer | Context hook events |
|------|-----------|---------------------|
| Claude | `agent-deck hooks install` | `SessionStart`, `UserPromptSubmit` |
| Codex | `agent-deck codex-hooks install` | `SessionStart`, `UserPromptSubmit` |
| Gemini | `agent-deck gemini-hooks install` | `SessionStart`, `BeforeAgent` |
| Cursor | `agent-deck cursor-hooks install` | `sessionStart`, `beforeSubmitPrompt` |
| Hermes | `agent-deck hermes-hooks install` | `on_session_start` |
| Kiro | `agent-deck kiro-hooks install` | `agentSpawn`, `userPromptSubmit` on global `agent-deck` custom agent |
| OpenCode | `agent-deck opencode-hooks install` | global plugin using the system prompt transform hook |

Kiro hooks are attached to a custom agent. Agent Deck should launch Kiro with the generated `agent-deck` agent only when hooks are installed and no other Kiro agent is explicitly configured.

## Tasks

- [x] Add `agent-deck agent-context` with `plain`, `hook-json`, and `codex-json` formats.
- [x] Ensure `agent-context` returns no output when hub config is absent or unreadable.
- [x] Keep hub secrets, invite tokens, TLS fingerprints, token paths, and host-specific deployment details out of generated context.
- [x] Update Claude hook install/status/remove logic to include hub context hooks.
- [x] Update Codex hook install/status/remove logic to include hub context hooks.
- [x] Update Gemini hook install/status/remove logic to include hub context hooks.
- [x] Update Cursor hook install/status/remove logic to include hub context hooks.
- [x] Update Hermes hook install/status/remove logic to include hub context hooks.
- [x] Add Kiro hook installer and automatic Agent Deck Kiro agent selection when appropriate.
- [x] Add OpenCode plugin installer.
- [x] Update CLI help and nested-session subcommand handling for new installers.
- [x] Update hub docs and CLI reference without private local system information.

## Verification

- Run focused internal hook tests:

```bash
GOCACHE=/tmp/agent-deck-go-build go test ./internal/session -run 'TestInjectClaudeHooks_Fresh|TestInjectClaudeHooks_UpgradesStatusOnlyInstallWithHubContext|TestInjectGeminiHooks_Fresh|TestRemoveGeminiHooks_PreservesUserHooks|TestInjectCursorHooks_Fresh|TestInjectCursorHooks_PreservesExistingHooks|TestInjectHermesHooks_AllEventsPresent|TestInjectHermesHooks_PreservesExistingConfig|TestInjectKiroHooks|TestRemoveKiroHooks|TestInjectOpenCodeHooks|TestRemoveOpenCodeHooks|TestBuildKiroCommand'
```

- Run focused CLI hook tests:

```bash
GOCACHE=/tmp/agent-deck-go-build go test ./cmd/agent-deck -run 'Test.*Hooks|TestEncodeAgentContext|TestNestedSessionAllowsCLICommands'
```

- Run broader package tests:

```bash
GOCACHE=/tmp/agent-deck-go-build go test ./cmd/agent-deck ./internal/session
```

- Build the binary:

```bash
GOCACHE=/tmp/agent-deck-go-build GOTMPDIR=/tmp go build -o /tmp/agent-deck-build-check ./cmd/agent-deck
```

- Check docs for accidental private deployment details:

```bash
rg -n "private-host|private-domain|private-ip|local-username" docs/AGENT-DECK-HUB.md skills/agent-deck/references/cli-reference.md docs/superpowers/plans/2026-07-07-agent-context-hooks.md
```
