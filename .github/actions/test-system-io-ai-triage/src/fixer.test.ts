import assert from "node:assert/strict";
import { test } from "node:test";
import type { Decision, EvidenceCluster } from "./types.ts";
import { collectFixTargets, isFixable } from "./fixer.ts";

function decision(over: Partial<Decision> = {}): Decision {
  return {
    verdict: "TEST_DEBT",
    confidence: 0.88,
    reason: "test drives product into unsupported state",
    citations: ["error_message", "screenshot"],
    waived: false,
    source: "model",
    check_state: "failure",
    kind: "bug",
    member_count: 1,
    ...over,
  };
}

function cluster(over: Partial<EvidenceCluster> = {}): EvidenceCluster {
  return {
    signature: "abcd1234efgh5678",
    label: "MM-T5795 join refused",
    member_count: 1,
    representative: {
      full_title: "MM-T5795 User can be added by admin after attribute added",
      file: "e2e-tests/playwright/specs/abac/join_channel.spec.ts",
      status: "failed",
      retry_count: 1,
      error_message: "User does not have required attributes to join the channel",
      screenshots: [{ s3_key: "orchestration/x.png" }],
      suggested: {
        verdict: "TEST_DEBT",
        confidence: 0.9,
        needs_ai: true,
        reason: "",
        citations: [],
      },
      ...over,
    } as EvidenceCluster["representative"],
    suggested: { verdict: "TEST_DEBT", confidence: 0.9, needs_ai: true, reason: "", citations: [] },
    ...over,
  } as EvidenceCluster;
}

test("TEST_DEBT on a pre-existing spec is fixable", () => {
  assert.equal(isFixable(decision(), cluster(), []), true);
});

test("refusal-blocked flake verdicts are fixable (test drives unsupported state)", () => {
  assert.equal(
    isFixable(
      decision({
        verdict: "FLAKY_INFRA",
        kind: "flaky",
        confidence: 0.93,
        refusal: true,
      }),
      cluster(),
      [],
    ),
    true,
  );
});

test("the author's own new specs are never auto-fixed (demo sentinel protection)", () => {
  const c = cluster();
  c.representative.file = "e2e-tests/playwright/specs/fuzz/ai_triage_demo.spec.ts";
  assert.equal(
    isFixable(decision({ confidence: 0.97 }), c, [
      "e2e-tests/playwright/specs/fuzz/ai_triage_demo.spec.ts",
    ]),
    false,
  );
});

test("product regressions are not fixable by editing tests", () => {
  assert.equal(isFixable(decision({ verdict: "PR_REGRESSION" }), cluster(), []), false);
  assert.equal(isFixable(decision({ verdict: "MAIN_REGRESSION" }), cluster(), []), false);
});

test("low-confidence and waived decisions are not fixable", () => {
  assert.equal(isFixable(decision({ confidence: 0.82 }), cluster(), []), false);
  assert.equal(isFixable(decision({ waived: true }), cluster(), []), false);
});

test("non test-framework paths are excluded", () => {
  const c = cluster();
  c.representative.file = "server/cmd/mattermost/main.go";
  assert.equal(isFixable(decision(), c, []), false);
});

test("collectFixTargets caps at max and skips ineligible", () => {
  const clusters = [
    cluster(),
    cluster({ signature: "22222222" }),
    cluster({ signature: "33333333" }),
  ];
  const decisions = [
    decision(),
    decision({ confidence: 0.5 }), // ineligible
    decision({ confidence: 0.9 }),
  ];
  const targets = collectFixTargets(clusters, decisions, [], 2);
  assert.equal(targets.length, 2);
  assert.equal(targets[0]!.signature, "abcd1234efgh5678");
  assert.equal(targets[1]!.signature, "33333333");
  assert.equal(targets[0]!.screenshots.join(","), "orchestration/x.png");
});
