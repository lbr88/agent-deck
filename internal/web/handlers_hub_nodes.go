package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

type hubNodeRenameAdminRequest struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
}

type hubNodeAdminRequest struct {
	NodeID string `json:"node_id"`
}

type hubInviteAdminCreateRequest struct {
	NodeName   string `json:"node_name"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
	Admin      bool   `json:"admin,omitempty"`
}

type hubInviteAdminCreateResponse struct {
	URL         string    `json:"url"`
	InviteToken string    `json:"invite_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type hubInviteAdminListResponse struct {
	Invites []hubInviteAdminResponse `json:"invites"`
}

type hubInviteAdminResponse struct {
	ID              string     `json:"id,omitempty"`
	NodeName        string     `json:"node_name"`
	ExpiresAt       time.Time  `json:"expires_at"`
	ConsumedAt      *time.Time `json:"consumed_at,omitempty"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	Admin           bool       `json:"admin"`
	CreatedByNodeID string     `json:"created_by_node_id,omitempty"`
	Status          string     `json:"status"`
}

type hubInviteRevokeAdminRequest struct {
	InviteID string `json:"invite_id"`
}

type hubTrustPendingAdminResponse struct {
	Requests []hubTrustRequestAdminResponse `json:"requests"`
}

type hubTrustRequestAdminResponse struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Version  string `json:"version,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Status   string `json:"status,omitempty"`
}

type hubTrustDecisionAdminRequest struct {
	NodeID string `json:"node_id"`
}

type hubStatusAdminResponse struct {
	Node HubNodeAdmin `json:"node"`
}

type hubAdminHTTPError struct {
	status int
	body   string
}

func (e *hubAdminHTTPError) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.body)
	if msg == "" {
		msg = http.StatusText(e.status)
	}
	return fmt.Sprintf("hub request failed: %s: %s", http.StatusText(e.status), msg)
}

func (s *Server) handleHubNodesAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	nodes, err := s.listConfiguredHubNodes(r.Context())
	if err != nil {
		writeHubAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, HubNodesAdminResponse{Nodes: nodes})
}

func (s *Server) handleHubNodeAdminByID(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("id"))
	if nodeID == "" {
		const prefix = "/api/hub/nodes/"
		nodeID = strings.TrimSpace(strings.TrimPrefix(r.URL.Path, prefix))
	}
	if nodeID == "" || strings.Contains(nodeID, "/") {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "hub node id is required")
		return
	}

	switch r.Method {
	case http.MethodPatch:
		if !s.checkMutationsAllowed(w) {
			return
		}
		if !s.checkMutationRateLimit(w) {
			return
		}
		var req RenameHubNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "name is required")
			return
		}
		node, err := s.renameConfiguredHubNode(r.Context(), nodeID, req.Name)
		if err != nil {
			writeHubAdminError(w, err)
			return
		}
		s.updateMenuHubNodeName(node.ID, node.Name)
		writeJSON(w, http.StatusOK, node)

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleHubNodeAdminAction(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	if !s.checkMutationsAllowed(w) {
		return
	}
	if !s.checkMutationRateLimit(w) {
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("id"))
	if nodeID == "" || strings.Contains(nodeID, "/") {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "hub node id is required")
		return
	}

	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/promote"):
		node, err := s.setConfiguredHubNodeAdmin(r.Context(), nodeID, true)
		if err != nil {
			writeHubAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, node)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/demote"):
		node, err := s.setConfiguredHubNodeAdmin(r.Context(), nodeID, false)
		if err != nil {
			writeHubAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, node)
	case r.Method == http.MethodDelete:
		if err := s.revokeConfiguredHubNode(r.Context(), nodeID); err != nil {
			writeHubAdminError(w, err)
			return
		}
		s.removeMenuHubNode(nodeID)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleHubInvitesAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	switch r.Method {
	case http.MethodGet:
		invites, err := s.listConfiguredHubInvites(r.Context())
		if err != nil {
			writeHubAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, HubInvitesAdminResponse{Invites: invites})
	case http.MethodPost:
		if !s.checkMutationsAllowed(w) {
			return
		}
		if !s.checkMutationRateLimit(w) {
			return
		}
		var req CreateHubInviteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
			return
		}
		req.NodeName = strings.TrimSpace(req.NodeName)
		if req.NodeName == "" {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "nodeName is required")
			return
		}
		if req.TTLSeconds < 0 {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "ttlSeconds must be greater than zero")
			return
		}
		invite, err := s.createConfiguredHubInvite(r.Context(), req)
		if err != nil {
			writeHubAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, invite)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleHubInviteRevokeAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	if !s.checkMutationsAllowed(w) {
		return
	}
	if !s.checkMutationRateLimit(w) {
		return
	}
	inviteID := strings.TrimSpace(r.PathValue("id"))
	if inviteID == "" || strings.Contains(inviteID, "/") {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "hub invite id is required")
		return
	}
	if err := s.revokeConfiguredHubInvite(r.Context(), inviteID); err != nil {
		writeHubAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHubTrustPendingAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	requests, err := s.listConfiguredHubTrustRequests(r.Context())
	if err != nil {
		writeHubAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, HubTrustRequestsAdminResponse{Requests: requests})
}

func (s *Server) handleHubTrustDecisionAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	if !s.checkMutationsAllowed(w) {
		return
	}
	if !s.checkMutationRateLimit(w) {
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("id"))
	if nodeID == "" || strings.Contains(nodeID, "/") {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "hub trust node id is required")
		return
	}
	allow := strings.HasSuffix(r.URL.Path, "/allow")
	if !allow && !strings.HasSuffix(r.URL.Path, "/deny") {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.setConfiguredHubTrustDecision(r.Context(), nodeID, allow); err != nil {
		writeHubAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listConfiguredHubNodes(ctx context.Context) ([]HubNodeAdmin, error) {
	var result HubNodesAdminResponse
	if err := s.hubAdminJSON(ctx, http.MethodGet, "/api/nodes", nil, &result); err != nil {
		return nil, err
	}
	return result.Nodes, nil
}

func (s *Server) renameConfiguredHubNode(ctx context.Context, nodeID, name string) (HubNodeAdmin, error) {
	var result HubNodeAdmin
	if err := s.hubAdminJSON(ctx, http.MethodPost, "/api/nodes/rename", hubNodeRenameAdminRequest{
		NodeID: strings.TrimSpace(nodeID),
		Name:   strings.TrimSpace(name),
	}, &result); err != nil {
		return HubNodeAdmin{}, err
	}
	if strings.TrimSpace(result.ID) == "" {
		return HubNodeAdmin{}, fmt.Errorf("hub response missing node id")
	}
	return result, nil
}

func (s *Server) setConfiguredHubNodeAdmin(ctx context.Context, nodeID string, admin bool) (HubNodeAdmin, error) {
	path := "/api/nodes/promote"
	if !admin {
		path = "/api/nodes/demote"
	}
	var result HubNodeAdmin
	if err := s.hubAdminJSON(ctx, http.MethodPost, path, hubNodeAdminRequest{NodeID: strings.TrimSpace(nodeID)}, &result); err != nil {
		return HubNodeAdmin{}, err
	}
	if strings.TrimSpace(result.ID) == "" {
		return HubNodeAdmin{}, fmt.Errorf("hub response missing node id")
	}
	return result, nil
}

func (s *Server) revokeConfiguredHubNode(ctx context.Context, nodeID string) error {
	return s.hubAdminJSON(ctx, http.MethodPost, "/api/nodes/revoke", hubNodeAdminRequest{NodeID: strings.TrimSpace(nodeID)}, nil)
}

func (s *Server) listConfiguredHubInvites(ctx context.Context) ([]HubInviteAdmin, error) {
	var result hubInviteAdminListResponse
	if err := s.hubAdminJSON(ctx, http.MethodGet, "/api/invites", nil, &result); err != nil {
		return nil, err
	}
	out := make([]HubInviteAdmin, 0, len(result.Invites))
	for _, invite := range result.Invites {
		out = append(out, HubInviteAdmin{
			ID:              invite.ID,
			NodeName:        invite.NodeName,
			ExpiresAt:       invite.ExpiresAt,
			ConsumedAt:      invite.ConsumedAt,
			RevokedAt:       invite.RevokedAt,
			Admin:           invite.Admin,
			CreatedByNodeID: invite.CreatedByNodeID,
			Status:          invite.Status,
		})
	}
	return out, nil
}

func (s *Server) createConfiguredHubInvite(ctx context.Context, req CreateHubInviteRequest) (CreateHubInviteResponse, error) {
	var result hubInviteAdminCreateResponse
	if err := s.hubAdminJSON(ctx, http.MethodPost, "/api/invites", hubInviteAdminCreateRequest{
		NodeName:   strings.TrimSpace(req.NodeName),
		TTLSeconds: req.TTLSeconds,
		Admin:      req.Admin,
	}, &result); err != nil {
		return CreateHubInviteResponse{}, err
	}
	return CreateHubInviteResponse{
		URL:         result.URL,
		InviteToken: result.InviteToken,
		ExpiresAt:   result.ExpiresAt,
		JoinCommand: webHubJoinCommand(result.URL, result.InviteToken),
	}, nil
}

func (s *Server) revokeConfiguredHubInvite(ctx context.Context, inviteID string) error {
	return s.hubAdminJSON(ctx, http.MethodPost, "/api/invites/revoke", hubInviteRevokeAdminRequest{InviteID: strings.TrimSpace(inviteID)}, nil)
}

func (s *Server) listConfiguredHubTrustRequests(ctx context.Context) ([]HubTrustRequestAdmin, error) {
	var result hubTrustPendingAdminResponse
	if err := s.hubAdminJSON(ctx, http.MethodGet, "/api/trust/pending", nil, &result); err != nil {
		return nil, err
	}
	out := make([]HubTrustRequestAdmin, 0, len(result.Requests))
	for _, request := range result.Requests {
		out = append(out, HubTrustRequestAdmin{
			NodeID:   request.NodeID,
			NodeName: request.NodeName,
			Version:  request.Version,
			OS:       request.OS,
			Arch:     request.Arch,
			Status:   request.Status,
		})
	}
	return out, nil
}

func (s *Server) setConfiguredHubTrustDecision(ctx context.Context, nodeID string, allow bool) error {
	path := "/api/trust/deny"
	if allow {
		path = "/api/trust/allow"
	}
	return s.hubAdminJSON(ctx, http.MethodPost, path, hubTrustDecisionAdminRequest{NodeID: strings.TrimSpace(nodeID)}, nil)
}

func webHubJoinCommand(hubURL, token string) string {
	return fmt.Sprintf("agent-deck hub join %s --token %s", strings.TrimSpace(hubURL), strings.TrimSpace(token))
}

func (s *Server) hubAdminJSON(ctx context.Context, method, path string, requestBody any, responseBody any) error {
	settings, token, err := configuredHubAdminCredentials()
	if err != nil {
		return err
	}
	endpoint, err := hubAdminEndpoint(settings.URL, settings.NodeID, path)
	if err != nil {
		return err
	}
	client, err := hubAdminHTTPClient(settings)
	if err != nil {
		return err
	}

	var body io.Reader
	if requestBody != nil {
		data, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &hubAdminHTTPError{status: resp.StatusCode, body: strings.TrimSpace(string(data))}
	}
	if responseBody == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
		return fmt.Errorf("decode hub response: %w", err)
	}
	return nil
}

func configuredHubAdminCredentials() (session.HubSettings, string, error) {
	config, err := session.LoadUserConfig()
	if err != nil {
		return session.HubSettings{}, "", fmt.Errorf("load user config: %w", err)
	}
	if config == nil || !config.Hub.Enabled() {
		return session.HubSettings{}, "", fmt.Errorf("hub is not configured; run agent-deck hub join first")
	}
	settings := config.Hub
	tokenFile := strings.TrimSpace(settings.TokenFile)
	if tokenFile == "" {
		return session.HubSettings{}, "", fmt.Errorf("hub token file is not configured; run agent-deck hub join again")
	}
	tokenData, err := os.ReadFile(tokenFile)
	if err != nil {
		return session.HubSettings{}, "", fmt.Errorf("read hub token file: %w", err)
	}
	token := strings.TrimSpace(string(tokenData))
	if token == "" {
		return session.HubSettings{}, "", fmt.Errorf("hub token file is empty; run agent-deck hub join again")
	}
	return settings, token, nil
}

func hubAdminEndpoint(rawHubURL, nodeID, path string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", fmt.Errorf("hub node id is not configured; run agent-deck hub join again")
	}
	u, err := url.Parse(strings.TrimSpace(rawHubURL))
	if err != nil {
		return "", fmt.Errorf("parse hub URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "wss", "https":
		u.Scheme = "https"
	default:
		return "", fmt.Errorf("hub URL must use wss://")
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("hub URL host is required")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u.Path = path
	q := u.Query()
	q.Set("node_id", nodeID)
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String(), nil
}

func hubAdminHTTPClient(settings session.HubSettings) (*http.Client, error) {
	tlsConfig := &tls.Config{
		ServerName: strings.TrimSpace(settings.ServerName),
	}
	if pinned := strings.TrimSpace(settings.PinnedCertSHA256); pinned != "" {
		// #nosec G402 -- certificate chain validation is replaced by exact SHA-256 pin validation below.
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			rawCerts := make([][]byte, 0, len(state.PeerCertificates))
			for _, cert := range state.PeerCertificates {
				rawCerts = append(rawCerts, cert.Raw)
			}
			return hub.VerifyPinnedCertificate(rawCerts, pinned)
		}
		return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}, Timeout: 30 * time.Second}, nil
	}
	if settings.TLSSkipVerify {
		// #nosec G402 -- explicit user-configured option for private/self-managed hubs.
		tlsConfig.InsecureSkipVerify = true
	}
	if pemFile := strings.TrimSpace(settings.CAPemFile); pemFile != "" {
		pemData, err := os.ReadFile(pemFile)
		if err != nil {
			return nil, fmt.Errorf("read hub CA PEM file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("no certificates found in hub CA PEM file")
		}
		tlsConfig.RootCAs = pool
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}, Timeout: 30 * time.Second}, nil
}

