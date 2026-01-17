//! Database operations for tracking individual file uploads using SeaORM.
//!
//! Files are registered during initialization and marked as uploaded during transfer.

use chrono::Utc;
use sea_orm::prelude::Expr;
use sea_orm::*;
use uuid::Uuid;

use crate::error::{AppError, AppResult};

/// File upload status information.
#[derive(Debug, Clone)]
pub struct FileStatus {
    pub is_uploaded: bool,
}

/// Register files for a report during initialization.
/// Returns the IDs of the created file records.
pub async fn register_files(
    db: &DatabaseConnection,
    report_id: Uuid,
    filenames: &[String],
) -> AppResult<Vec<Uuid>> {
    let mut ids = Vec::with_capacity(filenames.len());

    for filename in filenames {
        let id = Uuid::new_v4();

        let model = crate::entity::upload_file::ActiveModel {
            id: Set(id),
            report_id: Set(report_id),
            filename: Set(filename.clone()),
            file_size: Set(None),
            uploaded_at: Set(None),
            created_at: Set(Utc::now()),
        };

        crate::entity::upload_file::Entity::insert(model)
            .exec(db)
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to register file {}: {}", filename, e))
            })?;

        ids.push(id);
    }

    Ok(ids)
}

/// Mark a file as uploaded with its size.
/// Returns true if the file was found and updated.
pub async fn mark_uploaded(
    db: &DatabaseConnection,
    report_id: Uuid,
    filename: &str,
    file_size: i64,
) -> AppResult<bool> {
    let result = crate::entity::upload_file::Entity::update_many()
        .col_expr(
            crate::entity::upload_file::Column::FileSize,
            Expr::value(file_size),
        )
        .col_expr(
            crate::entity::upload_file::Column::UploadedAt,
            Expr::value(Utc::now()),
        )
        .filter(crate::entity::upload_file::Column::ReportId.eq(report_id))
        .filter(crate::entity::upload_file::Column::Filename.eq(filename))
        .filter(crate::entity::upload_file::Column::UploadedAt.is_null())
        .exec(db)
        .await
        .map_err(|e| AppError::Database(format!("Failed to mark file uploaded: {}", e)))?;

    Ok(result.rows_affected > 0)
}

/// Get status of specific files for a report.
pub async fn get_files_status(
    db: &DatabaseConnection,
    report_id: Uuid,
    filenames: &[String],
) -> AppResult<std::collections::HashMap<String, FileStatus>> {
    let files = crate::entity::upload_file::Entity::find()
        .filter(crate::entity::upload_file::Column::ReportId.eq(report_id))
        .filter(crate::entity::upload_file::Column::Filename.is_in(filenames.to_vec()))
        .all(db)
        .await
        .map_err(|e| AppError::Database(format!("Failed to get file status: {}", e)))?;

    let mut status_map = std::collections::HashMap::new();
    for file in files {
        status_map.insert(
            file.filename.clone(),
            FileStatus {
                is_uploaded: file.uploaded_at.is_some(),
            },
        );
    }

    Ok(status_map)
}

/// Get upload progress for a report.
/// Returns (total_files, uploaded_count, all_uploaded).
pub async fn get_upload_progress(
    db: &DatabaseConnection,
    report_id: Uuid,
) -> AppResult<(i64, i64, bool)> {
    let total = crate::entity::upload_file::Entity::find()
        .filter(crate::entity::upload_file::Column::ReportId.eq(report_id))
        .count(db)
        .await
        .map_err(|e| AppError::Database(format!("Failed to count files: {}", e)))?;

    let uploaded = crate::entity::upload_file::Entity::find()
        .filter(crate::entity::upload_file::Column::ReportId.eq(report_id))
        .filter(crate::entity::upload_file::Column::UploadedAt.is_not_null())
        .count(db)
        .await
        .map_err(|e| AppError::Database(format!("Failed to count uploaded: {}", e)))?;

    let total_i64 = total as i64;
    let uploaded_i64 = uploaded as i64;
    let all_uploaded = total > 0 && uploaded == total;

    Ok((total_i64, uploaded_i64, all_uploaded))
}

/// Get all registered filenames for a report.
pub async fn get_all_filenames(db: &DatabaseConnection, report_id: Uuid) -> AppResult<Vec<String>> {
    let files = crate::entity::upload_file::Entity::find()
        .filter(crate::entity::upload_file::Column::ReportId.eq(report_id))
        .all(db)
        .await
        .map_err(|e| AppError::Database(format!("Failed to get filenames: {}", e)))?;

    Ok(files.into_iter().map(|f| f.filename).collect())
}
