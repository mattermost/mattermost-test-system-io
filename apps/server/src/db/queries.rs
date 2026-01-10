//! Database query functions for all entities.

use chrono::{DateTime, Utc};
use rusqlite::{params, Connection};
use uuid::Uuid;

use crate::error::{AppError, AppResult};
use crate::models::{
    ExtractionStatus, GitHubContext, Pagination, PaginationParams, Report, ReportStats,
    ReportSummary, TestResult, TestSpec, TestSuite,
};

// ============================================================================
// Report Queries
// ============================================================================

/// Insert a new report.
pub fn insert_report(conn: &Connection, report: &Report) -> AppResult<()> {
    conn.execute(
        "INSERT INTO reports (id, created_at, deleted_at, extraction_status, file_path, framework, framework_version, error_message, has_files, files_deleted_at, github_context)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11)",
        params![
            report.id.to_string(),
            report.created_at.to_rfc3339(),
            report.deleted_at.map(|dt| dt.to_rfc3339()),
            report.extraction_status.as_str(),
            report.file_path.as_str(),
            report.framework.as_deref(),
            report.framework_version.as_deref(),
            report.error_message.as_deref(),
            report.has_files as i32,
            report.files_deleted_at.map(|dt| dt.to_rfc3339()),
            report.github_context.as_ref().and_then(|ctx| ctx.to_json_string()),
        ],
    )
    .map_err(|e| AppError::Database(format!("Failed to insert report: {}", e)))?;

    Ok(())
}

/// Get active report by ID (not soft-deleted).
pub fn get_active_report_by_id(conn: &Connection, id: Uuid) -> AppResult<Option<Report>> {
    let mut stmt = conn
        .prepare(
            "SELECT id, created_at, deleted_at, extraction_status, file_path, framework, framework_version, error_message, has_files, files_deleted_at, github_context
             FROM reports WHERE id = ?1 AND deleted_at IS NULL",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let result = stmt.query_row(params![id.to_string()], |row| {
        Ok(ReportRow {
            id: row.get(0)?,
            created_at: row.get(1)?,
            deleted_at: row.get(2)?,
            extraction_status: row.get(3)?,
            file_path: row.get(4)?,
            framework: row.get(5)?,
            framework_version: row.get(6)?,
            error_message: row.get(7)?,
            has_files: row.get(8)?,
            files_deleted_at: row.get(9)?,
            github_context: row.get(10)?,
        })
    });

    match result {
        Ok(row) => Ok(Some(row_to_report(row)?)),
        Err(rusqlite::Error::QueryReturnedNoRows) => Ok(None),
        Err(e) => Err(AppError::Database(e.to_string())),
    }
}

