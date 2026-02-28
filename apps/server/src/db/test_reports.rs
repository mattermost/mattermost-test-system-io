//! Database queries for reports.

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, QueryOrder,
    QuerySelect, Set,
};
use uuid::Uuid;

use crate::entity::test_report::{self as report, ActiveModel, Entity as Report};
use crate::error::{AppError, AppResult};
use crate::models::report::ReportEnvironmentMetadata;
use crate::models::{Framework, ListReportsQuery, ReportStatus};

use super::DbPool;

/// Parameters for inserting a new report.
pub struct InsertReportParams<'a> {
    pub id: Uuid,
    pub expected_jobs: i32,
    pub framework: Framework,
    pub repository: &'a str,
    pub branch: &'a str,
    pub commit: &'a str,
    pub run_id: &'a str,
    pub pr_number: Option<i32>,
    pub environment_metadata: Option<ReportEnvironmentMetadata>,
}

impl DbPool {
    /// Insert a new report.
    pub async fn insert_report(&self, params: InsertReportParams<'_>) -> AppResult<report::Model> {
        let now = Utc::now();

        let env_json = params
            .environment_metadata
            .and_then(|m: ReportEnvironmentMetadata| m.to_json());

        let model = ActiveModel {
            id: Set(params.id),
            expected_jobs: Set(params.expected_jobs),
            framework: Set(params.framework.as_str().to_string()),
            status: Set(ReportStatus::Initializing.as_str().to_string()),
            repository: Set(params.repository.to_string()),
            branch: Set(params.branch.to_string()),
            commit: Set(params.commit.to_string()),
            run_id: Set(params.run_id.to_string()),
            pr_number: Set(params.pr_number),
            environment_metadata: Set(env_json),
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

    /// Get a report by ID.
    pub async fn get_report_by_id(&self, id: Uuid) -> AppResult<Option<report::Model>> {
        let result = Report::find_by_id(id)
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get report: {}", e)))?;

        Ok(result)
    }

    /// Update report status.
    pub async fn update_report_status(
        &self,
        id: Uuid,
        status: ReportStatus,
    ) -> AppResult<report::Model> {
        let report = self
            .get_report_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Report {} not found", id)))?;

        let mut active: ActiveModel = report.into();
        active.status = Set(status.as_str().to_string());
        active.updated_at = Set(Utc::now());

        let result = active
            .update(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to update report status: {}", e)))?;

        Ok(result)
    }

    /// List reports with optional filtering.
    pub async fn list_reports(
        &self,
        query: &ListReportsQuery,
    ) -> AppResult<(Vec<report::Model>, u64)> {
        let mut select = Report::find();

        // Apply filters
        if let Some(ref framework) = query.framework {
            select = select.filter(report::Column::Framework.eq(framework.as_str()));
        }

        if let Some(ref status) = query.status {
            select = select.filter(report::Column::Status.eq(status.as_str()));
        }

        // Typed column filtering for repository (supports LIKE for suffix matching)
        if let Some(ref repository) = query.repository {
            if repository.contains('%') {
                select = select.filter(report::Column::Repository.like(repository));
            } else {
                select = select.filter(report::Column::Repository.eq(repository.clone()));
            }
        }

        // Typed column filtering for branch
        if let Some(ref branch) = query.branch {
            select = select.filter(report::Column::Branch.eq(branch.clone()));
        }

        // Typed column filtering for commit (prefix match)
        if let Some(ref commit) = query.commit {
            select = select.filter(report::Column::Commit.like(format!("{}%", commit)));
        }

        // Count total before pagination
        let total = select
            .clone()
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count reports: {}", e)))?;

        // Apply ordering and pagination
        let limit = query.limit.clamp(1, 100) as u64;
        let offset = query.offset.max(0) as u64;

        let reports = select
            .order_by_desc(report::Column::CreatedAt)
            .offset(offset)
            .limit(limit)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to list reports: {}", e)))?;

        Ok((reports, total))
    }

    /// List all reports grouped by repository for the landing page.
    /// Returns all reports ordered by creation date (newest first).
    /// Grouping and limiting per repo is done in application code.
    pub async fn list_all_reports_for_grouping(&self) -> AppResult<Vec<report::Model>> {
        let reports = Report::find()
            .order_by_desc(report::Column::CreatedAt)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to list reports: {}", e)))?;

        Ok(reports)
    }

    /// Count distinct full commit values matching a prefix.
    /// Used to detect ambiguous short SHAs (FR-006).
    pub async fn count_distinct_commits(&self, commit_prefix: &str) -> AppResult<usize> {
        use sea_orm::FromQueryResult;

        #[derive(Debug, FromQueryResult)]
        struct CountRow {
            count: i64,
        }

        let result = Report::find()
            .select_only()
            .column_as(
                sea_orm::sea_query::Expr::cust("COUNT(DISTINCT commit)"),
                "count",
            )
            .filter(report::Column::Commit.like(format!("{}%", commit_prefix)))
            .into_model::<CountRow>()
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count distinct commits: {}", e)))?;

        Ok(result.map(|r| r.count as usize).unwrap_or(0))
    }
}
