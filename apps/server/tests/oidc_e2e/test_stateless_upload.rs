//! E2E tests for the stateless upload endpoint `POST /api/v1/reports/register`.

use super::mock_oidc_provider::TestOidcClaims;
use super::test_helpers::*;

// ─────────────────────────────────────────────────────────────────────────────
// T037: API key auth — single register auto-creates report group + report
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_api_key_register_creates_report() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("stateless-api");
    let repo = format!("{}/my-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    let body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200001",
        "framework": "playwright",
        "name": "playwright",
        "gh_job_id": "300001",
        "gh_job_name": "run-tests (0)",
        "json_files": [
            { "path": "results/report.json", "size": 45000 }
        ],
        "screenshots": [
            { "path": "test-name/screenshot1.png", "size": 80000 }
        ]
    });

    let (status, resp) = register_report_with_api_key(&app, body).await;
    assert_eq!(status, 200, "Expected 200, got {}: {}", status, resp);

    // Verify response shape
    assert!(resp["report_id"].is_string(), "report_id should be string");
    assert!(resp["upload_id"].is_string(), "upload_id should be string");
    assert_eq!(resp["is_existing"].as_bool(), Some(false));
    assert_eq!(resp["report_status"].as_str(), Some("in_progress"));
    assert_eq!(resp["reports_in_group"].as_i64(), Some(1));

    // Accepted files
    assert_eq!(resp["accepted_json_files"].as_array().unwrap().len(), 1);
    assert_eq!(resp["accepted_screenshots"].as_array().unwrap().len(), 1);

    // Rejected files should be empty
    assert!(resp["rejected_json_files"].as_array().unwrap().is_empty());
    assert!(resp["rejected_screenshots"].as_array().unwrap().is_empty());

    // Verify accepted files have s3_key
    let json_file = &resp["accepted_json_files"][0];
    assert_eq!(json_file["path"].as_str(), Some("results/report.json"));
    assert!(
        json_file["s3_key"].as_str().unwrap().contains("json/"),
        "JSON s3_key should contain json/"
    );

    let screenshot = &resp["accepted_screenshots"][0];
    assert_eq!(
        screenshot["path"].as_str(),
        Some("test-name/screenshot1.png")
    );
    assert_eq!(screenshot["test_name"].as_str(), Some("test-name"));
}

// ─────────────────────────────────────────────────────────────────────────────
// T038: OIDC auth — per-report claims stored with check_run_id
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_oidc_auth_stores_per_report_claims() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("stateless-oidc");
    let repo = format!("{}/oidc-repo", org);

    // Create OIDC policy for this org
    create_test_policy(&app, &format!("{}/*", org), "contributor").await;

    // Issue token with a specific check_run_id
    let key = get_primary_key().await;
    let claims = TestOidcClaims::default_for(&mock.issuer_url)
        .with_repository(&repo)
        .with_check_run_id("check-run-42");

    let token = mock.issue_token(&claims, &key);

    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    let body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200002",
        "framework": "cypress",
        "name": "cypress",
        "gh_job_id": "300002",
        "gh_job_name": "test-report-300002",
        "json_files": [
            { "path": "results.json", "size": 1000 }
        ]
    });

    let (status, resp) = register_report_with_token(&app, &token, body).await;
    assert_eq!(status, 200, "Expected 200, got {}: {}", status, resp);
    assert_eq!(resp["is_existing"].as_bool(), Some(false));

    // Verify per-report OIDC claims are stored in the DB
    let report_id_str = resp["upload_id"].as_str().unwrap();
    let report_id: uuid::Uuid = report_id_str.parse().unwrap();
    let stored_claims =
        mattermost_tsio_lib::db::oidc_claims::find_by_upload_id(pool.connection(), report_id)
            .await
            .expect("Failed to query OIDC claims");

    assert!(
        stored_claims.is_some(),
        "Per-report OIDC claims should be stored"
    );
    let claims_dto = stored_claims.unwrap();
    assert_eq!(
        claims_dto.check_run_id.as_deref(),
        Some("check-run-42"),
        "check_run_id should be persisted"
    );
    assert_eq!(
        claims_dto.api_path, "/reports/register",
        "api_path audit field should be /reports/register"
    );
    assert_eq!(
        claims_dto.http_method, "POST",
        "http_method audit field should be POST"
    );
}

