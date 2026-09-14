package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tmuxInstallDirs are the well-known locations where a tmux binary may be
// installed but omitted from a sparse desktop-launcher, service, or non-login
// shell PATH.
var tmuxInstallDirs = []string{
	"/usr/bin",
	"/opt/homebrew/bin",
	"/usr/local/bin",
	"/opt/local/bin",
	"/home/linuxbrew/.linuxbrew/bin",
	"/snap/bin",
}

// resolveUserToolPATH appends conventional per-user binary directories that
// were omitted from a sparse desktop/service environment. Appending preserves
// every inherited resolution choice while making agent harnesses such as OMP
// available to a long-running Agent Deck process. PATH entries need not exist,
// so this stays filesystem-free on the command startup hot path.
func resolveUserToolPATH(path, home string) string {
	if home == "" {
		return path
	}
	onPath := map[string]bool{}
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		onPath[dir] = true
	}
	for _, dir := range []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".local", "share", "pnpm", "bin"),
		filepath.Join(home, ".local", "share", "pnpm"),
		filepath.Join(home, ".opencode", "bin"),
		filepath.Join(home, "bin"),
	} {
		if onPath[dir] {
			continue
		}
		if path == "" {
			path = dir
		} else {
			path += string(os.PathListSeparator) + dir
		}
		onPath[dir] = true
	}
	return path
}

func ensureUserToolsOnPath() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := os.Getenv("PATH")
	resolved := resolveUserToolPATH(path, home)
	if resolved != path {
		_ = os.Setenv("PATH", resolved)
	}
}

// resolveTmuxPATH returns path augmented with the first candidate dir that holds
// a tmux binary, when tmux is not already resolvable on path. It never
// duplicates a dir already present and is a no-op when tmux is resolvable or no
// candidate has it. Pure (deps injected) so it is unit-testable.
//
// The candidate dir is APPENDED, never prepended, and that is load-bearing.
// The mutated PATH is inherited by everything agent-deck spawns afterwards —
// tmux, but also the agent CLIs launched into sessions and the ps/pgrep probes.
// Prepending a broad dir like /usr/bin would put it ahead of a user's own
// resolution order inside those sessions, so a version-managed node, a
// ~/.local/bin python, or their own agent CLI would silently resolve to a
// different binary than it does in their shell.
//
// Appending cannot do that: this function only runs when tmux does not resolve
// at all, so the appended dir wins only for names that were already
// unresolvable. That makes `tmux` work without reordering anything that
// already worked.
func resolveTmuxPATH(path string, tmuxResolvable bool, candidates []string, hasTmux func(dir string) bool) string {
	if tmuxResolvable {
		return path
	}
	onPath := map[string]bool{}
	for _, d := range strings.Split(path, string(os.PathListSeparator)) {
		onPath[d] = true
	}
	for _, dir := range candidates {
		if onPath[dir] || !hasTmux(dir) {
			continue
		}
		if path == "" {
			return dir
		}
		return path + string(os.PathListSeparator) + dir
	}
	return path
}

// ensureTmuxOnPath augments the process $PATH so bare `tmux` invocations resolve
// even when agent-deck was launched from a minimal environment. The critical
// case: a notification click runs `terminal-notifier -execute "... agent-deck
// session focus <id> --attach"`, which inherits the launchd default PATH with no
// Homebrew dir — so every tmux call (switch-client, detach-client, list-clients)
// silently fails and the notification switch can never fire (it falls back to a
// focus_request the paused TUI only consumes on Ctrl+Q). Idempotent and a no-op
// when tmux is already resolvable.
func ensureTmuxOnPath() {
	_, err := exec.LookPath("tmux")
	newPath := resolveTmuxPATH(os.Getenv("PATH"), err == nil, tmuxInstallDirs, func(dir string) bool {
		info, statErr := os.Stat(filepath.Join(dir, "tmux"))
		// Require a regular, executable file: a non-executable file named "tmux"
		// can't satisfy a bare `tmux` invocation, so adding its dir to PATH is
		// pointless (exec.LookPath would reject it anyway).
		return statErr == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0
	})
	if newPath != os.Getenv("PATH") {
		_ = os.Setenv("PATH", newPath)
	}
}
