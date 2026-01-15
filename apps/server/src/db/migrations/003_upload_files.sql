-- Track individual file uploads for batched upload support
-- Files are registered during initialization, marked uploaded during transfer

CREATE TABLE IF NOT EXISTS upload_files (
    id TEXT PRIMARY KEY,
    report_id TEXT NOT NULL,
    filename TEXT NOT NULL,
    file_size INTEGER,              -- NULL until uploaded, bytes when complete
    uploaded_at TEXT,               -- NULL until uploaded, ISO 8601 when complete
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (report_id) REFERENCES reports(id) ON DELETE CASCADE,
    UNIQUE(report_id, filename)
);

-- Index for querying files by report
CREATE INDEX IF NOT EXISTS idx_upload_files_report ON upload_files(report_id);

-- Index for finding pending files (uploaded_at IS NULL)
CREATE INDEX IF NOT EXISTS idx_upload_files_pending ON upload_files(report_id)
    WHERE uploaded_at IS NULL;

-- Update schema_migrations
INSERT INTO schema_migrations (version, applied_at)
VALUES (3, datetime('now'));