// ─────────────────────────────────────────────────────────────────────────────
// T039: Idempotency — same gh_job_id returns existing job
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_idempotency_same_gh_job_id() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("stateless-idem");
    let repo = format!("{}/idempotent-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    let body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200003",
        "framework": "playwright",
        "name": "playwright",
        "gh_job_id": "300003",
        "gh_job_name": "test-job",
    });

    // First call — creates
    let (status1, resp1) = register_report_with_api_key(&app, body.clone()).await;
    assert_eq!(status1, 200, "First call: {}", resp1);
    assert_eq!(resp1["is_existing"].as_bool(), Some(false));

    let upload_id_1 = resp1["upload_id"].as_str().unwrap().to_string();
    let report_id_1 = resp1["report_id"].as_str().unwrap().to_string();

    // Second call with same gh_job_id — returns existing
    let (status2, resp2) = register_report_with_api_key(&app, body.clone()).await;
    assert_eq!(status2, 200, "Second call: {}", resp2);
    assert_eq!(resp2["is_existing"].as_bool(), Some(true));
    assert_eq!(resp2["upload_id"].as_str().unwrap(), upload_id_1);
    assert_eq!(resp2["report_id"].as_str().unwrap(), report_id_1);
}

// ─────────────────────────────────────────────────────────────────────────────
// T040: Multiple reports with same grouping key -> 1 report group
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_multiple_reports_same_grouping_key_one_report_group() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("stateless-multi");
    let repo = format!("{}/multi-report-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    // Report 1
    let body1 = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200004",
        "framework": "playwright",
        "name": "playwright",
        "gh_job_id": "300004",
        "gh_job_name": "run-tests (0)",
    });
    let (s1, r1) = register_report_with_api_key(&app, body1).await;
    assert_eq!(s1, 200, "Report 1: {}", r1);
    let report_id = r1["report_id"].as_str().unwrap().to_string();

    // Job 2 — same grouping key, different gh_job_id
    let body2 = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200004",
        "framework": "playwright",
        "name": "playwright",
        "gh_job_id": "300005",
        "gh_job_name": "run-tests (1)",
    });
    let (s2, r2) = register_report_with_api_key(&app, body2).await;
    assert_eq!(s2, 200, "Report 2: {}", r2);

    // Same report group
    assert_eq!(
        r2["report_id"].as_str().unwrap(),
        report_id,
        "Both reports should belong to the same report group"
    );

    // Different reports
    assert_ne!(
        r1["upload_id"].as_str().unwrap(),
        r2["upload_id"].as_str().unwrap(),
        "Reports should have different IDs"
    );

    // reports_in_group should be 2
    assert_eq!(r2["reports_in_group"].as_i64(), Some(2));
}

// ─────────────────────────────────────────────────────────────────────────────
// T041: Different names -> separate reports
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_different_names_separate_reports() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("stateless-fw");
    let repo = format!("{}/fw-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    // Playwright report
    let body_pw = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200005",
        "framework": "playwright",
        "name": "playwright-enterprise",
        "gh_job_id": "300006",
        "gh_job_name": "test-report-300006",
    });
    let (s1, r1) = register_report_with_api_key(&app, body_pw).await;
    assert_eq!(s1, 200, "PW report: {}", r1);

    // Cypress report — same grouping key except name
    let body_cy = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200005",
        "framework": "cypress",
        "name": "cypress-team",
        "gh_job_id": "300007",
        "gh_job_name": "test-report-300007",
    });
    let (s2, r2) = register_report_with_api_key(&app, body_cy).await;
    assert_eq!(s2, 200, "CY report: {}", r2);

    // Different reports
    assert_ne!(
        r1["report_id"].as_str().unwrap(),
        r2["report_id"].as_str().unwrap(),
        "Different names should produce separate reports"
    );
}

// ─────────────────────────────────────────────────────────────────────────────
// T042: Empty gh_run_id -> standalone report
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_missing_required_fields_rejected() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    // Missing gh_run_id — should fail deserialization or validation
    let body = serde_json::json!({
        "repository": "test-org/test-repo",
        "commit": commit,
        "framework": "detox",
        "name": "detox",
        "gh_job_id": "300008",
        "gh_job_name": "test",
    });

    let (status, _resp) = register_report_with_api_key(&app, body).await;
    assert!(
        status == 400 || status == 422,
        "Missing gh_run_id should be rejected, got {}",
        status
    );
}

// ─────────────────────────────────────────────────────────────────────────────
// T051: Retest job with different gh_job_name but same grouping key joins
//       the same report
// ─────────────────────────────────────────────────────────────────────────────

