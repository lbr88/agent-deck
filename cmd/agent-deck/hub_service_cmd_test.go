package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGenerateHubConnectSystemdUserUnit(t *testing.T) {
	unit, err := generateHubConnectSystemdUnit(hubServiceUnitSpec{
		Scope:         hubServiceScopeUser,
		Profile:       "work laptop",
		UnitName:      "agent-deck-hub-connect-work.service",
		AgentDeckPath: "/opt/Agent Deck/agent-deck",
		HomeDir:       "/home/alice",
		XDGConfigHome: "/home/alice/.config",
		XDGDataHome:   "/home/alice/.local/share",
		PathEnv:       "/opt/Agent Deck:/usr/bin",
	})
	if err != nil {
		t.Fatalf("generateHubConnectSystemdUnit: %v", err)
	}
	for _, want := range []string{
		"Description=Agent Deck Hub connector (profile work laptop)",
		`ExecStart="/opt/Agent Deck/agent-deck" --profile "work laptop" hub connect`,
		"Restart=always",
		"RestartSec=5s",
		`Environment="AGENTDECK_PROFILE=work laptop"`,
		`Environment="HOME=/home/alice"`,
		`Environment="XDG_CONFIG_HOME=/home/alice/.config"`,
		`Environment="XDG_DATA_HOME=/home/alice/.local/share"`,
		`Environment="PATH=/opt/Agent Deck:/usr/bin"`,
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("user unit missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "\nUser=") {
		t.Fatalf("user unit must not set User=:\n%s", unit)
	}
}

func TestGenerateHubConnectSystemdSystemUnitIncludesRunUser(t *testing.T) {
	unit, err := generateHubConnectSystemdUnit(hubServiceUnitSpec{
		Scope:         hubServiceScopeSystem,
		Profile:       "default",
		UnitName:      "agent-deck-hub-connect.service",
		AgentDeckPath: "/usr/local/bin/agent-deck",
		RunUser:       "alice",
		HomeDir:       "/home/alice",
		XDGConfigHome: "/home/alice/.config",
		XDGDataHome:   "/home/alice/.local/share",
		PathEnv:       "/usr/local/bin:/usr/bin",
	})
	if err != nil {
		t.Fatalf("generateHubConnectSystemdUnit: %v", err)
	}
	for _, want := range []string{
		"User=alice",
		`ExecStart="/usr/local/bin/agent-deck" --profile "default" hub connect`,
		`Environment="HOME=/home/alice"`,
		`Environment="XDG_CONFIG_HOME=/home/alice/.config"`,
		`Environment="XDG_DATA_HOME=/home/alice/.local/share"`,
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("system unit missing %q:\n%s", want, unit)
		}
	}
}

func TestDefaultHubConnectServiceUnitNameIncludesProfile(t *testing.T) {
	if got, want := defaultHubConnectServiceUnitName("default"), "agent-deck-hub-connect.service"; got != want {
		t.Fatalf("default unit = %q, want %q", got, want)
	}
	if got, want := defaultHubConnectServiceUnitName("work/laptop"), "agent-deck-hub-connect-work-laptop.service"; got != want {
		t.Fatalf("profile unit = %q, want %q", got, want)
	}
}

func TestInstallHubConnectSystemdServiceWritesUserUnitAndRunsSystemctl(t *testing.T) {
	tmp := t.TempDir()
	var calls [][]string
	restore := replaceHubServiceSystemctl(func(ctx context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	})
	defer restore()

	var out bytes.Buffer
	err := installHubConnectSystemdService(context.Background(), hubServiceOptions{
		Scope:         hubServiceScopeUser,
		Profile:       "work",
		UnitName:      "agent-deck-hub-connect-work.service",
		AgentDeckPath: "/usr/local/bin/agent-deck",
		UnitDir:       tmp,
		HomeDir:       "/home/alice",
		XDGConfigHome: "/home/alice/.config",
		XDGDataHome:   "/home/alice/.local/share",
		Enable:        true,
		Start:         true,
		Stdout:        &out,
	})
	if err != nil {
		t.Fatalf("installHubConnectSystemdService: %v", err)
	}

	unitPath := filepath.Join(tmp, "agent-deck-hub-connect-work.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if !strings.Contains(string(data), `--profile "work" hub connect`) {
		t.Fatalf("unit did not run hub connect with work profile:\n%s", string(data))
	}
	wantCalls := [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", "agent-deck-hub-connect-work.service"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("systemctl calls = %#v, want %#v", calls, wantCalls)
	}
	if !strings.Contains(out.String(), "Installed "+unitPath) {
		t.Fatalf("install output = %q, want unit path", out.String())
	}
}

