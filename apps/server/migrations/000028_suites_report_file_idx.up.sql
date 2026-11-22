-- Speed up POST /api/v1/reports/history EXISTS probes per report:
--   JOIN suites s ON s.report_id = r.id WHERE s.file = ANY($5::text[])
-- Without a (report_id, file) index, Postgres scans every suite row of each
-- candidate report, causing 60s ALB 504s for sparse or newly added specs.
CREATE INDEX IF NOT EXISTS suites_report_id_file_idx ON suites (report_id, file);
