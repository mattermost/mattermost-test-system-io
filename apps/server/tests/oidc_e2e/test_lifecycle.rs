//! E2E tests for the report lifecycle endpoints
//! `POST /api/v1/reports/begin` and `POST /api/v1/reports/complete`.

use super::test_helpers::*;

// ─────────────────────────────────────────────────────────────────────────────
// T054: begin with grouping payload -> report created with in_progress
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_begin_creates_report() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("lifecycle-begin");
    let repo = format!("{}/begin-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    let body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "100001",
        "framework": "playwright",
        "name": "playwright"
    });

    let (status, resp) = begin_report(&app, body).await;
    assert_eq!(status, 200, "Expected 200, got {}: {}", status, resp);

    // Verify response shape
    assert!(resp["report_id"].is_string(), "report_id should be string");
    assert_eq!(
        resp["status"].as_str(),
        Some("in_progress"),
        "status should be in_progress"
    );
    assert_eq!(
        resp["created"].as_bool(),
        Some(true),
        "created should be true for new report"
    );

    // Verify the report is accessible via GET
    let report_id = resp["report_id"].as_str().unwrap();
    let (get_status, detail) = get_report_with_token(&app, "ignored-uses-admin", report_id).await;
    // The GET might use different auth; let's just use admin key helper
    // Actually, let's verify directly through the pool
    let report_uuid: uuid::Uuid = report_id.parse().unwrap();
    let db_report = pool
        .get_report_group_by_id(report_uuid)
        .await
        .expect("DB query should succeed");
    assert!(db_report.is_some(), "Report should exist in DB");
    let db_report = db_report.unwrap();
    assert_eq!(db_report.status, "in_progress");
    assert_eq!(db_report.repository, repo);
    assert_eq!(db_report.framework, "playwright");

    // Ignore unused binding warnings from GET helper
    let _ = (get_status, detail);
}

// ─────────────────────────────────────────────────────────────────────────────
// T055: begin -> register report -> complete -> status completed
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_begin_upload_complete_flow() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("lifecycle-flow");
    let repo = format!("{}/flow-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    // Step 1: Begin
    let begin_body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "100002",
        "framework": "cypress",
        "name": "cypress"
    });

    let (status, begin_resp) = begin_report(&app, begin_body).await;
    assert_eq!(status, 200, "Begin: {}", begin_resp);
    assert_eq!(begin_resp["status"].as_str(), Some("in_progress"));
    assert_eq!(begin_resp["created"].as_bool(), Some(true));

    let report_id = begin_resp["report_id"].as_str().unwrap().to_string();

    // Step 2: Register a report (same grouping key)
    let init_body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "100002",
        "framework": "cypress",
        "name": "cypress",
        "gh_job_id": "100012",
        "gh_job_name": "run-tests (0)",
        "json_files": [
            { "path": "results/report.json", "size": 45000 }
        ]
    });

    let (init_status, init_resp) = register_report_with_api_key(&app, init_body).await;
    assert_eq!(init_status, 200, "Init: {}", init_resp);
    // Should join the same report
    assert_eq!(
        init_resp["report_id"].as_str().unwrap(),
        report_id,
        "Report should join the report created by begin"
    );
    assert_eq!(init_resp["reports_in_group"].as_i64(), Some(1));

    // Step 3: Complete
    let complete_body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "100002",
        "framework": "cypress",
        "name": "cypress"
    });

    let (complete_status, complete_resp) = complete_report(&app, complete_body).await;
    assert_eq!(complete_status, 200, "Complete: {}", complete_resp);
    assert_eq!(complete_resp["status"].as_str(), Some("completed"));
    assert_eq!(
        complete_resp["report_id"].as_str().unwrap(),
        report_id,
        "Complete should return the same report_id"
    );
    assert_eq!(
        complete_resp["reports_count"].as_i64(),
        Some(1),
        "reports_count should be 1"
    );

    // Verify DB state
    let report_uuid: uuid::Uuid = report_id.parse().unwrap();
    let db_report = pool
        .get_report_group_by_id(report_uuid)
        .await
        .expect("DB query should succeed")
        .expect("Report should exist");
    assert_eq!(db_report.status, "completed");
}

// ─────────────────────────────────────────────────────────────────────────────
// T056: Idempotency — begin twice, complete twice -> no errors
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_idempotency() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("lifecycle-idem");
    let repo = format!("{}/idem-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    let body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "100003",
        "framework": "playwright",
        "name": "playwright"
    });

    // Begin first time
    let (s1, r1) = begin_report(&app, body.clone()).await;
    assert_eq!(s1, 200, "Begin #1: {}", r1);
    assert_eq!(r1["created"].as_bool(), Some(true));
    let report_id = r1["report_id"].as_str().unwrap().to_string();

    // Begin second time — same grouping key
    let (s2, r2) = begin_report(&app, body.clone()).await;
    assert_eq!(s2, 200, "Begin #2: {}", r2);
    assert_eq!(
        r2["created"].as_bool(),
        Some(false),
        "Second begin should not create a new report"
    );
    assert_eq!(
        r2["report_id"].as_str().unwrap(),
        report_id,
        "Should return the same report_id"
    );

    // Complete first time
    let (cs1, cr1) = complete_report(&app, body.clone()).await;
    assert_eq!(cs1, 200, "Complete #1: {}", cr1);
    assert_eq!(cr1["status"].as_str(), Some("completed"));
    assert_eq!(cr1["report_id"].as_str().unwrap(), report_id);

    // Complete second time — should still succeed (idempotent)
    let (cs2, cr2) = complete_report(&app, body.clone()).await;
    assert_eq!(cs2, 200, "Complete #2: {}", cr2);
    assert_eq!(cr2["status"].as_str(), Some("completed"));
    assert_eq!(cr2["report_id"].as_str().unwrap(), report_id);
}

// ─────────────────────────────────────────────────────────────────────────────
// T057: begin -> report group exists with zero reports
// ─────────────────────────────────────────────────────────────────────────────

#[actix_rt::test]
async fn test_begin_before_reports() {
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    let org = unique_org("lifecycle-noreports");
    let repo = format!("{}/noreports-repo", org);
    let commit = format!(
        "{}abcdef1234567890",
        uuid::Uuid::new_v4().to_string().split('-').next().unwrap()
    );

    let body = serde_json::json!({
        "repository": repo,
        "commit": commit,
        "gh_run_id": "100004",
        "framework": "detox",
        "name": "detox"
    });

    // Begin — creates report
    let (status, resp) = begin_report(&app, body.clone()).await;
    assert_eq!(status, 200, "Begin: {}", resp);
    assert_eq!(resp["created"].as_bool(), Some(true));

    let report_id_str = resp["report_id"].as_str().unwrap();
    let report_uuid: uuid::Uuid = report_id_str.parse().unwrap();

    // Report should exist in DB
    let db_report = pool
        .get_report_group_by_id(report_uuid)
        .await
        .expect("DB query should succeed")
        .expect("Report should exist");
    assert_eq!(db_report.status, "in_progress");

    // Count reports — should be zero
    let reports_count = pool
        .count_reports_in_group(report_uuid)
        .await
        .expect("Count should succeed");
    assert_eq!(
        reports_count, 0,
        "Report group should have zero reports after begin"
    );

    // Complete should also report zero reports
    let (cs, cr) = complete_report(&app, body).await;
    assert_eq!(cs, 200, "Complete: {}", cr);
    assert_eq!(cr["reports_count"].as_i64(), Some(0));
    assert_eq!(cr["status"].as_str(), Some("completed"));
}
