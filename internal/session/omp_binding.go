package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/asheshgoplani/agent-deck/internal/safeio"
)

const ompActiveBindingName = ".agent-deck-active-session"
const ompBindingLimit = 16 * 1024
const ompHeaderLimit = 256 * 1024

// The same binding is consumed on the execution host for SSH/sandbox launches.
// This fragment deliberately contains no transcript-discovery-by-mtime logic.
func ompBindingSelectionShell() string {
	return `
omp_binding_fail() { echo "$1; history preserved" >&2; return 1; }
omp_safe_generation() {
  [ -n "$1" ] && [ "${#1}" -le 128 ] || return 1
  case "$1" in [0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz]*) ;; *) return 1;; esac
  case "$1" in *[!0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz._-]*) return 1;; esac
}
omp_read_control_snapshot() {
  control_path=$1; control_label=$2
  [ -f "$control_path" ] && [ ! -L "$control_path" ] || { omp_binding_fail "Invalid OMP $control_label"; return 1; }
  control_size=$(wc -c < "$control_path") || { omp_binding_fail "Cannot read OMP $control_label"; return 1; }
  [ "$control_size" -le 16384 ] || { omp_binding_fail "Oversized OMP $control_label"; return 1; }
  # Preserve trailing newlines, reject NUL stripping, and parse this bounded
  # snapshot rather than reopening a file that may have changed underneath us.
  control_snapshot=$(LC_ALL=C dd if="$control_path" bs=16385 count=1 2>/dev/null; printf '.') 2>/dev/null
  control_read_size=$(printf '%s' "$control_snapshot" | LC_ALL=C wc -c)
  [ "$control_read_size" -eq "$((control_size + 1))" ] || { omp_binding_fail "Invalid OMP $control_label bytes"; return 1; }
  control_snapshot=${control_snapshot%.}
  case "$control_snapshot" in *"$(printf '\r')"*) omp_binding_fail "Invalid OMP $control_label bytes"; return 1;; esac
}
omp_read_generation_file() {
  generation_path=$1; generation_label=$2
  omp_read_control_snapshot "$generation_path" "$generation_label" || return 1
  exec 8< <(printf '%s' "$control_snapshot") || { omp_binding_fail "Cannot open OMP $generation_label snapshot"; return 1; }
  IFS= read -r control_generation <&8 || { exec 8<&-; omp_binding_fail "Invalid OMP $generation_label"; return 1; }
  if IFS= read -r generation_extra <&8 || [ -n "$generation_extra" ]; then exec 8<&-; omp_binding_fail "Invalid OMP $generation_label"; return 1; fi
  exec 8<&-
  omp_safe_generation "$control_generation" || { omp_binding_fail "Invalid OMP $generation_label"; return 1; }
}
omp_read_binding_record() {
  record_path=$1
  omp_read_control_snapshot "$record_path" "active-conversation record $record_path" || return 1
  exec 9< <(printf '%s' "$control_snapshot") || { omp_binding_fail "Cannot open OMP active-conversation snapshot $record_path"; return 1; }
  IFS= read -r record_version <&9 || { exec 9<&-; omp_binding_fail "Invalid OMP active-conversation record $record_path"; return 1; }
  IFS= read -r record_file <&9 || { exec 9<&-; omp_binding_fail "Invalid OMP active-conversation record $record_path"; return 1; }
  IFS= read -r record_id <&9 || { exec 9<&-; omp_binding_fail "Invalid OMP active-conversation record $record_path"; return 1; }
  IFS= read -r record_state <&9 || { exec 9<&-; omp_binding_fail "Invalid OMP active-conversation record $record_path"; return 1; }
  IFS= read -r record_generation <&9 || { exec 9<&-; omp_binding_fail "Invalid OMP active-conversation record $record_path"; return 1; }
  if IFS= read -r record_extra <&9 || [ -n "$record_extra" ]; then exec 9<&-; omp_binding_fail "Invalid OMP active-conversation record $record_path"; return 1; fi
  exec 9<&-
  [ "$record_version" = 1 ] && [ -n "$record_id" ] && omp_safe_generation "$record_generation" || { omp_binding_fail "Invalid OMP active-conversation record $record_path"; return 1; }
  case "$record_file" in /*.jsonl) ;; *) omp_binding_fail "Invalid OMP active-conversation path $record_file"; return 1;; esac
  case "$record_state" in saved|pending) ;; *) omp_binding_fail "Invalid OMP active-conversation state in $record_path"; return 1;; esac
}
omp_canonical_candidate() {
  candidate_parent=${1%/*}; candidate_base=${1##*/}
  canonical_parent=$(CDPATH= cd -P -- "$candidate_parent" 2>/dev/null && pwd -P) || return 1
  printf '%s/%s\n' "$canonical_parent" "$candidate_base"
}
omp_path_category() {
  category_root=$1; category_row=$2; category_file=$3
  case "$category_file" in
    "$category_root"/*) if [ "${category_file%/*}" = "$category_row" ]; then printf 'owned\n'; else printf 'other\n'; fi ;;
    *) printf 'external\n' ;;
  esac
}
omp_validate_location() {
  validate_file=$1
  [ ! -L "$validate_file" ] || { omp_binding_fail "OMP active conversation is a transcript symlink $validate_file"; return 1; }
  managed_root=${session_dir%/*}
  real_managed_root=$(CDPATH= cd -P -- "$managed_root" 2>/dev/null && pwd -P) || { omp_binding_fail "Cannot resolve OMP managed root $managed_root"; return 1; }
  real_session_dir=$(CDPATH= cd -P -- "$session_dir" 2>/dev/null && pwd -P) || { omp_binding_fail "Cannot resolve OMP session directory $session_dir"; return 1; }
  [ "$real_session_dir" = "$real_managed_root/${session_dir##*/}" ] || { omp_binding_fail "OMP session directory crosses into another managed row $session_dir"; return 1; }
  lexical_category=$(omp_path_category "$managed_root" "$session_dir" "$validate_file")
  [ "$lexical_category" != other ] || { omp_binding_fail "OMP active conversation belongs to another entry or nested task $validate_file"; return 1; }
  canonical_file=$(omp_canonical_candidate "$validate_file") || { omp_binding_fail "Cannot resolve OMP active-conversation path $validate_file"; return 1; }
  physical_category=$(omp_path_category "$real_managed_root" "$real_session_dir" "$canonical_file")
  [ "$physical_category" != other ] && [ "$physical_category" = "$lexical_category" ] || { omp_binding_fail "OMP active conversation crosses into another entry or nested task $validate_file"; return 1; }
}
omp_read_header_id() {
  LC_ALL=C dd if="$1" bs=262144 count=1 2>/dev/null | head -n 32 | sed -n 's/.*"type":"session".*"id":"\([^"]*\)".*/\1/p' | head -n 1
}
omp_corroborated_legacy_root() {
  breadcrumb_managed_root=${session_dir%/*}; breadcrumb_omp_root=${breadcrumb_managed_root%/*}; corroborated_root=
  for breadcrumb in "$breadcrumb_omp_root"/*/terminal-sessions/*; do
    [ -f "$breadcrumb" ] && [ ! -L "$breadcrumb" ] || continue
    breadcrumb_size=$(wc -c < "$breadcrumb" 2>/dev/null) || continue
    [ "$breadcrumb_size" -le 16384 ] || continue
    # Keep trailing newlines with a sentinel and reject changed/stripped bytes:
    # bash otherwise drops NULs and could turn malformed evidence into a path.
    breadcrumb_record=$(LC_ALL=C dd if="$breadcrumb" bs=16385 count=1 2>/dev/null; printf '.') 2>/dev/null
    breadcrumb_read_size=$(printf '%s' "$breadcrumb_record" | LC_ALL=C wc -c)
    [ "$breadcrumb_read_size" -eq "$((breadcrumb_size + 1))" ] || continue
    breadcrumb_root=$(printf '%s' "${breadcrumb_record%.}" | sed -n '2p')
    [ "${breadcrumb_root%/*}" = "$session_dir" ] || continue
    for breadcrumb_candidate in "$session_dir"/*.jsonl; do
      [ -f "$breadcrumb_candidate" ] && [ ! -L "$breadcrumb_candidate" ] && [ "$breadcrumb_candidate" = "$breadcrumb_root" ] || continue
      if [ -n "$corroborated_root" ] && [ "$corroborated_root" != "$breadcrumb_candidate" ]; then return 1; fi
      corroborated_root=$breadcrumb_candidate
    done
  done
  [ -n "$corroborated_root" ] || return 1
  printf '%s\n' "$corroborated_root"
}
omp_sibling_binding_path() {
  sibling_dir=$1
  if [ -e "$sibling_dir/.agent-deck-launch-generation" ] || [ -L "$sibling_dir/.agent-deck-launch-generation" ]; then
    omp_read_generation_file "$sibling_dir/.agent-deck-launch-generation" "launch generation in $sibling_dir" || return 1
    sibling_generation=$control_generation
    sibling_record="$sibling_dir/.agent-deck-active-session.$sibling_generation"
    if [ ! -e "$sibling_record" ] && [ ! -L "$sibling_record" ]; then sibling_record="$sibling_dir/.agent-deck-source-binding.$sibling_generation"; fi
  else
    sibling_record="$sibling_dir/.agent-deck-active-session"
  fi
  [ -e "$sibling_record" ] || [ -L "$sibling_record" ] || return 2
  printf '%s\n' "$sibling_record"
}
omp_check_sibling_claims() {
  claim_file=$1; claim_id=$2; managed_root=${session_dir%/*}
  claim_canonical=$(omp_canonical_candidate "$claim_file") || return 1
  for sibling_dir in "$managed_root"/*; do
    [ -d "$sibling_dir" ] && [ ! -L "$sibling_dir" ] && [ "$sibling_dir" != "$session_dir" ] || continue
    if sibling_record=$(omp_sibling_binding_path "$sibling_dir"); then sibling_status=0; else sibling_status=$?; fi
    [ "$sibling_status" -eq 2 ] && continue
    [ "$sibling_status" -eq 0 ] || { omp_binding_fail "Invalid OMP ownership state in $sibling_dir"; return 1; }
    if (
      omp_read_binding_record "$sibling_record" || exit 2
      if [ -e "$sibling_dir/.agent-deck-launch-generation" ] || [ -L "$sibling_dir/.agent-deck-launch-generation" ]; then
        omp_read_generation_file "$sibling_dir/.agent-deck-launch-generation" "launch generation in $sibling_dir" || exit 2
        sibling_current_generation=$control_generation
        if [ "$sibling_record" = "$sibling_dir/.agent-deck-active-session.$sibling_current_generation" ]; then [ "$record_generation" = "$sibling_current_generation" ] || exit 2; fi
      fi
      [ "$record_id" != "$claim_id" ] || exit 10
      sibling_canonical=$(omp_canonical_candidate "$record_file") || exit 2
      [ "$sibling_canonical" != "$claim_canonical" ] || exit 10
      exit 0
    ); then sibling_status=0; else sibling_status=$?; fi
    [ "$sibling_status" -ne 10 ] || { omp_binding_fail "OMP conversation $claim_id is claimed by another Agent Deck entry $sibling_dir"; return 1; }
    [ "$sibling_status" -eq 0 ] || { omp_binding_fail "Invalid OMP ownership binding in $sibling_dir"; return 1; }
  done
}
bound_generation=; binding_file=; binding_scope=legacy; fresh_marker="$session_dir/.agent-deck-fresh-pending"; require_current_generation=0
if [ -n "${AGENTDECK_OMP_SOURCE_ERROR:-}" ]; then omp_binding_fail "$AGENTDECK_OMP_SOURCE_ERROR"; exit 1; fi
if [ -n "${AGENTDECK_OMP_SOURCE_BINDING:-}" ]; then
  omp_safe_generation "${AGENTDECK_OMP_LAUNCH_ID:-}" || { omp_binding_fail "Invalid OMP launch generation"; exit 1; }
  expected_source_binding="$session_dir/.agent-deck-source-binding.$AGENTDECK_OMP_LAUNCH_ID"
  [ "$AGENTDECK_OMP_SOURCE_BINDING" = "$expected_source_binding" ] || { omp_binding_fail "Invalid OMP source-binding path"; exit 1; }
  binding_scope=source
  if [ -e "$AGENTDECK_OMP_SOURCE_BINDING" ] || [ -L "$AGENTDECK_OMP_SOURCE_BINDING" ]; then binding_file=$AGENTDECK_OMP_SOURCE_BINDING; fi
elif [ -e "$session_dir/.agent-deck-launch-generation" ] || [ -L "$session_dir/.agent-deck-launch-generation" ]; then
  omp_read_generation_file "$session_dir/.agent-deck-launch-generation" "launch generation" || exit 1
  current_generation=$control_generation
  binding_file="$session_dir/.agent-deck-active-session.$current_generation"; require_current_generation=1
  binding_scope=current; fresh_marker="$session_dir/.agent-deck-fresh-pending.$current_generation"
  [ -e "$binding_file" ] || [ -L "$binding_file" ] || { omp_binding_fail "Previous OMP launch never acknowledged identity tracking"; exit 1; }
else
  binding_file="$session_dir/.agent-deck-active-session"
  if [ ! -e "$binding_file" ] && [ ! -L "$binding_file" ]; then binding_file=; fi
fi
if [ -n "$binding_file" ]; then
  omp_read_binding_record "$binding_file" || exit 1
  bound_file=$record_file; bound_id=$record_id; bound_state=$record_state; bound_generation=$record_generation
  if [ "$binding_scope" = source ]; then
    fresh_marker="$session_dir/.agent-deck-fresh-pending.$bound_generation"
    if [ "$bound_generation" = legacy ] && [ ! -e "$fresh_marker" ] && [ ! -L "$fresh_marker" ]; then fresh_marker="$session_dir/.agent-deck-fresh-pending"; fi
  fi
  if [ "$require_current_generation" -eq 1 ] && [ "$bound_generation" != "$current_generation" ]; then omp_binding_fail "OMP binding did not acknowledge current launch generation"; exit 1; fi
  if [ "$require_current_generation" -eq 1 ]; then
    omp_read_generation_file "$session_dir/.agent-deck-launch-generation" "launch generation" || exit 1
    [ "$control_generation" = "$current_generation" ] || { omp_binding_fail "OMP launch generation changed during identity validation"; exit 1; }
  fi
  omp_validate_location "$bound_file" || exit 1
  if [ -f "$bound_file" ] && [ ! -L "$bound_file" ]; then
    header_id=$(omp_read_header_id "$bound_file")
    [ -n "$header_id" ] && [ "$header_id" = "$bound_id" ] || { omp_binding_fail "OMP conversation ID mismatch at $bound_file"; exit 1; }
    omp_check_sibling_claims "$bound_file" "$bound_id" || exit 1
    source_file=$bound_file; root_count=1
  elif [ "$bound_state" = pending ] && [ ! -e "$bound_file" ]; then
    omp_check_sibling_claims "$bound_file" "$bound_id" || exit 1
    source_file=; root_count=0
  else
    omp_binding_fail "OMP active conversation is missing $bound_file"; exit 1
  fi
else
  if [ "$root_count" -gt 1 ]; then
    if corroborated_root=$(omp_corroborated_legacy_root); then source_file=$corroborated_root; root_count=1; fi
  fi
  if [ "$root_count" -eq 1 ]; then
    omp_validate_location "$source_file" || exit 1
    header_id=$(omp_read_header_id "$source_file")
    [ -n "$header_id" ] || { omp_binding_fail "OMP transcript has no valid bounded session header $source_file"; exit 1; }
    omp_check_sibling_claims "$source_file" "$header_id" || exit 1
  fi
fi
if [ -e "$fresh_marker" ] || [ -L "$fresh_marker" ]; then
  omp_read_generation_file "$fresh_marker" "fresh-conversation boundary" || exit 1
  fresh_generation=$control_generation
  if [ -z "$bound_generation" ] || [ "$fresh_generation" != "$bound_generation" ]; then omp_binding_fail "Fresh OMP creation did not establish a new active conversation; retry fresh start. Previous history is preserved"; exit 1; fi
fi
`
}

