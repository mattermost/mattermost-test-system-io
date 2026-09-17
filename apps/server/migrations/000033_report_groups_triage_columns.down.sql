DROP INDEX test_cases_identity_idx;
ALTER TABLE test_cases DROP COLUMN failure_locus, DROP COLUMN error_signature, DROP COLUMN identity_id;
ALTER TABLE report_groups DROP COLUMN base_sha, DROP COLUMN base_ref, DROP COLUMN branch_kind;
