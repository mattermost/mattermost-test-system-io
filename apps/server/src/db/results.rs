//! Database queries for suites and cases.

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ColumnTrait, EntityTrait, QueryFilter, QueryOrder, QuerySelect, Set,
};
use serde_json::Value as JsonValue;
use uuid::Uuid;

use crate::entity::case::{
    self as test_case, ActiveModel as TestCaseActiveModel, Entity as TestCase,
};
use crate::entity::suite::{
    self as test_suite, ActiveModel as TestSuiteActiveModel, Entity as TestSuite,
};
use crate::error::{AppError, AppResult};

use super::DbPool;

/// Represents a test suite to be inserted.
pub struct NewTestSuite {
    pub report_id: Uuid,
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
    pub report_id: Uuid,
    pub title: String,
    pub full_title: String,
    pub status: String,
    pub duration_ms: i32,
    pub retry_count: i32,
    pub error_message: Option<String>,
    pub sequence: i32,
    pub attachments: Option<JsonValue>,
}

impl DbPool {
    /// Insert a new test suite.
    pub async fn insert_test_suite(&self, suite: NewTestSuite) -> AppResult<test_suite::Model> {
        let id = Uuid::now_v7();
        let now = Utc::now();

        let model = TestSuiteActiveModel {
            id: Set(id),
            upload_id: Set(suite.report_id),
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
    pub async fn insert_test_case(&self, tc: NewTestCase) -> AppResult<test_case::Model> {
        let id = Uuid::now_v7();
        let now = Utc::now();

        let model = TestCaseActiveModel {
            id: Set(id),
            suite_id: Set(tc.suite_id),
            upload_id: Set(tc.report_id),
            title: Set(tc.title),
            full_title: Set(tc.full_title),
            status: Set(tc.status),
            duration_ms: Set(tc.duration_ms),
            retry_count: Set(tc.retry_count),
            error_message: Set(tc.error_message),
            sequence: Set(tc.sequence),
            attachments: Set(tc.attachments),
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

    /// Get test suites by report ID (through uploads).
    pub async fn get_test_suites_by_report_id(
        &self,
        report_id: Uuid,
    ) -> AppResult<Vec<test_suite::Model>> {
        use crate::entity::report as rpt;
        use sea_orm::{JoinType, RelationTrait};

        let result = TestSuite::find()
            .join(JoinType::InnerJoin, test_suite::Relation::Report.def())
            .filter(rpt::Column::ReportGroupId.eq(report_id))
            .order_by_asc(test_suite::Column::Id) // UUIDv7 is time-ordered
            .all(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to get test suites by report: {}", e))
            })?;

        Ok(result)
    }

    /// Get test cases by suite ID.
    pub async fn get_test_cases_by_suite_id(
        &self,
        suite_id: Uuid,
    ) -> AppResult<Vec<test_case::Model>> {
        let result = TestCase::find()
            .filter(test_case::Column::SuiteId.eq(suite_id))
            .order_by_asc(test_case::Column::Sequence)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to get test cases: {}", e)))?;

        Ok(result)
    }

    /// Get test cases by report ID.
    pub async fn get_test_cases_by_report_id(
        &self,
        report_id: Uuid,
    ) -> AppResult<Vec<test_case::Model>> {
        let result = TestCase::find()
            .filter(test_case::Column::UploadId.eq(report_id))
            .order_by_asc(test_case::Column::Sequence)
            .all(self.connection())
            .await
            .map_err(|e| {
                AppError::Database(format!("Failed to get test cases by report: {}", e))
            })?;

        Ok(result)
    }

    /// Search test cases by title within a report.
    /// Returns matching test cases with their suite info.
    pub async fn search_test_cases_by_report(
        &self,
        report_id: Uuid,
        search_query: &str,
        limit: u64,
    ) -> AppResult<Vec<(test_case::Model, test_suite::Model)>> {
        use crate::entity::report;
        use sea_orm::prelude::Expr;
        use sea_orm::sea_query::extension::postgres::PgExpr;
        use sea_orm::{JoinType, RelationTrait};

        // Use ILIKE for case-insensitive pattern matching (PostgreSQL)
        let search_pattern = format!("%{}%", search_query);

        // First, get the test cases that match the search
        let cases = TestCase::find()
            .join(JoinType::InnerJoin, test_case::Relation::Suite.def())
            .join(JoinType::InnerJoin, test_suite::Relation::Report.def())
            .filter(report::Column::ReportGroupId.eq(report_id))
            .filter(
                sea_orm::Condition::any()
                    .add(
                        Expr::col((test_case::Entity, test_case::Column::Title))
                            .ilike(&search_pattern),
                    )
                    .add(
                        Expr::col((test_case::Entity, test_case::Column::FullTitle))
                            .ilike(&search_pattern),
                    ),
            )
            .order_by_asc(test_case::Column::SuiteId)
            .order_by_asc(test_case::Column::Sequence)
            .limit(limit)
            .all(self.connection())
            .await
            .map_err(|e| AppError::Database(format!("Failed to search test cases: {}", e)))?;

        if cases.is_empty() {
            return Ok(Vec::new());
        }

        // Collect unique suite IDs
        let suite_ids: Vec<Uuid> = cases
            .iter()
            .map(|tc| tc.suite_id)
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
        let result: Vec<(test_case::Model, test_suite::Model)> = cases
            .into_iter()
            .filter_map(|tc| {
                suite_map
                    .get(&tc.suite_id)
                    .cloned()
                    .map(|suite| (tc, suite))
            })
            .collect();

        Ok(result)
    }

    /// Batch get test stats for multiple reports.
    /// Aggregates stats from suites through uploads.
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

        // Build placeholders for two IN clauses (CTE + upload_stats subquery)
        let n = report_ids.len();
        let in_clause_1: String = (1..=n)
            .map(|i| format!("${}", i))
            .collect::<Vec<_>>()
            .join(", ");
        let in_clause_2: String = (n + 1..=2 * n)
            .map(|i| format!("${}", i))
            .collect::<Vec<_>>()
            .join(", ");

        // Deduplicate test cases by full_title per report, picking the latest
        // retry result. This avoids double-counting when tests span multiple
        // uploads (e.g. parallel shards) or have retries.
        let sql = format!(
            r#"
            WITH deduplicated_cases AS (
                SELECT DISTINCT ON (r.report_group_id, tc.full_title)
                    r.report_group_id as report_id,
                    tc.status
                FROM cases tc
                INNER JOIN reports r ON tc.upload_id = r.id
                WHERE r.report_group_id IN ({})
                  AND tc.deleted_at IS NULL
                ORDER BY r.report_group_id, tc.full_title, tc.created_at DESC, tc.retry_count DESC
            )
            SELECT
                report_stats.report_id,
                COALESCE(case_stats.total, 0) as total,
                COALESCE(case_stats.passed, 0) as passed,
                COALESCE(case_stats.failed, 0) as failed,
                COALESCE(case_stats.skipped, 0) as skipped,
                COALESCE(case_stats.flaky, 0) as flaky,
                report_stats.duration_ms,
                report_stats.wall_clock_ms
            FROM (
                -- Aggregate report-level stats (duration, wall clock)
                SELECT
                    r.report_group_id as report_id,
                    SUM(r.duration_ms)::BIGINT as duration_ms,
                    CASE
                        WHEN MIN(r.start_time) IS NOT NULL AND MAX(r.duration_ms) IS NOT NULL THEN
                            (EXTRACT(EPOCH FROM (
                                MAX(r.start_time + (r.duration_ms || ' milliseconds')::interval) - MIN(r.start_time)
                            )) * 1000)::BIGINT
                        ELSE
                            NULL
                    END as wall_clock_ms
                FROM reports r
                WHERE r.report_group_id IN ({})
                GROUP BY r.report_group_id
            ) report_stats
            LEFT JOIN (
                -- Aggregate deduplicated test case stats
                SELECT
                    report_id,
                    COUNT(*) as total,
                    COUNT(*) FILTER (WHERE status IN ('passed', 'flaky')) as passed,
                    COUNT(*) FILTER (WHERE status IN ('failed', 'timedOut')) as failed,
                    COUNT(*) FILTER (WHERE status = 'skipped') as skipped,
                    COUNT(*) FILTER (WHERE status = 'flaky') as flaky
                FROM deduplicated_cases
                GROUP BY report_id
            ) case_stats ON case_stats.report_id = report_stats.report_id
            "#,
            in_clause_1, in_clause_2
        );

        // Double the values since we use two IN clauses in the query
        let values: Vec<sea_orm::Value> = report_ids
            .iter()
            .chain(report_ids.iter())
            .map(|id| sea_orm::Value::Uuid(Some(*id)))
            .collect();

        let results: Vec<StatsResult> = StatsResult::find_by_statement(
            Statement::from_sql_and_values(sea_orm::DatabaseBackend::Postgres, &sql, values),
        )
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

    /// Batch get test stats for individual reports (by report.id, not report_group_id).
    pub async fn get_test_stats_by_individual_report_ids(
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
        }

        let in_clause: String = (1..=report_ids.len())
            .map(|i| format!("${}", i))
            .collect::<Vec<_>>()
            .join(", ");

        let sql = format!(
            r#"
            SELECT
                tc.upload_id as report_id,
                COUNT(*) as total,
                COUNT(*) FILTER (WHERE tc.status IN ('passed', 'flaky')) as passed,
                COUNT(*) FILTER (WHERE tc.status IN ('failed', 'timedOut')) as failed,
                COUNT(*) FILTER (WHERE tc.status = 'skipped') as skipped,
                COUNT(*) FILTER (WHERE tc.status = 'flaky') as flaky,
                SUM(tc.duration_ms)::BIGINT as duration_ms
            FROM cases tc
            WHERE tc.upload_id IN ({})
              AND tc.deleted_at IS NULL
            GROUP BY tc.upload_id
            "#,
            in_clause
        );

        let values: Vec<sea_orm::Value> = report_ids
            .iter()
            .map(|id| sea_orm::Value::Uuid(Some(*id)))
            .collect();

        let results: Vec<StatsResult> = StatsResult::find_by_statement(
            Statement::from_sql_and_values(sea_orm::DatabaseBackend::Postgres, &sql, values),
        )
        .all(self.connection())
        .await
        .map_err(|e| AppError::Database(format!("Failed to get individual test stats: {}", e)))?;

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
                    wall_clock_ms: None,
                },
            );
        }

        Ok(stats_map)
    }
}