// The provider reports this identity, rather than a directory scan guessing
// which of its retained conversations is current. Keep the on-disk format
// readable by the remote launch shell without an additional JSON CLI.
type ompActiveBinding struct {
	File       string
	SessionID  string
	State      string
	Generation string
}

// Validate on the owner before any destructive restart action. The exact same
// error reaches TUI Enter, CLI, hub and web callers through the lifecycle API.
func (i *Instance) prepareOmpIdentity() error {
	if i.Tool != "omp" {
		return nil
	}
	refuse := func(err error) error {
		i.recordPrepareFailure(i.Command, err)
		return err
	}
	opts := i.resolvedOmpOptions()
	nonTUI := ompCommandUsesNonTUI(i.Command)
	if i.ompFreshStart {
		if !opts.NoSession && nonTUI {
			return refuse(fmt.Errorf("starting a fresh OMP conversation requires an interactive OMP command so its identity can be acknowledged; history is preserved"))
		}
		return nil
	}
	if i.IsForkAwaitingStart {
		_, ackRequired, found, metadataErr := parseOmpLaunchMetadata(i.ForkStartCommand)
		if metadataErr != nil {
			return refuse(metadataErr)
		}
		if found && !ackRequired {
			return refuse(fmt.Errorf("pending OMP native fork used a non-interactive command and cannot acknowledge its child identity; switch to an interactive OMP command and restart fresh to recover while preserving history"))
		}
		return nil
	}
	if !opts.NoSession && nonTUI && (opts.FromClaude || opts.FromCodex) {
		return refuse(fmt.Errorf("importing an OMP conversation requires an interactive OMP command so its identity can be acknowledged; history is preserved"))
	}
	if i.SSHHost != "" || i.IsSandboxed() || opts.NoSession || opts.FromClaude || opts.FromCodex {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".omp", "agent-deck", i.ID)
	// Interrupted native re-keying must be finalized by the migration logic
	// before its replacement can be made the stable active binding.
	migrationMarker := filepath.Join(dir, ".agent-deck-legacy-migration")
	if _, markerErr := os.Lstat(migrationMarker); markerErr == nil {
		if nonTUI {
			return refuse(fmt.Errorf("interrupted OMP identity migration requires an interactive OMP command to recover; history is preserved"))
		}
		return nil
	} else if !os.IsNotExist(markerErr) {
		return refuse(fmt.Errorf("cannot inspect OMP identity migration state: %w; history is preserved", markerErr))
	}
	binding, err := resolveOmpActiveBinding(dir)
	if nonTUI {
		if err == nil && binding != nil && binding.State == "pending" {
			if _, statErr := os.Stat(binding.File); os.IsNotExist(statErr) {
				err = fmt.Errorf("pending OMP identity has no transcript to resume; an interactive OMP command is required to complete or replace it; history is preserved")
			} else if statErr != nil {
				err = fmt.Errorf("cannot verify pending OMP transcript %s: %w; history is preserved", binding.File, statErr)
			}
		}
		if err != nil {
			return refuse(err)
		}
		return nil
	}
	if err == nil && binding != nil {
		bindingPath, _, _, pathErr := ompCurrentBindingPath(dir)
		if pathErr != nil {
			err = pathErr
		} else if _, statErr := os.Stat(bindingPath); os.IsNotExist(statErr) {
			err = writeOmpActiveBinding(dir, binding)
		}
	}
	if err != nil {
		i.recordPrepareFailure(i.Command, err)
	}
	return err
}

