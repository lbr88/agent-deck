package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"al.essio.dev/pkg/shellescape"
)

func TestOmpExecutionHostIdentityACK(t *testing.T) {
	for _, route := range []string{"ssh", "docker"} {
		for _, state := range []string{"loading", "saved", "pending", "title-warning", "identity-error", "wrong-id", "missing-binding", "malformed-status", "empty-status", "NUL-status", "NUL-generation", "trailing-generation", "missing-newline-generation", "extra-newline-generation"} {
			t.Run(route+"/"+state, func(t *testing.T) {
				targetHome := t.TempDir()
				const id, generation = "remote-ack", "current-ack-generation"
				dir := filepath.Join(targetHome, ".omp", "agent-deck", id)
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				write := func(file string, data []byte) {
					t.Helper()
					if err := os.WriteFile(file, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				write(filepath.Join(dir, ".agent-deck-launch-generation"), []byte(generation+"\n"))
				if state == "NUL-generation" {
					write(filepath.Join(dir, ".agent-deck-launch-generation"), []byte("current-ack-\x00generation\n"))
				} else if state == "trailing-generation" {
					write(filepath.Join(dir, ".agent-deck-launch-generation"), []byte(generation+"\ntrailing-fragment"))
				} else if state == "missing-newline-generation" {
					write(filepath.Join(dir, ".agent-deck-launch-generation"), []byte(generation))
				} else if state == "extra-newline-generation" {
					write(filepath.Join(dir, ".agent-deck-launch-generation"), []byte(generation+"\n\n"))
				}
				file := filepath.Join(dir, "current.jsonl")
				bindingState := "saved"
				if state == "pending" {
					bindingState = "pending"
				} else {
					writeOmpValidationTranscript(t, file, "current-id")
				}
				if state != "loading" && state != "identity-error" && state != "missing-binding" {
					writeOmpValidationBindingAt(t, dir, ompActiveBindingName+"."+generation, file, "current-id", bindingState, generation)
				}
				status := ompIdentityStatus{InstanceID: id, LaunchID: generation, SessionID: "current-id", SessionFile: file, IdentityReady: true}
				if state == "title-warning" {
					status.Error = "title warning; identity remains valid"
				} else if state == "identity-error" {
					status.IdentityReady = false
					status.Error = "provider tracking failed"
				} else if state == "wrong-id" {
					status.SessionID = "different-id"
				}
				if state != "loading" {
					data, err := json.MarshalIndent(status, "", "  ")
					if err != nil {
						t.Fatal(err)
					}
					if state == "malformed-status" {
						data = []byte("{ broken")
					} else if state == "empty-status" {
						data = nil
					} else if state == "NUL-status" {
						data = append(data, 0)
					}
					write(filepath.Join(dir, ".agent-deck-omp-status."+generation+".json"), data)
				}
				bin := t.TempDir()
				wrapper := "#!/bin/bash\nexec env HOME=" + shellescape.Quote(targetHome) + " bash -c \"${!#}\"\n"
				if err := os.WriteFile(filepath.Join(bin, route), []byte(wrapper), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				host, container := "", ""
				if route == "ssh" {
					host = "isolated-ack-target"
				} else {
					container = "isolated-ack-container"
				}
				got := readOmpTargetIdentityHealth(id, host, container)
				if got.Generation != generation && !strings.HasSuffix(state, "-generation") {
					t.Fatalf("lost exact target launch generation: %+v", got)
				}
				switch state {
				case "loading":
					if got.Status != nil || got.Warning != "" {
						t.Fatalf("missing ACK must remain loading: %+v", got)
					}
				case "saved", "pending", "title-warning":
					if got.Status == nil || !got.Status.IdentityReady || got.Warning != "" || got.Status.SessionID != "current-id" || got.Status.Error != status.Error {
						t.Fatalf("valid target identity rejected: %+v", got)
					}
				case "identity-error":
					if got.Status == nil || got.Status.IdentityReady || !strings.Contains(got.Status.Error, "provider tracking failed") {
						t.Fatalf("provider negative ACK hidden: %+v", got)
					}
				default:
					if got.Warning == "" {
						t.Fatalf("invalid target ACK accepted: %+v", got)
					}
				}
			})
		}
	}
}
