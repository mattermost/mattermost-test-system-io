//! GitHub OAuth routes for web UI authentication.
//!
//! Implements a short-lived access token + refresh token pattern:
//! - Access token: HS256 JWT in `tsio_session` HttpOnly cookie (default 15 min)
//! - Refresh token: opaque token (SHA-256 hashed in DB) in `tsio_refresh` HttpOnly cookie (default 7 days)
//!
//! Endpoints:
//! 1. GET /auth/github — Redirect to GitHub (with CSRF `state`)
//! 2. GET /auth/github/callback — Verify state, exchange code, issue token pair
//! 3. POST /auth/refresh — Rotate: validate refresh token, issue new pair, revoke old
//! 4. GET /auth/me — Return current user from access token
//! 5. POST /auth/logout — Revoke refresh token in DB, clear both cookies

use actix_web::cookie::{Cookie, SameSite};
use actix_web::{HttpRequest, HttpResponse, get, post, web};
use jsonwebtoken::{Algorithm, DecodingKey, EncodingKey, Header, Validation, decode, encode};
use secrecy::{ExposeSecret, SecretString};
use tracing::{info, warn};
use uuid::Uuid;

use crate::config::Config;
use crate::db::DbPool;
use crate::error::{AppError, AppResult};
use crate::models::user::{GitHubOrg, GitHubUserInfo, SessionClaims, UserResponse};

/// Access token cookie name (short-lived JWT).
const ACCESS_COOKIE: &str = "tsio_session";
/// Refresh token cookie name (long-lived opaque token).
const REFRESH_COOKIE: &str = "tsio_refresh";
/// OAuth CSRF state cookie — stores the random `state` parameter
/// sent to GitHub, verified on callback to prevent login CSRF.
const OAUTH_STATE_COOKIE: &str = "tsio_oauth_state";
/// Session JWT issuer.
pub const SESSION_ISSUER: &str = "tsio";
/// HTTP connect timeout for GitHub API calls.
const HTTP_CONNECT_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(5);
/// HTTP total timeout for GitHub API calls.
const HTTP_REQUEST_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(10);

/// Configure OAuth routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(github_login)
        .service(github_callback)
        .service(refresh)
        .service(get_current_user)
        .service(logout);
}

/// Build an HTTP client with timeouts.
fn build_http_client() -> reqwest::Client {
    reqwest::Client::builder()
        .connect_timeout(HTTP_CONNECT_TIMEOUT)
        .timeout(HTTP_REQUEST_TIMEOUT)
        .build()
        .expect("Failed to build HTTP client for OAuth")
}

/// Generate a cryptographically random string.
fn generate_random_hex() -> String {
    let random_bytes: [u8; 32] = rand::random();
    hex::encode(random_bytes)
}

// ============================================================================
// Endpoints
// ============================================================================

/// Redirect to GitHub OAuth authorization page.
///
/// GET /api/v1/auth/github
#[get("/auth/github")]
pub async fn github_login(config: web::Data<Config>) -> AppResult<HttpResponse> {
    let oauth = &config.github_oauth;
    if !oauth.enabled {
        return Err(AppError::InvalidInput(
            "GitHub OAuth is not configured".to_string(),
        ));
    }

    let client_id = oauth.client_id.as_ref().ok_or_else(|| {
        AppError::InvalidInput("GitHub OAuth client ID not configured".to_string())
    })?;

    let redirect_uri = oauth
        .redirect_url
        .as_deref()
        .unwrap_or("/api/v1/auth/github/callback");

    let state = generate_random_hex();

    let scope = if oauth.allowed_orgs.is_empty() {
        ""
    } else {
        "read:org"
    };

    let authorize_url = format!(
        "https://github.com/login/oauth/authorize?client_id={}&redirect_uri={}&state={}&scope={}",
        client_id,
        urlencoding::encode(redirect_uri),
        urlencoding::encode(&state),
        scope,
    );

    let mut state_cookie = Cookie::new(OAUTH_STATE_COOKIE, state);
    state_cookie.set_path("/");
    state_cookie.set_http_only(true);
    state_cookie.set_same_site(SameSite::Lax);
    state_cookie.set_secure(config.environment.is_production());

    Ok(HttpResponse::Found()
        .cookie(state_cookie)
        .append_header(("Location", authorize_url))
        .finish())
}

