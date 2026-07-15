package orchestration

import (
	"log/slog"
	"net/http"
	"time"
)

// Observability for the orchestration HTTP surface.
//
// The project does not bundle a Prometheus client (or any metrics framework)
// — go.opentelemetry.io/otel/metric only appears as an indirect dependency,
// and there is no /metrics endpoint. Instead, this middleware emits one
// structured slog line per request tagged with metric=<name> and the chosen
// endpoint label so operators can scrape latency / request-rate histograms
// from the same log aggregation pipeline that ingests application logs.
//
// Pair with the handler-level structured event logs in
// internal/orchestration: those cover state transitions, this covers the
// HTTP envelope.

// timingMiddleware wraps a handler and emits an Info-level slog line on
// completion containing the request's duration, status, and a stable
// endpoint label. No-op when logger is nil.
func timingMiddleware(logger *slog.Logger, endpoint string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if logger == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			dur := time.Since(start)
			logger.LogAttrs(r.Context(), slog.LevelInfo, "metric",
				slog.String("metric", "orchestration_request_duration_seconds"),
				slog.String("endpoint", endpoint),
				slog.Int("status", rec.status),
				slog.Float64("value", dur.Seconds()),
				slog.Int64("duration_ms", dur.Milliseconds()),
			)
		})
	}
}

// statusRecorder mirrors telemetry.statusRecorder but kept local so the API
// package does not import telemetry just for the type.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController and
// websocket upgrades reach Hijacker / Flusher on the original writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
