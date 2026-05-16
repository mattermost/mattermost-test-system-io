// Package health exposes /health (liveness) and /ready (readiness) probes.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handlers returns /health and /ready http handlers.
//
// /health always returns 200. /ready pings Postgres with a 500 ms budget;
// returns 503 on failure. The Postgres error is logged but not returned in
// the response so the public endpoint cannot leak internal connection details.
func Handlers(pool *pgxpool.Pool, version string, logger *slog.Logger) (health, ready http.HandlerFunc) {
	health = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": version,
		})
	}
	ready = func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			if logger != nil {
				logger.Warn("readiness probe failed", slog.String("reason", "postgres"), slog.String("error", err.Error()))
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not-ready",
				"reason": "postgres",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
	return
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
