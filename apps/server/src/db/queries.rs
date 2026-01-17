//! Database queries using SeaORM for PostgreSQL.
//!
//! This module provides async database operations for all entities.

use sea_orm::*;
use uuid::Uuid;

use crate::error::AppResult;
use crate::models::{
    DetoxJob as DetoxJobDomain, DetoxScreenshot as DetoxScreenshotDomain, ExtractionStatus,
    GitHubContext, JsonFileType, Pagination, PaginationParams, Report,
    ReportJson as ReportJsonDomain, ReportStats as ReportStatsDomain, ReportSummary,
    ScreenshotType, TestResult, TestSpec as TestSpecDomain, TestSpecWithResults, TestStatus,
    TestSuite as TestSuiteDomain,
};

// ============================================================================
// Report Queries
// ============================================================================

/// Insert a new report.
pub async fn insert_report(db: &DatabaseConnection, report: &Report) -> AppResult<()> {
    let github_json = report
        .github_context
        .as_ref()
        .and_then(|g| serde_json::to_value(g).ok());

    let model = crate::entity::report::ActiveModel {
        id: Set(report.id),
        created_at: Set(report.created_at),
        deleted_at: Set(report.deleted_at),
        extraction_status: Set(report.extraction_status.as_str().to_string()),
        file_path: Set(report.file_path.clone()),
        error_message: Set(report.error_message.clone()),
        has_files: Set(report.has_files),
        files_deleted_at: Set(report.files_deleted_at),
        framework: Set(report.framework.clone()),
        framework_version: Set(report.framework_version.clone()),
        platform: Set(report.platform.clone()),
        github_context: Set(github_json),
    };

    crate::entity::report::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(())
}

/// Get active report by ID (not soft-deleted).
pub async fn get_active_report_by_id(
    db: &DatabaseConnection,
    id: Uuid,
) -> AppResult<Option<Report>> {
    let result = crate::entity::report::Entity::find_by_id(id)
        .filter(crate::entity::report::Column::DeletedAt.is_null())
        .one(db)
        .await?;

    Ok(result.map(|m| model_to_report(&m)))
}

/// List reports with pagination.
pub async fn list_reports(
    db: &DatabaseConnection,
    params: &PaginationParams,
) -> AppResult<(Vec<ReportSummary>, Pagination)> {
    let limit = params.clamped_limit() as u64;
    let offset = params.offset() as u64;

    // Get total count
    let total = crate::entity::report::Entity::find()
        .filter(crate::entity::report::Column::DeletedAt.is_null())
        .count(db)
        .await?;

    // Get reports
    let reports = crate::entity::report::Entity::find()
        .filter(crate::entity::report::Column::DeletedAt.is_null())
        .order_by_desc(crate::entity::report::Column::CreatedAt)
        .offset(offset)
        .limit(limit)
        .all(db)
        .await?;

    let summaries: Vec<ReportSummary> = reports.iter().map(|r| model_to_report(r).into()).collect();
    let pagination = Pagination::new(params.page.unwrap_or(1), params.clamped_limit(), total);

    Ok((summaries, pagination))
}

// ============================================================================
// Report Stats Queries
// ============================================================================

/// Insert report stats.
pub async fn insert_report_stats(
    db: &DatabaseConnection,
    stats: &ReportStatsDomain,
) -> AppResult<()> {
    let model = crate::entity::report_stats::ActiveModel {
        id: NotSet,
        report_id: Set(stats.report_id),
        start_time: Set(stats.start_time),
        duration_ms: Set(stats.duration_ms),
        expected: Set(stats.expected),
        skipped: Set(stats.skipped),
        unexpected: Set(stats.unexpected),
        flaky: Set(stats.flaky),
    };

    crate::entity::report_stats::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(())
}

/// Get report stats.
pub async fn get_report_stats(
    db: &DatabaseConnection,
    report_id: Uuid,
) -> AppResult<Option<ReportStatsDomain>> {
    let result = crate::entity::report_stats::Entity::find()
        .filter(crate::entity::report_stats::Column::ReportId.eq(report_id))
        .one(db)
        .await?;

    Ok(result.map(|m| ReportStatsDomain {
        report_id: m.report_id,
        start_time: m.start_time,
        duration_ms: m.duration_ms,
        expected: m.expected,
        skipped: m.skipped,
        unexpected: m.unexpected,
        flaky: m.flaky,
    }))
}

