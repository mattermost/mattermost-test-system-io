//! Health check endpoints.

use actix_web::{get, web, HttpResponse};
use chrono::Utc;
use serde::Serialize;
use utoipa::ToSchema;

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
    match conn.query_row("SELECT 1", [], |_| Ok(())) {
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

/// Configure health routes.
pub fn configure_health_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(health).service(ready);
}