func ompSafeGeneration(generation string) bool {
	if len(generation) == 0 || len(generation) > 128 ||
		!((generation[0] >= '0' && generation[0] <= '9') ||
			(generation[0] >= 'A' && generation[0] <= 'Z') ||
			(generation[0] >= 'a' && generation[0] <= 'z')) {
		return false
	}
	for _, char := range generation {
		if (char >= '0' && char <= '9') || (char >= 'A' && char <= 'Z') ||
			(char >= 'a' && char <= 'z') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func openOmpRegularFile(path string) (*os.File, error) {
	// Bind validation and reads to one descriptor. O_NOFOLLOW closes the final
	// symlink swap window; O_NONBLOCK prevents a raced FIFO/device from hanging
	// lifecycle preflight before fstat rejects it.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	return file, nil
}

func readOmpBoundedRegularFile(path string, limit int64) ([]byte, error) {
	file, err := openOmpRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d-byte limit: %s", limit, path)
	}
	return data, nil
}

func readOmpControlLine(path, label string) (string, bool, error) {
	data, err := readOmpBoundedRegularFile(path, ompBindingLimit)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read OMP %s: %w", label, err)
	}
	if len(data) < 2 || data[len(data)-1] != '\n' || strings.Count(string(data), "\n") != 1 || strings.ContainsAny(string(data), "\x00\r") {
		return "", false, fmt.Errorf("invalid OMP %s; history is preserved", label)
	}
	return string(data[:len(data)-1]), true, nil
}

