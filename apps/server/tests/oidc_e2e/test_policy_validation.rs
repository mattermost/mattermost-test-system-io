//! E2E tests: OIDC policy validation scenarios.

use super::test_helpers::*;

/// (16) Create policy with admin role → 400 error.
#[actix_rt::test]
async fn test_admin_role_policy_rejected() {
    let org = unique_org("polval");
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    // Try to create policy with admin role — should be rejected
    let req = actix_web::test::TestRequest::post()
        .uri("/api/v1/auth/oidc-policies")
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({
            "repository_pattern": format!("{org}/any-repo"),
            "role": "admin",
        }))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(
        resp.status().as_u16(),
        400,
        "Admin role should be rejected in policy creation"
    );

    let body: serde_json::Value = actix_web::test::read_body_json(resp).await;
    assert!(
        body["message"]
            .as_str()
            .unwrap_or("")
            .contains("contributor"),
        "Error should mention allowed roles: {:?}",
        body
    );
}

/// Update policy to admin role → 400 error.
#[actix_rt::test]
async fn test_admin_role_policy_update_rejected() {
    let org = unique_org("polupd");
    let mock = get_mock().await;
    let pool = create_test_pool().await;
    let app = create_test_app(&pool, &mock.issuer_url).await;

    // Create a valid policy first
    let policy = create_test_policy(&app, &format!("{org}/*"), "contributor").await;
    let policy_id = policy["id"].as_str().unwrap();

    // Try to update to admin role — should be rejected
    let req = actix_web::test::TestRequest::put()
        .uri(&format!("/api/v1/auth/oidc-policies/{}", policy_id))
        .insert_header(("X-Admin-Key", TEST_ADMIN_KEY))
        .set_json(serde_json::json!({
            "role": "admin",
        }))
        .to_request();

    let resp = actix_web::test::call_service(&app, req).await;
    assert_eq!(
        resp.status().as_u16(),
        400,
        "Admin role update should be rejected"
    );
}
