//! Shared test helpers for OIDC E2E tests.

use actix_web::{App, dev::ServiceResponse, test, web};
use mattermost_tsio_lib::auth::AdminKey;
use mattermost_tsio_lib::config::{Config, GitHubOidcSettings};
use mattermost_tsio_lib::db::DbPool;
use mattermost_tsio_lib::services::EventBroadcaster;
use mattermost_tsio_lib::services::github_oidc::GitHubOidcVerifier;
use serde_json::Value;
use std::sync::OnceLock;
use uuid::Uuid;

use super::mock_oidc_provider::{MockOidcProvider, TestKeyPair};

/// Admin key used in tests.
pub const TEST_ADMIN_KEY: &str = "test-admin-key-for-oidc-e2e";

/// Shared mock OIDC provider — one HTTP server for all tests.
struct SharedMock {
    mock: MockOidcProvider,
    primary_key: TestKeyPair,
}

static SHARED_MOCK: OnceLock<SharedMock> = OnceLock::new();
static MIGRATIONS_RUN: OnceLock<()> = OnceLock::new();

async fn init_mock() -> &'static SharedMock {
    if let Some(m) = SHARED_MOCK.get() {
        return m;
    }
    let primary_key = TestKeyPair::generate("shared-test-key");
    let mock = MockOidcProvider::start(primary_key.clone()).await;
    let _ = SHARED_MOCK.set(SharedMock { mock, primary_key });
    SHARED_MOCK.get().unwrap()
}

/// Get the shared mock OIDC provider.
pub async fn get_mock() -> &'static MockOidcProvider {
    &init_mock().await.mock
}

/// Get the shared primary signing key.
pub async fn get_primary_key() -> TestKeyPair {
    init_mock().await.primary_key.clone()
}

/// Create a fresh DB pool. Migrations run only once.
pub async fn create_test_pool() -> DbPool {
    let mut config = Config::from_env().expect(
        "Failed to load config. Ensure TSIO_DB_URL or individual DB vars are set, \
         and that PostgreSQL is running.",
    );
    config.database.max_connections = 2;
    config.database.min_connections = 1;

    let pool = DbPool::new(&config)
        .await
        .expect("Failed to connect to database");

    if MIGRATIONS_RUN.get().is_none() {
        pool.run_migrations()
            .await
            .expect("Failed to run migrations");
        let _ = MIGRATIONS_RUN.set(());
    }

    pool
}

/// Generate a unique org name for test isolation.
pub fn unique_org(prefix: &str) -> String {
    format!(
        "{}-{}",
        prefix,
        Uuid::new_v4().to_string().split('-').next().unwrap()
    )
}

/// Create a test TSIO app.
pub async fn create_test_app(
    pool: &DbPool,
    mock_issuer_url: &str,
) -> impl actix_web::dev::Service<
    actix_http::Request,
    Response = ServiceResponse,
    Error = actix_web::Error,
> {
    let oidc_settings = GitHubOidcSettings {
        enabled: true,
        allowed_repos: vec![],
        issuer: mock_issuer_url.to_string(),
        audience: None,
    };

    let verifier = GitHubOidcVerifier::new(&oidc_settings);
    let admin_key = AdminKey::new(Some(TEST_ADMIN_KEY.to_string()));
    let broadcaster = EventBroadcaster::new();
    let config = Config::from_env().expect("Failed to load config for test app");

    test::init_service(
        App::new()
            .app_data(web::Data::new(pool.clone()))
            .app_data(web::Data::new(verifier))
            .app_data(web::Data::new(admin_key))
            .app_data(web::Data::new(broadcaster))
            .app_data(web::Data::new(config))
            .service(
                web::scope("/api/v1")
                    .configure(mattermost_tsio_lib::api::test_reports::configure_routes)
                    .configure(mattermost_tsio_lib::services::oidc_policy::configure_routes),
            ),
    )
    .await
}

/// Create an OIDC policy via the admin API.
pub async fn create_test_policy<S>(app: &S, pattern: &str, role: &str) -> Value
where
    S: actix_web::dev::Service<
            actix_http::Request,
            Response = ServiceResponse,
            Error = actix_web::Error,
        >,
{
    let req = test::TestRequest::post()
        .uri("/api/v1/auth/oidc-policies")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({
            "repository_pattern": pattern,
            "role": role,
        }))
        .to_request();

    let resp = test::call_service(app, req).await;
    let status = resp.status();
    let body: Value = test::read_body_json(resp).await;
    assert!(
        status.is_success(),
        "Failed to create policy ({}): {}",
        status,
        body
    );
    body
}

/// Upload a report using an OIDC Bearer token.
pub async fn upload_report_with_token<S>(
    app: &S,
    token: &str,
    github_metadata: Option<Value>,
) -> (u16, Value)
where
    S: actix_web::dev::Service<
            actix_http::Request,
            Response = ServiceResponse,
            Error = actix_web::Error,
        >,
{
    let mut body = serde_json::json!({
        "expected_jobs": 1,
        "framework": "playwright",
    });
    if let Some(meta) = github_metadata {
        body["github_metadata"] = meta;
    }

    let req = test::TestRequest::post()
        .uri("/api/v1/reports")
        .insert_header(("Authorization", format!("Bearer {}", token)))
        .set_json(body)
        .to_request();

    let resp = test::call_service(app, req).await;
    let status = resp.status().as_u16();
    let body: Value = test::read_body_json(resp).await;
    (status, body)
}

/// Get a report by ID using a Bearer token.
pub async fn get_report_with_token<S>(app: &S, token: &str, report_id: &str) -> (u16, Value)
where
    S: actix_web::dev::Service<
            actix_http::Request,
            Response = ServiceResponse,
            Error = actix_web::Error,
        >,
{
    let req = test::TestRequest::get()
        .uri(&format!("/api/v1/reports/{}", report_id))
        .insert_header(("Authorization", format!("Bearer {}", token)))
        .to_request();

    let resp = test::call_service(app, req).await;
    let status = resp.status().as_u16();
    let body: Value = test::read_body_json(resp).await;
    (status, body)
}

/// List reports using a Bearer token.
pub async fn list_reports_with_token<S>(app: &S, token: &str) -> (u16, Value)
where
    S: actix_web::dev::Service<
            actix_http::Request,
            Response = ServiceResponse,
            Error = actix_web::Error,
        >,
{
    let req = test::TestRequest::get()
        .uri("/api/v1/reports?limit=10")
        .insert_header(("Authorization", format!("Bearer {}", token)))
        .to_request();

    let resp = test::call_service(app, req).await;
    let status = resp.status().as_u16();
    let body: Value = test::read_body_json(resp).await;
    (status, body)
}

/// Attempt to access an admin endpoint with a Bearer token.
pub async fn access_admin_endpoint_with_token<S>(app: &S, token: &str) -> u16
where
    S: actix_web::dev::Service<
            actix_http::Request,
            Response = ServiceResponse,
            Error = actix_web::Error,
        >,
{
    let req = test::TestRequest::get()
        .uri("/api/v1/auth/oidc-policies")
        .insert_header(("Authorization", format!("Bearer {}", token)))
        .to_request();

    let resp = test::call_service(app, req).await;
    resp.status().as_u16()
}
