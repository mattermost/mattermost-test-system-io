//! Database queries for reports.

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, QueryOrder,
    QuerySelect, Set,
};
use uuid::Uuid;

use crate::entity::test_report::{self as report, ActiveModel, Entity as Report};
use crate::error::{AppError, AppResult};
use crate::models::{Framework, GitHubMetadata, ListReportsQuery, ReportStatus};

use super::DbPool;

impl DbPool {
    /// Insert a new report.
    pub async fn insert_report(
        &self,
        id: Uuid,
        expected_jobs: i32,
        framework: Framework,
        github_metadata: Option<GitHubMetadata>,
    ) -> AppResult<report::Model> {
        let now = Utc::now();

        let github_json = github_metadata.and_then(|m: GitHubMetadata| m.to_json());

        let model = ActiveModel {
            id: Set(id),
            expected_jobs: Set(expected_jobs),
            framework: Set(framework.as_str().to_string()),
            status: Set(ReportStatus::Initializing.as_str().to_string()),
            github_metadata: Set(github_json),
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
        use sea_orm::sea_query::Expr;

        let mut select = Report::find();

        // Apply filters
        if let Some(ref framework) = query.framework {
            select = select.filter(report::Column::Framework.eq(framework.as_str()));
        }

        if let Some(ref status) = query.status {
            select = select.filter(report::Column::Status.eq(status.as_str()));
        }

        // JSONB filtering for github_repo
        if let Some(ref github_repo) = query.github_repo {
            select = select.filter(
                Expr::cust_with_values("github_metadata->>'repo' = $1", [github_repo.clone()]),
            );
        }

        // JSONB filtering for github_branch
        if let Some(ref github_branch) = query.github_branch {
            select = select.filter(
                Expr::cust_with_values("github_metadata->>'branch' = $1", [github_branch.clone()]),
            );
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
}
