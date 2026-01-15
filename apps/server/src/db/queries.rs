//! Database query functions for all entities.

use chrono::{DateTime, Utc};
use rusqlite::{params, Connection};
use uuid::Uuid;

use crate::error::{AppError, AppResult};
use crate::models::{
    DetoxJob, DetoxScreenshot, ExtractionStatus, GitHubContext, Pagination, PaginationParams,
    Report, ReportStats, ReportSummary, ScreenshotType, TestResult, TestSpec, TestSuite,
};

// ============================================================================
// Report Queries
// ============================================================================

/// Insert a new report.
pub fn insert_report(conn: &Connection, report: &Report) -> AppResult<()> {
    conn.execute(
        "INSERT INTO reports (id, created_at, deleted_at, extraction_status, file_path, framework, framework_version, platform, error_message, has_files, files_deleted_at, github_context)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12)",
        params![
            report.id.to_string(),
            report.created_at.to_rfc3339(),
            report.deleted_at.map(|dt| dt.to_rfc3339()),
            report.extraction_status.as_str(),
            report.file_path.as_str(),
            report.framework.as_deref(),
            report.framework_version.as_deref(),
            report.platform.as_deref(),
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
            "SELECT id, created_at, deleted_at, extraction_status, file_path, framework, framework_version, platform, error_message, has_files, files_deleted_at, github_context
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
            platform: row.get(7)?,
            error_message: row.get(8)?,
            has_files: row.get(9)?,
            files_deleted_at: row.get(10)?,
            github_context: row.get(11)?,
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

    // Get reports with stats and platform
    let mut stmt = conn
        .prepare(
            "SELECT r.id, r.created_at, r.extraction_status, r.framework, r.framework_version,
                    r.platform,
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
                platform: row.get(5)?,
                start_time: row.get(6)?,
                duration_ms: row.get(7)?,
                expected: row.get(8)?,
                skipped: row.get(9)?,
                unexpected: row.get(10)?,
                flaky: row.get(11)?,
                github_context: row.get(12)?,
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
                    -- Failed: ok=0 AND final result is NOT skipped
                    (SELECT COUNT(DISTINCT tsp_f.id)
                     FROM test_specs tsp_f
                     WHERE tsp_f.suite_id = ts.id
                       AND tsp_f.ok = 0
                       AND NOT EXISTS (
                           SELECT 1 FROM test_results tr_f
                           WHERE tr_f.spec_id = tsp_f.id
                             AND tr_f.status = 'skipped'
                             AND tr_f.retry = (
                                 SELECT MAX(tr_f2.retry) FROM test_results tr_f2 WHERE tr_f2.spec_id = tsp_f.id
                             )
                       )
                    ) as failed_count,
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
            "SELECT id, title, ok, full_title, file_path, line, col
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

    for (id, title, ok, full_title, file_path, line, column) in specs {
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
            full_title,
            file_path,
            line,
            column,
            results,
            screenshots: None,
        });
    }

    Ok(result_specs)
}

/// Insert a test spec.
pub fn insert_test_spec(conn: &Connection, spec: &TestSpec) -> AppResult<i64> {
    conn.execute(
        "INSERT INTO test_specs (suite_id, title, ok, full_title, file_path, line, col)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)",
        params![
            spec.suite_id,
            spec.title.as_str(),
            spec.ok as i32,
            spec.full_title.as_str(),
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
    platform: Option<String>,
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
    platform: Option<String>,
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
    let extraction_status = ExtractionStatus::parse(&row.extraction_status)
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
        platform: row.platform,
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
    let extraction_status = ExtractionStatus::parse(&row.extraction_status)
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
        platform: row.platform,
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

// ============================================================================
// Detox Job Queries (T007)
// ============================================================================

/// Insert a new Detox job.
pub fn insert_detox_job(conn: &Connection, job: &DetoxJob) -> AppResult<()> {
    conn.execute(
        "INSERT INTO detox_jobs (id, report_id, job_name, created_at, tests_count, passed_count, failed_count, skipped_count, duration_ms)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)",
        params![
            job.id.to_string(),
            job.report_id.to_string(),
            job.job_name.as_str(),
            job.created_at.to_rfc3339(),
            job.tests_count,
            job.passed_count,
            job.failed_count,
            job.skipped_count,
            job.duration_ms,
        ],
    )
    .map_err(|e| AppError::Database(format!("Failed to insert detox job: {}", e)))?;

    Ok(())
}

/// Get Detox job by ID.
pub fn get_detox_job_by_id(conn: &Connection, id: Uuid) -> AppResult<Option<DetoxJob>> {
    let mut stmt = conn
        .prepare(
            "SELECT id, report_id, job_name, created_at, tests_count, passed_count, failed_count, skipped_count, duration_ms
             FROM detox_jobs WHERE id = ?1",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let result = stmt.query_row(params![id.to_string()], row_to_detox_job);

    match result {
        Ok(job) => Ok(Some(job?)),
        Err(rusqlite::Error::QueryReturnedNoRows) => Ok(None),
        Err(e) => Err(AppError::Database(e.to_string())),
    }
}

/// Get Detox jobs by report ID.
pub fn get_detox_jobs_by_report_id(conn: &Connection, report_id: Uuid) -> AppResult<Vec<DetoxJob>> {
    let mut stmt = conn
        .prepare(
            "SELECT id, report_id, job_name, created_at, tests_count, passed_count, failed_count, skipped_count, duration_ms
             FROM detox_jobs WHERE report_id = ?1 ORDER BY job_name",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let jobs = stmt
        .query_map(params![report_id.to_string()], row_to_detox_job)
        .map_err(|e| AppError::Database(e.to_string()))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| AppError::Database(e.to_string()))?;

    jobs.into_iter().collect()
}

fn row_to_detox_job(row: &rusqlite::Row<'_>) -> Result<AppResult<DetoxJob>, rusqlite::Error> {
    let id_str: String = row.get(0)?;
    let report_id_str: String = row.get(1)?;
    let job_name: String = row.get(2)?;
    let created_at_str: String = row.get(3)?;
    let tests_count: i32 = row.get(4)?;
    let passed_count: i32 = row.get(5)?;
    let failed_count: i32 = row.get(6)?;
    let skipped_count: i32 = row.get(7)?;
    let duration_ms: i64 = row.get(8)?;

    Ok((|| {
        let id = Uuid::parse_str(&id_str).map_err(|e| AppError::Database(e.to_string()))?;
        let report_id =
            Uuid::parse_str(&report_id_str).map_err(|e| AppError::Database(e.to_string()))?;
        let created_at = DateTime::parse_from_rfc3339(&created_at_str)
            .map_err(|e| AppError::Database(format!("Invalid date: {}", e)))?
            .with_timezone(&Utc);

        Ok(DetoxJob {
            id,
            report_id,
            job_name,
            created_at,
            tests_count,
            passed_count,
            failed_count,
            skipped_count,
            duration_ms,
        })
    })())
}

// ============================================================================
// Detox Screenshot Queries (T008)
// ============================================================================

/// Insert a new Detox screenshot.
pub fn insert_detox_screenshot(conn: &Connection, screenshot: &DetoxScreenshot) -> AppResult<i64> {
    conn.execute(
        "INSERT INTO detox_screenshots (job_id, test_full_name, screenshot_type, file_path, created_at, deleted_at)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6)",
        params![
            screenshot.job_id.to_string(),
            screenshot.test_full_name.as_str(),
            screenshot.screenshot_type.as_str(),
            screenshot.file_path.as_str(),
            screenshot.created_at.to_rfc3339(),
            screenshot.deleted_at.map(|dt| dt.to_rfc3339()),
        ],
    )
    .map_err(|e| AppError::Database(format!("Failed to insert detox screenshot: {}", e)))?;

    Ok(conn.last_insert_rowid())
}

/// Get screenshots by test full name (only active, not soft-deleted).
pub fn get_detox_screenshots_by_test_name(
    conn: &Connection,
    job_id: Uuid,
    test_full_name: &str,
) -> AppResult<Vec<DetoxScreenshot>> {
    let mut stmt = conn
        .prepare(
            "SELECT id, job_id, test_full_name, screenshot_type, file_path, created_at, deleted_at
             FROM detox_screenshots WHERE job_id = ?1 AND test_full_name = ?2 AND deleted_at IS NULL
             ORDER BY CASE screenshot_type WHEN 'testStart' THEN 0 WHEN 'testFnFailure' THEN 1 END, id",
        )
        .map_err(|e| AppError::Database(e.to_string()))?;

    let screenshots = stmt
        .query_map(
            params![job_id.to_string(), test_full_name],
            row_to_detox_screenshot,
        )
        .map_err(|e| AppError::Database(e.to_string()))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| AppError::Database(e.to_string()))?;

    screenshots.into_iter().collect()
}

/// Mark all Detox screenshots for a report as deleted (soft delete).
/// This is called during cleanup when report files are deleted.
pub fn mark_detox_screenshots_deleted_by_report_id(
    conn: &Connection,
    report_id: Uuid,
) -> AppResult<usize> {
    let rows_affected = conn
        .execute(
            "UPDATE detox_screenshots
             SET deleted_at = ?1
             WHERE job_id IN (SELECT id FROM detox_jobs WHERE report_id = ?2)
             AND deleted_at IS NULL",
            params![Utc::now().to_rfc3339(), report_id.to_string()],
        )
        .map_err(|e| AppError::Database(format!("Failed to mark screenshots deleted: {}", e)))?;

    Ok(rows_affected)
}

/// Get combined test results for a Detox report with pagination, status filter, and search.
/// T035: Implement combined test query joining test_specs with detox_jobs.
pub fn get_detox_combined_tests(
    conn: &Connection,
    report_id: Uuid,
    pagination: &PaginationParams,
    status_filter: Option<&str>,
    search_filter: Option<&str>,
) -> AppResult<(Vec<crate::api::detox::DetoxCombinedTestResult>, Pagination)> {
    use crate::api::detox::DetoxCombinedTestResult;

    let page = pagination.page.max(1);
    let limit = pagination.clamped_limit();
    let offset = pagination.offset();

    // Build WHERE clause for filters
    let mut conditions = vec!["dj.report_id = ?1".to_string()];
    let mut param_idx = 2;

    if let Some(status) = status_filter {
        conditions.push(format!("tr.status = ?{}", param_idx));
        param_idx += 1;
        let _ = status; // Used below in params
    }

    if let Some(search) = search_filter {
        conditions.push(format!(
            "(ts.title LIKE ?{} OR ts.full_title LIKE ?{})",
            param_idx,
            param_idx + 1
        ));
        let _ = search; // Used below in params
    }

    let where_clause = conditions.join(" AND ");

    // Build the query
    let sql = format!(
        r#"
        SELECT
            ts.id,
            ts.title,
            ts.full_title,
            tr.status,
            tr.duration_ms,
            tr.error_message,
            dj.id as job_id,
            dj.job_name,
            tsu.title as suite_title,
            (SELECT COUNT(*) FROM detox_screenshots ds WHERE ds.test_full_name = REPLACE(REPLACE(ts.full_title, '/', '_'), '"', '_') AND ds.job_id = dj.id AND ds.deleted_at IS NULL) > 0 as has_screenshots
        FROM test_specs ts
        JOIN test_results tr ON tr.spec_id = ts.id
        JOIN test_suites tsu ON ts.suite_id = tsu.id
        JOIN reports r ON tsu.report_id = r.id
        JOIN detox_jobs dj ON dj.report_id = r.id
        WHERE {}
        ORDER BY dj.job_name ASC, ts.id ASC
        LIMIT ?{} OFFSET ?{}
        "#,
        where_clause,
        param_idx,
        param_idx + 1
    );

    // Build parameters dynamically
    let report_id_str = report_id.to_string();
    let search_pattern = search_filter.map(|s| format!("%{}%", s));

    let mut stmt = conn
        .prepare(&sql)
        .map_err(|e| AppError::Database(format!("Failed to prepare query: {}", e)))?;

    // Execute query with appropriate params based on filters
    let results: Vec<DetoxCombinedTestResult> = match (status_filter, &search_pattern) {
        (Some(status), Some(search)) => {
            let rows = stmt
                .query_map(
                    params![report_id_str, status, search, search, limit, offset],
                    row_to_combined_test,
                )
                .map_err(|e| AppError::Database(format!("Failed to query tests: {}", e)))?;
            rows.filter_map(|r| r.ok().and_then(|r| r.ok())).collect()
        }
        (Some(status), None) => {
            let rows = stmt
                .query_map(
                    params![report_id_str, status, limit, offset],
                    row_to_combined_test,
                )
                .map_err(|e| AppError::Database(format!("Failed to query tests: {}", e)))?;
            rows.filter_map(|r| r.ok().and_then(|r| r.ok())).collect()
        }
        (None, Some(search)) => {
            let rows = stmt
                .query_map(
                    params![report_id_str, search, search, limit, offset],
                    row_to_combined_test,
                )
                .map_err(|e| AppError::Database(format!("Failed to query tests: {}", e)))?;
            rows.filter_map(|r| r.ok().and_then(|r| r.ok())).collect()
        }
        (None, None) => {
            let rows = stmt
                .query_map(params![report_id_str, limit, offset], row_to_combined_test)
                .map_err(|e| AppError::Database(format!("Failed to query tests: {}", e)))?;
            rows.filter_map(|r| r.ok().and_then(|r| r.ok())).collect()
        }
    };

    // Get total count for pagination
    let count_sql = format!(
        r#"
        SELECT COUNT(*)
        FROM test_specs ts
        JOIN test_results tr ON tr.spec_id = ts.id
        JOIN test_suites tsu ON ts.suite_id = tsu.id
        JOIN reports r ON ts.report_id = r.id
        JOIN detox_jobs dj ON dj.report_id = r.id
        WHERE {}
        "#,
        where_clause
    );

    let total: i64 = match (status_filter, &search_pattern) {
        (Some(status), Some(search)) => conn
            .query_row(
                &count_sql,
                params![report_id_str, status, search, search],
                |row| row.get(0),
            )
            .unwrap_or(0),
        (Some(status), None) => conn
            .query_row(&count_sql, params![report_id_str, status], |row| row.get(0))
            .unwrap_or(0),
        (None, Some(search)) => conn
            .query_row(&count_sql, params![report_id_str, search, search], |row| {
                row.get(0)
            })
            .unwrap_or(0),
        (None, None) => conn
            .query_row(&count_sql, params![report_id_str], |row| row.get(0))
            .unwrap_or(0),
    };

    Ok((results, Pagination::new(page, limit, total as u64)))
}

fn row_to_combined_test(
    row: &rusqlite::Row<'_>,
) -> Result<AppResult<crate::api::detox::DetoxCombinedTestResult>, rusqlite::Error> {
    use crate::api::detox::DetoxCombinedTestResult;

    let id: i64 = row.get(0)?;
    let title: String = row.get(1)?;
    let full_title: String = row.get(2)?;
    let status: String = row.get(3)?;
    let duration_ms: i64 = row.get(4)?;
    let error_message: Option<String> = row.get(5)?;
    let job_id_str: String = row.get(6)?;
    let job_name: String = row.get(7)?;
    let suite_title: Option<String> = row.get(8)?;
    let has_screenshots: bool = row.get(9)?;

    Ok(Ok(DetoxCombinedTestResult {
        id,
        title,
        full_title,
        status,
        duration_ms,
        error_message,
        job_id: job_id_str,
        job_name,
        suite_title,
        has_screenshots,
    }))
}

fn row_to_detox_screenshot(
    row: &rusqlite::Row<'_>,
) -> Result<AppResult<DetoxScreenshot>, rusqlite::Error> {
    let id: i64 = row.get(0)?;
    let job_id_str: String = row.get(1)?;
    let test_full_name: String = row.get(2)?;
    let screenshot_type_str: String = row.get(3)?;
    let file_path: String = row.get(4)?;
    let created_at_str: String = row.get(5)?;
    let deleted_at_str: Option<String> = row.get(6)?;

    Ok((|| {
        let job_id = Uuid::parse_str(&job_id_str).map_err(|e| AppError::Database(e.to_string()))?;
        let screenshot_type = ScreenshotType::parse(&screenshot_type_str).ok_or_else(|| {
            AppError::Database(format!("Invalid screenshot type: {}", screenshot_type_str))
        })?;
        let created_at = DateTime::parse_from_rfc3339(&created_at_str)
            .map_err(|e| AppError::Database(format!("Invalid date: {}", e)))?
            .with_timezone(&Utc);
        let deleted_at = deleted_at_str.and_then(|s| {
            DateTime::parse_from_rfc3339(&s)
                .map(|dt| dt.with_timezone(&Utc))
                .ok()
        });

        Ok(DetoxScreenshot {
            id: Some(id),
            job_id,
            test_full_name,
            screenshot_type,
            file_path,
            created_at,
            deleted_at,
        })
    })())
}
