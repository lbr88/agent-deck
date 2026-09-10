package session

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/asheshgoplani/agent-deck/internal/safeio"
	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
	"github.com/google/uuid"
)

//go:embed omp/identity.mjs
var ompIdentityExtension string

const ompIdentityArgs = ` --extension "$AGENTDECK_OMP_DIR/.agent-deck-identity.mjs"`

const (
	ompLaunchMetaPrefix = `: AGENTDECK_OMP_LAUNCH_META_V1_`
	ompLaunchMetaSuffix = `; `
)

// Install on the execution host, not the controller. SSH and sandbox launch
// wrappers therefore receive the same tracker, generation and authoritative
// title as local sessions. No Node installation is needed: OMP loads the JS.
func (i *Instance) ompIdentityLaunchSetup(ackRequired bool) string {
	generation := uuid.NewString()
	if !ackRequired {
		// Non-TUI OMP modes do not load the identity extension and cannot
		// acknowledge a launch generation. Keep only the metadata frame needed
		// by the host-side launch stager, and clear any tracking environment a
		// nested Agent Deck inherited before target-side binding selection.
		return fmt.Sprintf(ompLaunchMetaPrefix+`%s_0`+ompLaunchMetaSuffix+
			`unset AGENTDECK_OMP_DIR AGENTDECK_OMP_LAUNCH_ID AGENTDECK_OMP_SOURCE_BINDING AGENTDECK_OMP_SOURCE_ERROR; `,
			generation)
	}
	title, _ := json.Marshal(struct {
		Title string `json:"title"`
	}{i.GetTitleThreadSafe()})
	quote := shellescape.Quote
	return fmt.Sprintf(ompLaunchMetaPrefix+`%s_%s`+ompLaunchMetaSuffix+`session_dir=%s; managed_root=${session_dir%%/*}; omp_root=${managed_root%%/*}; `+
		`if [ -L "$omp_root" ] || { [ -e "$omp_root" ] && [ ! -d "$omp_root" ]; }; then echo 'Invalid OMP data root; history preserved' >&2; exit 1; fi; `+
		`if [ -L "$managed_root" ] || { [ -e "$managed_root" ] && [ ! -d "$managed_root" ]; }; then echo 'Invalid OMP managed session root; history preserved' >&2; exit 1; fi; `+
		`mkdir -p "$managed_root" || exit 1; `+
		`if [ -L "$managed_root" ] || [ ! -d "$managed_root" ]; then echo 'Invalid OMP managed session root; history preserved' >&2; exit 1; fi; `+
		`if [ -L "$session_dir" ] || { [ -e "$session_dir" ] && [ ! -d "$session_dir" ]; }; then echo 'Invalid OMP session row; history preserved' >&2; exit 1; fi; `+
		`if [ ! -e "$session_dir" ]; then mkdir "$session_dir" || exit 1; fi; `+
		`real_managed_root=$(CDPATH= cd -P -- "$managed_root" 2>/dev/null && pwd -P) || exit 1; real_session_dir=$(CDPATH= cd -P -- "$session_dir" 2>/dev/null && pwd -P) || exit 1; `+
		`if [ "$real_session_dir" != "$real_managed_root/${session_dir##*/}" ]; then echo 'OMP session row crosses another managed entry; history preserved' >&2; exit 1; fi; `+
		`source_binding="$session_dir/.agent-deck-source-binding.%s"; previous_binding="$session_dir/.agent-deck-active-session"; source_error=; `+
		`generation_file="$session_dir/.agent-deck-launch-generation"; if [ -e "$generation_file" ] || [ -L "$generation_file" ]; then `+
		`if [ ! -f "$generation_file" ] || [ -L "$generation_file" ] || [ "$(wc -c < "$generation_file")" -gt 16384 ]; then echo 'Invalid OMP launch generation; history preserved' >&2; exit 1; fi; `+
		`generation_size=$(wc -c < "$generation_file") || exit 1; generation_snapshot=$(LC_ALL=C dd if="$generation_file" bs=16385 count=1 2>/dev/null; printf '.') 2>/dev/null; generation_read_size=$(printf '%%s' "$generation_snapshot" | LC_ALL=C wc -c); `+
		`if [ "$generation_read_size" -ne "$((generation_size + 1))" ]; then echo 'Invalid OMP launch generation bytes; history preserved' >&2; exit 1; fi; generation_snapshot=${generation_snapshot%%.}; case "$generation_snapshot" in *"$(printf '\r')"*) echo 'Invalid OMP launch generation bytes; history preserved' >&2; exit 1;; esac; `+
		`exec 8< <(printf '%%s' "$generation_snapshot") || exit 1; IFS= read -r previous_generation <&8 || { exec 8<&-; echo 'Invalid OMP launch generation; history preserved' >&2; exit 1; }; if IFS= read -r generation_extra <&8 || [ -n "$generation_extra" ]; then exec 8<&-; echo 'Invalid OMP launch generation; history preserved' >&2; exit 1; fi; exec 8<&-; `+
		`[ "${#previous_generation}" -le 128 ] || { echo 'Invalid OMP launch generation; history preserved' >&2; exit 1; }; case "$previous_generation" in [0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz]*) ;; *) echo 'Invalid OMP launch generation; history preserved' >&2; exit 1;; esac; case "$previous_generation" in *[!0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz._-]*) echo 'Invalid OMP launch generation; history preserved' >&2; exit 1;; esac; `+
		`previous_binding="$previous_binding.$previous_generation"; if [ ! -f "$previous_binding" ]; then source_error='Previous OMP launch never acknowledged identity tracking; history preserved.'; fi; fi; `+
		`if [ -e "$previous_binding" ] || [ -L "$previous_binding" ]; then if [ ! -f "$previous_binding" ] || [ -L "$previous_binding" ] || [ "$(wc -c < "$previous_binding")" -gt 16384 ] || [ -e "$source_binding" ] || [ -L "$source_binding" ]; then echo 'Invalid OMP source binding; history preserved' >&2; exit 1; fi; (umask 077; cp "$previous_binding" "$source_binding") || exit 1; fi; `+
		`( umask 077; printf '%%s' %s > "$session_dir/.agent-deck-identity.mjs.$$" && mv -f "$session_dir/.agent-deck-identity.mjs.$$" "$session_dir/.agent-deck-identity.mjs" && `+
		`printf '%%s' %s > "$session_dir/.agent-deck-title.json.$$" && mv -f "$session_dir/.agent-deck-title.json.$$" "$session_dir/.agent-deck-title.json" && `+
		`printf '%%s\n' %s > "$session_dir/.agent-deck-launch-generation.$$" && mv -f "$session_dir/.agent-deck-launch-generation.$$" "$session_dir/.agent-deck-launch-generation" ) || { echo 'Failed to install OMP identity tracking; history preserved' >&2; exit 1; }; `+
		`export AGENTDECK_OMP_DIR="$session_dir" AGENTDECK_OMP_LAUNCH_ID=%s AGENTDECK_OMP_SOURCE_BINDING="$source_binding" AGENTDECK_OMP_SOURCE_ERROR="$source_error"; `,
		generation, "1", ompAgentDeckSessionDirExpr(i.ID), generation, quote(ompIdentityExtension), quote(string(title)), quote(generation), quote(generation))
}

