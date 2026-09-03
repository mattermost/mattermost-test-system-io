-- Distinguish a verdict measured offline from one produced live in CI.
--
-- The replay job re-adjudicates runs TSIO has already ingested: the classifier
-- and the model run over the same evidence a live triage job would have seen,
-- and the result is written here as a real ledger row. Nothing reads those
-- rows to flip a check — no CI job is listening — but they are the only way to
-- obtain an accuracy number before the calling repository's workflows are
-- merged.
--
-- They must not be mixed into the live accuracy figure. A replay verdict was
-- produced with hindsight available in the database (later runs of the same
-- test are already ingested) and without the timing pressure of a real job, so
-- averaging the two would quietly overstate what CI actually does.
ALTER TABLE triage_verdicts
    ADD COLUMN replay boolean NOT NULL DEFAULT false;

-- Accuracy and audit queries always filter on this, and replay rows are
-- expected to outnumber live ones during the collection window.
CREATE INDEX triage_verdicts_replay_idx ON triage_verdicts (repository, replay);
