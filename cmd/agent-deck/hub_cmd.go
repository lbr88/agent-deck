package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
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
	URL              string `json:"url"`
	NodeID           string `json:"node_id"`
	NodeName         string `json:"node_name"`
	NodeToken        string `json:"node_token"`
	TokenPath        string `json:"-"`
	TLSSkipVerify    bool   `json:"-"`
	CAPemFile        string `json:"-"`
	ServerName       string `json:"-"`
	PinnedCertSHA256 string `json:"-"`
}

type hubJoinRequest struct {
	InviteToken string `json:"invite_token"`
	NodeName    string `json:"node_name,omitempty"`
	Version     string `json:"version,omitempty"`
	OS          string `json:"os,omitempty"`
	Arch        string `json:"arch,omitempty"`
}

type hubInviteRequest struct {
	NodeName   string `json:"node_name"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
	Admin      bool   `json:"admin,omitempty"`
}

type hubInviteResult struct {
	URL         string    `json:"url"`
	InviteToken string    `json:"invite_token"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

type hubNodesResult struct {
	Nodes []hubNodeOutput `json:"nodes"`
}

type hubPromoteNodeRequest struct {
	NodeID string `json:"node_id"`
}

type hubBootstrapInviteResult struct {
	Created     bool
	InviteToken string
	JoinCommand string
}

type hubServerCertInfo struct {
	Host     string
	SHA256   string
	Subject  string
	NotAfter time.Time
}

type hubNodeOutput struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	OS         string     `json:"os,omitempty"`
	Arch       string     `json:"arch,omitempty"`
	Status     string     `json:"status"`
	Admin      bool       `json:"admin"`
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
	advertiseURL := fs.String("url", "", "Public wss:// hub URL printed by invites")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file; defaults to a generated self-signed cert in --data")
	tlsKey := fs.String("tls-key", "", "TLS private key file; defaults to a generated self-signed key in --data")
	bootstrapAdmin := fs.String("bootstrap-admin", "", "Create a first-run admin invite for this node name when no nodes exist")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-deck hub serve [--listen addr] [--url wss://host:port] [--bootstrap-admin node-name] [--data dir] [--tls-cert cert.pem --tls-key key.pem]")
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
	hubURL, err := configureHubAdvertiseURL(*dataDir, hubServeAdvertiseURL(*advertiseURL), *listen)
	if err != nil {
		return err
	}
	certFile, keyFile, generated, err := resolveHubServeTLSFiles(*dataDir, *tlsCert, *tlsKey)
	if err != nil {
		return err
	}

	server, err := hub.NewServer(hub.ServerConfig{
		ListenAddr:   *listen,
		DataDir:      *dataDir,
		CertFile:     certFile,
		KeyFile:      keyFile,
		AdvertiseURL: hubURL,
	})
	if err != nil {
		return err
	}
	defer server.Close()

	if generated {
		fmt.Printf("Generated self-signed hub certificate: %s\n", certFile)
		fmt.Printf("Generated self-signed hub key: %s\n", keyFile)
	}
	fmt.Printf("Agent Deck Hub listening on %s\n", *listen)
	fmt.Printf("Agent Deck Hub URL: %s\n", hubURL)
	if strings.TrimSpace(*bootstrapAdmin) != "" {
		result, err := createBootstrapAdminInviteIfNeeded(*dataDir, hubURL, *bootstrapAdmin, 24*time.Hour)
		if err != nil {
			return err
		}
		if result.Created {
			fmt.Printf("Bootstrap admin invite for %s:\n%s\n", strings.TrimSpace(*bootstrapAdmin), result.JoinCommand)
		} else {
			fmt.Println("Bootstrap admin invite skipped: hub already has registered nodes.")
		}
	}
	return server.Serve()
}

func resolveHubServeTLSFiles(dataDir, certFile, keyFile string) (string, string, bool, error) {
	dataDir = strings.TrimSpace(dataDir)
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	if (certFile == "") != (keyFile == "") {
		return "", "", false, fmt.Errorf("--tls-cert and --tls-key must be provided together")
	}
	if certFile != "" {
		return certFile, keyFile, false, nil
	}
	if dataDir == "" {
		return "", "", false, fmt.Errorf("hub data dir is required")
	}
	certFile = filepath.Join(dataDir, "hub-self-signed.crt")
	keyFile = filepath.Join(dataDir, "hub-self-signed.key")
	if fileExists(certFile) && fileExists(keyFile) {
		return certFile, keyFile, false, nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", "", false, fmt.Errorf("create hub data dir: %w", err)
	}
	if err := writeSelfSignedHubCertificate(certFile, keyFile); err != nil {
		return "", "", false, err
	}
	return certFile, keyFile, true, nil
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func writeSelfSignedHubCertificate(certFile, keyFile string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate hub TLS key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return fmt.Errorf("generate hub TLS serial: %w", err)
	}
	now := time.Now()
	dnsNames := []string{"localhost"}
	if hostname, err := os.Hostname(); err == nil {
		hostname = strings.TrimSpace(hostname)
		if hostname != "" && hostname != "localhost" {
			dnsNames = append(dnsNames, hostname)
		}
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "agent-deck hub self-signed",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("create hub TLS certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal hub TLS key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := atomicfile.WriteFileDurable(certFile, certPEM, 0o644); err != nil {
		return fmt.Errorf("write hub TLS certificate: %w", err)
	}
	if err := atomicfile.WriteFileDurable(keyFile, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write hub TLS key: %w", err)
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return fmt.Errorf("chmod hub TLS key: %w", err)
	}
	return nil
}

func configureHubAdvertiseURL(dataDir, rawURL, listenAddr string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		rawURL = defaultHubAdvertiseURL(listenAddr)
	}
	hubURL, err := normalizeHubAdvertiseURL(rawURL)
	if err != nil {
		return "", err
	}
	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		return "", err
	}
	defer store.Close()
	if err := store.SetAdvertiseURL(hubURL); err != nil {
		return "", err
	}
	return hubURL, nil
}

