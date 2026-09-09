package hub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

func TestOmpOwnerPreviewAndRestartExposeUnresolvedHistory(t *testing.T) {
	isolateHubActionConfig(t)
	t.Cleanup(testutil.IsolateTmuxSocket())
	const profile = "omp-owner-health"
	inst := session.NewInstanceWithTool("unresolved-omp", t.TempDir(), "omp")
	dir := filepath.Join(os.Getenv("HOME"), ".omp", "agent-deck", inst.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"original", "branch"} {
		if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", name)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	storage, err := session.NewStorageWithProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Save([]*session.Instance{inst}); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	backend := LocalActionBackend{Profile: profile}
	// The hub action reloads a fresh instance. It has never run UpdateStatus.
	preview, err := backend.Preview(context.Background(), inst.ID)
	if err != nil || !strings.Contains(preview, "original.jsonl") || !strings.Contains(preview, "branch.jsonl") {
		t.Fatalf("hub preview hid unresolved identities before Enter: %q %v", preview, err)
	}
	if err := backend.Restart(context.Background(), inst.ID); err == nil || !strings.Contains(err.Error(), "branch.jsonl") {
		t.Fatalf("hub restart hid actual preflight failure: %v", err)
	}
}
