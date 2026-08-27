import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  canWaive,
  decide,
  diffOverlaps,
  isProtectedRun,
  neverAutoWaive,
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

test("RELEASE runs never auto-waive; MAIN and PR may", () => {
  assert.equal(neverAutoWaive("RELEASE", "feat/x"), true);
  assert.equal(neverAutoWaive("PR", "release-11.4"), true);
  assert.equal(neverAutoWaive("PR", "release/2.44"), true);
  assert.equal(neverAutoWaive("MAIN", "main"), false);
  assert.equal(neverAutoWaive("MASTER", "main"), false);
  assert.equal(neverAutoWaive("PR", "main"), false);
  assert.equal(neverAutoWaive("PR", "feat/x"), false);
  // alias kept for callers
  assert.equal(isProtectedRun("RELEASE", "main"), true);
  assert.equal(isProtectedRun("MAIN", "main"), false);
});

test("MAIN run waives a confirmed flake so required checks can go green", () => {
  const w = canWaive({
    runType: "MAIN",
    branch: "main",
    verdict: "FLAKY_TEST",
    confidence: 0.92,
    citations: ["screenshot", "history"],
    amnestyGranted: true,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, true);
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

test("AI bug verdict on a recovered test is never re-flipped to flaky", () => {
  const d = decide({
    failure: failure(),
    runType: "PR",
    branch: "feat/x",
    changedFiles: ["e2e-tests/playwright/specs/login.spec.ts"],
    ai: {
      verdict: "PR_REGRESSION",
      confidence: 0.9,
      reason: "recovery hides a real bug — screenshot shows corrupted post content",
      citations: ["screenshot", "history"],
    },
  });
  assert.equal(d.verdict, "PR_REGRESSION");
  assert.equal(d.waived, false);
  assert.equal(d.check_state, "failure");
  assert.equal(d.kind, "bug");
});

test("chronic flag from the model survives decide()", () => {
  const d = decide({
    failure: failure(),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [],
    ai: {
      verdict: "FLAKY_TEST",
      confidence: 0.95,
      reason: "chronic flake (5/20) — same modal timeout on other PRs",
      citations: ["history", "failing_elsewhere", "this_run_recovered"],
      chronic: true,
    },
  });
  assert.equal(d.chronic, true);
  assert.equal(d.waived, true);
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
  assert.equal(
    diffOverlaps(["detox/e2e/test/login.e2e.ts"], "detox/e2e/test/login.e2e.ts", "Error: boom"),
    true,
  );
});

test("diffOverlaps ignores shared harness and unit-test edits", () => {
  const harness = [
    "detox/e2e/support/quarantine.ts",
    "detox/e2e/support/test_config.ts",
    "detox/utils/tsio-report-status.js",
    "app/utils/keyboard.test.ts",
  ];
  const stack =
    "Error: scroll failed\n    at load (detox/e2e/support/test_config.ts:12)\n    at detox/e2e/test/channels/list.e2e.ts:40";
  assert.equal(diffOverlaps(harness, "detox/e2e/test/channels/list.e2e.ts", stack), false);
});

test("diffOverlaps does not treat short path fragments as matches", () => {
  assert.equal(diffOverlaps(["app"], "detox/e2e/test/app_login.e2e.ts", "boom"), false);
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

test("AI INCONCLUSIVE with error text is overridden to a waived flake", () => {
  const d = decide({
    failure: failure({
      error_message: "Wait for LoginAvailable timed out",
      screenshots: [],
      suggested: {
        verdict: "INCONCLUSIVE",
        confidence: 0,
        needs_ai: true,
        reason: "no history",
        citations: [],
      },
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [".github/workflows/e2e.yml"],
    ai: {
      verdict: "INCONCLUSIVE",
      confidence: 0.3,
      reason: "no screenshots",
      citations: [],
    },
  });
  assert.equal(d.waived, true, d.reason);
  assert.equal(d.verdict, "FLAKY_INFRA");
  assert.equal(d.source, "policy");
});

test("PR that edits the failing spec cannot have its bug waived as CI-only", () => {
  const d = decide({
    failure: failure({
      file: "e2e-tests/playwright/specs/functional/login.spec.ts",
      error_message: "expect(locator).toBeVisible() failed",
      suggested: {
        verdict: "FLAKY_TEST",
        confidence: 1,
        needs_ai: false,
        reason: "recovered",
        citations: ["this_run_recovered"],
      },
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: ["e2e-tests/playwright/specs/functional/login.spec.ts"],
    ai: {
      verdict: "PR_REGRESSION",
      confidence: 0.9,
      reason: "PR modified the failing spec; screenshot shows wrong product state",
      citations: ["screenshot", "changed_files"],
    },
  });
  assert.equal(d.verdict, "PR_REGRESSION", d.reason);
  assert.equal(d.waived, false, "spec-touching bug verdicts fail closed");
  assert.equal(d.check_state, "failure");
});

test("AI PR_REGRESSION on CI-only PR is overridden to FLAKY_INFRA", () => {
  const d = decide({
    failure: failure({
      file: "detox/e2e/test/login.e2e.ts",
      error_message: "ConnectToServer timed out",
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
    changedFiles: [".github/workflows/e2e-detox-pr.yml", "detox/e2e/support/quarantine.ts"],
    ai: {
      verdict: "PR_REGRESSION",
      confidence: 0.95,
      reason: "workflow changed",
      citations: ["changed_files"],
    },
  });
  assert.equal(d.waived, true, d.reason);
  assert.equal(d.verdict, "FLAKY_INFRA");
});
