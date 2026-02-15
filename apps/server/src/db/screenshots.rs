//! Database queries for screenshots with upload tracking.
//!
//! Follows the same request-then-transfer pattern as job_files.

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, QueryOrder, Set,
};
use uuid::Uuid;

use crate::entity::screenshot::{self, ActiveModel, Entity as Screenshot};
use crate::error::{AppError, AppResult};

use super::DbPool;

/// Screenshot status values.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ScreenshotStatus {
    Pending,
    Uploaded,
}

impl ScreenshotStatus {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Uploaded => "uploaded",
        }
    }
}

/// Screenshot entry to insert into the database.
#[derive(Debug, Clone)]
pub struct ScreenshotEntry {
    pub filename: String,
    pub s3_key: String,
    pub size_bytes: i64,
    pub content_type: Option<String>,
    pub test_name: String,
    pub sequence: i32,
}

impl DbPool {
    /// Insert multiple screenshots in a batch (pending status).
    pub async fn insert_screenshots(
        &self,
        job_id: Uuid,
        screenshots: Vec<ScreenshotEntry>,
    ) -> AppResult<Vec<screenshot::Model>> {
        let now = Utc::now();

        let mut inserted = Vec::with_capacity(screenshots.len());

        for screenshot in screenshots {
            let id = Uuid::now_v7();
            let model = ActiveModel {
                id: Set(id),
                test_job_id: Set(job_id),
                test_case_id: Set(None), // linked after extraction
                filename: Set(screenshot.filename),
                s3_key: Set(screenshot.s3_key),
                size_bytes: Set(screenshot.size_bytes),
                content_type: Set(screenshot.content_type),
                test_name: Set(screenshot.test_name),
                sequence: Set(screenshot.sequence),
                status: Set(ScreenshotStatus::Pending.as_str().to_string()),
                created_at: Set(now),
                updated_at: Set(now),
                uploaded_at: Set(None),
                deleted_at: Set(None),
            };

            let result = model
                .insert(self.connection())
                .await
                .map_err(|e| AppError::Database(format!("Failed to insert screenshot: {}", e)))?;

            inserted.push(result);
        }

        Ok(inserted)
    }

    /// Get all screenshots for a job.
    pub async fn get_screenshots_by_job_id(
        &self,
        job_id: Uuid,
    ) -> AppResult<Vec<screenshot::Model>> {
        let result = Screenshot::find()
            .filter(screenshot::Column::TestJobId.eq(job_id))
            .order_by_asc(screenshot::Column::TestName)
            .order_by_asc(screenshot::Column::Sequence)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get screenshots: {}", e)))?;

        Ok(result)
    }

    /// Get pending screenshots for a job.
    pub async fn get_pending_screenshots(&self, job_id: Uuid) -> AppResult<Vec<screenshot::Model>> {
        let result = Screenshot::find()
            .filter(screenshot::Column::TestJobId.eq(job_id))
            .filter(screenshot::Column::Status.eq(ScreenshotStatus::Pending.as_str()))
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get pending screenshots: {}", e)))?;

        Ok(result)
    }

    /// Mark screenshots as uploaded.
    pub async fn mark_screenshots_uploaded(
        &self,
        job_id: Uuid,
        filenames: &[String],
    ) -> AppResult<u64> {
        let now = Utc::now();

        let result = Screenshot::update_many()
            .col_expr(
                screenshot::Column::Status,
                sea_orm::sea_query::Expr::value(ScreenshotStatus::Uploaded.as_str()),
            )
            .col_expr(
                screenshot::Column::UploadedAt,
                sea_orm::sea_query::Expr::value(now),
            )
            .filter(screenshot::Column::TestJobId.eq(job_id))
            .filter(screenshot::Column::Filename.is_in(filenames))
            .exec(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to mark screenshots uploaded: {}", e))
            })?;

        Ok(result.rows_affected)
    }

    /// Count pending screenshots for a job.
    pub async fn count_pending_screenshots(&self, job_id: Uuid) -> AppResult<u64> {
        let count = Screenshot::find()
            .filter(screenshot::Column::TestJobId.eq(job_id))
            .filter(screenshot::Column::Status.eq(ScreenshotStatus::Pending.as_str()))
            .count(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to count pending screenshots: {}", e))
            })?;

        Ok(count)
    }

    /// Count total screenshots for a job.
    pub async fn count_screenshots(&self, job_id: Uuid) -> AppResult<u64> {
        let count = Screenshot::find()
            .filter(screenshot::Column::TestJobId.eq(job_id))
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count screenshots: {}", e)))?;

        Ok(count)
    }

    /// Get screenshot upload progress for a job.
    pub async fn get_screenshot_upload_progress(&self, job_id: Uuid) -> AppResult<(u64, u64)> {
        let total = self.count_screenshots(job_id).await?;
        let pending = self.count_pending_screenshots(job_id).await?;
        let uploaded = total - pending;

        Ok((uploaded, total))
    }

    /// Check if all screenshots have been uploaded for a job.
    pub async fn all_screenshots_uploaded(&self, job_id: Uuid) -> AppResult<bool> {
        let pending = self.count_pending_screenshots(job_id).await?;
        Ok(pending == 0)
    }

    /// Get a specific screenshot by job_id and filename.
    pub async fn get_screenshot(
        &self,
        job_id: Uuid,
        filename: &str,
    ) -> AppResult<Option<screenshot::Model>> {
        let result = Screenshot::find()
            .filter(screenshot::Column::TestJobId.eq(job_id))
            .filter(screenshot::Column::Filename.eq(filename))
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get screenshot: {}", e)))?;

        Ok(result)
    }

    /// Link multiple screenshots to their matching test cases.
    /// Takes a list of (screenshot_id, test_case_id) pairs.
    pub async fn link_screenshots_to_test_cases(
        &self,
        mappings: &[(Uuid, Uuid)],
    ) -> AppResult<u64> {
        let mut linked_count = 0u64;

        for (screenshot_id, test_case_id) in mappings {
            let result = Screenshot::update_many()
                .col_expr(
                    screenshot::Column::TestCaseId,
                    sea_orm::sea_query::Expr::value(*test_case_id),
                )
                .filter(screenshot::Column::Id.eq(*screenshot_id))
                .exec(self.connection())
                .await
                .map_err(|e| AppError::Database(format!("Failed to link screenshot: {}", e)))?;

            linked_count += result.rows_affected;
        }

        Ok(linked_count)
    }
}
