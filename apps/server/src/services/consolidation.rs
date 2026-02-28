//! Per-spec result consolidation logic.
//!
//! Pure function — takes a list of test case results and returns
//! a consolidated view by applying priority rules:
//! 1. Latest commit SHA first
//! 2. Latest run attempt within a commit first

use crate::models::report::{
    ConsolidatedFilters, ConsolidatedResultsResponse, ConsolidatedSpec, SpecHistoryEntry,
};
use std::collections::HashMap;
use uuid::Uuid;

/// Input: a single test case result for consolidation.
#[derive(Debug, Clone)]
pub struct TestCaseInput {
    pub report_id: Uuid,
    pub full_title: String,
    pub status: String,
    pub duration_ms: i32,
    pub error_message: Option<String>,
    pub commit_sha: String,
    pub run_attempt: i32,
    pub created_at: chrono::DateTime<chrono::Utc>,
}

/// Consolidate test case results into a single merged view.
///
/// This is a pure function with no database access.
pub fn consolidate(
    inputs: Vec<TestCaseInput>,
    filters: ConsolidatedFilters,
) -> ConsolidatedResultsResponse {
    if inputs.is_empty() {
        return ConsolidatedResultsResponse {
            filters,
            overall_status: "passed".to_string(),
            total_specs: 0,
            passed: 0,
            failed: 0,
            skipped: 0,
            flaky: 0,
            contributing_reports: vec![],
            latest_commit_sha: String::new(),
            latest_run_attempt: 0,
            available_run_attempts: vec![],
            specs: vec![],
        };
    }

    // Collect contributing reports and determine latest commit/attempt
    let mut report_ids: Vec<Uuid> = inputs.iter().map(|i| i.report_id).collect();
    report_ids.sort();
    report_ids.dedup();

    // Find the latest commit (by created_at of its first test case)
    let latest_commit = inputs
        .iter()
        .max_by_key(|i| i.created_at)
        .map(|i| i.commit_sha.clone())
        .unwrap_or_default();

    // Find the latest run attempt for the latest commit
    let latest_attempt = inputs
        .iter()
        .filter(|i| i.commit_sha == latest_commit)
        .map(|i| i.run_attempt)
        .max()
        .unwrap_or(1);

    // Collect all available run attempts for the latest commit
    let mut available_attempts: Vec<i32> = inputs
        .iter()
        .filter(|i| i.commit_sha == latest_commit)
        .map(|i| i.run_attempt)
        .collect();
    available_attempts.sort();
    available_attempts.dedup();

    // Group by full_title → each group is one "spec"
    let mut spec_groups: HashMap<String, Vec<&TestCaseInput>> = HashMap::new();
    for input in &inputs {
        spec_groups
            .entry(input.full_title.clone())
            .or_default()
            .push(input);
    }

    // For each spec, pick the winning result
    let mut specs: Vec<ConsolidatedSpec> = spec_groups
        .into_iter()
        .map(|(full_title, mut cases)| {
            // Sort: latest created_at first (which correlates with latest commit + attempt)
            cases.sort_by(|a, b| {
                b.created_at
                    .cmp(&a.created_at)
                    .then(b.run_attempt.cmp(&a.run_attempt))
            });

            let winner = cases[0];
            let is_from_latest =
                winner.commit_sha == latest_commit && winner.run_attempt == latest_attempt;

            // Build history from all results (already sorted newest first)
            let history: Vec<SpecHistoryEntry> = cases
                .iter()
                .map(|c| SpecHistoryEntry {
                    commit_sha: c.commit_sha.clone(),
                    run_attempt: c.run_attempt,
                    status: c.status.clone(),
                    duration_ms: c.duration_ms,
                    error_message: c.error_message.clone(),
                    created_at: c.created_at.to_rfc3339(),
                })
                .collect();

            ConsolidatedSpec {
                full_title,
                status: winner.status.clone(),
                source_commit_sha: winner.commit_sha.clone(),
                source_run_attempt: winner.run_attempt,
                is_from_latest,
                duration_ms: winner.duration_ms,
                error_message: winner.error_message.clone(),
                history,
            }
        })
        .collect();

    // Sort specs alphabetically by full_title for stable ordering
    specs.sort_by(|a, b| a.full_title.cmp(&b.full_title));

    // Compute overall status and counts
    let mut passed = 0usize;
    let mut failed = 0usize;
    let mut skipped = 0usize;
    let mut flaky = 0usize;

    for spec in &specs {
        match spec.status.as_str() {
            "passed" => passed += 1,
            "failed" | "timedOut" => failed += 1,
            "skipped" => skipped += 1,
            "flaky" => flaky += 1,
            _ => passed += 1,
        }
    }

    let overall_status = if failed > 0 {
        "failed"
    } else if flaky > 0 {
        "flaky"
    } else {
        "passed"
    }
    .to_string();

    ConsolidatedResultsResponse {
        filters,
        overall_status,
        total_specs: specs.len(),
        passed,
        failed,
        skipped,
        flaky,
        contributing_reports: report_ids,
        latest_commit_sha: latest_commit,
        latest_run_attempt: latest_attempt,
        available_run_attempts: available_attempts,
        specs,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Utc;

    fn make_input(
        full_title: &str,
        status: &str,
        sha: &str,
        attempt: i32,
        mins_ago: i64,
    ) -> TestCaseInput {
        TestCaseInput {
            report_id: Uuid::new_v4(),
            full_title: full_title.to_string(),
            status: status.to_string(),
            duration_ms: 100,
            error_message: if status == "failed" {
                Some("assertion failed".to_string())
            } else {
                None
            },
            commit_sha: sha.to_string(),
            run_attempt: attempt,
            created_at: Utc::now() - chrono::Duration::minutes(mins_ago),
        }
    }

    fn default_filters() -> ConsolidatedFilters {
        ConsolidatedFilters {
            repository: "test".to_string(),
            target_name: "main".to_string(),
            commit_sha: "abc1234".to_string(),
            tool_name: "playwright".to_string(),
        }
    }

    #[test]
    fn test_latest_commit_wins() {
        let inputs = vec![
            make_input("spec_a", "failed", "commit_old", 1, 60),
            make_input("spec_a", "passed", "commit_new", 1, 10),
        ];
        let result = consolidate(inputs, default_filters());
        assert_eq!(result.specs[0].status, "passed");
        assert_eq!(result.overall_status, "passed");
    }

    #[test]
    fn test_latest_attempt_wins() {
        let inputs = vec![
            make_input("spec_a", "failed", "commit_a", 1, 20),
            make_input("spec_a", "passed", "commit_a", 2, 10),
        ];
        let result = consolidate(inputs, default_filters());
        assert_eq!(result.specs[0].status, "passed");
    }

    #[test]
    fn test_partial_rerun_fallback() {
        let inputs = vec![
            make_input("spec_a", "passed", "commit_a", 1, 30),
            // spec_a not re-run on commit_b
            make_input("spec_b", "passed", "commit_b", 1, 10),
        ];
        let result = consolidate(inputs, default_filters());
        assert_eq!(result.total_specs, 2);
        assert_eq!(result.overall_status, "passed");

        let spec_a = result
            .specs
            .iter()
            .find(|s| s.full_title == "spec_a")
            .unwrap();
        assert!(!spec_a.is_from_latest); // from older commit
    }

    #[test]
    fn test_overall_failed() {
        let inputs = vec![
            make_input("spec_a", "passed", "sha", 1, 10),
            make_input("spec_b", "failed", "sha", 1, 10),
        ];
        let result = consolidate(inputs, default_filters());
        assert_eq!(result.overall_status, "failed");
        assert_eq!(result.failed, 1);
    }

    #[test]
    fn test_overall_flaky() {
        let inputs = vec![
            make_input("spec_a", "passed", "sha", 1, 10),
            make_input("spec_b", "flaky", "sha", 1, 10),
        ];
        let result = consolidate(inputs, default_filters());
        assert_eq!(result.overall_status, "flaky");
    }

    #[test]
    fn test_is_from_latest_flag() {
        let inputs = vec![
            make_input("spec_a", "passed", "old_sha", 1, 60),
            make_input("spec_b", "passed", "new_sha", 1, 10),
        ];
        let result = consolidate(inputs, default_filters());

        let spec_a = result
            .specs
            .iter()
            .find(|s| s.full_title == "spec_a")
            .unwrap();
        let spec_b = result
            .specs
            .iter()
            .find(|s| s.full_title == "spec_b")
            .unwrap();

        assert!(!spec_a.is_from_latest);
        assert!(spec_b.is_from_latest);
    }

    #[test]
    fn test_empty_input() {
        let result = consolidate(vec![], default_filters());
        assert_eq!(result.total_specs, 0);
        assert_eq!(result.overall_status, "passed");
    }
}