/// Retest job with different gh_job_name but same grouping key joins the same report.
#[actix_rt::test]
async fn test_retest_joins_same_report() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("stateless-retest");
    let repo = format!("{}/retest-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    // Report 1 — original test run
    let body1 = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200006",
        "framework": "playwright",
        "name": "playwright",
        "gh_job_id": "300009",
        "gh_job_name": "run-tests (0)",
        "json_files": [
            { "path": "results/shard0.json", "size": 30000 }
        ]
    });
    let (s1, r1) = register_report_with_api_key(&app, body1).await;
    assert_eq!(s1, 200, "Report 1 (original): {}", r1);
    assert_eq!(r1["is_existing"].as_bool(), Some(false));

    let report_id = r1["report_id"].as_str().unwrap().to_string();
    let report_id_1 = r1["upload_id"].as_str().unwrap().to_string();

    // Job 2 — retest with different gh_job_name but SAME grouping key
    let body2 = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200006",
        "framework": "playwright",
        "name": "playwright",
        "gh_job_id": "300010",
        "gh_job_name": "run-failed-tests",
        "json_files": [
            { "path": "results/retry.json", "size": 15000 }
        ]
    });
    let (s2, r2) = register_report_with_api_key(&app, body2).await;
    assert_eq!(s2, 200, "Report 2 (retest): {}", r2);

    // Both reports should be in the same report group
    assert_eq!(
        r2["report_id"].as_str().unwrap(),
        report_id,
        "Retest report should join the same report group as the original"
    );

    // Reports should have different IDs
    let report_id_2 = r2["upload_id"].as_str().unwrap().to_string();
    assert_ne!(
        report_id_1, report_id_2,
        "Original and retest reports should have different report IDs"
    );

    // reports_in_group should be 2
    assert_eq!(
        r2["reports_in_group"].as_i64(),
        Some(2),
        "Report group should contain 2 reports after retest"
    );
}

// ─────────────────────────────────────────────────────────────────────────────
// T043: OIDC claims NOT in standard GET /reports/{id} response
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_oidc_claims_not_in_report_response() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("stateless-noclm");
    let repo = format!("{}/no-claims-repo", org);

    // Create OIDC policy
    create_test_policy(&app, &format!("{}/*", org), "contributor").await;

    let key = get_primary_key().await;
    let claims = TestOidcClaims::default_for(&mock.issuer_url).with_repository(&repo);
    let token = mock.issue_token(&claims, &key);

    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    let body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200007",
        "framework": "playwright",
        "name": "playwright",
        "gh_job_id": "300011",
        "gh_job_name": "test-report-300011",
    });

    let (status, resp) = register_report_with_token(&app, &token, body).await;
    assert_eq!(status, 200, "Init: {}", resp);

    let report_id = resp["report_id"].as_str().unwrap();

    // GET the report and verify no oidc_claims field
    let (get_status, detail) = get_report_with_token(&app, &token, report_id).await;
    assert_eq!(get_status, 200, "GET report: {}", detail);

    assert!(
        detail.get("oidc_claims").is_none() || detail["oidc_claims"].is_null(),
        "oidc_claims should NOT appear in GET /reports/{{id}} response"
    );
}

// ─────────────────────────────────────────────────────────────────────────────
// T044: name is required — register without name → 400
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_register_without_name_rejected() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    // Missing "name" field
    let body = serde_json::json!({
        "repository": "test-org/test-repo",
        "commit": commit,
        "framework": "playwright",
        "gh_job_id": "300012",
    });

    let (status, _resp) = register_report_with_api_key(&app, body).await;
    assert!(
        status == 400 || status == 422,
        "Register without name should be rejected, got {}",
        status
    );
}

// ─────────────────────────────────────────────────────────────────────────────
// T045: gh_pr_number required for pull_request events
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_pr_event_without_gh_pr_number_rejected() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("stateless-pr-check");
    let repo = format!("{}/pr-repo", org);

    // Create OIDC policy
    create_test_policy(&app, &format!("{}/*", org), "contributor").await;

    // Issue token with event_name = "pull_request" but no gh_pr_number in body
    let key = get_primary_key().await;
    let claims = TestOidcClaims::default_for(&mock.issuer_url)
        .with_repository(&repo)
        .with_event_name("pull_request");

    let token = mock.issue_token(&claims, &key);

    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    // Register WITHOUT gh_pr_number — should fail for PR events
    let body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "200008",
        "framework": "playwright",
        "name": "playwright",
        "gh_job_id": "300013",
        "gh_job_name": "test-report-300013",
    });

    let (status, resp) = register_report_with_token(&app, &token, body).await;
    assert_eq!(
        status, 400,
        "PR event without gh_pr_number should be rejected: {}",
        resp
    );
}

// ─────────────────────────────────────────────────────────────────────────────
// T046: Old endpoints removed — /jobs/init and POST /reports return 404/405
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_old_endpoints_removed() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    // POST /api/v1/jobs/init should not exist
    let req = actix_web::test::TestRequest::post()
        .uri("/api/v1/jobs/init")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({"repository": "test", "commit": "abc", "framework": "playwright", "name": "test"}))
        .to_request();
    let resp = actix_web::test::call_service(&app, req).await;
    assert!(
        resp.status().as_u16() == 404 || resp.status().as_u16() == 405,
        "POST /jobs/init should be gone, got {}",
        resp.status().as_u16()
    );

    // POST /api/v1/reports (old register) should not accept POST
    let req = actix_web::test::TestRequest::post()
        .uri("/api/v1/reports")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({"framework": "playwright", "name": "test", "repository": "test", "branch": "main", "commit": "abc"}))
        .to_request();
    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(
        resp.status().as_u16(),
        405,
        "POST /reports (old register) should return 405 Method Not Allowed"
    );
}
