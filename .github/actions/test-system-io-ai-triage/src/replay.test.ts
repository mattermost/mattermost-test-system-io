/**
 * Replay's two invariants.
 *
 * 1. A replay verdict must be REACHABLE. Replay asks "what would the gate have
 *    done", so it decides in gate mode; running it in shadow would record
 *    waived=false everywhere and measure nothing. This is the mistake that
 *    makes a measurement window produce a month of unusable rows, so it is
 *    pinned here rather than left to the caller.
 *
 * 2. A replay verdict must be DISTINGUISHABLE from a live one. It is decided
 *    with later runs of the same test already in the database, so counting it
 *    in the live accuracy figure would overstate what CI does.
 */
import { test } from "node:test";
import * as assert from "node:assert/strict";

import { clampInt } from "./replay.ts";
import { decide } from "./policy.ts";
import type { EvidenceFailure } from "./types.ts";

const chronicFlake = (): EvidenceFailure =>
  ({
    external_test_id: "MM-T2001",
    full_title: "channel switcher opens",
    file: "e2e-tests/playwright/specs/channels/switcher.spec.ts",
    status: "failed",
    retry_count: 0,
    error_message: "Timed out waiting for the switcher dialog",
    error_stack: "at switcher.spec.ts:41",
    screenshots: [],
    history: { runs: 20, failed: 8, flaky: 0, flips: 6, failure_rate: 0.4 },
    amnesty: { granted: true },
    suggested: {
      verdict: "FLAKY_TEST",
      confidence: 0.95,
      needs_ai: false,
      reason: "history",
      citations: ["history", "failing_elsewhere"],
    },
  }) as unknown as EvidenceFailure;

test("replay decides in gate mode, or it measures nothing", () => {
  const args = {
    failure: chronicFlake(),
    runType: "PR",
    branch: "feat/x",
    changedFiles: ["webapp/channels/src/components/unrelated/thing.tsx"],
  };

  // What replay does.
  const measured = decide({ ...args, mode: "gate" });
  assert.equal(measured.waived, true, "a clean chronic flake must be waivable in a replay");
  assert.equal(measured.check_state, "success");

  // What a shadow-mode replay would have recorded: nothing usable.
  const useless = decide({ ...args, mode: "shadow" });
  assert.equal(useless.waived, false);
  assert.match(useless.reason, /shadow mode observes only/);
});

test("replay never softens a PR regression — gate mode is not a free pass", () => {
  const broken = chronicFlake();
  // Same test, but the PR touched the failing file: attribution is no longer
  // explained by the test's own flakiness.
  const d = decide({
    failure: broken,
    runType: "PR",
    branch: "feat/x",
    changedFiles: ["e2e-tests/playwright/specs/channels/switcher.spec.ts"],
    mode: "gate",
  });
  assert.equal(d.waived, false, "diff overlap must refuse the waiver in replay too");
  assert.equal(d.check_state, "failure");
});

test("clampInt keeps a bad input from losing the batch", () => {
  assert.equal(clampInt("45", 30, 1, 180), 45);
  assert.equal(clampInt("", 30, 1, 180), 30, "unset falls back to the default");
  assert.equal(clampInt("banana", 30, 1, 180), 30, "garbage falls back, never NaN");
  assert.equal(clampInt("0", 30, 1, 180), 1, "below range clamps up");
  assert.equal(clampInt("9999", 30, 1, 180), 180, "above range clamps down");
  assert.equal(clampInt("-5", 20, 1, 500), 1);
});
