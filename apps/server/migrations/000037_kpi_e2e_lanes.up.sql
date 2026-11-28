-- Migration 000037: Configured E2E lanes and PR false-red rollup/attribution support (SEC-11509).
-- Zero indexes added to test_cases or suites; all DDL is transaction-safe for golang-migrate.

CREATE TABLE IF NOT EXISTS kpi_e2e_lanes (
    repository text        NOT NULL,
    lane       text        NOT NULL,
    enabled    boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository, lane)
);

CREATE INDEX IF NOT EXISTS kpi_e2e_lanes_repo_lower_idx
    ON kpi_e2e_lanes (lower(repository), lane)
    WHERE enabled;

INSERT INTO kpi_e2e_lanes (repository, lane) VALUES
    ('mattermost/mattermost', 'playwright-full'),
    ('mattermost/mattermost', 'playwright-full-fips'),
    ('mattermost/mattermost', 'playwright-full-enterprise'),
    ('mattermost/mattermost', 'playwright-full-enterprise-subpath'),
    ('mattermost/mattermost', 'cypress-full'),
    ('mattermost/mattermost', 'cypress-full-fips'),
    ('mattermost/mattermost', 'cypress-full-enterprise'),
    ('mattermost/mattermost-mobile', 'mobile-pr'),
    ('mattermost/mattermost-mobile', 'mobile-detox-pr'),
    ('mattermost/mattermost-mobile', 'mobile-maestro-pr'),
    ('mattermost/mattermost-mobile', 'mobile-pr-detox-ios'),
    ('mattermost/mattermost-mobile', 'mobile-pr-detox-android'),
    ('mattermost/mattermost-mobile', 'mobile-pr-detox-ipad'),
    ('mattermost/mattermost-mobile', 'mobile-pr-maestro-ios-e2e'),
    ('mattermost/mattermost-mobile', 'mobile-pr-maestro-android-e2e'),
    ('mattermost/mattermost-mobile', 'detox-ios'),
    ('mattermost/mattermost-mobile', 'detox-android'),
    ('mattermost/mattermost-mobile', 'detox-ipad'),
    ('mattermost/mattermost-mobile', 'maestro-ios'),
    ('mattermost/mattermost-mobile', 'maestro-android'),
    ('mattermost/desktop', 'desktop-pr')
ON CONFLICT (repository, lane) DO NOTHING;

ALTER TABLE kpi_group_rollups
    ADD COLUMN IF NOT EXISTS gh_run_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS gh_run_attempt_num integer GENERATED ALWAYS AS (
        CASE
            WHEN btrim(gh_run_attempt) = '' THEN 1
            WHEN btrim(gh_run_attempt) ~ '^[0-9]{1,9}$' THEN btrim(gh_run_attempt)::integer
            ELSE 0
        END
    ) STORED;

UPDATE kpi_group_rollups gr
   SET gh_run_id = rg.gh_run_id
  FROM report_groups rg
 WHERE rg.id = gr.report_group_id
   AND gr.gh_run_id = ''
   AND rg.gh_run_id <> '';

CREATE INDEX IF NOT EXISTS kpi_group_rollups_rerun_idx
    ON kpi_group_rollups (repository, commit_sha, lane, is_all_platforms, platform, gh_run_attempt_num);

CREATE INDEX IF NOT EXISTS kpi_group_rollups_pr_red_idx
    ON kpi_group_rollups (is_all_platforms, group_created_at DESC, repository, lane)
    WHERE gh_pr_number IS NOT NULL AND gh_pr_number > 0 AND is_red;

ALTER TABLE kpi_group_signature_rollups
    ADD COLUMN IF NOT EXISTS has_failed_test boolean NOT NULL DEFAULT true;

CREATE INDEX IF NOT EXISTS kpi_group_signature_rollups_sig_date_idx
    ON kpi_group_signature_rollups (repository, signature_id, stage, metric_date DESC, gh_pr_number)
    WHERE has_failed_test;

UPDATE report_groups rg
   SET kpi_rollups_refreshed_at = NULL
 WHERE rg.kpi_rollups_refreshed_at IS NOT NULL
   AND EXISTS (
       SELECT 1
         FROM kpi_group_signature_rollups gs
        WHERE gs.report_group_id = rg.id
   );
