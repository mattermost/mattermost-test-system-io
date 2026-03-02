//! Database queries for per-upload OIDC claims.

use sea_orm::{ActiveModelTrait, ColumnTrait, DatabaseConnection, EntityTrait, QueryFilter};
use uuid::Uuid;

use crate::entity::oidc_claim::{self, Entity as OidcClaim};
use crate::error::{AppError, AppResult};
use crate::models::oidc_claim::OidcClaimsResponse;

/// Insert a single OIDC claims row.
#[allow(dead_code)]
pub async fn insert_oidc_claims(
    conn: &DatabaseConnection,
    model: oidc_claim::ActiveModel,
) -> AppResult<()> {
    model
        .insert(conn)
        .await
        .map_err(|e| AppError::Database(format!("Failed to insert OIDC claims: {}", e)))?;

    Ok(())
}

/// Find OIDC claims for a single upload, returned as a DTO.
#[allow(dead_code)]
pub async fn find_by_upload_id(
    conn: &DatabaseConnection,
    upload_id: Uuid,
) -> AppResult<Option<OidcClaimsResponse>> {
    let result = OidcClaim::find()
        .filter(oidc_claim::Column::UploadId.eq(upload_id))
        .one(conn)
        .await
        .map_err(|e| {
            AppError::Database(format!(
                "Failed to find OIDC claims for upload {}: {}",
                upload_id, e
            ))
        })?;

    Ok(result.map(OidcClaimsResponse::from_entity))
}

/// Batch find OIDC claims for multiple uploads.
///
/// Returns a `Vec` of `(upload_id, DTO)` tuples for every upload that has a
/// claims row.  Uploads without claims are silently omitted.
#[allow(dead_code)]
pub async fn find_by_upload_ids(
    conn: &DatabaseConnection,
    upload_ids: &[Uuid],
) -> AppResult<Vec<(Uuid, OidcClaimsResponse)>> {
    if upload_ids.is_empty() {
        return Ok(Vec::new());
    }

    let rows = OidcClaim::find()
        .filter(oidc_claim::Column::UploadId.is_in(upload_ids.to_vec()))
        .all(conn)
        .await
        .map_err(|e| AppError::Database(format!("Failed to batch-find OIDC claims: {}", e)))?;

    let results = rows
        .into_iter()
        .map(|m| {
            let uid = m.upload_id;
            (uid, OidcClaimsResponse::from_entity(m))
        })
        .collect();

    Ok(results)
}