func ompCurrentBindingPath(dir string) (path, generation string, tracked bool, err error) {
	generation, tracked, err = readOmpControlLine(filepath.Join(dir, ".agent-deck-launch-generation"), "launch generation")
	if err != nil {
		return "", "", false, err
	}
	if !tracked {
		return filepath.Join(dir, ompActiveBindingName), "", false, nil
	}
	if !ompSafeGeneration(generation) {
		return "", "", false, fmt.Errorf("invalid OMP launch generation in %s; history is preserved", dir)
	}
	return filepath.Join(dir, ompActiveBindingName+"."+generation), generation, true, nil
}

func readOmpBindingRecord(path string) (*ompActiveBinding, error) {
	data, err := readOmpBoundedRegularFile(path, ompBindingLimit)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OMP active conversation: %w", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' || strings.ContainsAny(string(data), "\x00\r") {
		return nil, fmt.Errorf("invalid OMP active-conversation record %s; history is preserved", path)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) != 6 || lines[5] != "" || lines[0] != "1" {
		return nil, fmt.Errorf("invalid OMP active-conversation record %s; history is preserved", path)
	}
	binding := &ompActiveBinding{File: lines[1], SessionID: lines[2], State: lines[3], Generation: lines[4]}
	if !filepath.IsAbs(binding.File) || filepath.Ext(binding.File) != ".jsonl" ||
		binding.SessionID == "" || strings.ContainsAny(binding.SessionID, "\x00\r\n") ||
		!ompSafeGeneration(binding.Generation) ||
		(binding.State != "saved" && binding.State != "pending") {
		return nil, fmt.Errorf("invalid OMP active-conversation identity in %s; history is preserved", path)
	}
	return binding, nil
}

