//! OIDC policy management endpoints (admin CRUD).

use actix_web::{HttpResponse, delete, get, post, put, web};
use chrono::Utc;
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::DbPool;
use crate::error::{AppError, AppResult};
use crate::models::github_oidc::{
    CreateOidcPolicyRequest, OidcPolicy, OidcPolicyListResponse, OidcPolicyResponse,
    UpdateOidcPolicyRequest,
};
use crate::models::{ApiKeyRole, OIDC_ADMIN_DENIED_MSG};

/// Configure OIDC policy routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(list_policies)
        .service(create_policy)
        .service(get_policy)
        .service(update_policy)
        .service(delete_policy);
}

/// List all OIDC policies.
///
/// GET /api/v1/auth/oidc-policies
#[utoipa::path(
    get,
    path = "/api/v1/auth/oidc-policies",
    tag = "Auth",
    responses(
        (status = 200, description = "List of OIDC policies", body = OidcPolicyListResponse),
        (status = 401, description = "Unauthorized - admin role required")
    ),
    security(("api_key" = []))
)]
#[get("/auth/oidc-policies")]
pub async fn list_policies(auth: ApiKeyAuth, pool: web::Data<DbPool>) -> AppResult<HttpResponse> {
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to list OIDC policies".to_string(),
        ));
    }

    let policies = crate::db::github_oidc_policies::list_active(pool.connection()).await?;
    let items: Vec<OidcPolicyResponse> =
        policies.into_iter().map(OidcPolicyResponse::from).collect();

    Ok(HttpResponse::Ok().json(OidcPolicyListResponse { policies: items }))
}

/// Create a new OIDC policy.
///
/// POST /api/v1/auth/oidc-policies
#[utoipa::path(
    post,
    path = "/api/v1/auth/oidc-policies",
    tag = "Auth",
    request_body = CreateOidcPolicyRequest,
    responses(
        (status = 201, description = "OIDC policy created", body = OidcPolicyResponse),
        (status = 401, description = "Unauthorized - admin role required"),
        (status = 400, description = "Invalid input")
    ),
    security(("api_key" = []))
)]
#[post("/auth/oidc-policies")]
pub async fn create_policy(
    auth: ApiKeyAuth,
    body: web::Json<CreateOidcPolicyRequest>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to create OIDC policies".to_string(),
        ));
    }

    // Validate pattern
    let pattern = body.repository_pattern.trim();
    if pattern.is_empty() {
        return Err(AppError::InvalidInput(
            "repository_pattern is required".to_string(),
        ));
    }
    if !pattern.contains('/') {
        return Err(AppError::InvalidInput(
            "repository_pattern must include an org (e.g. \"org/repo\" or \"org/*\")".to_string(),
        ));
    }

    // Validate role — admin is not permitted for OIDC policies
    let role = body
        .role
        .as_ref()
        .and_then(|r| ApiKeyRole::parse(r))
        .unwrap_or(ApiKeyRole::Contributor);

    if role == ApiKeyRole::Admin {
        return Err(AppError::InvalidInput(
            "OIDC policies only support 'contributor' and 'viewer' roles. Admin access is not permitted via OIDC.".to_string(),
        ));
    }

    let policy = OidcPolicy {
        id: Uuid::new_v4().to_string(),
        repository_pattern: pattern.to_string(),
        role: role.as_str().to_string(),
        description: body.description.clone(),
        enabled: true,
        created_at: Utc::now(),
        updated_at: Utc::now(),
    };

    crate::db::github_oidc_policies::insert(pool.connection(), &policy).await?;

    Ok(HttpResponse::Created().json(OidcPolicyResponse::from(policy)))
}

/// Get an OIDC policy by ID.
///
/// GET /api/v1/auth/oidc-policies/{id}
#[utoipa::path(
    get,
    path = "/api/v1/auth/oidc-policies/{id}",
    tag = "Auth",
    params(("id" = String, Path, description = "Policy UUID")),
    responses(
        (status = 200, description = "OIDC policy details", body = OidcPolicyResponse),
        (status = 401, description = "Unauthorized - admin role required"),
        (status = 404, description = "Policy not found")
    ),
    security(("api_key" = []))
)]
#[get("/auth/oidc-policies/{id}")]
pub async fn get_policy(
    auth: ApiKeyAuth,
    path: web::Path<String>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to view OIDC policies".to_string(),
        ));
    }

    let id = path.into_inner();
    let policy = crate::db::github_oidc_policies::find_by_id(pool.connection(), &id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("OIDC policy {}", id)))?;

    Ok(HttpResponse::Ok().json(OidcPolicyResponse::from(policy)))
}

