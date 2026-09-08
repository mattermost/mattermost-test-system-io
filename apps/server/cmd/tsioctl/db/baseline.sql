-- Shared by tsioctl db baseline and scripts/baseline/baseline.sql.
-- One read-only statement, one snapshot. These are stored-test observations,
-- never actual GitHub blocks, causal attribution, or measured waiver impact.
WITH params AS (
    SELECT current_setting('tsio.baseline_repo') AS repo,
           current_setting('tsio.baseline_branch') AS branch,
           current_setting('tsio.baseline_days')::int AS days,
           current_setting('tsio.baseline_min_runs')::int AS min_runs,
           now() - make_interval(days => current_setting('tsio.baseline_days')::int) AS since
),
groups AS MATERIALIZED (
    SELECT g.*, count(r.id) AS reports_seen,
           count(r.id) FILTER (WHERE r.status = 'complete') AS reports_complete,
           g.status = 'completed' AND g.total_reports_expected IS NOT NULL
             AND count(r.id) = g.total_reports_expected
             AND count(r.id) FILTER (WHERE r.status = 'complete') = g.total_reports_expected
             AS ingestion_complete
    FROM report_groups g CROSS JOIN params p
    LEFT JOIN reports r ON r.report_group_id = g.id
    WHERE g.repository = p.repo
    GROUP BY g.id
),
-- Preserve unknown keys in coverage. Exclude them only from identity-based
-- history/replay; a run with an unkeyed row cannot qualify in that replay.
observations AS MATERIALIZED (
    SELECT g.id AS group_id, g.gh_run_id, g.gh_run_attempt, g.commit_sha,
           g.gh_pr_number, g.branch, g.framework, g.name, g.created_at,
           g.ingestion_complete, tc.stable_key,
           count(*) AS attempt_rows,
           count(*) FILTER (WHERE g.framework = 'playwright' AND nullif(tc.project, '') IS NULL)
             AS project_unknown_rows,
           count(*) FILTER (WHERE g.framework = 'cypress' AND tc.attempts IS NULL)
             AS cypress_legacy_rows,
           -- run_failed describes one report's execution. A different shard
           -- may have rerun the spec successfully; preserve the same any-pass
           -- plus any-failure => flaky group outcome as /tests/history.
           CASE
             WHEN bool_or(tc.status = 'flaky') THEN 'flaky'
             WHEN bool_or(tc.status IN ('passed', 'flaky'))
                  AND bool_or(tc.status IN ('failed', 'timedOut', 'interrupted')) THEN 'flaky'
             WHEN bool_or(tc.status IN ('failed', 'timedOut', 'interrupted')) THEN 'failed'
             WHEN bool_or(tc.status IN ('passed', 'flaky')) THEN 'passed'
             ELSE 'skipped'
           END AS outcome
    FROM groups g JOIN reports r ON r.report_group_id = g.id
    JOIN suites s ON s.report_id = r.id JOIN test_cases tc ON tc.suite_id = s.id
    GROUP BY g.id, g.gh_run_id, g.gh_run_attempt, g.commit_sha, g.gh_pr_number,
             g.branch, g.framework, g.name, g.created_at, g.ingestion_complete, tc.stable_key
),
window_groups AS MATERIALIZED (
    SELECT g.* FROM groups g CROSS JOIN params p WHERE g.created_at >= p.since
),
window_observations AS MATERIALIZED (
    SELECT o.* FROM observations o CROSS JOIN params p WHERE o.created_at >= p.since
),
master_retained AS MATERIALIZED (
    SELECT o.* FROM observations o CROSS JOIN params p
    WHERE o.branch = p.branch AND o.gh_pr_number IS NULL
      AND o.ingestion_complete AND o.stable_key IS NOT NULL AND o.outcome <> 'skipped'
),
master_window AS MATERIALIZED (
    SELECT m.* FROM master_retained m CROSS JOIN params p WHERE m.created_at >= p.since
),
group_observation_coverage AS MATERIALIZED (
    SELECT group_id, count(*) AS test_observations, bool_and(stable_key IS NOT NULL) AS all_keyed
    FROM window_observations GROUP BY group_id
),
run_coverage AS MATERIALIZED (
    SELECT g.gh_run_id, g.gh_run_attempt, g.commit_sha,
           count(DISTINCT g.id) AS report_groups,
           bool_and(g.ingestion_complete AND g.gh_run_id <> ''
             AND coalesce(o.test_observations > 0 AND o.all_keyed, false)) AS observed_groups_complete
    FROM window_groups g LEFT JOIN group_observation_coverage o ON o.group_id = g.id
    WHERE g.gh_pr_number IS NOT NULL
    GROUP BY g.gh_run_id, g.gh_run_attempt, g.commit_sha
),
pr_failures AS MATERIALIZED (
    SELECT * FROM window_observations WHERE gh_pr_number IS NOT NULL AND outcome = 'failed'
),
classified AS (
    SELECT f.gh_run_id, f.gh_run_attempt, f.commit_sha, k.k,
           f.stable_key IS NOT NULL AND h.runs_seen >= p.min_runs AND h.nonclean > 0 AS qualifies
    FROM pr_failures f CROSS JOIN params p CROSS JOIN (VALUES (10), (20), (50)) k(k)
    CROSS JOIN LATERAL (
        SELECT count(*) AS runs_seen, count(*) FILTER (WHERE m.outcome IN ('failed', 'flaky')) AS nonclean
        FROM (SELECT m.outcome FROM master_retained m
              WHERE m.stable_key = f.stable_key AND m.framework = f.framework
                -- Mattermost names its full PR and master suites differently.
                -- Keep the mapping explicit so enterprise/FIPS and frameworks
                -- cannot borrow one another's baseline. Other suites retain
                -- exact-name matching.
                AND m.name = CASE WHEN p.repo = 'mattermost/mattermost' AND p.branch = 'master' THEN
                    CASE f.name
                      WHEN 'cypress-full-enterprise' THEN 'cypress-full-enterprise-master'
                      WHEN 'cypress-full-fips' THEN 'cypress-full-fips-master'
                      WHEN 'playwright-full-enterprise' THEN 'playwright-full-enterprise-master'
                      WHEN 'playwright-full-fips' THEN 'playwright-full-fips-master'
                      ELSE f.name
                    END ELSE f.name END
                AND m.created_at < f.created_at AND m.created_at >= f.created_at - make_interval(days => p.days)
              ORDER BY m.created_at DESC, m.group_id DESC LIMIT k.k) m
    ) h
),
replay_runs AS (
    SELECT c.gh_run_id, c.gh_run_attempt, c.commit_sha, c.k,
           bool_and(c.qualifies) AND rc.observed_groups_complete AS qualifies
    FROM classified c JOIN run_coverage rc USING (gh_run_id, gh_run_attempt, commit_sha)
    GROUP BY c.gh_run_id, c.gh_run_attempt, c.commit_sha, c.k, rc.observed_groups_complete
),
replay AS (
    SELECT k.k AS prior_master_limit, count(r.gh_run_id) AS observed_failed_pr_run_attempts,
           count(r.gh_run_id) FILTER (WHERE r.qualifies) AS policy_candidates,
           round(100.0 * count(r.gh_run_id) FILTER (WHERE r.qualifies) / nullif(count(r.gh_run_id), 0), 2)
             AS candidate_pct
    FROM (VALUES (10), (20), (50)) k(k) LEFT JOIN replay_runs r ON r.k = k.k GROUP BY k.k
),
suite_rates AS (
    SELECT framework, name, count(DISTINCT group_id) AS complete_master_groups,
           count(*) AS executed_test_observations,
           count(*) FILTER (WHERE outcome = 'failed') AS failed,
           count(*) FILTER (WHERE outcome = 'flaky') AS retry_survivors,
           round(100.0 * count(*) FILTER (WHERE outcome = 'failed') / nullif(count(*), 0), 3) AS test_failure_pct,
           round(100.0 * count(*) FILTER (WHERE outcome = 'flaky') / nullif(count(*), 0), 3) AS observed_flake_pct
    FROM master_window GROUP BY framework, name
),
-- Each test/suite has its own latest observation. A preceding clean run does
-- not make a later uninterrupted break intermittent; only a recovery after the
-- first non-clean observation does. A one-observation failure stays unknown.
latest AS (
    SELECT DISTINCT ON (framework, name, stable_key) * FROM master_window
    ORDER BY framework, name, stable_key, created_at DESC, group_id DESC
),
streaks AS (
    SELECT l.framework, l.name, l.stable_key, count(*) AS observations,
           min(m.created_at) FILTER (WHERE m.outcome IN ('failed', 'flaky')) AS first_bad,
           max(m.created_at) FILTER (WHERE m.outcome IN ('passed', 'flaky')) AS last_success,
           count(*) FILTER (WHERE m.outcome = 'failed') AS outright_failures
    FROM latest l JOIN master_window m USING (framework, name, stable_key)
    WHERE l.outcome = 'failed'
    GROUP BY l.framework, l.name, l.stable_key
),
hard_breaks AS (
    SELECT framework, name, count(*) AS currently_failing_tests,
           count(*) FILTER (WHERE last_success > first_bad) AS recovered_then_failed,
           count(*) FILTER (WHERE (last_success IS NULL OR last_success < first_bad) AND outright_failures >= 2)
             AS persistent_failure_candidates,
           count(*) FILTER (WHERE (last_success IS NULL OR last_success < first_bad) AND outright_failures < 2)
             AS insufficient_failure_observations
    FROM streaks GROUP BY framework, name
),
first_bad AS (
    SELECT framework, name, stable_key, min(created_at) FILTER (WHERE outcome IN ('failed', 'flaky')) AS first_bad_at,
           min(created_at) FILTER (WHERE outcome = 'passed') AS first_clean_at
    FROM master_retained GROUP BY framework, name, stable_key
),
arrivals AS (
    SELECT framework, name, count(*) FILTER (WHERE first_clean_at < first_bad_at) AS first_observed_nonclean_after_clean,
           count(*) FILTER (WHERE first_clean_at IS NULL OR first_clean_at >= first_bad_at) AS no_prior_clean_observation,
           round(count(*) FILTER (WHERE first_clean_at < first_bad_at)::numeric / p.days, 3) AS observed_arrivals_per_day
    FROM first_bad CROSS JOIN params p WHERE first_bad_at >= p.since GROUP BY framework, name, p.days
),
cadence AS (
    SELECT framework, name, count(*) AS complete_master_groups,
           round(count(*)::numeric / p.days, 2) AS groups_per_day
    FROM window_groups g CROSS JOIN params p
    WHERE g.branch = p.branch AND g.gh_pr_number IS NULL AND g.ingestion_complete
    GROUP BY framework, name, p.days
)
SELECT jsonb_build_object(
    'measurement', 'stored_test_observations_and_policy_replay',
    'repository', p.repo, 'baseline_branch', p.branch, 'window_days', p.days,
    'min_prior_master_observations', p.min_runs, 'generated_at', now(),
    'limitations', jsonb_build_array(
       'No GitHub check outcomes or waiver decisions are stored here: actual PR blocks, causation and waiver impact are not measured.',
       'Coverage includes registered report groups only; entirely missing workflows/groups are unknown.',
       'Suite identity is framework + report group name; producers must use distinct names for distinct CI configurations.',
       'Historical Cypress retry-survivors are absent until producers emit attempt data; attempts columns alone do not prove producer coverage.',
       'Historical Playwright rows with unknown project remain separate from project-prefixed keys.',
       'Arrivals are first observed in retained data, not proven new failures; retention and identity changes censor earlier history.'),
    'coverage', (SELECT jsonb_build_object(
        'registered_groups', count(*), 'complete_ingestion_groups', count(*) FILTER (WHERE ingestion_complete),
        'incomplete_or_unknown_ingestion_groups', count(*) FILTER (WHERE NOT ingestion_complete),
        'groups_without_test_rows', count(*) FILTER (WHERE o.group_id IS NULL),
        'ci_run_attempts', count(DISTINCT (gh_run_id, gh_run_attempt, commit_sha)),
        'earliest_group', min(created_at), 'latest_group', max(created_at)) FROM window_groups g
        LEFT JOIN group_observation_coverage o ON o.group_id = g.id),
    'identity_coverage', (SELECT jsonb_build_object(
        'attempt_rows', coalesce(sum(attempt_rows),0),
        'unkeyed_attempt_rows', coalesce(sum(attempt_rows) FILTER (WHERE stable_key IS NULL),0),
        'playwright_project_unknown_rows', coalesce(sum(project_unknown_rows),0),
        'cypress_legacy_rows', coalesce(sum(cypress_legacy_rows),0)) FROM window_observations),
    'policy_replay', (SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY prior_master_limit),'[]'::jsonb) FROM replay r),
    'master_test_rates_by_suite', (SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY framework,name),'[]'::jsonb) FROM suite_rates r),
    'pr_group_fanout', (SELECT jsonb_build_object(
        'registered_pr_run_attempts', count(*), 'with_incomplete_observed_groups', count(*) FILTER (WHERE NOT observed_groups_complete),
        'min', min(report_groups), 'median', percentile_disc(0.5) WITHIN GROUP (ORDER BY report_groups),
        'p90', percentile_disc(0.9) WITHIN GROUP (ORDER BY report_groups), 'max', max(report_groups)) FROM run_coverage),
    'current_failure_patterns_by_suite', (SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY framework,name),'[]'::jsonb) FROM hard_breaks r),
    'observed_arrivals_by_suite', (SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY framework,name),'[]'::jsonb) FROM arrivals r),
    'master_cadence_by_suite', (SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY framework,name),'[]'::jsonb) FROM cadence r)
) FROM params p;