func readOmpActiveBindingSelection(dir string) (*ompActiveBinding, string, bool, error) {
	path, generation, tracked, err := ompCurrentBindingPath(dir)
	if err != nil {
		return nil, "", false, err
	}
	binding, err := readOmpBindingRecord(path)
	if err != nil {
		return nil, "", false, err
	}
	if binding == nil && tracked {
		source := filepath.Join(dir, ".agent-deck-source-binding."+generation)
		return nil, "", false, fmt.Errorf("OMP identity tracking is unavailable for generation %s in %s; history is preserved. The prior validated candidate, if any, remains at %s; inspect the failed pane/history and choose an explicit fresh start or recovery before retrying", generation, dir, source)
	}
	if binding != nil {
		if tracked && binding.Generation != generation {
			return nil, "", false, fmt.Errorf("OMP binding did not acknowledge current launch generation %s in %s; identity tracking is unavailable and history is preserved", generation, dir)
		}
		if err := validateOmpActiveBinding(dir, binding); err != nil {
			return nil, "", false, err
		}
	}
	afterPath, afterGeneration, afterTracked, err := ompCurrentBindingPath(dir)
	if err != nil {
		return nil, "", false, err
	}
	if afterPath != path || afterGeneration != generation || afterTracked != tracked {
		return nil, "", false, fmt.Errorf("OMP launch generation changed during identity validation in %s; retry without modifying history", dir)
	}
	return binding, generation, tracked, nil
}

