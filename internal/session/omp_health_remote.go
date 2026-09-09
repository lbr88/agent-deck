package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// Query the execution filesystem without modifying it or starting a provider.
// Status polling starts this outside the Instance lock; stateless hub previews
// can await the same bounded result. No network I/O runs on list navigation.
func readOmpTargetHealth(id, host, container string) string {
	observation := readOmpTargetIdentityHealth(id, host, container)
	if observation.Warning != "" {
		return observation.Warning
	}
	if observation.Status != nil {
		if observation.Status.Error != "" {
			return tmux.StripANSI(observation.Status.Error)
		}
		if observation.Status.IdentityReady {
			return ""
		}
	}
	if observation.Generation != "" {
		return "OMP execution host has not acknowledged identity tracking; it may still be loading."
	}
	return "" // Existing untracked legacy or unused row.
}

func readOmpTargetIdentityHealth(id, host, container string) ompLaunchObservation {
	// Length-frame JSON instead of requiring a JSON CLI on execution hosts.
	// A missing status for a tracked generation is loading, not a fatal binding
	// error. A negative provider ACK is returned even if no binding was written.
	script := `unset AGENTDECK_OMP_SOURCE_BINDING AGENTDECK_OMP_SOURCE_ERROR AGENTDECK_OMP_LAUNCH_ID AGENTDECK_OMP_DIR; ` +
		`session_dir=` + ompAgentDeckSessionDirExpr(id) + `; current_generation=; status_size=0; status_error=; ` + `
if [ -L "$session_dir" ] || { [ -e "$session_dir" ] && [ ! -d "$session_dir" ]; }; then
  echo 'Invalid OMP execution-host session directory; history preserved' >&2; exit 1
fi
if [ -e "$session_dir/.agent-deck-launch-generation" ] || [ -L "$session_dir/.agent-deck-launch-generation" ]; then
  generation_file="$session_dir/.agent-deck-launch-generation"
  [ -f "$generation_file" ] && [ ! -L "$generation_file" ] || { echo 'Invalid OMP launch generation' >&2; exit 1; }
  generation_size=$(wc -c < "$generation_file")
  [ "$generation_size" -le 16384 ] || { echo 'Invalid OMP launch generation' >&2; exit 1; }
  generation_snapshot=$(head -c 16385 "$generation_file"; printf '.') 2>/dev/null
  [ "$(printf '%s' "$generation_snapshot" | LC_ALL=C wc -c)" -eq "$((generation_size+1))" ] || { echo 'Invalid OMP launch generation bytes' >&2; exit 1; }
  # The sentinel preserves file newlines above. This command substitution drops
  # trailing newlines, so size-1 below requires exactly one final LF, not zero/two.
  # TestOmpExecutionHostIdentityACK executes this script for SSH and Docker.
  current_generation=$(printf '%s' "$generation_snapshot" | head -c "$generation_size")
  [ "$(printf '%s' "$current_generation" | LC_ALL=C wc -c)" -eq "$((generation_size-1))" ] || { echo 'Invalid OMP launch generation framing' >&2; exit 1; }
  case "$current_generation" in ""|*[!A-Za-z0-9._-]*) echo 'Invalid OMP launch generation' >&2; exit 1;; esac
  status_file="$session_dir/.agent-deck-omp-status.$current_generation.json"
  if [ -e "$status_file" ] || [ -L "$status_file" ]; then
    if [ -f "$status_file" ] && [ ! -L "$status_file" ] && status_size=$(wc -c < "$status_file") && [ "$status_size" -le 16384 ]; then
      [ "$status_size" -gt 0 ] || status_error='Empty OMP status metadata'
    else
      status_size=0
      status_error='Invalid OMP status metadata'
    fi
  fi
fi
printf '%s\n%s\n' "$current_generation" "$status_size"
if [ "$status_size" -gt 0 ]; then head -c "$status_size" "$status_file"; fi
printf '\n'
if [ -n "$status_error" ]; then printf '1\n%s' "$status_error"; exit 0; fi
if [ -n "$current_generation" ] && [ "$status_size" -eq 0 ]; then printf '0\n'; exit 0; fi
binding_output=$(
  exec 2>&1
  source_file=; root_count=0
  for candidate in "$session_dir"/*.jsonl; do
    if [ -f "$candidate" ]; then source_file=$candidate; root_count=$((root_count+1)); fi
  done
` + ompBindingSelectionShell() + `
  if [ "$root_count" -gt 1 ]; then
    echo "OMP has unbound histories; active conversation cannot be determined safely. Preserved candidates:" >&2
    candidate_count=0
    for candidate in "$session_dir"/*.jsonl; do
      if [ -f "$candidate" ]; then printf ' - %s\n' "$candidate" >&2; candidate_count=$((candidate_count+1)); fi
      [ "$candidate_count" -lt 20 ] || break
    done
    exit 1
  fi
  if [ -n "$current_generation" ]; then
    printf '%s\n%s\n%s\n' "$bound_generation" "$bound_id" "$bound_file"
  fi
)
binding_status=$?
printf '%s\n' "$binding_status"
printf '%s' "$binding_output" | head -c 8192
`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var output []byte
	var err error
	if host != "" {
		output, err = (&SSHRunner{Host: host}).remoteExec(ctx, "bash -c "+shellescape.Quote(script), nil)
	} else {
		// #nosec G204 -- fixed docker binary/subcommand; container is separate
		// argv, and the generated script shell-quotes the managed row ID.
		output, err = exec.CommandContext(ctx, "docker", "exec", container, "bash", "-c", script).CombinedOutput()
	}
	if err != nil {
		warning := strings.TrimSpace(tmux.StripANSI(fmt.Sprintf("OMP target identity check failed: %v\n%s", err, output)))
		if len(warning) > 8192 {
			warning = warning[:8192]
		}
		return ompLaunchObservation{Warning: warning}
	}
	return parseOmpTargetIdentityHealth(id, string(output))
}

