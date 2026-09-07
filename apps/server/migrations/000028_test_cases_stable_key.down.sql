-- The index is created out-of-band by `tsioctl db backfill-stable-key`, not by
-- the up migration, so drop it here defensively rather than assuming it exists.
DROP INDEX IF EXISTS test_cases_stable_key_idx;
DROP TRIGGER IF EXISTS test_cases_stable_key_trg ON test_cases;
DROP FUNCTION IF EXISTS test_cases_set_stable_key();
DROP FUNCTION IF EXISTS test_cases_stable_key(text, text, text, text, text);
ALTER TABLE test_cases DROP COLUMN IF EXISTS stable_key;
ALTER TABLE test_cases DROP COLUMN IF EXISTS project;
ALTER TABLE test_cases DROP COLUMN IF EXISTS file;