/// Handle GitHub OAuth callback.
///
/// GET /api/v1/auth/github/callback?code=...&state=...
#[get("/auth/github/callback")]
pub async fn github_callback(
    req: HttpRequest,
    query: web::Query<CallbackQuery>,
    config: web::Data<Config>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    let oauth = &config.github_oauth;
    if !oauth.enabled {
        return Err(AppError::InvalidInput(
            "GitHub OAuth is not configured".to_string(),
        ));
    }

    // --- CSRF state verification ---
    let expected_state = req
        .cookie(OAUTH_STATE_COOKIE)
        .map(|c| c.value().to_string())
        .ok_or_else(|| {
            warn!("OAuth callback: missing state cookie");
            AppError::Unauthorized("OAuth state verification failed".to_string())
        })?;

    let provided_state = query.state.as_deref().unwrap_or("");
    if provided_state.is_empty() || provided_state != expected_state {
        warn!("OAuth callback: state mismatch");
        return Err(AppError::Unauthorized(
            "OAuth state verification failed".to_string(),
        ));
    }

    let client_id = oauth.client_id.as_ref().ok_or_else(|| {
        AppError::InvalidInput("GitHub OAuth client ID not configured".to_string())
    })?;
    let client_secret = oauth.client_secret.as_ref().ok_or_else(|| {
        AppError::InvalidInput("GitHub OAuth client secret not configured".to_string())
    })?;

    // --- Exchange code for access token ---
    let http_client = build_http_client();
    let token_response: TokenResponse = http_client
        .post("https://github.com/login/oauth/access_token")
        .header("Accept", "application/json")
        .json(&serde_json::json!({
            "client_id": client_id,
            "client_secret": client_secret.expose_secret(),
            "code": query.code,
        }))
        .send()
        .await
        .map_err(|e| {
            warn!("OAuth: failed to exchange code: {}", e);
            AppError::Unauthorized("GitHub authentication failed".to_string())
        })?
        .json()
        .await
        .map_err(|e| {
            warn!("OAuth: failed to parse token response: {}", e);
            AppError::Unauthorized("GitHub authentication failed".to_string())
        })?;

    if let Some(ref err) = token_response.error {
        warn!("OAuth: GitHub returned error: {}", err);
        return Err(AppError::Unauthorized(
            "GitHub authentication failed".to_string(),
        ));
    }

    let gh_access_token: SecretString = token_response
        .access_token
        .map(SecretString::from)
        .ok_or_else(|| {
            warn!("OAuth: no access_token in response");
            AppError::Unauthorized("GitHub authentication failed".to_string())
        })?;

    // --- Fetch user info ---
    let user_info: GitHubUserInfo = http_client
        .get("https://api.github.com/user")
        .header(
            "Authorization",
            format!("Bearer {}", gh_access_token.expose_secret()),
        )
        .header("User-Agent", "tsio")
        .send()
        .await
        .map_err(|e| {
            warn!("OAuth: failed to fetch user info: {}", e);
            AppError::Unauthorized("GitHub authentication failed".to_string())
        })?
        .json()
        .await
        .map_err(|e| {
            warn!("OAuth: failed to parse user info: {}", e);
            AppError::Unauthorized("GitHub authentication failed".to_string())
        })?;

    // --- Check org membership if configured ---
    if !oauth.allowed_orgs.is_empty() {
        let orgs: Vec<GitHubOrg> = http_client
            .get("https://api.github.com/user/orgs")
            .header(
                "Authorization",
                format!("Bearer {}", gh_access_token.expose_secret()),
            )
            .header("User-Agent", "tsio")
            .send()
            .await
            .map_err(|e| {
                warn!("OAuth: failed to fetch user orgs: {}", e);
                AppError::Unauthorized("GitHub authentication failed".to_string())
            })?
            .json()
            .await
            .map_err(|e| {
                warn!("OAuth: failed to parse user orgs: {}", e);
                AppError::Unauthorized("GitHub authentication failed".to_string())
            })?;

        let user_orgs: Vec<String> = orgs.iter().map(|o| o.login.to_lowercase()).collect();
        let allowed = oauth
            .allowed_orgs
            .iter()
            .any(|a| user_orgs.contains(&a.to_lowercase()));

        if !allowed {
            warn!(
                "OAuth: user '{}' not in allowed orgs {:?}",
                user_info.login, oauth.allowed_orgs
            );
            return Err(AppError::Unauthorized(
                "You are not a member of an authorized organization".to_string(),
            ));
        }
    }

    // --- Upsert user in DB ---
    let user = crate::db::users::upsert_from_github(
        pool.connection(),
        user_info.id,
        &user_info.login,
        user_info.name.as_deref(),
        user_info.avatar_url.as_deref(),
        user_info.email.as_deref(),
    )
    .await?;

    info!(
        "GitHub OAuth login: user='{}' (id={})",
        user.username, user.id
    );

    // --- Issue token pair ---
    let is_prod = config.environment.is_production();
    let mut response = issue_token_pair(
        &user.id,
        &user.username,
        &user.role,
        oauth,
        pool.get_ref(),
        is_prod,
    )
    .await?;

    // Clear state cookie
    let mut clear_state = Cookie::new(OAUTH_STATE_COOKIE, "");
    clear_state.set_path("/");
    clear_state.set_http_only(true);
    clear_state.set_same_site(SameSite::Lax);
    clear_state.set_secure(is_prod);

    Ok(response.cookie(clear_state).finish())
}

