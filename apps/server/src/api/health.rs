//! Health check and config endpoints.

use actix_web::{HttpResponse, get, web};
use chrono::Utc;
use sea_orm::ConnectionTrait;
use serde::Serialize;
use utoipa::ToSchema;

use crate::config::Config;
use crate::db::DbPool;

/// Health check response.
#[derive(Serialize, ToSchema)]
pub struct HealthResponse {
    status: &'static str,
    timestamp: String,
}

/// Readiness check response.
#[derive(Serialize, ToSchema)]
pub struct ReadyResponse {
    status: &'static str,
    database: &'static str,
}

/// Client configuration response.
///
/// Field naming follows `{group}_{name}` convention (see `FeatureSettings`).
#[derive(Serialize, ToSchema)]
pub struct ClientConfigResponse {
    /// Upload timeout in milliseconds (how long before a report is considered timed out).
    upload_timeout_ms: u64,
    /// Whether HTML view tabs are enabled in the frontend.
    html_view_enabled: bool,
    /// Minimum characters required for search API.
    search_min_length: usize,
    /// Whether GitHub OAuth login is enabled.
    github_oauth_enabled: bool,
}

/// Health check endpoint.
///
/// Returns 200 if the service is running.
#[utoipa::path(
    get,
    path = "/api/v1/health",
    tag = "Health",
    responses(
        (status = 200, description = "Service is healthy", body = HealthResponse)
    )
)]
#[get("/health")]
pub async fn health() -> HttpResponse {
    HttpResponse::Ok().json(HealthResponse {
        status: "healthy",
        timestamp: Utc::now().to_rfc3339(),
    })
}

/// Readiness check endpoint.
///
/// Returns 200 if the service is ready to accept requests (database connected).
#[utoipa::path(
    get,
    path = "/api/v1/ready",
    tag = "Health",
    responses(
        (status = 200, description = "Service is ready", body = ReadyResponse),
        (status = 503, description = "Service unavailable")
    )
)]
#[get("/ready")]
pub async fn ready(pool: web::Data<DbPool>) -> HttpResponse {
    // Try a simple query to verify database connectivity
    let conn = pool.connection();
    let stmt =
        sea_orm::Statement::from_string(sea_orm::DatabaseBackend::Postgres, "SELECT 1".to_owned());
    match conn.query_one_raw(stmt).await {
        Ok(_) => HttpResponse::Ok().json(ReadyResponse {
            status: "ready",
            database: "connected",
        }),
        Err(_) => HttpResponse::ServiceUnavailable().json(serde_json::json!({
            "error": "NOT_READY",
            "message": "Database connection failed"
        })),
    }
}

/// Client configuration endpoint.
///
/// Returns configuration values needed by the frontend.
#[utoipa::path(
    get,
    path = "/api/v1/config",
    tag = "Health",
    responses(
        (status = 200, description = "Client configuration", body = ClientConfigResponse)
    )
)]
#[get("/config")]
pub async fn client_config(config: web::Data<Config>) -> HttpResponse {
    HttpResponse::Ok().json(ClientConfigResponse {
        upload_timeout_ms: config.features.upload_timeout_ms,
        html_view_enabled: config.features.html_view_enabled,
        search_min_length: config.features.search_min_length,
        github_oauth_enabled: config.github_oauth.enabled,
    })
}

/// Configure health routes.
pub fn configure_health_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(health).service(ready).service(client_config);
}