func (i *Instance) ompIdentityAckRequired() bool {
	return !i.resolvedOmpOptions().NoSession && !ompCommandUsesNonTUI(i.Command)
}

func ompCommandUsesNonTUI(command string) bool {
	fields, ok := ompShellWords(command)
	if !ok {
		return false
	}
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if field == "--" {
			return false
		}
		if field == "-p" || field == "--print" {
			return true
		}
		if strings.HasPrefix(field, "--mode=") && ompNonTUIMode(strings.TrimPrefix(field, "--mode=")) {
			return true
		}
		if field == "--mode" && index+1 < len(fields) {
			if ompNonTUIMode(fields[index+1]) {
				return true
			}
			index++
			continue
		}
		if ompFlagConsumesRequiredValue(field) && index+1 < len(fields) {
			index++
		}
	}
	return false
}

// Pinned to OMP v18.1.15's STRING_SETTERS plus its profile bootstrap flags.
// These flags consume the next argv even when it looks like another flag, so
// that value must never be reinterpreted as --print by Agent Deck.
func ompFlagConsumesRequiredValue(flag string) bool {
	switch flag {
	case "--cwd", "--config", "--add-dir", "--fork", "--provider", "--model",
		"--smol", "--slow", "--plan", "--prewalk-into", "--plan-yolo-into",
		"--max-time", "--service-tier", "--api-key", "--system-prompt",
		"--append-system-prompt", "--provider-session-id", "--prompt-cache-key",
		"--session-dir", "--models", "--tools", "--thinking", "--export",
		"--hook", "--extension", "-e", "--trusted-extension", "--plugin-dir",
		"--skills", "--approval-mode", "--profile", "--alias":
		return true
	default:
		return false
	}
}

