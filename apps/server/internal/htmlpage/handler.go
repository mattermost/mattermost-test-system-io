package htmlpage

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
)

// Handler serves HTML report pages.
type Handler struct {
	Pool   *pgxpool.Pool
	Orch   *orchestration.Store
	Site   SiteInfo
	Logger *slog.Logger
	// Fallback serves run paths with no dispatch record (uploaded-only reports).
	Fallback http.Handler
}

// ServeHome serves GET / and /reports.
func (h *Handler) ServeHome(w http.ResponseWriter, r *http.Request) {
	if h.Pool == nil {
		http.Error(w, "html pages unavailable", http.StatusServiceUnavailable)
		return
	}
	limit := 50
	offset := 0
	if p := r.URL.Query().Get("page"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			http.Error(w, "invalid page", http.StatusBadRequest)
			return
		}
		offset = (n - 1) * limit
	}
	repository := strings.TrimSpace(r.URL.Query().Get("repository"))
	branchFilter := strings.TrimSpace(r.URL.Query().Get("branch_filter"))
	if repository != "" {
		branchOptions, err := reports.LoadBranchFilterOptions(r.Context(), h.Pool, repository)
		if err != nil {
			if h.Logger != nil {
				h.Logger.Warn("htmlpage branch filters failed", slog.String("error", err.Error()))
			}
		} else {
			resolved := reports.ResolveBranchFilter(branchFilter, branchOptions)
			if resolved != branchFilter {
				q := r.URL.Query()
				if resolved == "" {
					q.Del("branch_filter")
				} else {
					q.Set("branch_filter", resolved)
				}
				http.Redirect(w, r, r.URL.Path+"?"+q.Encode(), http.StatusSeeOther)
				return
			}
		}
	}
	page, err := BuildHomePage(r.Context(), h.Pool, h.Site, limit, offset, repository, branchFilter)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage home build failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	html, err := RenderHome(page)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage home render failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	writeHTML(w, html)
}

type homeLiveFragmentResponse struct {
	HTML  string `json:"html"`
	Count int    `json:"count"`
}

// ServeRepo serves GET /reports/{repo}.
func (h *Handler) ServeRepo(w http.ResponseWriter, r *http.Request) {
	if h.Pool == nil {
		http.Error(w, "html pages unavailable", http.StatusServiceUnavailable)
		return
	}
	repo := strings.TrimSpace(chi.URLParam(r, "repo"))
	if !IsRepoPageSegment(repo) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	limit := 50
	offset := 0
	if p := r.URL.Query().Get("page"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			http.Error(w, "invalid page", http.StatusBadRequest)
			return
		}
		offset = (n - 1) * limit
	}
	branchFilter := strings.TrimSpace(r.URL.Query().Get("branch_filter"))
	branchOptions, err := reports.LoadBranchFilterOptions(r.Context(), h.Pool, repo)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage branch filters failed", slog.String("error", err.Error()))
		}
	} else {
		resolved := reports.ResolveBranchFilter(branchFilter, branchOptions)
		if resolved != branchFilter {
			q := r.URL.Query()
			if resolved == "" {
				q.Del("branch_filter")
			} else {
				q.Set("branch_filter", resolved)
			}
			http.Redirect(w, r, r.URL.Path+"?"+q.Encode(), http.StatusSeeOther)
			return
		}
	}
	page, err := BuildRepoPage(r.Context(), h.Pool, h.Site, repo, limit, offset, branchFilter)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage repo build failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	html, err := RenderRepo(page)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage repo render failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	writeHTML(w, html)
}

