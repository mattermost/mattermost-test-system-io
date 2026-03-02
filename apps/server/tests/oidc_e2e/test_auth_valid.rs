//! E2E tests: Valid OIDC token scenarios.

use super::mock_oidc_provider::TestOidcClaims;
use super::test_helpers::*;

/// (1) Valid OIDC token with contributor policy → upload succeeds.
#[actix_rt::test]
async fn test_valid_oidc_upload() {
    let org = unique_org("valid");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    // Create policy granting contributor to {org}/*
    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    // Issue token and upload
    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/test-repo"));
    let token = mock.issue_token(&claims, &key);
    let (status, body) = upload_report_with_token(&app, &token, &format!("{org}/test-repo")).await;

    assert_eq!(status, 200, "Register should succeed: {:?}", body);
    assert!(body["report_id"].is_string());
}

/// (2) Valid OIDC token → report list succeeds.
#[actix_rt::test]
async fn test_valid_oidc_list_reports() {
    let org = unique_org("valid");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/test-repo"));
    let token = mock.issue_token(&claims, &key);
    let (status, body) = list_reports_with_token(&app, &token).await;

    assert_eq!(status, 200, "List should succeed: {:?}", body);
    assert!(body["reports"].is_array());
}

/// (3) Valid OIDC token → report detail has typed columns, no github_metadata JSONB.
#[actix_rt::test]
async fn test_valid_oidc_report_detail() {
    let org = unique_org("valid");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/test-repo"));
    let token = mock.issue_token(&claims, &key);

    // Upload first
    let (_, upload_body) =
        upload_report_with_token(&app, &token, &format!("{org}/test-repo")).await;
    let report_id = upload_body["report_id"].as_str().unwrap();

    // Get detail
    let (status, body) = get_report_with_token(&app, &token, report_id).await;
    assert_eq!(status, 200, "Detail should succeed: {:?}", body);
    assert_eq!(body["id"].as_str().unwrap(), report_id);

    // Typed columns should be populated
    assert_eq!(
        body["repository"].as_str(),
        Some(format!("{org}/test-repo").as_str()),
        "repository typed column should match request"
    );
    // Branch is stored as the full ref from OIDC claims
    assert!(
        body["branch"].as_str().unwrap().contains("main"),
        "branch typed column should contain 'main'"
    );
    assert!(
        body["commit"].is_string() && !body["commit"].as_str().unwrap().is_empty(),
        "commit typed column should be populated"
    );

    // github_metadata JSONB should NOT be present
    assert!(
        body.get("github_metadata").is_none() || body["github_metadata"].is_null(),
        "github_metadata JSONB should not be in detail response"
    );
}
