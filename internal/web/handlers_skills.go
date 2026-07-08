package web

// Web UI skills management handlers.
//
// Mirror of the TUI `s` key dialog (internal/ui/home.go:6597) so Web users
// can attach/detach/list project-scoped skills without dropping to the
// terminal. Closes the MISSING rows in tests/web/PARITY_MATRIX.md
// "SKILLS MANAGEMENT" section.

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// SkillsService is the seam between web HTTP handlers and the on-disk
// skill catalog/attachment functions in internal/session. Tests inject a
// fake; production gets defaultSkillsService which delegates straight to
// the session package.
type SkillsService interface {
	ListSkillCatalog() ([]session.SkillCandidate, error)
	ListSessionSkills(sessionID, projectPath string) (SkillSessionState, error)
	AttachSkill(sessionID, projectPath, tool, skillRef, source string) (*session.ProjectSkillAttachment, error)
	DetachSkill(sessionID, projectPath, skillRef, source string) (*session.ProjectSkillAttachment, error)
}

type SkillSessionState struct {
	Catalog  []session.SkillCandidate
	Attached []session.ProjectSkillAttachment
}

type defaultSkillsService struct{}

func (defaultSkillsService) ListSkillCatalog() ([]session.SkillCandidate, error) {
	return session.ListAvailableSkills()
}

func (defaultSkillsService) ListSessionSkills(_, projectPath string) (SkillSessionState, error) {
	catalog, err := session.ListAvailableSkills()
	if err != nil {
		return SkillSessionState{}, err
	}
	attached, err := session.GetAttachedProjectSkills(projectPath)
	if err != nil {
		return SkillSessionState{}, err
	}
	return SkillSessionState{Catalog: catalog, Attached: attached}, nil
}

func (defaultSkillsService) AttachSkill(_, projectPath, tool, skillRef, source string) (*session.ProjectSkillAttachment, error) {
	return session.AttachSkillToProject(projectPath, tool, skillRef, source)
}

func (defaultSkillsService) DetachSkill(_, projectPath, skillRef, source string) (*session.ProjectSkillAttachment, error) {
	return session.DetachSkillFromProject(projectPath, skillRef, source)
}

type skillsCatalogResponse struct {
	Skills []session.SkillCandidate `json:"skills"`
}

type sessionSkillsResponse struct {
	Skills  []session.ProjectSkillAttachment `json:"skills"`
	Catalog []session.SkillCandidate         `json:"catalog,omitempty"`
}

type skillActionResponse struct {
	Skill *session.ProjectSkillAttachment `json:"skill"`
}

// handleSkillsCatalog serves GET /api/skills.
func (s *Server) handleSkillsCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}

	svc := s.skillsServiceOrDefault()
	catalog, err := svc.ListSkillCatalog()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to list skills")
		return
	}
	if catalog == nil {
		catalog = []session.SkillCandidate{}
	}
	writeJSON(w, http.StatusOK, skillsCatalogResponse{Skills: catalog})
}

// handleSessionSkills serves the per-session skill routes:
//
//	GET    /api/sessions/{id}/skills            -> list attached
//	POST   /api/sessions/{id}/skills/{name}     -> attach
//	DELETE /api/sessions/{id}/skills/{name}     -> detach
//
// The caller (handleSessionByAction) has already authorized the request
// and resolved sessionID + the remaining path suffix after `skills`.
func (s *Server) handleSessionSkills(w http.ResponseWriter, r *http.Request, sessionID, subpath string) {
	sess, ok := s.lookupSession(sessionID)
	if !ok {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "session not found")
		return
	}

	skillName := strings.TrimSpace(subpath)
	if decoded, err := url.PathUnescape(skillName); err == nil {
		skillName = decoded
	}
	source := r.URL.Query().Get("source")

	switch r.Method {
	case http.MethodGet:
		if skillName != "" {
			writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "route not found")
			return
		}
		svc := s.skillsServiceOrDefault()
		state, err := svc.ListSessionSkills(sessionID, sess.ProjectPath)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to list attached skills")
			return
		}
		if state.Attached == nil {
			state.Attached = []session.ProjectSkillAttachment{}
		}
		if state.Catalog == nil {
			state.Catalog = []session.SkillCandidate{}
		}
		writeJSON(w, http.StatusOK, sessionSkillsResponse{Skills: state.Attached, Catalog: state.Catalog})

	case http.MethodPost:
		if !s.checkMutationsAllowed(w) {
			return
		}
		if !s.checkMutationRateLimit(w) {
			return
		}
		if skillName == "" {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "skill name is required")
			return
		}
		if !session.SupportsProjectSkills(sess.Tool) {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "session tool does not support project skills")
			return
		}
		svc := s.skillsServiceOrDefault()
		att, err := svc.AttachSkill(sessionID, sess.ProjectPath, sess.Tool, skillName, source)
		if err != nil {
			writeSkillError(w, err)
			return
		}
		s.notifyMenuChanged()
		writeJSON(w, http.StatusOK, skillActionResponse{Skill: att})

	case http.MethodDelete:
		if !s.checkMutationsAllowed(w) {
			return
		}
		if !s.checkMutationRateLimit(w) {
			return
		}
		if skillName == "" {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "skill name is required")
			return
		}
		svc := s.skillsServiceOrDefault()
		att, err := svc.DetachSkill(sessionID, sess.ProjectPath, skillName, source)
		if err != nil {
			writeSkillError(w, err)
			return
		}
		s.notifyMenuChanged()
		writeJSON(w, http.StatusOK, skillActionResponse{Skill: att})

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

// writeSkillError maps session-layer skill errors to HTTP responses with
// stable codes the frontend can switch on.
func writeSkillError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrSkillNotFound):
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
	case errors.Is(err, session.ErrSkillNotAttached):
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
	case errors.Is(err, session.ErrSkillAlreadyAttached):
		writeAPIError(w, http.StatusConflict, ErrCodeBadRequest, err.Error())
	case errors.Is(err, session.ErrSkillAmbiguous):
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
	case errors.Is(err, session.ErrSkillUnsupportedKind):
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
	case errors.Is(err, session.ErrSkillTargetConflict):
		writeAPIError(w, http.StatusConflict, ErrCodeBadRequest, err.Error())
	default:
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
	}
}

// lookupSession resolves a sessionID to its MenuSession via the menu
// snapshot. Used by per-session handlers that need projectPath + tool.
func (s *Server) lookupSession(sessionID string) (*MenuSession, bool) {
	snapshot, err := s.menuData.LoadMenuSnapshot()
	if err != nil || snapshot == nil {
		return nil, false
	}
	for _, item := range snapshot.Items {
		if item.Type == MenuItemTypeSession && item.Session != nil && item.Session.ID == sessionID {
			return item.Session, true
		}
	}
	return nil, false
}

func (s *Server) skillsServiceOrDefault() SkillsService {
	if s.skills != nil {
		return s.skills
	}
	return defaultSkillsService{}
}
