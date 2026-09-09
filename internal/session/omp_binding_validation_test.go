package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"al.essio.dev/pkg/shellescape"
)

func writeOmpValidationTranscript(t *testing.T, file, id string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(fmt.Sprintf("{\"type\":\"title\",\"title\":\"kept\"}\n{\"type\":\"session\",\"id\":%q}\n", id)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeOmpValidationBinding(t *testing.T, dir, file, id, state, generation string) {
	writeOmpValidationBindingAt(t, dir, ompActiveBindingName, file, id, state, generation)
}

func writeOmpValidationBindingAt(t *testing.T, dir, name, file, id, state, generation string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf("1\n%s\n%s\n%s\n%s\n", file, id, state, generation)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runOmpBindingSelection(t *testing.T, dir string, env ...string) (string, error) {
	t.Helper()
	script := fmt.Sprintf(
		`session_dir=%s; source_file=; root_count=0; for candidate in "$session_dir"/*.jsonl; do if [ -f "$candidate" ]; then source_file="$candidate"; root_count=$((root_count + 1)); fi; done; %s printf '%%s\n%%s\n' "$source_file" "$root_count"`,
		shellescape.Quote(dir), ompBindingSelectionShell(),
	)
	command := exec.Command("bash", "-c", script)
	command.Env = append(os.Environ(), env...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestOmpBindingManifestMustBeBoundedExactAndRegular(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, dir, valid string)
	}{
		{
			name: "missing trailing newline",
			mutate: func(t *testing.T, dir, valid string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, ompActiveBindingName), []byte(strings.TrimSuffix(valid, "\n")), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "sixth line",
			mutate: func(t *testing.T, dir, valid string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, ompActiveBindingName), []byte(valid+"unexpected\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unterminated sixth line",
			mutate: func(t *testing.T, dir, valid string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, ompActiveBindingName), []byte(valid+"unexpected"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "NUL in transcript path",
			mutate: func(t *testing.T, dir, valid string) {
				t.Helper()
				malformed := strings.Replace(valid, "root.jsonl", "root.jsonl\x00", 1)
				if err := os.WriteFile(filepath.Join(dir, ompActiveBindingName), []byte(malformed), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "NUL in session ID",
			mutate: func(t *testing.T, dir, valid string) {
				t.Helper()
				malformed := strings.Replace(valid, "session-a", "session-\x00a", 1)
				if err := os.WriteFile(filepath.Join(dir, ompActiveBindingName), []byte(malformed), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversized",
			mutate: func(t *testing.T, dir, valid string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, ompActiveBindingName), []byte(valid+strings.Repeat("x", 20*1024)), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "manifest symlink",
			mutate: func(t *testing.T, dir, valid string) {
				t.Helper()
				outside := filepath.Join(filepath.Dir(filepath.Dir(dir)), "outside-binding")
				if err := os.WriteFile(outside, []byte(valid), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(dir, ompActiveBindingName)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(dir, ompActiveBindingName)); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "agent-deck", "entry-a")
			file := filepath.Join(dir, "root.jsonl")
			writeOmpValidationTranscript(t, file, "session-a")
			writeOmpValidationBinding(t, dir, file, "session-a", "saved", "generation-a")
			validBytes, err := os.ReadFile(filepath.Join(dir, ompActiveBindingName))
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, dir, string(validBytes))

			if _, err := readOmpActiveBinding(dir); err == nil {
				t.Fatal("Go accepted malformed OMP ownership manifest")
			}
			if output, err := runOmpBindingSelection(t, dir); err == nil {
				t.Fatalf("target shell accepted malformed OMP ownership manifest: %s", output)
			}
		})
	}
}

func TestOmpGenerationControlMustBeByteExact(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "NUL in generation", data: "gen\x00eration\n"},
		{name: "unterminated extra line", data: "generation\ntrailing-fragment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "agent-deck", "entry-a")
			file := filepath.Join(dir, "root.jsonl")
			writeOmpValidationTranscript(t, file, "session-a")
			writeOmpValidationBindingAt(t, dir, ompActiveBindingName+".generation", file, "session-a", "saved", "generation")
			if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readOmpActiveBinding(dir); err == nil {
				t.Fatal("Go accepted malformed generation control")
			}
			if output, err := runOmpBindingSelection(t, dir); err == nil {
				t.Fatalf("target shell accepted malformed generation control: %s", output)
			}
		})
	}
}

func TestOmpBindingRejectsFIFOsWithoutBlocking(t *testing.T) {
	t.Run("binding FIFO", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "agent-deck", "entry-a")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(filepath.Join(dir, ompActiveBindingName), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readOmpActiveBinding(dir); err == nil {
			t.Fatal("binding FIFO was accepted")
		}
	})

	t.Run("transcript FIFO", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "agent-deck", "entry-a")
		file := filepath.Join(dir, "root.jsonl")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(file, 0o600); err != nil {
			t.Fatal(err)
		}
		writeOmpValidationBinding(t, dir, file, "session-a", "saved", "generation-a")
		if _, err := readOmpActiveBinding(dir); err == nil {
			t.Fatal("transcript FIFO was accepted")
		}
	})
}

func TestOmpBindingAllowsOwnedPendingAndExternalMoveWithoutFallingBack(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent-deck", "entry-a")
	historical := filepath.Join(dir, "historical.jsonl")
	writeOmpValidationTranscript(t, historical, "historical-id")
	pending := filepath.Join(dir, "allocated.jsonl")
	writeOmpValidationBinding(t, dir, pending, "pending-id", "pending", "generation-a")

	binding, err := resolveOmpActiveBinding(dir)
	if err != nil {
		t.Fatal(err)
	}
	if binding == nil || binding.File != pending || binding.State != "pending" {
		t.Fatalf("pending binding fell back to history: %+v", binding)
	}
	output, err := runOmpBindingSelection(t, dir)
	if err != nil {
		t.Fatalf("target shell rejected pending identity: %v\n%s", err, output)
	}
	if output != "\n0\n" {
		t.Fatalf("pending identity selected historical transcript: %q", output)
	}

	external := filepath.Join(root, "moved", "root.jsonl")
	writeOmpValidationTranscript(t, external, "pending-id")
	writeOmpValidationBinding(t, dir, external, "pending-id", "saved", "generation-a")
	binding, err = readOmpActiveBinding(dir)
	if err != nil || binding == nil || binding.File != external {
		t.Fatalf("legitimate external /move rejected: %+v, %v", binding, err)
	}
}

func TestOmpBindingRejectsNestedPhysicalCrossRowAndSiblingClaims(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "agent-deck")
	owned := filepath.Join(managed, "entry-a")
	sibling := filepath.Join(managed, "entry-b")

	t.Run("nested task", func(t *testing.T) {
		file := filepath.Join(owned, "root", "task.jsonl")
		writeOmpValidationTranscript(t, file, "task-id")
		writeOmpValidationBinding(t, owned, file, "task-id", "saved", "generation-a")
		if _, err := readOmpActiveBinding(owned); err == nil {
			t.Fatal("nested task transcript was accepted as the row root")
		}
	})

	t.Run("external symlink ancestor enters sibling", func(t *testing.T) {
		if err := os.RemoveAll(owned); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(sibling, "root.jsonl")
		writeOmpValidationTranscript(t, target, "same-id")
		alias := filepath.Join(root, "external-alias")
		if err := os.Symlink(sibling, alias); err != nil {
			t.Fatal(err)
		}
		disguised := filepath.Join(alias, "root.jsonl")
		writeOmpValidationBinding(t, owned, disguised, "same-id", "saved", "generation-a")
		if _, err := readOmpActiveBinding(owned); err == nil {
			t.Fatal("Go accepted a symlink ancestor into another managed row")
		}
		if output, err := runOmpBindingSelection(t, owned); err == nil {
			t.Fatalf("target shell accepted a symlink ancestor into another managed row: %s", output)
		}
	})

	t.Run("sibling claims UUID", func(t *testing.T) {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		ownFile := filepath.Join(root, "outside", "owned.jsonl")
		siblingFile := filepath.Join(sibling, "sibling.jsonl")
		writeOmpValidationTranscript(t, ownFile, "duplicate-id")
		writeOmpValidationTranscript(t, siblingFile, "duplicate-id")
		writeOmpValidationBinding(t, owned, ownFile, "duplicate-id", "saved", "generation-a")
		writeOmpValidationBinding(t, sibling, siblingFile, "duplicate-id", "saved", "generation-b")
		if _, err := readOmpActiveBinding(owned); err == nil {
			t.Fatal("Go accepted an ID claimed by another row")
		}
		if output, err := runOmpBindingSelection(t, owned); err == nil {
			t.Fatalf("target shell accepted an ID claimed by another row: %s", output)
		}
	})
}

