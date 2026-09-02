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

		// --- Public: per-test history + triage ledger reads ---
		// Read-only and non-identifying, same posture as the report reads above.
		// Automated triage runs these on every failing E2E run, so keeping them
		// unauthenticated avoids handing a token to a read-only consumer.
		testsH := &testhistoryapi.Handlers{Pool: d.Pool, Logger: d.Logger}
		r.Get("/tests/history", testsH.History)
		r.Get("/tests/flakiness", testsH.Flakiness)
		r.Get("/tests/failing-elsewhere", testsH.FailingElsewhere)

		// amnesty and accuracy stay public: both answer questions about a test or
		// a repo in aggregate, and triage calls them on every failing run. The
		// verdict list is the exception and lives in the protected group below —
		// it is the audit trail, so each row carries the principal that wrote it
		// and the free-text reason a human gave for overruling it.
		triageH := &triageapi.Handlers{Pool: d.Pool, Logger: d.Logger}
		r.Get("/triage/amnesty", triageH.Amnesty)
		r.Get("/triage/accuracy", triageH.Accuracy)
		r.Get("/triage/evidence", triageH.Evidence)
		// W1 — labeled raw/effective pass rates over a window. Alerting reads
		// raw; check status reads effective. No unlabelled pass rate anywhere.
		r.Get("/triage/pass-rates", triageH.Rates)
		// W3 — blind waiver audit: the sample payload omits the AI verdict
		// (blindness enforced server-side); the reveal happens only after the
		// reviewer's submit. Sample + agreement are public reads like the other
		// aggregate reads; submits and per-item reveals are authenticated.
		r.Get("/triage/audit/agreement", triageH.AuditAgreement)
		// W13 — the single phase value every gating decision reads. Reads are
		// public (CI jobs + web); writes and the auto-demotion apply are
		// authenticated — the metrics demote, humans promote.
		r.Get("/triage/phase", triageH.Phase)
		r.Get("/triage/phase/evaluation", triageH.PhaseEvaluation)
		// W5/W14/W15c — release-cut guard, stabilization queue, SLA report.
		// W7 — master alerting: dry evaluation + replay are public tools; the
		// firing apply is the scheduled job's authenticated call.
		r.Get("/triage/alerts/evaluation", triageH.AlertEvaluation)
		r.Get("/triage/alerts/replay", triageH.AlertReplay)
		// R7-L3 — quarantine READS are public for the same reason the
		// stabilization queue is: the PR triage action and the test runner in
		// the tested repo's CI both consult it, and neither should need a
		// credential round-trip to find out whether a test is quarantined.
		// Every WRITE is authenticated below.
		r.Get("/triage/quarantine", triageH.ListQuarantine)
		// R7-L1 — "is the loop keeping up?": arrival vs drain, with the one
		// input worth changing named. Public so a dashboard can show it.
		r.Get("/triage/stabilization/throughput", triageH.StabilizationThroughput)

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

			// Triage ledger writes. Authenticated because a forged waiver record
			// would corrupt both the amnesty counter and the false-green metric —
			// the two things that keep automated greens accountable.
			r.Post("/triage/verdicts", triageH.CreateVerdicts)
			r.Post("/triage/verdicts/{id}/correction", triageH.Correct)

			// Reading the ledger is authenticated too. Unlike the aggregate reads
			// it returns rows, and each row names the credential that recorded the
			// verdict plus whatever free text a maintainer wrote when overruling
			// it — an audit trail of who did what, which is not the same kind of
			// data as "how flaky is this test".
			r.Get("/triage/verdicts", triageH.ListVerdicts)
			// B7: these four return ROWS — root_cause free text, suspect
			// commits (named engineers), OIDC subjects in promoted_by, and
			// the audit sample itself (which must stay unretrievable without
			// a credential or an auditor could un-blind themselves by commit
			// SHA via release-guard). Same standard as ListVerdicts.
			r.Get("/triage/audit/sample", triageH.AuditSample)
			r.Get("/triage/release-guard", triageH.ReleaseGuard)
			r.Get("/triage/stabilization/queue", triageH.StabilizationQueue)
			r.Get("/triage/sla", triageH.SLAReport)
			// W3 — blind audit submits + the post-submit reveal are authenticated:
			// a forged agreement row would corrupt W13's promotion gate.
			r.Post("/triage/audit/reviews", triageH.SubmitAuditReview)
			r.Get("/triage/audit/items/{id}", triageH.AuditItemDetail)
			// W13 — human phase changes + the scheduled job's demotion apply.
			r.Post("/triage/phase", triageH.SetPhase)
			r.Post("/triage/phase/evaluate", triageH.ApplyPhaseEvaluation)
			// Stabilization queue writes — allocating fixing effort is authenticated.
			r.Post("/triage/stabilization/promote", triageH.PromoteStabilization)
			r.Post("/triage/stabilization/resolve", triageH.ResolveStabilization)
			// R7-L3 — quarantine writes. Hiding a test from PR gating is a
			// human decision with an owner and a deadline, so the identity
			// comes from the authenticated subject and never from the body.
			r.Post("/triage/quarantine", triageH.Quarantine)
			r.Post("/triage/quarantine/{id}/release", triageH.ReleaseQuarantine)
			// W7 — the alerting job's apply: posts, opens/updates issues, records.
			r.Post("/triage/alerts/evaluate", triageH.AlertEvaluate)

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
