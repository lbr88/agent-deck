package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ompRemoteBootstrapFixture(t *testing.T) (dir, active, historical, breadcrumbs string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, ".omp", "agent-deck", "entry with 'quotes' $literal")
	active = filepath.Join(dir, "active.jsonl")
	historical = filepath.Join(dir, "historical.jsonl")
	writeOmpValidationTranscript(t, active, "active-id")
	writeOmpValidationTranscript(t, historical, "historical-id")
	breadcrumbs = filepath.Join(root, ".omp", "agent", "terminal-sessions")
	if err := os.MkdirAll(breadcrumbs, 0o700); err != nil {
		t.Fatal(err)
	}
	return
}

func writeOmpRemoteBreadcrumb(t *testing.T, path, transcript string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("recycled-terminal-id\n"+transcript+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Removing target-side corroboration must fail here even though local Go can
// still bootstrap the same entry: this executes the actual target selector.
func TestOmpRemoteBootstrapSelectsUniqueEntryScopedBreadcrumb(t *testing.T) {
	for _, sourceSetup := range []bool{false, true} {
		name := "legacy directory"
		if sourceSetup {
			name = "first tracking launch after setup"
		}
		t.Run(name, func(t *testing.T) {
			dir, active, historical, breadcrumbs := ompRemoteBootstrapFixture(t)
			writeOmpRemoteBreadcrumb(t, filepath.Join(breadcrumbs, "first-terminal"), active)
			// Two terminal records can corroborate the same root; only conflicting
			// root paths are ambiguous, not the number of terminal identifiers.
			writeOmpRemoteBreadcrumb(t, filepath.Join(breadcrumbs, "second-terminal"), active)
			writeOmpRemoteBreadcrumb(t, filepath.Join(breadcrumbs, "unrelated-terminal"), filepath.Join(filepath.Dir(dir), "sibling", "unrelated.jsonl"))
			local, err := resolveOmpActiveBinding(dir)
			if err != nil || local == nil || local.File != active {
				t.Fatalf("local bootstrap did not select expected root: %+v, %v", local, err)
			}
			var env []string
			if sourceSetup {
				if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("first-generation\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				env = []string{
					"AGENTDECK_OMP_LAUNCH_ID=first-generation",
					"AGENTDECK_OMP_SOURCE_BINDING=" + filepath.Join(dir, ".agent-deck-source-binding.first-generation"),
					"AGENTDECK_OMP_SOURCE_ERROR=",
				}
			}
			output, err := runOmpBindingSelection(t, dir, env...)
			if err != nil || output != active+"\n1\n" {
				t.Fatalf("target bootstrap disagrees with local active root: %v\n%s", err, output)
			}
			for _, file := range []string{active, historical} {
				if data, err := os.ReadFile(file); err != nil || !strings.Contains(string(data), `"title":"kept"`) {
					t.Fatalf("bootstrap changed native history %s: %q, %v", file, data, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, ompActiveBindingName)); !os.IsNotExist(err) {
				t.Fatalf("target selector must not manufacture an acknowledgment: %v", err)
			}
		})
	}
}

func TestOmpRemoteBootstrapDoesNotInferFromAmbiguousOrUnusableBreadcrumbs(t *testing.T) {
	for _, kind := range []string{"none", "conflicting", "cross-row", "nested task", "non-exact path", "oversized", "symlink", "wrong line", "NUL in path"} {
		t.Run(kind, func(t *testing.T) {
			dir, active, historical, breadcrumbs := ompRemoteBootstrapFixture(t)
			breadcrumb := filepath.Join(breadcrumbs, "terminal")
			switch kind {
			case "conflicting":
				writeOmpRemoteBreadcrumb(t, breadcrumb, active)
				writeOmpRemoteBreadcrumb(t, filepath.Join(breadcrumbs, "other-terminal"), historical)
			case "cross-row":
				writeOmpRemoteBreadcrumb(t, breadcrumb, filepath.Join(filepath.Dir(dir), "sibling", "active.jsonl"))
			case "nested task":
				nested := filepath.Join(dir, "active", "task.jsonl")
				writeOmpValidationTranscript(t, nested, "task-id")
				writeOmpRemoteBreadcrumb(t, breadcrumb, nested)
			case "non-exact path":
				writeOmpRemoteBreadcrumb(t, breadcrumb, dir+"/./active.jsonl")
			case "oversized":
				if err := os.WriteFile(breadcrumb, []byte("terminal\n"+active+"\n"+strings.Repeat("x", 20*1024)), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outside := filepath.Join(filepath.Dir(breadcrumbs), "outside-breadcrumb")
				writeOmpRemoteBreadcrumb(t, outside, active)
				if err := os.Symlink(outside, breadcrumb); err != nil {
					t.Fatal(err)
				}
			case "wrong line":
				if err := os.WriteFile(breadcrumb, []byte(active+"\nnot-a-root\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "NUL in path":
				writeOmpRemoteBreadcrumb(t, breadcrumb, active+"\x00")
			}
			if local, err := resolveOmpActiveBinding(dir); err == nil || local != nil {
				t.Fatalf("local bootstrap unexpectedly inferred a root: %+v, %v", local, err)
			}
			output, err := runOmpBindingSelection(t, dir)
			// The selector preserves the ambiguous count so each existing caller
			// can retain its resume/fork-specific error and candidate listing.
			if err == nil && !strings.HasSuffix(output, "\n2\n") {
				t.Fatalf("target inferred an active root from %s evidence: %q", kind, output)
			}
		})
	}
}

func TestOmpRemoteBootstrapValidatesCorroboratedRoot(t *testing.T) {
	for _, kind := range []string{"invalid header", "sibling ownership", "physical cross-row"} {
		t.Run(kind, func(t *testing.T) {
			dir, active, _, breadcrumbs := ompRemoteBootstrapFixture(t)
			writeOmpRemoteBreadcrumb(t, filepath.Join(breadcrumbs, "terminal"), active)
			switch kind {
			case "invalid header":
				if err := os.WriteFile(active, []byte("{\"type\":\"title\",\"title\":\"no session\"}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "sibling ownership":
				sibling := filepath.Join(filepath.Dir(dir), "sibling")
				siblingFile := filepath.Join(sibling, "sibling.jsonl")
				writeOmpValidationTranscript(t, siblingFile, "active-id")
				writeOmpValidationBinding(t, sibling, siblingFile, "active-id", "saved", "sibling-generation")
			case "physical cross-row":
				physicalDir := dir + "-physical"
				if err := os.Rename(dir, physicalDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(physicalDir, dir); err != nil {
					t.Fatal(err)
				}
			}
			if local, err := resolveOmpActiveBinding(dir); err == nil || local != nil {
				t.Fatalf("local bootstrap accepted unsafe root: %+v, %v", local, err)
			}
			if output, err := runOmpBindingSelection(t, dir); err == nil {
				t.Fatalf("target bootstrap bypassed %s validation: %s", kind, output)
			}
		})
	}
}

func TestOmpRemoteBootstrapCannotBypassTrackingOrFreshBoundary(t *testing.T) {
	for _, kind := range []string{"missing current acknowledgment", "pending fresh", "setup rejected previous generation"} {
		t.Run(kind, func(t *testing.T) {
			dir, active, _, breadcrumbs := ompRemoteBootstrapFixture(t)
			writeOmpRemoteBreadcrumb(t, filepath.Join(breadcrumbs, "terminal"), active)
			var env []string
			switch kind {
			case "missing current acknowledgment":
				if err := os.WriteFile(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("unacknowledged\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "pending fresh":
				if err := os.WriteFile(filepath.Join(dir, ".agent-deck-fresh-pending"), []byte("pending\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "setup rejected previous generation":
				env = []string{
					"AGENTDECK_OMP_LAUNCH_ID=next-generation",
					"AGENTDECK_OMP_SOURCE_BINDING=" + filepath.Join(dir, ".agent-deck-source-binding.next-generation"),
					"AGENTDECK_OMP_SOURCE_ERROR=Previous OMP launch never acknowledged identity tracking",
				}
			}
			if output, err := runOmpBindingSelection(t, dir, env...); err == nil || !strings.Contains(strings.ToLower(output), "history preserved") {
				t.Fatalf("breadcrumb bypassed %s guard: %v\n%s", kind, err, output)
			}
		})
	}
}

func TestOmpRemoteBootstrapRejectsUnboundTranscriptSymlink(t *testing.T) {
	managed := filepath.Join(t.TempDir(), ".omp", "agent-deck")
	dir := filepath.Join(managed, "entry-a")
	siblingFile := filepath.Join(managed, "entry-b", "root.jsonl")
	writeOmpValidationTranscript(t, siblingFile, "sibling-id")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "root.jsonl")
	if err := os.Symlink(siblingFile, link); err != nil {
		t.Fatal(err)
	}
	if output, err := runOmpBindingSelection(t, dir); err == nil || !strings.Contains(strings.ToLower(output), "history preserved") {
		t.Fatalf("unbound symlink was accepted as an owned transcript: %v\n%s", err, output)
	}
	if destination, err := os.Readlink(link); err != nil || destination != siblingFile {
		t.Fatalf("selector altered symlink evidence: %q, %v", destination, err)
	}
	if data, err := os.ReadFile(siblingFile); err != nil || !strings.Contains(string(data), "sibling-id") {
		t.Fatalf("selector altered sibling transcript: %q, %v", data, err)
	}
}
