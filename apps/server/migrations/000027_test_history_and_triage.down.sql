DROP TABLE IF EXISTS triage_verdicts;

DROP INDEX IF EXISTS report_groups_repo_branch_created_idx;
DROP INDEX IF EXISTS test_cases_external_id_idx;

ALTER TABLE test_cases DROP COLUMN IF EXISTS external_test_id;
