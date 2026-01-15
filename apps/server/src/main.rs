//! Rust Report Server - Main entry point.
//!
//! Starts the Actix-web server with configured routes and middleware.

mod api;
mod auth;
mod config;
mod db;
mod error;
mod middleware;
mod models;
mod services;

use std::path::PathBuf;
use std::sync::Arc;

use actix_cors::Cors;
use actix_files::{Files, NamedFile};
use actix_web::{http::header, web, App, HttpRequest, HttpServer, Result as ActixResult};
use tokio::sync::Semaphore;
use tracing::{error, info, warn, Level};
use tracing_subscriber::FmtSubscriber;
use utoipa::OpenApi;
use utoipa_swagger_ui::SwaggerUi;

use crate::auth::AdminKey;
use crate::config::Config;
use crate::db::DbPool;

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

/// OpenAPI documentation.
#[derive(OpenApi)]
#[openapi(
    info(
        title = "Rust Report Server",
        version = "0.1.0",
        description = "API server for uploading and viewing test reports (Playwright, Cypress, Detox)"
    ),
    servers(
        (url = "/", description = "Local server")
    ),
    paths(
        // Health endpoints
        api::health::health,
        api::health::ready,
        // Report endpoints
        api::reports::list_reports,
        api::reports::get_report,
        api::reports::get_report_html,
        api::reports::get_report_assets,
        api::reports::get_report_screenshots,
        api::reports::get_report_data_file,
        api::reports::get_report_suites,
        api::reports::get_suite_specs,
        // Detox endpoints
        api::detox::get_report_detox_jobs,
        api::detox::get_report_detox_tests,
        api::detox::get_detox_job,
        api::detox::get_detox_job_html,
        api::detox::get_detox_test_screenshots,
        // Upload endpoints
        services::upload::playwright::request_playwright_upload,
        services::upload::cypress::request_cypress_upload,
        services::upload::detox::request_detox_upload,
        services::upload::upload_report_files,
        // Auth endpoints
        services::auth_admin::create_api_key,
        services::auth_admin::list_api_keys,
        services::auth_admin::get_api_key,
        services::auth_admin::revoke_api_key,
        services::auth_admin::restore_api_key,
    ),
    components(
        schemas(
            // Common
            error::ErrorResponse,
            models::Pagination,
            models::PaginationParams,
            models::GitHubContext,
            // Health
            api::health::HealthResponse,
            api::health::ReadyResponse,
            // Reports
            models::ReportSummary,
            models::ReportDetail,
            models::ReportStats,
            models::ExtractionStatus,
            models::DetoxPlatform,
            api::reports::ReportListResponse,
            // Test suites/specs
            models::TestSuite,
            models::TestSuiteListResponse,
            models::TestSpecWithResults,
            models::TestSpecListResponse,
            models::ScreenshotInfo,
            models::TestResult,
            models::TestStatus,
            // Detox
            api::detox::DetoxJobSummaryResponse,
            api::detox::DetoxJobListResponse,
            api::detox::DetoxJobDetailResponse,
            api::detox::DetoxCombinedTestResult,
            api::detox::DetoxCombinedTestsResponse,
            api::detox::CombinedTestsQueryParams,
            api::detox::DetoxScreenshotResponse,
            api::detox::DetoxScreenshotsListResponse,
            // Upload
            services::upload::UploadRequest,
            services::upload::UploadRequestResponse,
            services::upload::UploadFilesResponse,
            services::upload::FileError,
            // Auth
            models::ApiKeyRole,
            models::ApiKeyCreateResponse,
            models::ApiKeyListItem,
            models::CreateApiKeyRequest,
            services::auth_admin::ListApiKeysResponse,
            services::auth_admin::RevokeApiKeyResponse,
            services::auth_admin::RestoreApiKeyResponse,
        )
    ),
    tags(
        (name = "Health", description = "Health check endpoints"),
        (name = "Reports", description = "Test report management"),
        (name = "Detox", description = "Detox-specific report endpoints"),
        (name = "Upload", description = "Report upload endpoints"),
        (name = "Auth", description = "API key management")
    ),
    modifiers(&SecurityAddon)
)]
struct ApiDoc;

/// Add API key security scheme.
struct SecurityAddon;