// ============================================================================
// Report JSON Queries
// ============================================================================

/// Insert or update report JSON data.
/// If a record with the same report_id and file_type exists, it will be updated.
pub async fn upsert_report_json(
    db: &DatabaseConnection,
    report_json: &ReportJsonDomain,
) -> AppResult<i64> {
    let file_type_str = report_json.file_type.as_str().to_string();

    // Check if record already exists
    let existing = crate::entity::report_json::Entity::find()
        .filter(crate::entity::report_json::Column::ReportId.eq(report_json.report_id))
        .filter(crate::entity::report_json::Column::FileType.eq(&file_type_str))
        .one(db)
        .await?;

    if let Some(existing_model) = existing {
        // Update existing record
        let mut active: crate::entity::report_json::ActiveModel = existing_model.into();
        active.data = Set(report_json.data.clone());
        let updated = active.update(db).await?;
        Ok(updated.id)
    } else {
        // Insert new record
        let model = crate::entity::report_json::ActiveModel {
            id: NotSet,
            report_id: Set(report_json.report_id),
            file_type: Set(file_type_str),
            data: Set(report_json.data.clone()),
            created_at: Set(chrono::Utc::now()),
        };

        let result = crate::entity::report_json::Entity::insert(model)
            .exec(db)
            .await?;

        Ok(result.last_insert_id)
    }
}

/// Get report JSON data by report ID and file type.
pub async fn get_report_json(
    db: &DatabaseConnection,
    report_id: Uuid,
    file_type: JsonFileType,
) -> AppResult<Option<ReportJsonDomain>> {
    let result = crate::entity::report_json::Entity::find()
        .filter(crate::entity::report_json::Column::ReportId.eq(report_id))
        .filter(crate::entity::report_json::Column::FileType.eq(file_type.as_str()))
        .one(db)
        .await?;

    Ok(result.map(|m| ReportJsonDomain {
        id: Some(m.id),
        report_id: m.report_id,
        file_type: JsonFileType::parse(&m.file_type).unwrap_or(file_type),
        data: m.data,
        created_at: Some(m.created_at),
    }))
}

/// Get all report JSON data for a report.
#[allow(dead_code)]
pub async fn get_report_json_all(
    db: &DatabaseConnection,
    report_id: Uuid,
) -> AppResult<Vec<ReportJsonDomain>> {
    let results = crate::entity::report_json::Entity::find()
        .filter(crate::entity::report_json::Column::ReportId.eq(report_id))
        .all(db)
        .await?;

    Ok(results
        .into_iter()
        .filter_map(|m| {
            JsonFileType::parse(&m.file_type).map(|ft| ReportJsonDomain {
                id: Some(m.id),
                report_id: m.report_id,
                file_type: ft,
                data: m.data,
                created_at: Some(m.created_at),
            })
        })
        .collect())
}

/// Delete all report JSON data for a report.
#[allow(dead_code)]
pub async fn delete_report_json(db: &DatabaseConnection, report_id: Uuid) -> AppResult<u64> {
    let result = crate::entity::report_json::Entity::delete_many()
        .filter(crate::entity::report_json::Column::ReportId.eq(report_id))
        .exec(db)
        .await?;

    Ok(result.rows_affected)
}

// ============================================================================
// Test Suite Queries
// ============================================================================

/// Insert a test suite.
pub async fn insert_test_suite(db: &DatabaseConnection, suite: &TestSuiteDomain) -> AppResult<i64> {
    let model = crate::entity::test_suite::ActiveModel {
        id: NotSet,
        report_id: Set(suite.report_id),
        title: Set(suite.title.clone()),
        file_path: Set(suite.file_path.clone()),
        specs_count: Set(suite.specs_count),
        passed_count: Set(suite.passed_count),
        failed_count: Set(suite.failed_count),
        flaky_count: Set(suite.flaky_count),
        skipped_count: Set(suite.skipped_count),
        duration_ms: Set(suite.duration_ms),
    };

    let result = crate::entity::test_suite::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(result.last_insert_id)
}

// ============================================================================
// Test Spec Queries
// ============================================================================

