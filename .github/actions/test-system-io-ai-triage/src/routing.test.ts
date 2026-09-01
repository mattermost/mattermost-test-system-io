import { test } from "node:test";
import * as assert from "node:assert/strict";
import { routeVerdict, resolveOwner } from "./routing.ts";

// W11 gate: a known product regression routes with evidence and never
// "proceeds"; a test-only flake proceeds; refusals route regardless of label.

test("W11: product regression routes, never proceeds", () => {
  const d = routeVerdict({ verdict: "MAIN_REGRESSION", suspectFiles: ["server/channels/app/channel.go"] });
  assert.equal(d.action, "route");
  assert.match(d.reason, /MAIN_REGRESSION/);
  if (d.action === "route") {
    assert.ok(d.evidence.includes("verdict:MAIN_REGRESSION"));
    assert.equal(d.owner, "codeowners-of-changed-product-files");
  }
});

test("W11: PR regression routes to the change's owners", () => {
  const d = routeVerdict({ verdict: "PR_REGRESSION", suspectFiles: ["webapp/src/x.ts"] });
  assert.equal(d.action, "route");
});

test("W11: product refusal routes even under a flaky label", () => {
  const d = routeVerdict({ verdict: "FLAKY_TEST", productRejection: true });
  assert.equal(d.action, "route");
  assert.match(d.reason, /deliberately refused/);
});

test("W11: test-side flake proceeds", () => {
  const d = routeVerdict({ verdict: "FLAKY_TEST", suspectFiles: ["e2e-tests/playwright/specs/x.spec.ts"] });
  assert.equal(d.action, "proceed");
});

test("W11: TEST_DEBT proceeds — the loop's bread and butter", () => {
  const d = routeVerdict({ verdict: "TEST_DEBT" });
  assert.equal(d.action, "proceed");
});

test("W11: INCONCLUSIVE and BUILD_OR_ENV_ERROR route to infra, fail closed", () => {
  assert.equal(routeVerdict({ verdict: "INCONCLUSIVE" }).action, "route");
  const d = routeVerdict({ verdict: "BUILD_OR_ENV_ERROR" });
  assert.equal(d.action, "route");
  if (d.action === "route") assert.equal(d.owner, "test-infra");
});

test("W11: unknown verdict fails closed to routing", () => {
  const d = routeVerdict({ verdict: "SOMETHING_NEW" });
  assert.equal(d.action, "route");
});

test("W11: owner resolution — test-only diffs fall back to test-infra", () => {
  assert.equal(resolveOwner(["e2e-tests/playwright/specs/a.spec.ts"]), "test-infra");
  assert.equal(resolveOwner(["server/app.go"]), "codeowners-of-changed-product-files");
});