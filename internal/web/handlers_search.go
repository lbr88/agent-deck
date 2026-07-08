package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func (s *Server) handleGlobalSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 20)
	if limit > 50 {
		limit = 50
	}
	recentDays := parseNonNegativeInt(r.URL.Query().Get("days"), 30)
	tier := strings.TrimSpace(r.URL.Query().Get("tier"))
	if tier == "" {
		tier = "auto"
	}

	searchEnabled := true
	cfg := session.GlobalSearchSettings{
		Enabled:        &searchEnabled,
		Tier:           tier,
		MemoryLimitMB:  100,
		RecentDays:     recentDays,
		IndexRateLimit: 200,
	}
	index, err := session.NewGlobalSearchIndex(session.GetClaudeConfigDir(), cfg)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}
	if index == nil {
		writeAPIError(w, http.StatusServiceUnavailable, ErrCodeNotImplemented, "global search is disabled")
		return
	}
	defer index.Close()

	waitForGlobalSearchIndex(index, 3*time.Second)

	resp := GlobalSearchResponse{
		Query:      query,
		Tier:       session.TierName(index.GetTier()),
		EntryCount: index.EntryCount(),
		Loading:    index.IsLoading(),
	}
	if query == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	results := index.Search(query)
	if len(results) == 0 {
		results = index.FuzzySearch(query)
	}
	resp.Results = projectGlobalSearchResults(query, results, limit)
	resp.Count = len(resp.Results)
	writeJSON(w, http.StatusOK, resp)
}

func waitForGlobalSearchIndex(index *session.GlobalSearchIndex, maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if !index.IsLoading() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func projectGlobalSearchResults(query string, results []*session.SearchResult, limit int) []GlobalSearchResult {
	if limit <= 0 {
		limit = 20
	}
	out := make([]GlobalSearchResult, 0, min(len(results), limit))
	queryLower := strings.ToLower(query)
	for _, result := range results {
		if len(out) >= limit {
			break
		}
		if result == nil || result.Entry == nil {
			continue
		}
		content := result.Entry.ContentString()
		if content == "" {
			if result.Snippet != "" {
				content = result.Snippet
			} else {
				content = result.Entry.Summary
			}
		}
		matchCount := 0
		if queryLower != "" {
			matchCount = strings.Count(strings.ToLower(content), queryLower)
		}
		out = append(out, GlobalSearchResult{
			SessionID:  result.Entry.SessionID,
			Summary:    result.Entry.Summary,
			Snippet:    result.Snippet,
			Content:    content,
			CWD:        result.Entry.CWD,
			FilePath:   result.Entry.FilePath,
			ModTime:    result.Entry.ModTime,
			Score:      result.Score,
			MatchCount: matchCount,
		})
	}
	return out
}

func parsePositiveInt(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parseNonNegativeInt(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