/// List active reports with pagination.
pub fn list_reports(
    conn: &Connection,
    params: &PaginationParams,
) -> AppResult<(Vec<ReportSummary>, Pagination)> {
    let limit = params.clamped_limit();
    let offset = params.offset();

    // Get total count
    let total: i64 = conn
        .query_row(
            "SELECT COUNT(*) FROM reports WHERE deleted_at IS NULL",
            [],
            |row| row.get(0),
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    // Get reports with stats
    let mut stmt = conn
        .prepare(
            "SELECT r.id, r.created_at, r.extraction_status, r.framework, r.framework_version,
                    s.start_time, s.duration_ms, s.expected, s.skipped, s.unexpected, s.flaky,
                    r.github_context
             FROM reports r
             LEFT JOIN report_stats s ON r.id = s.report_id
             WHERE r.deleted_at IS NULL
             ORDER BY r.created_at DESC
             LIMIT ?1 OFFSET ?2",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let reports = stmt
        .query_map(params![limit, offset], |row| {
            Ok(ReportSummaryRow {
                id: row.get(0)?,
                created_at: row.get(1)?,
                extraction_status: row.get(2)?,
                framework: row.get(3)?,
                framework_version: row.get(4)?,
                start_time: row.get(5)?,
                duration_ms: row.get(6)?,
                expected: row.get(7)?,
                skipped: row.get(8)?,
                unexpected: row.get(9)?,
                flaky: row.get(10)?,
                github_context: row.get(11)?,
            })
        })
        .map_err(|e| AppError::Database(e.to_string()))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| AppError::Database(e.to_string()))?;

    let summaries = reports
        .into_iter()
        .map(row_to_report_summary)
        .collect::<AppResult<Vec<_>>>()?;

    let pagination = Pagination::new(params.page, limit, total as u64);
    Ok((summaries, pagination))
}

/// Update report extraction status.
pub fn update_report_status(
    conn: &Connection,
    id: Uuid,
    status: ExtractionStatus,
    framework: Option<&str>,
    framework_version: Option<&str>,
    error_message: Option<&str>,
) -> AppResult<()> {
    conn.execute(
        "UPDATE reports SET extraction_status = ?1, framework = ?2, framework_version = ?3, error_message = ?4 WHERE id = ?5",
        params![
            status.as_str(),
            framework,
            framework_version,
            error_message,
            id.to_string(),
        ],
    )
    .map_err(|e| AppError::Database(format!("Failed to update report: {}", e)))?;

    Ok(())
}

/// Mark report files as deleted (only sets files_deleted_at timestamp).
/// The has_files field remains unchanged as it tracks whether the report originally had files.
pub fn mark_report_files_deleted(conn: &Connection, id: Uuid) -> AppResult<()> {
    conn.execute(
        "UPDATE reports SET files_deleted_at = ?1 WHERE id = ?2",
        params![Utc::now().to_rfc3339(), id.to_string(),],
    )
    .map_err(|e| AppError::Database(format!("Failed to mark files deleted: {}", e)))?;

    Ok(())
}

// ============================================================================
// Report Stats Queries
// ============================================================================

/// Insert report statistics.
pub fn insert_report_stats(conn: &Connection, stats: &ReportStats) -> AppResult<i64> {
    conn.execute(
        "INSERT INTO report_stats (report_id, start_time, duration_ms, expected, skipped, unexpected, flaky)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)",
        params![
            stats.report_id.to_string(),
            stats.start_time.to_rfc3339(),
            stats.duration_ms,
            stats.expected,
            stats.skipped,
            stats.unexpected,
            stats.flaky,
        ],
    )
    .map_err(|e| AppError::Database(format!("Failed to insert stats: {}", e)))?;

    Ok(conn.last_insert_rowid())
}

/// Get stats for a report.
pub fn get_report_stats(conn: &Connection, report_id: Uuid) -> AppResult<Option<ReportStats>> {
    let mut stmt = conn
        .prepare(
            "SELECT report_id, start_time, duration_ms, expected, skipped, unexpected, flaky
             FROM report_stats WHERE report_id = ?1",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let result = stmt.query_row(params![report_id.to_string()], |row| {
        Ok(StatsRow {
            report_id: row.get(0)?,
            start_time: row.get(1)?,
            duration_ms: row.get(2)?,
            expected: row.get(3)?,
            skipped: row.get(4)?,
            unexpected: row.get(5)?,
            flaky: row.get(6)?,
        })
    });

    match result {
        Ok(row) => Ok(Some(row_to_stats(row)?)),
        Err(rusqlite::Error::QueryReturnedNoRows) => Ok(None),
        Err(e) => Err(AppError::Database(e.to_string())),
    }
}

// ============================================================================
// Test Suite Queries
// ============================================================================

/// Insert a test suite.
pub fn insert_test_suite(conn: &Connection, suite: &TestSuite) -> AppResult<i64> {
    conn.execute(
        "INSERT INTO test_suites (report_id, title, file_path) VALUES (?1, ?2, ?3)",
        params![
            suite.report_id.to_string(),
            suite.title.as_str(),
            suite.file_path.as_str(),
        ],
    )
    .map_err(|e| AppError::Database(format!("Failed to insert suite: {}", e)))?;

    Ok(conn.last_insert_rowid())
}

/// Get test suites for a report with counts and duration.
pub fn get_test_suites(conn: &Connection, report_id: Uuid) -> AppResult<Vec<TestSuite>> {
    let mut stmt = conn
        .prepare(
            "SELECT ts.id, ts.report_id, ts.title, ts.file_path,
                    COUNT(DISTINCT tsp.id) as specs_count,
                    SUM(CASE WHEN tsp.ok = 1 THEN 1 ELSE 0 END) as passed_count,
                    SUM(CASE WHEN tsp.ok = 0 THEN 1 ELSE 0 END) as failed_count,
                    -- Flaky: ok=1 but has at least one failed result
                    (SELECT COUNT(DISTINCT tsp2.id)
                     FROM test_specs tsp2
                     WHERE tsp2.suite_id = ts.id
                       AND tsp2.ok = 1
                       AND EXISTS (
                           SELECT 1 FROM test_results tr2
                           WHERE tr2.spec_id = tsp2.id AND tr2.status = 'failed'
                       )
                    ) as flaky_count,
                    -- Skipped: latest result (max retry) has status='skipped'
                    (SELECT COUNT(DISTINCT tsp3.id)
                     FROM test_specs tsp3
                     WHERE tsp3.suite_id = ts.id
                       AND EXISTS (
                           SELECT 1 FROM test_results tr3
                           WHERE tr3.spec_id = tsp3.id
                             AND tr3.status = 'skipped'
                             AND tr3.retry = (
                                 SELECT MAX(tr4.retry) FROM test_results tr4 WHERE tr4.spec_id = tsp3.id
                             )
                       )
                    ) as skipped_count,
                    COALESCE(SUM(tr.duration_ms), 0) as duration_ms
             FROM test_suites ts
             LEFT JOIN test_specs tsp ON ts.id = tsp.suite_id
             LEFT JOIN test_results tr ON tsp.id = tr.spec_id
             WHERE ts.report_id = ?1
             GROUP BY ts.id
             ORDER BY ts.id",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let suites = stmt
        .query_map(params![report_id.to_string()], |row| {
            let report_id_str: String = row.get(1)?;
            Ok(TestSuite {
                id: Some(row.get(0)?),
                report_id: Uuid::parse_str(&report_id_str).unwrap_or_default(),
                title: row.get(2)?,
                file_path: row.get(3)?,
                specs_count: Some(row.get(4)?),
                passed_count: Some(row.get(5)?),
                failed_count: Some(row.get(6)?),
                flaky_count: Some(row.get(7)?),
                skipped_count: Some(row.get(8)?),
                duration_ms: Some(row.get(9)?),
            })
        })
        .map_err(|e| AppError::Database(e.to_string()))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| AppError::Database(e.to_string()))?;

    Ok(suites)
}

