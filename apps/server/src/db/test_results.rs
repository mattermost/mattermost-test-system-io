//! Database queries for test suites and test cases.

use chrono::Utc;
use sea_orm::{ActiveModelTrait, ColumnTrait, EntityTrait, PaginatorTrait, QueryFilter, QueryOrder, QuerySelect, Set};
use serde_json::Value as JsonValue;
use uuid::Uuid;

use crate::entity::test_case::{self, ActiveModel as TestCaseActiveModel, Entity as TestCase};
use crate::entity::test_suite::{self, ActiveModel as TestSuiteActiveModel, Entity as TestSuite};
use crate::error::{AppError, AppResult};

use super::DbPool;

/// Represents a test suite to be inserted.
pub struct NewTestSuite {
    pub job_id: Uuid,
    pub title: String,
    pub file_path: Option<String>,
    pub total_count: i32,
    pub passed_count: i32,
    pub failed_count: i32,
    pub skipped_count: i32,
    pub flaky_count: i32,
    pub duration_ms: i32,
    /// Actual test execution start time from framework JSON.
    pub start_time: Option<chrono::DateTime<chrono::Utc>>,
}

/// Represents a test case to be inserted.
pub struct NewTestCase {
    pub suite_id: Uuid,
    pub job_id: Uuid,
    pub title: String,
    pub full_title: String,
    pub status: String,
    pub duration_ms: i32,
    pub retry_count: i32,
    pub error_message: Option<String>,
    pub sequence: i32,
    pub attachments: Option<JsonValue>,
}

/// Query parameters for test suites.
#[derive(Debug, Default)]
pub struct QueryTestSuitesParams {
    pub job_id: Option<Uuid>,
    pub limit: i64,
    pub offset: i64,
}

/// Query parameters for test cases.
#[derive(Debug, Default)]
pub struct QueryTestCasesParams {
    pub job_id: Option<Uuid>,
    pub suite_id: Option<Uuid>,
    pub status: Option<String>,
    pub limit: i64,
    pub offset: i64,
}