impl utoipa::Modify for SecurityAddon {
    fn modify(&self, openapi: &mut utoipa::openapi::OpenApi) {
        if let Some(components) = openapi.components.as_mut() {
            components.add_security_scheme(
                "api_key",
                utoipa::openapi::security::SecurityScheme::ApiKey(
                    utoipa::openapi::security::ApiKey::Header(
                        utoipa::openapi::security::ApiKeyValue::new("X-API-Key"),
                    ),
                ),
            );
        }
    }
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
            error!("  - In production, RRV_DATABASE_URL and RRV_API_KEY must be set");
            error!("  - In production, values must not match development defaults");
            std::process::exit(1);
        }
    };

    info!("========================================");
    info!("  Rust Report Server");
    info!("  Environment: {}", config.environment);
    info!("========================================");

    if config.is_development() {
        warn!("Running in DEVELOPMENT mode - do not use in production!");
        info!("Using development defaults for DATABASE_URL and API_KEY");
    }

    // Create data directories
    tokio::fs::create_dir_all(&config.data_dir)
        .await
        .expect("Failed to create data directory");
    tokio::fs::create_dir_all(&config.backup_dir)
        .await
        .expect("Failed to create backup directory");

    // Initialize database (synchronous)
    let pool = DbPool::new(&config).expect("Failed to initialize database");
    info!("Database connection established");

    // Run migrations (synchronous, no backup needed here)
    db::migrations::run_migrations(&pool).expect("Failed to run migrations");
    info!("Database migrations complete");

    // Check version and conditionally backup
    let version_info = {
        let conn = pool.connection();
        db::version::check_version(&conn).expect("Failed to check server version")
    };
    let (needs_backup, version_changed) = version_info;

    if needs_backup {
        info!("Minor version bump detected, creating backup...");
        match db::backup::create_backup(
            &config.database_url,
            &config.data_dir,
            &config.backup_dir,
            db::version::SERVER_VERSION,
        )
        .await
        {
            Ok(Some(path)) => info!("Backup created at: {}", path.display()),
            Ok(None) => info!("Backup skipped (no database file)"),
            Err(e) => error!("Failed to create backup: {}", e),
        }

        // Cleanup old backups (keep last 5)
        if let Err(e) = db::backup::cleanup_old_backups(&config.backup_dir, 5).await {
            warn!("Failed to cleanup old backups: {}", e);
        }
    }

    // Update stored version if changed
    if version_changed {
        let conn = pool.connection();
        db::version::update_stored_version(&conn, db::version::SERVER_VERSION)
            .expect("Failed to update stored version");
    }

    info!("Server version: {}", db::version::SERVER_VERSION);

    // Start the cleanup background task
    let cleanup_config = services::CleanupConfig {
        data_dir: config.data_dir.clone(),
        retention_hours: config.artifact_retention_hours,
        interval_secs: if config.is_development() { 60 } else { 3600 }, // 1 min dev, 1 hour prod
    };
    services::start_cleanup_task(Arc::new(pool.clone()), cleanup_config);
    info!(
        "Cleanup service started (artifact retention: {} hours)",
        config.artifact_retention_hours
    );

    // Prepare shared state
    let bind_address = config.bind_address();
    let admin_key = AdminKey::new(config.admin_key.clone());
    let data_dir = config.data_dir.clone();
    let max_upload_size = config.max_upload_size;
    let max_files_per_request = config.max_files_per_request;
    let max_concurrent_uploads = config.max_concurrent_uploads;
    let upload_queue_timeout_secs = config.upload_queue_timeout_secs;
    let static_dir = config.static_dir.clone();
    let is_development = config.is_development();

    // Create upload semaphore to limit concurrent upload batches
    // This limits concurrent disk I/O operations (files are streamed directly to disk)
    let upload_semaphore = Arc::new(Semaphore::new(max_concurrent_uploads));
    info!(
        "Upload limits: {}MB max size, {} files/batch, {} concurrent batches, {}s queue timeout",
        max_upload_size / 1024 / 1024,
        max_files_per_request,
        max_concurrent_uploads,
        upload_queue_timeout_secs
    );

    if static_dir.is_some() {
        info!("Static file serving enabled from {:?}", static_dir);
    }

    let worker_count = if is_development {
        info!(
            "Starting server at http://{} (4 workers - development mode)",
            bind_address
        );
        4
    } else {
        let cpus = num_cpus::get();
        info!(
            "Starting server at http://{} ({} workers)",
            bind_address, cpus
        );
        cpus
    };

    // Start HTTP server
    let server = HttpServer::new(move || {
        // Configure CORS
        let cors = if is_development {
            // Permissive CORS for development
            Cors::default()
                .allowed_origin("http://localhost:3000")
                .allowed_origin("http://127.0.0.1:3000")
                .allowed_methods(vec!["GET", "POST", "PUT", "DELETE", "OPTIONS"])
                .allowed_headers(vec![
                    header::AUTHORIZATION,
                    header::ACCEPT,
                    header::CONTENT_TYPE,
                    "X-API-Key".parse().unwrap(),
                ])
                .max_age(3600)
        } else {
            // Restrictive CORS for production (same-origin only)
            Cors::default()
                .allowed_methods(vec!["GET", "POST", "PUT", "DELETE", "OPTIONS"])
                .allowed_headers(vec![
                    header::AUTHORIZATION,
                    header::ACCEPT,
                    header::CONTENT_TYPE,
                    "X-API-Key".parse().unwrap(),
                ])
                .max_age(3600)
        };

        let mut app = App::new()
            // Add CORS middleware (must be before other middleware)
            .wrap(cors)
            // Add request logging middleware
            .wrap(middleware::RequestLogger)
            // Add shared state
            .app_data(web::Data::new(pool.clone()))
            .app_data(web::Data::new(data_dir.clone()))
            .app_data(web::Data::new(admin_key.clone()))
            .app_data(web::Data::new(max_upload_size))
            .app_data(web::Data::new(max_files_per_request))
            .app_data(web::Data::new(upload_semaphore.clone()))
            .app_data(web::Data::new(upload_queue_timeout_secs))
            // Allow 10x max_upload_size at HTTP layer - actual limit enforced in streaming code
            // This prevents ECONNRESET when clients send large uploads with many optional files
            .app_data(web::PayloadConfig::new(max_upload_size * 10))
            // Configure API routes
            .service(
                web::scope("/api/v1")
                    .configure(api::configure_health_routes)
                    .configure(api::configure_report_routes)
                    .configure(api::configure_detox_routes)
                    .configure(services::configure_upload_routes)
                    .configure(services::configure_auth_routes),
            )
            // Swagger UI
            .service(
                SwaggerUi::new("/swagger-ui/{_:.*}")
                    .url("/api-docs/openapi.json", ApiDoc::openapi()),
            );

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

    // Set worker count
    server
        .workers(worker_count)
        .bind(&bind_address)?
        .run()
        .await
}
