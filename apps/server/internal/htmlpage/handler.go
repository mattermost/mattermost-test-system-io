package htmlpage

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

const (
	maxHTMLPage     = 1000
	kpiQueryTimeout = 10 * time.Second
)

// Handler serves HTML report pages.
type Handler struct {
	Pool   *pgxpool.Pool
	Orch   *orchestration.Store
	KPI    *kpi.Store
	Site   SiteInfo
	Logger *slog.Logger
	// Fallback serves run paths with no dispatch record (uploaded-only reports).
	Fallback http.Handler
	// StatusRule selects legacy vs shared read-time semantics ("" means legacy).
	StatusRule teststatus.Rule
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
		if err != nil || n < 1 || n > maxHTMLPage {
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
	page, err := BuildHomePageWithRule(r.Context(), h.Pool, h.Site, limit, offset, repository, branchFilter, h.StatusRule)
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
		if err != nil || n < 1 || n > maxHTMLPage {
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
	page, err := BuildRepoPageWithRule(r.Context(), h.Pool, h.Site, repo, limit, offset, branchFilter, h.StatusRule)
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
	rows, err := BuildHomeLiveRowsWithRule(r.Context(), h.Pool, repository, branchFilter, h.StatusRule)
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
		StatusRule:   h.StatusRule,
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
	body, total, live, err := BuildRunDispatchLiveWithRule(r.Context(), h.Orch, repo, commit, ghRunID, name, attempt, pageNum, h.StatusRule)
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

// ServeKPIOverview serves GET /kpi.
func (h *Handler) ServeKPIOverview(w http.ResponseWriter, r *http.Request) {
	if h.KPI == nil || h.KPI.Pool == nil {
		http.Error(w, "kpi pages unavailable", http.StatusServiceUnavailable)
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := h.KPI.NowTime()
	failuresParams, err := kpi.ParseFailuresParams(q, now)
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	summaryParams := failuresParams
	summaryParams.Sort = kpi.SortLaneAsc
	rawShow := strings.TrimSpace(q.Get("show"))
	showAll := rawShow == "all"

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	opts, err := h.KPI.LoadFilterOptions(ctx, summaryParams)
	if err != nil {
		handleKPIHTMLError(w, h.Logger, "htmlpage kpi filter options failed", "failed to load kpi filters", err)
		return
	}
	failures, err := h.KPI.QueryFailuresFiltered(ctx, failuresParams, !showAll)
	if err != nil {
		handleKPIHTMLError(w, h.Logger, "htmlpage kpi failures failed", "failed to load recurring failures", err)
		return
	}
	// If a narrow scope (e.g. a single lane) has 0 Active/Resurfaced rows but dormant
	// patterns exist, show All automatically unless the user explicitly chose ?show=active.
	if rawShow == "" && !showAll && failures.Pagination.Total == 0 && failures.Summary.TotalSignatures > 0 {
		showAll = true
		failures, err = h.KPI.QueryFailuresFiltered(ctx, failuresParams, false)
		if err != nil {
			handleKPIHTMLError(w, h.Logger, "htmlpage kpi failures failed", "failed to load recurring failures", err)
			return
		}
	}
	summary, err := h.KPI.QuerySummaryWithRecurring(ctx, summaryParams, &failures.Summary)
	if err != nil {
		handleKPIHTMLError(w, h.Logger, "htmlpage kpi summary failed", "failed to load kpi summary", err)
		return
	}

	page := BuildKPIOverviewPageWithShow(h.Site, opts, summary, failures, now, showAll)
	html, err := RenderKPIOverview(page)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage kpi overview render failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	writeKPIHTML(w, html)
}

// ServeKPIPRs serves GET /kpi/prs.
func (h *Handler) ServeKPIPRs(w http.ResponseWriter, r *http.Request) {
	if h.KPI == nil || h.KPI.Pool == nil {
		http.Error(w, "kpi pages unavailable", http.StatusServiceUnavailable)
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := h.KPI.NowTime()
	params, err := kpi.ParsePRsRedParams(q, now)
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	opts, err := h.KPI.LoadFilterOptions(ctx, params)
	if err != nil {
		handleKPIHTMLError(w, h.Logger, "htmlpage kpi prs filter options failed", "failed to load kpi filters", err)
		return
	}

	prResp, err := h.KPI.QueryPRsRed(ctx, params)
	if err != nil {
		handleKPIHTMLError(w, h.Logger, "htmlpage kpi prs failed", "failed to load pr e2e metrics", err)
		return
	}

	page := BuildKPIPRsPage(h.Site, opts, prResp, now)
	html, err := RenderKPIPRs(page)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage kpi prs render failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	writeKPIHTML(w, html)
}

// ServeKPIFailureDetail serves GET /kpi/failures/{id}.
func (h *Handler) ServeKPIFailureDetail(w http.ResponseWriter, r *http.Request) {
	if h.KPI == nil || h.KPI.Pool == nil {
		http.Error(w, "kpi pages unavailable", http.StatusServiceUnavailable)
		return
	}
	rawID := strings.TrimSpace(chi.URLParam(r, "id"))
	sigID, err := uuid.Parse(rawID)
	if err != nil {
		http.Error(w, "invalid failure signature id", http.StatusBadRequest)
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := h.KPI.NowTime()
	params, err := kpi.ParseFailureDetailParams(q, now)
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	detail, err := h.KPI.QueryFailureDetail(ctx, sigID, params)
	if err != nil {
		if errors.Is(err, kpi.ErrNotFound) {
			http.Error(w, "failure signature not found", http.StatusNotFound)
			return
		}
		handleKPIHTMLError(w, h.Logger, "htmlpage kpi failure detail failed", "failed to load failure signature", err)
		return
	}

	page := BuildKPIFailureDetailPage(h.Site, detail, now)
	html, err := RenderKPIFailureDetail(page)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("htmlpage kpi failure detail render failed", slog.String("error", err.Error()))
		}
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	writeKPIHTML(w, html)
}

func parseRawQuery(r *http.Request) (url.Values, error) {
	if r.URL == nil || r.URL.RawQuery == "" {
		return url.Values{}, nil
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errors.New("malformed query string")
	}
	return q, nil
}

func handleKPIHTMLError(w http.ResponseWriter, logger *slog.Logger, logMsg, userMsg string, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, kpi.ErrOverloaded) {
		if logger != nil {
			logger.Warn(logMsg+" (overloaded/timeout)", slog.String("error", err.Error()))
		}
		w.Header().Set("Retry-After", "5")
		http.Error(w, "kpi query temporarily unavailable; please retry", http.StatusServiceUnavailable)
		return
	}
	if logger != nil {
		logger.Warn(logMsg, slog.String("error", err.Error()))
	}
	http.Error(w, userMsg, http.StatusInternalServerError)
}

// writeHTML writes a rendered html/template page.
func writeHTML(w http.ResponseWriter, html []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html) //nolint:gosec // G705: html/template output is already escaped
}

func writeKPIHTML(w http.ResponseWriter, html []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=30, stale-while-revalidate=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html) //nolint:gosec // G705: html/template output is already escaped
}
