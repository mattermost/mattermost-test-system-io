-- Migration 000036: KPI base view, pre-aggregated daily/commit rollup tables,
-- and incremental catch-up sweep tracking (SEC-11516).
--
-- Production rollout note: zero indexes are added to test_cases or suites,
-- and all DDL is transaction-safe for golang-migrate.

ALTER TABLE report_groups
    ADD COLUMN IF NOT EXISTS kpi_rollups_refreshed_at timestamptz;

CREATE INDEX IF NOT EXISTS report_groups_kpi_unrefreshed_idx
    ON report_groups (created_at DESC)
    WHERE kpi_rollups_refreshed_at IS NULL;

CREATE INDEX IF NOT EXISTS verdicts_group_idx
    ON verdicts (report_group_id)
    WHERE report_group_id IS NOT NULL;

-- Canonical per-test execution view within each report_group, grouped by
-- (report_group_id, test_id, project, platform) using tsio_test_verdict().
-- Intentionally excludes orchestration_runs/leases/attempts so results remain
-- deterministic and rebuildable after `tsioctl orchestration prune`.
CREATE OR REPLACE VIEW kpi_test_results AS
SELECT
    g.id AS report_group_id,
    (g.created_at AT TIME ZONE 'UTC')::date AS metric_date,
    g.created_at AS group_created_at,
    g.repository,
    COALESCE(a.app, NULLIF(split_part(g.repository, '/', 2), ''), g.repository) AS app,
    g.framework,
    g.stage,
    g.name AS lane,
    g.run_group,
    g.branch,
    g.commit_sha,
    g.gh_run_id,
    g.gh_run_attempt,
    g.gh_pr_number,
    g.status AS group_status,
    tc.test_id,
    t.test_key,
    t.zephyr_key,
    t.file,
    t.full_title,
    COALESCE(tc.project, '') AS project,
    COALESCE(NULLIF(r.platform, ''), NULLIF(g.platform, ''), '') AS platform,
    tsio_test_verdict(
        ARRAY_AGG(tc.status ORDER BY r.created_at, r.id, s.ordinal, s.id, tc.ordinal, tc.retry_count, tc.id)
    ) AS verdict,
    (
        tsio_test_verdict(
            ARRAY_AGG(tc.status ORDER BY r.created_at, r.id, s.ordinal, s.id, tc.ordinal, tc.retry_count, tc.id)
        ) = 'passed'
        AND BOOL_OR(tc.retry_count = 0 AND tc.status = 'passed')
    ) AS first_attempt_passed,
    COUNT(*) FILTER (WHERE tc.status IN ('passed', 'flaky'))::integer AS passed_attempts,
    COUNT(*) FILTER (WHERE COALESCE(tc.status, '') NOT IN ('passed', 'flaky', 'skipped', 'pending'))::integer AS failed_attempts,
    COUNT(*) FILTER (WHERE tc.status IN ('skipped', 'pending'))::integer AS skipped_attempts,
    COUNT(*)::integer AS total_attempts,
    MIN(tc.retry_count)::integer AS min_retry_count,
    MAX(tc.retry_count)::integer AS max_retry_count,
    (ARRAY_AGG(tc.signature_id ORDER BY r.created_at, r.id, s.ordinal, s.id, tc.ordinal, tc.retry_count, tc.id)
        FILTER (WHERE tc.signature_id IS NOT NULL))[1] AS primary_signature_id,
    COALESCE(
        ARRAY_AGG(DISTINCT tc.signature_id) FILTER (WHERE tc.signature_id IS NOT NULL),
        '{}'::uuid[]
    ) AS signature_ids,
    SUM(COALESCE(tc.duration_ms, 0))::bigint AS duration_ms
