//! GitHub Actions OIDC token verification service.
//!
//! Verifies JWT tokens from GitHub Actions using the OIDC provider's JWKS.
//!
//! Security features:
//! - RS256 signature verification (algorithm pinned, no fallback)
//! - JWKS cached with TTL + automatic retry on key rotation (kid miss)
//! - HTTP timeouts on JWKS fetch to prevent hanging
//! - JWKS URL derived from configurable issuer
//! - OIDC policies cached with short TTL to reduce DB load
//! - Generic error messages to clients; details logged server-side
//! - Audience validation warned if not configured

use std::sync::Arc;
use std::time::{Duration, Instant};

use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode, decode_header};
use secrecy::{ExposeSecret, SecretString};
use tokio::sync::RwLock;
use tracing::{debug, error, info, warn};

use crate::config::GitHubOidcSettings;
use crate::db::DbPool;
use crate::models::github_oidc::{GitHubOidcClaims, OidcPolicy};
use crate::models::{ApiKeyRole, AuthenticatedCaller};

/// JWKS cache TTL (24 hours).
const JWKS_CACHE_TTL: Duration = Duration::from_secs(86400);

/// OIDC policy cache TTL (60 seconds).
const POLICY_CACHE_TTL: Duration = Duration::from_secs(60);

/// HTTP connect timeout for JWKS fetch.
const JWKS_CONNECT_TIMEOUT: Duration = Duration::from_secs(5);

/// HTTP total timeout for JWKS fetch.
const JWKS_REQUEST_TIMEOUT: Duration = Duration::from_secs(10);

/// Cached JWKS keys.
struct CachedKeys {
    keys: Vec<(String, DecodingKey)>,
    fetched_at: Instant,
}

/// Cached OIDC policies from DB.
struct CachedPolicies {
    policies: Vec<OidcPolicy>,
    fetched_at: Instant,
}

/// GitHub Actions OIDC token verifier.
#[derive(Clone)]
pub struct GitHubOidcVerifier {
    issuer: String,
    jwks_url: String,
    audience: Option<String>,
    allowed_repos: Vec<String>,
    jwks_cache: Arc<RwLock<Option<CachedKeys>>>,
    policy_cache: Arc<RwLock<Option<CachedPolicies>>>,
    http_client: reqwest::Client,
}

/// JWKS response from the OIDC provider.
#[derive(serde::Deserialize)]
struct JwksResponse {
    keys: Vec<serde_json::Value>,
}

impl GitHubOidcVerifier {
    /// Create a new verifier from settings.
    pub fn new(settings: &GitHubOidcSettings) -> Self {
        // Derive JWKS URL from issuer
        let jwks_url = format!("{}/.well-known/jwks", settings.issuer.trim_end_matches('/'));

        // Warn if audience is not configured
        if settings.audience.is_none() {
            warn!(
                "TSIO_GITHUB_OIDC_AUDIENCE is not set. \
                 Without audience validation, OIDC tokens minted for other services \
                 could be replayed against this server. \
                 Set TSIO_GITHUB_OIDC_AUDIENCE to your server URL for defense-in-depth."
            );
        }

        // Build HTTP client with timeouts
        let http_client = reqwest::Client::builder()
            .connect_timeout(JWKS_CONNECT_TIMEOUT)
            .timeout(JWKS_REQUEST_TIMEOUT)
            .build()
            .expect("Failed to build HTTP client for OIDC");

        info!(
            "GitHub OIDC verifier initialized (issuer={}, jwks_url={}, audience={:?}, allowed_repos={:?})",
            settings.issuer, jwks_url, settings.audience, settings.allowed_repos
        );

        Self {
            issuer: settings.issuer.clone(),
            jwks_url,
            audience: settings.audience.clone(),
            allowed_repos: settings.allowed_repos.clone(),
            jwks_cache: Arc::new(RwLock::new(None)),
            policy_cache: Arc::new(RwLock::new(None)),
            http_client,
        }
    }

    /// Verify a GitHub Actions OIDC token and return an authenticated caller.
    ///
    /// On failure, returns a generic error string safe for the client.
    /// Detailed errors are logged server-side.
    pub async fn verify_token(
        &self,
        token: &SecretString,
        pool: &DbPool,
    ) -> Result<AuthenticatedCaller, String> {
        // Decode header to get key ID (safe — header is not secret)
        let header = decode_header(token.expose_secret()).map_err(|e| {
            warn!("OIDC: invalid JWT header: {}", e);
            "Invalid token".to_string()
        })?;
        let kid = header.kid.ok_or_else(|| {
            warn!("OIDC: JWT missing 'kid' header");
            "Invalid token".to_string()
        })?;

        // Find the decoding key, retrying JWKS fetch on kid miss
        let decoding_key = self.find_key_with_retry(&kid).await.map_err(|e| {
            warn!("OIDC: key lookup failed for kid '{}': {}", kid, e);
            "Authentication failed".to_string()
        })?;

        // Build validation
        let mut validation = Validation::new(Algorithm::RS256);
        validation.set_issuer(&[&self.issuer]);
        if let Some(ref aud) = self.audience {
            validation.set_audience(&[aud]);
        } else {
            validation.validate_aud = false;
        }

        // Verify and decode
        let token_data =
            decode::<GitHubOidcClaims>(token.expose_secret(), &decoding_key, &validation).map_err(
                |e| {
                    warn!("OIDC: JWT verification failed: {}", e);
                    "Authentication failed".to_string()
                },
            )?;

        let claims = token_data.claims;

        debug!(
            "GitHub OIDC token verified: repo={}, actor={}, workflow={:?}",
            claims.repository, claims.actor, claims.workflow
        );

        // Check repository against policies (cached DB + env fallback)
        let role = self.resolve_role(&claims.repository, pool).await?;

        Ok(AuthenticatedCaller {
            key_id: format!("github-oidc:{}:{}", claims.repository, claims.actor),
            role,
            oidc_claims: Some(claims),
        })
    }

