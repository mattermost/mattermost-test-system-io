-- Report groups are the composite-identity aggregation entity: one row per
-- (repository, commit, gh_run_id, name, gh_run_attempt). Every per-job upload
-- (reports row) belongs to exactly one report_group.
CREATE TABLE report_groups (
    id                     uuid        PRIMARY KEY DEFAULT uuidv7(),
    framework              text        NOT NULL
                                       CHECK (framework IN ('playwright','cypress','detox')),
    name                   text        NOT NULL,
    -- Lifecycle. The auto-finalize predicate (count(reports.complete) >=
    -- total_reports_expected) flips in_progress → completed; the staleness
    -- reaper flips in_progress → incomplete after no upload activity for the
    -- configured idle window.
    status                 text        NOT NULL DEFAULT 'in_progress'
                                       CHECK (status IN ('in_progress','completed','incomplete')),
    -- Number of `reports` rows the controller declared at /reports/begin
    -- time (one per shard / worker). Drives the count-based auto-finalize
    -- when set; when NULL (group seeded via /reports/register without a
    -- prior /reports/begin), the group auto-completes on any per-shard
    -- upload finalization.
    total_reports_expected integer     CHECK (total_reports_expected IS NULL OR total_reports_expected > 0),
    repository             text        NOT NULL,
    -- branch is set by /reports/register; /reports/begin only carries the
    -- composite identity (repository, commit, gh_run_id, name, gh_run_attempt)
    -- so branch defaults to '' until the first register call.
    branch                 text        NOT NULL DEFAULT '',
    commit_sha             text        NOT NULL,
    gh_run_id              text        NOT NULL DEFAULT '',
    gh_run_attempt         text        NOT NULL DEFAULT '1',
    gh_pr_number           integer,
    environment_metadata   jsonb,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    -- Bumped on every per-shard upload so the staleness reaper can find
    -- groups that haven't seen activity within the idle window.
    last_upload_at         timestamptz NOT NULL DEFAULT now()
);

-- Composite identity: the (repository, commit, gh_run_id, name, gh_run_attempt)
-- tuple uniquely identifies a report group. All per-job uploads for the same
-- test run share this key.
CREATE UNIQUE INDEX report_groups_grouping_key_idx
    ON report_groups (repository, commit_sha, gh_run_id, name, gh_run_attempt);

CREATE INDEX report_groups_created_idx ON report_groups (created_at DESC);
CREATE INDEX report_groups_repository_idx ON report_groups (repository);
CREATE INDEX report_groups_commit_idx ON report_groups (commit_sha);

-- Reaper sweep: find in_progress groups whose last upload is older than the
-- staleness threshold. Partial index keeps it tiny (only in-flight rows).
CREATE INDEX report_groups_staleness_idx
    ON report_groups (last_upload_at)
    WHERE status = 'in_progress';
