//! Database queries for JSON files with upload and extraction tracking.
//!
//! Follows the request-then-transfer pattern for JSON file uploads.
//! JSON files contain rich test data per framework (Cypress, Detox, Playwright).

use chrono::Utc;
use sea_orm::{ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, Set};
use uuid::Uuid;

use crate::entity::json_file::{self, ActiveModel, Entity as JsonFile};
use crate::error::{AppError, AppResult};

use super::DbPool;

/// JSON file status values.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum JsonFileStatus {
    Pending,
    Uploaded,
}

impl JsonFileStatus {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Uploaded => "uploaded",
        }
    }
}

/// JSON file entry to insert into the database.
#[derive(Debug, Clone)]
pub struct JsonFileEntry {
    pub filename: String,
    pub s3_key: String,
    pub size_bytes: i64,
    pub content_type: Option<String>,
}

impl DbPool {
    /// Insert multiple JSON files in a batch (pending status).
    pub async fn insert_json_files(
        &self,
        job_id: Uuid,
        files: Vec<JsonFileEntry>,
    ) -> AppResult<Vec<json_file::Model>> {
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
                status: Set(JsonFileStatus::Pending.as_str().to_string()),
                extracted_at: Set(None),
                extraction_error: Set(None),
                created_at: Set(now),
                updated_at: Set(now),
                uploaded_at: Set(None),
                deleted_at: Set(None),
            };

            let result = model
                .insert(self.connection())
                .await
                .map_err(|e| AppError::Database(format!("Failed to insert JSON file: {}", e)))?;

            inserted.push(result);
        }

        Ok(inserted)
    }

    /// Get pending JSON files for a job.
    pub async fn get_pending_json_files(&self, job_id: Uuid) -> AppResult<Vec<json_file::Model>> {
        let result = JsonFile::find()
            .filter(json_file::Column::TestJobId.eq(job_id))
            .filter(json_file::Column::Status.eq(JsonFileStatus::Pending.as_str()))
            .filter(json_file::Column::DeletedAt.is_null())
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get pending JSON files: {}", e)))?;

        Ok(result)
    }

    /// Get uploaded JSON files for a job (ready for extraction).
    pub async fn get_uploaded_json_files(&self, job_id: Uuid) -> AppResult<Vec<json_file::Model>> {
        let result = JsonFile::find()
            .filter(json_file::Column::TestJobId.eq(job_id))
            .filter(json_file::Column::Status.eq(JsonFileStatus::Uploaded.as_str()))
            .filter(json_file::Column::DeletedAt.is_null())
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get uploaded JSON files: {}", e)))?;

        Ok(result)
    }

    /// Mark JSON files as uploaded.
    pub async fn mark_json_files_uploaded(
        &self,
        job_id: Uuid,
        filenames: &[String],
    ) -> AppResult<u64> {
        let now = Utc::now();

        let result = JsonFile::update_many()
            .col_expr(
                json_file::Column::Status,
                sea_orm::sea_query::Expr::value(JsonFileStatus::Uploaded.as_str()),
            )
            .col_expr(
                json_file::Column::UploadedAt,
                sea_orm::sea_query::Expr::value(now),
            )
            .filter(json_file::Column::TestJobId.eq(job_id))
            .filter(json_file::Column::Filename.is_in(filenames))
            .filter(json_file::Column::DeletedAt.is_null())
            .exec(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to mark JSON files uploaded: {}", e))
            })?;

        Ok(result.rows_affected)
    }

    /// Mark a JSON file as extracted.
    pub async fn mark_json_file_extracted(&self, file_id: Uuid) -> AppResult<json_file::Model> {
        let file = JsonFile::find_by_id(file_id)
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to find JSON file: {}", e)))?
            .ok_or_else(|| AppError::NotFound(format!("JSON file {}", file_id)))?;

        let mut active: ActiveModel = file.into();
        active.extracted_at = Set(Some(Utc::now()));
        active.extraction_error = Set(None);
        active.updated_at = Set(Utc::now());

        let result = active.update(self.connection()).await.map_err(|e| {
            AppError::Database(format!("Failed to mark JSON file extracted: {}", e))
        })?;

        Ok(result)
    }

    /// Count pending JSON files for a job.
    pub async fn count_pending_json_files(&self, job_id: Uuid) -> AppResult<u64> {
        let count = JsonFile::find()
            .filter(json_file::Column::TestJobId.eq(job_id))
            .filter(json_file::Column::Status.eq(JsonFileStatus::Pending.as_str()))
            .filter(json_file::Column::DeletedAt.is_null())
            .count(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to count pending JSON files: {}", e))
            })?;

        Ok(count)
    }

    /// Count total JSON files for a job.
    pub async fn count_json_files(&self, job_id: Uuid) -> AppResult<u64> {
        let count = JsonFile::find()
            .filter(json_file::Column::TestJobId.eq(job_id))
            .filter(json_file::Column::DeletedAt.is_null())
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count JSON files: {}", e)))?;

        Ok(count)
    }

    /// Get JSON file upload progress for a job.
    pub async fn get_json_upload_progress(&self, job_id: Uuid) -> AppResult<(u64, u64)> {
        let total = self.count_json_files(job_id).await?;
        let pending = self.count_pending_json_files(job_id).await?;
        let uploaded = total - pending;

        Ok((uploaded, total))
    }

    /// Check if all JSON files have been uploaded for a job.
    pub async fn all_json_files_uploaded(&self, job_id: Uuid) -> AppResult<bool> {
        let total = self.count_json_files(job_id).await?;
        if total == 0 {
            return Ok(false); // No files registered yet
        }
        let pending = self.count_pending_json_files(job_id).await?;
        Ok(pending == 0)
    }

    /// Get a specific JSON file by job_id and filename.
    pub async fn get_json_file(
        &self,
        job_id: Uuid,
        filename: &str,
    ) -> AppResult<Option<json_file::Model>> {
        let result = JsonFile::find()
            .filter(json_file::Column::TestJobId.eq(job_id))
            .filter(json_file::Column::Filename.eq(filename))
            .filter(json_file::Column::DeletedAt.is_null())
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get JSON file: {}", e)))?;

        Ok(result)
    }
}
