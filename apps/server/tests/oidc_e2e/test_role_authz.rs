//! E2E tests: Role-based authorization scenarios.

use super::mock_oidc_provider::TestOidcClaims;
use super::test_helpers::*;

/// (8) No matching policy → 401 (OIDC auth fails at role resolution).
#[actix_rt::test]
async fn test_no_matching_policy_rejected() {
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    // No policy created — repo won't match anything
    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository("unknown-org/unknown-repo");
    let token = mock.issue_token(&claims, &key);
    let (status, _) = upload_report_with_token(&app, &token, None).await;

    // Should fail — no policy matches
    assert!(
        status == 401 || status == 403,
        "No matching policy should reject: got {}",
        status
    );
}

/// (9) Viewer role → read allowed, write denied.
#[actix_rt::test]
async fn test_viewer_role_read_only() {
    let org = unique_org("viewer");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "viewer").await;

    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/some-repo"));
    let token = mock.issue_token(&claims, &key);

    // Read should succeed
    let (list_status, _) = list_reports_with_token(&app, &token).await;
    assert_eq!(list_status, 200, "Viewer should be able to list reports");

    // Write should be denied (viewer can't upload)
    let (upload_status, _) = upload_report_with_token(&app, &token, None).await;
    assert!(
        upload_status == 401 || upload_status == 403,
        "Viewer should not be able to upload: got {}",
        upload_status
    );
}

/// (10) Contributor role → read + upload allowed.
#[actix_rt::test]
async fn test_contributor_role_full_access() {
    let org = unique_org("contrib");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/test-repo"));
    let token = mock.issue_token(&claims, &key);

    // Read should succeed
    let (list_status, _) = list_reports_with_token(&app, &token).await;
    assert_eq!(
        list_status, 200,
        "Contributor should be able to list reports"
    );

    // Upload should succeed
    let (upload_status, body) = upload_report_with_token(&app, &token, None).await;
    assert_eq!(
        upload_status, 201,
        "Contributor should be able to upload: {:?}",
        body
    );
}

/// (11) OIDC caller on admin endpoint → 401 or 403.
#[actix_rt::test]
async fn test_oidc_admin_endpoint_denied() {
    let org = unique_org("admin-deny");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/repo"));
    let token = mock.issue_token(&claims, &key);

    // Admin endpoint should reject OIDC caller
    let status = access_admin_endpoint_with_token(&app, &token).await;
    assert!(
        status == 401 || status == 403,
        "OIDC caller should be denied admin access: got {}",
        status
    );
}
