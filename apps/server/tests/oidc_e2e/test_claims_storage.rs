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
    let (status, upload_body) =
        upload_report_with_token(&app, &token, &format!("{org}/test-repo")).await;
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

/// (13) OIDC upload → typed columns populated, OIDC claims stored separately.
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

    // Upload with repository from OIDC claims
    let (status, upload_body) =
        upload_report_with_token(&app, &token, &format!("{org}/oidc-repo")).await;
    assert_eq!(status, 201);

    let report_id = upload_body["report_id"].as_str().unwrap();
    let (status, detail) = get_report_with_token(&app, &token, report_id).await;
    assert_eq!(status, 200);

    // Typed columns should contain the values we passed in the request body
    assert_eq!(
        detail["repository"].as_str(),
        Some(format!("{org}/oidc-repo").as_str()),
        "repository typed column should match request"
    );
    assert_eq!(
        detail["branch"].as_str(),
        Some("main"),
        "branch typed column should match request"
    );
    assert!(
        detail["commit"].is_string() && !detail["commit"].as_str().unwrap().is_empty(),
        "commit typed column should be populated"
    );

    // github_metadata should NOT be present (removed JSONB)
    assert!(
        detail.get("github_metadata").is_none() || detail["github_metadata"].is_null(),
        "github_metadata JSONB should not be in response"
    );

    // oidc_claims should contain token-derived values (separate table)
    let oidc = &detail["oidc_claims"];
    assert!(!oidc.is_null(), "oidc_claims should be present");
    assert_eq!(
        oidc["repository"].as_str(),
        Some(format!("{org}/oidc-repo").as_str())
    );
    assert_eq!(oidc["ref"].as_str(), Some("refs/heads/main"));
    assert_eq!(oidc["sha"].as_str(), Some("abc123def456"));
}

/// (14) API-key upload → typed columns populated, no OIDC claims record.
#[actix_rt::test]
async fn test_api_key_upload_no_oidc_claims() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    // Upload with admin key (not OIDC) — must include required fields
    let req = actix_web::test::TestRequest::post()
        .uri("/api/v1/reports")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({
            "expected_jobs": 1,
            "framework": "playwright",
            "repository": "test-org/no-oidc-repo",
            "branch": "main",
            "commit": "deadbeef1234567"
        }))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(resp.status().as_u16(), 201);
    let upload_body: serde_json::Value = actix_web::test::read_body_json(resp).await;
    let report_id = upload_body["report_id"].as_str().unwrap();

    // Get detail
    let req = actix_web::test::TestRequest::get()
        .uri(&format!("/api/v1/reports/{}", report_id))
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    let detail: serde_json::Value = actix_web::test::read_body_json(resp).await;

    // oidc_claims should be absent
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
        detail["branch"].as_str(),
        Some("main"),
        "branch typed column should match request"
    );
    assert_eq!(
        detail["commit"].as_str(),
        Some("deadbeef1234567"),
        "commit typed column should match request"
    );

    // github_metadata JSONB should NOT be in response
    assert!(
        detail.get("github_metadata").is_none() || detail["github_metadata"].is_null(),
        "github_metadata JSONB should not be in response"
    );
}

/// (15) Upload with run_id → run_id typed column populated in response.
#[actix_rt::test]
async fn test_run_id_stored_in_typed_column() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let req = actix_web::test::TestRequest::post()
        .uri("/api/v1/reports")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({
            "expected_jobs": 1,
            "framework": "playwright",
            "repository": "test-org/run-id-repo",
            "branch": "feature-branch",
            "commit": "aabbccdd11223344",
            "run_id": "99887766",
            "pr_number": 42
        }))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(resp.status().as_u16(), 201);
    let upload_body: serde_json::Value = actix_web::test::read_body_json(resp).await;
    let report_id = upload_body["report_id"].as_str().unwrap();

    // Get detail
    let req = actix_web::test::TestRequest::get()
        .uri(&format!("/api/v1/reports/{}", report_id))
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
        detail["branch"].as_str(),
        Some("feature-branch"),
        "branch should match"
    );
    assert_eq!(
        detail["commit"].as_str(),
        Some("aabbccdd11223344"),
        "commit should match"
    );
    assert_eq!(
        detail["run_id"].as_str(),
        Some("99887766"),
        "run_id should match"
    );
    assert_eq!(
        detail["pr_number"].as_i64(),
        Some(42),
        "pr_number should match"
    );
}

