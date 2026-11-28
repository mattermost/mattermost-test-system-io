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
ALTER TABLE test_cases
    ADD COLUMN test_id      uuid REFERENCES tests(id),
    ADD COLUMN signature_id uuid REFERENCES failure_signatures(id);
