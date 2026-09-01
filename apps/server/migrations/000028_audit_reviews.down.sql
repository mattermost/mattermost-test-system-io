-- Down: the blind audit surface. Dropping it drops the agreement-rate history
-- (W13's gate), so only drop when retiring the audit program itself.
DROP TABLE IF EXISTS triage_audit_reviews;
