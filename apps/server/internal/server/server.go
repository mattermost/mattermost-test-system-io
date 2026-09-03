// Package server assembles the HTTP handler tree. Both cmd/tsio/main.go and
// the tests/e2e harness call Build to produce an http.Handler from the wiring
// they've supplied, so the router, middleware stack, and route table live in
// exactly one place.
package server

import (
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	apiroot "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	artifactapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/artifacts"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/clientcfg"
	filesapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/files"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/health"
	apimw "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/middleware"
	orchapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	testhistoryapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/testhistory"
	triageapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/triage"
	wsapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/ws"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oauth"
	authoidc "github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oidc"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/session"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/webui"
)

// Deps carries the injected dependencies. All pointer-type fields are required
// unless explicitly marked optional.
type Deps struct {
	Logger   *slog.Logger
	Pool     *pgxpool.Pool
	Store    storage.ObjectStore
	APIKeys  *apikey.Repo
	Sessions *session.Manager
	Refresh  *session.RefreshManager
	Policy   *policy.Engine
	OIDC     *authoidc.Verifier // optional — disables bearer auth when nil
	OAuth    *oauth.Flow        // optional — disables /auth/github/* when nil

	Hub       *events.Hub
	Publisher *events.Publisher

	// Test shard orchestration domain wiring. The concrete implementations
	// live in internal/orchestration. May be nil during early-phase scaffolding
	// — the handler stubs return 501 regardless.
	OrchestrationStore     *orchestration.Store
	OrchestrationPublisher *orchestration.Publisher

	Version   string
	CommitSHA string
	BuildTime string
	AdminKey  string

	// Client-facing config surfaced via GET /api/v1/config.
	UploadTimeoutMs int
	HTMLViewEnabled bool
	SearchMinLength int
	Environment     string
	RepoURL         string

	// HTTP wiring
	CORSAllowedOrigins []string
	OpenAPISpecPath    string // path to openapi.yaml (absolute or relative to cwd)
	PostLoginRedirect  string

	// Upload
	MaxUploadBytes   int64
	MaxArtifactBytes int64
	PresignTTL       time.Duration
}

