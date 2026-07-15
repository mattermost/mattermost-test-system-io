CREATE TABLE github_oidc_policies (
    id                     uuid        PRIMARY KEY DEFAULT uuidv7(),
    name                   text        NOT NULL UNIQUE,
    enabled                boolean     NOT NULL DEFAULT true,
    priority               integer     NOT NULL DEFAULT 100,
    match_repository       text,
    match_repository_owner text,
    match_workflow         text,
    match_ref              text,
    match_environment      text,
    grant_role             text        NOT NULL CHECK (grant_role IN ('uploader','viewer','editor','admin','denied')),
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX github_oidc_policies_enabled_priority_idx ON github_oidc_policies (enabled, priority);
