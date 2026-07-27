package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/childenv"
)

const (
	// DefaultRuntimeHandoffInterval is the default interval between checks for
	// an in-place replacement of the running executable.
	DefaultRuntimeHandoffInterval = time.Second

	runtimeHandoffValidationTimeout = 10 * time.Second
)

var (
	// ErrHandoffNotRequested is returned when Exec is called before a handoff
	// has been requested.
	ErrHandoffNotRequested = errors.New("runtime handoff has not been requested")

	// ErrExecUnsupported is returned on platforms without same-process exec
	// support.
	ErrExecUnsupported = errors.New("same-process exec is unsupported")

	// ErrHandoffTargetChanged is returned when the replacement changes while
	// it is being validated for the final exec.
	ErrHandoffTargetChanged = errors.New("runtime handoff target changed during validation")

	// ErrHandoffCanceled is returned when an explicit stop wins a race with an
	// update request. The process must remain stopped rather than re-exec.
	ErrHandoffCanceled = errors.New("runtime handoff was canceled")
)

const (
	runtimeHandoffIdle uint32 = iota
	runtimeHandoffRequested
	runtimeHandoffCanceled
	runtimeHandoffCommitted
)

// RuntimeHandoffOptions configures a RuntimeHandoff.
type RuntimeHandoffOptions struct {
	// Interval controls how often Watch checks the launch path. Values at or
	// below zero use DefaultRuntimeHandoffInterval.
	Interval time.Duration

	// OnRequest is called when Request first transitions the handoff to its
	// requested state. It is never called more than once.
	OnRequest func()

	// Args is the argument vector to preserve across Exec. It defaults to
	// os.Args. Args[0] is used to resolve—and then normalized to—the stable
	// launch path; all command arguments after it are preserved exactly.
	Args []string
}

// RuntimeHandoff watches the stable path used to launch this process and can
// replace the current process with a newly installed executable at that path.
// It owns no package-global state and is safe for concurrent use.
type RuntimeHandoff struct {
	path         string
	args         []string
	interval     time.Duration
	onRequest    func()
	initial      executableFingerprint
	state        atomic.Uint32
	callbackDone chan struct{}
	validate     func(context.Context, string) error
	execProcess  func(string, []string, []string) error
	environ      func() []string
}

type executableFingerprint struct {
	info    os.FileInfo
	size    int64
	modTime time.Time
}

// NewRuntimeHandoff captures the launch path's current fingerprint. The
// returned handoff detects a later replacement even when the process itself is
// still running from the old, now-unlinked inode.
func NewRuntimeHandoff(options RuntimeHandoffOptions) (*RuntimeHandoff, error) {
	if !RuntimeHandoffSupported() {
		return nil, ErrExecUnsupported
	}
	args := options.Args
	if len(args) == 0 {
		args = os.Args
	}
	args = append([]string(nil), args...)

	argv0 := ""
	if len(args) > 0 {
		argv0 = args[0]
	}
	path, err := ResolveLaunchPath(argv0)
	if err != nil {
		return nil, err
	}

	initial, err := fingerprintExecutable(path)
	if err != nil {
		return nil, fmt.Errorf("fingerprint launch executable %q: %w", path, err)
	}

	interval := options.Interval
	if interval <= 0 {
		interval = DefaultRuntimeHandoffInterval
	}

	return &RuntimeHandoff{
		path:         path,
		args:         args,
		interval:     interval,
		onRequest:    options.OnRequest,
		initial:      initial,
		callbackDone: make(chan struct{}),
		validate:     validateRuntimeHandoffTarget,
		execProcess:  replaceCurrentProcess,
		environ:      childenv.ForReexec,
	}, nil
}

// ResolveLaunchPath resolves argv0 to the stable path by which the program was
// launched. Looking up argv0 first is important for installs where that path is
// a stable symlink while os.Executable points at a versioned target.
func ResolveLaunchPath(argv0 string) (string, error) {
	return resolveLaunchPath(argv0, exec.LookPath, os.Executable)
}

