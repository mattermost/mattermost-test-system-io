//! Test System IO Server - Main entry point.
//!
//! Starts the Actix-web server with configured routes and middleware.

mod api;
mod auth;
mod config;
mod db;
mod entity;
mod error;
mod middleware;
mod migration;
mod models;
mod services;

use std::path::PathBuf;

use actix_cors::Cors;
use actix_files::{Files, NamedFile};
use actix_web::{App, HttpRequest, HttpServer, Result as ActixResult, http::header, web};
use tracing::{Level, error, info, warn};
use tracing_subscriber::FmtSubscriber;
use utoipa::OpenApi;
use utoipa_swagger_ui::SwaggerUi;

use crate::api::ApiDoc;
use crate::auth::AdminKey;
use crate::config::Config;
use crate::db::DbPool;
use crate::services::{EventBroadcaster, GitHubOidcVerifier, Storage};

/// SPA fallback handler - serves index.html for client-side routing.
async fn spa_fallback(req: HttpRequest) -> ActixResult<NamedFile> {
    let static_dir: &PathBuf = req
        .app_data::<web::Data<PathBuf>>()
        .expect("Static dir not configured")
        .get_ref();
    Ok(NamedFile::open(static_dir.join("index.html"))?)
}

/// Perform health check (for Docker healthcheck).
async fn health_check() -> bool {
    // Simple check - just verify we can load config
    Config::from_env().is_ok()
}

