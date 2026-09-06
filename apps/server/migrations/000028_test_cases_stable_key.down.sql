DROP INDEX IF EXISTS test_cases_stable_key_idx;
ALTER TABLE test_cases DROP COLUMN IF EXISTS stable_key;
ALTER TABLE test_cases DROP COLUMN IF EXISTS file;
