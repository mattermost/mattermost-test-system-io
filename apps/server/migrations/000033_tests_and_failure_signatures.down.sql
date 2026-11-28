ALTER TABLE test_cases
    DROP COLUMN IF EXISTS signature_id,
    DROP COLUMN IF EXISTS test_id;

DROP TABLE IF EXISTS failure_signatures;
DROP TABLE IF EXISTS tests;