func hubServeAdvertiseURL(flagValue string) string {
	flagValue = strings.TrimSpace(flagValue)
	if flagValue != "" {
		return flagValue
	}
	return strings.TrimSpace(os.Getenv("AGENT_DECK_HUB_URL"))
}

func normalizeHubAdvertiseURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := validateHubJoinURL(rawURL); err != nil {
		return "", err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse hub URL: %w", err)
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("hub URL must include a host")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" || u.Path == "/" {
		u.Path = ""
	} else {
		return "", fmt.Errorf("hub URL must not include a path")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func defaultHubAdvertiseURL(listenAddr string) string {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8421"
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		if strings.HasPrefix(listenAddr, ":") {
			host = ""
			port = strings.TrimPrefix(listenAddr, ":")
		} else {
			return "wss://" + listenAddr
		}
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = defaultNodeName()
	}
	if port == "" {
		return "wss://" + host
	}
	return "wss://" + net.JoinHostPort(host, port)
}

func hubJoinCommand(hubURL, token string) string {
	return fmt.Sprintf("agent-deck hub join %s --token %s", strings.TrimSpace(hubURL), strings.TrimSpace(token))
}

func createBootstrapAdminInviteIfNeeded(dataDir, hubURL, nodeName string, ttl time.Duration) (hubBootstrapInviteResult, error) {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return hubBootstrapInviteResult{}, fmt.Errorf("--bootstrap-admin requires a node name")
	}
	if ttl <= 0 {
		return hubBootstrapInviteResult{}, fmt.Errorf("bootstrap invite ttl must be greater than zero")
	}
	hubURL, err := normalizeHubAdvertiseURL(hubURL)
	if err != nil {
		return hubBootstrapInviteResult{}, err
	}
	store, err := hub.OpenStore(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		return hubBootstrapInviteResult{}, err
	}
	defer store.Close()
	count, err := store.NodeCount()
	if err != nil {
		return hubBootstrapInviteResult{}, err
	}
	if count != 0 {
		return hubBootstrapInviteResult{}, nil
	}
	token, err := store.CreateInviteWithOptions(hub.CreateInviteOptions{
		NodeName: nodeName,
		TTL:      ttl,
		Admin:    true,
	})
	if err != nil {
		return hubBootstrapInviteResult{}, err
	}
	return hubBootstrapInviteResult{
		Created:     true,
		InviteToken: token,
		JoinCommand: hubJoinCommand(hubURL, token),
	}, nil
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
	admin := fs.Bool("admin", false, "Invite the node as a hub admin")
	local := fs.Bool("local", false, "Create the invite from the local hub data directory instead of the configured hub")
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: agent-deck hub invite [--admin] [--local] [--data dir] [--ttl duration] <node-name>")
	}
	if *ttl <= 0 {
		return fmt.Errorf("--ttl must be greater than zero")
	}
	nodeName := strings.TrimSpace(fs.Arg(0))
	if nodeName == "" {
		return fmt.Errorf("node name is required")
	}
	if !*local && !flagWasSet(fs, "data") {
		config, err := session.LoadUserConfig()
		if err != nil {
			return fmt.Errorf("load user config: %w", err)
		}
		if config.Hub.Enabled() {
			result, err := createRemoteHubInvite(config.Hub, nodeName, *ttl, *admin)
			if err != nil {
				return err
			}
			fmt.Println(hubJoinCommand(result.URL, result.InviteToken))
			return nil
		}
	}

	store, err := hub.OpenStore(filepath.Join(*dataDir, "hub.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	hubURL, err := store.AdvertiseURL()
	if err != nil {
		if errors.Is(err, hub.ErrAdvertiseURLMissing) {
			return fmt.Errorf("hub URL is not configured; start the hub with `agent-deck hub serve` once so invites can print the full join command")
		}
		return err
	}
	hubURL, err = normalizeHubAdvertiseURL(hubURL)
	if err != nil {
		return err
	}
	token, err := store.CreateInviteWithOptions(hub.CreateInviteOptions{
		NodeName: nodeName,
		TTL:      *ttl,
		Admin:    *admin,
	})
	if err != nil {
		return err
	}
	fmt.Println(hubJoinCommand(hubURL, token))
	return nil
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	wasSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func createRemoteHubInvite(settings session.HubSettings, nodeName string, ttl time.Duration, admin bool) (hubInviteResult, error) {
	rawHubURL := strings.TrimSpace(settings.URL)
	inviteURL, err := hubInviteEndpoint(rawHubURL, strings.TrimSpace(settings.NodeID))
	if err != nil {
		return hubInviteResult{}, err
	}
	nodeToken, err := hubNodeToken(settings)
	if err != nil {
		return hubInviteResult{}, err
	}
	body, err := json.Marshal(hubInviteRequest{
		NodeName:   strings.TrimSpace(nodeName),
		TTLSeconds: int64(ttl / time.Second),
		Admin:      admin,
	})
	if err != nil {
		return hubInviteResult{}, err
	}
	client, _, err := hubJoinHTTPClient(hubJoinTLSOptions{
		TLSSkipVerify:    settings.TLSSkipVerify,
		CAPemFile:        strings.TrimSpace(settings.CAPemFile),
		ServerName:       strings.TrimSpace(settings.ServerName),
		PinnedCertSHA256: strings.TrimSpace(settings.PinnedCertSHA256),
	})
	if err != nil {
		return hubInviteResult{}, err
	}
	req, err := http.NewRequest(http.MethodPost, inviteURL, bytes.NewReader(body))
	if err != nil {
		return hubInviteResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+nodeToken)
	resp, err := client.Do(req)
	if err != nil {
		return hubInviteResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return hubInviteResult{}, fmt.Errorf("hub invite failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var result hubInviteResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return hubInviteResult{}, fmt.Errorf("decode hub invite response: %w", err)
	}
	if strings.TrimSpace(result.URL) == "" {
		result.URL = rawHubURL
	}
	result.URL, err = normalizeHubAdvertiseURL(result.URL)
	if err != nil {
		return hubInviteResult{}, fmt.Errorf("invalid hub invite response URL: %w", err)
	}
	result.InviteToken = strings.TrimSpace(result.InviteToken)
	if result.InviteToken == "" {
		return hubInviteResult{}, fmt.Errorf("hub invite response missing invite_token")
	}
	return result, nil
}

func hubInviteEndpoint(rawHubURL, nodeID string) (string, error) {
	return hubAuthenticatedEndpoint(rawHubURL, nodeID, "/api/invites")
}

func hubAuthenticatedEndpoint(rawHubURL, nodeID, path string) (string, error) {
	if err := validateHubJoinURL(rawHubURL); err != nil {
		return "", err
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", fmt.Errorf("hub node id is not configured; run agent-deck hub join again")
	}
	u, err := url.Parse(strings.TrimSpace(rawHubURL))
	if err != nil {
		return "", fmt.Errorf("parse hub URL: %w", err)
	}
	u.Scheme = "https"
	u.Path = path
	q := u.Query()
	q.Set("node_id", nodeID)
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String(), nil
}

func hubNodeToken(settings session.HubSettings) (string, error) {
	tokenFile := strings.TrimSpace(settings.TokenFile)
	if tokenFile == "" {
		return "", fmt.Errorf("hub token file is not configured; run agent-deck hub join again")
	}
	tokenData, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("read hub token file: %w", err)
	}
	nodeToken := strings.TrimSpace(string(tokenData))
	if nodeToken == "" {
		return "", fmt.Errorf("hub token file is empty; run agent-deck hub join again")
	}
	return nodeToken, nil
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

	tlsOptions := hubJoinTLSOptions{
		TLSSkipVerify: *tlsSkipVerify,
		CAPemFile:     strings.TrimSpace(*caPemFile),
		ServerName:    strings.TrimSpace(*serverName),
	}
	if !tlsOptions.TLSSkipVerify && tlsOptions.CAPemFile == "" {
		tlsOptions.TrustServerCert = promptTrustHubServerCert
	}
	result, err := exchangeHubInvite(hubURL, hubJoinRequest{
		InviteToken: strings.TrimSpace(*token),
		NodeName:    strings.TrimSpace(*nodeName),
		Version:     Version,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
	}, tlsOptions)
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
	if len(args) > 0 && args[0] == "promote" {
		return handleHubNodesPromote(args[1:])
	}
	defaultData, err := defaultHubDataDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("hub nodes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data", defaultData, "Hub data directory")
	jsonOutput := fs.Bool("json", false, "Output nodes as JSON")
	local := fs.Bool("local", false, "List nodes from the local hub data directory instead of the configured hub")
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if !*local && !flagWasSet(fs, "data") {
		config, err := session.LoadUserConfig()
		if err != nil {
			return fmt.Errorf("load user config: %w", err)
		}
		if config.Hub.Enabled() {
			nodeViews, err := listRemoteHubNodes(config.Hub)
			if err != nil {
				return err
			}
			return printHubNodes(nodeViews, *jsonOutput)
		}
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
	return printHubNodes(nodeViews, *jsonOutput)
}

func printHubNodes(nodeViews []hubNodeOutput, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(nodeViews)
	}
	if len(nodeViews) == 0 {
		fmt.Println("No hub nodes registered.")
		return nil
	}
	for _, node := range nodeViews {
		role := "node"
		if node.Admin {
			role = "admin"
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", node.ID, node.Name, node.Status, role)
	}
	return nil
}

func handleHubNodesPromote(args []string) error {
	defaultData, err := defaultHubDataDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("hub nodes promote", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data", defaultData, "Hub data directory")
	local := fs.Bool("local", false, "Promote the node in the local hub data directory instead of the configured hub")
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: agent-deck hub nodes promote [--local] [--data dir] <node-id>")
	}
	nodeID := strings.TrimSpace(fs.Arg(0))
	if nodeID == "" {
		return fmt.Errorf("node id is required")
	}
	if !*local && !flagWasSet(fs, "data") {
		config, err := session.LoadUserConfig()
		if err != nil {
			return fmt.Errorf("load user config: %w", err)
		}
		if config.Hub.Enabled() {
			node, err := promoteRemoteHubNode(config.Hub, nodeID)
			if err != nil {
				return err
			}
			fmt.Printf("Promoted %s to hub admin\n", node.ID)
			return nil
		}
	}
	store, err := hub.OpenStore(filepath.Join(*dataDir, "hub.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.SetNodeAdmin(nodeID, true); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("hub node %s not found", nodeID)
		}
		return err
	}
	fmt.Printf("Promoted %s to hub admin\n", nodeID)
	return nil
}

func listRemoteHubNodes(settings session.HubSettings) ([]hubNodeOutput, error) {
	endpoint, err := hubAuthenticatedEndpoint(strings.TrimSpace(settings.URL), strings.TrimSpace(settings.NodeID), "/api/nodes")
	if err != nil {
		return nil, err
	}
	nodeToken, err := hubNodeToken(settings)
	if err != nil {
		return nil, err
	}
	client, _, err := hubJoinHTTPClient(hubJoinTLSOptions{
		TLSSkipVerify:    settings.TLSSkipVerify,
		CAPemFile:        strings.TrimSpace(settings.CAPemFile),
		ServerName:       strings.TrimSpace(settings.ServerName),
		PinnedCertSHA256: strings.TrimSpace(settings.PinnedCertSHA256),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+nodeToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("hub nodes failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var result hubNodesResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode hub nodes response: %w", err)
	}
	return result.Nodes, nil
}

func promoteRemoteHubNode(settings session.HubSettings, nodeID string) (hubNodeOutput, error) {
	endpoint, err := hubAuthenticatedEndpoint(strings.TrimSpace(settings.URL), strings.TrimSpace(settings.NodeID), "/api/nodes/promote")
	if err != nil {
		return hubNodeOutput{}, err
	}
	nodeToken, err := hubNodeToken(settings)
	if err != nil {
		return hubNodeOutput{}, err
	}
	body, err := json.Marshal(hubPromoteNodeRequest{NodeID: strings.TrimSpace(nodeID)})
	if err != nil {
		return hubNodeOutput{}, err
	}
	client, _, err := hubJoinHTTPClient(hubJoinTLSOptions{
		TLSSkipVerify:    settings.TLSSkipVerify,
		CAPemFile:        strings.TrimSpace(settings.CAPemFile),
		ServerName:       strings.TrimSpace(settings.ServerName),
		PinnedCertSHA256: strings.TrimSpace(settings.PinnedCertSHA256),
	})
	if err != nil {
		return hubNodeOutput{}, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return hubNodeOutput{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+nodeToken)
	resp, err := client.Do(req)
	if err != nil {
		return hubNodeOutput{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return hubNodeOutput{}, fmt.Errorf("hub nodes promote failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var result hubNodeOutput
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return hubNodeOutput{}, fmt.Errorf("decode hub nodes promote response: %w", err)
	}
	if strings.TrimSpace(result.ID) == "" {
		return hubNodeOutput{}, fmt.Errorf("hub nodes promote response missing node id")
	}
	return result, nil
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
		URL:              strings.TrimSpace(hubConfig.URL),
		NodeID:           strings.TrimSpace(hubConfig.NodeID),
		NodeName:         nodeName,
		Token:            token,
		Version:          Version,
		TLSSkipVerify:    hubConfig.TLSSkipVerify,
		CAPemFile:        strings.TrimSpace(hubConfig.CAPemFile),
		ServerName:       strings.TrimSpace(hubConfig.ServerName),
		PinnedCertSHA256: strings.TrimSpace(hubConfig.PinnedCertSHA256),
		AttachBackend:    hub.NewTmuxAttachBackend(profile),
		ActionBackend:    hub.LocalActionBackend{Profile: profile},
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
		URL:              result.URL,
		NodeID:           result.NodeID,
		NodeName:         result.NodeName,
		TokenFile:        tokenPath,
		AutoConnect:      true,
		TLSSkipVerify:    result.TLSSkipVerify,
		CAPemFile:        strings.TrimSpace(result.CAPemFile),
		ServerName:       strings.TrimSpace(result.ServerName),
		PinnedCertSHA256: strings.TrimSpace(result.PinnedCertSHA256),
	}
	return nil
}

func promptTrustHubServerCert(info hubServerCertInfo) (bool, error) {
	fmt.Fprintln(os.Stderr, "The hub presented an unknown TLS certificate.")
	if strings.TrimSpace(info.Subject) != "" {
		fmt.Fprintf(os.Stderr, "Subject: %s\n", info.Subject)
	}
	if !info.NotAfter.IsZero() {
		fmt.Fprintf(os.Stderr, "Expires: %s\n", info.NotAfter.Format(time.RFC3339))
	}
	fmt.Fprintf(os.Stderr, "SHA256 fingerprint: %s\n", info.SHA256)
	fmt.Fprint(os.Stderr, "Trust this hub and store this fingerprint? [y/N] ")
	var answer string
	if _, err := fmt.Fscan(os.Stdin, &answer); err != nil {
		return false, nil
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func validateHubJoinURL(raw string) error {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "wss://") {
		return fmt.Errorf("hub join requires wss://; use TLS even for local deployments")
	}
	return nil
}

type hubJoinTLSOptions struct {
	TLSSkipVerify    bool
	CAPemFile        string
	ServerName       string
	PinnedCertSHA256 string
	TrustServerCert  func(hubServerCertInfo) (bool, error)
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

	client, acceptedCertFingerprint, err := hubJoinHTTPClient(tlsOptions)
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
	if strings.TrimSpace(result.PinnedCertSHA256) == "" && acceptedCertFingerprint != nil {
		result.PinnedCertSHA256 = strings.TrimSpace(*acceptedCertFingerprint)
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

func hubJoinHTTPClient(opts hubJoinTLSOptions) (*http.Client, *string, error) {
	acceptedFingerprint := ""
	tlsConfig := &tls.Config{
		InsecureSkipVerify: opts.TLSSkipVerify,
		ServerName:         opts.ServerName,
	}
	if strings.TrimSpace(opts.PinnedCertSHA256) != "" {
		pinned := strings.TrimSpace(opts.PinnedCertSHA256)
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			return hub.VerifyPinnedCertificate(rawCerts, pinned)
		}
		return &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
			Timeout:   30 * time.Second,
		}, &acceptedFingerprint, nil
	}
	if !opts.TLSSkipVerify && opts.CAPemFile == "" && opts.TrustServerCert != nil {
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("hub server did not present a certificate")
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("parse hub server certificate: %w", err)
			}
			info := hubServerCertInfo{
				SHA256:   hub.CertificateFingerprintSHA256(rawCerts[0]),
				Subject:  cert.Subject.String(),
				NotAfter: cert.NotAfter,
			}
			accepted, err := opts.TrustServerCert(info)
			if err != nil {
				return err
			}
			if !accepted {
				return fmt.Errorf("hub server certificate was not trusted")
			}
			acceptedFingerprint = info.SHA256
			return nil
		}
		return &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
			Timeout:   30 * time.Second,
		}, &acceptedFingerprint, nil
	}
	if opts.CAPemFile != "" {
		pemData, err := os.ReadFile(opts.CAPemFile)
		if err != nil {
			return nil, nil, fmt.Errorf("read --ca-pem-file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, nil, fmt.Errorf("no certificates found in --ca-pem-file")
		}
		tlsConfig.RootCAs = pool
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		Timeout:   30 * time.Second,
	}, &acceptedFingerprint, nil
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
			Admin:      node.Admin,
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
	fmt.Fprintln(w, "  invite  Create a single-use invite and print the join command")
	fmt.Fprintln(w, "  join    Join this agent-deck node to a hub")
	fmt.Fprintln(w, "  nodes   List registered hub nodes")
	fmt.Fprintln(w, "  connect Connect this node to the configured hub")
}
