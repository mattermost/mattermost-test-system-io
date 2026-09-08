package triageassessment

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// A group marked completed can still be overfull or have a failed extra shard.
// Completion here means exactly the declared shard count, all successfully
// ingested. Metadata alone does not prove the original upload's provenance.
const completeGroupSQL = `g.status='completed' AND g.total_reports_expected > 0
 AND (SELECT count(*) FROM reports cr WHERE cr.report_group_id=g.id)=g.total_reports_expected
 AND NOT EXISTS (SELECT 1 FROM reports cr WHERE cr.report_group_id=g.id AND
   (cr.status <> 'complete' OR coalesce(cr.error_message,'') <> '' OR cr.json_upload_status IN ('failed','timedout')
    OR cr.total_cases <> (SELECT count(DISTINCT (cs.id,ct.full_title,coalesce(ct.project,'')))
      FROM suites cs JOIN test_cases ct ON ct.suite_id=cs.id WHERE cs.report_id=cr.id)))`

const observationColumns = `coalesce(tc.stable_key,''), coalesce(tc.file,''), tc.full_title,
 coalesce(tc.project,''), g.framework, g.id::text, g.commit_sha, g.created_at,
 bool_or(tc.status IN ('failed','timedOut','interrupted','flaky') OR coalesce(tc.run_failed,false) OR coalesce(tc.attempts_failed,0)>0),
 bool_or(tc.status IN ('passed','flaky'))`
const observationJoins = ` FROM report_groups g JOIN reports r ON r.report_group_id=g.id
 JOIN suites s ON s.report_id=r.id JOIN test_cases tc ON tc.suite_id=s.id `
const observationGroup = ` GROUP BY tc.stable_key, tc.file, tc.full_title, tc.project, g.framework, g.id, g.commit_sha, g.created_at
 ORDER BY tc.stable_key, tc.file, tc.full_title, tc.project, g.id`