#[actix_web::main]
async fn main() -> std::io::Result<()> {
    // Check for --health-check flag (used by Docker HEALTHCHECK)
    let args: Vec<String> = std::env::args().collect();
    if args.iter().any(|arg| arg == "--health-check") {
        // Perform simple health check
        dotenvy::dotenv().ok();
        if health_check().await {
            std::process::exit(0);
        } else {
            std::process::exit(1);
        }
    }

    // Load environment variables from .env file
    dotenvy::dotenv().ok();

    // Initialize logging
    let subscriber = FmtSubscriber::builder()
        .with_max_level(Level::INFO)
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .finish();
    tracing::subscriber::set_global_default(subscriber).expect("Failed to set tracing subscriber");

    // Load configuration
    let config = match Config::from_env() {
        Ok(cfg) => cfg,
        Err(e) => {
            error!("Failed to load configuration: {}", e);
            error!("");
            error!("Please check your environment variables:");
            error!("  - RUST_ENV must be set to 'development' or 'production'");
            error!("  - In production, TSIO_DB_URL or TSIO_DB_PASSWORD must be set");
            error!("  - In production, values must not match development defaults");
            std::process::exit(1);
        }
    };

    info!("========================================");
    info!("  Test System IO Server");
    info!("  Environment: {}", config.environment);
    info!("========================================");

    if config.is_development() {
        warn!("Running in DEVELOPMENT mode - do not use in production!");
        info!("Using development defaults for TSIO_DB_URL and TSIO_AUTH_ADMIN_KEY");
    }

    // Initialize database (async)
    let pool = DbPool::new(&config)
        .await
        .expect("Failed to initialize database");
    info!("Database connection established");

    // Run migrations
    pool.run_migrations()
        .await
        .expect("Failed to run migrations");
    info!("Database migrations complete");

    // Cleanup expired/revoked refresh tokens
    match crate::db::refresh_tokens::cleanup_expired(pool.connection(), 86400).await {
        Ok(count) if count > 0 => info!("Cleaned up {} expired/revoked refresh tokens", count),
        Ok(_) => {}
        Err(e) => warn!("Failed to cleanup refresh tokens: {}", e),
    }

    // Initialize S3 storage
    let storage = Storage::new(&config.storage)
        .await
        .expect("Failed to initialize S3 storage");
    info!("S3 storage initialized: bucket={}", config.storage.bucket);

    // Initialize event broadcaster for WebSocket real-time updates
    let event_broadcaster = EventBroadcaster::new();
    info!("Event broadcaster initialized for WebSocket connections");

    // Initialize GitHub OIDC verifier (if enabled)
    let oidc_verifier = if config.github_oidc.enabled {
        let verifier = GitHubOidcVerifier::new(&config.github_oidc);
        info!("GitHub OIDC authentication enabled");
        Some(verifier)
    } else {
        info!("GitHub OIDC authentication disabled");
        None
    };

    if config.github_oauth.enabled {
        info!("GitHub OAuth login enabled");
    } else {
        info!("GitHub OAuth login disabled");
    }

    // Prepare shared state
    let bind_address = config.server.bind_address();
    let admin_key = AdminKey::new(config.auth.admin_key.clone());
    let max_upload_size = config.features.upload_max_size;
    let static_dir = config.server.static_dir.clone();
    let is_development = config.is_development();
    let allowed_origins = config.server.allowed_origins.clone();
    let oidc_verifier_clone = oidc_verifier.clone();
    let client_config = config; // Move config for sharing with the app

    info!("JSON payload limit: {}MB", max_upload_size / 1024 / 1024);

    if static_dir.is_some() {
        info!("Static file serving enabled from {:?}", static_dir);
    }

    // Extract server configuration values before moving config into closure
    let server_workers = client_config.server.workers;
    let server_backlog = client_config.server.backlog;
    let server_max_connections = client_config.server.max_connections;
    let server_max_connection_rate = client_config.server.max_connection_rate;

    let worker_count = if server_workers == 0 {
        num_cpus::get()
    } else {
        server_workers
    };

    info!(
        "Starting server at http://{} ({} workers, backlog={}, max_conn={}/worker)",
        bind_address, worker_count, server_backlog, server_max_connections
    );

    // Start HTTP server
    let server = HttpServer::new(move || {
        // Configure CORS
        //
        // Development: always allow the local Vite dev server.
        // Production: only allow origins listed in TSIO_SERVER_ALLOWED_ORIGINS.
        //   An empty list means no cross-origin requests are permitted (same-origin only).
        //   actix-cors does NOT fall back to reflecting the Origin header when
        //   allowed_origin_fn / allowed_origin are used — unlisted origins receive
        //   no ACAO header, which browsers treat as a CORS denial.
        let allowed_methods = vec!["GET", "POST", "PUT", "DELETE", "OPTIONS"];
        let allowed_headers = vec![
            header::AUTHORIZATION,
            header::ACCEPT,
            header::CONTENT_TYPE,
            "X-API-Key".parse().unwrap(),
        ];

        let cors = if is_development {
            // Development: allow local Vite dev server origins
            Cors::default()
                .allowed_origin("http://localhost:3000")
                .allowed_origin("http://127.0.0.1:3000")
                .allowed_methods(allowed_methods)
                .allowed_headers(allowed_headers)
                .supports_credentials()
                .max_age(3600)
        } else {
            // Production: only allow explicitly configured origins
            let mut cors_builder = Cors::default()
                .allowed_methods(allowed_methods)
                .allowed_headers(allowed_headers)
                .supports_credentials()
                .max_age(3600);
            for origin in &allowed_origins {
                cors_builder = cors_builder.allowed_origin(origin);
            }
            cors_builder
        };

        let app = App::new()
            // Add CORS middleware (must be before other middleware)
            .wrap(cors)
            // Add request logging middleware
            .wrap(middleware::RequestLogger)
            // Add shared state
            .app_data(web::Data::new(pool.clone()))
            .app_data(web::Data::new(storage.clone()))
            .app_data(web::Data::new(admin_key.clone()))
            .app_data(web::Data::new(max_upload_size))
            .app_data(web::Data::new(client_config.clone()))
            .app_data(web::Data::new(event_broadcaster.clone()));

        // Add OIDC verifier as app data if enabled
        let app = if let Some(ref verifier) = oidc_verifier_clone {
            app.app_data(web::Data::new(verifier.clone()))
        } else {
            app
        };

        let mut app = app
            // JSON payload limit for upload_json endpoint
            .app_data(web::PayloadConfig::new(max_upload_size))
            // Configure API routes
            .service(
                web::scope("/api/v1")
                    .configure(api::configure_health_routes)
                    .configure(api::configure_report_routes)
                    .configure(api::configure_job_routes)
                    .configure(api::configure_test_results_routes)
                    .configure(api::configure_websocket_routes)
                    .configure(services::configure_auth_routes)
                    .configure(services::configure_oauth_routes)
                    .configure(services::configure_oidc_policy_routes),
            )
            // File serving from S3 (proxy)
            .configure(api::configure_file_routes);

        // Swagger UI is only available in development to avoid leaking the API schema
        if is_development {
            app = app.service(
                SwaggerUi::new("/swagger-ui/{_:.*}")
                    .url("/api-docs/openapi.json", ApiDoc::openapi()),
            );
        }

        // Serve static files in production (when STATIC_DIR is set)
        if let Some(ref dir) = static_dir {
            app = app
                .app_data(web::Data::new(dir.clone()))
                // Serve static assets (js, css, images)
                .service(Files::new("/assets", dir.join("assets")).prefer_utf8(true))
                // Serve favicon
                .service(Files::new("/favicon", dir.clone()).index_file("favicon.ico"))
                // SPA fallback - serve index.html for all other routes
                .default_service(web::route().to(spa_fallback));
        }

        app
    });

    // Configure server with resource limits to prevent "too many open files" errors
    server
        .workers(worker_count)
        .backlog(server_backlog)
        .max_connections(server_max_connections)
        .max_connection_rate(server_max_connection_rate)
        .bind(&bind_address)?
        .run()
        .await
}
