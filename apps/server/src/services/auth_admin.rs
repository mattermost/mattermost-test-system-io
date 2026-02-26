//! API Key management endpoints.

use actix_web::{HttpResponse, delete, get, post, web};
use serde::Serialize;
use utoipa::ToSchema;

use crate::auth::ApiKeyAuth;
use crate::db::DbPool;
use crate::error::{AppError, AppResult};
use crate::models::{
    ApiKeyCreateResponse, ApiKeyListItem, ApiKeyRole, CreateApiKeyRequest, OIDC_ADMIN_DENIED_MSG,
};
use crate::services::api_key;

/// Configure auth admin routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(create_api_key)
        .service(list_api_keys)
        .service(get_api_key)
        .service(revoke_api_key)
        .service(restore_api_key);
}

/// Create a new API key.
///
/// POST /api/v1/auth/keys
/// Authorization: X-API-Key (admin role) or X-Admin-Key (bootstrap)
#[utoipa::path(
    post,
    path = "/api/v1/auth/keys",
    tag = "Auth",
    request_body = CreateApiKeyRequest,
    responses(
        (status = 201, description = "API key created", body = ApiKeyCreateResponse),
        (status = 401, description = "Unauthorized - admin role required"),
        (status = 400, description = "Invalid input")
    ),
    security(
        ("api_key" = [])
    )
)]
#[post("/auth/keys")]
pub async fn create_api_key(
    auth: ApiKeyAuth,
    body: web::Json<CreateApiKeyRequest>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    // Check admin permission
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to create API keys".to_string(),
        ));
    }

    // Validate name
    if body.name.trim().is_empty() {
        return Err(AppError::InvalidInput("Name is required".to_string()));
    }

    // Parse role
    let role = body
        .role
        .as_ref()
        .and_then(|r| ApiKeyRole::parse(r))
        .unwrap_or_default();

    // Create the key
    let (full_key, api_key) =
        api_key::create_key(pool.get_ref(), &body.name, role, body.expires_in.as_deref()).await?;

    Ok(HttpResponse::Created().json(ApiKeyCreateResponse {
        id: api_key.id,
        key: full_key,
        name: api_key.name,
        role: api_key.role,
        expires_at: api_key.expires_at.map(|d| d.to_rfc3339()),
        created_at: api_key.created_at.to_rfc3339(),
    }))
}

/// List all API keys.
///
/// GET /api/v1/auth/keys
/// Authorization: X-API-Key (admin role) or X-Admin-Key
#[utoipa::path(
    get,
    path = "/api/v1/auth/keys",
    tag = "Auth",
    responses(
        (status = 200, description = "List of API keys", body = ListApiKeysResponse),
        (status = 401, description = "Unauthorized - admin role required")
    ),
    security(
        ("api_key" = [])
    )
)]
#[get("/auth/keys")]
pub async fn list_api_keys(auth: ApiKeyAuth, pool: web::Data<DbPool>) -> AppResult<HttpResponse> {
    // Check admin permission
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to list API keys".to_string(),
        ));
    }

    let keys = api_key::list_keys(pool.get_ref()).await?;
    let items: Vec<ApiKeyListItem> = keys.into_iter().map(ApiKeyListItem::from).collect();

    Ok(HttpResponse::Ok().json(ListApiKeysResponse { keys: items }))
}

/// Get a single API key by ID.
///
/// GET /api/v1/auth/keys/{id}
/// Authorization: X-API-Key (admin role) or X-Admin-Key
#[utoipa::path(
    get,
    path = "/api/v1/auth/keys/{id}",
    tag = "Auth",
    params(
        ("id" = String, Path, description = "API key UUID")
    ),
    responses(
        (status = 200, description = "API key details", body = ApiKeyListItem),
        (status = 401, description = "Unauthorized - admin role required"),
        (status = 404, description = "API key not found")
    ),
    security(
        ("api_key" = [])
    )
)]
#[get("/auth/keys/{id}")]
pub async fn get_api_key(
    auth: ApiKeyAuth,
    path: web::Path<String>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    // Check admin permission
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to view API key details".to_string(),
        ));
    }

    let id = path.into_inner();
    let key = api_key::get_key(pool.get_ref(), &id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("API key {} not found", id)))?;

    Ok(HttpResponse::Ok().json(ApiKeyListItem::from(key)))
}

/// Revoke an API key.
///
/// DELETE /api/v1/auth/keys/{id}
/// Authorization: X-API-Key (admin role) or X-Admin-Key
#[utoipa::path(
    delete,
    path = "/api/v1/auth/keys/{id}",
    tag = "Auth",
    params(
        ("id" = String, Path, description = "API key UUID")
    ),
    responses(
        (status = 200, description = "API key revoked", body = RevokeApiKeyResponse),
        (status = 401, description = "Unauthorized - admin role required"),
        (status = 404, description = "API key not found or already revoked")
    ),
    security(
        ("api_key" = [])
    )
)]
#[delete("/auth/keys/{id}")]
pub async fn revoke_api_key(
    auth: ApiKeyAuth,
    path: web::Path<String>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    // Check admin permission
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to revoke API keys".to_string(),
        ));
    }

    let id = path.into_inner();

    // Prevent self-revocation for non-admin bootstrap
    if id == auth.caller.key_id && auth.caller.key_id != "admin" {
        return Err(AppError::InvalidInput(
            "Cannot revoke your own API key".to_string(),
        ));
    }

    let revoked = api_key::revoke_key(pool.get_ref(), &id).await?;

    if revoked {
        Ok(HttpResponse::Ok().json(RevokeApiKeyResponse {
            message: "API key revoked".to_string(),
            id,
        }))
    } else {
        Err(AppError::NotFound(format!(
            "API key {} not found or already revoked",
            id
        )))
    }
}

/// Restore a revoked API key.
///
/// POST /api/v1/auth/keys/{id}/restore
/// Authorization: X-API-Key (admin role) or X-Admin-Key
#[utoipa::path(
    post,
    path = "/api/v1/auth/keys/{id}/restore",
    tag = "Auth",
    params(
        ("id" = String, Path, description = "API key UUID")
    ),
    responses(
        (status = 200, description = "API key restored", body = RestoreApiKeyResponse),
        (status = 401, description = "Unauthorized - admin role required"),
        (status = 404, description = "API key not found or not revoked")
    ),
    security(
        ("api_key" = [])
    )
)]
#[post("/auth/keys/{id}/restore")]
pub async fn restore_api_key(
    auth: ApiKeyAuth,
    path: web::Path<String>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    // Check admin permission
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to restore API keys".to_string(),
        ));
    }

    let id = path.into_inner();
    let restored = api_key::restore_key(pool.get_ref(), &id).await?;

    if restored {
        Ok(HttpResponse::Ok().json(RestoreApiKeyResponse {
            message: "API key restored".to_string(),
            id,
        }))
    } else {
        Err(AppError::NotFound(format!(
            "API key {} not found or not revoked",
            id
        )))
    }
}

// Response types

#[derive(Debug, Serialize, ToSchema)]
pub struct ListApiKeysResponse {
    keys: Vec<ApiKeyListItem>,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct RevokeApiKeyResponse {
    message: String,
    id: String,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct RestoreApiKeyResponse {
    message: String,
    id: String,
}
