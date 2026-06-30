package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/atomicfile"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// ErrClaudeSessionMetadataNotFound means no ~/.claude/sessions/*.json metadata
// entry matched the requested Claude session ID.
var ErrClaudeSessionMetadataNotFound = errors.New("claude session metadata not found")

// ErrClaudeSessionMetadataUnreadable means the freshest candidate Claude
// metadata could not be read or parsed, so agent-deck cannot safely choose a
// stale older match.
var ErrClaudeSessionMetadataUnreadable = errors.New("claude session metadata unreadable")

// claudeSessionMeta is the subset of ~/.claude/sessions/<PID>.json that
// agent-deck reads for title sync (issue #572).
type claudeSessionMeta struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	UpdatedAt *int64 `json:"updatedAt"` // unix ms; nil when absent
}

type claudeSessionMetaCandidate struct {
	path string
	data []byte
	meta claudeSessionMeta
	time int64
}

type claudeSessionMetaProblem struct {
	path string
	time int64
	err  error
}

func freshestClaudeSessionMetaIn(claudeDir, sessionID string) (*claudeSessionMetaCandidate, error) {
	claudeDir = strings.TrimSpace(claudeDir)
	sessionID = strings.TrimSpace(sessionID)
	if claudeDir == "" || sessionID == "" {
		return nil, ErrClaudeSessionMetadataNotFound
	}
	entries, err := os.ReadDir(filepath.Join(claudeDir, "sessions"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrClaudeSessionMetadataNotFound
		}
		return nil, fmt.Errorf("read Claude session metadata: %w", err)
	}
	var best *claudeSessionMetaCandidate
	var newestProblem *claudeSessionMetaProblem
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(claudeDir, "sessions", entry.Name())
		var ts int64
		if info, err := entry.Info(); err == nil {
			ts = info.ModTime().UnixMilli()
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if newestProblem == nil || ts >= newestProblem.time {
				newestProblem = &claudeSessionMetaProblem{
					path: path,
					time: ts,
					err:  fmt.Errorf("read %s: %w", path, err),
				}
			}
			continue
		}
		var meta claudeSessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			if newestProblem == nil || ts >= newestProblem.time {
				newestProblem = &claudeSessionMetaProblem{
					path: path,
					time: ts,
					err:  fmt.Errorf("decode %s: %w", path, err),
				}
			}
			continue
		}
		if meta.SessionID != sessionID {
			continue
		}
		if meta.UpdatedAt != nil {
			ts = *meta.UpdatedAt
		}
		if best == nil || ts > best.time {
			best = &claudeSessionMetaCandidate{
				path: path,
				data: data,
				meta: meta,
				time: ts,
			}
		}
	}
	if newestProblem != nil && (best == nil || newestProblem.time >= best.time) {
		return nil, fmt.Errorf("%w: %v", ErrClaudeSessionMetadataUnreadable, newestProblem.err)
	}
	if best == nil {
		return nil, ErrClaudeSessionMetadataNotFound
	}
	return best, nil
}

// ClaudeSessionNameIn scans claudeDir/sessions/*.json and returns the trimmed
// `name` of the entry whose sessionId matches. Empty string when there's no
// match, no name, or the sessions dir is unreadable.
//
// The files are per-PID, so a resumed session can match several entries — the
// live process plus stale files left by earlier runs. The freshest entry (by
// updatedAt, falling back to file mtime) is authoritative, even when its name
// is empty: returning a stale file's old name would re-sync a title the user
// has since changed or cleared.
//
// Issue #572: Claude Code writes per-process metadata here when the user starts
// with `claude --name X` or runs `/rename X` mid-session. claudeDir is an
// explicit parameter so tests can point it at a temp dir.
func ClaudeSessionNameIn(claudeDir, sessionID string) string {
	best, err := freshestClaudeSessionMetaIn(claudeDir, sessionID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(best.meta.Name)
}

// ClaudeSessionName resolves the user's ~/.claude and returns the Claude
// session name for sessionID. Empty string on any error or no match.
func ClaudeSessionName(sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return ClaudeSessionNameIn(filepath.Join(home, ".claude"), sessionID)
}

// SyncClaudeSessionNameIn updates the freshest Claude session metadata entry
// matching sessionID to name. It preserves unknown JSON fields and writes the
// selected metadata file atomically. Transcript JSONL files are never touched.
func SyncClaudeSessionNameIn(claudeDir, sessionID, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	best, err := freshestClaudeSessionMetaIn(claudeDir, sessionID)
	if err != nil {
		return err
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(best.data, &raw); err != nil {
		return fmt.Errorf("decode Claude session metadata %s: %w", best.path, err)
	}
	nameJSON, err := json.Marshal(name)
	if err != nil {
		return fmt.Errorf("encode Claude session name: %w", err)
	}
	raw["name"] = nameJSON
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Claude session metadata %s: %w", best.path, err)
	}
	out = append(out, '\n')
	perm := os.FileMode(0o600)
	if info, err := os.Stat(best.path); err == nil {
		perm = info.Mode().Perm()
	}
	if err := atomicfile.WriteFile(best.path, out, perm); err != nil {
		return fmt.Errorf("write Claude session metadata %s: %w", best.path, err)
	}
	return nil
}

// SyncClaudeSessionNameForInstance pushes an explicit Agent Deck title into
// the matching Claude session metadata when this is a Claude-compatible session
// with a known Claude session ID. TitleLocked intentionally does not block this
// direction: it only blocks Claude -> Agent Deck title reconciliation.
func SyncClaudeSessionNameForInstance(inst *Instance) error {
	if inst == nil || !IsClaudeCompatible(inst.Tool) || strings.TrimSpace(inst.ClaudeSessionID) == "" {
		return nil
	}
	if strings.TrimSpace(inst.Title) == "" {
		return nil
	}
	return SyncClaudeSessionNameIn(GetClaudeConfigDirForInstance(inst), inst.ClaudeSessionID, inst.Title)
}

// ReconcileTitleFromClaude refreshes i.Title from the agent's current Claude
// session name. It is the shared core behind both the hook-event sync (#572)
// and the on-attach reconcile (#1114 follow-up): Claude's /rename fires no
// agent-deck hook, so an idle session's title and iTerm2 badge stay stale until
// the next turn boundary — reconciling on attach makes detach/reattach a
// reliable manual refresh.
//
// Honors the global sync_title switch and the per-session TitleLocked flag (so
// conductor titles like "SCRUM-351" survive Claude's own /rename). On a real
// change it mutates the in-memory instance (Title + tmux display name) and
// drops the iTerm2 badge-update signal so the attach-side WatchBadgeUpdates
// catch-up re-emits the fresh name instead of clobbering it with the old one.
//
// Returns the new name and true iff the title changed; the CALLER is
// responsible for persisting the instance to storage.
func (i *Instance) ReconcileTitleFromClaude(sessionID string) (string, bool) {
	if i == nil || i.TitleLocked {
		return "", false
	}
	if cfg, err := LoadUserConfig(); err == nil && cfg != nil && !cfg.GetSyncTitle() {
		return "", false
	}
	name := ClaudeSessionName(sessionID)
	if name == "" || name == i.Title {
		return "", false
	}
	i.Title = name
	i.SyncTmuxDisplayName()
	if tmuxSess := i.GetTmuxSession(); tmuxSess != nil && tmuxSess.Name != "" {
		_ = tmux.WriteBadgeUpdate(tmuxSess.Name, name)
	}
	return name, true
}
