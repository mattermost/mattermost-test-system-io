import { test } from "node:test";
import * as assert from "node:assert/strict";
import { buildReportURL, encodeBranchPathSegment } from "./report_url.ts";

test("encodeBranchPathSegment: slashes become ~", () => {
  assert.equal(encodeBranchPathSegment("feat/tsio"), "feat~tsio");
  assert.equal(encodeBranchPathSegment("refs/heads/main"), "main");
});

test("buildReportURL: deep-links report page with gh_run_id", () => {
  const url = buildReportURL("https://test-io.test.mattermost.com", {
    repository: "mattermost/mattermost-mobile",
    commit_sha: "abcdef0123456789",
    gh_run_id: "12345",
    name: "detox-android",
    branch: "cursor/e2e-tsio",
  });
  assert.equal(
    url,
    "https://test-io.test.mattermost.com/reports/mattermost-mobile/cursor~e2e-tsio/abcdef0/detox-android?gh_run_id=12345",
  );
});
