import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  canWaive,
  canWaiveAtPhase,
  modeForPhase,
  parsePhasePayload,
  mayFlipChecks,
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
    phase: 2,
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
    phase: 2,
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
    phase: 2,
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
    phase: 2,
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
    phase: 2,
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

// --- W6/W13 full matrix: 3 run types × 4 phases × the verdict set ---

test("W6 matrix: MAIN never waives MAIN_REGRESSION at any phase", () => {
  for (const phase of [0, 1, 2, 3]) {
    const w = canWaiveAtPhase({
      runType: "MAIN",
      branch: "main",
      phase,
      verdict: "MAIN_REGRESSION",
      confidence: 1,
      citations: ["failing_on_baseline", "failing_elsewhere"],
      amnestyGranted: true,
      diffOverlapsFailure: false,
    });
    assert.equal(w.waived, false, `phase ${phase}`);
  }
});

test("W6 matrix: RELEASE waives nothing at any phase", () => {
  for (const phase of [0, 1, 2, 3]) {
    for (const verdict of ["FLAKY_TEST", "FLAKY_INFRA", "MAIN_REGRESSION"]) {
      const w = canWaiveAtPhase({
        runType: "RELEASE",
        branch: "release-10.6",
        phase,
        verdict,
        confidence: 1,
        citations: ["history", "failing_elsewhere"],
        amnestyGranted: true,
        diffOverlapsFailure: false,
      });
      assert.equal(w.waived, false, `phase ${phase} verdict ${verdict}`);
    }
  }
});

test("W6/W13 matrix: PR gates from phase 1; MAIN confirmed flakes only from phase 2", () => {
  const prArgs = {
    runType: "PR",
    branch: "feat/x",
    verdict: "FLAKY_TEST",
    confidence: 0.95,
    citations: ["history", "failing_elsewhere"],
    amnestyGranted: true,
    diffOverlapsFailure: false,
  };
  assert.equal(canWaiveAtPhase({ ...prArgs, phase: 0 }).waived, false, "PR shadow at phase 0");
  assert.equal(canWaiveAtPhase({ ...prArgs, phase: 1 }).waived, true, "PR gates at phase 1");

  const mainArgs = { ...prArgs, runType: "MAIN", branch: "main" };
  assert.equal(
    canWaiveAtPhase({ ...mainArgs, phase: 1 }).waived,
    false,
    "MAIN still shadow at phase 1",
  );
  assert.equal(canWaiveAtPhase({ ...mainArgs, phase: 2 }).waived, true, "MAIN gates at phase 2");
  assert.equal(canWaiveAtPhase({ ...mainArgs, phase: 3 }).waived, true, "MAIN gates at phase 3");
});

test("W13: modeForPhase ladder", () => {
  assert.equal(modeForPhase("PR", 0), "shadow");
  assert.equal(modeForPhase("PR", 1), "gate");
  assert.equal(modeForPhase("MAIN", 1), "shadow");
  assert.equal(modeForPhase("MAIN", 2), "gate");
  assert.equal(modeForPhase("RELEASE", 1), "gate"); // gates, but policy waives nothing
  assert.equal(modeForPhase("", 0), "shadow");
});

// --- B4/B2/B3 regression: fail-closed phase parse; ledger-gated flips ---

test("B4: parsePhasePayload fails closed on every non-conforming shape", () => {
  assert.equal(parsePhasePayload({ phase: "gremlin" }), 0);
  assert.equal(parsePhasePayload({ phase: {} }), 0);
  assert.equal(parsePhasePayload({ phase: null }), 0);
  assert.equal(parsePhasePayload({ phase: 1.5 }), 0);
  assert.equal(parsePhasePayload({ phase: -1 }), 0);
  assert.equal(parsePhasePayload({ phase: 4 }), 0);
  assert.equal(parsePhasePayload("phase-2"), 0);
  assert.equal(parsePhasePayload(null), 0);
  // Conforming values pass through.
  assert.equal(parsePhasePayload({ phase: 0 }), 0);
  assert.equal(parsePhasePayload({ phase: 2 }), 2);
  assert.equal(parsePhasePayload({ phase: 3 }), 3);
});

