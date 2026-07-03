package main

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestHandoverSessionCLIHelperCreatesStoppedCodexSession(t *testing.T) {
	source := session.NewInstanceWithGroupAndTool("source task", t.TempDir(), "work", "claude")
	source.Command = "claude"
	var saved [][]*session.Instance

	res, err := handoverSession("", handoverSessionOptions{
		Source: "source task",
		To:     "codex",
	}, handoverSessionDeps{
		load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
			return nil, []*session.Instance{source}, nil, nil
		},
		save: func(_ *session.Storage, instances []*session.Instance, _ []*session.GroupData) error {
			saved = append(saved, cloneImportInstances(instances))
			return nil
		},
		start: func(*session.Instance, string) error {
			t.Fatal("start should not be called without --start")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("handoverSession: %v", err)
	}
	if res.Source != source {
		t.Fatal("result source should be the resolved source")
	}
	if res.Target == nil {
		t.Fatal("result target is nil")
	}
	if len(saved) != 1 {
		t.Fatalf("save count = %d, want 1", len(saved))
	}
	target := saved[0][1]
	if target.Tool != "codex" || target.Command != "codex" {
		t.Fatalf("target Tool/Command = %q/%q, want codex/codex", target.Tool, target.Command)
	}
	if target.Status != session.StatusStopped {
		t.Fatalf("target Status = %q, want stopped", target.Status)
	}
	if target.Title != "source task (codex)" {
		t.Fatalf("target Title = %q", target.Title)
	}
	if res.Started {
		t.Fatal("Started = true without --start")
	}
}

func TestHandoverSessionCLIHelperStartPersistsBeforeAndAfterStarting(t *testing.T) {
	source := session.NewInstanceWithGroupAndTool("source task", t.TempDir(), "work", "claude")
	var events []string
	var deliveredPrompt string

	res, err := handoverSession("", handoverSessionOptions{
		Source:  source.ID,
		To:      "opencode",
		Message: "focus on failing tests",
		Start:   true,
	}, handoverSessionDeps{
		load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
			return nil, []*session.Instance{source}, nil, nil
		},
		save: func(_ *session.Storage, instances []*session.Instance, _ []*session.GroupData) error {
			if len(instances) != 2 {
				t.Fatalf("save saw %d instances, want 2", len(instances))
			}
			events = append(events, "save:"+string(instances[1].Status))
			return nil
		},
		start: func(inst *session.Instance, prompt string) error {
			events = append(events, "start")
			deliveredPrompt = prompt
			inst.Status = session.StatusStarting
			return nil
		},
	})
	if err != nil {
		t.Fatalf("handoverSession --start: %v", err)
	}
	if !res.Started {
		t.Fatal("Started = false, want true")
	}
	want := "save:stopped,start,save:starting"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	if !strings.Contains(deliveredPrompt, "focus on failing tests") {
		t.Fatalf("delivered prompt missing operator message:\n%s", deliveredPrompt)
	}
}

func TestHandoverSessionCLIHelperValidationErrors(t *testing.T) {
	source := session.NewInstanceWithGroupAndTool("source task", t.TempDir(), "work", "codex")

	_, err := handoverSession("", handoverSessionOptions{
		Source: source.ID,
		To:     "codex",
	}, handoverSessionDeps{
		load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
			return nil, []*session.Instance{source}, nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "same tool") {
		t.Fatalf("same-tool error = %v, want same-tool validation", err)
	}

	_, err = handoverSession("", handoverSessionOptions{
		Source: source.ID,
		To:     "unknown",
	}, handoverSessionDeps{
		load: func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error) {
			return nil, []*session.Instance{source}, nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "allowed targets are claude, codex, opencode, kiro") {
		t.Fatalf("unknown-target error = %v, want allowed-targets validation", err)
	}
}

func TestHandoverResultJSON(t *testing.T) {
	source := session.NewInstanceWithGroupAndTool("source", t.TempDir(), "grp", "claude")
	target := session.NewInstanceWithGroupAndTool("source (codex)", source.ProjectPath, "grp", "codex")
	res := &handoverSessionResult{
		Result:  &session.HandoverResult{Source: source, Target: target, Started: true, Warning: "careful"},
		Started: true,
	}

	payload := handoverResultJSON(res)
	if payload["source_id"] != source.ID || payload["target_id"] != target.ID ||
		payload["target_tool"] != "codex" || payload["started"] != true ||
		payload["warning"] != "careful" {
		t.Fatalf("payload = %#v", payload)
	}
}