// ServeHomeLiveFragment serves GET /reports/fragment/home-live (live rows JSON).
func (h *Handler) ServeHomeLiveFragment(w http.ResponseWriter, r *http.Request) {
	if h.Pool == nil {
		http.Error(w, "html pages unavailable", http.StatusServiceUnavailable)
		return
	}
	repository := strings.TrimSpace(r.URL.Query().Get("repository"))
	branchFilter := strings.TrimSpace(r.URL.Query().Get("branch_filter"))
	if repository != "" {
		if options, err := reports.LoadBranchFilterOptions(r.Context(), h.Pool, repository); err == nil {
			branchFilter = reports.ResolveBranchFilter(branchFilter, options)
		}
	}
	rows, err := BuildHomeLiveRows(r.Context(), h.Pool, repository, branchFilter)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage home live fragment failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to load live runs", http.StatusInternalServerError)
		return
	}
	html, err := RenderHomeLiveRows(rows)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage home live render failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render live runs", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(homeLiveFragmentResponse{
		HTML:  string(html),
		Count: len(rows),
	})
}

// ServeRun serves GET /reports/{repo}/{branch}/{sha}/{name}.
func (h *Handler) ServeRun(w http.ResponseWriter, r *http.Request) {
	if h.Pool == nil {
		http.Error(w, "html pages unavailable", http.StatusServiceUnavailable)
		return
	}
	repo := strings.TrimSpace(chi.URLParam(r, "repo"))
	branch := strings.TrimSpace(chi.URLParam(r, "branch"))
	sha := strings.TrimSpace(chi.URLParam(r, "sha"))
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if !IsRepoPageSegment(repo) || repo == "" || branch == "" || sha == "" || name == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	pageNum := 1
	if p := r.URL.Query().Get("page"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			http.Error(w, "invalid page", http.StatusBadRequest)
			return
		}
		pageNum = n
	}
	opts := RunPageOpts{
		GHRunID:      strings.TrimSpace(r.URL.Query().Get("gh_run_id")),
		GHRunAttempt: strings.TrimSpace(r.URL.Query().Get("gh_run_attempt")),
		Page:         pageNum,
	}
	page, redirectQ, err := BuildRunPage(r.Context(), h.Pool, h.Orch, h.Site, repo, branch, sha, name, opts)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage run build failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	if redirectQ != nil {
		http.Redirect(w, r, r.URL.Path+"?"+redirectQ.Encode(), http.StatusSeeOther)
		return
	}
	// No dispatch record: keep the existing report view for uploaded reports.
	if page.Mode == modeEmpty && h.Fallback != nil {
		h.Fallback.ServeHTTP(w, r)
		return
	}
	html, err := RenderRun(page)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage run render failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	writeHTML(w, html)
}

type runDispatchLiveFragmentResponse struct {
	HTML     string `json:"html"`
	Count    int    `json:"count"`
	Total    int    `json:"total"`
	Terminal bool   `json:"terminal"`
}

// ServeRunDispatchLiveFragment serves GET /reports/fragment/run-dispatch-live.
func (h *Handler) ServeRunDispatchLiveFragment(w http.ResponseWriter, r *http.Request) {
	if h.Orch == nil {
		http.Error(w, "html pages unavailable", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	repo := strings.TrimSpace(q.Get("repository"))
	commit := strings.TrimSpace(q.Get("commit_sha"))
	ghRunID := strings.TrimSpace(q.Get("gh_run_id"))
	name := strings.TrimSpace(q.Get("name"))
	attempt := strings.TrimSpace(q.Get("gh_run_attempt"))
	pageNum := 1
	if p := q.Get("page"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			http.Error(w, "invalid page", http.StatusBadRequest)
			return
		}
		pageNum = n
	}
	if repo == "" || commit == "" || ghRunID == "" || name == "" {
		http.Error(w, "missing identity", http.StatusBadRequest)
		return
	}
	body, total, live, err := BuildRunDispatchLive(r.Context(), h.Orch, repo, commit, ghRunID, name, attempt, pageNum)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage run live fragment failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to load units", http.StatusInternalServerError)
		return
	}
	html, err := RenderRunDispatchLive(body)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage run live render failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render units", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(runDispatchLiveFragmentResponse{
		HTML:     string(html),
		Count:    len(body.Units),
		Total:    total,
		Terminal: !live,
	})
}

// writeHTML writes a rendered html/template page.
func writeHTML(w http.ResponseWriter, html []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html) //nolint:gosec // G705: html/template output is already escaped
}
