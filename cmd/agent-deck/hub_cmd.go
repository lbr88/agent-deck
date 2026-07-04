package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/agentpaths"
	"github.com/asheshgoplani/agent-deck/internal/atomicfile"
	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

type hubJoinResult struct {
	URL           string `json:"url"`
	NodeID        string `json:"node_id"`
	NodeName      string `json:"node_name"`
	NodeToken     string `json:"node_token"`
	TokenPath     string `json:"-"`
	TLSSkipVerify bool   `json:"-"`
	CAPemFile     string `json:"-"`
	ServerName    string `json:"-"`
}

type hubJoinRequest struct {
	InviteToken string `json:"invite_token"`
	NodeName    string `json:"node_name,omitempty"`
	Version     string `json:"version,omitempty"`
	OS          string `json:"os,omitempty"`
	Arch        string `json:"arch,omitempty"`
}

type hubNodeOutput struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	OS         string     `json:"os,omitempty"`
	Arch       string     `json:"arch,omitempty"`
	Status     string     `json:"status"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

func handleHub(profile string, args []string) {
	if len(args) == 0 {
		printHubUsage(os.Stderr)
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "serve":
		err = handleHubServe(args[1:])
	case "invite":
		err = handleHubInvite(args[1:])
	case "join":
		err = handleHubJoin(args[1:])
	case "nodes":
		err = handleHubNodes(args[1:])
	case "connect":
		err = handleHubConnect(profile, args[1:])
	case "help", "--help", "-h":
		printHubUsage(os.Stdout)
		return
	default:
		err = fmt.Errorf("unknown hub command %q", args[0])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleHubServe(args []string) error {
	defaultData, err := defaultHubDataDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("hub serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "127.0.0.1:8421", "Listen address for the hub server")
	dataDir := fs.String("data", defaultData, "Hub data directory")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file (required)")
	tlsKey := fs.String("tls-key", "", "TLS private key file (required)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub serve [--listen addr] [--data dir] --tls-cert cert.pem --tls-key key.pem")
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if strings.TrimSpace(*tlsCert) == "" || strings.TrimSpace(*tlsKey) == "" {
		return fmt.Errorf("agent-deck hub serve requires --tls-cert and --tls-key")
	}

	server, err := hub.NewServer(hub.ServerConfig{
		ListenAddr: *listen,
		DataDir:    *dataDir,
		CertFile:   *tlsCert,
		KeyFile:    *tlsKey,
	})
	if err != nil {
		return err
	}
	defer server.Close()

	fmt.Printf("Agent Deck Hub listening on %s\n", *listen)
	return server.Serve()
}

func handleHubInvite(args []string) error {
	defaultData, err := defaultHubDataDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("hub invite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data", defaultData, "Hub data directory")
	ttl := fs.Duration("ttl", 24*time.Hour, "Invite lifetime")
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: agent-deck hub invite [--data dir] [--ttl duration] <node-name>")
	}
	if *ttl <= 0 {
		return fmt.Errorf("--ttl must be greater than zero")
	}

	store, err := hub.OpenStore(filepath.Join(*dataDir, "hub.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	token, err := store.CreateInvite(strings.TrimSpace(fs.Arg(0)), *ttl)
	if err != nil {
		return err
	}
	fmt.Println(token)
	return nil
}

func handleHubJoin(args []string) error {
	defaultTokenPath, err := defaultHubTokenPath()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("hub join", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	token := fs.String("token", "", "Invite token from agent-deck hub invite")
	nodeName := fs.String("node-name", defaultNodeName(), "Node name to register")
	tokenFile := fs.String("token-file", defaultTokenPath, "Path for the joined node token")
	tlsSkipVerify := fs.Bool("tls-skip-verify", false, "UNSAFE: skip TLS certificate verification")
	caPemFile := fs.String("ca-pem-file", "", "PEM CA bundle for verifying the hub TLS certificate")
	serverName := fs.String("server-name", "", "TLS server name override")
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: agent-deck hub join wss://host:port --token <invite-token>")
	}
	hubURL := strings.TrimSpace(fs.Arg(0))
	if err := validateHubJoinURL(hubURL); err != nil {
		return err
	}
	if strings.TrimSpace(*token) == "" {
		return fmt.Errorf("--token is required")
	}

	config, err := session.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("load user config: %w", err)
	}

	result, err := exchangeHubInvite(hubURL, hubJoinRequest{
		InviteToken: strings.TrimSpace(*token),
		NodeName:    strings.TrimSpace(*nodeName),
		Version:     Version,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
	}, hubJoinTLSOptions{
		TLSSkipVerify: *tlsSkipVerify,
		CAPemFile:     strings.TrimSpace(*caPemFile),
		ServerName:    strings.TrimSpace(*serverName),
	})
	if err != nil {
		return err
	}
	result.TokenPath = *tokenFile
	result.TLSSkipVerify = *tlsSkipVerify
	result.CAPemFile = strings.TrimSpace(*caPemFile)
	result.ServerName = strings.TrimSpace(*serverName)

	if err := saveHubJoinConfig(config, result); err != nil {
		return err
	}
	if err := session.SaveUserConfig(config); err != nil {
		return err
	}

	fmt.Printf("Joined hub %s as %s\n", config.Hub.URL, config.Hub.NodeName)
	return nil
}

func handleHubNodes(args []string) error {
	defaultData, err := defaultHubDataDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("hub nodes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data", defaultData, "Hub data directory")
	jsonOutput := fs.Bool("json", false, "Output nodes as JSON")
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	store, err := hub.OpenStore(filepath.Join(*dataDir, "hub.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	nodes, err := store.Nodes()
	if err != nil {
		return err
	}
	nodeViews := hubNodeOutputs(nodes)
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(nodeViews)
	}
	if len(nodeViews) == 0 {
		fmt.Println("No hub nodes registered.")
		return nil
	}
	for _, node := range nodeViews {
		fmt.Printf("%s\t%s\t%s\n", node.ID, node.Name, node.Status)
	}
	return nil
}

func handleHubConnect(profile string, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return handleHubConnectWithContext(ctx, profile, args)
}

func handleHubConnectWithContext(ctx context.Context, profile string, args []string) error {
	fs := flag.NewFlagSet("hub connect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	config, err := session.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("load user config: %w", err)
	}
	hubConfig := config.Hub
	if !hubConfig.Enabled() {
		return fmt.Errorf("hub is not configured; run agent-deck hub join first")
	}
	tokenFile := strings.TrimSpace(hubConfig.TokenFile)
	if tokenFile == "" {
		return fmt.Errorf("hub token file is not configured; run agent-deck hub join again")
	}
	tokenData, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("read hub token file: %w", err)
	}
	token := strings.TrimSpace(string(tokenData))
	if token == "" {
		return fmt.Errorf("hub token file is empty; run agent-deck hub join again")
	}

	nodeName := strings.TrimSpace(hubConfig.NodeName)
	if nodeName == "" {
		nodeName = strings.TrimSpace(hubConfig.NodeID)
	}
	client := hub.NewClient(hub.ClientConfig{
		URL:           strings.TrimSpace(hubConfig.URL),
		NodeID:        strings.TrimSpace(hubConfig.NodeID),
		NodeName:      nodeName,
		Token:         token,
		Version:       Version,
		TLSSkipVerify: hubConfig.TLSSkipVerify,
		CAPemFile:     strings.TrimSpace(hubConfig.CAPemFile),
		ServerName:    strings.TrimSpace(hubConfig.ServerName),
		AttachBackend: hub.NewTmuxAttachBackend(profile),
		ActionBackend: hub.LocalActionBackend{Profile: profile},
	}, hub.LocalSessionSource{Profile: profile})

	fmt.Printf("Connecting to hub %s as %s\n", strings.TrimSpace(hubConfig.URL), nodeName)
	return client.Connect(ctx)
}

func saveHubJoinConfig(config *session.UserConfig, result hubJoinResult) error {
	if config == nil {
		return fmt.Errorf("config is required")
	}
	var err error
	result, err = normalizeHubJoinResult(result, "")
	if err != nil {
		return err
	}
	tokenPath := strings.TrimSpace(result.TokenPath)
	if tokenPath == "" {
		tokenPath, err = defaultHubTokenPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		return fmt.Errorf("create hub token dir: %w", err)
	}
	if err := atomicfile.WriteFileDurable(tokenPath, []byte(result.NodeToken+"\n"), 0o600); err != nil {
		return fmt.Errorf("write hub token file: %w", err)
	}
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		return fmt.Errorf("chmod hub token file: %w", err)
	}
	config.Hub = session.HubSettings{
		URL:           result.URL,
		NodeID:        result.NodeID,
		NodeName:      result.NodeName,
		TokenFile:     tokenPath,
		AutoConnect:   true,
		TLSSkipVerify: result.TLSSkipVerify,
		CAPemFile:     strings.TrimSpace(result.CAPemFile),
		ServerName:    strings.TrimSpace(result.ServerName),
	}
	return nil
}

func validateHubJoinURL(raw string) error {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "wss://") {
		return fmt.Errorf("hub join requires wss://; use TLS even for local deployments")
	}
	return nil
}

type hubJoinTLSOptions struct {
	TLSSkipVerify bool
	CAPemFile     string
	ServerName    string
}

func exchangeHubInvite(rawHubURL string, req hubJoinRequest, tlsOptions hubJoinTLSOptions) (hubJoinResult, error) {
	joinURL, err := hubJoinEndpoint(rawHubURL)
	if err != nil {
		return hubJoinResult{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return hubJoinResult{}, err
	}

	client, err := hubJoinHTTPClient(tlsOptions)
	if err != nil {
		return hubJoinResult{}, err
	}
	resp, err := client.Post(joinURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return hubJoinResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return hubJoinResult{}, fmt.Errorf("hub join failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var result hubJoinResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return hubJoinResult{}, fmt.Errorf("decode hub join response: %w", err)
	}
	if strings.TrimSpace(result.URL) == "" {
		result.URL = strings.TrimSpace(rawHubURL)
	}
	return normalizeHubJoinResult(result, rawHubURL)
}

func normalizeHubJoinResult(result hubJoinResult, fallbackURL string) (hubJoinResult, error) {
	result.URL = strings.TrimSpace(result.URL)
	if result.URL == "" {
		result.URL = strings.TrimSpace(fallbackURL)
	}
	if err := validateHubJoinURL(result.URL); err != nil {
		return hubJoinResult{}, fmt.Errorf("invalid hub join response URL: %w", err)
	}
	result.NodeID = strings.TrimSpace(result.NodeID)
	if result.NodeID == "" {
		return hubJoinResult{}, fmt.Errorf("hub join response missing node_id")
	}
	result.NodeName = strings.TrimSpace(result.NodeName)
	if result.NodeName == "" {
		return hubJoinResult{}, fmt.Errorf("hub join response missing node_name")
	}
	result.NodeToken = strings.TrimSpace(result.NodeToken)
	if result.NodeToken == "" {
		return hubJoinResult{}, fmt.Errorf("hub join response missing node_token")
	}
	return result, nil
}

func hubJoinEndpoint(rawHubURL string) (string, error) {
	if err := validateHubJoinURL(rawHubURL); err != nil {
		return "", err
	}
	u, err := url.Parse(strings.TrimSpace(rawHubURL))
	if err != nil {
		return "", fmt.Errorf("parse hub URL: %w", err)
	}
	u.Scheme = "https"
	u.Path = "/api/join"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func hubJoinHTTPClient(opts hubJoinTLSOptions) (*http.Client, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: opts.TLSSkipVerify,
		ServerName:         opts.ServerName,
	}
	if opts.CAPemFile != "" {
		pemData, err := os.ReadFile(opts.CAPemFile)
		if err != nil {
			return nil, fmt.Errorf("read --ca-pem-file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("no certificates found in --ca-pem-file")
		}
		tlsConfig.RootCAs = pool
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		Timeout:   30 * time.Second,
	}, nil
}

func defaultHubDataDir() (string, error) {
	dataDir, err := agentpaths.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "hub"), nil
}

func defaultHubTokenPath() (string, error) {
	configDir, err := agentpaths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "hub-node-token"), nil
}

func defaultNodeName() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "local"
	}
	return strings.TrimSpace(name)
}

func hubNodeOutputs(nodes []hub.Node) []hubNodeOutput {
	out := make([]hubNodeOutput, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, hubNodeOutput{
			ID:         node.ID,
			Name:       node.Name,
			Version:    node.Version,
			OS:         node.OS,
			Arch:       node.Arch,
			Status:     node.Status,
			LastSeenAt: node.LastSeenAt,
		})
	}
	return out
}

func printHubUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck hub <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  serve   Start the TLS hub server")
	fmt.Fprintln(w, "  invite  Create a single-use join invite")
	fmt.Fprintln(w, "  join    Join this agent-deck node to a hub")
	fmt.Fprintln(w, "  nodes   List registered hub nodes")
	fmt.Fprintln(w, "  connect Connect this node to the configured hub")
}
