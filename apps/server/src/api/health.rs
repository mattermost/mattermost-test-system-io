//! Health check endpoints.

use actix_web::{HttpResponse, get, web};
use chrono::Utc;
use sea_orm::ConnectionTrait;
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

/// Configure health routes.
pub fn configure_health_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(health).service(ready);
}
