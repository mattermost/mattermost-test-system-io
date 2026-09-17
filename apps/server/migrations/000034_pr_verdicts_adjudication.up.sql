-- Second-judge record: the adjudicator's per-finding answers and the final
-- decision the producer acted on. NULL when no adjudication ran.
ALTER TABLE pr_verdicts ADD COLUMN adjudication jsonb;