// ============================================================================
// Test Spec Queries
// ============================================================================

/// Get specs with results for a suite.
pub fn get_suite_specs_with_results(
    conn: &Connection,
    suite_id: i64,
) -> AppResult<Vec<crate::models::TestSpecWithResults>> {
    use crate::models::{TestResult, TestSpecWithResults, TestStatus};

    // First get all specs for the suite
    let mut spec_stmt = conn
        .prepare(
            "SELECT id, title, ok, spec_id, file_path, line, col
             FROM test_specs
             WHERE suite_id = ?1
             ORDER BY id",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let specs: Vec<(i64, String, bool, String, String, i32, i32)> = spec_stmt
        .query_map(params![suite_id], |row| {
            Ok((
                row.get(0)?,
                row.get(1)?,
                row.get::<_, i32>(2)? != 0,
                row.get(3)?,
                row.get(4)?,
                row.get(5)?,
                row.get(6)?,
            ))
        })
        .map_err(|e| AppError::Database(e.to_string()))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| AppError::Database(e.to_string()))?;

    // Now get results for each spec
    let mut result_stmt = conn
        .prepare(
            "SELECT id, status, duration_ms, retry, start_time, project_id, project_name, errors_json
             FROM test_results
             WHERE spec_id = ?1
             ORDER BY retry",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let mut result_specs = Vec::new();

    for (id, title, ok, spec_id, file_path, line, column) in specs {
        let results: Vec<TestResult> = result_stmt
            .query_map(params![id], |row| {
                let status_str: String = row.get(1)?;
                let status = match status_str.as_str() {
                    "passed" => TestStatus::Passed,
                    "failed" => TestStatus::Failed,
                    "skipped" => TestStatus::Skipped,
                    "timedOut" => TestStatus::TimedOut,
                    _ => TestStatus::Failed,
                };

                let start_time_str: String = row.get(4)?;
                let start_time = DateTime::parse_from_rfc3339(&start_time_str)
                    .map(|dt| dt.with_timezone(&Utc))
                    .unwrap_or_else(|_| Utc::now());

                Ok(TestResult {
                    id: Some(row.get(0)?),
                    spec_id: id,
                    status,
                    duration_ms: row.get(2)?,
                    retry: row.get(3)?,
                    start_time,
                    project_id: row.get(5)?,
                    project_name: row.get(6)?,
                    errors_json: row.get(7)?,
                })
            })
            .map_err(|e| AppError::Database(e.to_string()))?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|e| AppError::Database(e.to_string()))?;

        result_specs.push(TestSpecWithResults {
            id,
            title,
            ok,
            spec_id,
            file_path,
            line,
            column,
            results,
        });
    }

    Ok(result_specs)
}

/// Insert a test spec.
pub fn insert_test_spec(conn: &Connection, spec: &TestSpec) -> AppResult<i64> {
    conn.execute(
        "INSERT INTO test_specs (suite_id, title, ok, spec_id, file_path, line, col)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)",
        params![
            spec.suite_id,
            spec.title.as_str(),
            spec.ok as i32,
            spec.spec_id.as_str(),
            spec.file_path.as_str(),
            spec.line,
            spec.column,
        ],
    )
    .map_err(|e| AppError::Database(format!("Failed to insert spec: {}", e)))?;

    Ok(conn.last_insert_rowid())
}

// ============================================================================
// Test Result Queries
// ============================================================================

