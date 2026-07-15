-- Performance indexes and redundant-index cleanup.
--
-- Index build strategy: plain CREATE INDEX (non-CONCURRENT), matching
-- migration 000021. golang-migrate runs each migration inside a transaction,
-- which forbids CREATE INDEX CONCURRENTLY. For a very large existing table a
-- future deploy may need to build these out-of-band with CONCURRENTLY and mark
-- this migration as applied; the tables are small enough today that the brief
-- lock is acceptable.

-- report_json_files is looked up and ordered by report_id (stateless upload
-- finalize + /reports/{id}/json), but only the object_key UNIQUE index existed;
-- the report_id foreign key was unindexed, forcing a sequential scan per lookup.
CREATE INDEX IF NOT EXISTS report_json_files_report_id_created_idx
    ON report_json_files (report_id, created_at);

-- /reports/individual and /reports paginate over ALL reports ordered by
-- created_at with no group filter, which the (report_group_id, created_at)
-- index cannot serve. id DESC is a stable tie-breaker for keyset-style
-- pagination (uuidv7 ids preserve creation order).
CREATE INDEX IF NOT EXISTS reports_created_at_idx
    ON reports (created_at DESC, id DESC);

-- Lease expiry resets units with WHERE current_lease_id = $1. The column backs
-- a foreign key but was unindexed; the partial index keeps it small (only the
-- currently-leased units carry a non-NULL current_lease_id).
CREATE INDEX IF NOT EXISTS dispatch_units_current_lease_idx
    ON dispatch_units (current_lease_id)
    WHERE current_lease_id IS NOT NULL;

-- Drop indexes made redundant by the composite indexes added in 000021:
--   report_groups_created_at_desc_idx (created_at DESC, id DESC) supersedes
--   report_groups_created_idx (created_at DESC), and
--   report_groups_repository_created_at_idx (repository, created_at DESC)
--   supersedes report_groups_repository_idx (repository) as a leading-column
--   prefix.
DROP INDEX IF EXISTS report_groups_created_idx;
DROP INDEX IF EXISTS report_groups_repository_idx;