// ompShellWords is a deliberately strict POSIX-shell argv tokenizer for the
// configured OMP command. It recognizes quoting and escaping so flag-looking
// prose inside one prompt argument cannot disable tracking. Complex shell
// control syntax stops classification and therefore fails safe to TUI ACK.
func ompShellWords(command string) ([]string, bool) {
	var fields []string
	var word strings.Builder
	quote := byte(0)
	escaped := false
	started := false
	flush := func() {
		if started {
			fields = append(fields, word.String())
			word.Reset()
			started = false
		}
	}
	for index := 0; index < len(command); index++ {
		char := command[index]
		if escaped {
			word.WriteByte(char)
			started = true
			escaped = false
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
				started = true
				continue
			}
			if char == '\\' && quote == '"' {
				if index+1 < len(command) && strings.ContainsRune("$`\"\\\n", rune(command[index+1])) {
					escaped = true
					continue
				}
				word.WriteByte(char)
				started = true
				continue
			}
			if quote == '"' && (char == '$' || char == '`') {
				return nil, false
			}
			word.WriteByte(char)
			started = true
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
			started = true
		case '\\':
			escaped = true
			started = true
		case ' ', '\t', '\r':
			flush()
		case '\n', ';', '|', '&', '<', '>', '#', '$', '`', '(', ')', '{', '}', '[', ']', '*', '?', '~':
			flush()
			return nil, false
		default:
			word.WriteByte(char)
			started = true
		}
	}
	if quote != 0 || escaped {
		return nil, false
	}
	flush()
	return fields, true
}

func ompNonTUIMode(mode string) bool {
	switch mode {
	case "text", "json", "rpc", "acp", "rpc-ui":
		return true
	default:
		return false
	}
}

func (i *Instance) prepareOmpLaunchGeneration(command string) (launch, generation string, ackRequired bool, err error) {
	template, ackRequired, found, err := parseOmpLaunchMetadata(command)
	if err != nil {
		return "", "", false, err
	}
	if !found && ompCommandUsesNonTUI(command) {
		return command, "", false, nil
	}
	if !found {
		return "", "", false, fmt.Errorf("OMP launch command has no identity-generation template; history is preserved")
	}
	generation = uuid.NewString()
	launch = strings.ReplaceAll(command, template, generation)
	if launch == command {
		return "", "", false, fmt.Errorf("prepare OMP launch generation: template was not replaced")
	}
	return launch, generation, ackRequired, nil
}

func parseOmpLaunchMetadata(command string) (template string, ackRequired, found bool, err error) {
	remainder := command
	for {
		start := strings.Index(remainder, ompLaunchMetaPrefix)
		if start < 0 {
			break
		}
		if found {
			return "", false, false, fmt.Errorf("OMP launch command has ambiguous identity metadata; history is preserved")
		}
		payloadStart := start + len(ompLaunchMetaPrefix)
		end := strings.Index(remainder[payloadStart:], ompLaunchMetaSuffix)
		if end < 0 {
			return "", false, false, fmt.Errorf("OMP launch command has malformed identity metadata; history is preserved")
		}
		payload := remainder[payloadStart : payloadStart+end]
		parts := strings.Split(payload, "_")
		if len(parts) != 2 || !ompSafeGeneration(parts[0]) || (parts[1] != "0" && parts[1] != "1") {
			return "", false, false, fmt.Errorf("OMP launch command has invalid identity metadata; history is preserved")
		}
		template, ackRequired, found = parts[0], parts[1] == "1", true
		remainder = remainder[payloadStart+end+len(ompLaunchMetaSuffix):]
	}
	return template, ackRequired, found, nil
}

