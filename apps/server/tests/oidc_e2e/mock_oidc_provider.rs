//! Mock OIDC provider for E2E tests.
//!
//! Starts an in-process HTTP server serving a JWKS endpoint and issues
//! signed JWTs replicating GitHub's claim structure.

use actix_web::{App, HttpResponse, HttpServer, get, web};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use chrono::{Duration, Utc};
use jsonwebtoken::{Algorithm, EncodingKey, Header};
use rsa::RsaPrivateKey;
use rsa::pkcs1::EncodeRsaPrivateKey;
use rsa::pkcs8::LineEnding;
use rsa::traits::PublicKeyParts;
use serde::{Deserialize, Serialize};
use std::net::TcpListener;
use std::sync::{Arc, Mutex};

/// Test RSA key pair with its JWK representation.
#[derive(Clone)]
pub struct TestKeyPair {
    pub kid: String,
    pub encoding_key: EncodingKey,
    pub n_b64: String,
    pub e_b64: String,
}

impl TestKeyPair {
    pub fn generate(kid: &str) -> Self {
        use rsa::rand_core::OsRng;
        let bits = 2048;
        let private_key = RsaPrivateKey::new(&mut OsRng, bits).expect("failed to generate RSA key");

        let pem = private_key
            .to_pkcs1_pem(LineEnding::LF)
            .expect("failed to encode private key");
        let encoding_key =
            EncodingKey::from_rsa_pem(pem.as_bytes()).expect("failed to create encoding key");

        let public_key = private_key.to_public_key();
        let n_b64 = URL_SAFE_NO_PAD.encode(public_key.n().to_bytes_be());
        let e_b64 = URL_SAFE_NO_PAD.encode(public_key.e().to_bytes_be());

        TestKeyPair {
            kid: kid.to_string(),
            encoding_key,
            n_b64,
            e_b64,
        }
    }
}

/// Shared state for the mock OIDC provider.
pub struct MockOidcState {
    pub keys: Vec<TestKeyPair>,
}

/// JWKS response format.
#[derive(Serialize)]
struct JwksResponse {
    keys: Vec<JwkKey>,
}

#[derive(Serialize)]
struct JwkKey {
    kty: String,
    n: String,
    e: String,
    kid: String,
    alg: String,
    #[serde(rename = "use")]
    use_: String,
}

#[get("/.well-known/jwks")]
async fn jwks_endpoint(state: web::Data<Arc<Mutex<MockOidcState>>>) -> HttpResponse {
    let state = state.lock().unwrap();
    let keys: Vec<JwkKey> = state
        .keys
        .iter()
        .map(|k| JwkKey {
            kty: "RSA".to_string(),
            n: k.n_b64.clone(),
            e: k.e_b64.clone(),
            kid: k.kid.clone(),
            alg: "RS256".to_string(),
            use_: "sig".to_string(),
        })
        .collect();

    HttpResponse::Ok().json(JwksResponse { keys })
}

/// GitHub OIDC claims for signing test tokens.
///
/// Covers all ~29 GitHub Actions OIDC claims plus standard JWT fields.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TestOidcClaims {
    // --- Identity claims ---
    pub sub: String,
    pub repository: String,
    pub repository_owner: String,
    pub actor: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_owner_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_visibility: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub actor_id: Option<String>,

    // --- Git ref claims ---
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sha: Option<String>,
    #[serde(rename = "ref", skip_serializing_if = "Option::is_none")]
    pub git_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ref_type: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub head_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub base_ref: Option<String>,

    // --- Workflow / run claims ---
    #[serde(skip_serializing_if = "Option::is_none")]
    pub workflow: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub event_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_number: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_attempt: Option<String>,

    // --- Environment / runner claims ---
    #[serde(skip_serializing_if = "Option::is_none")]
    pub runner_environment: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment: Option<String>,

    // --- Check / workflow ref claims ---
    #[serde(skip_serializing_if = "Option::is_none")]
    pub check_run_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub job_workflow_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub job_workflow_sha: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub workflow_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub workflow_sha: Option<String>,

    // --- JWT standard fields ---
    pub iss: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub aud: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub jti: Option<String>,
    pub exp: i64,
    pub iat: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub nbf: Option<i64>,
}

