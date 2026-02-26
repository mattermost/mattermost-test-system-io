//! Actix-web extractors for API key authentication.
//!
//! # Authentication methods (checked in order)
//! 1. `X-Admin-Key` header - bootstrap admin key (constant-time comparison)
//! 2. `X-API-Key` header - database-backed API key
//! 3. `Authorization: Bearer <token>` - GitHub Actions OIDC JWT
//! 4. `tsio_session` cookie - GitHub OAuth session JWT
//!
//! # Security
//! - All secret values (API keys, admin keys) are wrapped in `SecretString`
//! - Secret values are never logged or exposed in debug output
//! - Memory is zeroized when secrets are dropped
//! - Constant-time comparison is used where applicable

use actix_web::dev::Payload;
use actix_web::http::StatusCode;
use actix_web::{FromRequest, HttpRequest, HttpResponse, ResponseError, web};
use secrecy::{ExposeSecret, SecretString};
use std::future::Future;
use std::pin::Pin;

use super::AdminKey;
use crate::config::{ADMIN_KEY_HEADER, API_KEY_HEADER, Config};
use crate::db::DbPool;
use crate::error::ErrorResponse;
use crate::models::{ApiKeyRole, AuthenticatedCaller, OIDC_ADMIN_DENIED_MSG};
use crate::services::api_key;
use crate::services::github_oidc::GitHubOidcVerifier;

/// Session cookie name (must match github_oauth.rs).
const SESSION_COOKIE: &str = "tsio_session";

/// Extract a secret header value, wrapping it in SecretString.
/// Returns None if the header is missing or invalid UTF-8.
fn extract_secret_header(req: &HttpRequest, header_name: &str) -> Option<SecretString> {
    req.headers()
        .get(header_name)
        .and_then(|v| v.to_str().ok())
        .map(|s| SecretString::from(s.to_string()))
}

/// Extract Bearer token from Authorization header, wrapped in SecretString.
fn extract_bearer_token(req: &HttpRequest) -> Option<SecretString> {
    req.headers()
        .get("Authorization")
        .and_then(|v| v.to_str().ok())
        .and_then(|s| s.strip_prefix("Bearer "))
        .map(|s| SecretString::from(s.to_string()))
}

/// Extract session token from cookie.
fn extract_session_cookie(req: &HttpRequest) -> Option<String> {
    req.cookie(SESSION_COOKIE).map(|c| c.value().to_string())
}

/// Authentication error for extractors.
#[derive(Debug)]
pub struct AuthError {
    message: String,
}

impl std::fmt::Display for AuthError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.message)
    }
}

impl ResponseError for AuthError {
    fn error_response(&self) -> HttpResponse {
        HttpResponse::build(StatusCode::UNAUTHORIZED).json(ErrorResponse {
            error: "UNAUTHORIZED".to_string(),
            message: self.message.clone(),
        })
    }
}

/// Extractor that requires a valid API key, OIDC token, or session.
///
/// Use this in handlers that require authentication:
/// ```ignore
/// async fn protected_handler(auth: ApiKeyAuth) -> impl Responder {
///     // auth.caller contains the authenticated caller info
/// }
/// ```
///
/// # Security
/// - API key from header is wrapped in `SecretString` immediately
/// - Key is never logged or exposed in debug output
/// - Memory is zeroized when the request completes
pub struct ApiKeyAuth {
    pub caller: AuthenticatedCaller,
}

impl FromRequest for ApiKeyAuth {
    type Error = AuthError;
    type Future = Pin<Box<dyn Future<Output = Result<Self, Self::Error>>>>;

