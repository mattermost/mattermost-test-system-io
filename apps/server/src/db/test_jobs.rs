//! Database queries for jobs.

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, QueryOrder,
    QuerySelect, Set,
};
use uuid::Uuid;

use crate::entity::test_job::{self as job, ActiveModel, Entity as Job};
use crate::error::{AppError, AppResult};
use crate::models::{EnvironmentMetadata, JobGitHubMetadata, JobStatus, QueryJobsParams};

use super::DbPool;

impl DbPool {
    /// Insert a new job.
    pub async fn insert_job(
        &self,
        id: Uuid,
        report_id: Uuid,
        github_metadata: Option<JobGitHubMetadata>,
        environment: Option<EnvironmentMetadata>,
        status: JobStatus,
    ) -> AppResult<job::Model> {
        let now = Utc::now();

        let github_json = github_metadata.and_then(|m: JobGitHubMetadata| m.to_json());
        let env_json = environment.and_then(|e: EnvironmentMetadata| e.to_json());

        let model = ActiveModel {
            id: Set(id),
            test_report_id: Set(report_id),
            github_metadata: Set(github_json),
            status: Set(status.as_str().to_string()),
            html_upload_status: Set(None),
            screenshots_upload_status: Set(None),
            json_upload_status: Set(None),
            html_path: Set(None),
            environment: Set(env_json),
            error_message: Set(None),
            duration_ms: Set(None),
            start_time: Set(None),
            created_at: Set(now),
            updated_at: Set(now),
            deleted_at: Set(None),
        };

        let result = model
            .insert(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to insert job: {}", e)))?;

        Ok(result)
    }

    /// Get a job by ID.
    pub async fn get_job_by_id(&self, id: Uuid) -> AppResult<Option<job::Model>> {
        let result = Job::find_by_id(id)
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get job: {}", e)))?;

        Ok(result)
    }

    /// Get all jobs for a report.
    pub async fn get_jobs_by_report_id(&self, report_id: Uuid) -> AppResult<Vec<job::Model>> {
        let result = Job::find()
            .filter(job::Column::TestReportId.eq(report_id))
            .order_by_asc(job::Column::Id) // UUIDv7 is time-ordered
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get jobs for report: {}", e)))?;

        Ok(result)
    }

    /// Update job status.
    pub async fn update_job_status(
        &self,
        id: Uuid,
        status: JobStatus,
        error_message: Option<String>,
    ) -> AppResult<job::Model> {
        let job = self
            .get_job_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Job {} not found", id)))?;

        let mut active: ActiveModel = job.into();
        active.status = Set(status.as_str().to_string());
        active.error_message = Set(error_message);
        active.updated_at = Set(Utc::now());

        let result = active
            .update(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to update job status: {}", e)))?;

        Ok(result)
    }

    /// Update job HTML path.
    pub async fn update_job_html_path(&self, id: Uuid, html_path: String) -> AppResult<job::Model> {
        let job = self
            .get_job_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Job {} not found", id)))?;

        let mut active: ActiveModel = job.into();
        active.html_path = Set(Some(html_path));
        active.updated_at = Set(Utc::now());

        let result = active
            .update(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to update job HTML path: {}", e)))?;

        Ok(result)
    }

    /// Update HTML upload status for a job.
    pub async fn set_html_upload_status(
        &self,
        id: Uuid,
        status: Option<&str>,
    ) -> AppResult<job::Model> {
        let job = self
            .get_job_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Job {} not found", id)))?;

        let mut active: ActiveModel = job.into();
        active.html_upload_status = Set(status.map(|s| s.to_string()));
        active.updated_at = Set(Utc::now());

        let result = active.update(self.connection()).await.map_err(|e| {
            AppError::Database(format!("Failed to update html_upload_status: {}", e))
        })?;

        Ok(result)
    }

    /// Update screenshots upload status for a job.
    pub async fn set_screenshots_upload_status(
        &self,
        id: Uuid,
        status: Option<&str>,
    ) -> AppResult<job::Model> {
        let job = self
            .get_job_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Job {} not found", id)))?;

        let mut active: ActiveModel = job.into();
        active.screenshots_upload_status = Set(status.map(|s| s.to_string()));
        active.updated_at = Set(Utc::now());

        let result = active.update(self.connection()).await.map_err(|e| {
            AppError::Database(format!("Failed to update screenshots_upload_status: {}", e))
        })?;

        Ok(result)
    }

    /// Update JSON upload status for a job.
    pub async fn set_json_upload_status(
        &self,
        id: Uuid,
        status: Option<&str>,
    ) -> AppResult<job::Model> {
        let job = self
            .get_job_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Job {} not found", id)))?;

        let mut active: ActiveModel = job.into();
        active.json_upload_status = Set(status.map(|s| s.to_string()));
        active.updated_at = Set(Utc::now());

        let result = active.update(self.connection()).await.map_err(|e| {
            AppError::Database(format!("Failed to update json_upload_status: {}", e))
        })?;

        Ok(result)
    }

    /// Update job duration and start time from JUnit stats.
    pub async fn update_job_duration(
        &self,
        id: Uuid,
        duration_ms: Option<i64>,
        start_time: Option<chrono::DateTime<Utc>>,
    ) -> AppResult<job::Model> {
        let job = self
            .get_job_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Job {} not found", id)))?;

        let mut active: ActiveModel = job.into();
        active.duration_ms = Set(duration_ms);
        active.start_time = Set(start_time);
        active.updated_at = Set(Utc::now());

        let result = active
            .update(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to update job duration: {}", e)))?;

        Ok(result)
    }

    /// Find a job by GitHub job ID within a report (for idempotency).
    pub async fn find_job_by_github_id(
        &self,
        report_id: Uuid,
        github_job_id: &str,
    ) -> AppResult<Option<job::Model>> {
        use sea_orm::sea_query::Expr;

        let result = Job::find()
            .filter(job::Column::TestReportId.eq(report_id))
            .filter(Expr::cust_with_values(
                "github_metadata->>'job_id' = $1",
                [github_job_id.to_string()],
            ))
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to find job by GitHub ID: {}", e)))?;

        Ok(result)
    }

    /// Count completed jobs for a report.
    pub async fn count_completed_jobs(&self, report_id: Uuid) -> AppResult<u64> {
        let count = Job::find()
            .filter(job::Column::TestReportId.eq(report_id))
            .filter(job::Column::Status.eq(JobStatus::Complete.as_str()))
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count completed jobs: {}", e)))?;

        Ok(count)
    }

    /// Batch count completed jobs for multiple reports.
    /// Returns a HashMap of report_id -> completed_jobs_count.
    pub async fn count_completed_jobs_batch(
        &self,
        report_ids: &[Uuid],
    ) -> AppResult<std::collections::HashMap<Uuid, i32>> {
        use sea_orm::{FromQueryResult, Statement};

        if report_ids.is_empty() {
            return Ok(std::collections::HashMap::new());
        }

        #[derive(Debug, FromQueryResult)]
        struct CountResult {
            report_id: Uuid,
            count: i64,
        }

        // Build placeholders for the IN clause
        let placeholders: Vec<String> = (1..=report_ids.len()).map(|i| format!("${}", i)).collect();
        let in_clause = placeholders.join(", ");

        let sql = format!(
            "SELECT test_report_id as report_id, COUNT(*) as count FROM test_jobs WHERE test_report_id IN ({}) AND status = 'complete' GROUP BY test_report_id",
            in_clause
        );

        let values: Vec<sea_orm::Value> = report_ids
            .iter()
            .map(|id| sea_orm::Value::Uuid(Some(*id)))
            .collect();

        let results: Vec<CountResult> = CountResult::find_by_statement(
            Statement::from_sql_and_values(sea_orm::DatabaseBackend::Postgres, &sql, values),
        )
        .all(self.connection())
        .await
        .map_err(|e| AppError::Database(format!("Failed to batch count completed jobs: {}", e)))?;

        let mut counts = std::collections::HashMap::new();
        for result in results {
            counts.insert(result.report_id, result.count as i32);
        }

        Ok(counts)
    }

    /// Query jobs with filtering and pagination.
    pub async fn query_jobs(&self, query: &QueryJobsParams) -> AppResult<(Vec<job::Model>, u64)> {
        use sea_orm::sea_query::Expr;

        let mut select = Job::find();

        // Apply filters
        if let Some(report_id) = query.report_id {
            select = select.filter(job::Column::TestReportId.eq(report_id));
        }

        if let Some(ref status) = query.status {
            select = select.filter(job::Column::Status.eq(status.as_str()));
        }

        // JSONB filtering for github_job_name (case-insensitive ILIKE)
        if let Some(ref github_job_name) = query.github_job_name {
            select = select.filter(Expr::cust_with_values(
                "github_metadata->>'job_name' ILIKE $1",
                [format!("%{}%", github_job_name)],
            ));
        }

        if let Some(ref from_date) = query.from_date {
            select = select.filter(job::Column::CreatedAt.gte(*from_date));
        }

        if let Some(ref to_date) = query.to_date {
            select = select.filter(job::Column::CreatedAt.lte(*to_date));
        }

        // Count total before pagination
        let total = select
            .clone()
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count jobs: {}", e)))?;

        // Apply ordering and pagination
        let limit = query.limit.clamp(1, 100) as u64;
        let offset = query.offset.max(0) as u64;

        let jobs = select
            .order_by_desc(job::Column::CreatedAt)
            .offset(offset)
            .limit(limit)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to query jobs: {}", e)))?;

        Ok((jobs, total))
    }
}
