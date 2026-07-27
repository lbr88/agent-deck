package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolveLaunchPathPrefersArgv0LookupAndPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "agent-deck-1.2.3")
	stable := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, versioned, "versioned-binary")
	if err := os.Symlink(versioned, stable); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	executableCalled := false
	got, err := resolveLaunchPath(
		"agent-deck",
		func(name string) (string, error) {
			if name != "agent-deck" {
				t.Fatalf("LookPath name = %q, want agent-deck", name)
			}
			return stable, nil
		},
		func() (string, error) {
			executableCalled = true
			return versioned, nil
		},
	)
	if err != nil {
		t.Fatalf("resolve launch path: %v", err)
	}
	if executableCalled {
		t.Fatal("os.Executable fallback called despite successful argv0 lookup")
	}
	if got != stable {
		t.Fatalf("launch path = %q, want stable symlink %q", got, stable)
	}
}

func TestResolveLaunchPathFallsBackToExecutable(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "agent-deck-1.2.3")

	got, err := resolveLaunchPath(
		"missing-agent-deck",
		func(string) (string, error) { return "", errors.New("not found") },
		func() (string, error) { return versioned, nil },
	)
	if err != nil {
		t.Fatalf("resolve launch path: %v", err)
	}
	if got != versioned {
		t.Fatalf("launch path = %q, want executable fallback %q", got, versioned)
	}
}

func TestResolveLaunchPathMigratesVersionedAbsolutePathToSameFilePATHAlias(t *testing.T) {
	dir := t.TempDir()
	versionedDir := filepath.Join(dir, "Cellar", "agent-deck", "1.2.3", "bin")
	if err := os.MkdirAll(versionedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	versioned := filepath.Join(versionedDir, "agent-deck")
	stable := filepath.Join(dir, "bin", "agent-deck")
	if err := os.MkdirAll(filepath.Dir(stable), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRuntimeHandoffTarget(t, versioned, "versioned-binary")
	if err := os.Symlink(versioned, stable); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	got, err := resolveLaunchPath(
		versioned,
		func(name string) (string, error) {
			switch name {
			case versioned:
				return versioned, nil
			case "agent-deck":
				return stable, nil
			default:
				return "", errors.New("not found")
			}
		},
		func() (string, error) { return versioned, nil },
	)
	if err != nil {
		t.Fatalf("resolve launch path: %v", err)
	}
	if got != stable {
		t.Fatalf("launch path = %q, want stable PATH alias %q", got, stable)
	}
}

func TestRuntimeHandoffDetectsInodeReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	body := "same-size-binary"
	writeRuntimeHandoffTarget(t, path, body)

	requested := make(chan struct{}, 1)
	var callbacks atomic.Int32
	h := newTestRuntimeHandoff(t, path, func() {
		callbacks.Add(1)
		requested <- struct{}{}
	})
	h.validate = func(context.Context, string) error { return nil }

	initialTime := h.initial.modTime
	replacement := filepath.Join(dir, "agent-deck.new")
	writeRuntimeHandoffTarget(t, replacement, body)
	if err := os.Chtimes(replacement, initialTime, initialTime); err != nil {
		t.Fatalf("set replacement timestamps: %v", err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("replace executable: %v", err)
	}

	runRuntimeHandoffWatch(t, h, requested)
	if !h.Requested() {
		t.Fatal("handoff was not requested after inode replacement")
	}
	if got := callbacks.Load(); got != 1 {
		t.Fatalf("callback count = %d, want 1", got)
	}
	if h.Request() {
		t.Fatal("second Request unexpectedly performed transition")
	}
	if got := callbacks.Load(); got != 1 {
		t.Fatalf("callback count after repeated Request = %d, want 1", got)
	}
}

func TestRuntimeHandoffDetectsSameInodeMetadataChange(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
		mtime  bool
	}{
		{name: "size", before: "old", after: "new-longer"},
		{name: "mtime", before: "old", after: "new", mtime: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "agent-deck")
			writeRuntimeHandoffTarget(t, path, tt.before)
			h := newTestRuntimeHandoff(t, path, nil)
			h.validate = func(context.Context, string) error { return nil }

			writeRuntimeHandoffTarget(t, path, tt.after)
			if tt.mtime {
				changedTime := h.initial.modTime.Add(2 * time.Second)
				if err := os.Chtimes(path, changedTime, changedTime); err != nil {
					t.Fatalf("change executable timestamp: %v", err)
				}
			}

			current, err := fingerprintExecutable(path)
			if err != nil {
				t.Fatalf("fingerprint changed target: %v", err)
			}
			if !os.SameFile(h.initial.info, current.info) {
				t.Fatal("test overwrite unexpectedly replaced the inode")
			}
			if !h.changedTargetIsValid(context.Background()) {
				t.Fatalf("same-inode %s change was not detected", tt.name)
			}
		})
	}
}

