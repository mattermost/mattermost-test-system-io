package orchestration

import "github.com/go-chi/chi/v5"

// RegisterPublic mounts the public read-only orchestration endpoint (status
// snapshot consumed by the dashboard). The browser dashboard fetches without
// credentials, mirroring the public report-read endpoints.
//
// Each route is wrapped in a per-endpoint timing middleware that emits a
// structured slog line tagged metric=orchestration_request_duration_seconds
// with a stable endpoint label, the HTTP status, and elapsed seconds. See
// metrics.go for the rationale (no Prometheus dep in this project).
//
// Routes attach with absolute paths rather than chi.Route so that callers can
// register public and protected endpoints under the same /orchestration prefix
// without chi's "Mount on an existing path" guard tripping.
func RegisterPublic(r chi.Router, h *Handlers) {
	r.With(timingMiddleware(h.Logger, "status")).Get("/orchestration/status", h.Status)
	r.With(timingMiddleware(h.Logger, "list")).Get("/orchestration/runs", h.ListRuns)
}

// Register mounts the write-side orchestration endpoints. The caller MUST
// have already applied auth middleware (e.g. via r.Group with
// authapi.RequireAuth) so every endpoint here inherits it — workers carry an
// API key or OIDC token to drive the begin/checkout/complete loop.
func Register(r chi.Router, h *Handlers) {
	r.With(timingMiddleware(h.Logger, "begin")).Post("/orchestration/begin", h.BeginRun)
	r.With(timingMiddleware(h.Logger, "checkout")).Post("/orchestration/checkout", h.Checkout)
	r.With(timingMiddleware(h.Logger, "complete")).Post("/orchestration/complete", h.Complete)
	r.With(timingMiddleware(h.Logger, "screenshots")).Post("/orchestration/screenshots", h.Screenshots)
}