func resolveLaunchPath(
	argv0 string,
	lookPath func(string) (string, error),
	executable func() (string, error),
) (string, error) {
	argv0 = strings.TrimSpace(argv0)
	var lookupErr error
	if argv0 != "" {
		path, err := lookPath(argv0)
		if err == nil || errors.Is(err, exec.ErrDot) {
			if strings.TrimSpace(path) != "" {
				resolved, absErr := absoluteLaunchPath(path)
				if absErr != nil {
					return "", absErr
				}
				// Old service definitions may launch an absolute, versioned
				// Homebrew Cellar path. Prefer a PATH alias only when it points
				// at this exact file now; that keeps future upgrades observable
				// without ever switching to an unrelated agent-deck binary.
				if strings.ContainsRune(argv0, os.PathSeparator) {
					if alias := sameExecutablePATHAlias(filepath.Base(argv0), resolved, lookPath); alias != "" {
						return alias, nil
					}
				}
				return resolved, nil
			}
		}
		lookupErr = err
	}

	path, err := executable()
	if err != nil {
		if lookupErr != nil {
			return "", fmt.Errorf("resolve launch path from argv0 %q (%v) or current executable: %w", argv0, lookupErr, err)
		}
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("resolve current executable: empty path")
	}
	return absoluteLaunchPath(path)
}

func sameExecutablePATHAlias(name, resolved string, lookPath func(string) (string, error)) string {
	if strings.TrimSpace(name) == "" || name == "." || name == string(os.PathSeparator) {
		return ""
	}
	alias, err := lookPath(name)
	if err != nil && !errors.Is(err, exec.ErrDot) {
		return ""
	}
	alias, err = absoluteLaunchPath(alias)
	if err != nil || alias == resolved {
		return ""
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		return ""
	}
	aliasInfo, err := os.Stat(alias)
	if err != nil || !os.SameFile(resolvedInfo, aliasInfo) {
		return ""
	}
	return alias
}

func absoluteLaunchPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make launch path %q absolute: %w", path, err)
	}
	// Deliberately do not evaluate symlinks: the symlink is often the stable
	// install path that future updates replace or retarget.
	return filepath.Clean(abs), nil
}

func fingerprintExecutable(path string) (executableFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return executableFingerprint{}, err
	}
	if !info.Mode().IsRegular() {
		return executableFingerprint{}, fmt.Errorf("not a regular file")
	}
	return executableFingerprint{
		info:    info,
		size:    info.Size(),
		modTime: info.ModTime(),
	}, nil
}

func (f executableFingerprint) changed(other executableFingerprint) bool {
	return !os.SameFile(f.info, other.info) || f.size != other.size || !f.modTime.Equal(other.modTime)
}

func (f executableFingerprint) equal(other executableFingerprint) bool {
	return os.SameFile(f.info, other.info) && f.size == other.size && f.modTime.Equal(other.modTime)
}

// Watch checks for an in-place executable update until one is validated, the
// handoff is otherwise requested, or ctx is canceled. Transient missing or
// invalid targets are ignored and checked again on the next interval.
func (h *RuntimeHandoff) Watch(ctx context.Context) error {
	if h == nil {
		return fmt.Errorf("watch runtime handoff: nil handoff")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if h.Requested() {
		return nil
	}

	if h.changedTargetIsValid(ctx) {
		h.Request()
		return nil
	}

	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if h.Requested() {
				return nil
			}
			if h.changedTargetIsValid(ctx) {
				h.Request()
				return nil
			}
		}
	}
}

func (h *RuntimeHandoff) changedTargetIsValid(ctx context.Context) bool {
	candidate, err := fingerprintExecutable(h.path)
	if err != nil || !h.initial.changed(candidate) {
		return false
	}
	return h.validateStableTarget(ctx, candidate) == nil
}

func (h *RuntimeHandoff) validateStableTarget(ctx context.Context, candidate executableFingerprint) error {
	if err := h.validate(ctx, h.path); err != nil {
		return err
	}
	// Ensure the path did not change again while its version command ran. A
	// racing update is retried rather than handing off to an unvalidated file.
	confirmed, err := fingerprintExecutable(h.path)
	if err != nil {
		return err
	}
	if !candidate.equal(confirmed) {
		return ErrHandoffTargetChanged
	}
	return nil
}