    fn from_request(req: &HttpRequest, _payload: &mut Payload) -> Self::Future {
        // Get DbPool from app data
        let pool = match req.app_data::<web::Data<DbPool>>() {
            Some(pool) => pool.clone(),
            None => {
                return Box::pin(async {
                    Err(AuthError {
                        message: "Internal configuration error".to_string(),
                    })
                });
            }
        };

        // Get stored admin key from app data (optional)
        let stored_admin_key = req.app_data::<web::Data<AdminKey>>().cloned();

        // Get OIDC verifier from app data (optional, only present if OIDC enabled)
        let oidc_verifier = req.app_data::<web::Data<GitHubOidcVerifier>>().cloned();

        // Get config for session verification
        let config = req.app_data::<web::Data<Config>>().cloned();

        // Extract secrets from headers - immediately wrapped in SecretString
        let provided_api_key: Option<SecretString> = extract_secret_header(req, API_KEY_HEADER);
        let provided_admin_key: Option<SecretString> = extract_secret_header(req, ADMIN_KEY_HEADER);

        // Extract Bearer token (SecretString) and session cookie
        let bearer_token: Option<SecretString> = extract_bearer_token(req);
        let session_token = extract_session_cookie(req);

        Box::pin(async move {
            // 1. Check admin key first (for bootstrap operations)
            // Uses constant-time comparison to prevent timing attacks
            if let Some(ref provided) = provided_admin_key
                && let Some(key) = stored_admin_key
                && key.verify(provided.expose_secret())
            {
                return Ok(ApiKeyAuth {
                    caller: AuthenticatedCaller {
                        key_id: "admin".to_string(),
                        role: ApiKeyRole::Admin,
                        oidc_claims: None,
                    },
                });
            }

            // 2. Check API key from database
            if let Some(ref key) = provided_api_key {
                match api_key::verify_key(&pool, key.expose_secret()).await {
                    Ok(caller) => return Ok(ApiKeyAuth { caller }),
                    Err(e) => {
                        return Err(AuthError {
                            message: e.to_string(),
                        });
                    }
                }
            }

            // 3. Check Bearer token (GitHub Actions OIDC)
            if let Some(ref token_secret) = bearer_token {
                if let Some(ref verifier) = oidc_verifier {
                    match verifier.verify_token(token_secret, pool.get_ref()).await {
                        Ok(caller) => {
                            // Enforce admin-denial at auth layer: OIDC callers
                            // MUST NOT be granted admin role (defense-in-depth).
                            if caller.role == ApiKeyRole::Admin {
                                tracing::warn!(
                                    repository = caller
                                        .oidc_claims
                                        .as_ref()
                                        .map(|c| c.repository.as_str())
                                        .unwrap_or("unknown"),
                                    "OIDC caller resolved to admin role — rejecting"
                                );
                                return Err(AuthError {
                                    message: OIDC_ADMIN_DENIED_MSG.to_string(),
                                });
                            }
                            // Log successful OIDC auth (FR-013)
                            if let Some(ref claims) = caller.oidc_claims {
                                tracing::info!(
                                    repository = %claims.repository,
                                    actor = %claims.actor,
                                    role = %caller.role,
                                    "OIDC authentication successful"
                                );
                            }
                            return Ok(ApiKeyAuth { caller });
                        }
                        Err(e) => {
                            tracing::warn!(
                                error = %e,
                                "OIDC authentication failed"
                            );
                            return Err(AuthError {
                                message: format!("OIDC authentication failed: {}", e),
                            });
                        }
                    }
                } else {
                    return Err(AuthError {
                        message: "Bearer token provided but OIDC is not enabled".to_string(),
                    });
                }
            }

            // 4. Check session cookie (GitHub OAuth session)
            if let Some(ref token) = session_token
                && let Some(ref cfg) = config
                && cfg.github_oauth.enabled
            {
                match crate::services::github_oauth::verify_session_token(
                    token,
                    &cfg.github_oauth.session_secret,
                ) {
                    Ok(claims) => {
                        let role = ApiKeyRole::parse(&claims.role).unwrap_or(ApiKeyRole::Viewer);
                        return Ok(ApiKeyAuth {
                            caller: AuthenticatedCaller {
                                key_id: format!("session:{}", claims.user_id),
                                role,
                                oidc_claims: None,
                            },
                        });
                    }
                    Err(_) => {
                        // Invalid session, fall through to error
                    }
                }
            }

            // No valid authentication found
            Err(AuthError {
                message: "Missing API key. Provide X-API-Key or Authorization: Bearer header."
                    .to_string(),
            })
        })
    }
}
