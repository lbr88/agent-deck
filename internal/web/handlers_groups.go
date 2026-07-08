package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type groupsListResponse struct {
	Groups []*MenuGroup `json:"groups"`
}

func (s *Server) handleGroupsCollection(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		snapshot, err := s.menuData.LoadMenuSnapshot()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to load group data")
			return
		}
		resp := groupsListResponse{
			Groups: make([]*MenuGroup, 0),
		}
		for _, item := range snapshot.Items {
			if item.Type == MenuItemTypeGroup && item.Group != nil {
				resp.Groups = append(resp.Groups, item.Group)
			}
		}
		writeJSON(w, http.StatusOK, resp)

	case http.MethodPost:
		if !s.checkMutationsAllowed(w) {
			return
		}
		if !s.checkMutationRateLimit(w) {
			return
		}
		var req CreateGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
			return
		}
		if req.Name == "" {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "name is required")
			return
		}
		if s.mutator == nil {
			writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, "mutations not available")
			return
		}
		groupPath, err := s.mutator.CreateGroup(req.Name, req.ParentPath)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
			return
		}
		s.notifyMenuChanged()
		writeJSON(w, http.StatusCreated, map[string]string{"path": groupPath})

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleGroupByPath(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}

	const groupPrefix = "/api/groups/"
	groupPath := strings.TrimPrefix(r.URL.Path, groupPrefix)
	if groupPath == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "group path is required")
		return
	}
	if strings.HasSuffix(groupPath, "/change") {
		s.handleGroupChange(w, r, strings.TrimSuffix(groupPath, "/change"))
		return
	}
	if strings.HasSuffix(groupPath, "/reorder") {
		s.handleGroupReorder(w, r, strings.TrimSuffix(groupPath, "/reorder"))
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
		var req RenameGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
			return
		}
		if req.Name == "" {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "name is required")
			return
		}
		if s.mutator == nil {
			writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, "mutations not available")
			return
		}
		if err := s.mutator.RenameGroup(groupPath, req.Name); err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
			return
		}
		s.notifyMenuChanged()
		writeJSON(w, http.StatusOK, map[string]string{"path": groupPath, "name": req.Name})

	case http.MethodDelete:
		if !s.checkMutationsAllowed(w) {
			return
		}
		if !s.checkMutationRateLimit(w) {
			return
		}
		if groupPath == session.DefaultGroupPath {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "cannot delete default group")
			return
		}
		if s.mutator == nil {
			writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, "mutations not available")
			return
		}
		if err := s.mutator.DeleteGroup(groupPath); err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
			return
		}
		s.notifyMenuChanged()
		writeJSON(w, http.StatusOK, map[string]string{"deleted": groupPath})

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleGroupChange(w http.ResponseWriter, r *http.Request, groupPath string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if strings.TrimSpace(groupPath) == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "group path is required")
		return
	}
	if !s.checkMutationsAllowed(w) {
		return
	}
	if !s.checkMutationRateLimit(w) {
		return
	}
	var req ReparentGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if s.mutator == nil {
		writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, "mutations not available")
		return
	}
	newPath, err := s.mutator.ReparentGroup(groupPath, req.DestParentPath)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}
	s.notifyMenuChanged()
	writeJSON(w, http.StatusOK, map[string]string{"oldPath": groupPath, "path": newPath})
}

func (s *Server) handleGroupReorder(w http.ResponseWriter, r *http.Request, groupPath string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if strings.TrimSpace(groupPath) == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "group path is required")
		return
	}
	if !s.checkMutationsAllowed(w) {
		return
	}
	if !s.checkMutationRateLimit(w) {
		return
	}
	var req ReorderGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Direction) == "" && req.Position == nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "direction or position is required")
		return
	}
	if s.mutator == nil {
		writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, "mutations not available")
		return
	}
	from, to, err := s.mutator.ReorderGroup(groupPath, req.Direction, req.Position)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}
	s.notifyMenuChanged()
	writeJSON(w, http.StatusOK, map[string]any{"path": groupPath, "fromPosition": from, "toPosition": to})
}
