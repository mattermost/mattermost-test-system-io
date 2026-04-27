-- Orchestration runs are the top-level entity for the test-shard orchestrator:
-- one row per `begin run` call. Each run owns a queue of dispatch_units and is
-- addressed by API clients via the same composite identity used by report_groups.
CREATE TABLE orchestration_runs (
    id                       uuid        PRIMARY KEY DEFAULT uuidv7(),
    -- Composite-identity tuple (mirrors report_groups). The
    -- orchestration_runs_identity_idx UNIQUE index below drives the
    -- begin-run idempotency lookup.
    repository               text        NOT NULL,
    commit_sha               text        NOT NULL,
    gh_run_id                text        NOT NULL,
    name                     text        NOT NULL,
    gh_run_attempt           text        NOT NULL DEFAULT '1',
    framework                text        NOT NULL CHECK (framework = 'playwright'),
    branch                   text        NOT NULL DEFAULT '',
    gh_pr_number             integer,
    playwright_project       text,
    -- Configuration knobs supplied (or defaulted) by the caller at begin-run.
    lease_timeout_ms         bigint      NOT NULL CHECK (lease_timeout_ms > 0),
    run_timeout_ms           bigint      NOT NULL CHECK (run_timeout_ms > 0),
    retest_on_fail           boolean     NOT NULL DEFAULT FALSE,
    retest_budget            integer     NOT NULL DEFAULT 1 CHECK (retest_budget >= 0),
    -- Materialized counters; updated transactionally with dispatch_units state.
    -- retest_eligible_count is a SUBSET of completed_fail_count and is NOT added
    -- to the sanity sum below — it's a derived counter, not a state.
    retest_eligible_count    integer     NOT NULL DEFAULT 0,
    status                   text        NOT NULL DEFAULT 'in_progress'
                                         CHECK (status IN ('in_progress','completed','timed_out')),
    pending_count            integer     NOT NULL DEFAULT 0,
    leased_count             integer     NOT NULL DEFAULT 0,
    completed_pass_count     integer     NOT NULL DEFAULT 0,
    completed_fail_count     integer     NOT NULL DEFAULT 0,
    completed_skipped_count  integer     NOT NULL DEFAULT 0,
    abandoned_count          integer     NOT NULL DEFAULT 0,
    total_units              integer     NOT NULL,
    -- SHA-256 of the canonical-form dispatch_units list submitted at begin-run.
    -- Used as the idempotency key on retries: a retry with a matching hash
    -- replays the existing snapshot; a retry with a different hash conflicts.
    dispatch_units_hash      bytea       NOT NULL,
    started_at               timestamptz NOT NULL DEFAULT now(),
    deadline                 timestamptz NOT NULL,
    terminal_at              timestamptz,
    -- Owner of the run: exactly one of these is non-null. Enforced by the
    -- orchestration_runs_owner_ck CHECK below.
    owner_oidc_subject       text,
    owner_api_key_id         uuid        REFERENCES api_keys(id) ON DELETE SET NULL,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT orchestration_runs_counts_ck CHECK (
        pending_count + leased_count + completed_pass_count + completed_fail_count
            + completed_skipped_count + abandoned_count = total_units
    ),
    CONSTRAINT orchestration_runs_terminal_ck CHECK (
        (status = 'in_progress') = (terminal_at IS NULL)
    ),
    CONSTRAINT orchestration_runs_owner_ck CHECK (
        owner_oidc_subject IS NOT NULL OR owner_api_key_id IS NOT NULL
    )
);

-- Composite identity: drives the begin-run idempotency lookup. Mirrors
-- report_groups_grouping_key_idx on the report_groups table so the same tuple
-- can join against uploaded reports at UI query time.
CREATE UNIQUE INDEX orchestration_runs_identity_idx
    ON orchestration_runs (repository, commit_sha, gh_run_id, name, gh_run_attempt);

-- Reaper scan: find in-progress runs whose deadline has passed.
CREATE INDEX orchestration_runs_status_deadline_idx
    ON orchestration_runs (status, deadline)
    WHERE status = 'in_progress';

-- UI listing scoped to caller (most-recent-first by creation time).
CREATE INDEX orchestration_runs_owner_idx
    ON orchestration_runs (owner_oidc_subject, created_at DESC);

-- Cross-attempt browsing on the existing /reports/:repo/:branch/:commit family.
CREATE INDEX orchestration_runs_repo_commit_idx
    ON orchestration_runs (repository, commit_sha);
