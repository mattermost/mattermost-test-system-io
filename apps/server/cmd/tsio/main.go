// Command tsio is the HTTP server for Test System IO.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oauth"
	authoidc "github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oidc"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/session"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/server"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/telemetry"
)

// Build-time variables, set via -ldflags.
var (
	version   = "dev"
	commitSHA = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := telemetry.NewLogger(cfg.LogFormat, cfg.LogLevel).With(
		slog.String("service", "tsio"),
		slog.String("version", version),
	)
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if cfg.DBAutoMigrate {
		logger.Info("applying embedded migrations")
		if err := db.Migrate(cfg.DatabaseURL); err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Idempotent OIDC policy bootstrap. Used by ephemeral staging deploys to
	// re-seed the org-wide CI grant after the database is recreated. Empty
	// TSIO_BOOTSTRAP_OIDC_POLICIES is a no-op (production path).
	if err := authapi.BootstrapPolicies(ctx, pool, cfg.BootstrapOIDCPolicies, logger); err != nil {
		return fmt.Errorf("bootstrap oidc policies: %w", err)
	}

	store, err := storage.New(ctx, storage.Config{
		Endpoint:       cfg.S3Endpoint,
		Region:         cfg.S3Region,
		Bucket:         cfg.S3Bucket,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		ForcePathStyle: cfg.S3ForcePathStyle,
	})
	if err != nil {
		return fmt.Errorf("init storage: %w", err)
	}

	var oidcVerifier *authoidc.Verifier
	if ov, err := authoidc.New(ctx, cfg.GitHubActionsOIDCIssuer, cfg.GitHubActionsOIDCAudience); err != nil {
		logger.Warn("oidc verifier unavailable", slog.String("error", err.Error()))
	} else {
		oidcVerifier = ov
	}

	var oauthFlow *oauth.Flow
	if cfg.GitHubOAuthClientID != "" {
		oauthFlow = oauth.NewFlow(oauth.Config{
			ClientID:     cfg.GitHubOAuthClientID,
			ClientSecret: cfg.GitHubOAuthClientSecret,
			RedirectURL:  cfg.GitHubOAuthRedirectURL,
		})
	}

	hub := events.NewHub()
	publisher := &events.Publisher{Hub: hub}

	orchestrationStore := &orchestration.Store{Pool: pool, Logger: logger}
	orchestrationPublisher := &orchestration.Publisher{Hub: hub, Logger: logger}
	reaper := &orchestration.Reaper{
		Store:     orchestrationStore,
		Publisher: orchestrationPublisher,
		Logger:    logger,
	}
	if err := reaper.Start(ctx); err != nil {
		// Log and continue: the reaper is a backstop for lease expiration;
		// every checkout also lazily expires overdue leases inline.
		logger.Error("orchestration reaper start", slog.String("error", err.Error()))
	}
	defer reaper.Stop()

	reportsReaper := &reports.Reaper{
		Pool:      pool,
		Publisher: publisher,
		Logger:    logger,
	}
	if err := reportsReaper.Start(ctx); err != nil {
		logger.Error("reports reaper start", slog.String("error", err.Error()))
	}
	defer reportsReaper.Stop()

	handler := server.Build(server.Deps{
		Logger:                 logger,
		Pool:                   pool,
		Store:                  store,
		APIKeys:                &apikey.Repo{Pool: pool},
		Sessions:               &session.Manager{Pool: pool, TTL: cfg.SessionTTL},
		Refresh:                &session.RefreshManager{Pool: pool, TTL: cfg.RefreshTokenTTL},
		Policy:                 &policy.Engine{Pool: pool},
		OIDC:                   oidcVerifier,
		OAuth:                  oauthFlow,
		Hub:                    hub,
		Publisher:              publisher,
		OrchestrationStore:     orchestrationStore,
		OrchestrationPublisher: orchestrationPublisher,
		Version:                version,
		CommitSHA:              commitSHA,
		BuildTime:              buildTime,
		AdminKey:               cfg.AdminKey,
		UploadTimeoutMs:        cfg.UploadTimeoutMs,
		HTMLViewEnabled:        cfg.HTMLViewEnabled,
		SearchMinLength:        cfg.SearchMinLength,
		Environment:            cfg.Environment,
		RepoURL:                cfg.RepoURL,
		CORSAllowedOrigins:     cfg.CORSAllowedOrigins,
		OpenAPISpecPath:        cfg.OpenAPISpecPath,
		PostLoginRedirect:      "/",
		MaxUploadBytes:         cfg.MaxUploadBytes,
		MaxArtifactBytes:       cfg.MaxArtifactBytes,
		PresignTTL:             5 * time.Minute,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	// Bounded channel so the goroutine can deliver the listen error and exit
	// even if run() has already returned via the ctx.Done() path.
	serveErr := make(chan error, 1)
	go func() {
		tlsEnabled := cfg.TLSCertFile != "" && cfg.TLSKeyFile != ""
		logger.Info("listening",
			slog.String("addr", cfg.HTTPListenAddr),
			slog.Bool("tls", tlsEnabled),
			slog.String("commit", commitSHA),
			slog.String("build_time", buildTime),
		)
		var err error
		if tlsEnabled {
			err = srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		// Startup failure (e.g. "address already in use") or runtime crash.
		// Surface as the run() error rather than masking it with a Shutdown
		// status that never had a chance to run.
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}
