-- Reverse of the up (no-op on fresh databases where 000031's down drops the
-- whole table anyway).
ALTER TABLE alert_firings DROP COLUMN IF EXISTS issue_claimed;
