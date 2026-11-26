// Package kpi serves /api/v1/kpi* endpoints for pre-aggregated CI health metrics.
package kpi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

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
	params, err := kpi.ParseSummaryParams(r.URL.Query(), h.Store.NowTime())
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}

	resp, err := h.Store.QuerySummary(r.Context(), params)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("kpi summary query failed", slog.String("error", err.Error()))
		}
		api.WriteError(w, r, err)
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
	params, err := kpi.ParseFlakyParams(r.URL.Query(), h.Store.NowTime())
	if err != nil {
		if errors.Is(err, kpi.ErrValidation) {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}

	resp, err := h.Store.QueryFlaky(r.Context(), params)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("kpi flaky query failed", slog.String("error", err.Error()))
		}
		api.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
