CREATE TABLE oidc_claims (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    report_id        uuid        REFERENCES reports(id) ON DELETE CASCADE,
    issuer           text        NOT NULL,
    subject          text        NOT NULL,
    audience         text        NOT NULL,
    repository       text,
    repository_owner text,
    workflow         text,
    ref              text,
    environment      text,
    raw_claims       jsonb       NOT NULL,
    verified_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oidc_claims_subject_verified_idx ON oidc_claims (subject, verified_at DESC);
CREATE INDEX oidc_claims_report_idx ON oidc_claims (report_id);
