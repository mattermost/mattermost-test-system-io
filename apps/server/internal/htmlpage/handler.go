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
)

// HTML page handlers.
type Handler struct {
	Pool   *pgxpool.Pool
	Site   SiteInfo
	Logger *slog.Logger
}

// GET / and /reports.
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}

type homeLiveFragmentResponse struct {
	HTML  string `json:"html"`
	Count int    `json:"count"`
}

// GET /reports/{repo}.
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}

// GET /reports/fragment/home-live — live rows JSON.
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
