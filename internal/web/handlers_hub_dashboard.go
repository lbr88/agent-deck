package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/hub"
)

type HubDashboardProxy interface {
	ProxyHubWeb(ctx context.Context, nodeID string, req hub.WebProxyRequest) (hub.WebProxyResponse, error)
}

func (s *Server) handleHubDashboardProxy(w http.ResponseWriter, r *http.Request) {
	nodeID, remotePath, ok := parseHubDashboardPath(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "hub dashboard node id is required")
		return
	}
	if remotePath == "/events/menu" || strings.HasPrefix(remotePath, "/events/menu?") {
		s.handleHubDashboardSSE(w, r, nodeID, "menu", "/api/menu")
		return
	}
	if remotePath == "/events/command-center" || strings.HasPrefix(remotePath, "/events/command-center?") {
		s.handleHubDashboardSSE(w, r, nodeID, "command-center", "/api/command-center/status")
		return
	}
	if remotePath == "/ws/session" || strings.HasPrefix(remotePath, "/ws/session/") {
		if !s.authorizeWSRequest(r) {
			writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
			return
		}
		if !s.hubDashboardNodeWebAvailable(nodeID) {
			s.writeHubDashboardUnavailable(w, nodeID)
			return
		}
		s.handleHubDashboardSessionWS(w, r, nodeID, remotePath)
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	if !s.hubDashboardNodeWebAvailable(nodeID) {
		s.writeHubDashboardUnavailable(w, nodeID)
		return
	}

	proxy, ok := s.mutator.(HubDashboardProxy)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, "hub dashboard proxy not available")
		return
	}
	body, err := readHubDashboardRequestBody(r)
	if err != nil {
		writeAPIError(w, http.StatusRequestEntityTooLarge, ErrCodeBadRequest, err.Error())
		return
	}
	resp, err := proxy.ProxyHubWeb(r.Context(), nodeID, hub.WebProxyRequest{
		Method:  r.Method,
		Path:    remotePath,
		Header:  filteredHubDashboardRequestHeader(r.Header),
		BodyB64: base64.StdEncoding.EncodeToString(body),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, ErrCodeInternalError, err.Error())
		return
	}
	writeHubDashboardProxyResponse(w, nodeID, resp)
}

func (s *Server) hubDashboardNodeWebAvailable(nodeID string) bool {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || s == nil || s.menuData == nil {
		return true
	}
	snapshot, err := s.menuData.LoadMenuSnapshot()
	if err != nil || snapshot == nil {
		return true
	}
	for _, node := range snapshot.HubNodes {
		if node.ID == nodeID {
			return node.WebAvailable
		}
	}
	return true
}

func (s *Server) writeHubDashboardUnavailable(w http.ResponseWriter, nodeID string) {
	writeAPIError(
		w,
		http.StatusServiceUnavailable,
		ErrCodeUnavailable,
		fmt.Sprintf("remote dashboard for hub node %q is not available", nodeID),
	)
}

func (s *Server) handleHubDashboardSessionWS(w http.ResponseWriter, r *http.Request, nodeID, remotePath string) {
	sessionID, ok := hubDashboardWSSessionID(remotePath)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "session id is required")
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	writer := newWSConnWriter(conn)
	_ = writer.WriteJSON(wsServerMessage{
		Type:      "status",
		Event:     "connected",
		SessionID: sessionID,
		Profile:   s.cfg.Profile,
		ReadOnly:  s.cfg.ReadOnly,
		Time:      time.Now().UTC(),
	})
	_ = writer.WriteJSON(wsServerMessage{
		Type:      "status",
		Event:     "ready",
		SessionID: sessionID,
		Time:      time.Now().UTC(),
	})

	var bridge terminalBridge
	attacher, ok := s.mutator.(HubTerminalAttacher)
	if !ok {
		_ = writer.WriteJSON(wsServerMessage{
			Type:      "error",
			Code:      "HUB_TERMINAL_UNAVAILABLE",
			Message:   "hub terminal attach is not available",
			SessionID: sessionID,
			Time:      time.Now().UTC(),
		})
	} else {
		attachCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		var stream hub.AttachStream
		if terminalSandboxShellRequested(r) {
			stream, err = attacher.OpenHubSandboxShellTerminal(attachCtx, nodeID, sessionID, hub.TerminalSize{Cols: 80, Rows: 24})
		} else {
			stream, err = attacher.OpenHubTerminal(attachCtx, nodeID, sessionID, hub.TerminalSize{Cols: 80, Rows: 24})
		}
		cancel()
		if err != nil {
			_ = writer.WriteJSON(wsServerMessage{
				Type:      "error",
				Code:      "HUB_TERMINAL_ATTACH_FAILED",
				Message:   "failed to attach hub terminal bridge",
				Hint:      "Make sure the remote hub node is online and running an agent-deck version that supports interactive hub attach.",
				SessionID: sessionID,
				Time:      time.Now().UTC(),
			})
		} else if bridge, err = newHubAttachBridge(stream, sessionID, writer); err != nil {
			_ = stream.Close()
			_ = writer.WriteJSON(wsServerMessage{
				Type:      "error",
				Code:      "HUB_TERMINAL_ATTACH_FAILED",
				Message:   "failed to initialize hub terminal bridge",
				SessionID: sessionID,
				Time:      time.Now().UTC(),
			})
		} else {
			defer bridge.Close()
			_ = writer.WriteJSON(wsServerMessage{
				Type:      "status",
				Event:     "terminal_attached",
				SessionID: sessionID,
				Time:      time.Now().UTC(),
			})
		}
	}

	s.serveTerminalWSMessages(conn, writer, sessionID, bridge)
}