test("B2/B3: gate mode refuses flips when the ledger did not record", () => {
  const refused = mayFlipChecks("gate", false);
  assert.equal(refused.allowed, false);
  assert.match(refused.reason ?? "", /refusing to flip/);
  assert.equal(mayFlipChecks("gate", true).allowed, true);
});

test("B2/B3: shadow mode tolerates a ledger skip — it flips nothing anyway", () => {
  const tolerated = mayFlipChecks("shadow", false);
  assert.equal(tolerated.allowed, true);
  assert.match(tolerated.reason ?? "", /shadow mode observes/);
});

// ---------------------------------------------------------------------------
// R7-B — the rate-shift gate.
//
// The most expensive error class (a historically flaky test that this time
// broke for real) must not rest on model judgment. These tests assert the
// gate refuses the waiver from the signal alone, whatever the model said.
// ---------------------------------------------------------------------------

/** A shifted comparison: 40% on the baseline, 3-of-3 here. p = 0.064 <= 0.10. */
function shiftedRate(): NonNullable<EvidenceFailure["rate_shift"]> {
  return {
    ok: true,
    baseline_runs: 20,
    baseline_failed: 8,
    baseline_rate: 0.4,
    pr_runs: 3,
    pr_failed: 3,
    pr_rate: 1,
    p_value: 0.064,
    shifted: true,
    alpha: 0.1,
  };
}

/** An unshifted comparison: 40% on the baseline, 1-of-3 here. p = 0.784. */
function unshiftedRate(): NonNullable<EvidenceFailure["rate_shift"]> {
  return {
    ok: true,
    baseline_runs: 20,
    baseline_failed: 8,
    baseline_rate: 0.4,
    pr_runs: 3,
    pr_failed: 1,
    pr_rate: 0.333,
    p_value: 0.784,
    shifted: false,
    alpha: 0.1,
  };
}

const flakyWaiveArgs = {
  runType: "PR",
  branch: "feat/x",
  verdict: "FLAKY_TEST",
  confidence: 0.92,
  citations: ["screenshot", "history"],
  amnestyGranted: true,
  diffOverlapsFailure: false,
};

test("R7-B: a shifted failure rate refuses a FLAKY_* waiver", () => {
  const w = canWaive({ ...flakyWaiveArgs, rateShiftedAtCommit: true });
  assert.equal(w.waived, false);
  assert.match(w.reason, /rate shifted materially/);
});

test("R7-B: an unshifted failure rate leaves the waiver alone", () => {
  const w = canWaive({ ...flakyWaiveArgs, rateShiftedAtCommit: false });
  assert.equal(w.waived, true);
  assert.equal(w.reason, "FLAKY_TEST");
});

test("R7-B: an absent shift signal never refuses — it is not evidence of no shift", () => {
  const w = canWaive(flakyWaiveArgs);
  assert.equal(w.waived, true, "a missing comparison must preserve prior behaviour");
});

test("R7-B: the gate applies to every FLAKY_* verdict, not just FLAKY_TEST", () => {
  for (const verdict of ["FLAKY_TEST", "FLAKY_INFRA", "FLAKY_SERVER"]) {
    const w = canWaive({ ...flakyWaiveArgs, verdict, rateShiftedAtCommit: true });
    assert.equal(w.waived, false, `${verdict} must be refused on a shifted rate`);
    assert.match(w.reason, /rate shifted materially/);
  }
});

test("R7-B: confidence cannot buy past the gate — it is a gate, not a score", () => {
  for (const confidence of [0.85, 0.9, 0.99, 1]) {
    const w = canWaive({ ...flakyWaiveArgs, confidence, rateShiftedAtCommit: true });
    assert.equal(w.waived, false, `confidence ${confidence} must not clear the gate`);
  }
});

test("R7-B: the gate does not touch the pre-existing MAIN_REGRESSION carve-out", () => {
  // A bystander PR hitting a failure already failing on the baseline is not a
  // flake waiver, and the shift gate must not widen its blast radius into it.
  const w = canWaive({
    runType: "PR",
    branch: "feat/x",
    verdict: "MAIN_REGRESSION",
    confidence: 0.95,
    citations: ["failing_on_baseline", "failing_elsewhere"],
    amnestyGranted: true,
    diffOverlapsFailure: false,
    rateShiftedAtCommit: true,
  });
  assert.equal(w.waived, true);
  assert.match(w.reason, /pre-existing on the baseline/);
});

