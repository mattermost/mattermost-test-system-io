import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  canWaive,
  decide,
  diffOverlaps,
  isProductRejection,
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

test("model-observed screenshot refusal blocks flake waivers (MM-67594_13 case)", () => {
  const d = decide({
    failure: failure({
      status: "failed",
      retry_count: 1,
      // The banner text is ONLY in the screenshot — error text is a bare timeout.
      error_message: "TimeoutError: locator.waitFor: Timeout 30000ms exceeded.",
      suggested: {
        verdict: "FLAKY_INFRA",
        confidence: 0.92,
        needs_ai: true,
        reason: "recovered on retry",
        citations: ["this_run_recovered"],
      },
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [],
    ai: {
      verdict: "FLAKY_INFRA",
      confidence: 0.92,
      reason: "screenshot shows red server rejection banner",
      citations: ["screenshot", "this_run_recovered"],
      product_refusal: true,
    },
  });
  assert.equal(d.waived, false, "screenshot-observed refusal must stay red");
  assert.ok(d.reason.includes("deliberately refusing"), d.reason);
});

test("product-rejection errors are never waived as flake, regardless of confidence", () => {
  const d = decide({
    failure: failure({
      status: "failed",
      retry_count: 1,
      error_message:
        "TimeoutError waiting for .TeamPolicyConfirmationModal: server said: You cannot save these rules because they would remove your access to this policy",
      suggested: {
        verdict: "FLAKY_SERVER",
        confidence: 0.85,
        needs_ai: true,
        reason: "recovered on retry",
        citations: ["this_run_recovered"],
      },
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [],
    ai: {
      verdict: "FLAKY_SERVER",
      confidence: 1,
      reason: "recovered, no product overlap",
      citations: ["this_run_recovered", "history"],
    },
  });
  assert.equal(d.verdict, "FLAKY_SERVER");
  assert.equal(d.waived, false, "rejection must stay red for human review");
  assert.ok(d.reason.includes("deliberately refusing"), d.reason);
});

test("filesystem EACCES is infra noise, not a product rejection", () => {
  assert.equal(isProductRejection("EACCES: permission denied, open '/tmp/app.db'"), false);
  assert.equal(
    isProductRejection("You cannot save these rules because they would remove your access"),
    true,
  );
  assert.equal(isProductRejection(undefined, undefined), false);
});

test("waivers at the confidence floor are flagged borderline", () => {
  const floor = decide({
    failure: failure(),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [],
    ai: {
      verdict: "FLAKY_TEST",
      confidence: 0.85,
      reason: "timing race",
      citations: ["screenshot", "history", "this_run_recovered"],
    },
  });
  assert.equal(floor.waived, true);
  assert.equal(floor.borderline, true);

  const solid = decide({
    failure: failure(),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [],
    ai: {
      verdict: "FLAKY_TEST",
      confidence: 0.95,
      reason: "timing race",
      citations: ["screenshot", "history", "this_run_recovered"],
    },
  });
  assert.equal(solid.waived, true);
  assert.equal(solid.borderline, false);

  // Unwaived decisions are never borderline — the row is already red.
  const red = decide({
    failure: failure({
      status: "failed",
      retry_count: 0,
      suggested: {
        verdict: "TEST_DEBT",
        confidence: 0.9,
        needs_ai: false,
        reason: "stayed red",
        citations: ["error_message"],
      },
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [],
  });
  assert.equal(red.waived, false);
  assert.equal(red.borderline, false);
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

// --- W4/W6: run-type waiver policy + bystander amnesty carve-out ---

test("W6: MAIN never waives MAIN_REGRESSION — the baseline is this run", () => {
  const w = canWaive({
    runType: "MAIN",
    branch: "main",
    verdict: "MAIN_REGRESSION",
    confidence: 1,
    citations: ["failing_on_baseline", "failing_elsewhere"],
    amnestyGranted: true,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, false);
  assert.match(w.reason, /MAIN runs never waive MAIN_REGRESSION/);
});

test("W4: bystander PR waives a pre-existing baseline failure even with amnesty expired", () => {
  const w = canWaive({
    runType: "PR",
    branch: "feat/unrelated",
    verdict: "MAIN_REGRESSION",
    confidence: 0.95,
    citations: ["failing_on_baseline", "failing_elsewhere"],
    amnestyGranted: false,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, true);
  assert.equal(w.reason, "pre-existing on the baseline branch");
});

test("W4: expired amnesty still denies FLAKY on a MAIN run — master goes hard red", () => {
  const w = canWaive({
    runType: "MAIN",
    branch: "main",
    verdict: "FLAKY_TEST",
    confidence: 0.95,
    citations: ["history", "failing_elsewhere"],
    amnestyGranted: false,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, false);
  assert.equal(w.reason, "amnesty denied");
});

test("W4: expired amnesty still denies FLAKY on a PR — the PR is not a bystander to its own flake", () => {
  const w = canWaive({
    runType: "PR",
    branch: "feat/x",
    verdict: "FLAKY_TEST",
    confidence: 0.95,
    citations: ["history", "failing_elsewhere"],
    amnestyGranted: false,
    diffOverlapsFailure: false,
  });
  assert.equal(w.waived, false);
  assert.equal(w.reason, "amnesty denied");
});
