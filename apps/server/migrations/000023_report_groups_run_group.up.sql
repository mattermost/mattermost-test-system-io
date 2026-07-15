-- Producer-declared run family: CIs that belong together (e.g. detox + maestro)
-- send the same run_group at /reports/begin|register. Consolidation groups by
-- run_group instead of a hardcoded name map in the server.

ALTER TABLE report_groups
    ADD COLUMN run_group text;

CREATE INDEX report_groups_run_group_idx
    ON report_groups (run_group)
    WHERE run_group IS NOT NULL;

-- One-time backfill for rows uploaded before producers started sending run_group.
UPDATE report_groups
SET run_group = 'mobile-pr'
WHERE run_group IS NULL
  AND name IN ('mobile-pr', 'mobile-detox-pr', 'mobile-maestro-pr');

UPDATE report_groups
SET run_group = 'mobile-main'
WHERE run_group IS NULL
  AND name IN (
    'mobile-main',
    'mobile-detox-main',
    'mobile-maestro-main',
    'mobile-master',
    'mobile-detox-master',
    'mobile-maestro-master'
  );

UPDATE report_groups
SET run_group = 'cmt-mobile'
WHERE run_group IS NULL
  AND name IN ('cmt-mobile', 'mobile-cmt-detox', 'mobile-cmt-maestro', 'mobile-cmt');
