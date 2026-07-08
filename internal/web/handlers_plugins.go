package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

var errPluginManagerNotAvailable = errors.New("plugin manager not available")

// PluginManager is the seam between web HTTP handlers and plugin attachment
// state. ui.WebMutator implements this so hub session IDs can route to the
// owning Agent Deck node; tests can inject a fake manager.
type PluginManager interface {
	ListPluginCatalog() []PluginCatalogEntry
	ListSessionPlugins(sessionID string, sess *MenuSession) (SessionPluginsResponse, error)
	AttachPlugin(sessionID string, sess *MenuSession, name string, noChannelLink bool) (PluginMutateResponse, error)
	DetachPlugin(sessionID string, sess *MenuSession, name string) (PluginMutateResponse, error)
}

type PluginCatalogEntry struct {
	Name         string `json:"name"`
	PluginName   string `json:"pluginName,omitempty"`
	Source       string `json:"source,omitempty"`
	EmitsChannel bool   `json:"emitsChannel,omitempty"`
	AutoInstall  bool   `json:"autoInstall,omitempty"`
}

type PluginsCatalogResponse struct {
	Plugins []PluginCatalogEntry `json:"plugins"`
}

type SessionPluginsResponse struct {
	SessionID                 string               `json:"sessionId"`
	Catalog                   []PluginCatalogEntry `json:"catalog"`
	Plugins                   []string             `json:"plugins"`
	Channels                  []string             `json:"channels,omitempty"`
	PluginChannelLinkDisabled bool                 `json:"pluginChannelLinkDisabled,omitempty"`
}

type PluginMutateResponse struct {
	SessionID                 string   `json:"sessionId"`
	Plugins                   []string `json:"plugins"`
	Channels                  []string `json:"channels,omitempty"`
	PluginChannelLinkDisabled bool     `json:"pluginChannelLinkDisabled,omitempty"`
	RestartRequired           bool     `json:"restartRequired"`
}

type pluginMutateRequest struct {
	NoChannelLink bool `json:"noChannelLink,omitempty"`
}

type defaultPluginManager struct{}

func NewDefaultPluginManager() PluginManager { return defaultPluginManager{} }

func (defaultPluginManager) ListPluginCatalog() []PluginCatalogEntry {
	return pluginCatalogEntriesFromSession()
}

func (defaultPluginManager) ListSessionPlugins(sessionID string, sess *MenuSession) (SessionPluginsResponse, error) {
	return pluginStateFromMenuSession(sessionID, sess), nil
}

func (defaultPluginManager) AttachPlugin(string, *MenuSession, string, bool) (PluginMutateResponse, error) {
	return PluginMutateResponse{}, errPluginManagerNotAvailable
}

func (defaultPluginManager) DetachPlugin(string, *MenuSession, string) (PluginMutateResponse, error) {
	return PluginMutateResponse{}, errPluginManagerNotAvailable
}

func pluginCatalogEntriesFromSession() []PluginCatalogEntry {
	plugins := session.GetAvailablePlugins()
	names := session.GetAvailablePluginNames()
	out := make([]PluginCatalogEntry, 0, len(names))
	for _, name := range names {
		def := plugins[name]
		out = append(out, PluginCatalogEntry{
			Name:         name,
			PluginName:   def.Name,
			Source:       def.Source,
			EmitsChannel: def.EmitsChannel,
			AutoInstall:  def.AutoInstall,
		})
	}
	return out
}

func pluginStateFromMenuSession(sessionID string, sess *MenuSession) SessionPluginsResponse {
	return SessionPluginsResponse{
		SessionID:                 sessionID,
		Catalog:                   pluginCatalogEntriesFromSession(),
		Plugins:                   sortedStrings(sessPlugins(sess)),
		Channels:                  sortedStrings(sessChannels(sess)),
		PluginChannelLinkDisabled: sess != nil && sess.PluginChannelLinkDisabled,
	}
}

func sessPlugins(sess *MenuSession) []string {
	if sess == nil {
		return nil
	}
	return sess.Plugins
}

func sessChannels(sess *MenuSession) []string {
	if sess == nil {
		return nil
	}
	return sess.Channels
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	if out == nil {
		return []string{}
	}
	return out
}

func (s *Server) pluginManagerOrDefault() PluginManager {
	if s.plugins != nil {
		return s.plugins
	}
	return defaultPluginManager{}
}

func (s *Server) handlePluginsCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	catalog := s.pluginManagerOrDefault().ListPluginCatalog()
	if catalog == nil {
		catalog = []PluginCatalogEntry{}
	}
	writeJSON(w, http.StatusOK, PluginsCatalogResponse{Plugins: catalog})
}

func (s *Server) handleSessionPluginsRouter(w http.ResponseWriter, r *http.Request) {
	s.handleSessionPlugins(w, r, r.PathValue("id"), r.PathValue("name"))
}

func (s *Server) handleSessionPlugins(w http.ResponseWriter, r *http.Request, sessionID, rawName string) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	if strings.TrimSpace(sessionID) == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "session id is required")
		return
	}
	sess, ok := s.lookupSession(sessionID)
	if !ok {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "session not found")
		return
	}
	manager := s.pluginManagerOrDefault()

	if rawName == "" {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		state, err := manager.ListSessionPlugins(sessionID, sess)
		if err != nil {
			writePluginError(w, err)
			return
		}
		if state.Catalog == nil {
			state.Catalog = []PluginCatalogEntry{}
		}
		if state.Plugins == nil {
			state.Plugins = []string{}
		}
		if state.Channels == nil {
			state.Channels = []string{}
		}
		writeJSON(w, http.StatusOK, state)
		return
	}

	name, err := url.PathUnescape(rawName)
	if err != nil || strings.TrimSpace(name) == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "plugin name is required")
		return
	}
	name = strings.TrimSpace(name)

	switch r.Method {
	case http.MethodPost:
		if !s.checkMutationsAllowed(w) || !s.checkMutationRateLimit(w) {
			return
		}
		var req pluginMutateRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid plugin request JSON")
				return
			}
		}
		resp, err := manager.AttachPlugin(sessionID, sess, name, req.NoChannelLink)
		if err != nil {
			writePluginError(w, err)
			return
		}
		s.notifyMenuChanged()
		writeJSON(w, http.StatusOK, resp)

	case http.MethodDelete:
		if !s.checkMutationsAllowed(w) || !s.checkMutationRateLimit(w) {
			return
		}
		resp, err := manager.DetachPlugin(sessionID, sess, name)
		if err != nil {
			writePluginError(w, err)
			return
		}
		s.notifyMenuChanged()
		writeJSON(w, http.StatusOK, resp)

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func writePluginError(w http.ResponseWriter, err error) {
	if errors.Is(err, errPluginManagerNotAvailable) {
		writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, err.Error())
		return
	}
	var mutErr *session.MutationError
	if errors.As(err, &mutErr) {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, mutErr.Error())
		return
	}
	if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not attached") {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, fmt.Sprintf("%v", err))
}