func validateRuntimeHandoffTarget(ctx context.Context, path string) error {
	validationCtx, cancel := context.WithTimeout(ctx, runtimeHandoffValidationTimeout)
	defer cancel()
	output, err := exec.CommandContext(validationCtx, path, "version").Output()
	if err != nil {
		return err
	}
	return validateAgentDeckVersionOutput(output)
}

func validateAgentDeckVersionOutput(output []byte) error {
	line := strings.TrimSpace(string(output))
	if strings.ContainsAny(line, "\r\n") || !strings.HasPrefix(line, "Agent Deck v") {
		return fmt.Errorf("unexpected `version` output %q", line)
	}
	version := strings.TrimSpace(strings.TrimPrefix(line, "Agent Deck v"))
	if idx := strings.IndexByte(version, ' '); idx >= 0 {
		version = version[:idx]
	}
	if version == "" {
		return fmt.Errorf("unexpected empty version in output %q", line)
	}
	return nil
}

// Request marks the handoff requested and invokes OnRequest on the first call.
// It returns true only for the call that performed the transition.
func (h *RuntimeHandoff) Request() bool {
	if h == nil || !h.state.CompareAndSwap(runtimeHandoffIdle, runtimeHandoffRequested) {
		return false
	}
	defer close(h.callbackDone)
	if h.onRequest != nil {
		h.onRequest()
	}
	return true
}

// Requested reports whether this handoff has been requested.
func (h *RuntimeHandoff) Requested() bool {
	return h != nil && h.state.Load() == runtimeHandoffRequested
}

// Cancel prevents a pending or future handoff. It is used when an explicit
// operator/service stop wins a race with replacement detection.
func (h *RuntimeHandoff) Cancel() bool {
	if h == nil {
		return false
	}
	for {
		state := h.state.Load()
		if state == runtimeHandoffCanceled || state == runtimeHandoffCommitted {
			return false
		}
		if h.state.CompareAndSwap(state, runtimeHandoffCanceled) {
			return true
		}
	}
}

// Path returns the stable executable path watched and used by Exec.
func (h *RuntimeHandoff) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// Exec replaces the current process with the executable at Path, preserving
// command arguments, the environment current at the time of this call, and the
// current working directory. argv0 is normalized to Path. On Unix a successful
// call never returns and retains the same PID.
func (h *RuntimeHandoff) Exec() error {
	if h == nil {
		return fmt.Errorf("exec runtime handoff: nil handoff")
	}
	state := h.state.Load()
	if state == runtimeHandoffCanceled {
		return ErrHandoffCanceled
	}
	if state != runtimeHandoffRequested {
		return ErrHandoffNotRequested
	}
	// Request invokes lifecycle cleanup synchronously on the watcher goroutine.
	// Foreground server loops can return as soon as their listener closes, so
	// join that callback before replacing the process image.
	<-h.callbackDone
	if h.state.Load() == runtimeHandoffCanceled {
		return ErrHandoffCanceled
	}
	candidate, err := fingerprintExecutable(h.path)
	if err != nil {
		return fmt.Errorf("fingerprint runtime handoff target before exec: %w", err)
	}
	if err := h.validateStableTarget(context.Background(), candidate); err != nil {
		return fmt.Errorf("validate runtime handoff target before exec: %w", err)
	}
	// This CAS is the final stop-vs-update arbitration point. Once committed,
	// Cancel deliberately loses; before it, an explicit stop prevents exec even
	// if it arrived while the replacement's version command was running.
	if !h.state.CompareAndSwap(runtimeHandoffRequested, runtimeHandoffCommitted) {
		if h.state.Load() == runtimeHandoffCanceled {
			return ErrHandoffCanceled
		}
		return fmt.Errorf("commit runtime handoff from unexpected state %d", h.state.Load())
	}
	args := append([]string(nil), h.args...)
	if len(args) == 0 {
		args = []string{h.path}
	} else {
		// Pin argv0 to the stable launch path. This matters after migrating an
		// old service that originally used a versioned Homebrew Cellar path:
		// the next process must keep watching the stable alias too.
		args[0] = h.path
	}
	return h.execProcess(h.path, args, h.environ())
}

// RuntimeHandoffSupported reports whether this platform can replace the
// current process while retaining its PID.
func RuntimeHandoffSupported() bool { return runtimeHandoffSupported() }
