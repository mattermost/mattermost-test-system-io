DROP INDEX IF EXISTS triage_verdicts_replay_idx;
ALTER TABLE triage_verdicts DROP COLUMN IF EXISTS replay;
