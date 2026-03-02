//! Database queries for reports (individual uploads).

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, QueryOrder,
    QuerySelect, Set,
};
use uuid::Uuid;

use crate::entity::report::{self as report, ActiveModel, Entity as Report};
use crate::error::{AppError, AppResult};
use crate::models::{EnvironmentMetadata, UploadStatus};

use super::DbPool;

impl DbPool {
    /// Insert a new report (individual upload).
    #[allow(clippy::too_many_arguments)]
    pub async fn insert_report(
        &self,
        id: Uuid,
        report_group_id: Uuid,
        name: &str,
        gh_job_id: Option<String>,
        gh_job_name: Option<String>,
        environment: Option<EnvironmentMetadata>,
        status: UploadStatus,
    ) -> AppResult<report::Model> {
        let now = Utc::now();

        let env_json = environment.and_then(|e: EnvironmentMetadata| e.to_json());

        let model = ActiveModel {
            id: Set(id),
            report_group_id: Set(report_group_id),
            name: Set(name.to_string()),
            gh_job_id: Set(gh_job_id),
            gh_job_name: Set(gh_job_name),
            status: Set(status.as_str().to_string()),
            screenshots_upload_status: Set(None),
            json_upload_status: Set(None),
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
            .map_err(|e| AppError::Database(format!("Failed to insert report: {}", e)))?;

        Ok(result)
    }

    /// List individual reports with pagination, newest first.
    pub async fn list_individual_reports(
        &self,
        limit: u64,
        offset: u64,
    ) -> AppResult<(Vec<report::Model>, u64)> {
        let select = Report::find().order_by_desc(report::Column::CreatedAt);

        let total = select
            .clone()
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count reports: {}", e)))?;

        let reports = select
            .offset(offset)
            .limit(limit)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to list reports: {}", e)))?;

        Ok((reports, total))
    }

    /// Get a report by ID.
    pub async fn get_report_by_id(&self, id: Uuid) -> AppResult<Option<report::Model>> {
        let result = Report::find_by_id(id)
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get report: {}", e)))?;

        Ok(result)
    }

    /// Get all reports for a report group.
    pub async fn get_reports_by_group_id(
        &self,
        report_group_id: Uuid,
    ) -> AppResult<Vec<report::Model>> {
        let result = Report::find()
            .filter(report::Column::ReportGroupId.eq(report_group_id))
            .order_by_asc(report::Column::Id) // UUIDv7 is time-ordered
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get reports for group: {}", e)))?;

        Ok(result)
    }

    /// Update report status.
    pub async fn update_report_status(
        &self,
        id: Uuid,
        status: UploadStatus,
        error_message: Option<String>,
    ) -> AppResult<report::Model> {
        let report = self
            .get_report_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Report {} not found", id)))?;

        let mut active: ActiveModel = report.into();
        active.status = Set(status.as_str().to_string());
        active.error_message = Set(error_message);
        active.updated_at = Set(Utc::now());

        let result = active
            .update(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to update report status: {}", e)))?;

        Ok(result)
    }

    /// Update screenshots upload status for a report.
    pub async fn set_screenshots_upload_status(
        &self,
        id: Uuid,
        status: Option<&str>,
    ) -> AppResult<report::Model> {
        let report = self
            .get_report_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Report {} not found", id)))?;

        let mut active: ActiveModel = report.into();
        active.screenshots_upload_status = Set(status.map(|s| s.to_string()));
        active.updated_at = Set(Utc::now());

        let result = active.update(self.connection()).await.map_err(|e| {
            AppError::Database(format!("Failed to update screenshots_upload_status: {}", e))
        })?;

        Ok(result)
    }

    /// Update JSON upload status for a report.
    pub async fn set_json_upload_status(
        &self,
        id: Uuid,
        status: Option<&str>,
    ) -> AppResult<report::Model> {
        let report = self
            .get_report_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Report {} not found", id)))?;

        let mut active: ActiveModel = report.into();
        active.json_upload_status = Set(status.map(|s| s.to_string()));
        active.updated_at = Set(Utc::now());

        let result = active.update(self.connection()).await.map_err(|e| {
            AppError::Database(format!("Failed to update json_upload_status: {}", e))
        })?;

        Ok(result)
    }

    /// Update report duration and start time from JUnit stats.
    pub async fn update_report_duration(
        &self,
        id: Uuid,
        duration_ms: Option<i64>,
        start_time: Option<chrono::DateTime<Utc>>,
    ) -> AppResult<report::Model> {
        let report = self
            .get_report_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Report {} not found", id)))?;

        let mut active: ActiveModel = report.into();
        active.duration_ms = Set(duration_ms);
        active.start_time = Set(start_time);
        active.updated_at = Set(Utc::now());

        let result = active
            .update(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to update report duration: {}", e)))?;

        Ok(result)
    }

    /// Find a report by GitHub job ID within a report group (for idempotency).
    pub async fn find_report_by_gh_job_id(
        &self,
        report_group_id: Uuid,
        gh_job_id: &str,
    ) -> AppResult<Option<report::Model>> {
        let result = Report::find()
            .filter(report::Column::ReportGroupId.eq(report_group_id))
            .filter(report::Column::GhJobId.eq(gh_job_id))
            .one(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to find report by GitHub ID: {}", e))
            })?;

        Ok(result)
    }

    /// Count completed reports for a report group.
    pub async fn count_completed_reports(&self, report_group_id: Uuid) -> AppResult<u64> {
        let count = Report::find()
            .filter(report::Column::ReportGroupId.eq(report_group_id))
            .filter(report::Column::Status.eq(UploadStatus::Complete.as_str()))
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count completed reports: {}", e)))?;

        Ok(count)
    }

    /// Batch count completed reports for multiple report groups.
    /// Returns a HashMap of report_group_id -> completed_reports_count.
    pub async fn count_completed_reports_batch(
        &self,
        report_group_ids: &[Uuid],
    ) -> AppResult<std::collections::HashMap<Uuid, i32>> {
        use sea_orm::{FromQueryResult, Statement};

        if report_group_ids.is_empty() {
            return Ok(std::collections::HashMap::new());
        }

        #[derive(Debug, FromQueryResult)]
        struct CountResult {
            report_group_id: Uuid,
            count: i64,
        }

        // Build placeholders for the IN clause
        let placeholders: Vec<String> = (1..=report_group_ids.len())
            .map(|i| format!("${}", i))
            .collect();
        let in_clause = placeholders.join(", ");

        let sql = format!(
            "SELECT report_group_id, COUNT(*) as count FROM reports WHERE report_group_id IN ({}) AND status = 'complete' GROUP BY report_group_id",
            in_clause
        );

        let values: Vec<sea_orm::Value> = report_group_ids
            .iter()
            .map(|id| sea_orm::Value::Uuid(Some(*id)))
            .collect();

        let results: Vec<CountResult> = CountResult::find_by_statement(
            Statement::from_sql_and_values(sea_orm::DatabaseBackend::Postgres, &sql, values),
        )
        .all(self.connection())
        .await
        .map_err(|e| {
            AppError::Database(format!("Failed to batch count completed reports: {}", e))
        })?;

        let mut counts = std::collections::HashMap::new();
        for result in results {
            counts.insert(result.report_group_id, result.count as i32);
        }

        Ok(counts)
    }
}