// Build constructs the chi router with the full feature set and returns it as
// an http.Handler. When OpenAPISpecPath is set, request validation is enabled
// for the /api/v1 subtree.
func Build(d Deps) chi.Router {
	r := apiroot.NewRouter(apiroot.Deps{
		Logger:             d.Logger,
		CORSAllowedOrigins: d.CORSAllowedOrigins,
	})

	healthHandler, readyHandler := health.Handlers(d.Pool, d.Version)
	r.Get("/health", healthHandler)
	r.Get("/ready", readyHandler)

	// Public file proxy. The web builds /files/{s3_key} URLs for screenshots
	// and other inline assets; this redirects to a presigned object-store URL.
	{
		h := &filesapi.Handlers{Store: d.Store, PresignTTL: d.PresignTTL}
		r.Get("/files/*", h.Get)
	}

	if d.OpenAPISpecPath != "" {
		uiH, rawSpecH := apiroot.SwaggerHandlers(d.OpenAPISpecPath)
		r.Get("/swagger-ui/*", uiH)
		r.Get("/api/v1/openapi.yaml", rawSpecH)
	}

	reportsH := &reports.Handlers{
		Pool:             d.Pool,
		Store:            d.Store,
		Publisher:        d.Publisher,
		Logger:           d.Logger,
		MaxUploadBytes:   d.MaxUploadBytes,
		MaxArtifactBytes: d.MaxArtifactBytes,
		PresignTTL:       d.PresignTTL,
		SearchMinLength:  d.SearchMinLength,
	}
	artifactsH := &artifactapi.Handlers{Pool: d.Pool, Store: d.Store, PresignTTL: d.PresignTTL}
	wsH := &wsapi.Handler{Hub: d.Hub}
	authH := &authapi.Handlers{
		Flow:              d.OAuth,
		Sessions:          d.Sessions,
		Refresher:         d.Refresh,
		Pool:              d.Pool,
		PostLoginRedirect: d.PostLoginRedirect,
		AdminKey:          d.AdminKey,
	}
	cfgH := &clientcfg.Handlers{
		UploadTimeoutMs:    d.UploadTimeoutMs,
		HTMLViewEnabled:    d.HTMLViewEnabled,
		SearchMinLength:    d.SearchMinLength,
		GitHubOAuthEnabled: d.OAuth != nil,
		ServerVersion:      d.Version,
		Environment:        d.Environment,
		RepoURL:            d.RepoURL,
		CommitSHA:          d.CommitSHA,
		BuildTime:          d.BuildTime,
	}

	var validator *apimw.OpenAPIValidator
	if d.OpenAPISpecPath != "" {
		v, err := apimw.NewOpenAPIValidator(d.OpenAPISpecPath)
		if err == nil {
			validator = v
		} else if d.Logger != nil {
			d.Logger.Warn("openapi validator disabled", slog.String("error", err.Error()))
		}
	}

	r.Route("/api/v1", func(r chi.Router) {
		if validator != nil {
			r.Use(validator.Middleware)
		}

		// --- Public: client config + build info ---
		r.Get("/config", cfgH.Config)
		r.Get("/info", cfgH.Info)

		// --- Public: auth endpoints (cookies carry identity) ---
		r.Get("/auth/github", authH.StartRedirect)
		r.Post("/auth/github/start", authH.Start)
		r.Get("/auth/github/callback", authH.Callback)
		r.Get("/auth/me", authH.Me)
		r.Post("/auth/refresh", authH.Refresh)
		r.Post("/auth/logout", authH.Logout)

		// Admin-key-gated bootstrap endpoint (X-Admin-Key header).
		r.Post("/auth/oidc-policies", authH.CreateOIDCPolicy)

		// --- Public: report reads ---
		r.Get("/reports", reportsH.List)
		r.Get("/reports/grouped", reportsH.Grouped)
		r.Get("/reports/individual", reportsH.Individual)
		r.Get("/reports/consolidated", reportsH.Consolidated)
		r.Get("/reports/{id}", reportsH.Detail)
		r.Get("/reports/{id}/suites", reportsH.Suites)
		r.Get("/reports/{id}/suites/{suiteID}/specs", reportsH.SuiteSpecs)
		r.Get("/reports/{id}/cases", reportsH.Cases)
		r.Get("/reports/{id}/json", reportsH.JSONFile)
		r.Get("/reports/{id}/search", reportsH.Search)

		// --- Public: per-test history and triage reads ---
		//
		// Unauthenticated on purpose. These answer questions about a test in
		// aggregate — counters, rates, prior verdicts — and both CI and the
		// triage agent consult them on every failing run. Handing a read-only
		// consumer a credential to fetch counters buys nothing.
		testsH := &testhistoryapi.Handlers{Pool: d.Pool, Logger: d.Logger}
		r.Get("/tests/history", testsH.History)
		r.Get("/tests/flakiness", testsH.Flakiness)
		r.Get("/tests/failing-elsewhere", testsH.FailingElsewhere)

		triageH := &triageapi.Handlers{Pool: d.Pool, Logger: d.Logger}
		// The short-circuit, and the only endpoint that says whether a check may
		// go green. Arithmetic over stored history — no model, and none can be
		// consulted, which is why a green from here is explicable later.
		r.Get("/triage/attribution", triageH.Attribution)
		// One payload with everything needed to adjudicate a run: failures
		// clustered by normalized error, plus history and screenshots.
		r.Get("/triage/evidence", triageH.Evidence)
		// Raw and effective pass-rates. Raw is computed from run outcomes, so no
		// waiver can move the number the team is judged by.
		r.Get("/triage/pass-rates", triageH.Rates)
		// The fix queue, ranked by blast radius, with each entry's fix-attempt
		// history attached.
		r.Get("/triage/queue", triageH.Queue)
		// "Has anyone already filed this?" — before the agent opens a duplicate.
		r.Get("/triage/signature-issues", triageH.SignatureIssues)
		// Verdict accuracy and the false-green count.
		r.Get("/triage/accuracy", triageH.Accuracy)
		// Product defects E2E surfaced. A test high on this list is not flaky —
		// it keeps catching real bugs, which is the opposite signal.
		r.Get("/triage/defects", triageH.Defects)

		// --- Public: WebSocket (anonymous; the dashboard never attaches creds) ---
		r.Get("/ws", wsH.Events)

		// --- Public: orchestration status snapshot ---
		// The dashboard fetches this without credentials, alongside the public
		// report-reads above. Mutations (begin/checkout/complete/screenshots)
		// stay in the protected group below.
		publicOrchH := &orchapi.Handlers{
			Pool:               d.Pool,
			Store:              d.OrchestrationStore,
			ObjectStore:        d.Store,
			Publisher:          d.OrchestrationPublisher,
			ReportsPublisher:   d.Publisher,
			Logger:             d.Logger,
			LeaseRetentionMs:   60_000,
			MaxScreenshotBytes: 10 * 1024 * 1024,
		}
		orchapi.RegisterPublic(r, publicOrchH)

		// --- Protected: writes + admin-ish reads ---
		r.Group(func(r chi.Router) {
			r.Use(authapi.RequireAuth(d.APIKeys, d.Sessions, d.OIDC, d.Policy))

			// Stateless upload lifecycle: each shard authenticates and registers
			// itself independently; no controller-side coordination is required.
			r.Post("/reports/begin", reportsH.Begin)
			r.Post("/reports/register", reportsH.Register)
			r.Post("/reports/upload/{rid}/{uid}/json", reportsH.UploadJSON)
			r.Post("/reports/upload/{rid}/{uid}/screenshots", reportsH.UploadScreenshots)

			// Legacy single-shot bundle upload (returns 410).
			r.Post("/reports", reportsH.Upload)
			r.Delete("/reports/{id}", reportsH.Delete)

			// Triage ledger writes. Authenticated because a forged verdict
			// would corrupt the false-green metric, and a forged fix attempt
			// would either re-enter a test into the loop or park it on a human
			// who never agreed to take it.
			r.Post("/triage/verdicts", triageH.CreateVerdicts)
			r.Post("/triage/verdicts/{id}/correction", triageH.Correct)
			r.Post("/triage/attempts", triageH.RecordFixAttempt)
			// Product bugs are filed in the issue tracker by the agent; this
			// records that it happened, so the next master run links the
			// existing ticket instead of opening a duplicate.
			r.Post("/triage/escalations", triageH.RecordEscalation)

			// Reading the ledger is authenticated too: unlike the aggregate
			// reads it returns rows, and each row names the credential that
			// wrote the verdict plus whatever a maintainer typed when
			// overruling it.
			r.Get("/triage/verdicts", triageH.ListVerdicts)

			r.Get("/artifacts/{id}", artifactsH.Get)

			// Test shard orchestration.
			orchH := &orchapi.Handlers{
				Pool:               d.Pool,
				Store:              d.OrchestrationStore,
				ObjectStore:        d.Store,
				Publisher:          d.OrchestrationPublisher,
				ReportsPublisher:   d.Publisher,
				Logger:             d.Logger,
				LeaseRetentionMs:   60_000,
				MaxScreenshotBytes: 10 * 1024 * 1024,
			}
			orchapi.Register(r, orchH)
		})
	})

	// Mount the embedded web UI last so the chi /api/v1 subtree keeps its
	// normal 404 behavior; only requests that miss every other route fall
	// through to the SPA handler.
	if webH, err := webui.Handler(); err != nil {
		if d.Logger != nil {
			d.Logger.Warn("embedded web ui disabled", slog.String("error", err.Error()))
		}
	} else {
		r.NotFound(webH.ServeHTTP)
	}

	return r
}
