-- POST /reports/history selects suites by spec file across a time window. The
-- file is the request's primary selector, and suites had no index on it, so
-- every call scanned each suite belonging to a group in the window — tens of
-- thousands of rows on a busy repository.
CREATE INDEX IF NOT EXISTS suites_file_idx ON suites (file);
