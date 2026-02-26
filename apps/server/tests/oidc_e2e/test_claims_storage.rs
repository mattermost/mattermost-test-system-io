//! E2E tests: OIDC claims storage scenarios.

use super::mock_oidc_provider::TestOidcClaims;
use super::test_helpers::*;

/// (12) OIDC upload → 13 safe claims + 3 audit fields persisted correctly.
#[actix_rt::test]
async fn test_oidc_claims_persisted() {
    let org = unique_org("claims");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/test-repo"));
    let token = mock.issue_token(&claims, &key);

    // Upload
    let (status, upload_body) = upload_report_with_token(&app, &token, None).await;
    assert_eq!(status, 201);

    let report_id = upload_body["report_id"].as_str().unwrap();

    // Get report detail — should include oidc_claims
    let (status, detail) = get_report_with_token(&app, &token, report_id).await;
    assert_eq!(status, 200);

    let oidc = &detail["oidc_claims"];
    assert!(!oidc.is_null(), "oidc_claims should be present");
    assert_eq!(
        oidc["repository"].as_str(),
        Some(format!("{org}/test-repo").as_str())
    );
    assert_eq!(oidc["repository_owner"].as_str(), Some(org.as_str()));
    assert_eq!(oidc["actor"].as_str(), Some("test-user"));
    assert_eq!(oidc["sha"].as_str(), Some("abc123def456"));
    assert_eq!(oidc["ref"].as_str(), Some("refs/heads/main"));
    assert_eq!(oidc["ref_type"].as_str(), Some("branch"));
    assert_eq!(oidc["workflow"].as_str(), Some("CI Tests"));
    assert_eq!(oidc["event_name"].as_str(), Some("push"));
    assert_eq!(oidc["run_id"].as_str(), Some("12345"));
    assert_eq!(oidc["run_number"].as_str(), Some("42"));
    assert_eq!(oidc["run_attempt"].as_str(), Some("1"));

    // Audit fields
    assert_eq!(oidc["resolved_role"].as_str(), Some("contributor"));
    assert_eq!(oidc["api_path"].as_str(), Some("/api/v1/reports"));
    assert_eq!(oidc["http_method"].as_str(), Some("POST"));

    // sub IS persisted (safe claim)
    assert!(oidc["sub"].is_string(), "sub should be persisted");

    // Excluded claims should NOT be present
    assert!(oidc.get("jti").is_none() || oidc["jti"].is_null());
    assert!(oidc.get("iss").is_none() || oidc["iss"].is_null());
    assert!(oidc.get("aud").is_none() || oidc["aud"].is_null());
    assert!(oidc.get("exp").is_none() || oidc["exp"].is_null());
}

/// (13) OIDC upload with explicit metadata → both coexist independently.
#[actix_rt::test]
async fn test_oidc_claims_coexist_with_metadata() {
    let org = unique_org("coexist");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/oidc-repo"));
    let token = mock.issue_token(&claims, &key);

    // Upload with explicit metadata that differs from OIDC claims
    let explicit_meta = serde_json::json!({
        "repository": "explicit-org/explicit-repo",
        "ref": "refs/heads/feature-branch",
        "sha": "explicit-sha-999",
    });
    let (status, upload_body) = upload_report_with_token(&app, &token, Some(explicit_meta)).await;
    assert_eq!(status, 201);

    let report_id = upload_body["report_id"].as_str().unwrap();
    let (status, detail) = get_report_with_token(&app, &token, report_id).await;
    assert_eq!(status, 200);

    // github_metadata should contain explicit values (untouched)
    let meta = &detail["github_metadata"];
    assert_eq!(
        meta["repository"].as_str(),
        Some("explicit-org/explicit-repo")
    );
    assert_eq!(meta["ref"].as_str(), Some("refs/heads/feature-branch"));
    assert_eq!(meta["sha"].as_str(), Some("explicit-sha-999"));

    // oidc_claims should contain token-derived values (separate)
    let oidc = &detail["oidc_claims"];
    assert_eq!(
        oidc["repository"].as_str(),
        Some(format!("{org}/oidc-repo").as_str())
    );
    assert_eq!(oidc["ref"].as_str(), Some("refs/heads/main"));
    assert_eq!(oidc["sha"].as_str(), Some("abc123def456"));
}

/// (14) API-key upload → no OIDC claims record.
#[actix_rt::test]
async fn test_api_key_upload_no_oidc_claims() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    // Upload with admin key (not OIDC)
    let req = actix_web::test::TestRequest::post()
        .uri("/api/v1/reports")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({
            "expected_jobs": 1,
            "framework": "playwright",
        }))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(resp.status().as_u16(), 201);
    let upload_body: serde_json::Value = actix_web::test::read_body_json(resp).await;
    let report_id = upload_body["report_id"].as_str().unwrap();

    // Get detail — oidc_claims should be absent
    let req = actix_web::test::TestRequest::get()
        .uri(&format!("/api/v1/reports/{}", report_id))
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    let detail: serde_json::Value = actix_web::test::read_body_json(resp).await;

    assert!(
        detail.get("oidc_claims").is_none() || detail["oidc_claims"].is_null(),
        "API-key upload should have no oidc_claims"
    );
}