func readOmpActiveBinding(dir string) (*ompActiveBinding, error) {
	binding, _, _, err := readOmpActiveBindingSelection(dir)
	return binding, err
}

func validateOmpActiveBinding(dir string, binding *ompActiveBinding) error {
	if err := validateOmpBindingLocation(dir, binding.File); err != nil {
		return err
	}
	info, err := os.Lstat(binding.File)
	if os.IsNotExist(err) && binding.State == "pending" {
		return validateOmpSiblingClaims(dir, binding) // Never resurrect prior history.
	}
	if err != nil {
		return fmt.Errorf("OMP active conversation is unavailable at %s: %w", binding.File, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("OMP active conversation is not a regular transcript: %s", binding.File)
	}
	id, err := readOmpRootID(binding.File)
	if err != nil {
		return err
	}
	if id != binding.SessionID {
		return fmt.Errorf("OMP conversation ID mismatch at %s: expected %s, found %s", binding.File, binding.SessionID, id)
	}
	return validateOmpSiblingClaims(dir, binding)
}

type ompPathCategory int

const (
	ompPathExternal ompPathCategory = iota
	ompPathOwned
	ompPathOtherManaged
)

func classifyOmpPath(root, row, file string) (ompPathCategory, error) {
	cleanRoot, cleanRow, cleanFile := filepath.Clean(root), filepath.Clean(row), filepath.Clean(file)
	rel, err := filepath.Rel(cleanRoot, cleanFile)
	if err != nil {
		return ompPathExternal, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ompPathExternal, nil
	}
	if filepath.Dir(cleanFile) == cleanRow {
		return ompPathOwned, nil
	}
	return ompPathOtherManaged, nil
}

func canonicalOmpCandidate(file string) (string, error) {
	resolved, err := filepath.EvalSymlinks(file)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(file))
	if parentErr != nil {
		return "", parentErr
	}
	return filepath.Join(parent, filepath.Base(file)), nil
}

