-- API keys carry a role. Until now every key was an uploader (CI); a viewer key is for protected reads
-- such as the history summary and the MCP server, an editor or admin key for people. Existing keys stay
-- uploader, so CI is unaffected.
ALTER TABLE api_keys
    ADD COLUMN role text NOT NULL DEFAULT 'uploader'
    CHECK (role IN ('uploader', 'viewer', 'editor', 'admin'));
