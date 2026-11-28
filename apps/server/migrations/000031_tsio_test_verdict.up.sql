-- Canonical per-test verdict rollup matching internal/teststatus.TestVerdict.
-- Attempt classification:
--   passed, flaky           -> pass
--   skipped, pending        -> skip
--   anything else (incl '') -> fail (fail-closed)
-- Empty or NULL input returns NULL so missing attempts are not counted.
DROP FUNCTION IF EXISTS tsio_test_verdict(text[]);

CREATE OR REPLACE FUNCTION tsio_test_verdict(statuses text[], framework_flaky boolean DEFAULT false) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN COALESCE(cardinality(statuses), 0) = 0 THEN NULL
        WHEN (
            BOOL_OR(s IN ('passed', 'flaky'))
            AND BOOL_OR(COALESCE(s, '') NOT IN ('passed', 'flaky', 'skipped', 'pending'))
        ) OR BOOL_OR(s = 'flaky') OR COALESCE(framework_flaky, false) THEN 'flaky'
        WHEN BOOL_OR(COALESCE(s, '') NOT IN ('passed', 'flaky', 'skipped', 'pending')) THEN 'failed'
        WHEN BOOL_OR(s IN ('passed', 'flaky')) THEN 'passed'
        ELSE 'skipped'
    END
    FROM unnest(statuses) AS s;
$$;
