//! E2E tests: Schema column verification scenarios.

use super::test_helpers::*;

/// API-key register -> typed columns populated, no OIDC claims in response.
#[actix_rt::test]
async fn test_api_key_upload_no_oidc_claims() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let unique_commit = format!(
        "{}deadbeef1234567",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    // Register with admin key (not OIDC) -- must include required fields
    let req = actix_web::test::TestRequest::post()
        .uri("/api/v1/reports/register")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({
            "framework": "playwright",
            "name": "playwright",
            "repository": "test-org/no-oidc-repo",
            "commit": unique_commit,
            "gh_run_id": "400001",
            "gh_job_id": "400001",
            "gh_job_name": "test-report-400001",
        }))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(resp.status().as_u16(), 200);
    let register_body: serde_json::Value = actix_web::test::read_body_json(resp).await;
    let report_group_id = register_body["report_id"].as_str().unwrap();

    // Get detail (report group)
    let req = actix_web::test::TestRequest::get()
        .uri(&format!("/api/v1/reports/{}", report_group_id))
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    let detail: serde_json::Value = actix_web::test::read_body_json(resp).await;

    // oidc_claims should be absent (no longer in API responses)
    assert!(
        detail.get("oidc_claims").is_none() || detail["oidc_claims"].is_null(),
        "API-key upload should have no oidc_claims"
    );

    // Typed columns should be populated from request body
    assert_eq!(
        detail["repository"].as_str(),
        Some("test-org/no-oidc-repo"),
        "repository typed column should match request"
    );
    assert_eq!(
        detail["commit"].as_str(),
        Some(unique_commit.as_str()),
        "commit typed column should match request"
    );

    // github_metadata JSONB should NOT be in response
    assert!(
        detail.get("github_metadata").is_none() || detail["github_metadata"].is_null(),
        "github_metadata JSONB should not be in response"
    );
}

/// Register with gh_run_id -> gh_run_id typed column populated in response.
#[actix_rt::test]
async fn test_gh_run_id_stored_in_typed_column() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let unique_commit = format!(
        "{}aabbccdd11223344",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    let req = actix_web::test::TestRequest::post()
        .uri("/api/v1/reports/register")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({
            "framework": "playwright",
            "name": "playwright",
            "repository": "test-org/run-id-repo",
            "commit": unique_commit,
            "gh_run_id": "99887766",
            "gh_pr_number": 42,
            "gh_job_id": "400002",
            "gh_job_name": "test-report-400002",
        }))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(resp.status().as_u16(), 200);
    let register_body: serde_json::Value = actix_web::test::read_body_json(resp).await;
    let report_group_id = register_body["report_id"].as_str().unwrap();

    // Get detail (report group)
    let req = actix_web::test::TestRequest::get()
        .uri(&format!("/api/v1/reports/{}", report_group_id))
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    let detail: serde_json::Value = actix_web::test::read_body_json(resp).await;

    assert_eq!(
        detail["repository"].as_str(),
        Some("test-org/run-id-repo"),
        "repository should match"
    );
    assert_eq!(
        detail["commit"].as_str(),
        Some(unique_commit.as_str()),
        "commit should match"
    );
    assert_eq!(
        detail["gh_run_id"].as_str(),
        Some("99887766"),
        "gh_run_id should match"
    );
    assert_eq!(
        detail["gh_pr_number"].as_i64(),
        Some(42),
        "gh_pr_number should match"
    );
}
