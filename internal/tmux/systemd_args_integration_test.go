package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/stretchr/testify/require"
)

// This must run the production command builder AND launch boundary. Running
// only bash, or copying systemd-run's argv into a test helper, misses systemd's
// expansion of ${file%.jsonl} before the pane's bash ever sees the command.
func TestSystemdLaunchPreservesShellArguments(t *testing.T) {
	if testing.Short() {
		t.Skip("real systemd-user integration")
	}
	requireSystemdUserRun(t)
	skipIfNoTmuxBinary(t)

	for _, route := range []string{"service", "scope", "fallback-scope", "fallback-direct"} {
		for _, existingServer := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing-server=%t", route, existingServer), func(t *testing.T) {
				runSystemdArgumentFixture(t, route, existingServer)
			})
		}
	}
}

// The old service compatibility path uses the manager's long-standing $$
// escaping rule, which is also available on current managers. Exercise it
// against real systemd instead of merely asserting the escaped argv spelling.
func TestSystemdLegacyServicePreservesShellArguments(t *testing.T) {
	if testing.Short() {
		t.Skip("real systemd-user integration")
	}
	requireSystemdUserRun(t)
	skipIfNoTmuxBinary(t)
	pinSystemdRunVersion(t, 253)
	for _, existingServer := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-server=%t", existingServer), func(t *testing.T) {
			runSystemdArgumentFixture(t, "service", existingServer)
		})
	}
}

// A failed/unknown version probe must not guess the scope expansion default.
// Simulate a client rejecting the modern flag at the external launcher seam,
// then run Start's real service -> scope -> direct fallback and its real pane.
func TestSystemdUnknownClientFallsBackWithoutChangingCommand(t *testing.T) {
	skipIfNoTmuxBinary(t)
	pinSystemdRunVersion(t, 0)
	previous := execCommand
	defer func() { execCommand = previous }()
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "systemd-run" {
			return exec.Command("false")
		}
		return previous(name, args...)
	}
	runSystemdArgumentFixture(t, "rejected-systemd-direct", false)
}

func runSystemdArgumentFixture(t *testing.T, route string, existingServer bool) {
	t.Helper()
	root := t.TempDir()
	// The cwd is a separate tmux argument; protect it as well as the script.
	project := filepath.Join(root, "project ${AD_LITERAL} 'quoted' space")
	require.NoError(t, os.Mkdir(project, 0o700))
	report := filepath.Join(root, "result")
	suffix := randomServerSuffix(t)
	socketName := "ad-expand-" + suffix
	socketPath := filepath.Join(os.Getenv("TMUX_TMPDIR"), fmt.Sprintf("tmux-%d", os.Getuid()), socketName)
	mode := route
	if strings.HasPrefix(route, "fallback-") || route == "rejected-systemd-direct" {
		mode = "service"
	}
	sess := &Session{
		Name:                       "agentdeck_expand_" + suffix,
		WorkDir:                    project,
		SocketName:                 socketName,
		LaunchAs:                   mode,
		RunCommandAsInitialProcess: true,
	}
	unit := serviceUnitBase(sess.Name) + ".service"
	if route == "scope" || route == "fallback-scope" {
		unit = serviceUnitBase(sess.Name) + ".scope"
	}
	// Register cleanup before launch, including when a failing launch leaves a
	// transient unit behind. These names/socket paths belong only to this test.
	t.Cleanup(func() {
		if route != "fallback-direct" && route != "rejected-systemd-direct" {
			_ = exec.Command("systemctl", "--user", "stop", unit).Run()
			_ = exec.Command("systemctl", "--user", "reset-failed", unit).Run()
		}
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
	})
	if existingServer {
		bootstrap := exec.Command("tmux", "-S", socketPath, "-f", "/dev/null", "new-session", "-d", "-s", "keeper", "sleep", "60")
		out, err := bootstrap.CombinedOutput()
		require.NoError(t, err, "create isolated existing server: %s", out)
	}

	command := `source_file='some dir/session.jsonl'; plain='value with spaces and "quotes"'; ` +
		`printf '%s\n' "${source_file%.jsonl}" "${source_file##*/}" "$plain" '$literal ${untouched} $$' "$$" "$PWD" > ` +
		shellescape.Quote(report) + `; exec sleep 60`
	launcher, args := sess.startCommandSpec(project, command)
	if route == "fallback-scope" {
		args = buildScopeArgsFromTmuxArgs(sess.Name, stripSystemdRunPrefix(args))
	} else if route == "fallback-direct" {
		launcher, args = "tmux", stripSystemdRunPrefix(args)
	}
	if launcher == "systemd-run" {
		// Service-mode children inherit the manager's environment, not the
		// caller's. Pass test isolation explicitly; do not change the manager.
		args = append([]string{
			"--setenv=HOME=" + root,
			"--setenv=TMUX_TMPDIR=" + os.Getenv("TMUX_TMPDIR"),
			"--setenv=AGENTDECK_PROFILE=_test",
			"--setenv=XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		}, args...)
	}
	if route == "rejected-systemd-direct" {
		require.NoError(t, sess.Start(command), "real Start must recover through direct fallback")
	} else {
		cmd := newSpawnCommand(launcher, args...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "production %s launch: %s", route, out)
	}

	var data []byte
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err = os.ReadFile(report)
		if err == nil && len(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")) == 6 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err, "pane must write its argument report")
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	require.Len(t, lines, 6, "complete pane output: %q", data)
	require.Equal(t, []string{
		"some dir/session", "session.jsonl", `value with spaces and "quotes"`, "$literal ${untouched} $$",
	}, lines[:4], "systemd must deliver the shell source verbatim")
	pid, err := strconv.Atoi(lines[4])
	require.NoError(t, err, "bash's $$ must expand to its PID, not a literal dollar")
	require.Positive(t, pid)
	require.Equal(t, project, lines[5], "systemd must preserve dollars/quotes in tmux's cwd argument")
	panePID, err := exec.Command("tmux", "-S", socketPath, "display-message", "-t", sess.Name, "-p", "#{pane_pid}").Output()
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(string(panePID)), lines[4], "the reported PID must be the pane leader")
}
