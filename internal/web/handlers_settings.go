package web

import (
	"context"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}

	// Tool-visibility filter (issue #1259) is read from the process registry at
	// request time, so it reflects the current config (re-probed only when config
	// changes — see currentRegistry). It is a display filter only.
	hubConfigured, hubAdmin := s.hubManagementCapabilities(r.Context())
	writeJSON(w, http.StatusOK, SettingsResponse{
		Profile:            s.cfg.Profile,
		ReadOnly:           s.cfg.ReadOnly,
		WebMutations:       s.cfg.WebMutations,
		Version:            buildVersion(),
		HubConfigured:      hubConfigured,
		HubAdmin:           hubAdmin,
		ToolFilter:         session.ToolFilterActive(),
		VisibleTools:       session.VisibleToolNames(),
		ToolFilterFallback: session.ToolFilterFallbackActive(),
		HiddenTools:        session.ConfiguredHiddenToolNames(),
		PickerTools:        session.PickerToolNames(),
	})
}

func (s *Server) hubManagementCapabilities(ctx context.Context) (bool, bool) {
	if _, _, err := configuredHubAdminCredentials(); err != nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	var status hubStatusAdminResponse
	if err := s.hubAdminJSON(ctx, http.MethodGet, "/api/status", nil, &status); err != nil {
		return true, false
	}
	return true, status.Node.Admin
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	profiles, err := session.ListProfiles()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}
	// Ensure current profile appears in list even if its directory lacks state.db.
	current := s.cfg.Profile
	found := false
	for _, p := range profiles {
		if p == current {
			found = true
			break
		}
	}
	if !found {
		profiles = append([]string{current}, profiles...)
	}
	writeJSON(w, http.StatusOK, ProfilesResponse{
		Current:  current,
		Profiles: profiles,
	})
}

// buildVersion returns the binary version from embedded build info.
// Falls back to "dev" when build info is unavailable (e.g. during tests).
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