func TestOmpBindingHeaderIsBoundedAndMustMatchProviderID(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "mismatch", body: "{\"type\":\"session\",\"id\":\"different\"}\n"},
		{name: "header after line bound", body: strings.Repeat("{\"type\":\"title\"}\n", 32) + "{\"type\":\"session\",\"id\":\"session-a\"}\n"},
		{name: "header after byte bound", body: "{\"type\":\"title\",\"title\":\"" + strings.Repeat("x", 300*1024) + "\"}\n{\"type\":\"session\",\"id\":\"session-a\"}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "agent-deck", "entry-a")
			file := filepath.Join(dir, "root.jsonl")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			writeOmpValidationBinding(t, dir, file, "session-a", "saved", "generation-a")
			if _, err := readOmpActiveBinding(dir); err == nil {
				t.Fatal("Go accepted invalid bounded transcript identity")
			}
			if output, err := runOmpBindingSelection(t, dir); err == nil {
				t.Fatalf("target shell accepted invalid bounded transcript identity: %s", output)
			}
		})
	}
}

func TestResolveOmpBindingRequiresAcknowledgmentOfLastInstalledGeneration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent-deck", "entry-a")
	file := filepath.Join(dir, "root.jsonl")
	writeOmpValidationTranscript(t, file, "session-a")
	writeOmpValidationBinding(t, dir, file, "session-a", "saved", "older-generation")
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("new-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveOmpActiveBinding(dir); err == nil || !strings.Contains(strings.ToLower(err.Error()), "tracking") {
		t.Fatalf("stale binding was accepted after an unacknowledged launch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("older-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeOmpValidationBindingAt(t, dir, ompActiveBindingName+".older-generation", file, "session-a", "saved", "older-generation")
	if binding, err := resolveOmpActiveBinding(dir); err != nil || binding == nil {
		t.Fatalf("current-generation binding rejected: %+v, %v", binding, err)
	}

	// The execution-host selector runs after launch setup installs the *next*
	// generation. It must still resume the validated previous binding so the
	// extension can acknowledge the new generation on session_start.
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("next-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceBinding := filepath.Join(dir, ".agent-deck-source-binding.next-generation")
	writeOmpValidationBindingAt(t, dir, filepath.Base(sourceBinding), file, "session-a", "saved", "older-generation")
	if output, err := runOmpBindingSelection(t, dir,
		"AGENTDECK_OMP_LAUNCH_ID=next-generation",
		"AGENTDECK_OMP_SOURCE_BINDING="+sourceBinding,
		"AGENTDECK_OMP_SOURCE_ERROR=",
	); err != nil || !strings.Contains(output, file) {
		t.Fatalf("target selector incorrectly required the newly installed generation: %v\n%s", err, output)
	}
}

func TestOmpSourceSelectorFailsClosedOnSetupErrorsAndUnsafePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent-deck", "entry-a")
	file := filepath.Join(dir, "root.jsonl")
	writeOmpValidationTranscript(t, file, "session-a")

	if output, err := runOmpBindingSelection(t, dir,
		"AGENTDECK_OMP_LAUNCH_ID=next-generation",
		"AGENTDECK_OMP_SOURCE_BINDING="+filepath.Join(dir, ".agent-deck-source-binding.next-generation"),
		"AGENTDECK_OMP_SOURCE_ERROR=Previous OMP launch never acknowledged identity tracking",
	); err == nil || !strings.Contains(output, "never acknowledged") {
		t.Fatalf("source setup error did not fail visibly: %v\n%s", err, output)
	}
	if output, err := runOmpBindingSelection(t, dir,
		"AGENTDECK_OMP_LAUNCH_ID=../unsafe",
		"AGENTDECK_OMP_SOURCE_BINDING="+filepath.Join(dir, ".agent-deck-source-binding...unsafe"),
		"AGENTDECK_OMP_SOURCE_ERROR=",
	); err == nil || !strings.Contains(strings.ToLower(output), "generation") {
		t.Fatalf("unsafe launch generation was accepted: %v\n%s", err, output)
	}
	if output, err := runOmpBindingSelection(t, dir,
		"AGENTDECK_OMP_LAUNCH_ID=next-generation",
		"AGENTDECK_OMP_SOURCE_BINDING="+filepath.Join(filepath.Dir(dir), ".agent-deck-source-binding.next-generation"),
		"AGENTDECK_OMP_SOURCE_ERROR=",
	); err == nil || !strings.Contains(strings.ToLower(output), "source-binding path") {
		t.Fatalf("cross-row source-binding path was accepted: %v\n%s", err, output)
	}
}

func TestOmpBindingFreshBoundaryFollowsSelectedGeneration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent-deck", "entry-a")
	file := filepath.Join(dir, "root.jsonl")
	writeOmpValidationTranscript(t, file, "session-a")
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("current-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeOmpValidationBindingAt(t, dir, ompActiveBindingName+".current-generation", file, "session-a", "saved", "current-generation")
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-fresh-pending.old-generation"), []byte("wrong-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if binding, err := resolveOmpActiveBinding(dir); err != nil || binding == nil {
		t.Fatalf("stale prior-generation fresh marker blocked current binding: %+v, %v", binding, err)
	}

	sourceBinding := filepath.Join(dir, ".agent-deck-source-binding.next-generation")
	writeOmpValidationBindingAt(t, dir, filepath.Base(sourceBinding), file, "session-a", "saved", "old-generation")
	if output, err := runOmpBindingSelection(t, dir,
		"AGENTDECK_OMP_LAUNCH_ID=next-generation",
		"AGENTDECK_OMP_SOURCE_BINDING="+sourceBinding,
		"AGENTDECK_OMP_SOURCE_ERROR=",
	); err == nil || !strings.Contains(strings.ToLower(output), "fresh") {
		t.Fatalf("source snapshot ignored its prior-generation fresh boundary: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-deck-fresh-pending.old-generation"), []byte("old-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runOmpBindingSelection(t, dir,
		"AGENTDECK_OMP_LAUNCH_ID=next-generation",
		"AGENTDECK_OMP_SOURCE_BINDING="+sourceBinding,
		"AGENTDECK_OMP_SOURCE_ERROR=",
	); err != nil || !strings.Contains(output, file) {
		t.Fatalf("matching prior-generation fresh boundary rejected: %v\n%s", err, output)
	}
}

func TestResolveOmpBindingValidatesInferredLegacyOwnership(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "agent-deck")
	dir := filepath.Join(managed, "entry-a")
	sibling := filepath.Join(managed, "entry-b")
	file := filepath.Join(dir, "root.jsonl")
	siblingFile := filepath.Join(sibling, "root.jsonl")
	writeOmpValidationTranscript(t, file, "duplicate-id")
	writeOmpValidationTranscript(t, siblingFile, "duplicate-id")
	writeOmpValidationBinding(t, sibling, siblingFile, "duplicate-id", "saved", "sibling-generation")

	if _, err := resolveOmpActiveBinding(dir); err == nil || !strings.Contains(strings.ToLower(err.Error()), "another agent deck entry") {
		t.Fatalf("inferred legacy root bypassed sibling ownership validation: %v", err)
	}
}

func TestOmpBindingRejectsClaimHeldBySiblingSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "agent-deck")
	dir := filepath.Join(managed, "entry-a")
	sibling := filepath.Join(managed, "entry-b")
	file := filepath.Join(root, "moved", "active.jsonl")
	siblingFile := filepath.Join(sibling, "previous.jsonl")
	writeOmpValidationTranscript(t, file, "reserved-id")
	writeOmpValidationTranscript(t, siblingFile, "reserved-id")
	writeOmpValidationBinding(t, dir, file, "reserved-id", "saved", "entry-a-generation")
	if err := os.WriteFile(filepath.Join(sibling, ".agent-deck-launch-generation"), []byte("sibling-current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeOmpValidationBindingAt(t, sibling, ".agent-deck-source-binding.sibling-current", siblingFile, "reserved-id", "saved", "legacy")

	if _, err := readOmpActiveBinding(dir); err == nil || !strings.Contains(strings.ToLower(err.Error()), "another agent deck entry") {
		t.Fatalf("Go ignored UUID reserved by sibling source snapshot: %v", err)
	}
	if output, err := runOmpBindingSelection(t, dir); err == nil || !strings.Contains(strings.ToLower(output), "another agent deck entry") {
		t.Fatalf("target shell ignored UUID reserved by sibling source snapshot: %v\n%s", err, output)
	}
}
