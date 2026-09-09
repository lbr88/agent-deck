package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Probe the local client once, only when a launch actually contains dollars.
// Unlike --user operations, --version needs no running service manager.
var systemdRunVersion = sync.OnceValue(func() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemd-run", "--version").Output()
	if err != nil {
		return 0
	}
	var version int
	if _, err := fmt.Sscanf(string(out), "systemd %d", &version); err != nil {
		return 0
	}
	return version
})

// protectSystemdRunArgs preserves tmux's arguments through systemd's extra
// environment-expansion layer. This runs ONLY at the execution boundary: the
// stored command spec stays raw, so service -> scope -> direct retries neither
// double-escape dollars nor hand escaped source directly to bash.
func protectSystemdRunArgs(args []string) []string {
	commandIndex := -1
	for n, arg := range args {
		if arg == "tmux" {
			commandIndex = n
			break
		}
	}
	if commandIndex < 0 {
		return args
	}
	needsProtection := false
	for _, arg := range args[commandIndex+1:] {
		if strings.Contains(arg, "$") {
			needsProtection = true
			break
		}
	}
	if !needsProtection {
		return args
	}

	version := systemdRunVersion()
	if version <= 0 || version >= 254 {
		// Unknown clients fail safely: if this flag is unsupported, Start's
		// existing fallback eventually runs direct tmux with the untouched
		// arguments. Never guess a scope's version-dependent default.
		protected := make([]string, 0, len(args)+1)
		protected = append(protected, args[:commandIndex]...)
		protected = append(protected, "--expand-environment=no")
		return append(protected, args[commandIndex:]...)
	}

	// Before v254 scopes did not expand variables and have no disable flag.
	if wasScopeModeArgs(args[:commandIndex]) {
		return args
	}
	// Old service managers still expand ExecStart arguments. Their documented
	// $$ escape preserves every literal dollar, including bash's ${x%...},
	// ${x##...}, $$ and user-supplied dollars in paths or quoted arguments.
	protected := append([]string(nil), args...)
	for n := commandIndex + 1; n < len(protected); n++ {
		protected[n] = strings.ReplaceAll(protected[n], "$", "$$")
	}
	return protected
}
