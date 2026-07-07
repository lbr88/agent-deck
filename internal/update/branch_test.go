package update

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeUpdateChannelDefaultsToReleaseAndAcceptsBranch(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default", input: "", want: UpdateChannelRelease},
		{name: "release", input: "release", want: UpdateChannelRelease},
		{name: "branch", input: "branch", want: UpdateChannelBranch},
		{name: "trims and lowercases", input: " Branch ", want: UpdateChannelBranch},
		{name: "invalid", input: "nightly", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeUpdateChannel(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCommitMatchesAcceptsFullOrShortSHA(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{
			name:    "full match",
			current: "abcdef1234567890",
			latest:  "abcdef1234567890",
			want:    true,
		},
		{
			name:    "current short prefix",
			current: "abcdef1",
			latest:  "abcdef1234567890",
			want:    true,
		},
		{
			name:    "latest short prefix",
			current: "abcdef1234567890",
			latest:  "abcdef1",
			want:    true,
		},
		{
			name:    "too short prefix is not trusted",
			current: "abc",
			latest:  "abcdef1234567890",
			want:    false,
		},
		{
			name:    "different",
			current: "abcdef1234567890",
			latest:  "1111111234567890",
			want:    false,
		},
		{
			name:    "missing current",
			current: "",
			latest:  "abcdef1234567890",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CommitMatches(tt.current, tt.latest))
		})
	}
}

func TestCheckBranchUpdateUsesConfiguredRepoAndBranch(t *testing.T) {
	require.NoError(t, SetGitHubRepo("lbr88/agent-deck"))
	t.Cleanup(func() { require.NoError(t, SetGitHubRepo("")) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/"+GitHubRepo+"/commits/main", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("upstream repo endpoint should not be used when updates.repo is configured")
	})
	mux.HandleFunc("GET /repos/lbr88/agent-deck/commits/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sha":"abcdef1234567890","html_url":"https://github.com/lbr88/agent-deck/commit/abcdef1234567890"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origURL := apiBaseURL
	apiBaseURL = srv.URL
	t.Cleanup(func() { apiBaseURL = origURL })
	isolateUpdatePaths(t)

	info, err := CheckBranchUpdate("1111111234567890", "main", true)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.True(t, info.Available)
	assert.Equal(t, "lbr88/agent-deck", info.Repo)
	assert.Equal(t, "main", info.Branch)
	assert.Equal(t, "1111111234567890", info.CurrentCommit)
	assert.Equal(t, "abcdef1234567890", info.LatestCommit)
	assert.Equal(t, "https://github.com/lbr88/agent-deck/commit/abcdef1234567890", info.CommitURL)
}

func TestCheckBranchUpdateReportsLatestWhenCommitMatches(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/"+GitHubRepo+"/commits/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sha":"abcdef1234567890","html_url":"https://github.com/asheshgoplani/agent-deck/commit/abcdef1234567890"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origURL := apiBaseURL
	apiBaseURL = srv.URL
	t.Cleanup(func() { apiBaseURL = origURL })
	isolateUpdatePaths(t)

	info, err := CheckBranchUpdate("abcdef1", "main", true)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.False(t, info.Available)
	assert.Equal(t, "abcdef1234567890", info.LatestCommit)
}

func TestPerformBranchUpdateBuildsAndInstalls(t *testing.T) {
	oldBinary := []byte("old-agent-deck")
	newBinary := []byte("new-branch-agent-deck")
	execPath := selfUpdateTestTarget(t, oldBinary)

	origBuilder := branchSourceBuilder
	branchSourceBuilder = func(info *BranchUpdateInfo, tempDir string) ([]byte, error) {
		assert.Equal(t, "main", info.Branch)
		assert.NotEmpty(t, tempDir)
		return newBinary, nil
	}
	t.Cleanup(func() { branchSourceBuilder = origBuilder })

	err := PerformBranchUpdate(&BranchUpdateInfo{
		Repo:         GitHubRepo,
		Branch:       "main",
		LatestCommit: "abcdef1234567890",
	})
	require.NoError(t, err)

	installed, err := os.ReadFile(execPath)
	require.NoError(t, err)
	assert.Equal(t, newBinary, installed)
}

func TestPerformBranchUpdateBuildFailureLeavesBinaryUntouched(t *testing.T) {
	oldBinary := []byte("old-agent-deck")
	execPath := selfUpdateTestTarget(t, oldBinary)

	origBuilder := branchSourceBuilder
	branchSourceBuilder = func(info *BranchUpdateInfo, tempDir string) ([]byte, error) {
		return nil, errors.New("build failed")
	}
	t.Cleanup(func() { branchSourceBuilder = origBuilder })

	err := PerformBranchUpdate(&BranchUpdateInfo{
		Repo:         GitHubRepo,
		Branch:       "main",
		LatestCommit: "abcdef1234567890",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build failed")

	installed, readErr := os.ReadFile(execPath)
	require.NoError(t, readErr)
	assert.Equal(t, oldBinary, installed)
}