FROM test_cases tc
JOIN suites s ON s.id = tc.suite_id
JOIN reports r ON r.id = s.report_id
JOIN report_groups g ON g.id = r.report_group_id
JOIN tests t ON t.id = tc.test_id
LEFT JOIN LATERAL (
    SELECT ap.app
    FROM apps ap
    WHERE lower(ap.repository) = lower(g.repository)
    ORDER BY (ap.repository = g.repository) DESC
    LIMIT 1
) a ON true
WHERE tc.test_id IS NOT NULL
GROUP BY
    g.id,
    g.created_at,
    g.repository,
    a.app,
    g.framework,
    g.stage,
    g.name,
    g.run_group,
    g.branch,
    g.commit_sha,
    g.gh_run_id,
    g.gh_run_attempt,
    g.gh_pr_number,
    g.status,
    tc.test_id,
    t.test_key,
    t.zephyr_key,
    t.file,
    t.full_title,
    COALESCE(tc.project, ''),
    COALESCE(NULLIF(r.platform, ''), NULLIF(g.platform, ''), '');

-- Per-group (and per-platform slice) durable snapshot.
-- Preserves orchestration outcomes/retried-unit flags across `orchestration prune`,
-- prevents multi-platform group double-counting, and stores `test_counts` for
-- O(group_tests) delta updates to kpi_daily_test_executions.
CREATE TABLE kpi_group_rollups (
    report_group_id                    uuid        NOT NULL REFERENCES report_groups(id) ON DELETE CASCADE,
    is_all_platforms                   boolean     NOT NULL,
    platform                           text        NOT NULL DEFAULT '',
    metric_date                        date        NOT NULL,
    group_created_at                   timestamptz NOT NULL,
    repository                         text        NOT NULL,
    app                                text        NOT NULL,
    framework                          text        NOT NULL,
    stage                              text        NOT NULL,
    lane                               text        NOT NULL,
    commit_sha                         text        NOT NULL,
    gh_run_attempt                     text        NOT NULL,
    gh_pr_number                       integer,
    group_status                       text        NOT NULL,
    has_orchestration                  boolean     NOT NULL DEFAULT false,
    orch_status                        text,
    is_completed                       boolean     NOT NULL DEFAULT false,
    is_red                             boolean     NOT NULL DEFAULT false,
    is_attempt1                        boolean     NOT NULL DEFAULT false,
    is_attempt1_passed                 boolean     NOT NULL DEFAULT false,
    is_attempt1_strict_passed          boolean     NOT NULL DEFAULT false,
    has_retried_unit                   boolean     NOT NULL DEFAULT false,
    total_duration_ms                  bigint,
    tests_total                        integer     NOT NULL DEFAULT 0,
    tests_passed                       integer     NOT NULL DEFAULT 0,
    tests_failed                       integer     NOT NULL DEFAULT 0,
    tests_flaky                        integer     NOT NULL DEFAULT 0,
    tests_skipped                      integer     NOT NULL DEFAULT 0,
    first_attempt_passed_count         integer     NOT NULL DEFAULT 0,
    attempt1_tests_run                 integer     NOT NULL DEFAULT 0,
    attempt1_first_attempt_passed_count integer    NOT NULL DEFAULT 0,
    failed_test_occurrences            integer     NOT NULL DEFAULT 0,
    triaged_test_occurrences           integer     NOT NULL DEFAULT 0,
    has_active_verdict                 boolean     NOT NULL DEFAULT false,
    verdicts_total                     integer     NOT NULL DEFAULT 0,
    verdicts_reused                    integer     NOT NULL DEFAULT 0,
    verdict_label_counts               jsonb       NOT NULL DEFAULT '{}'::jsonb,
    test_counts                        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_at                         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (report_group_id, is_all_platforms, platform)
);

CREATE INDEX kpi_group_rollups_bucket_idx
    ON kpi_group_rollups (metric_date, repository, stage, lane, is_all_platforms, platform);

CREATE INDEX kpi_group_rollups_stage_created_idx
    ON kpi_group_rollups (stage, is_all_platforms, group_created_at DESC);