/// Refresh the access token using the refresh token.
///
/// Rotates: old refresh token is revoked, new pair is issued.
///
/// POST /api/v1/auth/refresh
#[post("/auth/refresh")]
pub async fn refresh(
    req: HttpRequest,
    config: web::Data<Config>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    let oauth = &config.github_oauth;
    if !oauth.enabled {
        return Err(AppError::Unauthorized(
            "GitHub OAuth is not configured".to_string(),
        ));
    }

    // Read refresh token from cookie
    let refresh_token = req
        .cookie(REFRESH_COOKIE)
        .map(|c| c.value().to_string())
        .ok_or_else(|| AppError::Unauthorized("No refresh token".to_string()))?;

    // Validate refresh token against DB
    let token_hash = crate::db::refresh_tokens::hash_token(&refresh_token);
    let user_id = crate::db::refresh_tokens::find_valid_by_hash(pool.connection(), &token_hash)
        .await?
        .ok_or_else(|| {
            warn!("Refresh: invalid or expired refresh token");
            AppError::Unauthorized("Invalid refresh token".to_string())
        })?;

    // Revoke the old refresh token (rotation)
    crate::db::refresh_tokens::revoke_by_hash(pool.connection(), &token_hash).await?;

    // Fetch user from DB
    let user = crate::db::users::find_by_id(pool.connection(), &user_id.to_string())
        .await?
        .ok_or_else(|| {
            warn!("Refresh: user {} not found", user_id);
            AppError::Unauthorized("User not found".to_string())
        })?;

    // Issue new token pair
    let is_prod = config.environment.is_production();
    let mut response = issue_token_pair(
        &user.id,
        &user.username,
        &user.role,
        oauth,
        pool.get_ref(),
        is_prod,
    )
    .await?;

    Ok(response.finish())
}

/// Get current authenticated user from access token.
///
/// GET /api/v1/auth/me
#[get("/auth/me")]
pub async fn get_current_user(
    req: HttpRequest,
    config: web::Data<Config>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    let oauth = &config.github_oauth;
    if !oauth.enabled {
        return Ok(HttpResponse::Ok().json(serde_json::json!({ "user": null })));
    }

    let token = match req.cookie(ACCESS_COOKIE) {
        Some(c) => c.value().to_string(),
        None => return Ok(HttpResponse::Ok().json(serde_json::json!({ "user": null }))),
    };

    let claims = match verify_session_token(&token, &oauth.session_secret) {
        Ok(c) => c,
        Err(_) => return Ok(HttpResponse::Ok().json(serde_json::json!({ "user": null }))),
    };

    let user = crate::db::users::find_by_id(pool.connection(), &claims.user_id).await?;

    match user {
        Some(u) => {
            let response: UserResponse = u.into();
            Ok(HttpResponse::Ok().json(serde_json::json!({ "user": response })))
        }
        None => Ok(HttpResponse::Ok().json(serde_json::json!({ "user": null }))),
    }
}