func validateOmpBindingLocation(dir, file string) error {
	managedRoot := filepath.Dir(dir)
	lexical, err := classifyOmpPath(managedRoot, dir, file)
	if err != nil {
		return err
	}
	if lexical == ompPathOtherManaged {
		return fmt.Errorf("OMP active conversation points to another entry or nested task: %s", file)
	}
	realRoot, err := filepath.EvalSymlinks(managedRoot)
	if err != nil {
		return fmt.Errorf("resolve OMP managed root: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve OMP entry directory: %w", err)
	}
	if realDir != filepath.Join(realRoot, filepath.Base(dir)) {
		return fmt.Errorf("OMP entry directory crosses into another managed row: %s", dir)
	}
	realFile, err := canonicalOmpCandidate(file)
	if err != nil {
		return fmt.Errorf("resolve OMP active conversation path %s: %w", file, err)
	}
	physical, err := classifyOmpPath(realRoot, realDir, realFile)
	if err != nil {
		return err
	}
	if physical == ompPathOtherManaged || physical != lexical {
		return fmt.Errorf("OMP active conversation crosses into another entry or nested task: %s", file)
	}
	return nil
}

func validateOmpSiblingClaims(dir string, binding *ompActiveBinding) error {
	managedRoot := filepath.Dir(dir)
	entries, err := os.ReadDir(managedRoot)
	if err != nil {
		return fmt.Errorf("inspect OMP ownership rows: %w", err)
	}
	canonical, err := canonicalOmpCandidate(binding.File)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || filepath.Join(managedRoot, entry.Name()) == filepath.Clean(dir) {
			continue
		}
		siblingDir := filepath.Join(managedRoot, entry.Name())
		siblingPath, siblingGeneration, siblingTracked, pathErr := ompCurrentBindingPath(siblingDir)
		if pathErr != nil {
			return fmt.Errorf("invalid OMP ownership state in %s: %w", siblingDir, pathErr)
		}
		sibling, readErr := readOmpBindingRecord(siblingPath)
		if readErr != nil {
			return fmt.Errorf("invalid OMP ownership binding in %s: %w", siblingDir, readErr)
		}
		sourceSnapshot := false
		if sibling == nil && siblingTracked {
			siblingPath = filepath.Join(siblingDir, ".agent-deck-source-binding."+siblingGeneration)
			sibling, readErr = readOmpBindingRecord(siblingPath)
			if readErr != nil {
				return fmt.Errorf("invalid OMP source ownership binding in %s: %w", siblingDir, readErr)
			}
			sourceSnapshot = sibling != nil
		}
		if sibling == nil {
			continue
		}
		if siblingTracked && !sourceSnapshot && sibling.Generation != siblingGeneration {
			return fmt.Errorf("invalid OMP ownership binding in %s: generation %s did not acknowledge current generation %s", siblingDir, sibling.Generation, siblingGeneration)
		}
		siblingCanonical, canonicalErr := canonicalOmpCandidate(sibling.File)
		if canonicalErr != nil {
			return fmt.Errorf("invalid OMP ownership path in %s: %w", siblingDir, canonicalErr)
		}
		if sibling.SessionID == binding.SessionID || siblingCanonical == canonical {
			return fmt.Errorf("OMP conversation %s at %s is claimed by another Agent Deck entry %s; history is preserved", binding.SessionID, binding.File, siblingDir)
		}
	}
	return nil
}