func loadAssessment(ctx context.Context, tx pgx.Tx, selector Selector, freshness time.Duration) (Assessment, error) {
	a := Assessment{Run: Run{Selector: selector}, Outcome: Unknown, PolicyVersion: policyVersion,
		Tests: []Test{}, Reasons: []string{"shadow_mode_no_automatic_clearance", "historical_upload_source_not_proven", "github_job_conclusion_not_verified"},
		BaselinePolicy: BaselinePolicy{WindowHours: int(baselineWindow.Hours()), FreshnessHours: int(freshness.Hours()), MinimumDistinctCommits: minimumBaselineRuns}}
	var created time.Time
	var framework string
	var complete, claimsMismatch bool
	var reportedTests, reportedFailures int64
	err := tx.QueryRow(ctx, `SELECT g.id::text,g.created_at,g.framework,coalesce((`+completeGroupSQL+`),false),
 coalesce((SELECT sum(total_cases) FROM reports WHERE report_group_id=g.id),0),
 coalesce((SELECT sum(failed_cases+flaky_cases) FROM reports WHERE report_group_id=g.id),0),
 EXISTS(SELECT 1 FROM reports r JOIN oidc_claims c ON c.report_id=r.id WHERE r.report_group_id=g.id AND
  (c.issuer <> 'https://token.actions.githubusercontent.com' OR c.repository IS DISTINCT FROM g.repository OR c.raw_claims->>'sha' IS DISTINCT FROM g.commit_sha))
 FROM report_groups g WHERE g.repository=$1 AND g.commit_sha=$2 AND g.gh_run_id=$3 AND g.gh_run_attempt=$4 AND g.name=$5`,
		selector.Repository, selector.CommitSHA, selector.GHRunID, selector.GHRunAttempt, selector.Name).
		Scan(&a.Run.ReportGroupID, &created, &framework, &complete, &reportedTests, &reportedFailures, &claimsMismatch)
	if errors.Is(err, pgx.ErrNoRows) {
		a.Reasons = append(a.Reasons, "exact_run_not_found")
		return a, nil
	}
	if err != nil {
		return a, err
	}
	current, err := observations(ctx, tx, `SELECT `+observationColumns+observationJoins+` WHERE g.id=$1`+observationGroup, a.Run.ReportGroupID)
	if err != nil {
		return a, err
	}
	keys := []string{}
	anyPassed := false
	for _, row := range current {
		anyPassed = anyPassed || row.passed
		if row.failed {
			keys = append(keys, row.key)
		}
	}
	// Keep every observed failure in the response even when the group is not
	// complete, but never make an attribution from partial/inconsistent evidence.
	if !complete || claimsMismatch || reportedTests == 0 || len(current) == 0 || (reportedFailures > 0 && len(keys) == 0) {
		reason := "exact_run_incomplete"
		switch {
		case claimsMismatch:
			reason = "run_upload_claims_mismatch"
		case complete && (reportedTests == 0 || len(current) == 0):
			reason = "empty_run_cannot_exclude_infrastructure_failure"
		case complete && reportedFailures > 0 && len(keys) == 0:
			reason = "failure_summary_missing_test_evidence"
		}
		a.Reasons = append(a.Reasons, reason)
		for _, row := range current {
			if row.failed {
				a.Tests = append(a.Tests, Test{StableKey: row.key, File: row.file, FullTitle: row.title,
					Project: row.project, Framework: row.framework, Outcome: Unknown, Reasons: []string{reason}, BaselineGroupIDs: []string{}})
			}
		}
		return a, nil
	}
	if len(keys) == 0 {
		if !anyPassed {
			a.Reasons = append(a.Reasons, "no_test_execution_observed")
			return a, nil
		}
		a.Outcome = NoFailure
		a.Reasons = append(a.Reasons, "no_test_failure_observed_in_complete_report")
		return a, nil
	}
	history, err := observations(ctx, tx, `SELECT `+observationColumns+observationJoins+`
 WHERE tc.stable_key=ANY($1) AND g.repository=$2 AND g.name=$3 AND g.framework=$7
 AND g.branch='master' AND g.gh_pr_number IS NULL AND g.commit_sha<>$4
 AND g.created_at >= $5 AND g.created_at < $6 AND g.updated_at <= $6
 AND `+completeGroupSQL+`
 AND NOT EXISTS (SELECT 1 FROM reports cr JOIN oidc_claims c ON c.report_id=cr.id WHERE cr.report_group_id=g.id AND
 (c.issuer <> 'https://token.actions.githubusercontent.com' OR c.repository IS DISTINCT FROM g.repository
 OR c.ref IS DISTINCT FROM 'refs/heads/master' OR c.raw_claims->>'sha' IS DISTINCT FROM g.commit_sha))`+observationGroup,
		keys, selector.Repository, baselineName(selector.Repository, selector.Name, framework), selector.CommitSHA, created.Add(-baselineWindow), created, framework)
	if err != nil {
		return a, err
	}
	seen := map[string]bool{}
	for _, row := range current {
		if row.failed && !seen[row.key] {
			seen[row.key] = true
			a.Tests = append(a.Tests, assessTest(row, current, history, created, freshness))
		}
	}
	a.Outcome = aggregate(a.Tests)
	a.Reasons = append(a.Reasons, "all_observed_failure_keys_assessed")
	return a, nil
}

// These are the four independently scheduled Mattermost master suites. No
// suffix or edition inference is permitted for other repositories or names.
func baselineName(repository, name, framework string) string {
	if repository == "mattermost/mattermost" {
		switch name {
		case "cypress-full-enterprise", "cypress-full-fips":
			if framework == "cypress" {
				return name + "-master"
			}
		case "playwright-full-enterprise", "playwright-full-fips":
			if framework == "playwright" {
				return name + "-master"
			}
		}
	}
	return name
}

func observations(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]observation, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []observation{}
	for rows.Next() {
		var o observation
		if err := rows.Scan(&o.key, &o.file, &o.title, &o.project, &o.framework, &o.groupID, &o.commit, &o.createdAt, &o.failed, &o.passed); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}