CREATE TABLE kpi_group_signature_rollups (
    report_group_id   uuid        NOT NULL REFERENCES report_groups(id) ON DELETE CASCADE,
    platform          text        NOT NULL DEFAULT '',
    signature_id      uuid        NOT NULL REFERENCES failure_signatures(id) ON DELETE CASCADE,
    metric_date       date        NOT NULL,
    repository        text        NOT NULL,
    app               text        NOT NULL,
    stage             text        NOT NULL,
    lane              text        NOT NULL,
    gh_pr_number      integer,
    occurrence_count  integer     NOT NULL DEFAULT 0,
    raw_attempt_count integer     NOT NULL DEFAULT 0,
    affected_test_ids uuid[]      NOT NULL DEFAULT '{}'::uuid[],
    last_seen_at      timestamptz NOT NULL,
    PRIMARY KEY (report_group_id, platform, signature_id)
);

CREATE INDEX kpi_group_signature_rollups_bucket_idx
    ON kpi_group_signature_rollups (metric_date, repository, stage, lane, platform, signature_id);

-- Daily rollup per (date, repository, stage, lane, platform-scope).
-- Query with `is_all_platforms = true` when unfiltered, or `is_all_platforms = false AND platform = $p`.
CREATE TABLE kpi_daily_lane_metrics (
    metric_date                         date        NOT NULL,
    repository                          text        NOT NULL,
    app                                 text        NOT NULL,
    framework                           text        NOT NULL,
    stage                               text        NOT NULL,
    lane                                text        NOT NULL,
    is_all_platforms                    boolean     NOT NULL,
    platform                            text        NOT NULL DEFAULT '',
    tests_total                         integer     NOT NULL DEFAULT 0,
    tests_passed                        integer     NOT NULL DEFAULT 0,
    tests_failed                        integer     NOT NULL DEFAULT 0,
    tests_flaky                         integer     NOT NULL DEFAULT 0,
    tests_skipped                       integer     NOT NULL DEFAULT 0,
    first_attempt_passed_count          integer     NOT NULL DEFAULT 0,
    attempt1_tests_run                  integer     NOT NULL DEFAULT 0,
    attempt1_first_attempt_passed_count integer     NOT NULL DEFAULT 0,
    total_runs                          integer     NOT NULL DEFAULT 0,
    completed_runs                      integer     NOT NULL DEFAULT 0,
    incomplete_runs                     integer     NOT NULL DEFAULT 0,
    in_progress_runs                    integer     NOT NULL DEFAULT 0,
    orchestrated_runs                   integer     NOT NULL DEFAULT 0,
    upload_only_runs                    integer     NOT NULL DEFAULT 0,
    attempt1_lanes_total                integer     NOT NULL DEFAULT 0,
    attempt1_lanes_passed               integer     NOT NULL DEFAULT 0,
    attempt1_lanes_strict_elig          integer     NOT NULL DEFAULT 0,
    attempt1_lanes_strict_pass          integer     NOT NULL DEFAULT 0,
    duration_samples_ms                 bigint[]    NOT NULL DEFAULT '{}'::bigint[],
    duration_sum_ms                     bigint      NOT NULL DEFAULT 0,
    duration_count                      integer     NOT NULL DEFAULT 0,
    failed_runs_count                   integer     NOT NULL DEFAULT 0,
    triaged_runs_count                  integer     NOT NULL DEFAULT 0,
    failed_test_occurrences             integer     NOT NULL DEFAULT 0,
    triaged_test_occurrences            integer     NOT NULL DEFAULT 0,
    verdicts_total                      integer     NOT NULL DEFAULT 0,
    verdicts_reused                     integer     NOT NULL DEFAULT 0,
    verdict_label_counts                jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_at                          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (metric_date, repository, stage, lane, is_all_platforms, platform)
);

CREATE INDEX kpi_daily_lane_metrics_app_date_idx
    ON kpi_daily_lane_metrics (app, is_all_platforms, metric_date DESC);

CREATE INDEX kpi_daily_lane_metrics_date_idx
    ON kpi_daily_lane_metrics (metric_date DESC, is_all_platforms);

