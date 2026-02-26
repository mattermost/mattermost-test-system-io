//! E2E tests: JWKS key rotation scenario.

use super::mock_oidc_provider::{TestKeyPair, TestOidcClaims};
use super::test_helpers::*;

/// (15) Key rotation mid-test → server refreshes JWKS cache → new key works.
#[actix_rt::test]
async fn test_key_rotation_handled() {
    let org = unique_org("rotation");
    // Create an isolated mock provider for this test to avoid race conditions with other tests
    let original_key = TestKeyPair::generate("isolated-key");
    let mock = super::mock_oidc_provider::MockOidcProvider::start(original_key.clone()).await;

    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    // First request with original key — should succeed
    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/repo"));
    let token1 = mock.issue_token(&claims, &original_key);
    let (status, _) = upload_report_with_token(&app, &token1, None).await;
    assert_eq!(status, 201, "First upload with original key should succeed");

    // Rotate to a new key and wait for the mock to serve the updated JWKS
    let new_key = TestKeyPair::generate("rotated-key");
    mock.rotate_keys(new_key.clone());
    tokio::time::sleep(std::time::Duration::from_millis(100)).await;

    // Second request with new key — server should refresh JWKS and succeed
    let claims2 =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/repo"));
    let token2 = mock.issue_token(&claims2, &new_key);
    let (status, _) = upload_report_with_token(&app, &token2, None).await;

    // No need to restore original key since this mock is isolated to this test
    // mock.rotate_keys(original_key);

    assert_eq!(
        status, 201,
        "Upload with rotated key should succeed after JWKS refresh"
    );
}