/**
 * The ABAC cases, end to end through decide().
 *
 * Both were waived 2/2 in the round-6 backtest at a stated confidence of 0.90:
 * the model read the diff, saw a 40% historical failure rate, and called it a
 * flake. The diff is 30 files all under .github/, so diffOverlaps is false and
 * the existing overlap gate cannot fire. The rate-shift gate is the only thing
 * standing between that verdict and a false green.
 */
for (const testId of ["MM-T5824", "MM-T5820"]) {
  test(`R7-B: ${testId} (ABAC, pr-37732) is refused on the rate shift`, () => {
    const f = failure({
      external_test_id: testId,
      full_title: `system_console › abac › ${testId} file permissions render`,
      title: `${testId} file permissions render`,
      file: "e2e-tests/playwright/specs/functional/system_console/abac/file_access/file_permissions_render.spec.ts",
      error_message: `policy "test-policy" should appear after search — Expected: true, Received: false`,
      suggested: {
        verdict: "FLAKY_TEST",
        confidence: 0.8,
        needs_ai: true,
        reason: "historically unstable",
        citations: ["flip_count", "historical_failure_rate", "rate_shifted_at_commit"],
      },
      rate_shift: shiftedRate(),
    });
    const d = decide({
      failure: f,
      runType: "PR",
      branch: "cherry-pick-abac",
      // The real diff: 30 files, all under .github/ — CI-only, so the
      // diff-overlap gate is false and cannot save this case.
      changedFiles: [".github/workflows/e2e-tests.yml", ".github/actions/x/action.yml"],
      ai: {
        verdict: "FLAKY_TEST",
        confidence: 0.9,
        reason: "the history shows a high failure rate (40%) with multiple flips",
        citations: ["history", "pr_diff"],
      },
      phase: 1,
    });
    assert.equal(d.waived, false, `${testId} must not green — this was a false green in round 6`);
    assert.equal(d.check_state, "failure");
    assert.match(d.reason, /rate shifted materially/);
  });

  test(`R7-B: ${testId} still waives when the rate did NOT shift`, () => {
    // The control: same test, same 40% history, but it only failed once here.
    // That IS ordinary flakiness and must stay waivable, or the gate is just
    // a blanket refusal for every flaky test.
    const f = failure({
      external_test_id: testId,
      rate_shift: unshiftedRate(),
      suggested: {
        verdict: "FLAKY_TEST",
        confidence: 0.8,
        needs_ai: true,
        reason: "historically unstable",
        citations: ["flip_count", "historical_failure_rate"],
      },
    });
    const d = decide({
      failure: f,
      runType: "PR",
      branch: "cherry-pick-abac",
      changedFiles: [".github/workflows/e2e-tests.yml"],
      ai: {
        verdict: "FLAKY_TEST",
        confidence: 0.9,
        reason: "recovered, matches its usual flake signature",
        citations: ["history", "pr_diff"],
      },
      phase: 1,
    });
    assert.equal(d.waived, true, `${testId} at its baseline rate must stay waivable`);
    assert.equal(d.check_state, "success");
  });
}

test("R7-B: decide() reads the shift from the pack and never recomputes it", () => {
  // ok:false with shifted:true is a contradictory pack. The action must honour
  // the server's `shifted` verbatim — one threshold, one place (rateshift.go).
  const d = decide({
    failure: failure({
      rate_shift: { ...shiftedRate(), ok: false },
      suggested: {
        verdict: "FLAKY_TEST",
        confidence: 0.8,
        needs_ai: true,
        reason: "historically unstable",
        citations: ["flip_count", "historical_failure_rate"],
      },
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: [".github/workflows/e2e.yml"],
    ai: {
      verdict: "FLAKY_TEST",
      confidence: 0.9,
      reason: "flake",
      citations: ["history", "pr_diff"],
    },
    phase: 1,
  });
  assert.equal(d.waived, false, "shifted:true is authoritative regardless of ok");
});
