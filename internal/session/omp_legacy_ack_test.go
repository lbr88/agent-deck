package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOmpLegacyMigrationRemainsRecoverableUntilProviderAcknowledges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inst := &Instance{ID: "interrupted-migration", Tool: "omp"}
	dir := filepath.Join(home, ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	original := filepath.Join(dir, "2026-09-09_original.jsonl")
	replacement := filepath.Join(dir, "2026-09-09_replacement.jsonl")
	marker := filepath.Join(dir, ".agent-deck-legacy-migration")
	write(original, "{\"type\":\"session\",\"id\":\"original\"}\n")
	write(replacement, "{\"type\":\"session\",\"id\":\"replacement\"}\n")
	write(marker, filepath.Base(original)+"\n")
	write(filepath.Join(dir, ompActiveBindingName), "1\n"+original+"\noriginal\nsaved\nlegacy\n")
	probe := filepath.Join(home, "omp-probe")
	if err := os.WriteFile(probe, []byte(`#!/bin/sh
set -eu
[ "$1" = --resume ] && [ "$2" = "$EXPECTED_OMP_RESUME" ] || { echo "wrong conversation: $*" >&2; exit 41; }
if [ "${ACK_OMP_IDENTITY:-}" = yes ]; then
  id=${2##*_}; id=${id%.jsonl}
  printf '1\n%s\n%s\nsaved\n%s\n' "$2" "$id" "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
fi
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPECTED_OMP_RESUME", replacement)
	t.Setenv("ACK_OMP_IDENTITY", "no")
	run := func() {
		t.Helper()
		if err := inst.prepareOmpIdentity(); err != nil {
			t.Fatalf("recovery preflight rejected preserved migration: %v", err)
		}
		if out, err := exec.Command("bash", "-c", inst.buildOmpCommand(probe)).CombinedOutput(); err != nil {
			t.Fatalf("migration retry did not resume intended replacement: %v\n%s", err, out)
		}
	}
	// The provider exits before ACK on two consecutive attempts. Archiving the
	// copied root must not erase the only recoverable identity checkpoint.
	for attempt := 0; attempt < 2; attempt++ {
		run()
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("attempt %d cleared migration recovery before provider ACK: %v", attempt+1, err)
		}
	}
	t.Setenv("ACK_OMP_IDENTITY", "yes")
	run()
	if got, err := os.ReadFile(filepath.Join(dir, ".agent-deck-legacy-collisions", "original", filepath.Base(original))); err != nil || !strings.Contains(string(got), `"id":"original"`) {
		t.Fatalf("original history was not preserved: %s, %v", got, err)
	}
	// OMP can branch again before exiting. Once ACKed, the retained migration
	// marker must not make that legitimate extra history ambiguous on restart.
	branched := filepath.Join(dir, "2026-09-09_branched.jsonl")
	write(branched, "{\"type\":\"session\",\"id\":\"branched\"}\n")
	generation, _, err := readOmpControlLine(filepath.Join(dir, ".agent-deck-launch-generation"), "launch generation")
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, ompActiveBindingName+"."+generation), fmt.Sprintf("1\n%s\nbranched\nsaved\n%s\n", branched, generation))
	t.Setenv("EXPECTED_OMP_RESUME", branched)
	run()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("acknowledged migration marker was not cleared: %v", err)
	}
	for _, file := range []string{replacement, branched} {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("retained history was lost: %s: %v", file, err)
		}
	}
}
