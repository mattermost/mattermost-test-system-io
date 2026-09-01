-- W13 — the rollout phase gate.
--
-- One row, one number, one source of truth. Everything that gates triage
-- authority (MAIN auto-waive, PR check flips, the stabilization loop) reads
-- THIS value — never a second flag, an env var, or a workflow input.
--
--   0 shadow — observe + comment, flip nothing
--   1 PR gate — PR checks may green on waived flakes; master comment-only
--   2 master gate — MAIN auto-waive on (W6 policy applies)
--   3 self-healing — stabilization loop + SLA clocks live
--
-- Demotion is automatic (by exactly one phase, immediately, on a measured
-- trigger); promotion is manual and only offered after two consecutive clean
-- weeks. Humans promote; the metrics demote.

CREATE TABLE triage_phase (
    id         integer     PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    phase      integer     NOT NULL DEFAULT 0 CHECK (phase BETWEEN 0 AND 3),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text        NOT NULL DEFAULT ''
);

INSERT INTO triage_phase (id, phase, updated_by) VALUES (1, 0, 'bootstrap');