impl TestOidcClaims {
    pub fn default_for(issuer: &str) -> Self {
        let now = Utc::now();
        Self {
            // Identity
            sub: "repo:test-org/test-repo:ref:refs/heads/main".to_string(),
            repository: "test-org/test-repo".to_string(),
            repository_owner: "test-org".to_string(),
            actor: "test-user".to_string(),
            repository_id: None,
            repository_owner_id: None,
            repository_visibility: None,
            actor_id: None,
            // Git ref
            sha: Some("abc123def456".to_string()),
            git_ref: Some("refs/heads/main".to_string()),
            ref_type: Some("branch".to_string()),
            head_ref: None,
            base_ref: None,
            // Workflow / run
            workflow: Some("CI Tests".to_string()),
            event_name: Some("push".to_string()),
            run_id: Some("12345".to_string()),
            run_number: Some("42".to_string()),
            run_attempt: Some("1".to_string()),
            // Environment / runner
            runner_environment: Some("github-hosted".to_string()),
            environment: None,
            // Check / workflow ref
            check_run_id: Some("mock-check-run-1".to_string()),
            job_workflow_ref: None,
            job_workflow_sha: None,
            workflow_ref: None,
            workflow_sha: None,
            // JWT standard
            iss: issuer.to_string(),
            aud: None,
            jti: None,
            exp: (now + Duration::minutes(10)).timestamp(),
            iat: now.timestamp(),
            nbf: None,
        }
    }

    pub fn expired(mut self) -> Self {
        let past = Utc::now() - Duration::hours(1);
        self.exp = past.timestamp();
        self.iat = (past - Duration::minutes(10)).timestamp();
        self
    }

    pub fn with_audience(mut self, aud: &str) -> Self {
        self.aud = Some(aud.to_string());
        self
    }

    pub fn with_issuer(mut self, iss: &str) -> Self {
        self.iss = iss.to_string();
        self
    }

    pub fn with_repository(mut self, repo: &str) -> Self {
        self.repository = repo.to_string();
        self.sub = format!("repo:{}:ref:refs/heads/main", repo);
        if let Some(pos) = repo.find('/') {
            self.repository_owner = repo[..pos].to_string();
        }
        self
    }

    pub fn with_check_run_id(mut self, id: &str) -> Self {
        self.check_run_id = Some(id.to_string());
        self
    }

    pub fn with_event_name(mut self, name: &str) -> Self {
        self.event_name = Some(name.to_string());
        self
    }
}

/// Mock OIDC provider serving a JWKS endpoint.
pub struct MockOidcProvider {
    pub issuer_url: String,
    pub state: Arc<Mutex<MockOidcState>>,
}

impl MockOidcProvider {
    /// Start the mock OIDC provider on an ephemeral port.
    pub async fn start(initial_key: TestKeyPair) -> Self {
        let state = Arc::new(Mutex::new(MockOidcState {
            keys: vec![initial_key],
        }));

        let listener = TcpListener::bind("127.0.0.1:0").expect("failed to bind");
        let port = listener.local_addr().unwrap().port();
        let issuer_url = format!("http://127.0.0.1:{}", port);

        let state_data = state.clone();
        let server = HttpServer::new(move || {
            App::new()
                .app_data(web::Data::new(state_data.clone()))
                .service(jwks_endpoint)
        })
        .listen(listener)
        .expect("failed to listen")
        .disable_signals()
        .run();

        // Fire and forget — server lives for the process lifetime
        tokio::spawn(server);

        MockOidcProvider { issuer_url, state }
    }

    /// Issue a signed JWT with the given claims using the specified key.
    pub fn issue_token(&self, claims: &TestOidcClaims, key: &TestKeyPair) -> String {
        let mut header = Header::new(Algorithm::RS256);
        header.kid = Some(key.kid.clone());
        jsonwebtoken::encode(&header, claims, &key.encoding_key).expect("failed to encode JWT")
    }

    /// Rotate keys: replace all keys with a new key.
    pub fn rotate_keys(&self, new_key: TestKeyPair) {
        let mut state = self.state.lock().unwrap();
        state.keys = vec![new_key];
    }
}