    /// Find a decoding key by kid. On miss, force a JWKS refresh and retry once.
    async fn find_key_with_retry(&self, kid: &str) -> Result<DecodingKey, String> {
        // First attempt: use cached keys
        let keys = self.get_or_fetch_keys(false).await?;
        if let Some((_, key)) = keys.iter().find(|(k, _)| k == kid) {
            return Ok(key.clone());
        }

        // Kid not found — force refresh (key rotation may have occurred)
        info!(
            "OIDC: kid '{}' not in cache, forcing JWKS refresh for key rotation",
            kid
        );
        let keys = self.get_or_fetch_keys(true).await?;
        keys.iter()
            .find(|(k, _)| k == kid)
            .map(|(_, key)| key.clone())
            .ok_or_else(|| format!("Unknown key ID '{}' after JWKS refresh", kid))
    }

    /// Resolve the role for a repository by checking cached DB policies, then env config.
    async fn resolve_role(&self, repository: &str, pool: &DbPool) -> Result<ApiKeyRole, String> {
        // Check cached DB policies
        let policies = self.get_or_fetch_policies(pool).await?;

        for policy in &policies {
            if matches_pattern(&policy.repository_pattern, repository) {
                let role = ApiKeyRole::parse(&policy.role).unwrap_or(ApiKeyRole::Contributor);
                debug!(
                    "OIDC policy matched: pattern='{}', role={}, repo={}",
                    policy.repository_pattern, policy.role, repository
                );
                return Ok(role);
            }
        }

        // Fallback to env var allow-list
        for pattern in &self.allowed_repos {
            if matches_pattern(pattern, repository) {
                debug!(
                    "OIDC env allow-list matched: pattern='{}', repo={}",
                    pattern, repository
                );
                return Ok(ApiKeyRole::Contributor);
            }
        }

        warn!(
            "GitHub OIDC: repository '{}' not in any allow-list",
            repository
        );
        Err(format!(
            "Repository '{}' is not authorized for OIDC access",
            repository
        ))
    }

    /// Get cached JWKS keys or fetch from provider. If `force_refresh` is true, skip cache.
    async fn get_or_fetch_keys(
        &self,
        force_refresh: bool,
    ) -> Result<Vec<(String, DecodingKey)>, String> {
        if !force_refresh {
            let cache = self.jwks_cache.read().await;
            if let Some(ref cached) = *cache
                && cached.fetched_at.elapsed() < JWKS_CACHE_TTL
            {
                return Ok(cached.keys.clone());
            }
        }

        // Try to fetch new keys
        match self.fetch_jwks().await {
            Ok(keys) => {
                let mut cache = self.jwks_cache.write().await;
                *cache = Some(CachedKeys {
                    keys: keys.clone(),
                    fetched_at: Instant::now(),
                });
                Ok(keys)
            }
            Err(e) => {
                // If we have stale cached keys and this isn't a forced refresh, use them
                if !force_refresh {
                    let cache = self.jwks_cache.read().await;
                    if let Some(ref cached) = *cache {
                        warn!("Failed to refresh JWKS, using stale cache: {}", e);
                        return Ok(cached.keys.clone());
                    }
                }
                Err(e)
            }
        }
    }

    /// Fetch JWKS from the OIDC provider (derived from issuer URL).
    async fn fetch_jwks(&self) -> Result<Vec<(String, DecodingKey)>, String> {
        info!("Fetching GitHub OIDC JWKS from {}", self.jwks_url);

        let response: JwksResponse = self
            .http_client
            .get(&self.jwks_url)
            .send()
            .await
            .map_err(|e| format!("Failed to fetch JWKS: {}", e))?
            .json()
            .await
            .map_err(|e| format!("Failed to parse JWKS response: {}", e))?;

        let mut keys = Vec::new();
        for jwk_value in &response.keys {
            let jwk: jsonwebtoken::jwk::Jwk = match serde_json::from_value(jwk_value.clone()) {
                Ok(j) => j,
                Err(e) => {
                    warn!("Failed to parse JWK: {}", e);
                    continue;
                }
            };

            if let Some(ref kid) = jwk.common.key_id {
                match DecodingKey::from_jwk(&jwk) {
                    Ok(key) => keys.push((kid.clone(), key)),
                    Err(e) => warn!("Failed to create decoding key from JWK {}: {}", kid, e),
                }
            }
        }

        info!("Loaded {} JWKS keys from GitHub OIDC provider", keys.len());
        Ok(keys)
    }