// tmux's IPC command buffer is smaller than a bundled extension. Stage the
// complete, already-wrapped launch on the tmux host; the provider-side setup
// still executes inside SSH/docker. The opened script removes itself before
// running, and a failed tmux spawn is cleaned by the lifecycle caller.
func stageOmpLaunch(command string) (string, string, error) {
	root, err := GetAgentDeckDir()
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(root, "launch-scripts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	file := filepath.Join(dir, "omp-"+uuid.NewString()+".sh")
	script := "#!/bin/bash\nrm -f -- " + shellescape.Quote(file) + "\n" + command + "\n"
	if err := safeio.SafeOverwrite(file, []byte(script), safeio.Options{Perm: 0o600, SkipBackup: true}); err != nil {
		return "", "", err
	}
	return "bash " + shellescape.Quote(file), file, nil
}

func (i *Instance) startTmuxWithOmpTracking(command string) error {
	if i.Tool != "omp" {
		return i.tmuxSession.Start(command)
	}
	if i.resolvedOmpOptions().NoSession {
		return i.tmuxSession.Start(command)
	}
	command, generation, ackRequired, err := i.prepareOmpLaunchGeneration(command)
	if err != nil {
		return err
	}
	launch, file, err := stageOmpLaunch(command)
	if err != nil {
		return fmt.Errorf("prepare OMP launch script: %w", err)
	}
	err = i.tmuxSession.Start(launch)
	if err == nil && ackRequired {
		err = i.waitForOmpIdentityAck(generation, send.DefaultAgentReadyTimeout)
	}
	if err != nil {
		_ = os.Remove(file)
	}
	return err
}

type ompLaunchObservation struct {
	Generation string
	Status     *ompIdentityStatus
	Warning    string
}

// A spawn stamp means another caller completed a launch while this caller was
// waiting for the per-instance lock. OMP may only inherit that success after
// the current generation is still a fully validated identity ACK. Initial
// messages are never silently discarded on this path.
func (i *Instance) ompDeduplicatedLaunch(withMessage bool) error {
	if !i.ompIdentityAckRequired() {
		if err := i.requireFreshOmpPrimaryPane(); err != nil {
			return err
		}
		if withMessage {
			return fmt.Errorf("OMP was started by another request; the initial message was not sent")
		}
		return nil
	}
	observation := i.observeOmpLaunchIdentity()
	if observation.Warning != "" {
		return fmt.Errorf("concurrent OMP launch did not establish validated identity: %s", observation.Warning)
	}
	if observation.Generation == "" || observation.Status == nil {
		return fmt.Errorf("concurrent OMP launch has not acknowledged identity; history is preserved")
	}
	if !observation.Status.IdentityReady {
		reason := observation.Status.Error
		if reason == "" {
			reason = "OMP identity tracking is unavailable; history is preserved"
		}
		return fmt.Errorf("concurrent OMP launch did not establish identity: %s", tmux.StripANSI(reason))
	}
	if err := i.requireFreshOmpPrimaryPane(); err != nil {
		return err
	}
	if withMessage {
		return fmt.Errorf("OMP was started by another request; the initial message was not sent")
	}
	return nil
}

func (i *Instance) requireFreshOmpPrimaryPane() error {
	if i.tmuxSession == nil {
		return fmt.Errorf("cannot verify OMP primary pane: tmux session is not initialized")
	}
	alive, err := i.tmuxSession.PrimaryPaneAliveFresh()
	if err != nil {
		return fmt.Errorf("cannot verify OMP primary pane liveness: %w", err)
	}
	if !alive {
		return fmt.Errorf("OMP primary pane exited before launch acknowledgment")
	}
	return nil
}

func (i *Instance) observeOmpLaunchIdentity() ompLaunchObservation {
	if i.SSHHost != "" || i.IsSandboxed() {
		if i.SSHHost == "" && i.SandboxContainer == "" {
			return ompLaunchObservation{Warning: "OMP sandbox container is unavailable"}
		}
		return readOmpTargetIdentityHealth(i.ID, i.SSHHost, i.SandboxContainer)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ompLaunchObservation{Warning: err.Error()}
	}
	dir := filepath.Join(home, ".omp", "agent-deck", i.ID)
	generation, exists, err := readOmpControlLine(filepath.Join(dir, ".agent-deck-launch-generation"), "launch generation")
	if err != nil {
		return ompLaunchObservation{Warning: err.Error()}
	}
	if !exists {
		return ompLaunchObservation{}
	}
	observation := ompLaunchObservation{Generation: generation}
	if !ompSafeGeneration(generation) {
		observation.Warning = "Invalid OMP launch generation; history is preserved"
		return observation
	}
	data, err := readOmpHealthFile(filepath.Join(dir, ".agent-deck-omp-status."+generation+".json"))
	if os.IsNotExist(err) {
		return observation
	}
	if err != nil {
		observation.Warning = err.Error()
		return observation
	}
	var status ompIdentityStatus
	if json.Unmarshal(data, &status) != nil || status.InstanceID != i.ID || status.LaunchID != generation {
		observation.Warning = "OMP returned invalid identity status; history is preserved"
		return observation
	}
	observation.Status = &status
	if status.IdentityReady {
		binding, bindingErr := readOmpActiveBinding(dir)
		if bindingErr != nil {
			observation.Warning = bindingErr.Error()
		} else if binding == nil || binding.Generation != generation || binding.SessionID != status.SessionID || binding.File != status.SessionFile {
			observation.Warning = "OMP identity status does not match its active binding; history is preserved"
		}
	}
	return observation
}

func (i *Instance) ompLaunchFailure(message string) error {
	output := ""
	if i.tmuxSession != nil {
		output, _ = i.tmuxSession.CaptureHistoryLines(50)
	}
	output = strings.TrimSpace(tmux.StripANSI(output))
	if len(output) > 8192 {
		output = output[len(output)-8192:]
	}
	if output != "" && !strings.Contains(message, output) {
		message += ": " + output
	}
	err := fmt.Errorf("%s", message)
	i.recordPrepareFailure(i.Command, err)
	return err
}

// A tmux create acknowledgment is not a provider-start acknowledgment. Wait
// for the exact per-spawn generation to publish a validated binding/status.
func (i *Instance) waitForOmpIdentityAck(expected string, timeout time.Duration) error {
	if i.Tool != "omp" || i.tmuxSession == nil || expected == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = send.DefaultAgentReadyTimeout
	}
	deadline := time.Now().Add(timeout)
	seenExpected := false
	lastWarning := ""
	for {
		observation := i.observeOmpLaunchIdentity()
		if observation.Generation == expected {
			seenExpected = true
			if observation.Warning != "" {
				return i.ompLaunchFailure(observation.Warning)
			}
			if observation.Status != nil {
				status := observation.Status
				if !status.IdentityReady {
					reason := status.Error
					if reason == "" {
						reason = "OMP identity tracking is unavailable; history is preserved"
					}
					return i.ompLaunchFailure(tmux.StripANSI(reason))
				}
				// A ready file from a process that already died is not a successful
				// interactive launch. Check liveness immediately before accepting it.
				if err := i.requireFreshOmpPrimaryPane(); err != nil {
					return i.ompLaunchFailure(err.Error() + " for launch " + expected)
				}
				i.mu.Lock()
				i.ompIdentityWarning = tmux.StripANSI(status.Error)
				i.ompMetadataCheckedAt = time.Now()
				i.ForkStartCommand = ""
				i.IsForkAwaitingStart = false
				i.mu.Unlock()
				return nil
			}
		} else {
			if seenExpected && observation.Generation != "" {
				return i.ompLaunchFailure("OMP launch generation changed before identity acknowledgment; history is preserved")
			}
			lastWarning = observation.Warning
		}
		if livenessErr := i.requireFreshOmpPrimaryPane(); livenessErr != nil {
			// The process can publish a negative ACK and exit in the narrow gap
			// between this iteration's metadata read and its liveness probe. Take
			// one final bounded read so the provider's durable reason wins over a
			// generic dead-pane diagnosis.
			final := i.observeOmpLaunchIdentity()
			if final.Generation == expected {
				if final.Warning != "" {
					return i.ompLaunchFailure(final.Warning)
				}
				if final.Status != nil && !final.Status.IdentityReady {
					reason := final.Status.Error
					if reason == "" {
						reason = "OMP identity tracking is unavailable; history is preserved"
					}
					return i.ompLaunchFailure(tmux.StripANSI(reason))
				}
			}
			return i.ompLaunchFailure(livenessErr.Error() + " for launch " + expected)
		}
		if !time.Now().Before(deadline) {
			message := "Timed out waiting for OMP identity acknowledgment for launch " + expected
			if lastWarning != "" {
				message += ": " + lastWarning
			}
			return i.ompLaunchFailure(message)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
