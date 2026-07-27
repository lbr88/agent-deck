# Codex Title Lock Design

## Context

Agent Deck synchronizes native Codex thread titles into the corresponding
Agent Deck session. Codex can populate its thread title from the first prompt,
including very long prompts. The current reconciler treats every native Codex
title as an explicit `/rename` and overwrites the Agent Deck title even when
`Instance.TitleLocked` is true.

Live session inspection reproduced the issue with locked Codex sessions whose
Agent Deck titles had been replaced by provider titles between 200 and 734
characters long. An unlocked handover session had also received a 3,833
character title. Claude's inbound title reconciler already treats
`TitleLocked` as authoritative.

## Goal

Preserve an explicit Agent Deck rename for Codex-compatible sessions while
retaining native Codex-to-Agent-Deck title synchronization for sessions that
remain unlocked.

## Design

`Instance.reconcileTitleFromCodexLocked` will return without reading or
applying native Codex title metadata when `Instance.TitleLocked` is true. This
matches the existing Claude reconciliation contract:

- Explicit Agent Deck renames set `TitleLocked=true` and remain authoritative.
- Unlocked Codex sessions continue to adopt a different native Codex title.
- Agent Deck-to-Codex rename synchronization remains unchanged. The lock only
  controls the inbound provider-to-Agent-Deck direction.
- The global `sync_title=false` setting continues to disable inbound title
  synchronization for all supported providers.

No title-length heuristic will be added. Length does not reliably distinguish
an automatic provider title from an intentional user rename, and the persisted
title lock already records the required precedence.

## Provider Scope

The current codebase has inbound provider-title reconciliation for Claude and
Codex only. Claude already honors `TitleLocked` and filters provider-derived
names using Claude's `nameSource`. Gemini, OpenCode, and Kiro do not currently
copy provider titles into Agent Deck, so they require no behavior change.

## Testing

Regression tests will prove that:

- A locked Codex session ignores a different native Codex title and performs
  no persistence write.
- An unlocked Codex session still adopts and persists a different native
  Codex title.
- Existing Agent Deck-to-Codex explicit rename synchronization remains green.

The focused `internal/session` tests and the full Go test suite will be run
before completion.
