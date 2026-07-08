package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/asheshgoplani/agent-deck/internal/logging"
	"github.com/gorilla/websocket"
)

type wsClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type wsServerMessage struct {
	Type      string    `json:"type"` // status, error
	Event     string    `json:"event,omitempty"`
	Code      string    `json:"code,omitempty"`
	Message   string    `json:"message,omitempty"`
	Hint      string    `json:"hint,omitempty"` // #782: actionable next step for terminal-fatal errors
	SessionID string    `json:"sessionId,omitempty"`
	Profile   string    `json:"profile,omitempty"`
	ReadOnly  bool      `json:"readOnly,omitempty"`
	Time      time.Time `json:"time,omitempty"`
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     allowWSOrigin,
}

func allowWSOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host == "" {
		return false
	}

	return strings.EqualFold(originURL.Host, r.Host)
}

func (s *Server) handleSessionWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	if !s.authorizeWSRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	const prefix = "/ws/session/"
	sessionID := strings.TrimPrefix(r.URL.Path, prefix)
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "session id is required")
		return
	}

	snapshot, err := s.menuData.LoadMenuSnapshot()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load session data")
		return
	}

	menuSession, found := snapshotSessionByID(snapshot, sessionID)
	if !found {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
		return
	}
	sandboxShell := terminalSandboxShellRequested(r)

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
		Profile:   snapshot.Profile,
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
	if hubNodeID, hubSessionID, ok := hubTerminalTarget(menuSession); ok {
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
			if sandboxShell {
				stream, err = attacher.OpenHubSandboxShellTerminal(attachCtx, hubNodeID, hubSessionID, hub.TerminalSize{Cols: 80, Rows: 24})
			} else {
				stream, err = attacher.OpenHubTerminal(attachCtx, hubNodeID, hubSessionID, hub.TerminalSize{Cols: 80, Rows: 24})
			}
			cancel()
			if err != nil {
				logging.ForComponent(logging.CompWeb).Error("hub_terminal_attach_failed",
					slog.String("session_id", sessionID),
					slog.String("hub_node_id", hubNodeID),
					slog.String("hub_session_id", hubSessionID),
					slog.String("error", err.Error()))
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
	} else if sandboxShell {
		opener, ok := s.mutator.(LocalSandboxShellOpener)
		if !ok {
			_ = writer.WriteJSON(wsServerMessage{
				Type:      "error",
				Code:      "SANDBOX_TERMINAL_UNAVAILABLE",
				Message:   "sandbox terminal shell is not available",
				SessionID: sessionID,
				Time:      time.Now().UTC(),
			})
		} else {
			attachCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			tmuxSession, tmuxSocket, err := opener.OpenLocalSandboxShell(attachCtx, sessionID)
			cancel()
			if err != nil {
				logging.ForComponent(logging.CompWeb).Error("sandbox_terminal_attach_failed",
					slog.String("session_id", sessionID),
					slog.String("error", err.Error()))
				_ = writer.WriteJSON(wsServerMessage{
					Type:      "error",
					Code:      "SANDBOX_TERMINAL_ATTACH_FAILED",
					Message:   "failed to attach sandbox terminal bridge",
					Hint:      "Make sure the session is sandboxed and its Docker container is still running.",
					SessionID: sessionID,
					Time:      time.Now().UTC(),
				})
			} else if bridge, err = newTmuxPTYBridge(tmuxSession, tmuxSocket, sessionID, writer); err != nil {
				_ = writer.WriteJSON(wsServerMessage{
					Type:      "error",
					Code:      "SANDBOX_TERMINAL_ATTACH_FAILED",
					Message:   "failed to initialize sandbox terminal bridge",
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
	} else if menuSession.TmuxSession != "" {
		if acknowledger, ok := s.mutator.(LocalTerminalAcknowledger); ok {
			if err := acknowledger.AcknowledgeLocalTerminal(sessionID); err != nil {
				logging.ForComponent(logging.CompWeb).Warn("terminal_acknowledge_failed",
					slog.String("session_id", sessionID),
					slog.String("error", err.Error()))
			}
		}
		bridge, err = newTmuxPTYBridge(menuSession.TmuxSession, menuSession.TmuxSocketName, sessionID, writer)
		if err != nil {
			logging.ForComponent(logging.CompWeb).Error("terminal_attach_failed",
				slog.String("session_id", sessionID),
				slog.String("tmux_session", menuSession.TmuxSession),
				slog.String("error", err.Error()))
			code := "TERMINAL_ATTACH_FAILED"
			message := "failed to attach terminal bridge"
			// #782: terminal-fatal errors get an actionable hint so the
			// WebUI can render guidance instead of repeating an opaque
			// `[error:CODE]` line on every reconnect attempt.
			hint := "Check the server logs for details."
			if errors.Is(err, ErrTmuxSessionNotFound) {
				code = "TMUX_SESSION_NOT_FOUND"
				message = "tmux session is not available"
				hint = "The tmux session for this entry no longer exists. Restart it from the sidebar (Restart icon, or press 'r' with the row focused) to create a fresh tmux session."
			}
			_ = writer.WriteJSON(wsServerMessage{
				Type:      "error",
				Code:      code,
				Message:   message,
				Hint:      hint,
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

func (s *Server) serveTerminalWSMessages(conn *websocket.Conn, writer *wsConnWriter, sessionID string, bridge terminalBridge) {
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
			) {
				logging.ForComponent(logging.CompWeb).Warn("websocket_closed_unexpectedly",
					slog.String("session_id", sessionID),
					slog.String("error", err.Error()))
			}
			return
		}

		var msg wsClientMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			_ = writer.WriteJSON(wsServerMessage{
				Type:      "error",
				Code:      "INVALID_MESSAGE",
				Message:   "invalid json payload",
				SessionID: sessionID,
				Time:      time.Now().UTC(),
			})
			continue
		}

		switch msg.Type {
		case "ping":
			_ = writer.WriteJSON(wsServerMessage{
				Type:      "status",
				Event:     "pong",
				SessionID: sessionID,
				Time:      time.Now().UTC(),
			})
		case "input":
			if s.cfg.ReadOnly {
				_ = writer.WriteJSON(wsServerMessage{
					Type:      "error",
					Code:      "READ_ONLY",
					Message:   "input is disabled in read-only mode",
					SessionID: sessionID,
					Time:      time.Now().UTC(),
				})
				continue
			}
			if bridge == nil {
				_ = writer.WriteJSON(wsServerMessage{
					Type:      "error",
					Code:      "NO_TERMINAL_BRIDGE",
					Message:   "terminal bridge is not attached",
					SessionID: sessionID,
					Time:      time.Now().UTC(),
				})
				continue
			}
			if err := bridge.WriteInput(msg.Data); err != nil {
				_ = writer.WriteJSON(wsServerMessage{
					Type:      "error",
					Code:      "INPUT_WRITE_FAILED",
					Message:   "failed to send input to terminal",
					SessionID: sessionID,
					Time:      time.Now().UTC(),
				})
			}
		case "resize":
			if bridge == nil {
				_ = writer.WriteJSON(wsServerMessage{
					Type:      "error",
					Code:      "NO_TERMINAL_BRIDGE",
					Message:   "terminal bridge is not attached",
					SessionID: sessionID,
					Time:      time.Now().UTC(),
				})
				continue
			}
			if err := bridge.Resize(msg.Cols, msg.Rows); err != nil {
				_ = writer.WriteJSON(wsServerMessage{
					Type:      "error",
					Code:      "RESIZE_FAILED",
					Message:   "failed to resize terminal",
					SessionID: sessionID,
					Time:      time.Now().UTC(),
				})
			}
		default:
			_ = writer.WriteJSON(wsServerMessage{
				Type:      "error",
				Code:      "UNSUPPORTED_MESSAGE",
				Message:   "supported message types: ping,input,resize",
				SessionID: sessionID,
				Time:      time.Now().UTC(),
			})
		}
	}
}

func hubTerminalTarget(menuSession *MenuSession) (nodeID, sessionID string, ok bool) {
	if menuSession == nil {
		return "", "", false
	}
	if nodeID, sessionID, ok = ParseHubSessionWebID(menuSession.ID); ok {
		return nodeID, sessionID, true
	}
	nodeID = strings.TrimSpace(menuSession.HubNodeID)
	sessionID = strings.TrimSpace(menuSession.HubSessionID)
	if menuSession.Source == "hub" && nodeID != "" && sessionID != "" {
		return nodeID, sessionID, true
	}
	return "", "", false
}

func terminalSandboxShellRequested(r *http.Request) bool {
	if r == nil {
		return false
	}
	q := r.URL.Query()
	return strings.EqualFold(strings.TrimSpace(q.Get("shell")), "sandbox") ||
		strings.EqualFold(strings.TrimSpace(q.Get("terminal")), "sandbox")
}

func snapshotSessionByID(snapshot *MenuSnapshot, sessionID string) (*MenuSession, bool) {
	if snapshot == nil {
		return nil, false
	}
	for _, item := range snapshot.Items {
		if item.Type != MenuItemTypeSession || item.Session == nil {
			continue
		}
		if item.Session.ID == sessionID {
			return item.Session, true
		}
	}
	return nil, false
}
