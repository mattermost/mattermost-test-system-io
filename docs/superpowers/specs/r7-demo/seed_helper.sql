CREATE OR REPLACE FUNCTION seed_run(
  p_branch text, p_commit text, p_pr int, p_runid text,
  p_testid text, p_title text, p_file text, p_status text,
  p_err text, p_stack text, p_created timestamptz, p_retry int DEFAULT 0
) RETURNS void AS $fn$
DECLARE gid uuid; rid uuid; sid uuid;
BEGIN
  INSERT INTO report_groups(framework, name, status, repository, branch, commit_sha,
                            gh_run_id, gh_run_attempt, gh_pr_number, created_at,
                            updated_at, last_upload_at, reports_count, run_group)
  VALUES ('playwright', 'e2e-tests', 'completed', 'mattermost/mattermost', p_branch, p_commit,
          p_runid, '1', p_pr, p_created, p_created, p_created, 1, 'e2e')
  RETURNING id INTO gid;

  INSERT INTO reports(report_group_id, name, status, created_at, updated_at,
                      total_suites, total_cases, passed_cases, failed_cases)
  VALUES (gid, 'shard-1', 'complete', p_created, p_created, 1, 1,
          CASE WHEN p_status='passed' THEN 1 ELSE 0 END,
          CASE WHEN p_status='passed' THEN 0 ELSE 1 END)
  RETURNING id INTO rid;

  INSERT INTO suites(report_id, title, file, ordinal, total_count)
  VALUES (rid, 'e2e suite', p_file, 0, 1) RETURNING id INTO sid;

  INSERT INTO test_cases(suite_id, title, full_title, status, retry_count, duration_ms,
                         error_message, error_stack, ordinal, external_test_id)
  VALUES (sid, p_title, p_title, p_status, p_retry, 1000, p_err, p_stack, 0, p_testid);
END $fn$ LANGUAGE plpgsql;
