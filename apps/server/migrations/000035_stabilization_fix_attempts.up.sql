-- What happened the last time something tried to fix this test.
--
-- The agent fix loop attempts a repair, and a human reviews before anything
-- merges. The half that was missing is what happens when the attempt does NOT
-- work: without a record, the loop re-attempts the same unfixable test every
-- cycle (burning model calls on a problem it has already failed), and nobody
-- can see which queued tests have already defeated it.
--
-- The existing loop guard counts autofix commits on one PR branch, which stops
-- an AI<->CI ping-pong inside a single PR. It says nothing across cycles, which
-- is the case this table covers.
--
-- One row per attempt, not per test: "tried three times, three different
-- errors" is the evidence a reviewer needs, and collapsing it to a counter
-- throws away the reason each attempt failed.
CREATE TABLE stabilization_fix_attempts (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    repository       text        NOT NULL,
    external_test_id text        NOT NULL,
    -- fixed:      a fix was pushed; the next E2E run is the validation
    -- failed:     the agent tried and could not produce a usable change
    -- blocked:    a guard refused before it tried (branch cap, ban gate)
    -- needs_human: escalated deliberately — attempts exhausted
    outcome          text        NOT NULL CHECK (outcome IN ('fixed','failed','blocked','needs_human')),
    -- Free text from the agent: what it tried and why it stopped. This is what
    -- a human reads first, so it is the reason the table is not just a counter.
    detail           text        NOT NULL DEFAULT '',
    -- The PR the attempt produced, when it produced one.
    pr_url           text,
    -- Cluster signature, so an attempt can be tied back to its ledger verdict.
    cluster_signature text,
    attempted_by     text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- The queue joins this per (repo, test) and the loop reads it before choosing
-- a target, so both lookups are covered by one index.
CREATE INDEX stabilization_fix_attempts_test_idx
    ON stabilization_fix_attempts (repository, external_test_id, created_at DESC);
