-- Drop API key roles; every remaining key becomes an uploader again, as before 000027. Revoke viewer,
-- editor and admin keys first if they must not regain upload access.
ALTER TABLE api_keys DROP COLUMN role;
