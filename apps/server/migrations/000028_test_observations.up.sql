CREATE TABLE test_observations (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    identity_id uuid NOT NULL REFERENCES test_identities(id) ON DELETE CASCADE,
    report_group_id uuid NOT NULL REFERENCES report_groups(id) ON DELETE CASCADE,
    report_id uuid REFERENCES reports(id) ON DELETE CASCADE,
    test_case_id uuid REFERENCES test_cases(id) ON DELETE SET NULL,
    attempt_id uuid REFERENCES attempts(id) ON DELETE SET NULL,
    branch_kind text NOT NULL CHECK (branch_kind IN ('trunk','pr','release','other')),
    branch text NOT NULL,
    base_ref text,
    base_sha text,
    commit_sha text NOT NULL,
    gh_pr_number integer,
    gh_run_attempt integer NOT NULL DEFAULT 1,
    attempt_index integer NOT NULL DEFAULT 0,
    lane text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('passed','failed','skipped','flaky','timedOut','interrupted')),
    retry_count integer NOT NULL DEFAULT 0,
    duration_ms bigint,
    error_signature bytea,
    error_excerpt text,
    failure_locus text,
    is_infra_stub boolean NOT NULL DEFAULT false,
    observed_at timestamptz NOT NULL,
    UNIQUE (identity_id, report_group_id, attempt_index, lane)
);
CREATE INDEX test_observations_health_idx ON test_observations (identity_id, lane, branch_kind, observed_at DESC);
CREATE INDEX test_observations_group_idx ON test_observations (report_group_id);
CREATE INDEX test_observations_trunk_recent_idx ON test_observations (branch, lane, observed_at DESC) WHERE branch_kind = 'trunk' AND NOT is_infra_stub;
