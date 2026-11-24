DROP TABLE IF EXISTS apps;

ALTER TABLE reports
    DROP COLUMN IF EXISTS platform;

DROP INDEX IF EXISTS report_groups_stage_created_idx;

ALTER TABLE report_groups
    DROP COLUMN IF EXISTS edition,
    DROP COLUMN IF EXISTS app_version,
    DROP COLUMN IF EXISTS server_version,
    DROP COLUMN IF EXISTS platform,
    DROP COLUMN IF EXISTS head_ref,
    DROP COLUMN IF EXISTS base_ref,
    DROP COLUMN IF EXISTS event_name,
    DROP COLUMN IF EXISTS stage;