/// Insert a test spec.
pub async fn insert_test_spec(db: &DatabaseConnection, spec: &TestSpecDomain) -> AppResult<i64> {
    let model = crate::entity::test_spec::ActiveModel {
        id: NotSet,
        suite_id: Set(spec.suite_id),
        title: Set(spec.title.clone()),
        ok: Set(spec.ok),
        full_title: Set(spec.full_title.clone()),
        file_path: Set(spec.file_path.clone()),
        line: Set(spec.line),
        column: Set(spec.column),
    };

    let result = crate::entity::test_spec::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(result.last_insert_id)
}

// ============================================================================
// Test Result Queries
// ============================================================================

/// Insert a test result.
pub async fn insert_test_result(db: &DatabaseConnection, result: &TestResult) -> AppResult<i64> {
    // Convert errors_json from String to JsonValue for PostgreSQL jsonb
    let errors_json = result
        .errors_json
        .as_ref()
        .and_then(|s| serde_json::from_str::<serde_json::Value>(s).ok());

    let model = crate::entity::test_result::ActiveModel {
        id: NotSet,
        spec_id: Set(result.spec_id),
        status: Set(result.status.as_str().to_string()),
        duration_ms: Set(result.duration_ms),
        retry: Set(result.retry),
        start_time: Set(result.start_time),
        project_id: Set(result.project_id.clone()),
        project_name: Set(result.project_name.clone()),
        errors_json: Set(errors_json),
    };

    let insert_result = crate::entity::test_result::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(insert_result.last_insert_id)
}

// ============================================================================
// Detox Job Queries
// ============================================================================

/// Insert a detox job.
pub async fn insert_detox_job(db: &DatabaseConnection, job: &DetoxJobDomain) -> AppResult<()> {
    let model = crate::entity::detox_job::ActiveModel {
        id: Set(job.id),
        report_id: Set(job.report_id),
        job_name: Set(job.job_name.clone()),
        created_at: Set(job.created_at),
        tests_count: Set(job.tests_count),
        passed_count: Set(job.passed_count),
        failed_count: Set(job.failed_count),
        skipped_count: Set(job.skipped_count),
        duration_ms: Set(job.duration_ms),
    };

    crate::entity::detox_job::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(())
}

/// Get detox job by ID.
pub async fn get_detox_job_by_id(
    db: &DatabaseConnection,
    id: Uuid,
) -> AppResult<Option<DetoxJobDomain>> {
    let result = crate::entity::detox_job::Entity::find_by_id(id)
        .one(db)
        .await?;

    Ok(result.map(model_to_detox_job))
}

// ============================================================================
// Detox Screenshot Queries
// ============================================================================

/// Insert a detox screenshot.
pub async fn insert_detox_screenshot(
    db: &DatabaseConnection,
    screenshot: &DetoxScreenshotDomain,
) -> AppResult<i64> {
    let model = crate::entity::detox_screenshot::ActiveModel {
        id: NotSet,
        job_id: Set(screenshot.job_id),
        test_full_name: Set(screenshot.test_full_name.clone()),
        screenshot_type: Set(screenshot.screenshot_type.as_str().to_string()),
        file_path: Set(screenshot.file_path.clone()),
        created_at: Set(screenshot.created_at),
        deleted_at: Set(screenshot.deleted_at),
    };

    let result = crate::entity::detox_screenshot::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(result.last_insert_id)
}

/// Update report status with framework info.
pub async fn update_report_status(
    db: &DatabaseConnection,
    id: Uuid,
    status: ExtractionStatus,
    framework: Option<&str>,
    framework_version: Option<&str>,
    error_message: Option<&str>,
) -> AppResult<()> {
    let model = crate::entity::report::Entity::find_by_id(id)
        .one(db)
        .await?;

    if let Some(m) = model {
        let mut active: crate::entity::report::ActiveModel = m.into();
        active.extraction_status = Set(status.as_str().to_string());
        if let Some(fw) = framework {
            active.framework = Set(Some(fw.to_string()));
        }
        if let Some(fv) = framework_version {
            active.framework_version = Set(Some(fv.to_string()));
        }
        active.error_message = Set(error_message.map(|s| s.to_string()));
        active.update(db).await?;
    }

    Ok(())
}