func TestRuntimeHandoffRetriesInvalidChangedTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "old")

	requested := make(chan struct{}, 1)
	h := newTestRuntimeHandoff(t, path, func() { requested <- struct{}{} })
	var validationCalls atomic.Int32
	var valid atomic.Bool
	h.validate = func(context.Context, string) error {
		validationCalls.Add(1)
		if !valid.Load() {
			return errors.New("invalid target")
		}
		return nil
	}
	writeRuntimeHandoffTarget(t, path, "new-and-different")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- h.Watch(ctx) }()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for validationCalls.Load() == 0 {
		select {
		case <-deadline.C:
			t.Fatal("changed target was never validated")
		case <-time.After(time.Millisecond):
		}
	}
	if h.Requested() {
		t.Fatal("invalid changed target requested a handoff")
	}

	valid.Store(true)
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatalf("valid target did not request handoff: %v", ctx.Err())
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}
	if validationCalls.Load() < 2 {
		t.Fatalf("validation calls = %d, want at least 2", validationCalls.Load())
	}
}

func TestValidateRuntimeHandoffTargetRunsVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script executable fixture is Unix-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = version ] || exit 2\nprintf 'Agent Deck vtest\\n'\n")

	if err := validateRuntimeHandoffTarget(context.Background(), path); err != nil {
		t.Fatalf("version validation failed: %v", err)
	}
}

func TestValidateRuntimeHandoffTargetRejectsNonAgentDeckExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script executable fixture is Unix-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "not-agent-deck")
	writeRuntimeHandoffTarget(t, path, "#!/bin/sh\nexit 0\n")
	if err := validateRuntimeHandoffTarget(context.Background(), path); err == nil {
		t.Fatal("non-Agent-Deck executable passed runtime validation")
	}
}

func TestValidateAgentDeckVersionOutput(t *testing.T) {
	for _, valid := range []string{
		"Agent Deck v1.10.11\n",
		"Agent Deck vbranch-main\n",
		"Agent Deck v1.10.11 (update available: v1.10.12)\n",
	} {
		if err := validateAgentDeckVersionOutput([]byte(valid)); err != nil {
			t.Errorf("valid output %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"",
		"true\n",
		"Agent Deck v\n",
		"warning\nAgent Deck v1.10.11\n",
	} {
		if err := validateAgentDeckVersionOutput([]byte(invalid)); err == nil {
			t.Errorf("invalid output %q accepted", invalid)
		}
	}
}

func TestRuntimeHandoffRequestCallbackExactlyOnceConcurrently(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "old")

	var callbacks atomic.Int32
	h := newTestRuntimeHandoff(t, path, func() { callbacks.Add(1) })
	var transitions atomic.Int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h.Request() {
				transitions.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := transitions.Load(); got != 1 {
		t.Fatalf("successful Request calls = %d, want 1", got)
	}
	if got := callbacks.Load(); got != 1 {
		t.Fatalf("callback count = %d, want 1", got)
	}
}

func TestRuntimeHandoffCancelPreventsRequestAndExec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "old")
	h := newTestRuntimeHandoff(t, path, nil)
	if !h.Cancel() {
		t.Fatal("initial Cancel returned false")
	}
	if h.Request() {
		t.Fatal("Request succeeded after Cancel")
	}
	if h.Requested() {
		t.Fatal("canceled handoff reports requested")
	}
	if err := h.Exec(); !errors.Is(err, ErrHandoffCanceled) {
		t.Fatalf("Exec error = %v, want ErrHandoffCanceled", err)
	}
}

func TestRuntimeHandoffExecWaitsForShutdownCallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "old")
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	h := newTestRuntimeHandoff(t, path, func() {
		close(callbackStarted)
		<-releaseCallback
	})
	h.validate = func(context.Context, string) error { return nil }
	execCalled := make(chan struct{})
	h.execProcess = func(string, []string, []string) error {
		close(execCalled)
		return errors.New("exec sentinel")
	}
	go h.Request()
	<-callbackStarted
	execDone := make(chan struct{})
	go func() {
		_ = h.Exec()
		close(execDone)
	}()
	select {
	case <-execCalled:
		t.Fatal("exec ran before shutdown callback completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseCallback)
	select {
	case <-execDone:
	case <-time.After(time.Second):
		t.Fatal("Exec did not resume after shutdown callback completed")
	}
}

func TestRuntimeHandoffExecRejectsTargetChangedDuringFinalValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "old")
	h := newTestRuntimeHandoff(t, path, nil)
	h.validate = func(context.Context, string) error {
		replacement := filepath.Join(dir, "replacement")
		writeRuntimeHandoffTarget(t, replacement, "new")
		return os.Rename(replacement, path)
	}
	execCalled := false
	h.execProcess = func(string, []string, []string) error {
		execCalled = true
		return nil
	}
	if !h.Request() {
		t.Fatal("initial Request returned false")
	}
	err := h.Exec()
	if !errors.Is(err, ErrHandoffTargetChanged) {
		t.Fatalf("Exec error = %v, want ErrHandoffTargetChanged", err)
	}
	if execCalled {
		t.Fatal("exec ran after target changed during final validation")
	}
}

