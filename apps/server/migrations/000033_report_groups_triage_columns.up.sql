ALTER TABLE report_groups
    ADD COLUMN branch_kind text CHECK (branch_kind IN ('trunk','pr','release','other')),
    ADD COLUMN base_ref text,
    ADD COLUMN base_sha text;
ALTER TABLE test_cases
    ADD COLUMN identity_id uuid REFERENCES test_identities(id) ON DELETE SET NULL,
    ADD COLUMN error_signature bytea,
    ADD COLUMN failure_locus text;
CREATE INDEX test_cases_identity_idx ON test_cases (identity_id) WHERE identity_id IS NOT NULL;
