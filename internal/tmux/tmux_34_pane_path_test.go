package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmux 3.4's no-client output path runs every expanded format through
// utf8_strvis(). That release unconditionally inserts one backslash before a
// dollar followed by a letter, underscore, or opening brace. tmux 3.5 fixed
// this by applying that transformation only when VIS_DQ was requested.
//
// These are byte-for-byte observations from Ubuntu Noble's
// tmux 3.4-1ubuntu0.1 binary, the version used by GitHub's ubuntu-latest runner.
func TestParsePanePathOutput_Tmux34DollarSerialization(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "variable-like dollars are escaped",
			output: `3.4|/work/project \$plain $$ \${brace} end$`,
			want:   `/work/project $plain $$ ${brace} end$`,
		},
		{
			name:   "a genuine backslash before a variable-like dollar is preserved",
			output: `3.4|/work/real\\$slash`,
			want:   `/work/real\$slash`,
		},
		{
			name:   "a 3.4 patch release keeps the legacy behavior",
			output: `3.4a|/work/\$literal`,
			want:   `/work/$literal`,
		},
		{
			name:   "tmux 3.5 does not add the legacy escape",
			output: `3.5|/work/real\$slash`,
			want:   `/work/real\$slash`,
		},
		{
			name:   "tmux 3.5 patch releases do not add the legacy escape",
			output: `3.5a|/work/real\$slash`,
			want:   `/work/real\$slash`,
		},
		{
			name:   "unknown versions are not guessed",
			output: `next|/work/real\$slash`,
			want:   `/work/real\$slash`,
		},
		{
			name:   "custom numeric versions are not treated as verified legacy releases",
			output: `3.4custom|/work/real\$slash`,
			want:   `/work/real\$slash`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parsePanePathOutput(tt.output))
		})
	}
}

// Decoding must remain one-to-one even when both the escaped-looking path and
// its unescaped sibling exist. Guessing based on which candidate exists can
// silently retarget a live pane to another project.
func TestParsePanePathOutput_DistinguishesBackslashDollarSiblingDirectories(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, `project ${AD_LITERAL}`)
	backslashed := filepath.Join(root, `project \${AD_LITERAL}`)
	require.NoError(t, os.Mkdir(plain, 0o755))
	require.NoError(t, os.Mkdir(backslashed, 0o755))

	legacyPlain := parsePanePathOutput("3.4|" + filepath.Join(root, `project \${AD_LITERAL}`))
	legacyBackslashed := parsePanePathOutput("3.4|" + filepath.Join(root, `project \\${AD_LITERAL}`))
	modernBackslashed := parsePanePathOutput("3.5|" + filepath.Join(root, `project \${AD_LITERAL}`))

	assert.Equal(t, plain, legacyPlain)
	assert.Equal(t, paneCwdOK, classifyPaneCwd(plain, legacyPlain))
	assert.Equal(t, backslashed, legacyBackslashed)
	assert.Equal(t, paneCwdOK, classifyPaneCwd(backslashed, legacyBackslashed))
	assert.Equal(t, backslashed, modernBackslashed)
	assert.Equal(t, paneCwdOK, classifyPaneCwd(backslashed, modernBackslashed))
}

func TestParsePanePathOutput_PreservesPathSpacesAndControlFraming(t *testing.T) {
	assert.Equal(t, "  /work/trailing  ", parsePanePathOutput("3.4|  /work/trailing  \r\n"))
}

func TestParsePanePathOutput_RejectsMissingVersionFrame(t *testing.T) {
	assert.Empty(t, parsePanePathOutput(`/work/\$literal`))
	assert.Empty(t, parsePanePathOutput(`/work/name|part`))
	assert.Empty(t, parsePanePathOutput(`|/work/name`))
}

// Exercise every production consumer against the installed tmux. GitHub's
// ubuntu-latest runner currently supplies tmux 3.4-1ubuntu0.1, so this is an
// end-to-end regression for both the exact old serializer and the same-query
// #{version} discriminator. It also runs on newer tmux, where no decoding is
// needed.
func TestPanePathConsumersPreserveDollarPaths(t *testing.T) {
	skipIfNoTmuxBinary(t)

	root := t.TempDir()
	workDir := filepath.Join(root, `project ${AD_LITERAL} \${REAL_LITERAL} 'quoted' | colon: space`)
	require.NoError(t, os.Mkdir(workDir, 0o755))

	socket := DefaultSocketName()
	name := "agentdeck_tmux34_path_consumers"
	_ = runBoundedRun(socket, "kill-session", "-t", name)
	t.Cleanup(func() { _ = runBoundedRun(socket, "kill-session", "-t", name) })
	require.NoError(t, runBoundedRun(socket, "new-session", "-d", "-s", name, "-c", workDir, "sleep", "60"))

	RefreshSessionCache()
	sess := &Session{Name: name, SocketName: socket}
	// new-session acknowledges the fork before the child has necessarily
	// chdir'ed. As in the deleted-cwd regression, wait for actual placement;
	// a wrongly decoded or wrong-directory report still fails this guard.
	requirePaneSettledIn(t, sess, workDir)
	reported, err := panePathProbe(sess)
	require.NoError(t, err)
	assert.Equal(t, workDir, reported)
	assert.Equal(t, workDir, sess.GetWorkDir())

	managed, err := ListAllSessions()
	require.NoError(t, err)
	assert.Equal(t, workDir, findSessionWorkDir(managed, name))

	discovered, err := DiscoverAllTmuxSessions()
	require.NoError(t, err)
	assert.Equal(t, workDir, findSessionWorkDir(discovered, name))
}

func findSessionWorkDir(sessions []*Session, name string) string {
	for _, sess := range sessions {
		if sess.Name == name {
			return sess.WorkDir
		}
	}
	return "missing: " + name + " among " + strings.Join(sessionNames(sessions), ", ")
}

func sessionNames(sessions []*Session) []string {
	names := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		names = append(names, sess.Name)
	}
	return names
}