/// Logout: revoke refresh token server-side, clear both cookies.
///
/// POST /api/v1/auth/logout
#[post("/auth/logout")]
pub async fn logout(
    req: HttpRequest,
    config: web::Data<Config>,
    pool: web::Data<DbPool>,
) -> AppResult<HttpResponse> {
    let is_prod = config.environment.is_production();

    // Revoke refresh token in DB if present
    if let Some(refresh_cookie) = req.cookie(REFRESH_COOKIE) {
        let token_hash = crate::db::refresh_tokens::hash_token(refresh_cookie.value());
        let _ = crate::db::refresh_tokens::revoke_by_hash(pool.connection(), &token_hash).await;
    }

    // Clear both cookies
    let mut clear_access = Cookie::new(ACCESS_COOKIE, "");
    clear_access.set_path("/");
    clear_access.set_http_only(true);
    clear_access.set_same_site(SameSite::Lax);
    clear_access.set_secure(is_prod);

    let mut clear_refresh = Cookie::new(REFRESH_COOKIE, "");
    clear_refresh.set_path("/");
    clear_refresh.set_http_only(true);
    clear_refresh.set_same_site(SameSite::Lax);
    clear_refresh.set_secure(is_prod);

    Ok(HttpResponse::Ok()
        .cookie(clear_access)
        .cookie(clear_refresh)
        .json(serde_json::json!({ "message": "Logged out" })))
}

// ============================================================================
// Helpers
// ============================================================================

/// Issue an access token + refresh token pair.
///
/// - Access token: HS256 JWT in `tsio_session` cookie
/// - Refresh token: opaque random token in `tsio_refresh` cookie (hash stored in DB)
async fn issue_token_pair(
    user_id: &str,
    username: &str,
    role: &str,
    oauth: &crate::config::GitHubOAuthSettings,
    pool: &DbPool,
    is_production: bool,
) -> AppResult<actix_web::HttpResponseBuilder> {
    // Create short-lived access token JWT
    let access_token = create_access_token(
        user_id,
        username,
        role,
        &oauth.session_secret,
        oauth.access_token_ttl_secs,
    )?;

    // Create refresh token and store hash in DB
    let raw_refresh_token = crate::db::refresh_tokens::generate_token();
    let refresh_hash = crate::db::refresh_tokens::hash_token(&raw_refresh_token);
    let user_uuid = Uuid::parse_str(user_id).map_err(|e| AppError::InvalidInput(e.to_string()))?;

    crate::db::refresh_tokens::insert(
        pool.connection(),
        user_uuid,
        &refresh_hash,
        oauth.refresh_token_ttl_secs,
    )
    .await?;

    // Build access cookie (short TTL)
    let mut access_cookie = Cookie::new(ACCESS_COOKIE, access_token);
    access_cookie.set_path("/");
    access_cookie.set_http_only(true);
    access_cookie.set_same_site(SameSite::Lax);
    access_cookie.set_secure(is_production);

    // Build refresh cookie (long TTL)
    let mut refresh_cookie = Cookie::new(REFRESH_COOKIE, raw_refresh_token);
    refresh_cookie.set_path("/");
    refresh_cookie.set_http_only(true);
    refresh_cookie.set_same_site(SameSite::Strict); // Stricter for refresh
    refresh_cookie.set_secure(is_production);

    let mut response = HttpResponse::Found();
    response.cookie(access_cookie);
    response.cookie(refresh_cookie);

    Ok(response)
}

fn create_access_token(
    user_id: &str,
    username: &str,
    role: &str,
    secret: &SecretString,
    ttl_secs: u64,
) -> AppResult<String> {
    let now = chrono::Utc::now();
    let exp = now + chrono::Duration::seconds(ttl_secs as i64);

    let claims = SessionClaims {
        sub: user_id.to_string(),
        iss: SESSION_ISSUER.to_string(),
        exp: exp.timestamp() as usize,
        iat: now.timestamp() as usize,
        user_id: user_id.to_string(),
        username: username.to_string(),
        role: role.to_string(),
    };

    let key = EncodingKey::from_secret(secret.expose_secret().as_bytes());
    encode(&Header::default(), &claims, &key)
        .map_err(|e| AppError::InvalidInput(format!("Failed to create access token: {}", e)))
}

/// Verify an access token JWT and return claims.
pub fn verify_session_token(token: &str, secret: &SecretString) -> Result<SessionClaims, String> {
    let key = DecodingKey::from_secret(secret.expose_secret().as_bytes());
    let mut validation = Validation::new(Algorithm::HS256);
    validation.set_issuer(&[SESSION_ISSUER]);
    validation.validate_aud = false;

    let token_data = decode::<SessionClaims>(token, &key, &validation)
        .map_err(|e| format!("Invalid session token: {}", e))?;

    Ok(token_data.claims)
}

// ============================================================================
// Types
// ============================================================================

#[derive(serde::Deserialize)]
pub struct CallbackQuery {
    pub code: String,
    pub state: Option<String>,
}

#[derive(serde::Deserialize)]
struct TokenResponse {
    access_token: Option<String>,
    error: Option<String>,
}
