//! Actix-web extractors for API key authentication.
//!
//! # Security
//! - All secret values (API keys, admin keys) are wrapped in `SecretString`
//! - Secret values are never logged or exposed in debug output
//! - Memory is zeroized when secrets are dropped
//! - Constant-time comparison is used where applicable

use actix_web::dev::Payload;
use actix_web::http::StatusCode;
use actix_web::{web, FromRequest, HttpRequest, HttpResponse, ResponseError};
use secrecy::{ExposeSecret, SecretString};
use std::future::{ready, Ready};

use super::AdminKey;
use crate::config::{ADMIN_KEY_HEADER, API_KEY_HEADER};
use crate::db::DbPool;
use crate::error::ErrorResponse;
use crate::models::AuthenticatedCaller;
use crate::services::api_key;

/// Extract a secret header value, wrapping it in SecretString.
/// Returns None if the header is missing or invalid UTF-8.
fn extract_secret_header(req: &HttpRequest, header_name: &str) -> Option<SecretString> {
    req.headers()
        .get(header_name)
        .and_then(|v| v.to_str().ok())
        .map(|s| SecretString::from(s.to_string()))
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

/// Extractor that requires a valid API key.
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
    type Future = Ready<Result<Self, Self::Error>>;

    fn from_request(req: &HttpRequest, _payload: &mut Payload) -> Self::Future {
        // Get DbPool from app data
        let pool = match req.app_data::<web::Data<DbPool>>() {
            Some(pool) => pool,
            None => {
                return ready(Err(AuthError {
                    message: "Internal configuration error".to_string(),
                }));
            }
        };

        // Get stored admin key from app data (optional)
        let stored_admin_key = req.app_data::<web::Data<AdminKey>>();

        // Extract secrets from headers - immediately wrapped in SecretString
        let provided_api_key: Option<SecretString> = extract_secret_header(req, API_KEY_HEADER);
        let provided_admin_key: Option<SecretString> = extract_secret_header(req, ADMIN_KEY_HEADER);

        // Check admin key first (for bootstrap operations)
        // Uses constant-time comparison to prevent timing attacks
        if let Some(ref provided) = provided_admin_key {
            if let Some(key) = stored_admin_key {
                if key.verify(provided.expose_secret()) {
                    // Admin key authenticated - return admin caller
                    // Note: provided_admin_key is dropped here, memory zeroized
                    return ready(Ok(ApiKeyAuth {
                        caller: AuthenticatedCaller {
                            key_id: "admin".to_string(),
                            name: "Admin (Bootstrap)".to_string(),
                            key_prefix: "admin".to_string(),
                            role: crate::models::ApiKeyRole::Admin,
                        },
                    }));
                }
            }
        }

        // Check API key from database
        match provided_api_key {
            Some(ref key) => {
                // Verify the key - expose_secret() is the only way to access the value
                match api_key::verify_key(pool.get_ref(), key.expose_secret()) {
                    Ok(caller) => ready(Ok(ApiKeyAuth { caller })),
                    Err(e) => ready(Err(AuthError {
                        message: e.to_string(),
                    })),
                }
                // Note: key is dropped here, memory zeroized
            }
            None => {
                // No key provided
                ready(Err(AuthError {
                    message: "Missing API key. Provide X-API-Key header.".to_string(),
                }))
            }
        }
    }
}