/// Insert a test result.
pub fn insert_test_result(conn: &Connection, result: &TestResult) -> AppResult<i64> {
    conn.execute(
        "INSERT INTO test_results (spec_id, status, duration_ms, retry, start_time, project_id, project_name, errors_json)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)",
        params![
            result.spec_id,
            result.status.as_str(),
            result.duration_ms,
            result.retry,
            result.start_time.to_rfc3339(),
            result.project_id.as_str(),
            result.project_name.as_str(),
            result.errors_json.as_deref(),
        ],
    )
    .map_err(|e| AppError::Database(format!("Failed to insert result: {}", e)))?;

    Ok(conn.last_insert_rowid())
}

// ============================================================================
// Helper Types and Functions
// ============================================================================

struct ReportRow {
    id: String,
    created_at: String,
    deleted_at: Option<String>,
    extraction_status: String,
    file_path: String,
    framework: Option<String>,
    framework_version: Option<String>,
    error_message: Option<String>,
    has_files: i32,
    files_deleted_at: Option<String>,
    github_context: Option<String>,
}

struct ReportSummaryRow {
    id: String,
    created_at: String,
    extraction_status: String,
    framework: Option<String>,
    framework_version: Option<String>,
    start_time: Option<String>,
    duration_ms: Option<i64>,
    expected: Option<i32>,
    skipped: Option<i32>,
    unexpected: Option<i32>,
    flaky: Option<i32>,
    github_context: Option<String>,
}

struct StatsRow {
    report_id: String,
    start_time: String,
    duration_ms: i64,
    expected: i32,
    skipped: i32,
    unexpected: i32,
    flaky: i32,
}

fn row_to_report(row: ReportRow) -> AppResult<Report> {
    let id = Uuid::parse_str(&row.id).map_err(|e| AppError::Database(e.to_string()))?;
    let created_at = DateTime::parse_from_rfc3339(&row.created_at)
        .map_err(|e| AppError::Database(format!("Invalid date: {}", e)))?
        .with_timezone(&Utc);
    let deleted_at = row.deleted_at.and_then(|s| {
        DateTime::parse_from_rfc3339(&s)
            .map(|dt| dt.with_timezone(&Utc))
            .ok()
    });
    let extraction_status = ExtractionStatus::from_str(&row.extraction_status)
        .ok_or_else(|| AppError::Database(format!("Invalid status: {}", row.extraction_status)))?;
    let files_deleted_at = row.files_deleted_at.and_then(|s| {
        DateTime::parse_from_rfc3339(&s)
            .map(|dt| dt.with_timezone(&Utc))
            .ok()
    });
    let github_context = row
        .github_context
        .and_then(|s| GitHubContext::from_json_string(&s));

    Ok(Report {
        id,
        created_at,
        deleted_at,
        extraction_status,
        file_path: row.file_path,
        framework: row.framework,
        framework_version: row.framework_version,
        error_message: row.error_message,
        has_files: row.has_files != 0,
        files_deleted_at,
        github_context,
    })
}

fn row_to_report_summary(row: ReportSummaryRow) -> AppResult<ReportSummary> {
    let id = Uuid::parse_str(&row.id).map_err(|e| AppError::Database(e.to_string()))?;
    let created_at = DateTime::parse_from_rfc3339(&row.created_at)
        .map_err(|e| AppError::Database(format!("Invalid date: {}", e)))?
        .with_timezone(&Utc);
    let extraction_status = ExtractionStatus::from_str(&row.extraction_status)
        .ok_or_else(|| AppError::Database(format!("Invalid status: {}", row.extraction_status)))?;

    let stats = if let Some(st) = row.start_time {
        let start_time = DateTime::parse_from_rfc3339(&st)
            .map_err(|e| AppError::Database(format!("Invalid date: {}", e)))?
            .with_timezone(&Utc);

        Some(ReportStats {
            report_id: id,
            start_time,
            duration_ms: row.duration_ms.unwrap_or(0),
            expected: row.expected.unwrap_or(0),
            skipped: row.skipped.unwrap_or(0),
            unexpected: row.unexpected.unwrap_or(0),
            flaky: row.flaky.unwrap_or(0),
        })
    } else {
        None
    };

    let github_context = row
        .github_context
        .and_then(|s| GitHubContext::from_json_string(&s));

    Ok(ReportSummary {
        id,
        created_at,
        extraction_status,
        framework: row.framework,
        framework_version: row.framework_version,
        stats,
        github_context,
    })
}

fn row_to_stats(row: StatsRow) -> AppResult<ReportStats> {
    let report_id =
        Uuid::parse_str(&row.report_id).map_err(|e| AppError::Database(e.to_string()))?;
    let start_time = DateTime::parse_from_rfc3339(&row.start_time)
        .map_err(|e| AppError::Database(format!("Invalid date: {}", e)))?
        .with_timezone(&Utc);

    Ok(ReportStats {
        report_id,
        start_time,
        duration_ms: row.duration_ms,
        expected: row.expected,
        skipped: row.skipped,
        unexpected: row.unexpected,
        flaky: row.flaky,
    })
}
