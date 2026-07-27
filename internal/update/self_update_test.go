package update

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallSelfUpdateBinaryRejectsCandidateBeforeSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script candidate is Unix-only")
	}
	isolateUpdatePaths(t)

	dir := t.TempDir()
	execPath := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("old-agent-deck")
	require.NoError(t, os.WriteFile(execPath, oldBinary, 0o755))

	badCandidate := []byte("#!/bin/sh\n[ \"$1\" = version ] || exit 41\necho broken >&2\nexit 42\n")
	err := installSelfUpdateBinary(execPath, badCandidate)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "candidate `version` failed")
	assert.Contains(t, err.Error(), "broken")

	installed, readErr := os.ReadFile(execPath)
	require.NoError(t, readErr)
	assert.Equal(t, oldBinary, installed)
	assertNoSelfUpdateScratchFiles(t, dir)
}

func TestInstallSelfUpdateBinaryRejectsSuccessfulNonAgentDeckExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script candidate is Unix-only")
	}
	isolateUpdatePaths(t)
	dir := t.TempDir()
	execPath := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("old-agent-deck")
	require.NoError(t, os.WriteFile(execPath, oldBinary, 0o755))

	err := installSelfUpdateBinary(execPath, []byte("#!/bin/sh\nexit 0\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output is not Agent Deck")
	installed, readErr := os.ReadFile(execPath)
	require.NoError(t, readErr)
	assert.Equal(t, oldBinary, installed)
}

func TestInstallSelfUpdateBinaryStagesUniqueCandidatesBesideTarget(t *testing.T) {
	isolateUpdatePaths(t)
	dir := t.TempDir()
	execPath := filepath.Join(dir, "agent-deck")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0o755))

	origValidator := validateSelfUpdateCandidate
	var candidates []string
	validateSelfUpdateCandidate = func(path string) error {
		candidates = append(candidates, path)
		assert.Equal(t, dir, filepath.Dir(path))
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.NotZero(t, info.Mode().Perm()&0o111)
		return nil
	}
	t.Cleanup(func() { validateSelfUpdateCandidate = origValidator })

	require.NoError(t, installSelfUpdateBinary(execPath, []byte("new-one")))
	require.NoError(t, installSelfUpdateBinary(execPath, []byte("new-two")))
	require.Len(t, candidates, 2)
	assert.NotEqual(t, candidates[0], candidates[1])

	installed, err := os.ReadFile(execPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("new-two"), installed)
	assertNoSelfUpdateScratchFiles(t, dir)
}

func TestInstallSelfUpdateBinaryRestoresBackupAfterAmbiguousReplaceFailure(t *testing.T) {
	isolateUpdatePaths(t)
	dir := t.TempDir()
	execPath := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("known-good-old-binary")
	newBinary := []byte("candidate-binary")
	require.NoError(t, os.WriteFile(execPath, oldBinary, 0o751))

	origValidator := validateSelfUpdateCandidate
	validateSelfUpdateCandidate = func(string) error { return nil }
	t.Cleanup(func() { validateSelfUpdateCandidate = origValidator })

	origReplace := replaceSelfUpdateFile
	var calls atomic.Int32
	replaceSelfUpdateFile = func(source, target string) error {
		if calls.Add(1) == 1 {
			// Model the most pessimistic platform failure: the destination was
			// changed, but the replacement API still returned an error.
			if err := origReplace(source, target); err != nil {
				return err
			}
			return errors.New("injected replacement failure after mutation")
		}
		return origReplace(source, target)
	}
	t.Cleanup(func() { replaceSelfUpdateFile = origReplace })

	err := installSelfUpdateBinary(execPath, newBinary)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "previous binary restored")
	assert.Equal(t, int32(2), calls.Load(), "failed replace plus atomic rollback")

	installed, readErr := os.ReadFile(execPath)
	require.NoError(t, readErr)
	assert.Equal(t, oldBinary, installed)
	info, statErr := os.Stat(execPath)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0o751), info.Mode().Perm())
	assertNoSelfUpdateScratchFiles(t, dir)
}