func TestRuntimeHandoffCancelDuringFinalValidationPreventsExec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "old")
	h := newTestRuntimeHandoff(t, path, nil)
	validationStarted := make(chan struct{})
	releaseValidation := make(chan struct{})
	h.validate = func(context.Context, string) error {
		close(validationStarted)
		<-releaseValidation
		return nil
	}
	execCalled := false
	h.execProcess = func(string, []string, []string) error {
		execCalled = true
		return nil
	}
	if !h.Request() {
		t.Fatal("initial Request returned false")
	}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Exec() }()
	<-validationStarted
	if !h.Cancel() {
		t.Fatal("Cancel lost before handoff commit")
	}
	close(releaseValidation)
	if err := <-errCh; !errors.Is(err, ErrHandoffCanceled) {
		t.Fatalf("Exec error = %v, want ErrHandoffCanceled", err)
	}
	if execCalled {
		t.Fatal("exec ran after cancellation during final validation")
	}
}

func TestRuntimeHandoffExecPreservesArgsCurrentEnvAndCwd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "old")
	args := []string{path, "headless", "--listen", "127.0.0.1:7777"}
	h, err := NewRuntimeHandoff(RuntimeHandoffOptions{Args: args})
	if err != nil {
		t.Fatalf("NewRuntimeHandoff: %v", err)
	}
	args[1] = "mutated-after-construction"
	t.Setenv("AGENT_DECK_HANDOFF_TEST_ENV", "current-at-exec")
	workingDir := t.TempDir()
	t.Chdir(workingDir)

	var gotPath string
	var gotArgs, gotEnv []string
	var gotCwd string
	sentinel := errors.New("exec test sentinel")
	h.execProcess = func(path string, args, env []string) error {
		gotPath = path
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		gotCwd, _ = os.Getwd()
		return sentinel
	}
	h.validate = func(context.Context, string) error { return nil }
	if !h.Request() {
		t.Fatal("initial Request returned false")
	}
	if err := h.Exec(); !errors.Is(err, sentinel) {
		t.Fatalf("Exec error = %v, want sentinel", err)
	}

	if gotPath != path {
		t.Fatalf("exec path = %q, want %q", gotPath, path)
	}
	wantArgs := []string{path, "headless", "--listen", "127.0.0.1:7777"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("exec args = %#v, want %#v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("exec args = %#v, want %#v", gotArgs, wantArgs)
		}
	}
	if !containsEnv(gotEnv, "AGENT_DECK_HANDOFF_TEST_ENV=current-at-exec") {
		t.Fatalf("exec environment did not contain current marker")
	}
	if gotCwd != workingDir {
		t.Fatalf("exec cwd = %q, want %q", gotCwd, workingDir)
	}
}

func TestRuntimeHandoffExecRequiresRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-deck")
	writeRuntimeHandoffTarget(t, path, "old")
	h := newTestRuntimeHandoff(t, path, nil)
	called := false
	h.execProcess = func(string, []string, []string) error {
		called = true
		return nil
	}

	if err := h.Exec(); !errors.Is(err, ErrHandoffNotRequested) {
		t.Fatalf("Exec error = %v, want ErrHandoffNotRequested", err)
	}
	if called {
		t.Fatal("process exec was attempted before Request")
	}
}

func newTestRuntimeHandoff(t *testing.T, path string, onRequest func()) *RuntimeHandoff {
	t.Helper()
	h, err := NewRuntimeHandoff(RuntimeHandoffOptions{
		Args:      []string{path, "headless"},
		Interval:  5 * time.Millisecond,
		OnRequest: onRequest,
	})
	if err != nil {
		t.Fatalf("NewRuntimeHandoff: %v", err)
	}
	return h
}

func runRuntimeHandoffWatch(t *testing.T, h *RuntimeHandoff, requested <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- h.Watch(ctx) }()

	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatalf("handoff was not requested: %v", ctx.Err())
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}
}

func writeRuntimeHandoffTarget(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write runtime handoff target: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("make runtime handoff target executable: %v", err)
	}
}

func containsEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}
