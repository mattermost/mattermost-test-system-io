-- Upgrade deployments that already applied the original migration 28, whose
-- stable_key was STORED GENERATED and which had no project column or trigger.
-- DROP EXPRESSION preserves the stored values and existing index; it does not
-- rewrite the heap. All historical keys remain unchanged until explicitly
-- backfilled. Fresh installations already have the desired shape.
-- Catalog locks can wait for existing transactions; no data/index scan runs.
ALTER TABLE test_cases ADD COLUMN IF NOT EXISTS project text;
ALTER TABLE test_cases ALTER COLUMN stable_key DROP EXPRESSION IF EXISTS;

CREATE OR REPLACE FUNCTION test_cases_stable_key(
    external_test_id text,
    full_title text,
    title text,
    file text,
    project text
) RETURNS text
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT CASE
        WHEN project IS NOT NULL AND project <> ''
            THEN project || ' :: ' || base.key
        ELSE base.key
    END
    FROM (
        SELECT coalesce(
            nullif(external_test_id, ''),
            CASE
                WHEN full_title IS NULL OR full_title = '' THEN
                    CASE WHEN file IS NOT NULL AND file <> ''
                        THEN file || ' :: ' || title
                        ELSE title
                    END
                WHEN file IS NOT NULL AND file <> ''
                     AND left(full_title, length(file)) <> file THEN
                    file || ' :: ' || full_title
                ELSE full_title
            END
        ) AS key
    ) AS base;
$$;

-- BEFORE INSERT OR UPDATE, not just INSERT: the out-of-band backfill sets
-- file and project on existing rows, and stable_key has to be recomputed from
-- them in the same statement. Without the UPDATE branch the backfill would
-- populate the inputs and leave the key NULL.
CREATE OR REPLACE FUNCTION test_cases_set_stable_key() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.stable_key := test_cases_stable_key(
        NEW.external_test_id, NEW.full_title, NEW.title, NEW.file, NEW.project
    );
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS test_cases_stable_key_trg ON test_cases;
CREATE TRIGGER test_cases_stable_key_trg
    BEFORE INSERT OR UPDATE ON test_cases
    FOR EACH ROW EXECUTE FUNCTION test_cases_set_stable_key();
