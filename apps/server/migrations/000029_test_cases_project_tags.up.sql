ALTER TABLE test_cases
    ADD COLUMN IF NOT EXISTS project text,
    ADD COLUMN IF NOT EXISTS tags jsonb NOT NULL DEFAULT '[]';