func parseOmpTargetIdentityHealth(id, output string) ompLaunchObservation {
	var generation string
	invalid := func() ompLaunchObservation {
		observed := ""
		if ompSafeGeneration(generation) {
			observed = generation
		}
		return ompLaunchObservation{Generation: observed, Warning: "OMP execution host returned invalid identity status; history is preserved."}
	}
	generation, rest, ok := strings.Cut(output, "\n")
	if !ok || (generation != "" && !ompSafeGeneration(generation)) {
		return invalid()
	}
	sizeText, rest, ok := strings.Cut(rest, "\n")
	size, err := strconv.Atoi(strings.TrimSpace(sizeText))
	if !ok || err != nil || size < 0 || size > ompBindingLimit || len(rest) <= size || rest[size] != '\n' {
		return invalid()
	}
	statusJSON, rest := rest[:size], rest[size+1:]
	bindingResult, bindingData, ok := strings.Cut(rest, "\n")
	if !ok || (bindingResult != "0" && bindingResult != "1") || len(bindingData) > 8192 {
		return invalid()
	}
	observation := ompLaunchObservation{Generation: generation}
	if size > 0 {
		var status ompIdentityStatus
		if generation == "" || json.Unmarshal([]byte(statusJSON), &status) != nil || status.InstanceID != id || status.LaunchID != generation {
			observation.Warning = invalid().Warning
			return observation
		}
		observation.Status = &status
		if !status.IdentityReady {
			return observation // The provider's own failure is authoritative.
		}
		fields := strings.Split(bindingData, "\n")
		if bindingResult == "0" && (len(fields) != 3 || fields[0] != generation || fields[1] != status.SessionID || fields[2] != status.SessionFile) {
			observation.Warning = "OMP identity status does not match its active binding; history is preserved."
			return observation
		}
	}
	if bindingResult != "0" {
		observation.Warning = strings.TrimSpace(tmux.StripANSI(bindingData))
		if observation.Warning == "" {
			observation.Warning = "OMP execution-host binding validation failed; history is preserved."
		}
	}
	return observation
}
