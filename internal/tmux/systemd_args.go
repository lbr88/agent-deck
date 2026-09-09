package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Probe the local client only when a launch actually contains dollars. Keep a
// successful version; briefly cache failures so service -> scope fallback does
// not repeat a slow probe, but a transient timeout does not poison all launches.
// Unlike --user operations, --version needs no running service manager.
var systemdRunVersion = newSystemdRunVersionCache(probeSystemdRunVersion, time.Now)

const systemdRunVersionRetryDelay = 5 * time.Second

func newSystemdRunVersionCache(probe func() int, now func() time.Time) func() int {
	var mu sync.Mutex
	var version int
	var retryAt time.Time
	return func() int {
		// Serialize probes as well as cache access: concurrent launches share
		// one bounded probe rather than each paying for a failed subprocess.
		mu.Lock()
		defer mu.Unlock()
		if version > 0 || now().Before(retryAt) {
			return version
		}
		version = probe()
		if version <= 0 {
			version = 0
			retryAt = now().Add(systemdRunVersionRetryDelay)
		}
		return version
	}
}

func probeSystemdRunVersion() int {
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
}

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
		// Let append perform checked growth; do not compute len(args)+1.
		protected := append([]string(nil), args[:commandIndex]...)
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
