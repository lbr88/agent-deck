package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	UpdateChannelRelease = "release"
	UpdateChannelBranch  = "branch"
	DefaultUpdateBranch  = "main"
)

type BranchHead struct {
	Repo    string
	Branch  string
	SHA     string
	HTMLURL string
}

type BranchUpdateInfo struct {
	Available     bool
	Repo          string
	Branch        string
	CurrentCommit string
	LatestCommit  string
	CommitURL     string
}

type BranchSourceBuilder func(info *BranchUpdateInfo, tempDir string) ([]byte, error)

var branchSourceBuilder BranchSourceBuilder = buildBranchSource

func NormalizeUpdateChannel(channel string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "", UpdateChannelRelease:
		return UpdateChannelRelease, nil
	case UpdateChannelBranch:
		return UpdateChannelBranch, nil
	default:
		return "", fmt.Errorf("invalid update channel %q; expected %q or %q", channel, UpdateChannelRelease, UpdateChannelBranch)
	}
}

func NormalizeUpdateBranch(branch string) string {
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" {
		return DefaultUpdateBranch
	}
	return trimmed
}

func BranchVersion(branch string) string {
	name := NormalizeUpdateBranch(branch)
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-")
	return "branch-" + replacer.Replace(name)
}

func CommitMatches(current, latest string) bool {
	current = strings.ToLower(strings.TrimSpace(current))
	latest = strings.ToLower(strings.TrimSpace(latest))
	if current == "" || latest == "" {
		return false
	}
	if current == latest {
		return true
	}
	if len(current) >= 7 && strings.HasPrefix(latest, current) {
		return true
	}
	if len(latest) >= 7 && strings.HasPrefix(current, latest) {
		return true
	}
	return false
}

func ShortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) <= 12 {
		if commit == "" {
			return "unknown"
		}
		return commit
	}
	return commit[:12]
}

func FetchBranchHead(branch string) (*BranchHead, error) {
	branch = NormalizeUpdateBranch(branch)
	apiURL := fmt.Sprintf("%s/repos/%s/commits/%s", apiBaseURL, ConfiguredGitHubRepo(), url.PathEscape(branch))

	resp, authed, err := githubAPIGet(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch branch %s: %w", branch, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("branch %s not found on GitHub repo %s", branch, ConfiguredGitHubRepo())
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden && !authed {
			return nil, fmt.Errorf("GitHub API rate limit exceeded fetching branch %s (anonymous limit is 60/hour). Set GITHUB_TOKEN or install/login with the gh CLI to authenticate", branch)
		}
		return nil, fmt.Errorf("GitHub API returned status %d for branch %s", resp.StatusCode, branch)
	}

	var payload struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to parse branch %s: %w", branch, err)
	}
	if strings.TrimSpace(payload.SHA) == "" {
		return nil, fmt.Errorf("branch %s response did not include a commit SHA", branch)
	}

	return &BranchHead{
		Repo:    ConfiguredGitHubRepo(),
		Branch:  branch,
		SHA:     strings.TrimSpace(payload.SHA),
		HTMLURL: payload.HTMLURL,
	}, nil
}

func CheckBranchUpdate(currentCommit, branch string, forceCheck bool) (*BranchUpdateInfo, error) {
	branch = NormalizeUpdateBranch(branch)
	info := &BranchUpdateInfo{
		Available:     false,
		Repo:          ConfiguredGitHubRepo(),
		Branch:        branch,
		CurrentCommit: strings.TrimSpace(currentCommit),
	}

	if isUpdateCheckSkipped() {
		return info, nil
	}

	if !forceCheck {
		cache, err := loadCache()
		if err == nil && cacheMatchesBranch(cache, branch) && time.Since(cache.CheckedAt) < checkInterval {
			info.LatestCommit = cache.LatestCommit
			info.CommitURL = cache.CommitURL
			info.Available = !CommitMatches(info.CurrentCommit, info.LatestCommit)
			return info, nil
		}
	}

	head, err := FetchBranchHead(branch)
	if err != nil {
		return info, err
	}

	info.Repo = head.Repo
	info.Branch = head.Branch
	info.LatestCommit = head.SHA
	info.CommitURL = head.HTMLURL
	info.Available = !CommitMatches(info.CurrentCommit, info.LatestCommit)

	_ = saveCache(&UpdateCache{
		CheckedAt:      time.Now(),
		Channel:        UpdateChannelBranch,
		Repo:           info.Repo,
		Branch:         info.Branch,
		CurrentCommit:  info.CurrentCommit,
		LatestCommit:   info.LatestCommit,
		CommitURL:      info.CommitURL,
		CurrentVersion: "",
		LatestVersion:  "",
	})

	return info, nil
}

