package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type hubServiceScope string

const (
	hubServiceScopeUser   hubServiceScope = "user"
	hubServiceScopeSystem hubServiceScope = "system"
)

const (
	defaultHubServiceRestartSec = "5s"
	defaultHubServiceUnitBase   = "agent-deck-hub-connect"
)

type hubServiceOptions struct {
	Scope         hubServiceScope
	Profile       string
	UnitName      string
	AgentDeckPath string
	UnitDir       string
	RunUser       string
	HomeDir       string
	XDGConfigHome string
	XDGDataHome   string
	Enable        bool
	Start         bool
	DryRun        bool
	Stdout        io.Writer
}

type hubServiceUnitSpec struct {
	Scope         hubServiceScope
	Profile       string
	UnitName      string
	AgentDeckPath string
	RunUser       string
	HomeDir       string
	XDGConfigHome string
	XDGDataHome   string
	PathEnv       string
	RestartSec    string
}

var runHubServiceSystemctl = func(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var hubServiceRuntimeGOOS = runtime.GOOS

func handleHubService(profile string, args []string) error {
	if len(args) == 0 {
		printHubServiceUsage(os.Stderr)
		return fmt.Errorf("hub service subcommand is required")
	}
	switch args[0] {
	case "install":
		return handleHubServiceInstall(profile, args[1:])
	case "uninstall", "remove":
		return handleHubServiceUninstall(profile, args[1:])
	case "status":
		return handleHubServiceStatus(profile, args[1:])
	case "help", "--help", "-h":
		printHubServiceUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown hub service command %q", args[0])
	}
}

func handleHubServiceInstall(profile string, args []string) error {
	opts, err := parseHubServiceInstallOptions(profile, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return installHubConnectSystemdService(context.Background(), opts)
}

func handleHubServiceUninstall(profile string, args []string) error {
	opts, err := parseHubServiceBaseOptions("hub service uninstall", profile, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return uninstallHubConnectSystemdService(context.Background(), opts)
}

func handleHubServiceStatus(profile string, args []string) error {
	opts, err := parseHubServiceBaseOptions("hub service status", profile, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return statusHubConnectSystemdService(context.Background(), opts)
}

func parseHubServiceInstallOptions(profile string, args []string) (hubServiceOptions, error) {
	opts := defaultHubServiceOptions(profile)
	fs := newHubServiceFlagSet("hub service install")
	flagPtrs := addHubServiceFlags(fs, &opts)
	noEnable := fs.Bool("no-enable", false, "Write the unit but do not enable it")
	noStart := fs.Bool("no-start", false, "Write the unit but do not start it")
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return hubServiceOptions{}, err
		}
		return hubServiceOptions{}, err
	}
	if fs.NArg() != 0 {
		return hubServiceOptions{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if err := applyHubServiceFlags(&opts, flagPtrs); err != nil {
		return hubServiceOptions{}, err
	}
	if err := finalizeHubServiceOptions(&opts); err != nil {
		return hubServiceOptions{}, err
	}
	opts.Enable = !*noEnable
	opts.Start = !*noStart
	return opts, nil
}

func parseHubServiceBaseOptions(name, profile string, args []string) (hubServiceOptions, error) {
	opts := defaultHubServiceOptions(profile)
	fs := newHubServiceFlagSet(name)
	flagPtrs := addHubServiceFlags(fs, &opts)
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return hubServiceOptions{}, err
		}
		return hubServiceOptions{}, err
	}
	if fs.NArg() != 0 {
		return hubServiceOptions{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if err := applyHubServiceFlags(&opts, flagPtrs); err != nil {
		return hubServiceOptions{}, err
	}
	if err := finalizeHubServiceOptions(&opts); err != nil {
		return hubServiceOptions{}, err
	}
	return opts, nil
}

func defaultHubServiceOptions(profile string) hubServiceOptions {
	return hubServiceOptions{
		Profile: strings.TrimSpace(profile),
		Scope:   hubServiceScopeUser,
		Enable:  true,
		Start:   true,
		Stdout:  os.Stdout,
	}
}

type hubServiceFlagPointers struct {
	userScope     *bool
	systemScope   *bool
	globalScope   *bool
	profile       *string
	unitName      *string
	agentDeckPath *string
	unitDir       *string
	runUser       *string
	homeDir       *string
	xdgConfigHome *string
	xdgDataHome   *string
	dryRun        *bool
}

func newHubServiceFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: agent-deck hub service %s [--user|--system] [options]\n", strings.TrimPrefix(name, "hub service "))
		fs.PrintDefaults()
	}
	return fs
}

func addHubServiceFlags(fs *flag.FlagSet, opts *hubServiceOptions) hubServiceFlagPointers {
	return hubServiceFlagPointers{
		userScope:     fs.Bool("user", false, "Install/manage a per-user systemd service (default)"),
		systemScope:   fs.Bool("system", false, "Install/manage a system-wide systemd service"),
		globalScope:   fs.Bool("global", false, "Alias for --system"),
		profile:       fs.String("profile", opts.Profile, "Agent Deck profile the service should connect"),
		unitName:      fs.String("unit", "", "Systemd unit name (default derives from profile)"),
		agentDeckPath: fs.String("agent-deck", "", "Path to the agent-deck executable"),
		unitDir:       fs.String("unit-dir", "", "Override systemd unit directory"),
		runUser:       fs.String("run-user", "", "User= for --system services (default: sudo user or current user)"),
		homeDir:       fs.String("home", "", "HOME exported to the service"),
		xdgConfigHome: fs.String("xdg-config-home", "", "XDG_CONFIG_HOME exported to the service"),
		xdgDataHome:   fs.String("xdg-data-home", "", "XDG_DATA_HOME exported to the service"),
		dryRun:        fs.Bool("dry-run", false, "Print the unit instead of writing or calling systemctl"),
	}
}

func applyHubServiceFlags(opts *hubServiceOptions, ptrs hubServiceFlagPointers) error {
	if ptrs.systemScope != nil && (*ptrs.systemScope || *ptrs.globalScope) {
		opts.Scope = hubServiceScopeSystem
	}
	if ptrs.userScope != nil && *ptrs.userScope && ((*ptrs.systemScope) || (*ptrs.globalScope)) {
		return fmt.Errorf("--user and --system/--global are mutually exclusive")
	}
	opts.Profile = strings.TrimSpace(*ptrs.profile)
	opts.UnitName = strings.TrimSpace(*ptrs.unitName)
	opts.AgentDeckPath = strings.TrimSpace(*ptrs.agentDeckPath)
	opts.UnitDir = strings.TrimSpace(*ptrs.unitDir)
	opts.RunUser = strings.TrimSpace(*ptrs.runUser)
	opts.HomeDir = strings.TrimSpace(*ptrs.homeDir)
	opts.XDGConfigHome = strings.TrimSpace(*ptrs.xdgConfigHome)
	opts.XDGDataHome = strings.TrimSpace(*ptrs.xdgDataHome)
	opts.DryRun = *ptrs.dryRun
	return nil
}

func finalizeHubServiceOptions(opts *hubServiceOptions) error {
	if opts.Profile == "" {
		opts.Profile = "default"
	}
	var err error
	if opts.AgentDeckPath == "" {
		opts.AgentDeckPath, err = findAgentDeckExecutable()
		if err != nil {
			return err
		}
	}
	if opts.UnitName == "" {
		opts.UnitName = defaultHubConnectServiceUnitName(opts.Profile)
	} else if !strings.HasSuffix(opts.UnitName, ".service") {
		opts.UnitName += ".service"
	}
	if opts.Scope == hubServiceScopeSystem && opts.RunUser == "" {
		opts.RunUser = defaultHubServiceRunUser()
	}
	if opts.HomeDir == "" {
		opts.HomeDir = defaultHubServiceHomeDir(opts.RunUser)
	}
	return nil
}

func installHubConnectSystemdService(ctx context.Context, opts hubServiceOptions) error {
	if err := ensureHubSystemdSupported(); err != nil {
		return err
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if err := finalizeInstallHubServiceOptions(&opts); err != nil {
		return err
	}
	spec := hubServiceUnitSpecFromOptions(opts)
	unit, err := generateHubConnectSystemdUnit(spec)
	if err != nil {
		return err
	}
	if opts.DryRun {
		fmt.Fprint(opts.Stdout, unit)
		return nil
	}
	if err := os.MkdirAll(opts.UnitDir, 0o755); err != nil {
		return fmt.Errorf("create systemd unit dir: %w", err)
	}
	unitPath := filepath.Join(opts.UnitDir, opts.UnitName)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	if err := runHubServiceSystemctl(ctx, hubServiceSystemctlArgs(opts.Scope, "daemon-reload")...); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if opts.Enable && opts.Start {
		if err := runHubServiceSystemctl(ctx, hubServiceSystemctlArgs(opts.Scope, "enable", "--now", opts.UnitName)...); err != nil {
			return fmt.Errorf("systemctl enable --now %s: %w", opts.UnitName, err)
		}
	} else if opts.Enable {
		if err := runHubServiceSystemctl(ctx, hubServiceSystemctlArgs(opts.Scope, "enable", opts.UnitName)...); err != nil {
			return fmt.Errorf("systemctl enable %s: %w", opts.UnitName, err)
		}
	} else if opts.Start {
		if err := runHubServiceSystemctl(ctx, hubServiceSystemctlArgs(opts.Scope, "start", opts.UnitName)...); err != nil {
			return fmt.Errorf("systemctl start %s: %w", opts.UnitName, err)
		}
	}
	fmt.Fprintf(opts.Stdout, "Installed %s\n", unitPath)
	if opts.Scope == hubServiceScopeUser {
		fmt.Fprintln(opts.Stdout, "For startup without an active login session, run: loginctl enable-linger $USER")
	}
	return nil
}

func uninstallHubConnectSystemdService(ctx context.Context, opts hubServiceOptions) error {
	if err := ensureHubSystemdSupported(); err != nil {
		return err
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if err := finalizeInstallHubServiceOptions(&opts); err != nil {
		return err
	}
	unitPath := filepath.Join(opts.UnitDir, opts.UnitName)
	if opts.DryRun {
		fmt.Fprintf(opts.Stdout, "Would disable and remove %s\n", unitPath)
		return nil
	}
	_ = runHubServiceSystemctl(ctx, hubServiceSystemctlArgs(opts.Scope, "disable", "--now", opts.UnitName)...)
	if err := os.Remove(unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove systemd unit: %w", err)
	}
	if err := runHubServiceSystemctl(ctx, hubServiceSystemctlArgs(opts.Scope, "daemon-reload")...); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	fmt.Fprintf(opts.Stdout, "Removed %s\n", unitPath)
	return nil
}

func statusHubConnectSystemdService(ctx context.Context, opts hubServiceOptions) error {
	if err := ensureHubSystemdSupported(); err != nil {
		return err
	}
	if err := finalizeInstallHubServiceOptions(&opts); err != nil {
		return err
	}
	return runHubServiceSystemctl(ctx, hubServiceSystemctlArgs(opts.Scope, "status", opts.UnitName)...)
}

func ensureHubSystemdSupported() error {
	if hubServiceRuntimeGOOS != "linux" {
		return fmt.Errorf("hub service uses systemd and is only supported on Linux")
	}
	return nil
}

func finalizeInstallHubServiceOptions(opts *hubServiceOptions) error {
	var err error
	if opts.Profile == "" {
		opts.Profile = "default"
	}
	if opts.AgentDeckPath == "" {
		opts.AgentDeckPath, err = findAgentDeckExecutable()
		if err != nil {
			return err
		}
	}
	if opts.UnitName == "" {
		opts.UnitName = defaultHubConnectServiceUnitName(opts.Profile)
	} else if !strings.HasSuffix(opts.UnitName, ".service") {
		opts.UnitName += ".service"
	}
	if opts.Scope == hubServiceScopeSystem && opts.RunUser == "" {
		opts.RunUser = defaultHubServiceRunUser()
	}
	if opts.UnitDir == "" {
		opts.UnitDir, err = defaultHubServiceUnitDir(opts.Scope)
		if err != nil {
			return err
		}
	}
	if opts.HomeDir == "" {
		opts.HomeDir = defaultHubServiceHomeDir(opts.RunUser)
	}
	return nil
}

func hubServiceUnitSpecFromOptions(opts hubServiceOptions) hubServiceUnitSpec {
	return hubServiceUnitSpec{
		Scope:         opts.Scope,
		Profile:       opts.Profile,
		UnitName:      opts.UnitName,
		AgentDeckPath: opts.AgentDeckPath,
		RunUser:       opts.RunUser,
		HomeDir:       opts.HomeDir,
		XDGConfigHome: coalesce(opts.XDGConfigHome, os.Getenv("XDG_CONFIG_HOME")),
		XDGDataHome:   coalesce(opts.XDGDataHome, os.Getenv("XDG_DATA_HOME")),
		PathEnv:       buildHubServicePathEnv(opts.AgentDeckPath),
		RestartSec:    defaultHubServiceRestartSec,
	}
}

func generateHubConnectSystemdUnit(spec hubServiceUnitSpec) (string, error) {
	if spec.Profile == "" {
		spec.Profile = "default"
	}
	if spec.UnitName == "" {
		spec.UnitName = defaultHubConnectServiceUnitName(spec.Profile)
	}
	if spec.AgentDeckPath == "" {
		return "", fmt.Errorf("agent-deck executable path is required")
	}
	if spec.RestartSec == "" {
		spec.RestartSec = defaultHubServiceRestartSec
	}
	wantedBy := "default.target"
	if spec.Scope == hubServiceScopeSystem {
		wantedBy = "multi-user.target"
	}

	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=Agent Deck Hub connector")
	if spec.Profile != "default" {
		b.WriteString(" (profile ")
		b.WriteString(spec.Profile)
		b.WriteString(")")
	}
	b.WriteString("\n")
	if spec.Scope == hubServiceScopeSystem {
		b.WriteString("After=network-online.target\n")
		b.WriteString("Wants=network-online.target\n\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	if spec.Scope == hubServiceScopeSystem {
		if strings.TrimSpace(spec.RunUser) == "" {
			return "", fmt.Errorf("--run-user is required for --system services")
		}
		b.WriteString("User=")
		b.WriteString(systemdUnitToken(spec.RunUser))
		b.WriteString("\n")
	}
	if spec.HomeDir != "" {
		b.WriteString("WorkingDirectory=")
		b.WriteString(systemdQuote(spec.HomeDir))
		b.WriteString("\n")
	}
	b.WriteString("ExecStart=")
	b.WriteString(systemdQuote(spec.AgentDeckPath))
	b.WriteString(" --profile ")
	b.WriteString(systemdQuote(spec.Profile))
	b.WriteString(" hub connect\n")
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=")
	b.WriteString(spec.RestartSec)
	b.WriteString("\n")
	b.WriteString("Environment=")
	b.WriteString(systemdQuote("AGENTDECK_PROFILE=" + spec.Profile))
	b.WriteString("\n")
	if spec.HomeDir != "" {
		b.WriteString("Environment=")
		b.WriteString(systemdQuote("HOME=" + spec.HomeDir))
		b.WriteString("\n")
	}
	if spec.XDGConfigHome != "" {
		b.WriteString("Environment=")
		b.WriteString(systemdQuote("XDG_CONFIG_HOME=" + spec.XDGConfigHome))
		b.WriteString("\n")
	}
	if spec.XDGDataHome != "" {
		b.WriteString("Environment=")
		b.WriteString(systemdQuote("XDG_DATA_HOME=" + spec.XDGDataHome))
		b.WriteString("\n")
	}
	if spec.PathEnv != "" {
		b.WriteString("Environment=")
		b.WriteString(systemdQuote("PATH=" + spec.PathEnv))
		b.WriteString("\n")
	}
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=")
	b.WriteString(wantedBy)
	b.WriteString("\n")
	return b.String(), nil
}

func hubServiceSystemctlArgs(scope hubServiceScope, args ...string) []string {
	if scope == hubServiceScopeUser {
		return append([]string{"--user"}, args...)
	}
	return args
}

func defaultHubServiceUnitDir(scope hubServiceScope) (string, error) {
	if scope == hubServiceScopeSystem {
		return "/etc/systemd/system", nil
	}
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome != "" {
		return filepath.Join(configHome, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func defaultHubConnectServiceUnitName(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" || profile == "default" {
		return defaultHubServiceUnitBase + ".service"
	}
	return defaultHubServiceUnitBase + "-" + sanitizeHubServiceUnitPart(profile) + ".service"
}

func sanitizeHubServiceUnitPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "default"
	}
	return out
}

func systemdQuote(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, "\n", " ")
	return `"` + replacer.Replace(s) + `"`
}

func systemdUnitToken(s string) string {
	return strings.NewReplacer(" ", `\x20`, "\t", `\x09`, "\n", "").Replace(strings.TrimSpace(s))
}

func findAgentDeckExecutable() (string, error) {
	// Prefer the stable argv0/PATH entry (for example /opt/homebrew/bin) over
	// os.Executable, which may be a versioned Homebrew Cellar target that is
	// removed on the next upgrade.
	if path := session.FindAgentDeck(); strings.TrimSpace(path) != "" {
		return path, nil
	}
	if path, err := exec.LookPath("agent-deck"); err == nil && strings.TrimSpace(path) != "" {
		return path, nil
	}
	return "", fmt.Errorf("agent-deck executable not found")
}

func buildHubServicePathEnv(agentDeckPath string) string {
	path := strings.TrimSpace(os.Getenv("PATH"))
	dir := filepath.Dir(agentDeckPath)
	if dir == "." || dir == "" {
		return path
	}
	if path == "" {
		return dir
	}
	for _, part := range filepath.SplitList(path) {
		if part == dir {
			return path
		}
	}
	return dir + string(os.PathListSeparator) + path
}

func defaultHubServiceRunUser() string {
	if sudoUser := strings.TrimSpace(os.Getenv("SUDO_USER")); sudoUser != "" && sudoUser != "root" {
		return sudoUser
	}
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.Username) != "" {
		return strings.TrimSpace(u.Username)
	}
	return ""
}

func defaultHubServiceHomeDir(runUser string) string {
	if runUser != "" {
		if u, err := user.Lookup(runUser); err == nil && strings.TrimSpace(u.HomeDir) != "" {
			return strings.TrimSpace(u.HomeDir)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func printHubServiceUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck hub service <install|uninstall|status> [--user|--system] [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	if runtime.GOOS == "linux" {
		fmt.Fprintln(w, "  agent-deck hub service install --user")
		fmt.Fprintln(w, "  sudo agent-deck hub service install --system --run-user $USER")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The service runs: agent-deck --profile <profile> hub connect")
	fmt.Fprintf(w, "Default unit name changes by profile, e.g. %s or %s-work.service.\n", defaultHubServiceUnitBase+".service", defaultHubServiceUnitBase)
}
