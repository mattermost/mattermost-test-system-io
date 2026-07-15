-- A report is a per-job upload within a report_group (one shard in a parallel
-- matrix, one retest, etc.). The composite identity lives on report_groups.
CREATE TABLE reports (
    id                        uuid        PRIMARY KEY DEFAULT uuidv7(),
    report_group_id           uuid        NOT NULL REFERENCES report_groups(id) ON DELETE CASCADE,
    name                      text        NOT NULL, -- denormalized from report_groups
    status                    text        NOT NULL DEFAULT 'pending'
                                          CHECK (status IN ('pending','processing','complete','failed')),
    gh_job_id                 text,
    gh_job_name               text,
    json_upload_status        text        CHECK (json_upload_status IS NULL OR json_upload_status IN ('started','completed','failed','timedout')),
    screenshots_upload_status text        CHECK (screenshots_upload_status IS NULL OR screenshots_upload_status IN ('started','completed','failed','timedout')),
    -- duration_ms is the sum of per-case durations; wall_clock_ms is the
    -- span from earliest test start to latest test end (populated by the
    -- consolidator when the framework provides timestamps). The web renders
    -- them in separate pills so keep them distinct.
    duration_ms               bigint,
    wall_clock_ms             bigint,
    start_time                timestamptz,
    error_message             text,
    total_suites              integer     NOT NULL DEFAULT 0,
    total_cases               integer     NOT NULL DEFAULT 0,
    passed_cases              integer     NOT NULL DEFAULT 0,
    failed_cases              integer     NOT NULL DEFAULT 0,
    skipped_cases             integer     NOT NULL DEFAULT 0,
    flaky_cases               integer     NOT NULL DEFAULT 0,
    uploaded_by_api_key_id    uuid        REFERENCES api_keys(id) ON DELETE SET NULL,
    uploaded_by_oidc_subject  text,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reports_counts_ck CHECK (
        passed_cases + failed_cases + skipped_cases + flaky_cases = total_cases
    )
);
CREATE INDEX reports_group_created_idx ON reports (report_group_id, created_at DESC);
CREATE INDEX reports_status_idx ON reports (status);
-- Idempotency: the same gh_job_id within a group cannot be registered twice.
CREATE UNIQUE INDEX reports_group_gh_job_idx
    ON reports (report_group_id, gh_job_id)
    WHERE gh_job_id IS NOT NULL;