func PerformBranchUpdate(info *BranchUpdateInfo) error {
	if info == nil {
		return fmt.Errorf("branch update info is required")
	}
	if strings.TrimSpace(info.LatestCommit) == "" {
		return fmt.Errorf("branch update requires a target commit")
	}

	execPath, upgradeCmd, managed, err := detectHomebrewManagedInstall()
	if err != nil {
		return fmt.Errorf("failed to detect install type: %w", err)
	}
	if managed {
		return fmt.Errorf("homebrew-managed install detected at %s; use `%s`", execPath, upgradeCmd)
	}

	tempDir, err := os.MkdirTemp("", "agent-deck-branch-update-*")
	if err != nil {
		return fmt.Errorf("failed to create update temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	binaryData, err := branchSourceBuilder(info, tempDir)
	if err != nil {
		return err
	}
	if len(binaryData) == 0 {
		return fmt.Errorf("branch build produced an empty binary")
	}

	return installSelfUpdateBinary(execPath, binaryData)
}

func buildBranchSource(info *BranchUpdateInfo, tempDir string) ([]byte, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git is required for branch updates: %w", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		return nil, fmt.Errorf("go is required for branch updates: %w", err)
	}

	repo := strings.TrimSpace(info.Repo)
	if repo == "" {
		repo = ConfiguredGitHubRepo()
	}
	branch := NormalizeUpdateBranch(info.Branch)
	sourceDir := filepath.Join(tempDir, "source")
	outputPath := filepath.Join(tempDir, "agent-deck")
	repoURL := "https://github.com/" + repo + ".git"

	fmt.Printf("Fetching %s branch %s...\n", repo, branch)
	if err := runUpdateCommand(tempDir, "git", "clone", "--depth", "1", "--branch", branch, repoURL, sourceDir); err != nil {
		return nil, err
	}
	if err := runUpdateCommand(sourceDir, "git", "fetch", "--depth", "1", "origin", info.LatestCommit); err != nil {
		return nil, err
	}
	if err := runUpdateCommand(sourceDir, "git", "checkout", "--detach", info.LatestCommit); err != nil {
		return nil, err
	}

	fmt.Printf("Building %s at %s...\n", BranchVersion(branch), ShortCommit(info.LatestCommit))
	ldflags := fmt.Sprintf("-X main.Version=%s -X main.Commit=%s", BranchVersion(branch), info.LatestCommit)
	if err := runUpdateCommand(sourceDir, "go", "build", "-ldflags", ldflags, "-o", outputPath, "./cmd/agent-deck"); err != nil {
		return nil, err
	}

	binaryData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read built binary: %w", err)
	}
	return binaryData, nil
}

func runUpdateCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			return fmt.Errorf("%s failed: %w", name, err)
		}
		return fmt.Errorf("%s failed: %w\n%s", name, err, text)
	}
	return nil
}

func cacheMatchesBranch(cache *UpdateCache, branch string) bool {
	if cache == nil {
		return false
	}
	if cache.Channel != UpdateChannelBranch {
		return false
	}
	if !cacheMatchesConfiguredRepo(cache) {
		return false
	}
	return NormalizeUpdateBranch(cache.Branch) == NormalizeUpdateBranch(branch)
}
