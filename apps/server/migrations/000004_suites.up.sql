CREATE TABLE suites (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    report_id       uuid        NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    parent_suite_id uuid        REFERENCES suites(id) ON DELETE CASCADE,
    title           text        NOT NULL,
    file            text,
    line            integer,
    col             integer,
    duration_ms     bigint,
    -- Per-suite aggregate counts populated at consolidation time. The web's
    -- TestSuite DTO renders these in the detail page's suite header.
    total_count     integer     NOT NULL DEFAULT 0,
    passed_count    integer     NOT NULL DEFAULT 0,
    failed_count    integer     NOT NULL DEFAULT 0,
    skipped_count   integer     NOT NULL DEFAULT 0,
    flaky_count     integer     NOT NULL DEFAULT 0,
    -- Actual earliest test-start time from the framework JSON (not the
    -- consolidation timestamp). Used for chronological ordering in the UI.
    start_time      timestamptz,
    ordinal         integer     NOT NULL
);
CREATE INDEX suites_report_parent_order_idx ON suites (report_id, parent_suite_id, ordinal);
