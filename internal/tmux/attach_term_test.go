//go:build !windows
// +build !windows

package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestEnsureSaneAttachTERMEnv(t *testing.T) {
	const fallback = "TERM=xterm-256color"

	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"missing", []string{"PATH=/usr/bin", "HOME=/home/x"}, fallback},
		{"empty", []string{"TERM=", "PATH=/usr/bin"}, fallback},
		{"whitespace", []string{"TERM=  \t", "PATH=/usr/bin"}, fallback},
		{"dumb", []string{"TERM=dumb", "PATH=/usr/bin"}, fallback},
		{"unknown", []string{"TERM=unknown", "PATH=/usr/bin"}, fallback},
		{"uppercase unknown", []string{"TERM=UNKNOWN", "PATH=/usr/bin"}, fallback},
		{"sane inherited", []string{"TERM=xterm-kitty", "PATH=/usr/bin"}, "TERM=xterm-kitty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EnsureSaneAttachTERMEnv(tc.env)
			n, term := countTERMEntries(got)
			if n != 1 || term != tc.want {
				t.Fatalf("TERM entries = %d last=%q, want exactly one %q (env=%v)", n, term, tc.want, got)
			}
		})
	}
}

func TestEnsureSaneAttachTERMEnvDropsDuplicateTERMEntries(t *testing.T) {
	got := EnsureSaneAttachTERMEnv([]string{"TERM=dumb", "PATH=/usr/bin", "TERM=unknown"})
	n, term := countTERMEntries(got)
	if n != 1 || term != "TERM=xterm-256color" {
		t.Fatalf("duplicate TERM entries not normalized: n=%d term=%q env=%v", n, term, got)
	}
}

func TestEnsureSaneAttachTERMMaterializesNilEnv(t *testing.T) {
	t.Setenv("TERM", "dumb")
	cmd := exec.Command("tmux", "attach-session", "-t", "sess")
	if cmd.Env != nil {
		t.Fatalf("new command Env = %v, want nil before normalization", cmd.Env)
	}
	EnsureSaneAttachTERM(cmd)
	n, term := countTERMEntries(cmd.Env)
	if n != 1 || term != "TERM=xterm-256color" {
		t.Fatalf("materialized TERM entries = %d last=%q env=%v", n, term, cmd.Env)
	}
}

func TestSessionAttachCommandsForceSaneTERM(t *testing.T) {
	t.Setenv("TERM", "dumb")
	s := &Session{Name: "agentdeck_term_abc", SocketName: "agentdeck"}

	for _, cmd := range []*exec.Cmd{
		s.attachCmd(context.Background()),
		s.attachReadOnlyCmd(context.Background()),
	} {
		n, term := countTERMEntries(cmd.Env)
		if n != 1 || term != "TERM=xterm-256color" {
			t.Fatalf("%v TERM entries = %d last=%q env=%v", cmd.Args, n, term, cmd.Env)
		}
	}
}

func countTERMEntries(env []string) (n int, last string) {
	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			n++
			last = kv
		}
	}
	return n, last
}
