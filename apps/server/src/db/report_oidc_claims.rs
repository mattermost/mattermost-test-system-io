//! Database operations for report OIDC claims.

use sea_orm::*;
use uuid::Uuid;

use crate::error::AppResult;
use crate::models::report_oidc_claim::ReportOidcClaimsResponse;

/// Insert a new report OIDC claims record.
pub async fn insert(
    db: &DatabaseConnection,
    model: crate::entity::report_oidc_claim::ActiveModel,
) -> AppResult<()> {
    crate::entity::report_oidc_claim::Entity::insert(model)
        .exec(db)
        .await?;
    Ok(())
}

/// Find OIDC claims for a single report.
pub async fn find_by_report_id(
    db: &DatabaseConnection,
    report_id: Uuid,
) -> AppResult<Option<ReportOidcClaimsResponse>> {
    let result = crate::entity::report_oidc_claim::Entity::find()
        .filter(crate::entity::report_oidc_claim::Column::ReportId.eq(report_id))
        .one(db)
        .await?;

    Ok(result.map(ReportOidcClaimsResponse::from_entity))
}

/// Find OIDC claims for multiple reports (batch query).
pub async fn find_by_report_ids(
    db: &DatabaseConnection,
    report_ids: &[Uuid],
) -> AppResult<Vec<(Uuid, ReportOidcClaimsResponse)>> {
    if report_ids.is_empty() {
        return Ok(vec![]);
    }

    let results = crate::entity::report_oidc_claim::Entity::find()
        .filter(crate::entity::report_oidc_claim::Column::ReportId.is_in(report_ids.to_vec()))
        .all(db)
        .await?;

    Ok(results
        .into_iter()
        .map(|m| {
            let report_id = m.report_id;
            (report_id, ReportOidcClaimsResponse::from_entity(m))
        })
        .collect())
}
