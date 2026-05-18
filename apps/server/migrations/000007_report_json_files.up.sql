CREATE TABLE report_json_files (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    report_id  uuid        NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    object_key text        NOT NULL UNIQUE,
    size_bytes bigint      NOT NULL,
    sha256     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
