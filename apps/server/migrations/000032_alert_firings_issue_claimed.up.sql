-- R2-1/major 7: heal environments that migrated 000031 BEFORE issue_claimed
-- was added to it in-place on this branch. Fresh databases already have the
-- column from 000031; IF NOT EXISTS makes this a no-op there.
ALTER TABLE alert_firings ADD COLUMN IF NOT EXISTS issue_claimed timestamptz;
