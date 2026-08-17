/**
 * Cheap proof of the mobile MVP green path — no Detox farm required.
 *
 * Spec goal: after E2E posts a red e2e-test/detox-ios, triage classifies
 * failures as flaky with enough evidence and flips that context to success.
 *
 * Fixture mirrors PR #9996 dogfood (run 31990330011): harness files in the
 * PR diff must NOT block waivers; AI-confirmed FLAKY_* must waive and flip.
 */
import { test } from "node:test";
import * as assert from "node:assert/strict";
import { decide, rollup } from "./policy.ts";
import { contextsToFlip, flakeSuccessDescription } from "./flip.ts";
import type { EvidenceFailure } from "./types.ts";

/** Files from mattermost-mobile#9996 at the dogfood head (minus .github / .md). */
const PR_9996_CHANGED = [
  ".github/workflows/e2e-detox-pr.yml",
  "detox/README.md",
  "detox/create_android_emulator.sh",
  "detox/e2e/support/quarantine.ts",
  "detox/e2e/support/test_config.ts",
  "detox/e2e/test/products/channels/search/saved_messages.e2e.ts",
  "detox/utils/tsio-report-status.js",
  "app/components/post_draft/send_button/index.test.tsx",
  "app/utils/keyboard.test.ts",
  "app/actions/remote/entry/app.test.ts",
];

function histFlaky(over: Partial<EvidenceFailure> = {}): EvidenceFailure {
  return {
    full_title: "Channels › scroll fails",
    title: "scroll fails",
    file: "detox/e2e/test/products/channels/channel_list.e2e.ts",
    status: "failed",
    retry_count: 0,
    duration_ms: 5000,
    error_message: 'unable to scroll right in "<RCTEnhancedScrollView: 0xabc>"',
    error_stack:
      "Error: unable to scroll\n" +
      "    at scroll (detox/e2e/support/ui/screen.ts:10)\n" +
      "    at loadConfig (detox/e2e/support/test_config.ts:12)\n" +
      "    at Object.<anonymous> (detox/e2e/test/products/channels/channel_list.e2e.ts:40)",
    screenshots: [{ s3_key: "orchestration/x.png", url: "https://example/x.png" }],
    suggested: {
      verdict: "FLAKY_TEST",
      confidence: 0.8,
      needs_ai: true,
      reason: "historically unstable; screenshots can confirm",
      citations: ["flip_count", "historical_failure_rate"],
    },
    amnesty: { granted: true, reason: "under limit" },
    ...over,
  };
}

test("MVP: harness edits on #9996 do not block an AI flake waiver", () => {
  const d = decide({
    failure: histFlaky(),
    runType: "PR",
    branch: "claude/ai-e2e-failure-analysis-6e22f4",
    changedFiles: PR_9996_CHANGED,
    ai: {
      verdict: "FLAKY_TEST",
      confidence: 0.92,
      reason: "same spinner / scroll flake as history",
      citations: ["screenshot", "history"],
    },
  });
  assert.equal(d.waived, true, d.reason);
  assert.equal(d.check_state, "success");
  assert.equal(d.verdict, "FLAKY_TEST");
});

test("MVP: three waived flake clusters flip e2e-test/detox-ios in gate mode", () => {
  const decisions = ["scroll", "hittable", "archived"].map((title) =>
    decide({
      failure: histFlaky({ title, full_title: `Channels › ${title}` }),
      runType: "PR",
      branch: "claude/ai-e2e-failure-analysis-6e22f4",
      changedFiles: PR_9996_CHANGED,
      ai: {
        verdict: title === "archived" ? "FLAKY_SERVER" : "FLAKY_TEST",
        confidence: 0.9,
        reason: "flake",
        citations: ["screenshot", "history"],
      },
    }),
  );
  for (const d of decisions) {
    assert.equal(d.waived, true, d.reason);
  }
  const summary = rollup(decisions);
  assert.equal(summary.waived, true);
  assert.equal(summary.state, "success");

  const flip = contextsToFlip({
    mode: "gate",
    waived: summary.waived,
    hasFailures: true,
    explicit: ["e2e-test/detox-ios"],
    discovered: [],
    triageContext: "e2e-test/ai-triage-detox-ios",
  });
  assert.deepEqual(flip, ["e2e-test/detox-ios"]);
  assert.ok(
    flakeSuccessDescription("e2e-test/ai-triage-detox-ios", summary.description).length <= 140,
  );
});

test("MVP: one INCONCLUSIVE cluster keeps the original check red (fail closed)", () => {
  const ok = decide({
    failure: histFlaky(),
    runType: "PR",
    branch: "feat/x",
    changedFiles: PR_9996_CHANGED,
    ai: {
      verdict: "FLAKY_TEST",
      confidence: 0.9,
      reason: "ok",
      citations: ["screenshot", "history"],
    },
  });
  const bad = decide({
    failure: histFlaky({
      suggested: {
        verdict: "INCONCLUSIVE",
        confidence: 0,
        needs_ai: true,
        reason: "history does not decide",
        citations: [],
      },
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: PR_9996_CHANGED,
    // agent parse failure → no ai
  });
  assert.equal(ok.waived, true);
  assert.equal(bad.waived, false);
  const summary = rollup([ok, bad]);
  assert.equal(summary.waived, false);
  assert.deepEqual(
    contextsToFlip({
      mode: "gate",
      waived: summary.waived,
      hasFailures: true,
      explicit: ["e2e-test/detox-ios"],
      discovered: [],
      triageContext: "e2e-test/ai-triage-detox-ios",
    }),
    [],
  );
});

test("MVP: MAIN flake waiver flips e2e-test/detox-ios (release-branch source commit)", () => {
  const decisions = ["a", "b"].map((title) =>
    decide({
      failure: histFlaky({ title, full_title: `Channels › ${title}` }),
      runType: "MAIN",
      branch: "main",
      changedFiles: [],
      ai: {
        verdict: "FLAKY_TEST",
        confidence: 0.9,
        reason: "flake on main",
        citations: ["screenshot", "history"],
      },
    }),
  );
  for (const d of decisions) assert.equal(d.waived, true, d.reason);
  const summary = rollup(decisions);
  assert.equal(summary.waived, true);
  assert.deepEqual(
    contextsToFlip({
      mode: "gate",
      waived: summary.waived,
      hasFailures: true,
      explicit: ["e2e-test/detox-ios"],
      discovered: [],
      triageContext: "e2e-test/ai-triage-detox-ios",
    }),
    ["e2e-test/detox-ios"],
  );
});

test("MVP: changing the failing e2e spec itself still blocks a flake waiver", () => {
  const d = decide({
    failure: histFlaky({
      file: "detox/e2e/test/products/channels/search/saved_messages.e2e.ts",
    }),
    runType: "PR",
    branch: "feat/x",
    changedFiles: PR_9996_CHANGED,
    ai: {
      verdict: "FLAKY_TEST",
      confidence: 0.95,
      reason: "looks flaky",
      citations: ["screenshot", "history"],
    },
  });
  assert.equal(d.waived, false);
  assert.match(d.reason, /diff overlaps|ambiguous/i);
});