// OMP can prepend a title entry before the session header. Never load an entire
// conversation (the incident transcript was 60 MB) to identify its root.
func readOmpRootID(path string) (string, error) {
	f, err := openOmpRegularFile(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, ompHeaderLimit+1))
	scanner.Buffer(make([]byte, 4096), ompHeaderLimit)
	for n := 0; n < 32 && scanner.Scan(); n++ {
		var entry struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return "", fmt.Errorf("invalid OMP transcript header at %s: %w", path, err)
		}
		if entry.Type == "session" && entry.ID != "" {
			if strings.ContainsAny(entry.ID, "\x00\r\n") {
				return "", fmt.Errorf("invalid OMP transcript session ID at %s", path)
			}
			return entry.ID, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("invalid bounded OMP transcript header at %s: %w", path, err)
	}
	return "", fmt.Errorf("OMP transcript has no valid bounded session header: %s", path)
}

func writeOmpActiveBinding(dir string, binding *ompActiveBinding) error {
	data := fmt.Sprintf("1\n%s\n%s\n%s\n%s\n", binding.File, binding.SessionID, binding.State, binding.Generation)
	path := filepath.Join(dir, ompActiveBindingName)
	if generation, exists, err := readOmpControlLine(filepath.Join(dir, ".agent-deck-launch-generation"), "launch generation"); err != nil {
		return err
	} else if exists {
		if generation != binding.Generation || !ompSafeGeneration(generation) {
			return fmt.Errorf("refusing to write OMP binding for stale generation %s; history is preserved", binding.Generation)
		}
		path += "." + generation
	}
	return safeio.SafeOverwrite(path, []byte(data), safeio.Options{Perm: 0o600, SkipBackup: true})
}

// resolveOmpActiveBinding bootstraps legacy entries without rewriting history.
// Multiple roots are acceptable with a recorded binding. Without one, only a
// uniquely corroborated, entry-scoped provider breadcrumb can select a root.
func resolveOmpActiveBinding(dir string) (*ompActiveBinding, error) {
	binding, currentGeneration, tracked, err := readOmpActiveBindingSelection(dir)
	if err != nil {
		return nil, err
	}
	freshMarker := filepath.Join(dir, ".agent-deck-fresh-pending")
	if tracked {
		freshMarker += "." + currentGeneration
	}
	if pending, exists, pendingErr := readOmpControlLine(freshMarker, "fresh-conversation boundary"); pendingErr != nil {
		return nil, pendingErr
	} else if exists {
		if !ompSafeGeneration(pending) || binding == nil || binding.Generation != pending {
			return nil, fmt.Errorf("fresh OMP creation did not establish a new active conversation; retry fresh start. Previous history is preserved in %s", dir)
		}
	}
	if binding != nil {
		return binding, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OMP conversation directory: %w", err)
	}
	var roots []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && filepath.Ext(entry.Name()) == ".jsonl" {
			roots = append(roots, filepath.Join(dir, entry.Name()))
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}
	source := roots[0]
	if len(roots) > 1 {
		source = ompCorroboratedLegacyRoot(dir, roots)
		if source == "" {
			return nil, fmt.Errorf("OMP has %d unbound histories in %s; active conversation cannot be determined safely. Preserved candidates:\n%s", len(roots), dir, strings.Join(roots, "\n"))
		}
	}
	id, err := readOmpRootID(source)
	if err != nil {
		return nil, err
	}
	binding = &ompActiveBinding{File: source, SessionID: id, State: "saved", Generation: "legacy"}
	if err := validateOmpActiveBinding(dir, binding); err != nil {
		return nil, err
	}
	return binding, nil
}

func ompCorroboratedLegacyRoot(dir string, roots []string) string {
	// Only accept breadcrumbs naming one of THIS entry's top-level roots;
	// terminal identifiers can be recycled and must never establish ownership.
	ompRoot := filepath.Dir(filepath.Dir(dir))
	breadcrumbs, _ := filepath.Glob(filepath.Join(ompRoot, "*", "terminal-sessions", "*"))
	selected := ""
	for _, path := range breadcrumbs {
		data, err := readOmpBoundedRegularFile(path, ompBindingLimit)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) < 2 || filepath.Dir(lines[1]) != dir {
			continue
		}
		for _, root := range roots {
			if lines[1] != root {
				continue
			}
			if selected != "" && selected != root {
				return ""
			}
			selected = root
		}
	}
	return selected
}
