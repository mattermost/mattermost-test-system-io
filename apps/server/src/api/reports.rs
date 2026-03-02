//! Report API handlers.

use actix_web::{HttpResponse, web};
use serde::Serialize;
use tracing::info;
use utoipa::ToSchema;
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::DbPool;
use crate::db::report_groups::InsertReportGroupParams;
use crate::error::{AppError, AppResult};
use crate::models::{
    BeginResponse, CompleteResponse, Framework, ListReportsQuery, ReportDetailResponse,
    ReportGroupingRequest, ReportListResponse, ReportStatus, ReportSummary, UploadStatus,
    UploadSummary,
};

/// Response for test suite (simplified for report-level aggregation).
#[derive(Debug, Serialize, ToSchema)]
pub struct TestSuiteResponse {
    pub id: Uuid,
    /// Short ID for display (timestamp portion of UUIDv7).
    pub short_id: String,
    pub report_id: Uuid,
    /// Report display name (gh_job_name or "Report N").
    pub report_name: Option<String>,
    /// Report number for display (parsed from suffix or sequential).
    pub report_number: i32,
    pub title: String,
    pub file_path: Option<String>,
    pub specs_count: i32,
    pub total_count: i32,
    pub passed_count: i32,
    pub failed_count: i32,
    pub skipped_count: i32,
    pub flaky_count: i32,
    pub duration_ms: i32,
    /// Actual test execution start time from framework JSON.
    pub start_time: Option<chrono::DateTime<chrono::Utc>>,
    /// When the suite was created in the database.
    pub created_at: chrono::DateTime<chrono::Utc>,
}

/// Report entry info for filtering.
#[derive(Debug, Serialize, ToSchema)]
pub struct ReportEntryInfo {
    pub report_id: Uuid,
    pub report_name: String,
    pub report_number: i32,
}

/// Response for report suites endpoint.
#[derive(Debug, Serialize, ToSchema)]
pub struct ReportSuitesResponse {
    pub suites: Vec<TestSuiteResponse>,
    /// List of report entries for filtering (only included when multiple reports exist).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub reports: Option<Vec<ReportEntryInfo>>,
}

/// Response for a single test result (one execution/retry).
#[derive(Debug, Serialize, ToSchema)]
pub struct TestResultResponse {
    pub id: Uuid,
    pub status: String,
    pub duration_ms: i32,
    pub retry: i32,
    pub start_time: String,
    pub project_id: String,
    pub project_name: String,
    pub errors_json: Option<String>,
    /// Attachments (screenshots, videos) for this test result.
    pub attachments: Option<serde_json::Value>,
}

/// Response for a test spec (a logical test with potentially multiple results/retries).
#[derive(Debug, Serialize, ToSchema)]
pub struct TestSpecResponse {
    pub id: Uuid,
    pub title: String,
    pub ok: bool,
    pub spec_id: String,
    pub file_path: String,
    pub line: i32,
    pub column: i32,
    pub results: Vec<TestResultResponse>,
    pub screenshots: Vec<ScreenshotInfo>,
}

/// Screenshot info for a test spec.
#[derive(Debug, Serialize, ToSchema)]
pub struct ScreenshotInfo {
    pub file_path: String,
    pub screenshot_type: String,
}

/// Response for suite specs endpoint.
#[derive(Debug, Serialize, ToSchema)]
pub struct SuiteSpecsResponse {
    pub specs: Vec<TestSpecResponse>,
}

/// Search query parameters.
#[derive(Debug, serde::Deserialize, ToSchema)]
pub struct SearchQuery {
    /// Search query string (matches test case title).
    pub q: String,
    /// Maximum number of results per suite (default: 100, max: 500).
    #[serde(default = "default_search_limit")]
    pub limit: u64,
}

fn default_search_limit() -> u64 {
    100
}

/// A matched test case within a suite.
#[derive(Debug, Serialize, ToSchema)]
pub struct SearchMatchedTestCase {
    pub test_case_id: Uuid,
    pub title: String,
    pub full_title: String,
    pub status: String,
    /// Matching tokens from the search query found in title/full_title.
    pub match_tokens: Vec<String>,
}

/// Search results grouped by suite.
#[derive(Debug, Serialize, ToSchema)]
pub struct SearchSuiteResult {
    pub suite_id: Uuid,
    pub suite_title: String,
    pub suite_file_path: Option<String>,
    pub report_id: Uuid,
    /// Test cases in this suite that match the search query.
    pub matches: Vec<SearchMatchedTestCase>,
}

/// Response for search endpoint.
#[derive(Debug, Serialize, ToSchema)]
pub struct SearchResponse {
    /// The search query.
    pub query: String,
    /// Minimum search length configured on server.
    pub search_min_length: usize,
    /// Total number of matching test cases across all suites.
    pub total_matches: usize,
    /// Results grouped by suite.
    pub results: Vec<SearchSuiteResult>,
}

