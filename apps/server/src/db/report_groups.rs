//! Database queries for report groups.

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, QueryOrder,
    QuerySelect, Set,
};
use uuid::Uuid;

use crate::entity::report_group::{self as report_group, ActiveModel, Entity as ReportGroup};
use crate::error::{AppError, AppResult};
use crate::models::report::ReportEnvironmentMetadata;
use crate::models::{Framework, ListReportsQuery, ReportStatus};

use super::DbPool;

/// Parameters for inserting a new report group.
#[allow(dead_code)]
pub struct InsertReportGroupParams<'a> {
    pub id: Uuid,
    pub framework: Framework,
    pub name: &'a str,
    pub repository: &'a str,
    pub branch: &'a str,
    pub commit: &'a str,
    pub gh_run_id: &'a str,
    pub gh_run_attempt: &'a str,
    pub gh_pr_number: Option<i32>,
    pub environment_metadata: Option<ReportEnvironmentMetadata>,
}

impl DbPool {
    /// Insert a new report group.
    #[allow(dead_code)]
    pub async fn insert_report_group(
        &self,
        params: InsertReportGroupParams<'_>,
    ) -> AppResult<report_group::Model> {
        let now = Utc::now();

        let env_json = params
            .environment_metadata
            .and_then(|m: ReportEnvironmentMetadata| m.to_json());

        let model = ActiveModel {
            id: Set(params.id),
            framework: Set(params.framework.as_str().to_string()),
            name: Set(params.name.to_string()),
            status: Set(ReportStatus::InProgress.as_str().to_string()),
            repository: Set(params.repository.to_string()),
            branch: Set(params.branch.to_string()),
            commit: Set(params.commit.to_string()),
            gh_run_id: Set(params.gh_run_id.to_string()),
            gh_run_attempt: Set(params.gh_run_attempt.to_string()),
            gh_pr_number: Set(params.gh_pr_number),
            environment_metadata: Set(env_json),
            created_at: Set(now),
            updated_at: Set(now),
            deleted_at: Set(None),
        };

        let result = model
            .insert(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to insert report group: {}", e)))?;

        Ok(result)
    }

    /// Get a report group by ID.
    pub async fn get_report_group_by_id(&self, id: Uuid) -> AppResult<Option<report_group::Model>> {
        let result = ReportGroup::find_by_id(id)
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get report group: {}", e)))?;

        Ok(result)
    }

    /// Update report group status.
    #[allow(dead_code)]
    pub async fn update_report_group_status(
        &self,
        id: Uuid,
        status: ReportStatus,
    ) -> AppResult<report_group::Model> {
        let report_group = self
            .get_report_group_by_id(id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Report group {} not found", id)))?;

        let mut active: ActiveModel = report_group.into();
        active.status = Set(status.as_str().to_string());
        active.updated_at = Set(Utc::now());

        let result = active.update(self.connection()).await.map_err(|e| {
            AppError::Database(format!("Failed to update report group status: {}", e))
        })?;

        Ok(result)
    }

    /// List report groups with optional filtering.
    pub async fn list_report_groups(
        &self,
        query: &ListReportsQuery,
    ) -> AppResult<(Vec<report_group::Model>, u64)> {
        let mut select = ReportGroup::find();

        // Apply filters
        if let Some(ref framework) = query.framework {
            select = select.filter(report_group::Column::Framework.eq(framework.as_str()));
        }

        if let Some(ref name) = query.name {
            select = select.filter(report_group::Column::Name.eq(name.clone()));
        }

        if let Some(ref status) = query.status {
            select = select.filter(report_group::Column::Status.eq(status.as_str()));
        }

        // Typed column filtering for repository (supports LIKE for suffix matching)
        if let Some(ref repository) = query.repository {
            if repository.contains('%') {
                select = select.filter(report_group::Column::Repository.like(repository));
            } else {
                select = select.filter(report_group::Column::Repository.eq(repository.clone()));
            }
        }

        // Typed column filtering for branch (supports LIKE for PR pattern matching)
        if let Some(ref branch) = query.branch {
            if branch.contains('%') {
                select = select.filter(report_group::Column::Branch.like(branch));
            } else {
                select = select.filter(report_group::Column::Branch.eq(branch.clone()));
            }
        }

        // Typed column filtering for commit (prefix match)
        if let Some(ref commit) = query.commit {
            select = select.filter(report_group::Column::Commit.like(format!("{}%", commit)));
        }

        // Filter by gh_run_attempt
        if let Some(ref gh_run_attempt) = query.gh_run_attempt {
            select = select.filter(report_group::Column::GhRunAttempt.eq(gh_run_attempt.clone()));
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
            .order_by_desc(report_group::Column::CreatedAt)
            .offset(offset)
            .limit(limit)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to list reports: {}", e)))?;

        Ok((reports, total))
    }

    /// List all report groups grouped by repository for the landing page.
    /// Returns all report groups ordered by creation date (newest first).
    /// Grouping and limiting per repo is done in application code.
    pub async fn list_all_report_groups_for_grouping(&self) -> AppResult<Vec<report_group::Model>> {
        let reports = ReportGroup::find()
            .order_by_desc(report_group::Column::CreatedAt)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to list reports: {}", e)))?;

        Ok(reports)
    }

    /// Upsert or find an existing report by the grouping key.
    ///
    /// Uses raw SQL with `ON CONFLICT ... DO UPDATE SET updated_at = NOW() RETURNING *`
    /// on the `idx_reports_grouping_key` unique index
    /// `(repository, commit, gh_run_id, framework, gh_run_attempt) WHERE deleted_at IS NULL`.
    ///
    /// Returns `(report_group::Model, bool)` where the bool is `true` when a new row was inserted.
    /// Detection uses the PostgreSQL `xmax` system column: `xmax = 0` means INSERT,
    /// nonzero means UPDATE (i.e., the row already existed).
    #[allow(dead_code)]
    pub async fn upsert_or_find_report_group(
        &self,
        params: InsertReportGroupParams<'_>,
    ) -> AppResult<(report_group::Model, bool)> {
        use sea_orm::{FromQueryResult, Statement};

        // We need a custom result struct that includes the xmax system column.
        #[derive(Debug, FromQueryResult)]
        struct UpsertRow {
            id: Uuid,
            framework: String,
            name: String,
            status: String,
            repository: String,
            branch: String,
            commit: String,
            gh_run_id: String,
            gh_run_attempt: String,
            gh_pr_number: Option<i32>,
            environment_metadata: Option<serde_json::Value>,
            created_at: chrono::DateTime<chrono::Utc>,
            updated_at: chrono::DateTime<chrono::Utc>,
            deleted_at: Option<chrono::DateTime<chrono::Utc>>,
            xmax: i64,
        }

        let env_json = params
            .environment_metadata
            .and_then(|m: ReportEnvironmentMetadata| m.to_json());

        let now = chrono::Utc::now();

        let sql = r#"
            INSERT INTO report_groups (id, framework, name, status, repository, branch, commit, gh_run_id, gh_run_attempt, gh_pr_number, environment_metadata, created_at, updated_at)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
            ON CONFLICT (repository, commit, gh_run_id, name, gh_run_attempt)
                WHERE deleted_at IS NULL
            DO UPDATE SET updated_at = NOW()
            RETURNING *, xmax::text::bigint AS xmax
        "#;

        let values: Vec<sea_orm::Value> = vec![
            params.id.into(),
            params.framework.as_str().to_string().into(),
            params.name.to_string().into(),
            ReportStatus::InProgress.as_str().to_string().into(),
            params.repository.to_string().into(),
            params.branch.to_string().into(),
            params.commit.to_string().into(),
            params.gh_run_id.to_string().into(),
            params.gh_run_attempt.to_string().into(),
            params.gh_pr_number.into(),
            env_json.into(),
            now.into(),
            now.into(),
        ];

        let row = UpsertRow::find_by_statement(Statement::from_sql_and_values(
            sea_orm::DatabaseBackend::Postgres,
            sql,
            values,
        ))
        .one(self.connection())
        .await
        .map_err(|e| AppError::Database(format!("Failed to upsert report: {}", e)))?
        .ok_or_else(|| AppError::Database("Upsert returned no rows".to_string()))?;

        let is_new = row.xmax == 0;

        let model = report_group::Model {
            id: row.id,
            framework: row.framework,
            name: row.name,
            status: row.status,
            repository: row.repository,
            branch: row.branch,
            commit: row.commit,
            gh_run_id: row.gh_run_id,
            gh_run_attempt: row.gh_run_attempt,
            gh_pr_number: row.gh_pr_number,
            environment_metadata: row.environment_metadata,
            created_at: row.created_at,
            updated_at: row.updated_at,
            deleted_at: row.deleted_at,
        };

        Ok((model, is_new))
    }

    /// Count all reports belonging to a report group.
    #[allow(dead_code)]
    pub async fn count_reports_in_group(&self, report_group_id: Uuid) -> AppResult<i64> {
        use sea_orm::{FromQueryResult, Statement};

        #[derive(Debug, FromQueryResult)]
        struct CountRow {
            count: i64,
        }

        let row = CountRow::find_by_statement(Statement::from_sql_and_values(
            sea_orm::DatabaseBackend::Postgres,
            "SELECT COUNT(*) as count FROM reports WHERE report_group_id = $1",
            vec![report_group_id.into()],
        ))
        .one(self.connection())
        .await
        .map_err(|e| AppError::Database(format!("Failed to count reports: {}", e)))?;

        Ok(row.map(|r| r.count).unwrap_or(0))
    }

    /// Find a report group by its grouping key.
    ///
    /// The grouping key is `(repository, commit, gh_run_id, name, gh_run_attempt)`
    /// with the constraint `deleted_at IS NULL`.
    /// Returns `None` if no matching report group exists.
    #[allow(dead_code)]
    pub async fn find_report_group_by_grouping_key(
        &self,
        repository: &str,
        commit: &str,
        gh_run_id: &str,
        name: &str,
        gh_run_attempt: &str,
    ) -> AppResult<Option<report_group::Model>> {
        let result = ReportGroup::find()
            .filter(report_group::Column::Repository.eq(repository))
            .filter(report_group::Column::Commit.eq(commit))
            .filter(report_group::Column::GhRunId.eq(gh_run_id))
            .filter(report_group::Column::Name.eq(name))
            .filter(report_group::Column::GhRunAttempt.eq(gh_run_attempt))
            .filter(report_group::Column::DeletedAt.is_null())
            .one(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to find report by grouping key: {}", e))
            })?;

        Ok(result)
    }

    /// Count distinct full commit values matching a prefix.
    /// Used to detect ambiguous short SHAs (FR-006).
    pub async fn count_distinct_commits(&self, commit_prefix: &str) -> AppResult<usize> {
        use sea_orm::FromQueryResult;

        #[derive(Debug, FromQueryResult)]
        struct CountRow {
            count: i64,
        }

        let result = ReportGroup::find()
            .select_only()
            .column_as(
                sea_orm::sea_query::Expr::cust("COUNT(DISTINCT commit)"),
                "count",
            )
            .filter(report_group::Column::Commit.like(format!("{}%", commit_prefix)))
            .into_model::<CountRow>()
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count distinct commits: {}", e)))?;

        Ok(result.map(|r| r.count as usize).unwrap_or(0))
    }
}
