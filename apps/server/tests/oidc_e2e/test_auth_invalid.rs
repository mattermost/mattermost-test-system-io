//! E2E tests: Invalid OIDC token scenarios.

use super::mock_oidc_provider::{TestKeyPair, TestOidcClaims};
use super::test_helpers::*;

/// (4) Expired token → 401.
#[actix_rt::test]
async fn test_expired_token_rejected() {
    let org = unique_org("invalid");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims = TestOidcClaims::default_for(&mock.issuer_url)
        .with_repository(&format!("{org}/test-repo"))
        .expired();
    let token = mock.issue_token(&claims, &key);
    let (status, _) = upload_report_with_token(&app, &token, None).await;

    assert_eq!(status, 401, "Expired token should be rejected");
}

/// (5) Bad signature (key not in JWKS) → 401.
#[actix_rt::test]
async fn test_bad_signature_rejected() {
    let org = unique_org("invalid");
    let unknown_key = TestKeyPair::generate("unknown-key");
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    // Sign with unknown key (not in JWKS)
    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/test-repo"));
    let token = mock.issue_token(&claims, &unknown_key);
    let (status, _) = upload_report_with_token(&app, &token, None).await;

    assert_eq!(
        status, 401,
        "Token signed with unknown key should be rejected"
    );
}

/// (6) Audience mismatch → 401.
#[actix_rt::test]
async fn test_audience_mismatch_rejected() {
    let org = unique_org("invalid");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;

    // Configure app WITH audience validation
    let oidc_settings = mattermost_tsio_lib::config::GitHubOidcSettings {
        enabled: true,
        allowed_repos: vec![],
        issuer: mock.issuer_url.clone(),
        audience: Some("expected-audience".to_string()),
    };
    let verifier =
        mattermost_tsio_lib::services::github_oidc::GitHubOidcVerifier::new(&oidc_settings);
    let admin_key = mattermost_tsio_lib::auth::AdminKey::new(Some(TEST_ADMIN_KEY.to_string()));
    let broadcaster = mattermost_tsio_lib::services::EventBroadcaster::new();
    let config = mattermost_tsio_lib::config::Config::from_env()
        .expect("Failed to load config for test app");

    let app = actix_web::test::init_service(
        actix_web::App::new()
            .app_data(actix_web::web::Data::new(pool.clone()))
            .app_data(actix_web::web::Data::new(verifier))
            .app_data(actix_web::web::Data::new(admin_key))
            .app_data(actix_web::web::Data::new(broadcaster))
            .app_data(actix_web::web::Data::new(config))
            .service(
                actix_web::web::scope("/api/v1")
                    .configure(mattermost_tsio_lib::api::test_reports::configure_routes)
                    .configure(mattermost_tsio_lib::services::oidc_policy::configure_routes),
            ),
    )
    .await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    // Sign token with WRONG audience
    let claims = TestOidcClaims::default_for(&mock.issuer_url)
        .with_repository(&format!("{org}/test-repo"))
        .with_audience("wrong-audience");
    let token = mock.issue_token(&claims, &key);
    let (status, _) = upload_report_with_token(&app, &token, None).await;

    assert_eq!(status, 401, "Token with wrong audience should be rejected");
}

/// (7) Issuer mismatch → 401.
#[actix_rt::test]
async fn test_issuer_mismatch_rejected() {
    let org = unique_org("invalid");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    // Sign token with wrong issuer
    let claims = TestOidcClaims::default_for(&mock.issuer_url)
        .with_repository(&format!("{org}/test-repo"))
        .with_issuer("https://evil-issuer.example.com");
    let token = mock.issue_token(&claims, &key);
    let (status, _) = upload_report_with_token(&app, &token, None).await;

    assert_eq!(status, 401, "Token with wrong issuer should be rejected");
}
