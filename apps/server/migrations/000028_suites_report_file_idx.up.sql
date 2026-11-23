-- Speed up POST /api/v1/reports/history EXISTS probes per report:
--   JOIN suites s ON s.report_id = r.id WHERE s.file = ANY($5::text[])
-- Without a (report_id, file) index, Postgres scans every suite row of each
-- candidate report, causing 60s ALB 504s for sparse or newly added specs.
--
-- Production rollout note: golang-migrate's pgx5 driver wraps every migration
-- in a transaction, so CONCURRENTLY cannot live inside this file. On large
-- production databases (~5M+ suites rows), run the concurrent build manually
-- BEFORE deploying this migration so the valid-index check below is a zero-lock no-op:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS suites_report_id_file_idx ON suites (report_id, file);
-- If an earlier concurrent build failed and left an invalid index behind, drop
-- and rebuild it before deploying:
--   DROP INDEX CONCURRENTLY IF EXISTS suites_report_id_file_idx;
--   CREATE INDEX CONCURRENTLY suites_report_id_file_idx ON suites (report_id, file);
DO $$
DECLARE
    suites_schema text;
    idx_valid     boolean;
BEGIN
    SELECT n.nspname
    INTO suites_schema
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.oid = 'suites'::regclass;

    SELECT i.indisvalid
    INTO idx_valid
    FROM pg_class c
    JOIN pg_index i ON i.indexrelid = c.oid
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relname = 'suites_report_id_file_idx'
      AND n.nspname = suites_schema;

    IF idx_valid IS FALSE THEN
        EXECUTE format('DROP INDEX %I.%I', suites_schema, 'suites_report_id_file_idx');
    END IF;

    IF idx_valid IS DISTINCT FROM TRUE THEN
        EXECUTE format('CREATE INDEX %I ON %I.suites (report_id, file)', 'suites_report_id_file_idx', suites_schema);
    END IF;
END $$;
