package tmux

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func pinSystemdRunVersion(t *testing.T, version int) {
	t.Helper()
	previous := systemdRunVersion
	systemdRunVersion = func() int { return version }
	t.Cleanup(func() { systemdRunVersion = previous })
}

func TestSystemdVersionCacheRetriesUnknownAfterCooldown(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	probes := 0
	version := newSystemdRunVersionCache(func() int {
		probes++
		// Model two transient failures, including the time spent probing.
		now = now.Add(2 * time.Second)
		if probes < 3 {
			return 0
		}
		return 253
	}, func() time.Time { return now })

	for attempt := 1; attempt <= 2; attempt++ {
		require.Zero(t, version())
		require.Equal(t, attempt, probes, "unknown result must be retried after its cooldown")
		for range 3 {
			require.Zero(t, version(), "immediate service/scope retries must share the failed probe")
		}
		now = now.Add(systemdRunVersionRetryDelay - time.Nanosecond)
		require.Zero(t, version(), "cooldown starts when the slow probe finishes")
		require.Equal(t, attempt, probes)
		now = now.Add(time.Nanosecond)
	}
	require.Equal(t, 253, version())
	require.Equal(t, 3, probes)
	now = now.Add(24 * time.Hour)
	require.Equal(t, 253, version())
	require.Equal(t, 3, probes, "a successful result must remain cached")
}

func TestSystemdVersionCacheKeepsSuccessfulVersion(t *testing.T) {
	for _, parsed := range []int{253, 254, 259} {
		t.Run(strconv.Itoa(parsed), func(t *testing.T) {
			now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
			probes := 0
			version := newSystemdRunVersionCache(func() int {
				probes++
				return parsed
			}, func() time.Time { return now })
			require.Equal(t, parsed, version())
			now = now.Add(24 * time.Hour)
			require.Equal(t, parsed, version())
			require.Equal(t, 1, probes)
		})
	}
}

func TestSystemdVersionCacheSharesConcurrentProbe(t *testing.T) {
	for _, parsed := range []int{0, 253, 259} {
		t.Run(strconv.Itoa(parsed), func(t *testing.T) {
			const callers = 16
			var probes atomic.Int32
			entered := make(chan struct{}, callers)
			release := make(chan struct{})
			version := newSystemdRunVersionCache(func() int {
				probes.Add(1)
				entered <- struct{}{}
				<-release
				return parsed
			}, func() time.Time { return time.Unix(1, 0) })
			start := make(chan struct{})
			results := make(chan int, callers)
			var ready sync.WaitGroup
			ready.Add(callers)
			for range callers {
				go func() {
					ready.Done()
					<-start
					results <- version()
				}()
			}
			ready.Wait()
			close(start)
			<-entered
			close(release)
			for range callers {
				require.Equal(t, parsed, <-results)
			}
			require.EqualValues(t, 1, probes.Load(), "concurrent launches must share the same probe")
		})
	}
}

// These assertions cover the launch boundary (not source text): changing the
// version branch or mutating a reusable command spec corrupts either a legacy
// launch or its later direct fallback.
func TestSystemdArgumentProtectionVersionCompatibility(t *testing.T) {
	const command = `file='dir/a.jsonl'; printf '%s\n' "${file%.jsonl}" "${file##*/}" "$plain" '$literal' "$$"`
	const escaped = `file='dir/a.jsonl'; printf '%s\n' "$${file%.jsonl}" "$${file##*/}" "$$plain" '$$literal' "$$$$"`
	for _, tc := range []struct {
		name       string
		version    int
		mode       string
		wantFlag   bool
		wantSource string
		wantCwd    string
	}{
		{"modern service", 259, "service", true, command, "/work/$literal"},
		{"modern scope", 259, "scope", true, command, "/work/$literal"},
		{"first supported service", 254, "service", true, command, "/work/$literal"},
		{"legacy service", 253, "service", false, escaped, "/work/$$literal"},
		{"legacy scope", 253, "scope", false, command, "/work/$literal"},
		{"unknown service fails safely", 0, "service", true, command, "/work/$literal"},
		{"unknown scope fails safely", 0, "scope", true, command, "/work/$literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinSystemdRunVersion(t, tc.version)
			sess := &Session{Name: "argument-test", LaunchAs: tc.mode, RunCommandAsInitialProcess: true}
			launcher, raw := sess.startCommandSpec("/work/$literal", command)
			original := append([]string(nil), raw...)
			cmd := newSpawnCommand(launcher, raw...)
			require.Equal(t, tc.wantFlag, containsArg(cmd.Args, "--expand-environment=no"))
			launched := stripSystemdRunPrefix(cmd.Args[1:])
			require.Equal(t, tc.wantSource, launched[len(launched)-1])
			require.Equal(t, tc.wantCwd, argumentAfter(t, launched, "-c"))
			require.Equal(t, original, raw, "building a launch must not mutate the fallback source")
			direct := newSpawnCommand("tmux", stripSystemdRunPrefix(raw)...)
			require.Equal(t, command, direct.Args[len(direct.Args)-1], "direct fallback must receive unescaped bash source")
			require.Equal(t, "/work/$literal", argumentAfter(t, direct.Args, "-c"))
		})
	}
}

func TestSystemdScopeFallbackProtectsOriginalArguments(t *testing.T) {
	for _, version := range []int{253, 259, 0} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			pinSystemdRunVersion(t, version)
			const command = `printf '%s' '${unchanged} $$'`
			sess := &Session{Name: "argument-fallback", LaunchAs: "service", RunCommandAsInitialProcess: true}
			launcher, raw := sess.startCommandSpec("/work", command)
			_ = newSpawnCommand(launcher, raw...)
			scope := buildScopeArgsFromTmuxArgs(sess.Name, stripSystemdRunPrefix(raw))
			cmd := newSpawnCommand("systemd-run", scope...)
			require.Equal(t, command, cmd.Args[len(cmd.Args)-1], "scope fallback must never inherit service escaping")
			require.Equal(t, version != 253, containsArg(cmd.Args, "--expand-environment=no"))
		})
	}
}

func TestSystemdDollarFreeLaunchNeedsNoVersionProbe(t *testing.T) {
	previous := systemdRunVersion
	systemdRunVersion = func() int { t.Fatal("dollar-free launch must not probe systemd"); return 0 }
	t.Cleanup(func() { systemdRunVersion = previous })
	sess := &Session{Name: "no-dollar", LaunchAs: "service", RunCommandAsInitialProcess: true}
	launcher, raw := sess.startCommandSpec("/work", "exec sleep 60")
	cmd := newSpawnCommand(launcher, raw...)
	require.Equal(t, append([]string{launcher}, raw...), cmd.Args)
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func argumentAfter(t *testing.T, args []string, target string) string {
	t.Helper()
	for n, arg := range args {
		if arg == target && n+1 < len(args) {
			return args[n+1]
		}
	}
	t.Fatalf("missing argument %q in %q", target, args)
	return ""
}
