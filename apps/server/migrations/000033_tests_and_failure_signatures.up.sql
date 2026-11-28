CREATE TABLE tests (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    repository    text        NOT NULL,
    framework     text        NOT NULL,
    test_key      text        NOT NULL,
    zephyr_key    text,
    file          text        NOT NULL,
    full_title    text        NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repository, test_key)
);

CREATE TABLE failure_signatures (
    id                         uuid        PRIMARY KEY DEFAULT uuidv7(),
    fingerprint                text        NOT NULL UNIQUE,
    algo_version               smallint    NOT NULL,
    error_class                text,
    normalized_message         text        NOT NULL,
    top_frame                  text,
    sample_message             text,
    first_seen_at              timestamptz NOT NULL,
    first_seen_report_group_id uuid,
    merged_into_id             uuid        REFERENCES failure_signatures(id)
);

-- Indexes on test_cases(test_id) and test_cases(signature_id) are deferred to SEC-11506.
-- Add columns first, then attach NOT VALID foreign keys and validate separately
-- so PostgreSQL avoids an ACCESS EXCLUSIVE table scan on test_cases.
ALTER TABLE test_cases
    ADD COLUMN IF NOT EXISTS test_id      uuid,
    ADD COLUMN IF NOT EXISTS signature_id uuid;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'test_cases_test_id_fkey'
          AND conrelid = 'test_cases'::regclass
    ) THEN
        ALTER TABLE test_cases
            ADD CONSTRAINT test_cases_test_id_fkey
            FOREIGN KEY (test_id) REFERENCES tests(id) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'test_cases_signature_id_fkey'
          AND conrelid = 'test_cases'::regclass
    ) THEN
        ALTER TABLE test_cases
            ADD CONSTRAINT test_cases_signature_id_fkey
            FOREIGN KEY (signature_id) REFERENCES failure_signatures(id) NOT VALID;
    END IF;
END $$;
