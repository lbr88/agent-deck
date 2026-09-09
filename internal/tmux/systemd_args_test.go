package tmux

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func pinSystemdRunVersion(t *testing.T, version int) {
	t.Helper()
	previous := systemdRunVersion
	systemdRunVersion = func() int { return version }
	t.Cleanup(func() { systemdRunVersion = previous })
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