func hubDashboardWSSessionID(remotePath string) (string, bool) {
	path, _, _ := strings.Cut(remotePath, "?")
	const prefix = "/ws/session/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(path, prefix)
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "/") {
		return "", false
	}
	sessionID, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(sessionID) == "" || strings.Contains(sessionID, "/") {
		return "", false
	}
	return strings.TrimSpace(sessionID), true
}

func parseHubDashboardPath(r *http.Request) (nodeID, remotePath string, ok bool) {
	const prefix = "/hub/dashboard/"
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	if rest == "" || rest == r.URL.Path {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	nodeID, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(nodeID) == "" {
		return "", "", false
	}
	if len(parts) == 1 || parts[1] == "" {
		remotePath = "/"
	} else {
		remotePath = "/" + parts[1]
	}
	if r.URL.RawQuery != "" {
		remotePath += "?" + r.URL.RawQuery
	}
	return strings.TrimSpace(nodeID), remotePath, true
}

func readHubDashboardRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, hub.MaxWebProxyBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if len(data) > hub.MaxWebProxyBodyBytes {
		return nil, fmt.Errorf("hub dashboard request body exceeds %d bytes", hub.MaxWebProxyBodyBytes)
	}
	return data, nil
}

func filteredHubDashboardRequestHeader(src http.Header) map[string][]string {
	out := make(map[string][]string)
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(key))
		switch canonical {
		case "Accept", "Accept-Encoding", "Accept-Language", "Content-Type", "If-Modified-Since", "If-None-Match", "User-Agent":
		default:
			continue
		}
		out[canonical] = append([]string(nil), values...)
	}
	return out
}

func writeHubDashboardProxyResponse(w http.ResponseWriter, nodeID string, resp hub.WebProxyResponse) {
	body, err := base64.StdEncoding.DecodeString(resp.BodyB64)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, ErrCodeInternalError, "remote dashboard returned invalid body")
		return
	}
	header := w.Header()
	for key, values := range resp.Header {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(key))
		if canonical == "" || strings.EqualFold(canonical, "Content-Length") {
			continue
		}
		for _, value := range values {
			header.Add(canonical, value)
		}
	}
	body = rewriteHubDashboardBody(nodeID, header.Get("Content-Type"), body)
	header.Del("Content-Length")
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func rewriteHubDashboardBody(nodeID, contentType string, body []byte) []byte {
	ct := strings.ToLower(contentType)
	if !strings.Contains(ct, "text/html") &&
		!strings.Contains(ct, "javascript") &&
		!strings.Contains(ct, "text/css") &&
		!strings.Contains(ct, "application/json") {
		return body
	}
	prefix := "/hub/dashboard/" + url.PathEscape(nodeID)
	replacements := []struct{ old, new string }{
		{`"/api/`, `"` + prefix + `/api/`},
		{`'/api/`, `'` + prefix + `/api/`},
		{"`/api/", "`" + prefix + "/api/"},
		{`"/events/`, `"` + prefix + `/events/`},
		{`'/events/`, `'` + prefix + `/events/`},
		{"`/events/", "`" + prefix + "/events/"},
		{`"/ws/`, `"` + prefix + `/ws/`},
		{`'/ws/`, `'` + prefix + `/ws/`},
		{"`/ws/", "`" + prefix + "/ws/"},
		{`"/static/`, `"` + prefix + `/static/`},
		{`'/static/`, `'` + prefix + `/static/`},
		{`"/s/`, `"` + prefix + `/s/`},
		{`'/s/`, `'` + prefix + `/s/`},
		{"`/s/", "`" + prefix + "/s/"},
		{`href="/manifest.webmanifest"`, `href="` + prefix + `/manifest.webmanifest"`},
		{`href='/manifest.webmanifest'`, `href='` + prefix + `/manifest.webmanifest'`},
		{`navigator.serviceWorker.register('/sw.js', { scope: '/' })`, `Promise.resolve()`},
	}
	text := string(body)
	for _, repl := range replacements {
		text = strings.ReplaceAll(text, repl.old, repl.new)
	}
	return []byte(text)
}

func (s *Server) handleHubDashboardSSE(w http.ResponseWriter, r *http.Request, nodeID, eventName, remotePath string) {
	if !s.authorizeStreamRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	if !s.hubDashboardNodeWebAvailable(nodeID) {
		s.writeHubDashboardUnavailable(w, nodeID)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "stream unavailable")
		return
	}
	proxy, ok := s.mutator.(HubDashboardProxy)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, "hub dashboard proxy not available")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	last := ""
	emit := func() error {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		resp, err := proxy.ProxyHubWeb(ctx, nodeID, hub.WebProxyRequest{Method: http.MethodGet, Path: remotePath})
		if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil
		}
		body, err := base64.StdEncoding.DecodeString(resp.BodyB64)
		if err != nil {
			return nil
		}
		current := string(body)
		if current == "" || current == last {
			return nil
		}
		last = current
		var payload any = json.RawMessage(body)
		if !json.Valid(body) {
			payload = map[string]string{"error": "remote dashboard returned invalid JSON"}
		}
		return writeSSEEvent(w, flusher, eventName, payload)
	}
	_ = emit()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := emit(); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEComment(w, flusher, "keepalive"); err != nil {
				return
			}
		}
	}
}
