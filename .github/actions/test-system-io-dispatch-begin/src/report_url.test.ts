import { test } from "node:test";
import * as assert from "node:assert/strict";
import { buildReportURL, encodeBranchPathSegment } from "./report_url.ts";

test("encodeBranchPathSegment: slashes become ~", () => {
  assert.equal(encodeBranchPathSegment("feat/tsio"), "feat~tsio");
  assert.equal(encodeBranchPathSegment("refs/heads/main"), "main");
});

test("buildReportURL: deep-links report page with gh_run_id and attempt", () => {
  const url = buildReportURL("https://test-io.test.mattermost.com", {
    repository: "mattermost/mattermost-mobile",
    commit_sha: "abcdef0123456789",
    gh_run_id: "12345",
    gh_run_attempt: "2",
    name: "detox-android",
    branch: "pr-10032",
  });
  assert.equal(
    url,
    "https://test-io.test.mattermost.com/reports/mattermost-mobile/pr-10032/abcdef0/detox-android?gh_run_id=12345&gh_run_attempt=2",
  );
});

test("buildReportURL: defaults gh_run_attempt to 1", () => {
  const url = buildReportURL("https://test-io.test.mattermost.com", {
    repository: "mattermost/mattermost-mobile",
    commit_sha: "abcdef0123456789",
    gh_run_id: "12345",
    name: "detox-android",
    branch: "pr-10032",
  });
  assert.equal(
    url,
    "https://test-io.test.mattermost.com/reports/mattermost-mobile/pr-10032/abcdef0/detox-android?gh_run_id=12345&gh_run_attempt=1",
  );
});
