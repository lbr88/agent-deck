package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"al.essio.dev/pkg/shellescape"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

// Reopen the actual SQLite file, without carrying any Instance or database
// connection across the simulated Agent Deck process restart.
func reopenOmpForkTestStorage(t *testing.T, storage *Storage) *Storage {
	t.Helper()
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(storage.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Storage{db: db, dbPath: storage.Path(), profile: "_test"}
}

func TestOmpPendingForkSQLiteReloadAfterUnsavedAckResumesChild(t *testing.T) {
	// TestMain supplies the isolated HOME and tmux server. Keep that HOME here
	// so the real Start/Restart panes see the same isolated provider files.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	parent := NewInstanceWithTool("omp-persist-parent", t.TempDir(), "omp")
	parentDir := filepath.Join(home, ".omp", "agent-deck", parent.ID)
	parentFile := filepath.Join(parentDir, "parent-root.jsonl")
	writeOmpValidationTranscript(t, parentFile, "persist-parent-id")
	writeOmpValidationBinding(t, parentDir, parentFile, "persist-parent-id", "saved", "parent-generation")
	t.Cleanup(func() { _ = os.RemoveAll(parentDir) })
	logFile := filepath.Join(t.TempDir(), "provider-invocations")
	parent.Command = writeOmpLaunchAckProbe(t, "persist-fork-probe", fmt.Sprintf(`
mode=
source_file=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --fork|--resume) mode=$1; shift; source_file=${1-} ;;
  esac
  shift
done
printf '%%s|%%s|%%s\n' "$mode" "$source_file" "$AGENTDECK_OMP_LAUNCH_ID" >> %s
session_file="$AGENTDECK_OMP_DIR/child-root.jsonl"
session_id="$AGENTDECK_INSTANCE_ID-child"
if [ "$mode" = --fork ]; then
  test -f "$source_file"
  test ! -e "$session_file"
  printf '{"type":"session","id":"%%s"}\n' "$session_id" > "$session_file"
  printf 'keep child artifact\n' > "$AGENTDECK_OMP_DIR/provider-artifact"
else
  test "$mode" = --resume
  test "$source_file" = "$session_file"
  test -f "$session_file"
fi
printf '1\n%%s\n%%s\nsaved\n%%s\n' "$session_file" "$session_id" "$AGENTDECK_OMP_LAUNCH_ID" > "$AGENTDECK_OMP_DIR/.agent-deck-active-session.$AGENTDECK_OMP_LAUNCH_ID"
printf '{"instance_id":"%%s","launch_id":"%%s","session_id":"%%s","session_file":"%%s","identity_ready":true,"error":""}\n' "$AGENTDECK_INSTANCE_ID" "$AGENTDECK_OMP_LAUNCH_ID" "$session_id" "$session_file" > "$AGENTDECK_OMP_DIR/.agent-deck-omp-status.$AGENTDECK_OMP_LAUNCH_ID.json"
sleep 10`, shellescape.Quote(logFile)))
	forked, recipe, err := parent.CreateForkedInstanceForTool("Durable child", "forks", nil)
	if err != nil {
		t.Fatal(err)
	}
	childDir := filepath.Join(home, ".omp", "agent-deck", forked.ID)
	t.Cleanup(func() { _ = os.RemoveAll(childDir) })
	storage := newTestStorage(t)
	if err := storage.Save([]*Instance{forked}); err != nil {
		t.Fatal(err)
	}
	var generations []string
	for attempt := 0; attempt < 2; attempt++ {
		storage = reopenOmpForkTestStorage(t, storage)
		loaded, err := storage.Load()
		if err != nil || len(loaded) != 1 {
			t.Fatalf("Load attempt %d: instances=%d err=%v", attempt, len(loaded), err)
		}
		child := loaded[0]
		t.Cleanup(func() { _ = child.KillAndWait() })
		if !child.IsForkAwaitingStart || child.ForkStartCommand != recipe {
			t.Fatalf("attempt %d lost the persisted pending fork recipe", attempt)
		}
		if attempt == 0 {
			err = child.Start()
		} else {
			err = child.Restart()
		}
		if err != nil {
			t.Fatalf("launch attempt %d: %v", attempt, err)
		}
		if child.IsForkAwaitingStart || child.ForkStartCommand != "" {
			t.Fatal("actual provider ACK did not clear in-memory pending intent")
		}
		generation, err := os.ReadFile(filepath.Join(childDir, ".agent-deck-launch-generation"))
		if err != nil {
			t.Fatal(err)
		}
		generations = append(generations, strings.TrimSpace(string(generation)))
		if err := child.KillAndWait(); err != nil {
			t.Fatal(err)
		}
		// Simulate a crash after the first successful provider ACK but before
		// Agent Deck saves it. The next process must safely replay the old recipe.
		if attempt == 1 {
			if err := storage.Save([]*Instance{child}); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "--fork|" + parentFile + "|" + generations[0] + "\n--resume|" + filepath.Join(childDir, "child-root.jsonl") + "|" + generations[1] + "\n"
	if string(data) != want || generations[0] == generations[1] {
		t.Fatalf("persisted retry did not resume the exact child with a fresh generation: %q, want %q", data, want)
	}
	if data, err := os.ReadFile(filepath.Join(childDir, "provider-artifact")); err != nil || string(data) != "keep child artifact\n" {
		t.Fatalf("persisted retry destroyed child evidence: %q, %v", data, err)
	}
	if _, err := os.Stat(parentFile); err != nil {
		t.Fatalf("persisted retry destroyed the parent history: %v", err)
	}
	storage = reopenOmpForkTestStorage(t, storage)
	loaded, err := storage.Load()
	if err != nil || len(loaded) != 1 {
		t.Fatalf("Load after acknowledged Save: instances=%d err=%v", len(loaded), err)
	}
	if loaded[0].IsForkAwaitingStart || loaded[0].ForkStartCommand != "" || loaded[0].Command != parent.Command {
		t.Fatal("normal Save resurrected pending intent after a successful ACK")
	}
}

func TestOmpPendingForkSurvivesSQLiteReopen(t *testing.T) {
	for _, method := range []string{"Save", "InsertSessionAndVerify"} {
		t.Run(method, func(t *testing.T) {
			parent, _, _, _, _ := makeOmpForkRecoveryFixture(t)
			forked, recipe, err := parent.CreateForkedInstanceForTool("Saved child", "forks", nil)
			if err != nil {
				t.Fatal(err)
			}
			if !forked.IsForkAwaitingStart || recipe == "" || forked.Command != "omp" {
				t.Fatal("factory did not create a deferred OMP fork")
			}
			storage := newTestStorage(t)
			if method == "Save" {
				err = storage.Save([]*Instance{forked})
			} else {
				err = storage.InsertSessionAndVerify(forked, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			storage = reopenOmpForkTestStorage(t, storage)
			loaded, err := storage.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 1 {
				t.Fatalf("loaded %d instances, want one saved fork", len(loaded))
			}
			child := loaded[0]
			if child.ID != forked.ID || child.Command != "omp" || !child.TitleLocked {
				t.Fatal("fork row metadata was not preserved")
			}
			if !child.IsForkAwaitingStart || child.ForkStartCommand != recipe {
				t.Fatalf("SQLite reload lost native fork intent: awaiting=%t recipe bytes=%d, want %d", child.IsForkAwaitingStart, len(child.ForkStartCommand), len(recipe))
			}
			lite, groups, err := storage.LoadLite()
			if err != nil {
				t.Fatal(err)
			}
			restored, _, err := storage.convertToInstances(&StorageData{Instances: lite, Groups: groups})
			if err != nil || len(restored) != 1 || !restored[0].IsForkAwaitingStart || restored[0].ForkStartCommand != recipe {
				t.Fatalf("LoadLite conversion lost pending intent: %v", err)
			}
		})
	}
}

func TestOmpPendingForkToolDataPreservesExactRecipeAndExtras(t *testing.T) {
	recipe := "\n AGENTDECK_INSTANCE_ID='child' bash -c 'printf \"%s\" \"${value:-$$}\"'\n"
	inst := &Instance{Tool: "omp", IsForkAwaitingStart: true, ForkStartCommand: recipe}
	existing := json.RawMessage(`{"other_extension":{"enabled":true}}`)
	pending := writeOmpPendingForkToToolData(existing, inst)
	if got := readOmpPendingForkFromToolData(pending, "omp"); got != recipe {
		t.Fatalf("recipe bytes changed: got %q want %q", got, recipe)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(pending, &fields); err != nil || string(fields["other_extension"]) != `{"enabled":true}` {
		t.Fatalf("writing fork intent lost unrelated extras: %s, %v", pending, err)
	}
	inst.IsForkAwaitingStart = false
	inst.ForkStartCommand = ""
	cleared := statedb.MergeToolDataExtras(pending, writeOmpPendingForkToToolData(nil, inst))
	if err := json.Unmarshal(cleared, &fields); err != nil || string(fields[toolDataOmpPendingForkCommandKey]) != `""` {
		t.Fatalf("acknowledged recipe did not write an explicit clear: %s, %v", cleared, err)
	}
	if string(fields["other_extension"]) != `{"enabled":true}` {
		t.Fatalf("clearing pending intent lost unrelated extras: %s", cleared)
	}
}

func TestOmpPendingForkPersistenceLeavesOtherProvidersTransient(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "pi", "opencode", "shell"} {
		t.Run(tool, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			inst := NewInstance("unchanged-"+tool, t.TempDir())
			inst.Tool = tool
			inst.Command = tool
			inst.IsForkAwaitingStart = true
			inst.ForkStartCommand = "provider-specific transient recipe"
			storage := newTestStorage(t)
			if err := storage.Save([]*Instance{inst}); err != nil {
				t.Fatal(err)
			}
			storage = reopenOmpForkTestStorage(t, storage)
			loaded, err := storage.Load()
			if err != nil || len(loaded) != 1 {
				t.Fatalf("Load: instances=%d err=%v", len(loaded), err)
			}
			if loaded[0].IsForkAwaitingStart || loaded[0].ForkStartCommand != "" || loaded[0].Command != tool {
				t.Fatal("OMP persistence changed another provider's fork handling")
			}
			existing := json.RawMessage(`{"omp_pending_fork_command":"old OMP recipe","other":true}`)
			if got := writeOmpPendingForkToToolData(existing, inst); string(got) != string(existing) {
				t.Fatalf("non-OMP writer changed extras: %s", got)
			}
			if got := readOmpPendingForkFromToolData(existing, tool); got != "" {
				t.Fatalf("non-OMP reader restored an OMP recipe: %q", got)
			}
		})
	}
}
