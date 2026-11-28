-- Add CI run context columns to report_groups, per-shard platform to reports,
-- repository->app config table, and backfill historical stage values matching
-- internal/api/reports/branch_filter.go rules.

ALTER TABLE report_groups
    ADD COLUMN stage          text NOT NULL DEFAULT 'other'
                                   CHECK (stage IN ('pr', 'main', 'release', 'nightly', 'manual', 'other')),
    ADD COLUMN event_name     text,
    ADD COLUMN base_ref       text,
    ADD COLUMN head_ref       text,
    ADD COLUMN platform       text,
    ADD COLUMN server_version text,
    ADD COLUMN app_version    text,
    ADD COLUMN edition        text;

ALTER TABLE reports
    ADD COLUMN platform text;

CREATE TABLE IF NOT EXISTS apps (
    repository text PRIMARY KEY,
    app        text NOT NULL
);

INSERT INTO apps (repository, app) VALUES
    ('mattermost/mattermost', 'mattermost'),
    ('mattermost/mattermost-mobile', 'mobile'),
    ('mattermost/desktop', 'desktop')
ON CONFLICT (repository) DO NOTHING;

UPDATE report_groups
   SET stage = CASE
           WHEN lower(btrim(COALESCE(event_name, ''))) = 'schedule' THEN 'nightly'
           WHEN lower(btrim(COALESCE(event_name, ''))) = 'workflow_dispatch' THEN 'manual'
           WHEN lower(btrim(COALESCE(event_name, ''))) IN ('pull_request', 'pull_request_target', 'merge_group') THEN 'pr'
           WHEN (gh_pr_number IS NOT NULL AND gh_pr_number > 0)
             OR regexp_replace(COALESCE(branch, ''), '^refs/(heads|tags)/', '') ~* '^pr-0*[1-9][0-9]*$' THEN 'pr'
           WHEN lower(regexp_replace(COALESCE(branch, ''), '^refs/(heads|tags)/', '')) IN ('main', 'master') THEN 'main'
           WHEN lower(regexp_replace(COALESCE(branch, ''), '^refs/(heads|tags)/', '')) LIKE 'release-%' THEN 'release'
           ELSE 'other'
       END
 WHERE stage = 'other'
   AND stage IS DISTINCT FROM (
           CASE
               WHEN lower(btrim(COALESCE(event_name, ''))) = 'schedule' THEN 'nightly'
               WHEN lower(btrim(COALESCE(event_name, ''))) = 'workflow_dispatch' THEN 'manual'
               WHEN lower(btrim(COALESCE(event_name, ''))) IN ('pull_request', 'pull_request_target', 'merge_group') THEN 'pr'
               WHEN (gh_pr_number IS NOT NULL AND gh_pr_number > 0)
                 OR regexp_replace(COALESCE(branch, ''), '^refs/(heads|tags)/', '') ~* '^pr-0*[1-9][0-9]*$' THEN 'pr'
               WHEN lower(regexp_replace(COALESCE(branch, ''), '^refs/(heads|tags)/', '')) IN ('main', 'master') THEN 'main'
               WHEN lower(regexp_replace(COALESCE(branch, ''), '^refs/(heads|tags)/', '')) LIKE 'release-%' THEN 'release'
               ELSE 'other'
           END
       );

CREATE INDEX IF NOT EXISTS report_groups_stage_created_idx
    ON report_groups (stage, created_at DESC);