func TestInstallHubConnectSystemdServiceDryRunDoesNotWriteOrStart(t *testing.T) {
	tmp := t.TempDir()
	var calls [][]string
	restore := replaceHubServiceSystemctl(func(ctx context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	})
	defer restore()

	var out bytes.Buffer
	err := installHubConnectSystemdService(context.Background(), hubServiceOptions{
		Scope:         hubServiceScopeSystem,
		Profile:       "default",
		UnitName:      "agent-deck-hub-connect.service",
		AgentDeckPath: "/usr/local/bin/agent-deck",
		UnitDir:       tmp,
		RunUser:       "alice",
		HomeDir:       "/home/alice",
		DryRun:        true,
		Stdout:        &out,
	})
	if err != nil {
		t.Fatalf("install dry-run: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run systemctl calls = %#v, want none", calls)
	}
	if _, err := os.Stat(filepath.Join(tmp, "agent-deck-hub-connect.service")); !os.IsNotExist(err) {
		t.Fatalf("dry-run unit stat = %v, want not exist", err)
	}
	if !strings.Contains(out.String(), "User=alice") || !strings.Contains(out.String(), "hub connect") {
		t.Fatalf("dry-run output missing system unit:\n%s", out.String())
	}
}

func TestUninstallHubConnectSystemdServiceRemovesUnitAndReloads(t *testing.T) {
	tmp := t.TempDir()
	unitPath := filepath.Join(tmp, "agent-deck-hub-connect.service")
	if err := os.WriteFile(unitPath, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}
	var calls [][]string
	restore := replaceHubServiceSystemctl(func(ctx context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	})
	defer restore()

	var out bytes.Buffer
	err := uninstallHubConnectSystemdService(context.Background(), hubServiceOptions{
		Scope:         hubServiceScopeSystem,
		Profile:       "default",
		UnitName:      "agent-deck-hub-connect.service",
		AgentDeckPath: "/usr/local/bin/agent-deck",
		UnitDir:       tmp,
		RunUser:       "alice",
		Stdout:        &out,
	})
	if err != nil {
		t.Fatalf("uninstallHubConnectSystemdService: %v", err)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("unit stat = %v, want removed", err)
	}
	wantCalls := [][]string{
		{"disable", "--now", "agent-deck-hub-connect.service"},
		{"daemon-reload"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("systemctl calls = %#v, want %#v", calls, wantCalls)
	}
	if !strings.Contains(out.String(), "Removed "+unitPath) {
		t.Fatalf("uninstall output = %q, want removed path", out.String())
	}
}

func TestStatusHubConnectSystemdServiceUsesScopeAndUnit(t *testing.T) {
	var calls [][]string
	restore := replaceHubServiceSystemctl(func(ctx context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	})
	defer restore()

	err := statusHubConnectSystemdService(context.Background(), hubServiceOptions{
		Scope:         hubServiceScopeUser,
		Profile:       "work",
		UnitName:      "agent-deck-hub-connect-work.service",
		AgentDeckPath: "/usr/local/bin/agent-deck",
		UnitDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("statusHubConnectSystemdService: %v", err)
	}
	wantCalls := [][]string{{"--user", "status", "agent-deck-hub-connect-work.service"}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("systemctl calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestInstallHubConnectSystemdServiceRejectsNonLinux(t *testing.T) {
	prev := hubServiceRuntimeGOOS
	hubServiceRuntimeGOOS = "darwin"
	t.Cleanup(func() { hubServiceRuntimeGOOS = prev })

	err := installHubConnectSystemdService(context.Background(), hubServiceOptions{
		Scope:         hubServiceScopeUser,
		Profile:       "default",
		UnitName:      "agent-deck-hub-connect.service",
		AgentDeckPath: "/usr/local/bin/agent-deck",
		UnitDir:       t.TempDir(),
		DryRun:        true,
	})
	if err == nil || !strings.Contains(err.Error(), "systemd") {
		t.Fatalf("install error = %v, want systemd/Linux error", err)
	}
}

func TestParseHubServiceInstallOptionsSystemAlias(t *testing.T) {
	opts, err := parseHubServiceInstallOptions("default", []string{
		"--global",
		"--profile", "ops",
		"--agent-deck", "/bin/agent-deck",
		"--unit-dir", "/tmp/systemd",
		"--run-user", "deploy",
		"--home", "/home/deploy",
		"--no-start",
	})
	if err != nil {
		t.Fatalf("parseHubServiceInstallOptions: %v", err)
	}
	if opts.Scope != hubServiceScopeSystem || opts.Profile != "ops" || opts.RunUser != "deploy" {
		t.Fatalf("parsed opts = %+v", opts)
	}
	if opts.Start {
		t.Fatalf("Start = true, want false after --no-start")
	}
	if !opts.Enable {
		t.Fatalf("Enable = false, want true by default")
	}
}

func TestParseHubServiceInstallOptionsHelpReturnsErrHelp(t *testing.T) {
	_, err := parseHubServiceInstallOptions("default", []string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parse --help error = %v, want flag.ErrHelp", err)
	}
}

func replaceHubServiceSystemctl(fn func(context.Context, ...string) error) func() {
	prev := runHubServiceSystemctl
	runHubServiceSystemctl = fn
	return func() { runHubServiceSystemctl = prev }
}