/// (16) List reports → typed columns present in summary, no github_metadata JSONB.
#[actix_rt::test]
async fn test_list_reports_typed_columns() {
    let org = unique_org("list-typed");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims =
        TestOidcClaims::default_for(&mock.issuer_url).with_repository(&format!("{org}/list-repo"));
    let token = mock.issue_token(&claims, &key);

    // Upload a report
    let (status, _) = upload_report_with_token(&app, &token, &format!("{org}/list-repo")).await;
    assert_eq!(status, 201);

    // List reports — filter by repository to find our report
    let req = actix_web::test::TestRequest::get()
        .uri(&format!(
            "/api/v1/reports?repository={org}/list-repo&limit=1"
        ))
        .insert_header(("Authorization", format!("Bearer {}", token)))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(resp.status().as_u16(), 200);
    let body: serde_json::Value = actix_web::test::read_body_json(resp).await;

    let reports = body["reports"].as_array().expect("reports should be array");
    assert!(!reports.is_empty(), "should find at least one report");

    let report = &reports[0];

    // Typed columns should be present
    assert_eq!(
        report["repository"].as_str(),
        Some(format!("{org}/list-repo").as_str()),
        "repository should be present in list summary"
    );
    assert!(
        report["branch"].is_string(),
        "branch should be present in list summary"
    );
    assert!(
        report["commit"].is_string(),
        "commit should be present in list summary"
    );
    assert!(
        report["run_id"].is_string(),
        "run_id should be present in list summary"
    );

    // github_metadata JSONB should NOT be present
    assert!(
        report.get("github_metadata").is_none() || report["github_metadata"].is_null(),
        "github_metadata JSONB should not be in list response"
    );

    // OIDC claims should be present (uploaded via OIDC)
    assert!(
        !report["oidc_claims"].is_null(),
        "oidc_claims should be present for OIDC uploads"
    );
}

/// (17) Report detail → typed columns present, no github_metadata JSONB.
#[actix_rt::test]
async fn test_report_detail_typed_columns_no_jsonb() {
    let org = unique_org("detail-typed");
    let key = get_primary_key().await;
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    create_test_policy(&app, &format!("{org}/*"), "contributor").await;

    let claims = TestOidcClaims::default_for(&mock.issuer_url)
        .with_repository(&format!("{org}/detail-repo"));
    let token = mock.issue_token(&claims, &key);

    let (status, upload_body) =
        upload_report_with_token(&app, &token, &format!("{org}/detail-repo")).await;
    assert_eq!(status, 201);

    let report_id = upload_body["report_id"].as_str().unwrap();
    let (status, detail) = get_report_with_token(&app, &token, report_id).await;
    assert_eq!(status, 200);

    // Verify all typed columns are present
    assert_eq!(detail["id"].as_str(), Some(report_id));
    assert_eq!(
        detail["repository"].as_str(),
        Some(format!("{org}/detail-repo").as_str())
    );
    assert_eq!(detail["branch"].as_str(), Some("main"));
    assert!(
        detail["commit"].is_string() && detail["commit"].as_str().unwrap().len() >= 7,
        "commit should be a non-empty string"
    );
    assert!(
        detail["run_id"].is_string(),
        "run_id should be present (defaults to empty string)"
    );
    assert!(detail["framework"].is_string());
    assert!(detail["status"].is_string());
    assert!(detail["expected_jobs"].is_number());
    assert!(detail["created_at"].is_string());
    assert!(detail["updated_at"].is_string());
    assert!(detail["jobs"].is_array());

    // github_metadata JSONB should NOT be present
    assert!(
        detail.get("github_metadata").is_none() || detail["github_metadata"].is_null(),
        "github_metadata JSONB should not be in detail response"
    );
}
