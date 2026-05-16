package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/telemetry"
)

// Deps bundles the dependencies every handler package may need.
type Deps struct {
	Logger             *slog.Logger
	CORSAllowedOrigins []string
}

// NewRouter builds the base chi router with shared middleware. Feature-specific
// handlers are mounted by the caller (cmd/tsio/main.go) before http.ListenAndServe.
func NewRouter(deps Deps) *chi.Mux {
	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(RequireHTTPS)
	r.Use(telemetry.Middleware(deps.Logger))

	if len(deps.CORSAllowedOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   deps.CORSAllowedOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization", "X-API-Key", "X-Admin-Key", "X-Report-Idempotency-Key"},
			AllowCredentials: true,
			MaxAge:           300,
		}))
	}

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Simple liveness; readiness uses /ready via the health package.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	return r
}
