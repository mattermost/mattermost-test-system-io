-- Initial schema for Rust Report Viewer
-- Migration: 001_initial_schema

-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

-- Reports table: Primary entity for uploaded test reports
CREATE TABLE IF NOT EXISTS reports (
    id TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    deleted_at TEXT,
    extraction_status TEXT NOT NULL DEFAULT 'pending' CHECK (extraction_status IN ('pending', 'completed', 'failed')),
    file_path TEXT NOT NULL,
    error_message TEXT,
    -- File tracking
    has_files INTEGER NOT NULL DEFAULT 1,
    files_deleted_at TEXT,
    -- Framework info
    framework TEXT,
    framework_version TEXT,
    -- Platform for mobile tests (ios, android, etc.)
    platform TEXT,
    -- GitHub context (JSON)
    github_context TEXT
);

CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reports_deleted_at ON reports(deleted_at);

-- Report statistics: 1:1 relationship with reports
CREATE TABLE IF NOT EXISTS report_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    report_id TEXT NOT NULL UNIQUE,
    start_time TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    expected INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    unexpected INTEGER NOT NULL DEFAULT 0,
    flaky INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (report_id) REFERENCES reports(id) ON DELETE CASCADE
);

-- Test suites: Test files extracted from results.json
CREATE TABLE IF NOT EXISTS test_suites (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    report_id TEXT NOT NULL,
    title TEXT NOT NULL,
    file_path TEXT NOT NULL,
    FOREIGN KEY (report_id) REFERENCES reports(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_test_suites_report_id ON test_suites(report_id);

-- Test specs: Individual test specifications
CREATE TABLE IF NOT EXISTS test_specs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    suite_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    ok INTEGER NOT NULL,
    full_title TEXT NOT NULL,
    file_path TEXT NOT NULL,
    line INTEGER NOT NULL,
    col INTEGER NOT NULL,
    FOREIGN KEY (suite_id) REFERENCES test_suites(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_test_specs_suite_id ON test_specs(suite_id);

-- Test results: Individual test execution results (supports retries)
CREATE TABLE IF NOT EXISTS test_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    spec_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('passed', 'failed', 'skipped', 'timedOut')),
    duration_ms INTEGER NOT NULL,
    retry INTEGER NOT NULL DEFAULT 0,
    start_time TEXT NOT NULL,
    project_id TEXT NOT NULL,
    project_name TEXT NOT NULL,
    errors_json TEXT,
    FOREIGN KEY (spec_id) REFERENCES test_specs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_test_results_spec_id ON test_results(spec_id);

-- Detox jobs table: Individual job within a Detox report
CREATE TABLE IF NOT EXISTS detox_jobs (
    id TEXT PRIMARY KEY,
    report_id TEXT NOT NULL,
    job_name TEXT NOT NULL,
    created_at TEXT NOT NULL,
    tests_count INTEGER NOT NULL DEFAULT 0,
    passed_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    skipped_count INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (report_id) REFERENCES reports(id) ON DELETE CASCADE,
    UNIQUE(report_id, job_name)
);

CREATE INDEX IF NOT EXISTS idx_detox_jobs_report_id ON detox_jobs(report_id);

-- Detox screenshots table: Screenshots captured during test execution
CREATE TABLE IF NOT EXISTS detox_screenshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    test_full_name TEXT NOT NULL,
    screenshot_type TEXT NOT NULL,
    file_path TEXT NOT NULL,
    created_at TEXT NOT NULL,
    deleted_at TEXT,
    FOREIGN KEY (job_id) REFERENCES detox_jobs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_detox_screenshots_job_id ON detox_screenshots(job_id);
CREATE INDEX IF NOT EXISTS idx_detox_screenshots_test_name ON detox_screenshots(test_full_name);
CREATE INDEX IF NOT EXISTS idx_detox_screenshots_deleted_at ON detox_screenshots(deleted_at) WHERE deleted_at IS NULL;

-- Server metadata: Tracks server version and settings
CREATE TABLE IF NOT EXISTS server_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Mark this migration as applied
INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (1, datetime('now'));
