//! Database queries for HTML files with upload tracking.
//!
//! Follows the request-then-transfer pattern for HTML report uploads.

use chrono::Utc;
use sea_orm::{ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, Set};
use uuid::Uuid;

use crate::entity::html_file::{self, ActiveModel, Entity as HtmlFile};
use crate::error::{AppError, AppResult};

use super::DbPool;

/// HTML file status values.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum HtmlFileStatus {
    Pending,
    Uploaded,
}

impl HtmlFileStatus {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Uploaded => "uploaded",
        }
    }
}

/// HTML file entry to insert into the database.
#[derive(Debug, Clone)]
pub struct HtmlFileEntry {
    pub filename: String,
    pub s3_key: String,
    pub size_bytes: i64,
    pub content_type: Option<String>,
}

impl DbPool {
    /// Insert multiple HTML files in a batch (pending status).
    pub async fn insert_html_files(
        &self,
        job_id: Uuid,
        files: Vec<HtmlFileEntry>,
    ) -> AppResult<Vec<html_file::Model>> {
        let now = Utc::now();

        let mut inserted = Vec::with_capacity(files.len());

        for file in files {
            let id = Uuid::now_v7();
            let model = ActiveModel {
                id: Set(id),
                test_job_id: Set(job_id),
                filename: Set(file.filename),
                s3_key: Set(file.s3_key),
                size_bytes: Set(file.size_bytes),
                content_type: Set(file.content_type),
                status: Set(HtmlFileStatus::Pending.as_str().to_string()),
                created_at: Set(now),
                updated_at: Set(now),
                uploaded_at: Set(None),
                deleted_at: Set(None),
            };

            let result = model
                .insert(self.connection())
                .await
                .map_err(|e| AppError::Database(format!("Failed to insert HTML file: {}", e)))?;

            inserted.push(result);
        }

        Ok(inserted)
    }

    /// Get pending HTML files for a job.
    pub async fn get_pending_html_files(&self, job_id: Uuid) -> AppResult<Vec<html_file::Model>> {
        let result = HtmlFile::find()
            .filter(html_file::Column::TestJobId.eq(job_id))
            .filter(html_file::Column::Status.eq(HtmlFileStatus::Pending.as_str()))
            .filter(html_file::Column::DeletedAt.is_null())
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get pending HTML files: {}", e)))?;

        Ok(result)
    }

    /// Mark HTML files as uploaded.
    pub async fn mark_html_files_uploaded(
        &self,
        job_id: Uuid,
        filenames: &[String],
    ) -> AppResult<u64> {
        let now = Utc::now();

        let result = HtmlFile::update_many()
            .col_expr(
                html_file::Column::Status,
                sea_orm::sea_query::Expr::value(HtmlFileStatus::Uploaded.as_str()),
            )
            .col_expr(
                html_file::Column::UploadedAt,
                sea_orm::sea_query::Expr::value(now),
            )
            .filter(html_file::Column::TestJobId.eq(job_id))
            .filter(html_file::Column::Filename.is_in(filenames))
            .filter(html_file::Column::DeletedAt.is_null())
            .exec(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to mark HTML files uploaded: {}", e))
            })?;

        Ok(result.rows_affected)
    }

    /// Count pending HTML files for a job.
    pub async fn count_pending_html_files(&self, job_id: Uuid) -> AppResult<u64> {
        let count = HtmlFile::find()
            .filter(html_file::Column::TestJobId.eq(job_id))
            .filter(html_file::Column::Status.eq(HtmlFileStatus::Pending.as_str()))
            .filter(html_file::Column::DeletedAt.is_null())
            .count(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to count pending HTML files: {}", e))
            })?;

        Ok(count)
    }

    /// Count total HTML files for a job.
    pub async fn count_html_files(&self, job_id: Uuid) -> AppResult<u64> {
        let count = HtmlFile::find()
            .filter(html_file::Column::TestJobId.eq(job_id))
            .filter(html_file::Column::DeletedAt.is_null())
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count HTML files: {}", e)))?;

        Ok(count)
    }

    /// Get HTML file upload progress for a job.
    pub async fn get_html_upload_progress(&self, job_id: Uuid) -> AppResult<(u64, u64)> {
        let total = self.count_html_files(job_id).await?;
        let pending = self.count_pending_html_files(job_id).await?;
        let uploaded = total - pending;

        Ok((uploaded, total))
    }

    /// Check if all HTML files have been uploaded for a job.
    pub async fn all_html_files_uploaded(&self, job_id: Uuid) -> AppResult<bool> {
        let pending = self.count_pending_html_files(job_id).await?;
        Ok(pending == 0)
    }

    /// Get a specific HTML file by job_id and filename.
    pub async fn get_html_file(
        &self,
        job_id: Uuid,
        filename: &str,
    ) -> AppResult<Option<html_file::Model>> {
        let result = HtmlFile::find()
            .filter(html_file::Column::TestJobId.eq(job_id))
            .filter(html_file::Column::Filename.eq(filename))
            .filter(html_file::Column::DeletedAt.is_null())
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get HTML file: {}", e)))?;

        Ok(result)
    }

    /// Get root HTML filenames for multiple jobs.
    /// Returns a map of job_id -> filename for jobs that have a root HTML file.
    pub async fn get_root_html_filenames_for_jobs(
        &self,
        job_ids: &[Uuid],
    ) -> AppResult<std::collections::HashMap<Uuid, String>> {
        use std::collections::HashMap;

        if job_ids.is_empty() {
            return Ok(HashMap::new());
        }

        let files = HtmlFile::find()
            .filter(html_file::Column::TestJobId.is_in(job_ids.to_vec()))
            .filter(html_file::Column::DeletedAt.is_null())
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get HTML files: {}", e)))?;

        let mut result: HashMap<Uuid, String> = HashMap::new();

        // Find first .html file in root for each job
        for file in files {
            if file.filename.ends_with(".html")
                && !file.filename.contains('/')
                && !result.contains_key(&file.test_job_id)
            {
                result.insert(file.test_job_id, file.filename);
            }
        }

        Ok(result)
    }
}