/// Get all Detox jobs for a report.
pub async fn get_detox_jobs_by_report_id(
    db: &DatabaseConnection,
    report_id: Uuid,
) -> AppResult<Vec<DetoxJobDomain>> {
    let results = crate::entity::detox_job::Entity::find()
        .filter(crate::entity::detox_job::Column::ReportId.eq(report_id))
        .order_by_asc(crate::entity::detox_job::Column::CreatedAt)
        .all(db)
        .await?;

    Ok(results.into_iter().map(model_to_detox_job).collect())
}

/// Get test suites for a report.
pub async fn get_test_suites(
    db: &DatabaseConnection,
    report_id: Uuid,
) -> AppResult<Vec<TestSuiteDomain>> {
    let results = crate::entity::test_suite::Entity::find()
        .filter(crate::entity::test_suite::Column::ReportId.eq(report_id))
        .order_by_asc(crate::entity::test_suite::Column::Id)
        .all(db)
        .await?;

    Ok(results
        .into_iter()
        .map(|m| TestSuiteDomain {
            id: Some(m.id),
            report_id: m.report_id,
            title: m.title,
            file_path: m.file_path,
            specs_count: m.specs_count,
            passed_count: m.passed_count,
            failed_count: m.failed_count,
            flaky_count: m.flaky_count,
            skipped_count: m.skipped_count,
            duration_ms: m.duration_ms,
        })
        .collect())
}

/// Get specs with results for a suite.
pub async fn get_suite_specs_with_results(
    db: &DatabaseConnection,
    suite_id: i64,
) -> AppResult<Vec<TestSpecWithResults>> {
    let specs = crate::entity::test_spec::Entity::find()
        .filter(crate::entity::test_spec::Column::SuiteId.eq(suite_id))
        .order_by_asc(crate::entity::test_spec::Column::Id)
        .all(db)
        .await?;

    let spec_ids: Vec<i64> = specs.iter().map(|s| s.id).collect();

    // Get all results for these specs
    let results = if spec_ids.is_empty() {
        vec![]
    } else {
        crate::entity::test_result::Entity::find()
            .filter(crate::entity::test_result::Column::SpecId.is_in(spec_ids))
            .all(db)
            .await?
    };

    Ok(specs
        .into_iter()
        .map(|m| {
            // Find results for this spec
            let spec_results: Vec<TestResult> = results
                .iter()
                .filter(|r| r.spec_id == m.id)
                .map(|r| TestResult {
                    id: Some(r.id),
                    spec_id: r.spec_id,
                    status: TestStatus::parse(&r.status),
                    duration_ms: r.duration_ms,
                    retry: r.retry,
                    start_time: r.start_time,
                    project_id: r.project_id.clone(),
                    project_name: r.project_name.clone(),
                    // Convert JsonValue back to String for domain model
                    errors_json: r.errors_json.as_ref().map(|v| v.to_string()),
                })
                .collect();

            TestSpecWithResults {
                id: m.id,
                title: m.title,
                ok: m.ok,
                full_title: m.full_title,
                file_path: m.file_path,
                line: m.line,
                column: m.column,
                results: spec_results,
                screenshots: None,
            }
        })
        .collect())
}

/// Get Detox screenshots by test name.
pub async fn get_detox_screenshots_by_test_name(
    db: &DatabaseConnection,
    job_id: Uuid,
    test_full_name: &str,
) -> AppResult<Vec<DetoxScreenshotDomain>> {
    let results = crate::entity::detox_screenshot::Entity::find()
        .filter(crate::entity::detox_screenshot::Column::JobId.eq(job_id))
        .filter(crate::entity::detox_screenshot::Column::TestFullName.eq(test_full_name))
        .filter(crate::entity::detox_screenshot::Column::DeletedAt.is_null())
        .order_by_asc(crate::entity::detox_screenshot::Column::Id)
        .all(db)
        .await?;

    Ok(results
        .iter()
        .filter_map(model_to_detox_screenshot)
        .collect())
}

/// Combined test data for Detox.
#[derive(Debug, Clone, serde::Serialize, utoipa::ToSchema)]
pub struct CombinedDetoxTest {
    pub job_id: Uuid,
    pub job_name: String,
    pub spec_id: i64,
    pub spec_title: String,
    pub spec_ok: bool,
    pub spec_full_title: String,
    pub result_id: i64,
    pub result_status: String,
    pub result_duration_ms: i64,
}