func writeHubAdminError(w http.ResponseWriter, err error) {
	var remote *hubAdminHTTPError
	if errors.As(err, &remote) {
		status := remote.status
		if status == 0 {
			status = http.StatusBadGateway
		}
		message := strings.TrimSpace(remote.body)
		if message == "" {
			message = http.StatusText(status)
		}
		writeAPIError(w, status, hubAdminErrorCode(status), message)
		return
	}
	writeAPIError(w, http.StatusBadGateway, ErrCodeInternalError, err.Error())
}

func hubAdminErrorCode(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return ErrCodeUnauthorized
	case http.StatusForbidden:
		return ErrCodeForbidden
	case http.StatusNotFound:
		return ErrCodeNotFound
	case http.StatusBadRequest:
		return ErrCodeBadRequest
	case http.StatusMethodNotAllowed:
		return ErrCodeMethodNotAllowed
	default:
		return ErrCodeInternalError
	}
}

func (s *Server) updateMenuHubNodeName(nodeID, name string) {
	nodeID = strings.TrimSpace(nodeID)
	name = strings.TrimSpace(name)
	if nodeID == "" || name == "" {
		return
	}
	if mmd, ok := s.menuData.(*MemoryMenuData); ok {
		mmd.UpdateHubNodeName(nodeID, name)
		return
	}
	s.notifyMenuChangedWithoutInvalidation()
}

func (s *Server) removeMenuHubNode(nodeID string) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return
	}
	if mmd, ok := s.menuData.(*MemoryMenuData); ok {
		mmd.RemoveHubNode(nodeID)
		return
	}
	s.notifyMenuChangedWithoutInvalidation()
}