impl DbPool {
    /// Insert a new test suite.
    pub async fn insert_test_suite(&self, suite: NewTestSuite) -> AppResult<test_suite::Model> {
        let id = Uuid::now_v7();
        let now = Utc::now();

        let model = TestSuiteActiveModel {
            id: Set(id),
            test_job_id: Set(suite.job_id),
            title: Set(suite.title),
            file_path: Set(suite.file_path),
            total_count: Set(suite.total_count),
            passed_count: Set(suite.passed_count),
            failed_count: Set(suite.failed_count),
            skipped_count: Set(suite.skipped_count),
            flaky_count: Set(suite.flaky_count),
            duration_ms: Set(suite.duration_ms),
            start_time: Set(suite.start_time),
            created_at: Set(now),
            updated_at: Set(now),
            deleted_at: Set(None),
        };

        let result = model
            .insert(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to insert test suite: {}", e)))?;

        Ok(result)
    }

    /// Insert a new test case.
    pub async fn insert_test_case(&self, test_case: NewTestCase) -> AppResult<test_case::Model> {
        let id = Uuid::now_v7();
        let now = Utc::now();

        let model = TestCaseActiveModel {
            id: Set(id),
            test_suite_id: Set(test_case.suite_id),
            test_job_id: Set(test_case.job_id),
            title: Set(test_case.title),
            full_title: Set(test_case.full_title),
            status: Set(test_case.status),
            duration_ms: Set(test_case.duration_ms),
            retry_count: Set(test_case.retry_count),
            error_message: Set(test_case.error_message),
            sequence: Set(test_case.sequence),
            attachments: Set(test_case.attachments),
            created_at: Set(now),
            updated_at: Set(now),
            deleted_at: Set(None),
        };

        let result = model
            .insert(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to insert test case: {}", e)))?;

        Ok(result)
    }

    /// Get a single test suite by ID.
    pub async fn get_test_suite_by_id(
        &self,
        suite_id: Uuid,
    ) -> AppResult<Option<test_suite::Model>> {
        let result = TestSuite::find_by_id(suite_id)
            .one(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get test suite: {}", e)))?;

        Ok(result)
    }

    /// Get test suites by report ID (through jobs).
    pub async fn get_test_suites_by_report_id(
        &self,
        report_id: Uuid,
    ) -> AppResult<Vec<test_suite::Model>> {
        use crate::entity::test_job as job;
        use sea_orm::{JoinType, RelationTrait};

        let result = TestSuite::find()
            .join(JoinType::InnerJoin, test_suite::Relation::Job.def())
            .filter(job::Column::TestReportId.eq(report_id))
            .order_by_asc(test_suite::Column::Id) // UUIDv7 is time-ordered
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get test suites by report: {}", e)))?;

        Ok(result)
    }

    /// Get test cases by suite ID.
    pub async fn get_test_cases_by_suite_id(
        &self,
        suite_id: Uuid,
    ) -> AppResult<Vec<test_case::Model>> {
        let result = TestCase::find()
            .filter(test_case::Column::TestSuiteId.eq(suite_id))
            .order_by_asc(test_case::Column::Sequence)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get test cases: {}", e)))?;

        Ok(result)
    }

    /// Get test cases by job ID.
    pub async fn get_test_cases_by_job_id(
        &self,
        job_id: Uuid,
    ) -> AppResult<Vec<test_case::Model>> {
        let result = TestCase::find()
            .filter(test_case::Column::TestJobId.eq(job_id))
            .order_by_asc(test_case::Column::Sequence)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get test cases by job: {}", e)))?;

        Ok(result)
    }

    /// Query test suites with pagination.
    pub async fn query_test_suites(
        &self,
        query: &QueryTestSuitesParams,
    ) -> AppResult<(Vec<test_suite::Model>, u64)> {
        let mut select = TestSuite::find();

        if let Some(job_id) = query.job_id {
            select = select.filter(test_suite::Column::TestJobId.eq(job_id));
        }

        // Count total before pagination
        let total = select
            .clone()
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count test suites: {}", e)))?;

        // Apply pagination
        let limit = query.limit.clamp(1, 100) as u64;
        let offset = query.offset.max(0) as u64;

        let suites = select
            .order_by_desc(test_suite::Column::CreatedAt)
            .offset(offset)
            .limit(limit)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to query test suites: {}", e)))?;

        Ok((suites, total))
    }

    /// Query test cases with pagination.
    pub async fn query_test_cases(
        &self,
        query: &QueryTestCasesParams,
    ) -> AppResult<(Vec<test_case::Model>, u64)> {
        let mut select = TestCase::find();

        if let Some(job_id) = query.job_id {
            select = select.filter(test_case::Column::TestJobId.eq(job_id));
        }

        if let Some(suite_id) = query.suite_id {
            select = select.filter(test_case::Column::TestSuiteId.eq(suite_id));
        }

        if let Some(ref status) = query.status {
            select = select.filter(test_case::Column::Status.eq(status));
        }

        // Count total before pagination
        let total = select
            .clone()
            .count(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to count test cases: {}", e)))?;

        // Apply pagination
        let limit = query.limit.clamp(1, 100) as u64;
        let offset = query.offset.max(0) as u64;

        let cases = select
            .order_by_asc(test_case::Column::TestSuiteId)
            .order_by_asc(test_case::Column::Sequence)
            .offset(offset)
            .limit(limit)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to query test cases: {}", e)))?;

        Ok((cases, total))
    }

    /// Search test cases by title within a report.
    /// Returns matching test cases with their suite info.
    pub async fn search_test_cases_by_report(
        &self,
        report_id: Uuid,
        search_query: &str,
        limit: u64,
    ) -> AppResult<Vec<(test_case::Model, test_suite::Model)>> {
        use crate::entity::test_job;
        use sea_orm::prelude::Expr;
        use sea_orm::sea_query::extension::postgres::PgExpr;
        use sea_orm::{JoinType, RelationTrait};

        // Use ILIKE for case-insensitive pattern matching (PostgreSQL)
        let search_pattern = format!("%{}%", search_query);

        // First, get the test cases that match the search
        let test_cases = TestCase::find()
            .join(JoinType::InnerJoin, test_case::Relation::TestSuite.def())
            .join(JoinType::InnerJoin, test_suite::Relation::Job.def())
            .filter(test_job::Column::TestReportId.eq(report_id))
            .filter(
                sea_orm::Condition::any()
                    .add(Expr::col((test_case::Entity, test_case::Column::Title)).ilike(&search_pattern))
                    .add(Expr::col((test_case::Entity, test_case::Column::FullTitle)).ilike(&search_pattern)),
            )
            .order_by_asc(test_case::Column::TestSuiteId)
            .order_by_asc(test_case::Column::Sequence)
            .limit(limit)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to search test cases: {}", e)))?;

        if test_cases.is_empty() {
            return Ok(Vec::new());
        }

        // Collect unique suite IDs
        let suite_ids: Vec<Uuid> = test_cases
            .iter()
            .map(|tc| tc.test_suite_id)
            .collect::<std::collections::HashSet<_>>()
            .into_iter()
            .collect();

        // Fetch the suites
        let suites = TestSuite::find()
            .filter(test_suite::Column::Id.is_in(suite_ids))
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to fetch test suites: {}", e)))?;

        // Build a lookup map
        let suite_map: std::collections::HashMap<Uuid, test_suite::Model> =
            suites.into_iter().map(|s| (s.id, s)).collect();

        // Join test cases with their suites
        let result: Vec<(test_case::Model, test_suite::Model)> = test_cases
            .into_iter()
            .filter_map(|tc| {
                suite_map
                    .get(&tc.test_suite_id)
                    .cloned()
                    .map(|suite| (tc, suite))
            })
            .collect();

        Ok(result)
    }

    /// Batch get test stats for multiple reports.
    /// Aggregates stats from test_suites through jobs.
    /// Returns a HashMap of report_id -> TestStats.
    pub async fn get_test_stats_by_report_ids(
        &self,
        report_ids: &[Uuid],
    ) -> AppResult<std::collections::HashMap<Uuid, crate::models::TestStats>> {
        use sea_orm::{FromQueryResult, Statement};

        if report_ids.is_empty() {
            return Ok(std::collections::HashMap::new());
        }

        #[derive(Debug, FromQueryResult)]
        struct StatsResult {
            report_id: Uuid,
            total: i64,
            passed: i64,
            failed: i64,
            skipped: i64,
            flaky: i64,
            duration_ms: Option<i64>,
            wall_clock_ms: Option<i64>,
        }

        // Build placeholders for two IN clauses (one for each subquery)
        let n = report_ids.len();
        let in_clause_1: String = (1..=n).map(|i| format!("${}", i)).collect::<Vec<_>>().join(", ");
        let in_clause_2: String = (n+1..=2*n).map(|i| format!("${}", i)).collect::<Vec<_>>().join(", ");

        // Use subqueries to aggregate job stats and test_suite stats separately
        // This avoids multiplying job duration by the number of test suites per job
        let sql = format!(
            r#"
            SELECT
                job_stats.report_id,
                COALESCE(suite_stats.total, 0) as total,
                COALESCE(suite_stats.passed, 0) as passed,
                COALESCE(suite_stats.failed, 0) as failed,
                COALESCE(suite_stats.skipped, 0) as skipped,
                COALESCE(suite_stats.flaky, 0) as flaky,
                job_stats.duration_ms,
                job_stats.wall_clock_ms
            FROM (
                -- Aggregate job-level stats (duration, wall clock)
                SELECT
                    j.test_report_id as report_id,
                    SUM(j.duration_ms)::BIGINT as duration_ms,
                    CASE
                        WHEN MIN(j.start_time) IS NOT NULL AND MAX(j.duration_ms) IS NOT NULL THEN
                            (EXTRACT(EPOCH FROM (
                                MAX(j.start_time + (j.duration_ms || ' milliseconds')::interval) - MIN(j.start_time)
                            )) * 1000)::BIGINT
                        ELSE
                            NULL
                    END as wall_clock_ms
                FROM test_jobs j
                WHERE j.test_report_id IN ({})
                GROUP BY j.test_report_id
            ) job_stats
            LEFT JOIN (
                -- Aggregate test suite stats
                SELECT
                    j.test_report_id as report_id,
                    SUM(ts.total_count) as total,
                    SUM(ts.passed_count) as passed,
                    SUM(ts.failed_count) as failed,
                    SUM(ts.skipped_count) as skipped,
                    SUM(ts.flaky_count) as flaky
                FROM test_jobs j
                INNER JOIN test_suites ts ON ts.test_job_id = j.id
                WHERE j.test_report_id IN ({})
                GROUP BY j.test_report_id
            ) suite_stats ON suite_stats.report_id = job_stats.report_id
            "#,
            in_clause_1, in_clause_2
        );

        // Double the values since we use two IN clauses in the query
        let values: Vec<sea_orm::Value> = report_ids
            .iter()
            .chain(report_ids.iter())
            .map(|id| sea_orm::Value::Uuid(Some(*id)))
            .collect();

        let results: Vec<StatsResult> = StatsResult::find_by_statement(Statement::from_sql_and_values(
            sea_orm::DatabaseBackend::Postgres,
            &sql,
            values,
        ))
        .all(self.connection())
        .await
        .map_err(|e| AppError::Database(format!("Failed to get test stats: {}", e)))?;

        let mut stats_map = std::collections::HashMap::new();
        for result in results {
            stats_map.insert(
                result.report_id,
                crate::models::TestStats {
                    total: result.total as i32,
                    passed: result.passed as i32,
                    failed: result.failed as i32,
                    skipped: result.skipped as i32,
                    flaky: result.flaky as i32,
                    duration_ms: result.duration_ms.map(|ms| ms.max(0)),
                    wall_clock_ms: result.wall_clock_ms.map(|ms| ms.max(0)),
                },
            );
        }

        Ok(stats_map)
    }
}
