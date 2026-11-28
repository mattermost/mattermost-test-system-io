// Package kpi serves /api/v1/kpi* endpoints for pre-aggregated CI health metrics.
package kpi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

const kpiQueryTimeout = 10 * time.Second

// Handlers bundles HTTP handlers for /api/v1/kpi*.
type Handlers struct {
	Store  *kpi.Store
	Logger *slog.Logger
}

// Summary serves GET /api/v1/kpi/summary.
func (h *Handlers) Summary(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil || h.Store.Pool == nil {
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	params, err := kpi.ParseSummaryParams(q, h.Store.NowTime())
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	resp, err := h.Store.QuerySummary(ctx, params)
	if err != nil {
		handleKPIError(w, r, h.Logger, "kpi summary query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Flaky serves GET /api/v1/kpi/flaky.
func (h *Handlers) Flaky(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil || h.Store.Pool == nil {
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	params, err := kpi.ParseFlakyParams(q, h.Store.NowTime())
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	resp, err := h.Store.QueryFlaky(ctx, params)
	if err != nil {
		handleKPIError(w, r, h.Logger, "kpi flaky query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// PRsRed serves GET /api/v1/kpi/prs/red.
func (h *Handlers) PRsRed(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil || h.Store.Pool == nil {
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	params, err := kpi.ParsePRsRedParams(q, h.Store.NowTime())
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	resp, err := h.Store.QueryPRsRed(ctx, params)
	if err != nil {
		handleKPIError(w, r, h.Logger, "kpi prs red query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Failures serves GET /api/v1/kpi/failures.
func (h *Handlers) Failures(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil || h.Store.Pool == nil {
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	params, err := kpi.ParseFailuresParams(q, h.Store.NowTime())
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	resp, err := h.Store.QueryFailures(ctx, params)
	if err != nil {
		handleKPIError(w, r, h.Logger, "kpi failures query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// FailureDetail serves GET /api/v1/kpi/failures/{id}.
func (h *Handlers) FailureDetail(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil || h.Store.Pool == nil {
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	idStr := chi.URLParam(r, "id")
	sigID, err := uuid.Parse(idStr)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid signature id")
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	params, err := kpi.ParseFailureDetailParams(q, h.Store.NowTime())
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	resp, err := h.Store.QueryFailureDetail(ctx, sigID, params)
	if err != nil {
		handleKPIError(w, r, h.Logger, "kpi failure detail query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// TestState serves GET /api/v1/tests/{id}/state.
func (h *Handlers) TestState(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil || h.Store.Pool == nil {
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	idStr := chi.URLParam(r, "id")
	testID, err := uuid.Parse(idStr)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid test id")
		return
	}
	q, err := parseRawQuery(r)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	params, err := kpi.ParseTestStateParams(q, h.Store.NowTime())
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), kpiQueryTimeout)
	defer cancel()

	resp, err := h.Store.QueryTestState(ctx, testID, params)
	if err != nil {
		handleKPIError(w, r, h.Logger, "test state query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleKPIError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, msg string, err error) {
	if errors.Is(err, kpi.ErrNotFound) {
		api.WriteError(w, r, api.ErrNotFound)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, kpi.ErrOverloaded) {
		if logger != nil {
			logger.Warn(msg+" (overloaded/timeout)", slog.String("error", err.Error()))
		}
		w.Header().Set("Retry-After", "5")
		api.WriteErrorCode(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "kpi query temporarily unavailable; please retry")
		return
	}
	if logger != nil {
		logger.Error(msg, slog.String("error", err.Error()))
	}
	api.WriteError(w, r, err)
}

func parseRawQuery(r *http.Request) (url.Values, error) {
	if r.URL == nil || r.URL.RawQuery == "" {
		return url.Values{}, nil
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed query string", kpi.ErrValidation)
	}
	return q, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=30, stale-while-revalidate=60")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
