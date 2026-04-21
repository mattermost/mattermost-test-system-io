CREATE TABLE test_cases (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    suite_id      uuid        NOT NULL REFERENCES suites(id) ON DELETE CASCADE,
    title         text        NOT NULL,
    -- full_title is the ancestor-prefixed path (e.g. "Login > Logs in with email").
    -- Populated at consolidation; used by search + consolidated view + screenshot linker.
    full_title    text        NOT NULL,
    status        text        NOT NULL CHECK (status IN ('passed','failed','skipped','flaky','timedOut','interrupted')),
    retry_count   integer     NOT NULL DEFAULT 0,
    duration_ms   bigint,
    error_message text,
    error_stack   text,
    annotations   jsonb       NOT NULL DEFAULT '[]',
    -- Framework-specific attachment metadata (Cypress context paths, Playwright
    -- attachment refs). Screenshots live in report_screenshots with their own FK.
    attachments   jsonb,
    ordinal       integer     NOT NULL
);
CREATE INDEX test_cases_suite_order_idx ON test_cases (suite_id, ordinal);
CREATE INDEX test_cases_failed_idx ON test_cases (suite_id) WHERE status = 'failed';
