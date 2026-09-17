CREATE TABLE test_health (
    identity_id uuid NOT NULL REFERENCES test_identities(id) ON DELETE CASCADE,
    lane text NOT NULL,
    base_ref text NOT NULL,
    window_runs integer NOT NULL,
    pass_count integer NOT NULL,
    fail_count integer NOT NULL,
    flaky_count integer NOT NULL,
    instability_rate double precision NOT NULL,
    consecutive_fails integer NOT NULL,
    last_pass_at timestamptz,
    last_fail_at timestamptz,
    dominant_signature bytea,
    dominant_locus text,
    classification text NOT NULL CHECK (classification IN ('healthy','flaky','broken','unstable','unknown','retired')),
    classification_since timestamptz NOT NULL,
    consecutive_refreshes integer NOT NULL DEFAULT 1,
    computed_at timestamptz NOT NULL DEFAULT now(),
    engine_version text NOT NULL,
    PRIMARY KEY (identity_id, lane, base_ref)
);
CREATE INDEX test_health_class_idx ON test_health (classification, lane, base_ref);