-- Daily rollup per failure signature.
CREATE TABLE kpi_daily_signature_metrics (
    metric_date       date        NOT NULL,
    repository        text        NOT NULL,
    app               text        NOT NULL,
    stage             text        NOT NULL,
    lane              text        NOT NULL,
    platform          text        NOT NULL DEFAULT '',
    signature_id      uuid        NOT NULL REFERENCES failure_signatures(id) ON DELETE CASCADE,
    occurrence_count  integer     NOT NULL DEFAULT 0,
    raw_attempt_count integer     NOT NULL DEFAULT 0,
    group_count       integer     NOT NULL DEFAULT 0,
    affected_test_ids uuid[]      NOT NULL DEFAULT '{}'::uuid[],
    pr_numbers        integer[]   NOT NULL DEFAULT '{}'::integer[],
    last_seen_at      timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (metric_date, repository, stage, lane, platform, signature_id)
);

CREATE INDEX kpi_daily_signature_metrics_sig_date_idx
    ON kpi_daily_signature_metrics (signature_id, metric_date DESC);

CREATE INDEX kpi_daily_signature_metrics_app_date_idx
    ON kpi_daily_signature_metrics (app, metric_date DESC);

-- Per-(repository, commit_sha, stage, lane, test_id, project, platform) outcomes
-- for KPI 3 (flaky) and SEC-11520 GET /api/v1/tests/{id}/state.
-- Note: a kpi_test_results row with verdict='flaky' (including Cypress single-row
-- flaky) increments BOTH pass_count and fail_count so is_mixed is true.
CREATE TABLE kpi_test_commit_outcomes (
    repository    text        NOT NULL,
    commit_sha    text        NOT NULL,
    stage         text        NOT NULL,
    lane          text        NOT NULL,
    test_id       uuid        NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
    project       text        NOT NULL DEFAULT '',
    platform      text        NOT NULL DEFAULT '',
    app           text        NOT NULL,
    metric_date   date        NOT NULL,
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    pass_count    integer     NOT NULL DEFAULT 0,
    fail_count    integer     NOT NULL DEFAULT 0,
    flaky_count   integer     NOT NULL DEFAULT 0,
    skip_count    integer     NOT NULL DEFAULT 0,
    run_count     integer     NOT NULL DEFAULT 0,
    is_mixed      boolean     GENERATED ALWAYS AS (pass_count > 0 AND fail_count > 0) STORED,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository, commit_sha, stage, lane, test_id, project, platform)
);

CREATE INDEX kpi_test_commit_outcomes_app_date_idx
    ON kpi_test_commit_outcomes (app, metric_date DESC);

CREATE INDEX kpi_test_commit_outcomes_flaky_idx
    ON kpi_test_commit_outcomes (app, metric_date DESC, test_id, project, platform)
    WHERE is_mixed;

CREATE INDEX kpi_test_commit_outcomes_test_idx
    ON kpi_test_commit_outcomes (test_id, last_seen_at DESC);

-- Daily per-test execution counts for KPI 4 denominator ("rate per 1,000 affected-test runs"
-- and ">= 500 runs in 7d" Quiet/Not-enough-runs/Resurfaced state transitions).
CREATE TABLE kpi_daily_test_executions (
    metric_date date        NOT NULL,
    repository  text        NOT NULL,
    app         text        NOT NULL,
    stage       text        NOT NULL,
    lane        text        NOT NULL,
    platform    text        NOT NULL DEFAULT '',
    test_id     uuid        NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
    run_count   integer     NOT NULL DEFAULT 0,
    pass_count  integer     NOT NULL DEFAULT 0,
    fail_count  integer     NOT NULL DEFAULT 0,
    flaky_count integer     NOT NULL DEFAULT 0,
    skip_count  integer     NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (metric_date, repository, stage, lane, platform, test_id)
);

CREATE INDEX kpi_daily_test_executions_test_date_idx
    ON kpi_daily_test_executions (test_id, metric_date DESC);