/// Update an OIDC policy.
///
/// PUT /api/v1/auth/oidc-policies/{id}
#[utoipa::path(
    put,
    path = "/api/v1/auth/oidc-policies/{id}",
    tag = "Auth",
    params(("id" = String, Path, description = "Policy UUID")),
    request_body = UpdateOidcPolicyRequest,
    responses(
        (status = 200, description = "OIDC policy updated", body = OidcPolicyResponse),
        (status = 401, description = "Unauthorized - admin role required"),
        (status = 404, description = "Policy not found")
    ),
    security(("api_key" = []))
)]
#[put("/auth/oidc-policies/{id}")]
pub async fn update_policy(
    auth: ApiKeyAuth,
    path: web::Path<String>,
    body: web::Json<UpdateOidcPolicyRequest>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to update OIDC policies".to_string(),
        ));
    }

    let id = path.into_inner();

    // Validate pattern if provided
    if let Some(ref pat) = body.repository_pattern {
        let pat = pat.trim();
        if !pat.is_empty() && !pat.contains('/') {
            return Err(AppError::InvalidInput(
                "repository_pattern must include an org (e.g. \"org/repo\" or \"org/*\")"
                    .to_string(),
            ));
        }
    }

    // Validate role if provided — admin is not permitted for OIDC policies
    if let Some(ref role_str) = body.role {
        match ApiKeyRole::parse(role_str) {
            Some(ApiKeyRole::Admin) => {
                return Err(AppError::InvalidInput(
                    "OIDC policies only support 'contributor' and 'viewer' roles. Admin access is not permitted via OIDC.".to_string(),
                ));
            }
            None => {
                return Err(AppError::InvalidInput(format!(
                    "Invalid role '{}'. Must be contributor or viewer",
                    role_str
                )));
            }
            _ => {}
        }
    }

    let updated = crate::db::github_oidc_policies::update(
        pool.connection(),
        &id,
        body.repository_pattern.as_deref(),
        body.role.as_deref(),
        body.description.as_deref(),
        body.enabled,
    )
    .await?;

    if !updated {
        return Err(AppError::NotFound(format!("OIDC policy {}", id)));
    }

    let policy = crate::db::github_oidc_policies::find_by_id(pool.connection(), &id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("OIDC policy {}", id)))?;

    Ok(HttpResponse::Ok().json(OidcPolicyResponse::from(policy)))
}

/// Delete an OIDC policy.
///
/// DELETE /api/v1/auth/oidc-policies/{id}
#[utoipa::path(
    delete,
    path = "/api/v1/auth/oidc-policies/{id}",
    tag = "Auth",
    params(("id" = String, Path, description = "Policy UUID")),
    responses(
        (status = 200, description = "OIDC policy deleted"),
        (status = 401, description = "Unauthorized - admin role required"),
        (status = 404, description = "Policy not found")
    ),
    security(("api_key" = []))
)]
#[delete("/auth/oidc-policies/{id}")]
pub async fn delete_policy(
    auth: ApiKeyAuth,
    path: web::Path<String>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    if auth.caller.is_oidc() {
        return Err(AppError::Unauthorized(OIDC_ADMIN_DENIED_MSG.to_string()));
    }
    if !auth.caller.is_admin() {
        return Err(AppError::Unauthorized(
            "Admin role required to delete OIDC policies".to_string(),
        ));
    }

    let id = path.into_inner();
    let deleted = crate::db::github_oidc_policies::delete(pool.connection(), &id).await?;

    if deleted {
        Ok(HttpResponse::Ok().json(serde_json::json!({
            "message": "OIDC policy deleted",
            "id": id,
        })))
    } else {
        Err(AppError::NotFound(format!("OIDC policy {}", id)))
    }
}
