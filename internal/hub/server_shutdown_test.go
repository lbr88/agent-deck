package hub

import (
	"context"
	"path/filepath"
	"testing"
)

func TestServerShutdownBeforeServeIsSafe(t *testing.T) {
	server, err := NewServer(ServerConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before Serve: %v", err)
	}
	// A shutdown racing ahead of Serve must prevent a late listener from
	// starting. Bogus certificate paths prove Serve returns before bind/TLS.
	server.cfg.CertFile = filepath.Join(t.TempDir(), "missing.crt")
	server.cfg.KeyFile = filepath.Join(t.TempDir(), "missing.key")
	if err := server.Serve(); err != nil {
		t.Fatalf("Serve after Shutdown: %v", err)
	}
}
