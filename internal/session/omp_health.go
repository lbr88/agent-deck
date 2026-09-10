package session

import (
	"os"
	"path/filepath"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

type ompIdentityStatus struct {
	InstanceID    string `json:"instance_id"`
	LaunchID      string `json:"launch_id"`
	SessionID     string `json:"session_id"`
	SessionFile   string `json:"session_file"`
	IdentityReady bool   `json:"identity_ready"`
	Error         string `json:"error"`
}

func readOmpHealthFile(path string) ([]byte, error) {
	return readOmpBoundedRegularFile(path, ompBindingLimit)
}

// Called by the background metadata poll, BEFORE status-specific fast returns.
// No transcript bodies, subprocesses or writes run on the navigation/render
// path. Hub sessions get this metadata from their owning node, not the relay.
func (i *Instance) refreshOmpMetadataLocked() {
	if i.Tool != "omp" {
		i.ompIdentityWarning = ""
		return
	}
	if i.ompMetadataPending != nil || time.Since(i.ompMetadataCheckedAt) < 2*time.Second {
		return
	}
	i.ompMetadataCheckedAt = time.Now()
	i.ompIdentityWarning = ""
	if !i.ompIdentityAckRequired() {
		return
	}
	if i.SSHHost != "" || i.IsSandboxed() {
		if i.SSHHost == "" && i.SandboxContainer == "" {
			return
		}
		done := make(chan struct{})
		i.ompMetadataPending = done
		i.ompIdentityWarning = "Checking OMP identity on its execution host."
		id, tool, host, container, sandboxed := i.ID, i.Tool, i.SSHHost, i.SandboxContainer, i.IsSandboxed()
		go func() {
			warning := readOmpTargetHealth(id, host, container)
			i.mu.Lock()
			if i.ompMetadataPending == done {
				i.ompMetadataPending = nil
				if i.Tool == tool && i.SSHHost == host && i.SandboxContainer == container && i.IsSandboxed() == sandboxed {
					i.ompIdentityWarning = warning
				} else {
					i.ompIdentityWarning = ""
					i.ompMetadataCheckedAt = time.Time{}
				}
			}
			i.mu.Unlock()
			close(done)
		}()
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		i.ompIdentityWarning = err.Error()
		return
	}
	dir := filepath.Join(home, ".omp", "agent-deck", i.ID)
	// Use the same exact generation/status/binding comparison as launch.
	// A valid binding alone is insufficient if the running provider reports a
	// different conversation; this must be visible before the next restart.
	observation := i.observeOmpLaunchIdentity()
	if observation.Warning != "" {
		i.ompIdentityWarning = tmux.StripANSI(observation.Warning)
		return
	}
	if observation.Generation != "" {
		status := observation.Status
		if status == nil {
			i.ompIdentityWarning = "OMP has not acknowledged identity tracking for this launch. It may still be loading; do not fork until tracking is ready."
			return
		}
		if status.Error != "" {
			i.ompIdentityWarning = tmux.StripANSI(status.Error)
			return
		}
		if !status.IdentityReady {
			i.ompIdentityWarning = "OMP identity tracking is unavailable; history is preserved."
		}
		return // Shared observer already validated the tracked active binding.
	}
	_, err = resolveOmpActiveBinding(dir)
	if err != nil {
		i.ompIdentityWarning = tmux.StripANSI(err.Error())
	}
}

func (i *Instance) ompHealthPreview() string {
	i.mu.Lock()
	// Hub preview actions load a fresh Instance without a preceding status
	// poll. Validate here too, on the existing background preview I/O path.
	i.refreshOmpMetadataLocked()
	if i.Tool != "omp" {
		i.mu.Unlock()
		return ""
	}
	pending := i.ompMetadataPending
	i.mu.Unlock()
	if pending != nil {
		// This is the asynchronous preview worker, never Update/View or the
		// navigation lock. The target command itself has a three-second deadline.
		select {
		case <-pending:
		case <-time.After(3 * time.Second):
		}
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.Tool != "omp" || i.ompIdentityWarning == "" {
		return ""
	}
	return "OMP session needs attention\n" + i.ompIdentityWarning + "\n\n"
}