func TestInstallSelfUpdateBinaryPreservesBackupWhenRollbackFails(t *testing.T) {
	isolateUpdatePaths(t)
	dir := t.TempDir()
	execPath := filepath.Join(dir, "agent-deck")
	oldBinary := []byte("known-good-old-binary")
	require.NoError(t, os.WriteFile(execPath, oldBinary, 0o751))

	origValidator := validateSelfUpdateCandidate
	validateSelfUpdateCandidate = func(string) error { return nil }
	t.Cleanup(func() { validateSelfUpdateCandidate = origValidator })

	origReplace := replaceSelfUpdateFile
	var calls atomic.Int32
	replaceSelfUpdateFile = func(string, string) error {
		calls.Add(1)
		return errors.New("injected replace failure")
	}
	t.Cleanup(func() { replaceSelfUpdateFile = origReplace })

	err := installSelfUpdateBinary(execPath, []byte("candidate"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "previous binary preserved at")
	assert.Equal(t, int32(2), calls.Load(), "publish attempt plus rollback attempt")

	matches, globErr := filepath.Glob(filepath.Join(dir, ".agent-deck.rollback-*"))
	require.NoError(t, globErr)
	require.Len(t, matches, 1, "known-good rollback artifact must survive failed restore")
	preserved, readErr := os.ReadFile(matches[0])
	require.NoError(t, readErr)
	assert.Equal(t, oldBinary, preserved)
}

func TestInstallSelfUpdateBinarySerializesAcrossProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script candidates are Unix-only")
	}

	dir := t.TempDir()
	execPath := filepath.Join(dir, "agent-deck")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0o755))

	firstCandidate := filepath.Join(dir, "first-candidate")
	secondCandidate := filepath.Join(dir, "second-candidate")
	firstBytes := []byte("#!/bin/sh\nif [ \"$1\" != version ]; then exit 2; fi\n: > \"$AD_FIRST_VALIDATING\"\nwhile [ ! -e \"$AD_RELEASE_FIRST\" ]; do sleep 0.1; done\necho 'Agent Deck vfirst'\n")
	secondBytes := []byte("#!/bin/sh\nif [ \"$1\" != version ]; then exit 2; fi\n: > \"$AD_SECOND_VALIDATING\"\necho 'Agent Deck vsecond'\n")
	require.NoError(t, os.WriteFile(firstCandidate, firstBytes, 0o755))
	require.NoError(t, os.WriteFile(secondCandidate, secondBytes, 0o755))

	firstValidating := filepath.Join(dir, "first-validating")
	secondValidating := filepath.Join(dir, "second-validating")
	releaseFirst := filepath.Join(dir, "release-first")
	cacheDir := filepath.Join(dir, "cache")
	firstHelperStarted := filepath.Join(dir, "first-helper-started")
	secondHelperStarted := filepath.Join(dir, "second-helper-started")

	firstCmd, firstOutput := selfUpdateHelperCommand(
		t, execPath, firstCandidate, cacheDir, firstHelperStarted,
		firstValidating, secondValidating, releaseFirst,
	)
	require.NoError(t, firstCmd.Start())
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFirst, nil, 0o600)
		if firstCmd.Process != nil {
			_ = firstCmd.Process.Kill()
		}
	})

	require.Eventually(t, func() bool {
		_, err := os.Stat(firstValidating)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "first updater never entered candidate validation")

	secondCmd, secondOutput := selfUpdateHelperCommand(
		t, execPath, secondCandidate, cacheDir, secondHelperStarted,
		firstValidating, secondValidating, releaseFirst,
	)
	require.NoError(t, secondCmd.Start())
	t.Cleanup(func() {
		if secondCmd.Process != nil {
			_ = secondCmd.Process.Kill()
		}
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(secondHelperStarted)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "second updater helper never started")

	// The first candidate is deliberately blocked inside `version` while it
	// owns the updater lock. A second process must not even begin validation.
	time.Sleep(350 * time.Millisecond)
	_, secondStatErr := os.Stat(secondValidating)
	assert.True(t, os.IsNotExist(secondStatErr), "second process validated before first released the lock")

	require.NoError(t, os.WriteFile(releaseFirst, nil, 0o600))
	require.NoError(t, firstCmd.Wait(), "first helper output:\n%s", firstOutput.String())
	firstCmd.Process = nil
	require.NoError(t, secondCmd.Wait(), "second helper output:\n%s", secondOutput.String())
	secondCmd.Process = nil

	installed, err := os.ReadFile(execPath)
	require.NoError(t, err)
	assert.Equal(t, secondBytes, installed)
	assertNoSelfUpdateScratchFiles(t, dir)
}

func TestSelfUpdateProcessHelper(t *testing.T) {
	if os.Getenv("AD_SELF_UPDATE_HELPER") != "1" {
		return
	}

	execPath := os.Getenv("AD_SELF_UPDATE_TARGET")
	candidatePath := os.Getenv("AD_SELF_UPDATE_CANDIDATE")
	if err := os.WriteFile(os.Getenv("AD_SELF_UPDATE_HELPER_STARTED"), nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	candidate, err := os.ReadFile(candidatePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := installSelfUpdateBinary(execPath, candidate); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	os.Exit(0)
}

func selfUpdateHelperCommand(
	t *testing.T,
	execPath, candidatePath, cacheDir, helperStarted,
	firstValidating, secondValidating, releaseFirst string,
) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestSelfUpdateProcessHelper$")
	cmd.Env = append(os.Environ(),
		"AD_SELF_UPDATE_HELPER=1",
		"AD_SELF_UPDATE_TARGET="+execPath,
		"AD_SELF_UPDATE_CANDIDATE="+candidatePath,
		"AD_SELF_UPDATE_HELPER_STARTED="+helperStarted,
		"AD_FIRST_VALIDATING="+firstValidating,
		"AD_SECOND_VALIDATING="+secondValidating,
		"AD_RELEASE_FIRST="+releaseFirst,
		"HOME="+filepath.Join(filepath.Dir(cacheDir), "helper-home"),
		"XDG_DATA_HOME="+filepath.Join(filepath.Dir(cacheDir), "helper-data"),
		"XDG_CACHE_HOME="+cacheDir,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	return cmd, &output
}

func assertNoSelfUpdateScratchFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if strings.Contains(name, ".update-") || strings.Contains(name, ".rollback-") {
			t.Errorf("self-update scratch file was not cleaned up: %s", filepath.Join(dir, name))
		}
	}
}
