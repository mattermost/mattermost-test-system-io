import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  canWaive,
  decide,
  diffOverlaps,
  isProtectedRun,
  rollup,
  WAIVE_CONFIDENCE,
} from "./policy.ts";
import type { EvidenceFailure } from "./types.ts";

function failure(over: Partial<EvidenceFailure> = {}): EvidenceFailure {
  return {
    full_title: "Login › MM-T1 logs in",
    title: "MM-T1 logs in",
    status: "failed",
    retry_count: 0,
    duration_ms: 1000,
    screenshots: [],
    suggested: {
      verdict: "FLAKY_TEST",
      confidence: 1,
      needs_ai: false,
      reason: "recovered",
      citations: ["this_run_recovered"],
    },
    amnesty: { granted: true, reason: "ok" },
    ...over,
  };
}

test("main/release runs never auto-waive", () => {
  assert.equal(isProtectedRun("MAIN", "feat/x"), true);
  assert.equal(isProtectedRun("PR", "main"), true);
  assert.equal(isProtectedRun("PR", "release-11.4"), true);
  assert.equal(isProtectedRun("PR", "feat/x"), false);
});

test("in-run recovery waives on a PR", () => {
  const w = canWaive({
    runType: "PR",
    branch: "feat/x",
    verdict: "FLAKY_TEST",
    confidence: 1,
    citations: ["this_run_recovered"],
    amnestyGranted: true,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, true);
});

test("waiver requires 0.85 confidence", () => {
  const w = canWaive({
    runType: "PR",
    branch: "feat/x",
    verdict: "FLAKY_TEST",
    confidence: 0.8,
    citations: ["flip_count", "historical_failure_rate"],
    amnestyGranted: true,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, false);
  assert.ok(w.reason.includes(String(WAIVE_CONFIDENCE)));
});

test("pre-existing main failure waives on a PR", () => {
  const w = canWaive({
    runType: "PR",
    branch: "feat/x",
    verdict: "MAIN_REGRESSION",
    confidence: 0.95,
    citations: ["failing_on_baseline", "failing_elsewhere"],
    amnestyGranted: true,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, true);
});

test("PR_REGRESSION never waives", () => {
  const w = canWaive({
    runType: "PR",
    branch: "feat/x",
    verdict: "PR_REGRESSION",
    confidence: 1,
    citations: ["never_failed_on_baseline", "isolated_to_this_pr"],
    amnestyGranted: true,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, false);
});

test("diff overlap blocks an AI flake waiver", () => {
  const d = decide({
    failure: failure({
      file: "detox/e2e/test/login.e2e.ts",
      suggested: {
        verdict: "INCONCLUSIVE",
        confidence: 0,
        needs_ai: true,
        reason: "unknown",
        citations: [],
      },
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: ["detox/e2e/test/login.e2e.ts"],
    ai: {
      verdict: "FLAKY_TEST",
      confidence: 0.95,
      reason: "looks like a spinner flake",
      citations: ["screenshot", "error_message"],
    },
  });
  assert.equal(d.waived, false);
  assert.equal(d.source, "policy");
});

test("diffOverlaps matches spec path and stack frames", () => {
  assert.equal(
    diffOverlaps(["app/login.tsx"], "detox/e2e/login.e2e.ts", "at run (app/login.tsx:10)"),
    true,
  );
  assert.equal(diffOverlaps(["README.md"], "detox/e2e/login.e2e.ts", "boom"), false);
});

test("one unwaived failure keeps the run red", () => {
  const r = rollup([
    {
      verdict: "FLAKY_TEST",
      confidence: 1,
      reason: "a",
      citations: ["this_run_recovered"],
      waived: true,
      source: "history",
      check_state: "success",
      kind: "flaky",
      member_count: 12,
    },
    {
      verdict: "PR_REGRESSION",
      confidence: 0.7,
      reason: "b",
      citations: ["isolated_to_this_pr"],
      waived: false,
      source: "history",
      check_state: "failure",
      kind: "bug",
      member_count: 1,
    },
  ]);
  assert.equal(r.waived, false);
  assert.equal(r.state, "failure");
  assert.equal(r.verdict, "PR_REGRESSION");
});

test("no failures is success", () => {
  const r = rollup([]);
  assert.equal(r.state, "success");
  assert.equal(r.description, "no failures");
});

test("rollup counts clustered members, not just clusters", () => {
  const r = rollup([
    {
      verdict: "FLAKY_TEST",
      confidence: 1,
      reason: "spinner",
      citations: ["this_run_recovered"],
      waived: true,
      source: "history",
      check_state: "success",
      kind: "flaky",
      member_count: 300,
    },
  ]);
  assert.equal(r.waived, true);
  assert.equal(r.state, "success");
  assert.match(r.description, /300 failure\(s\) in 1 cluster/);
});

test("decide uses history suggestion when no model call", () => {
  const d = decide({
    failure: failure({ status: "flaky" }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [],
  });
  assert.equal(d.waived, true);
  assert.equal(d.source, "history");
});