/// Get combined Detox tests with pagination.
pub async fn get_detox_combined_tests(
    db: &DatabaseConnection,
    report_id: Uuid,
    params: &PaginationParams,
) -> AppResult<(Vec<CombinedDetoxTest>, Pagination)> {
    // Get jobs for this report
    let jobs = crate::entity::detox_job::Entity::find()
        .filter(crate::entity::detox_job::Column::ReportId.eq(report_id))
        .all(db)
        .await?;

    if jobs.is_empty() {
        return Ok((
            Vec::new(),
            Pagination::new(params.page.unwrap_or(1), params.clamped_limit(), 0),
        ));
    }

    // Get suites for this report
    let suites = crate::entity::test_suite::Entity::find()
        .filter(crate::entity::test_suite::Column::ReportId.eq(report_id))
        .all(db)
        .await?;

    let suite_ids: Vec<i64> = suites.iter().map(|s| s.id).collect();

    // Get all specs for these suites
    let specs = if suite_ids.is_empty() {
        vec![]
    } else {
        crate::entity::test_spec::Entity::find()
            .filter(crate::entity::test_spec::Column::SuiteId.is_in(suite_ids))
            .all(db)
            .await?
    };

    let spec_ids: Vec<i64> = specs.iter().map(|s| s.id).collect();

    // Get all results for these specs
    let results = if spec_ids.is_empty() {
        vec![]
    } else {
        crate::entity::test_result::Entity::find()
            .filter(crate::entity::test_result::Column::SpecId.is_in(spec_ids))
            .all(db)
            .await?
    };

    // Build combined tests
    let mut combined: Vec<CombinedDetoxTest> = Vec::new();

    for job in &jobs {
        for spec in &specs {
            for result in results.iter().filter(|r| r.spec_id == spec.id) {
                combined.push(CombinedDetoxTest {
                    job_id: job.id,
                    job_name: job.job_name.clone(),
                    spec_id: spec.id,
                    spec_title: spec.title.clone(),
                    spec_ok: spec.ok,
                    spec_full_title: spec.full_title.clone(),
                    result_id: result.id,
                    result_status: result.status.clone(),
                    result_duration_ms: result.duration_ms,
                });
            }
        }
    }

    // Apply filtering by status if provided
    if let Some(ref status_filter) = params.status {
        combined.retain(|t| t.result_status == *status_filter);
    }

    // Paginate
    let total = combined.len() as i64;
    let limit = params.clamped_limit() as usize;
    let offset = params.offset() as usize;

    let page_data: Vec<CombinedDetoxTest> = combined.into_iter().skip(offset).take(limit).collect();

    Ok((
        page_data,
        Pagination::new(params.page.unwrap_or(1), limit as u32, total as u64),
    ))
}

// ============================================================================
// Helper Functions
// ============================================================================

fn model_to_report(m: &crate::entity::report::Model) -> Report {
    let github_context = m
        .github_context
        .as_ref()
        .and_then(|v| serde_json::from_value::<GitHubContext>(v.clone()).ok());

    Report {
        id: m.id,
        created_at: m.created_at,
        deleted_at: m.deleted_at,
        extraction_status: ExtractionStatus::parse(&m.extraction_status)
            .unwrap_or(ExtractionStatus::Pending),
        file_path: m.file_path.clone(),
        framework: m.framework.clone(),
        framework_version: m.framework_version.clone(),
        platform: m.platform.clone(),
        error_message: m.error_message.clone(),
        has_files: m.has_files,
        files_deleted_at: m.files_deleted_at,
        github_context,
    }
}

fn model_to_detox_job(m: crate::entity::detox_job::Model) -> DetoxJobDomain {
    DetoxJobDomain {
        id: m.id,
        report_id: m.report_id,
        job_name: m.job_name,
        created_at: m.created_at,
        tests_count: m.tests_count,
        passed_count: m.passed_count,
        failed_count: m.failed_count,
        skipped_count: m.skipped_count,
        duration_ms: m.duration_ms,
    }
}

fn model_to_detox_screenshot(
    m: &crate::entity::detox_screenshot::Model,
) -> Option<DetoxScreenshotDomain> {
    ScreenshotType::parse(&m.screenshot_type).map(|st| DetoxScreenshotDomain {
        id: Some(m.id),
        job_id: m.job_id,
        test_full_name: m.test_full_name.clone(),
        screenshot_type: st,
        file_path: m.file_path.clone(),
        created_at: m.created_at,
        deleted_at: m.deleted_at,
    })
}