    /// Get cached OIDC policies or fetch from DB (60s TTL).
    async fn get_or_fetch_policies(&self, pool: &DbPool) -> Result<Vec<OidcPolicy>, String> {
        {
            let cache = self.policy_cache.read().await;
            if let Some(ref cached) = *cache
                && cached.fetched_at.elapsed() < POLICY_CACHE_TTL
            {
                return Ok(cached.policies.clone());
            }
        }

        let conn = pool.connection();
        let policies = crate::db::github_oidc_policies::list_enabled(conn)
            .await
            .map_err(|e| {
                error!("Failed to load OIDC policies from DB: {}", e);
                format!("Failed to load OIDC policies: {}", e)
            })?;

        let mut cache = self.policy_cache.write().await;
        *cache = Some(CachedPolicies {
            policies: policies.clone(),
            fetched_at: Instant::now(),
        });

        Ok(policies)
    }
}

/// Check if a repository matches a pattern.
///
/// Patterns:
/// - `org/repo` — exact match
/// - `org/*` — matches all repos in the org
///
/// A bare `*` is **not** supported; every pattern must include an org prefix.
pub fn matches_pattern(pattern: &str, repository: &str) -> bool {
    // Reject bare wildcard — require at least an org scope
    if !pattern.contains('/') {
        return false;
    }

    if let Some(org_prefix) = pattern.strip_suffix("/*") {
        // Wildcard: match any repo in the org
        repository.starts_with(&format!("{}/", org_prefix))
    } else {
        // Exact match
        pattern == repository
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_matches_pattern_exact() {
        assert!(matches_pattern("org/repo", "org/repo"));
        assert!(!matches_pattern("org/repo", "org/other"));
        assert!(!matches_pattern("org/repo", "other/repo"));
    }

    #[test]
    fn test_matches_pattern_wildcard() {
        assert!(matches_pattern("org/*", "org/repo"));
        assert!(matches_pattern("org/*", "org/another-repo"));
        assert!(!matches_pattern("org/*", "other/repo"));
    }

    #[test]
    fn test_bare_wildcard_rejected() {
        assert!(!matches_pattern("*", "org/repo"));
        assert!(!matches_pattern("*", "any/thing"));
    }

    #[test]
    fn test_invalid_patterns_rejected() {
        assert!(!matches_pattern("", "org/repo"));
        assert!(!matches_pattern("just-a-name", "org/repo"));
    }

    /// Simulates how comma-separated env var values are parsed and matched.
    #[test]
    fn test_comma_separated_repos_parsing() {
        let env_value = "org/repo-a, org/repo-b, other-org/*";
        let patterns: Vec<String> = env_value
            .split(',')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect();

        assert_eq!(patterns, vec!["org/repo-a", "org/repo-b", "other-org/*"]);
    }

    /// Verifies that iterating over parsed patterns finds the right match.
    #[test]
    fn test_comma_separated_repos_matching() {
        let patterns = vec!["org/repo-a", "org/repo-b", "other-org/*"];

        // Exact matches
        assert!(patterns.iter().any(|p| matches_pattern(p, "org/repo-a")));
        assert!(patterns.iter().any(|p| matches_pattern(p, "org/repo-b")));

        // Wildcard match
        assert!(
            patterns
                .iter()
                .any(|p| matches_pattern(p, "other-org/anything"))
        );

        // No match
        assert!(!patterns.iter().any(|p| matches_pattern(p, "org/repo-c")));
        assert!(!patterns.iter().any(|p| matches_pattern(p, "unknown/repo")));
    }

    #[test]
    fn test_comma_separated_with_whitespace_and_empties() {
        let env_value = " org/repo ,, , other-org/* , ";
        let patterns: Vec<String> = env_value
            .split(',')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect();

        assert_eq!(patterns, vec!["org/repo", "other-org/*"]);
        assert!(patterns.iter().any(|p| matches_pattern(p, "org/repo")));
        assert!(patterns.iter().any(|p| matches_pattern(p, "other-org/foo")));
    }

    #[test]
    fn test_jwks_url_derived_from_issuer() {
        let settings = GitHubOidcSettings {
            enabled: true,
            issuer: "https://token.actions.githubusercontent.com".to_string(),
            audience: None,
            allowed_repos: vec![],
        };
        let verifier = GitHubOidcVerifier::new(&settings);
        assert_eq!(
            verifier.jwks_url,
            "https://token.actions.githubusercontent.com/.well-known/jwks"
        );
    }

    #[test]
    fn test_jwks_url_strips_trailing_slash() {
        let settings = GitHubOidcSettings {
            enabled: true,
            issuer: "https://example.com/".to_string(),
            audience: None,
            allowed_repos: vec![],
        };
        let verifier = GitHubOidcVerifier::new(&settings);
        assert_eq!(verifier.jwks_url, "https://example.com/.well-known/jwks");
    }
}