/// List reports with pagination and filtering.
#[utoipa::path(
    get,
    path = "/reports",
    tag = "Reports",
    params(
        ("limit" = Option<i32>, Query, description = "Results per page (default 20, max 100)"),
        ("offset" = Option<i32>, Query, description = "Pagination offset"),
        ("framework" = Option<String>, Query, description = "Filter by framework"),
        ("status" = Option<String>, Query, description = "Filter by status")
    ),
    responses(
        (status = 200, description = "List of reports", body = ReportListResponse),
    )
)]
pub async fn list_reports(
    pool: web::Data<DbPool>,
    query: web::Query<ListReportsQuery>,
) -> AppResult<HttpResponse> {
    let query = query.into_inner();
    let (reports, total) = pool.list_report_groups(&query).await?;

    // Batch fetch completed report counts and test stats for all report groups
    let report_ids: Vec<uuid::Uuid> = reports.iter().map(|r| r.id).collect();
    let reports_complete_map = pool.count_completed_reports_batch(&report_ids).await?;
    let test_stats_map = pool.get_test_stats_by_report_ids(&report_ids).await?;

    let reports: Vec<ReportSummary> = reports
        .into_iter()
        .map(|r| {
            // Extract short ID (first 13 chars of UUID - timestamp portion)
            let short_id = r.id.to_string()[..13].to_string();

            // Get test stats, only include if there are any tests
            let test_stats = test_stats_map
                .get(&r.id)
                .cloned()
                .and_then(|stats| if stats.total > 0 { Some(stats) } else { None });

            let environment_metadata = crate::models::report::ReportEnvironmentMetadata::from_json(
                r.environment_metadata.as_ref(),
            );
            let environment_metadata = if environment_metadata.is_empty() {
                None
            } else {
                Some(environment_metadata)
            };

            ReportSummary {
                id: r.id,
                short_id,
                framework: Framework::parse(&r.framework).unwrap_or(Framework::Playwright),
                name: r.name,
                status: ReportStatus::parse(&r.status).unwrap_or(ReportStatus::InProgress),
                reports_complete: *reports_complete_map.get(&r.id).unwrap_or(&0),
                test_stats,
                repository: r.repository,
                branch: r.branch,
                commit: r.commit,
                gh_run_id: r.gh_run_id,
                gh_run_attempt: r.gh_run_attempt,
                gh_pr_number: r.gh_pr_number,
                environment_metadata,
                created_at: r.created_at,
            }
        })
        .collect();

    let response = ReportListResponse {
        reports,
        total: total as i64,
        limit: query.limit,
        offset: query.offset,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get test suites for a report.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/suites",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID")
    ),
    responses(
        (status = 200, description = "List of test suites for the report", body = ReportSuitesResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn get_report_suites(
    pool: web::Data<DbPool>,
    path: web::Path<Uuid>,
) -> AppResult<HttpResponse> {
    let report_id = path.into_inner();

    // Verify report group exists
    let _report = pool
        .get_report_group_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    // Get all reports for this report group and parse report numbers
    let entries = pool.get_reports_by_group_id(report_id).await?;

    // Helper to parse suffix number from report name (e.g., "ubuntu-chrome-5" -> Some(5))
    fn parse_suffix_number(name: &str) -> Option<i32> {
        // Try to find a trailing number after a separator (-, _, or space)
        let trimmed = name.trim();
        for sep in ['-', '_', ' '] {
            if let Some(pos) = trimmed.rfind(sep)
                && let Ok(num) = trimmed[pos + 1..].parse::<i32>()
            {
                return Some(num);
            }
        }
        // Also try parsing just the last characters as a number
        let chars: Vec<char> = trimmed.chars().collect();
        for i in (0..chars.len()).rev() {
            let suffix: String = chars[i..].iter().collect();
            if let Ok(num) = suffix.parse::<i32>() {
                return Some(num);
            }
            if !chars[i].is_ascii_digit() {
                break;
            }
        }
        None
    }

    // Build report entry info with parsed or sequential numbers
    let entry_infos: Vec<(Uuid, String, i32)> = entries
        .into_iter()
        .enumerate()
        .map(|(i, r)| {
            let display_name = r
                .gh_job_name
                .clone()
                .unwrap_or_else(|| format!("Report {}", i + 1));

            // Try to parse suffix number, otherwise use sequential (1-based)
            let report_number = parse_suffix_number(&display_name).unwrap_or((i + 1) as i32);

            (r.id, display_name, report_number)
        })
        .collect();

    // Build lookup map: report_id -> (name, number)
    let entry_map: std::collections::HashMap<Uuid, (String, i32)> = entry_infos
        .iter()
        .map(|(id, name, num)| (*id, (name.clone(), *num)))
        .collect();

    // Build reports list for filtering (only if multiple reports)
    let reports_for_response = if entry_infos.len() > 1 {
        Some(
            entry_infos
                .iter()
                .map(|(id, name, num)| ReportEntryInfo {
                    report_id: *id,
                    report_name: name.clone(),
                    report_number: *num,
                })
                .collect(),
        )
    } else {
        None
    };

    // Get all test suites for this report (through individual reports)
    let suites = pool.get_test_suites_by_report_id(report_id).await?;

    let suite_responses: Vec<TestSuiteResponse> = suites
        .into_iter()
        .map(|s| {
            let (report_name, report_number) = entry_map
                .get(&s.upload_id)
                .map(|(n, num)| (Some(n.clone()), *num))
                .unwrap_or((None, 1));

            TestSuiteResponse {
                id: s.id,
                short_id: s.id.to_string()[..13].to_string(),
                report_name,
                report_number,
                report_id: s.upload_id,
                title: s.title,
                file_path: s.file_path,
                specs_count: s.total_count,
                total_count: s.total_count,
                passed_count: s.passed_count,
                failed_count: s.failed_count,
                skipped_count: s.skipped_count,
                flaky_count: s.flaky_count,
                duration_ms: s.duration_ms,
                start_time: s.start_time,
                created_at: s.created_at,
            }
        })
        .collect();

    let response = ReportSuitesResponse {
        suites: suite_responses,
        reports: reports_for_response,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get report group details.
#[utoipa::path(
    get,
    path = "/reports/{report_id}",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID")
    ),
    responses(
        (status = 200, description = "Report details with reports", body = ReportDetailResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn get_report(pool: web::Data<DbPool>, path: web::Path<Uuid>) -> AppResult<HttpResponse> {
    let id = path.into_inner();

    // Try as report group first, then fall back to individual report
    let (report, report_id) = if let Some(rg) = pool.get_report_group_by_id(id).await? {
        (rg, id)
    } else if let Some(individual) = pool.get_report_by_id(id).await? {
        // Individual report ID — look up its parent group
        let group_id = individual.report_group_id;
        let rg = pool
            .get_report_group_by_id(group_id)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Report group {} not found", group_id)))?;
        (rg, group_id)
    } else {
        return Err(AppError::NotFound(format!("Report {}", id)));
    };

    // Get individual reports for this report group
    let report_entries = pool.get_reports_by_group_id(report_id).await?;

    let report_summaries: Vec<UploadSummary> = report_entries
        .into_iter()
        .enumerate()
        .map(|(i, r)| {
            let display_name = r
                .gh_job_name
                .clone()
                .unwrap_or_else(|| format!("Report {}", i + 1));

            UploadSummary {
                id: r.id,
                short_id: r.id.to_string()[..13].to_string(),
                gh_job_id: r.gh_job_id,
                gh_job_name: r.gh_job_name,
                display_name,
                status: UploadStatus::parse(&r.status).unwrap_or(UploadStatus::Pending),
            }
        })
        .collect();

    // Parse environment metadata
    let environment_metadata = crate::models::report::ReportEnvironmentMetadata::from_json(
        report.environment_metadata.as_ref(),
    );
    let environment_metadata = if environment_metadata.is_empty() {
        None
    } else {
        Some(environment_metadata)
    };

    let response = ReportDetailResponse {
        id: report.id,
        framework: Framework::parse(&report.framework).unwrap_or(Framework::Playwright),
        name: report.name,
        status: ReportStatus::parse(&report.status).unwrap_or(ReportStatus::InProgress),
        repository: report.repository,
        branch: report.branch,
        commit: report.commit,
        gh_run_id: report.gh_run_id,
        gh_run_attempt: report.gh_run_attempt,
        gh_pr_number: report.gh_pr_number,
        environment_metadata,
        created_at: report.created_at,
        updated_at: report.updated_at,
        reports: report_summaries,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Path parameters for suite specs endpoint.
#[derive(serde::Deserialize)]
pub struct SuiteSpecsPath {
    pub report_id: Uuid,
    pub suite_id: Uuid,
}

/// Get test specs for a specific suite.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/suites/{suite_id}/specs",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("suite_id" = Uuid, Path, description = "Suite UUID")
    ),
    responses(
        (status = 200, description = "List of test specs for the suite", body = SuiteSpecsResponse),
        (status = 404, description = "Report or suite not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn get_suite_specs(
    pool: web::Data<DbPool>,
    path: web::Path<SuiteSpecsPath>,
) -> AppResult<HttpResponse> {
    let SuiteSpecsPath {
        report_id,
        suite_id,
    } = path.into_inner();

    // Verify report group exists
    let _report = pool
        .get_report_group_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    // Get the suite to get the report_id
    let suite = pool
        .get_test_suite_by_id(suite_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Suite {}", suite_id)))?;

    // Get test cases for this suite
    let test_cases = pool.get_test_cases_by_suite_id(suite_id).await?;

    // Get screenshots for this report
    let screenshots = pool.get_screenshots_by_report_id(suite.upload_id).await?;

    // Group test cases by full_title to combine retries into specs
    use std::collections::HashMap;
    let mut specs_map: HashMap<String, Vec<_>> = HashMap::new();
    for tc in test_cases {
        specs_map.entry(tc.full_title.clone()).or_default().push(tc);
    }

    // Convert to spec responses
    let mut specs: Vec<TestSpecResponse> = specs_map
        .into_iter()
        .map(|(full_title, cases)| {
            // Sort by retry_count to get correct order
            let mut cases = cases;
            cases.sort_by_key(|c| c.retry_count);

            // Use first case for spec-level info
            let first = &cases[0];

            // Determine if spec passed (last result is passed/flaky or all passed)
            // "flaky" status means test passed after retries, so it's also considered passed
            let ok = cases
                .iter()
                .any(|c| c.status == "passed" || c.status == "flaky");

            // Create results from all cases (retries)
            let results: Vec<TestResultResponse> = cases
                .iter()
                .map(|c| TestResultResponse {
                    id: c.id,
                    status: c.status.clone(),
                    duration_ms: c.duration_ms,
                    retry: c.retry_count,
                    start_time: c.created_at.to_rfc3339(),
                    project_id: "default".to_string(),
                    project_name: "default".to_string(),
                    errors_json: c.error_message.as_ref().map(|msg| {
                        // Wrap error message in JSON array format expected by frontend
                        serde_json::to_string(&vec![msg]).unwrap_or_else(|_| "[]".to_string())
                    }),
                    attachments: c.attachments.clone(),
                })
                .collect();

            // Get screenshots linked to test cases in this spec
            // First try to get by test_case_id (preferred), then fallback to name matching
            let case_ids: Vec<_> = cases.iter().map(|c| c.id).collect();
            let mut spec_screenshots: Vec<ScreenshotInfo> = screenshots
                .iter()
                .filter(|s| {
                    // Primary: match by test_case_id (linked during extraction)
                    if let Some(tc_id) = s.case_id
                        && case_ids.contains(&tc_id)
                    {
                        return true;
                    }
                    // Fallback: match by test_name for old/unlinked data
                    let normalized_test_name = s.test_name.replace('/', " > ");
                    full_title == s.test_name
                        || full_title == normalized_test_name
                        || full_title.starts_with(&format!("{} [", s.test_name))
                        || full_title.starts_with(&format!("{} [", normalized_test_name))
                })
                .map(|s| {
                    // Extract screenshot type from filename (e.g., "testStart.png" -> "testStart")
                    let screenshot_type = s
                        .filename
                        .strip_suffix(".png")
                        .or_else(|| s.filename.strip_suffix(".jpg"))
                        .or_else(|| s.filename.strip_suffix(".jpeg"))
                        .unwrap_or(&s.filename)
                        .to_string();
                    ScreenshotInfo {
                        file_path: s.s3_key.clone(),
                        screenshot_type,
                    }
                })
                .collect();

            // Sort screenshots by Detox order: testStart, testFnFailure, testDone
            spec_screenshots.sort_by_key(|s| {
                match s.screenshot_type.as_str() {
                    "testStart" => 0,
                    "testFnFailure" => 1,
                    "testDone" => 2,
                    _ => 3, // Other screenshot types come last
                }
            });

            TestSpecResponse {
                id: first.id,
                title: first.title.clone(),
                ok,
                spec_id: first.id.to_string(),
                file_path: full_title.clone(),
                line: 0,
                column: 0,
                results,
                screenshots: spec_screenshots,
            }
        })
        .collect();

    // Sort specs by their first case's sequence
    specs.sort_by_key(|s| s.results.first().map(|r| r.retry).unwrap_or(0));

    let response = SuiteSpecsResponse { specs };

    Ok(HttpResponse::Ok().json(response))
}

/// Search test cases within a report.
///
/// Searches for test cases matching the query string in their title/full_title.
/// Results are grouped by suite for easier UI rendering.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/search",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("q" = String, Query, description = "Search query string"),
        ("limit" = Option<u64>, Query, description = "Maximum results (default: 100, max: 500)")
    ),
    responses(
        (status = 200, description = "Search results grouped by suite", body = SearchResponse),
        (status = 400, description = "Query too short", body = crate::error::ErrorResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn search_test_cases(
    pool: web::Data<DbPool>,
    config: web::Data<crate::config::Config>,
    path: web::Path<Uuid>,
    query: web::Query<SearchQuery>,
) -> AppResult<HttpResponse> {
    let report_id = path.into_inner();
    let search_query = query.into_inner();
    let search_min_length = config.features.search_min_length;

    // Validate query length
    let q = search_query.q.trim();
    if q.is_empty() || q.len() < search_min_length {
        return Ok(HttpResponse::Ok().json(SearchResponse {
            query: search_query.q,
            search_min_length,
            total_matches: 0,
            results: vec![],
        }));
    }

    // Verify report group exists
    let _report = pool
        .get_report_group_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    // Limit to max 500 results
    let limit = search_query.limit.min(500);

    // Search for test cases
    let results = pool
        .search_test_cases_by_report(report_id, q, limit)
        .await?;

    // Group results by suite and extract match tokens
    let mut suite_map: std::collections::HashMap<Uuid, SearchSuiteResult> =
        std::collections::HashMap::new();

    let query_lower = q.to_lowercase();

    for (tc, suite) in results {
        // Extract match tokens from title and full_title
        let match_tokens = extract_match_tokens(&tc.title, &tc.full_title, &query_lower);

        let matched_case = SearchMatchedTestCase {
            test_case_id: tc.id,
            title: tc.title,
            full_title: tc.full_title,
            status: tc.status,
            match_tokens,
        };

        suite_map
            .entry(suite.id)
            .or_insert_with(|| SearchSuiteResult {
                suite_id: suite.id,
                suite_title: suite.title.clone(),
                suite_file_path: suite.file_path.clone(),
                report_id: tc.upload_id,
                matches: vec![],
            })
            .matches
            .push(matched_case);
    }

    // Convert to vec and count total matches
    let results: Vec<SearchSuiteResult> = suite_map.into_values().collect();
    let total_matches: usize = results.iter().map(|s| s.matches.len()).sum();

    let response = SearchResponse {
        query: search_query.q,
        search_min_length,
        total_matches,
        results,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Extract matching tokens from title and full_title.
/// Returns tokens (words/substrings) that contain the query.
fn extract_match_tokens(title: &str, full_title: &str, query_lower: &str) -> Vec<String> {
    let mut tokens = Vec::new();

    // Check title for matches
    let title_lower = title.to_lowercase();
    if let Some(start) = title_lower.find(query_lower) {
        // Extract the matching portion with some context (the word containing it)
        let token = extract_word_containing_match(title, start, query_lower.len());
        if !tokens.contains(&token) {
            tokens.push(token);
        }
    }

    // Check full_title for additional matches not in title
    let full_title_lower = full_title.to_lowercase();
    if let Some(start) = full_title_lower.find(query_lower) {
        let token = extract_word_containing_match(full_title, start, query_lower.len());
        if !tokens.contains(&token) {
            tokens.push(token);
        }
    }

    tokens
}

/// Extract the word/token containing the match at the given position.
fn extract_word_containing_match(text: &str, match_start: usize, match_len: usize) -> String {
    let chars: Vec<char> = text.chars().collect();
    let char_start = text[..match_start].chars().count();
    let char_end = char_start + text[match_start..match_start + match_len].chars().count();

    // Find word boundaries (spaces, punctuation)
    let word_chars: &[char] = &[' ', '\t', '\n', '>', '<', '|', '[', ']', '(', ')', '{', '}'];

    let mut word_start = char_start;
    while word_start > 0 && !word_chars.contains(&chars[word_start - 1]) {
        word_start -= 1;
    }

    let mut word_end = char_end;
    while word_end < chars.len() && !word_chars.contains(&chars[word_end]) {
        word_end += 1;
    }

    chars[word_start..word_end].iter().collect()
}

/// Extract PR number from branch names like "pr-1234-branch" or "pr-1234-something".
/// Returns the number portion if matched.
fn regex_extract_pr_number(branch: &str) -> Option<&str> {
    let rest = branch.strip_prefix("pr-")?;
    let num_end = rest.find(|c: char| !c.is_ascii_digit())?;
    if num_end == 0 {
        return None;
    }
    Some(&rest[..num_end])
}

/// Get reports grouped by repository for the landing page.
#[utoipa::path(
    get,
    path = "/reports/grouped",
    tag = "Reports",
    responses(
        (status = 200, description = "Reports grouped by repository", body = crate::models::report::GroupedReportsResponse),
    )
)]
pub async fn grouped_reports(pool: web::Data<DbPool>) -> AppResult<HttpResponse> {
    use crate::models::report::{
        Framework as Fw, GroupedReportsResponse, MAX_RUNS_PER_REPO, ReportStatus as Rs,
        RepositoryGroup, RunEntry,
    };
    use std::collections::HashMap;

    let reports = pool.list_all_report_groups_for_grouping().await?;

    // Group by repository name (portion after '/')
    let mut groups_map: HashMap<String, Vec<RunEntry>> = HashMap::new();
    let mut group_latest: HashMap<String, chrono::DateTime<chrono::Utc>> = HashMap::new();
    let mut group_full_repo: HashMap<String, String> = HashMap::new();

    // Batch fetch test stats
    let report_ids: Vec<uuid::Uuid> = reports.iter().map(|r| r.id).collect();
    let test_stats_map = pool.get_test_stats_by_report_ids(&report_ids).await?;

    for r in &reports {
        let full_repo = &r.repository;
        let repo_name = full_repo
            .rsplit('/')
            .next()
            .unwrap_or(full_repo)
            .to_string();

        if repo_name.is_empty() {
            continue; // skip reports without repository
        }

        let branch = &r.branch;
        // Build a URL-friendly branch segment:
        // - "refs/heads/main" -> "main"
        // - "refs/heads/pr-1234-branch" -> "pr-1234" (extract PR number)
        // - "refs/pull/1234/merge" -> "pr-1234"
        // - "refs/tags/v1.0" -> "v1.0"
        let stripped = branch
            .strip_prefix("refs/heads/")
            .or_else(|| branch.strip_prefix("refs/tags/"))
            .unwrap_or(branch);
        let short_branch = if let Some(rest) = branch.strip_prefix("refs/pull/") {
            // refs/pull/1234/merge -> pr-1234
            let pr_num = rest.split('/').next().unwrap_or(rest);
            format!("pr-{}", pr_num)
        } else if let Some(caps) = regex_extract_pr_number(stripped) {
            // pr-1234-branch, pr-1234-something -> pr-1234
            format!("pr-{}", caps)
        } else {
            stripped.to_string()
        };
        let commit = &r.commit;
        let short_sha = if commit.len() >= 7 {
            commit[..7].to_string()
        } else {
            commit.clone()
        };

        let framework = Fw::parse(&r.framework).unwrap_or(Fw::Playwright);
        let url_path = format!(
            "/reports/{}/{}/{}/{}",
            repo_name, short_branch, short_sha, r.name
        );

        let test_stats = test_stats_map
            .get(&r.id)
            .cloned()
            .and_then(|s| if s.total > 0 { Some(s) } else { None });

        let entry = RunEntry {
            report_id: r.id,
            framework,
            name: r.name.clone(),
            status: Rs::parse(&r.status).unwrap_or(Rs::InProgress),
            branch: short_branch.clone(),
            commit: commit.clone(),
            short_sha,
            run_number: None,
            run_attempt: None,
            gh_run_attempt: r.gh_run_attempt.clone(),
            gh_run_id: if r.gh_run_id.is_empty() {
                None
            } else {
                Some(r.gh_run_id.clone())
            },
            gh_pr_number: r.gh_pr_number,
            test_stats,
            created_at: r.created_at,
            url_path,
        };

        let runs = groups_map.entry(repo_name.clone()).or_default();
        if runs.len() < MAX_RUNS_PER_REPO {
            runs.push(entry);
        }

        group_latest
            .entry(repo_name.clone())
            .and_modify(|latest| {
                if r.created_at > *latest {
                    *latest = r.created_at;
                }
            })
            .or_insert(r.created_at);

        group_full_repo
            .entry(repo_name)
            .or_insert(full_repo.clone());
    }

    // Build sorted groups
    let mut groups: Vec<RepositoryGroup> = groups_map
        .into_iter()
        .map(|(repo_name, runs)| {
            let latest = group_latest.get(&repo_name).copied().unwrap_or_default();
            let full_repo = group_full_repo.get(&repo_name).cloned().unwrap_or_default();
            RepositoryGroup {
                repository: full_repo,
                repository_name: repo_name,
                latest_run_at: latest,
                runs,
            }
        })
        .collect();

    groups.sort_by(|a, b| b.latest_run_at.cmp(&a.latest_run_at));

    Ok(HttpResponse::Ok().json(GroupedReportsResponse { groups }))
}

/// Query parameters for the consolidated results endpoint.
#[derive(Debug, serde::Deserialize)]
pub struct ConsolidatedQuery {
    pub repository: String,
    pub branch: String,
    pub commit: String,
    pub name: String,
    pub run_attempt: Option<i32>,
    /// Optional: pin to a specific report group by its UUID.
    /// When provided, only that report group is used instead of the latest one.
    pub gid: Option<Uuid>,
}

/// Get consolidated test results for a specific repo + branch + commit + tool.
pub async fn consolidated_results(
    pool: web::Data<DbPool>,
    query: web::Query<ConsolidatedQuery>,
) -> AppResult<HttpResponse> {
    use crate::models::report::ConsolidatedFilters;
    use crate::services::consolidation::{TestCaseInput, consolidate};
    use std::collections::HashMap;

    let q = query.into_inner();

    // Validate commit length
    if q.commit.len() < 7 {
        return Err(AppError::InvalidInput(
            "commit must be at least 7 characters".to_string(),
        ));
    }

    // Check for commit ambiguity
    let distinct_count = pool.count_distinct_commits(&q.commit).await?;
    if distinct_count > 1 {
        return Err(AppError::InvalidInput(format!(
            "Ambiguous commit prefix '{}' matches {} distinct commits. Use the full 40-character SHA.",
            q.commit, distinct_count
        )));
    }

    // Build repository suffix filter (match name portion after '/')
    let repo_filter = format!("%/{}", q.repository);

    // Expand short branch names to full refs (DB stores refs/heads/... or refs/tags/...)
    // For PR branches like "pr-1234", match any ref containing the PR number
    // (e.g., refs/heads/pr-1234-branch, refs/pull/1234/merge)
    let branch_filter = if q.branch.starts_with("refs/") {
        q.branch.clone()
    } else if let Some(pr_num) = q.branch.strip_prefix("pr-") {
        if pr_num.chars().all(|c| c.is_ascii_digit()) {
            // Use LIKE to match refs/heads/pr-N% or refs/pull/N/%
            format!("refs/%{}%", pr_num)
        } else {
            format!("refs/heads/{}", q.branch)
        }
    } else {
        format!("refs/heads/{}", q.branch)
    };

    // Get matching reports
    let list_query = crate::models::ListReportsQuery {
        framework: None,
        name: Some(q.name.clone()),
        status: None,
        repository: Some(repo_filter),
        branch: Some(branch_filter),
        commit: Some(q.commit.clone()),
        gh_run_attempt: None,
        limit: 100,
        offset: 0,
    };
    let (reports, _) = pool.list_report_groups(&list_query).await?;

    if reports.is_empty() {
        return Err(AppError::NotFound(
            "No reports match the filter".to_string(),
        ));
    }

    // Select which report group(s) to use:
    // - If gid is provided, use only that specific report group
    // - Otherwise, keep only the latest report group per name
    let reports = if let Some(pinned_gid) = q.gid {
        reports
            .into_iter()
            .filter(|r| r.id == pinned_gid)
            .collect::<Vec<_>>()
    } else {
        let mut latest_by_name: HashMap<String, usize> = HashMap::new();
        for (i, r) in reports.iter().enumerate() {
            let run_attempt: i32 = r.gh_run_attempt.parse().unwrap_or(1);
            if let Some(pinned) = q.run_attempt
                && run_attempt != pinned
            {
                continue;
            }
            match latest_by_name.get(&r.name) {
                Some(&idx) if reports[idx].created_at >= r.created_at => {}
                _ => {
                    latest_by_name.insert(r.name.clone(), i);
                }
            }
        }
        let mut kept: Vec<_> = latest_by_name
            .into_values()
            .map(|i| reports[i].clone())
            .collect();
        kept.sort_by(|a, b| b.created_at.cmp(&a.created_at));
        kept
    };

    if reports.is_empty() {
        return Err(AppError::NotFound(
            "No reports match the filter".to_string(),
        ));
    }

    // Build test case inputs from the selected report groups
    let mut inputs: Vec<TestCaseInput> = Vec::new();
    let mut earliest_start: Option<chrono::DateTime<chrono::Utc>> = None;
    let mut latest_end: Option<chrono::DateTime<chrono::Utc>> = None;

    for report in &reports {
        let commit_sha = report.commit.clone();
        let report_run_attempt: i32 = report.gh_run_attempt.parse().unwrap_or(1);

        let entries = pool.get_reports_by_group_id(report.id).await?;
        for entry in &entries {
            // Track wall-clock span from report start_time and duration_ms
            if let Some(start) = entry.start_time {
                earliest_start = Some(
                    earliest_start
                        .map_or(start, |prev: chrono::DateTime<chrono::Utc>| prev.min(start)),
                );
                if let Some(dur) = entry.duration_ms {
                    let end = start + chrono::Duration::milliseconds(dur);
                    latest_end = Some(
                        latest_end.map_or(end, |prev: chrono::DateTime<chrono::Utc>| prev.max(end)),
                    );
                }
            }

            let suites = pool.get_test_suites_by_report_id(report.id).await?;
            for suite in &suites {
                if suite.upload_id != entry.id {
                    continue;
                }
                let cases = pool.get_test_cases_by_suite_id(suite.id).await?;
                for tc in cases {
                    inputs.push(TestCaseInput {
                        report_id: report.id,
                        full_title: tc.full_title,
                        status: tc.status,
                        duration_ms: tc.duration_ms,
                        error_message: tc.error_message,
                        commit_sha: commit_sha.clone(),
                        run_attempt: report_run_attempt,
                        created_at: tc.created_at,
                    });
                }
            }
        }
    }

    // Wall-clock duration: earliest report start to latest report end
    let duration_ms = match (earliest_start, latest_end) {
        (Some(start), Some(end)) => {
            let ms = (end - start).num_milliseconds();
            if ms > 0 { Some(ms) } else { None }
        }
        _ => None,
    };

    let filters = ConsolidatedFilters {
        repository: q.repository,
        target_name: q.branch,
        commit_sha: q.commit,
        tool_name: q.name,
    };

    let result = consolidate(inputs, filters, duration_ms);
    Ok(HttpResponse::Ok().json(result))
}

// ── Begin / Complete handlers ─────────────────────────────────────────────────

/// Signal the start of a test run.
///
/// Creates a report with `in_progress` status if none exists for the grouping key.
/// If a report already exists, returns it without modification (idempotent).
#[utoipa::path(
    post,
    path = "/reports/begin",
    tag = "Reports",
    request_body = ReportGroupingRequest,
    responses(
        (status = 200, description = "Report begun", body = BeginResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn begin(
    auth: ApiKeyAuth,
    pool: web::Data<DbPool>,
    body: web::Json<ReportGroupingRequest>,
) -> AppResult<HttpResponse> {
    let req = body.into_inner();

    // ── Validate required fields ─────────────────────────────────────────
    if req.repository.trim().is_empty() || !req.repository.contains('/') {
        return Err(AppError::InvalidInput(
            "repository is required in org/repo format".to_string(),
        ));
    }
    if req.commit.len() < 7
        || req.commit.len() > 40
        || !req.commit.chars().all(|c| c.is_ascii_hexdigit())
    {
        return Err(AppError::InvalidInput(
            "commit must be a 7-40 character hex SHA".to_string(),
        ));
    }
    if req.name.trim().is_empty()
        || !req
            .name
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
    {
        return Err(AppError::InvalidInput(
            "name is required and must contain only alphanumeric characters, hyphens, and underscores".to_string(),
        ));
    }
    if req.gh_run_id.is_empty() || !req.gh_run_id.chars().all(|c| c.is_ascii_digit()) {
        return Err(AppError::InvalidInput(
            "gh_run_id is required and must be numeric".to_string(),
        ));
    }

    let gh_run_id = &req.gh_run_id;

    // ── Derive gh_run_attempt from OIDC claims (or default "1") ──────────
    let gh_run_attempt = auth
        .caller
        .oidc_claims
        .as_ref()
        .and_then(|c| c.run_attempt.clone())
        .unwrap_or_else(|| "1".to_string());

    if !gh_run_attempt.chars().all(|c| c.is_ascii_digit()) {
        return Err(AppError::InvalidInput(
            "run_attempt must be numeric".to_string(),
        ));
    }

    // Derive branch from OIDC claims if available
    let branch = auth
        .caller
        .oidc_claims
        .as_ref()
        .and_then(|c| c.head_ref.clone().or_else(|| c.git_ref.clone()))
        .unwrap_or_default();

    // ── Validate gh_pr_number for pull_request events ────────────────────
    let is_pr_event = auth
        .caller
        .oidc_claims
        .as_ref()
        .and_then(|c| c.event_name.as_deref())
        .map(|e| e == "pull_request")
        .unwrap_or(false);

    if is_pr_event && req.gh_pr_number.is_none() {
        return Err(AppError::InvalidInput(
            "gh_pr_number is required for pull_request events".to_string(),
        ));
    }

    // ── Upsert or find report ────────────────────────────────────────────
    let report_id = Uuid::now_v7();
    let (report, is_new) = pool
        .upsert_or_find_report_group(InsertReportGroupParams {
            id: report_id,
            framework: req.framework,
            name: &req.name,
            repository: &req.repository,
            branch: &branch,
            commit: &req.commit,
            gh_run_id,
            gh_run_attempt: &gh_run_attempt,
            gh_pr_number: req.gh_pr_number,
            environment_metadata: None,
        })
        .await?;

    info!(
        "Report begin: report_id={}, created={}, repository={}, name={}",
        report.id, is_new, req.repository, req.name
    );

    Ok(HttpResponse::Ok().json(BeginResponse {
        report_id: report.id,
        status: ReportStatus::parse(&report.status).unwrap_or(ReportStatus::InProgress),
        created: is_new,
    }))
}

/// Signal the end of a test run.
///
/// Transitions the report to `completed` status. Returns the number of reports
/// in the report. If no report matches the grouping key, returns 404.
/// Calling twice is safe (idempotent).
#[utoipa::path(
    post,
    path = "/reports/complete",
    tag = "Reports",
    request_body = ReportGroupingRequest,
    responses(
        (status = 200, description = "Report completed", body = CompleteResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "No matching report", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn complete(
    auth: ApiKeyAuth,
    pool: web::Data<DbPool>,
    body: web::Json<ReportGroupingRequest>,
) -> AppResult<HttpResponse> {
    let req = body.into_inner();

    // ── Validate required fields ─────────────────────────────────────────
    if req.repository.trim().is_empty() || !req.repository.contains('/') {
        return Err(AppError::InvalidInput(
            "repository is required in org/repo format".to_string(),
        ));
    }
    if req.commit.len() < 7
        || req.commit.len() > 40
        || !req.commit.chars().all(|c| c.is_ascii_hexdigit())
    {
        return Err(AppError::InvalidInput(
            "commit must be a 7-40 character hex SHA".to_string(),
        ));
    }
    if req.name.trim().is_empty()
        || !req
            .name
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
    {
        return Err(AppError::InvalidInput(
            "name is required and must contain only alphanumeric characters, hyphens, and underscores".to_string(),
        ));
    }
    if req.gh_run_id.is_empty() || !req.gh_run_id.chars().all(|c| c.is_ascii_digit()) {
        return Err(AppError::InvalidInput(
            "gh_run_id is required and must be numeric".to_string(),
        ));
    }

    let gh_run_id = &req.gh_run_id;

    // ── Derive gh_run_attempt from OIDC claims (or default "1") ──────────
    let gh_run_attempt = auth
        .caller
        .oidc_claims
        .as_ref()
        .and_then(|c| c.run_attempt.clone())
        .unwrap_or_else(|| "1".to_string());

    if !gh_run_attempt.chars().all(|c| c.is_ascii_digit()) {
        return Err(AppError::InvalidInput(
            "run_attempt must be numeric".to_string(),
        ));
    }

    // ── Find report by grouping key ──────────────────────────────────────
    let report = pool
        .find_report_group_by_grouping_key(
            &req.repository,
            &req.commit,
            gh_run_id,
            &req.name,
            &gh_run_attempt,
        )
        .await?
        .ok_or_else(|| {
            AppError::NotFound(format!(
                "No report found for repository={}, commit={}, gh_run_id={}, name={}, gh_run_attempt={}",
                req.repository, req.commit, gh_run_id, req.name, gh_run_attempt
            ))
        })?;

    // ── Transition to completed ──────────────────────────────────────────
    let updated = pool
        .update_report_group_status(report.id, ReportStatus::Completed)
        .await?;

    // ── Count reports ────────────────────────────────────────────────────
    let reports_count = pool.count_reports_in_group(report.id).await?;

    info!(
        "Report complete: report_id={}, reports_count={}, repository={}, name={}",
        report.id, reports_count, req.repository, req.name
    );

    Ok(HttpResponse::Ok().json(CompleteResponse {
        report_id: updated.id,
        status: ReportStatus::parse(&updated.status).unwrap_or(ReportStatus::Completed),
        reports_count,
    }))
}

/// List individual reports (not grouped).
pub async fn list_individual_reports(
    pool: web::Data<DbPool>,
    query: web::Query<crate::models::ListReportsQuery>,
) -> AppResult<HttpResponse> {
    use crate::models::report::{IndividualReportListResponse, IndividualReportSummary};

    let q = query.into_inner();
    let limit = q.limit.clamp(1, 100) as u64;
    let offset = q.offset.max(0) as u64;

    let (reports, total) = pool.list_individual_reports(limit, offset).await?;

    // Batch fetch test stats for individual reports
    let report_ids: Vec<uuid::Uuid> = reports.iter().map(|r| r.id).collect();
    let test_stats_map = pool
        .get_test_stats_by_individual_report_ids(&report_ids)
        .await?;

    // Batch fetch parent report groups for git metadata
    let group_ids: Vec<uuid::Uuid> = reports.iter().map(|r| r.report_group_id).collect();
    let mut group_map: std::collections::HashMap<uuid::Uuid, crate::entity::report_group::Model> =
        std::collections::HashMap::new();
    for gid in &group_ids {
        if !group_map.contains_key(gid)
            && let Some(g) = pool.get_report_group_by_id(*gid).await?
        {
            group_map.insert(*gid, g);
        }
    }

    let summaries: Vec<IndividualReportSummary> = reports
        .into_iter()
        .map(|r| {
            let test_stats = test_stats_map
                .get(&r.id)
                .cloned()
                .and_then(|s| if s.total > 0 { Some(s) } else { None });

            let group = group_map.get(&r.report_group_id);

            IndividualReportSummary {
                id: r.id,
                short_id: r.id.to_string()[..13].to_string(),
                report_group_id: r.report_group_id,
                name: r.name,
                status: r.status,
                gh_job_id: r.gh_job_id,
                gh_job_name: r.gh_job_name,
                repository: group.map(|g| g.repository.clone()),
                branch: group.map(|g| g.branch.clone()),
                commit: group.map(|g| g.commit.clone()),
                test_stats,
                duration_ms: r.duration_ms,
                created_at: r.created_at,
            }
        })
        .collect();

    Ok(HttpResponse::Ok().json(IndividualReportListResponse {
        reports: summaries,
        total: total as i64,
        limit: q.limit,
        offset: q.offset,
    }))
}

/// Configure report routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(web::resource("/reports").route(web::get().to(list_reports)))
        .service(web::resource("/reports/individual").route(web::get().to(list_individual_reports)))
        .service(web::resource("/reports/begin").route(web::post().to(begin)))
        .service(
            web::resource("/reports/register")
                .route(web::post().to(super::register::register_report)),
        )
        .service(web::resource("/reports/complete").route(web::post().to(complete)))
        .service(web::resource("/reports/grouped").route(web::get().to(grouped_reports)))
        .service(web::resource("/reports/consolidated").route(web::get().to(consolidated_results)))
        .service(web::resource("/reports/{report_id}").route(web::get().to(get_report)))
        .service(
            web::resource("/reports/{report_id}/suites").route(web::get().to(get_report_suites)),
        )
        .service(
            web::resource("/reports/{report_id}/suites/{suite_id}/specs")
                .route(web::get().to(get_suite_specs)),
        )
        .service(
            web::resource("/reports/{report_id}/search").route(web::get().to(search_test_cases)),
        )
        // Upload routes
        .service(
            web::resource("/reports/upload/{report_group_id}/{report_id}/json")
                .route(web::post().to(super::register::upload_json)),
        )
        .service(
            web::resource("/reports/upload/{report_group_id}/{report_id}/screenshots")
                .route(web::post().to(super::register::upload_screenshots)),
        );
}
