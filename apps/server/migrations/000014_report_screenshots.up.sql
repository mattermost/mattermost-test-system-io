-- Screenshots uploaded via /reports/upload/{rid}/{uid}/screenshots land here
-- first, then get linked to their test_cases after JSON extraction runs. The
-- two sides of the upload arrive in arbitrary order (JSON first, screenshots
-- first, or interleaved), so case_id stays nullable during the staging phase.
CREATE TABLE report_screenshots (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    report_id       uuid        NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    filename        text        NOT NULL,
    s3_key          text        NOT NULL UNIQUE,
    size_bytes      bigint      NOT NULL,
    -- test_name is the path-derived identity used to match against
    -- test_cases.full_title (e.g. "Suite > Test"); populated at upload time.
    test_name       text        NOT NULL,
    case_id         uuid        REFERENCES test_cases(id) ON DELETE SET NULL,
    screenshot_type text,
    sequence        integer     NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX report_screenshots_report_idx     ON report_screenshots (report_id);
CREATE INDEX report_screenshots_case_idx       ON report_screenshots (case_id);
CREATE INDEX report_screenshots_unlinked_idx   ON report_screenshots (report_id) WHERE case_id IS NULL;
