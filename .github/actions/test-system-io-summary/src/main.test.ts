import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  buildCommitStatusMessage,
  buildFailureMessage,
  computeMissedCount,
  deriveCommitState,
} from "./main.ts";
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
    gh_run_attempt: "1",
    name: "detox-android",
    branch: "pr-10032",
  });
  assert.equal(
    url,
    "https://test-io.test.mattermost.com/reports/mattermost-mobile/pr-10032/abcdef0/detox-android?gh_run_id=12345&gh_run_attempt=1",
  );
});

test("computeMissedCount: 0 when every unit reached a terminal outcome", () => {
  assert.equal(computeMissedCount(267, 267), 0);
});

test("computeMissedCount: counts units still pending/leased/abandoned", () => {
  assert.equal(computeMissedCount(267, 266), 1);
});

test("buildCommitStatusMessage: 100% pass, nothing missed, complete", () => {
  const msg = buildCommitStatusMessage({
    incomplete: false,
    rate: 100,
    rateStr: "100%",
    passed: 905,
    rateDenom: 905,
    failed: 0,
    totalSpecs: 266,
    missedCount: 0,
  });
  assert.equal(msg, "100% passed (905), 266 specs");
});

test("buildCommitStatusMessage: 100% pass but a spec never completed", () => {
  const msg = buildCommitStatusMessage({
    incomplete: true,
    rate: 100,
    rateStr: "100%",
    passed: 905,
    rateDenom: 905,
    failed: 0,
    totalSpecs: 266,
    missedCount: 1,
  });
  assert.equal(msg, "⚠ 1 spec(s) missed, the rest 100% passed (905), 266 specs");
});

test("buildCommitStatusMessage: incomplete run with zero missed units still flags it", () => {
  const msg = buildCommitStatusMessage({
    incomplete: true,
    rate: 100,
    rateStr: "100%",
    passed: 266,
    rateDenom: 266,
    failed: 0,
    totalSpecs: 266,
    missedCount: 0,
  });
  assert.equal(msg, "⚠ run incomplete, 100% passed (266), 266 specs");
});

test("deriveCommitState: error when incomplete, regardless of failures", () => {
  assert.equal(deriveCommitState(true, 0), "error");
  assert.equal(deriveCommitState(true, 3), "error");
});

test("deriveCommitState: success when complete with no failed units", () => {
  assert.equal(deriveCommitState(false, 0), "success");
});

test("deriveCommitState: failure when complete but some units failed", () => {
  assert.equal(deriveCommitState(false, 1), "failure");
});

test("buildFailureMessage: fails on incomplete even with zero failed units", () => {
  assert.equal(
    buildFailureMessage(true, "completed", 1, 0),
    "run incomplete: status=completed missed=1",
  );
});

test("buildFailureMessage: fails on failed units when otherwise complete", () => {
  assert.equal(buildFailureMessage(false, "completed", 0, 2), "2 unit(s) failed");
});

test("buildFailureMessage: null when complete, nothing failed, nothing missed", () => {
  assert.equal(buildFailureMessage(false, "completed", 0, 0), null);
});
