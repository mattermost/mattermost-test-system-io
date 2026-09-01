/**
 * W11 — product-bug routing decision.
 *
 * The stabilization loop repairs TESTS. When its diagnosis says the failure is
 * a product bug, the loop must NOT attempt a fix: file to the owning team
 * with the evidence and close the queue item as routed. This is that decision
 * as a pure function, so it is testable before the loop (W14 action half)
 * exists.
 *
 * Routing target resolution (CODEOWNERS) is the caller's job: W0 found no
 * e2e-tests/** entry in mattermost's CODEOWNERS, so until the team adds
 * entries, resolveOwner() falls back to the test-infra team rather than
 * guessing a product team from spec paths.
 */

export type RoutingDecision =
  | { action: "proceed"; reason: string }
  | { action: "route"; reason: string; owner: string; evidence: string[] };

/** Verdicts that mean the PRODUCT is broken, not the test. */
const PRODUCT_BUG_VERDICTS = new Set(["PR_REGRESSION", "MAIN_REGRESSION"]);

/** Verdicts that are unambiguously test-side and may be fixed by the loop. */
const TEST_SIDE_VERDICTS = new Set(["TEST_DEBT"]);

export function routeVerdict(input: {
  verdict: string;
  productRejection?: boolean;
  suspectFiles?: string[];
  e2eRoot?: string;
}): RoutingDecision {
  // A product refusal (the product deliberately said no) is a product bug by
  // definition, whatever the verdict label says.
  if (input.productRejection) {
    return {
      action: "route",
      reason: "product deliberately refused the action — not a test bug",
      owner: resolveOwner(input.suspectFiles ?? []),
      evidence: ["product_refusal"],
    };
  }
  if (PRODUCT_BUG_VERDICTS.has(input.verdict)) {
    return {
      action: "route",
      reason: `${input.verdict} — the change under test broke behavior; the loop repairs tests, not products`,
      owner: resolveOwner(input.suspectFiles ?? []),
      evidence: [`verdict:${input.verdict}`],
    };
  }
  if (input.verdict === "INCONCLUSIVE" || input.verdict === "BUILD_OR_ENV_ERROR") {
    return {
      action: "route",
      reason: `${input.verdict} — outside the loop's remit; a human or the infra queue owns it`,
      owner: "test-infra",
      evidence: [`verdict:${input.verdict}`],
    };
  }
  if (TEST_SIDE_VERDICTS.has(input.verdict) || input.verdict.startsWith("FLAKY_")) {
    return { action: "proceed", reason: `${input.verdict} — test-side, the loop may repair it` };
  }
  // Unknown verdict: fail closed to routing, never to a blind fix.
  return {
    action: "route",
    reason: `unknown verdict ${input.verdict} — fail closed to a human`,
    owner: "test-infra",
    evidence: [`verdict:${input.verdict}`],
  };
}

/**
 * Owner resolution. Product files under webapp/ or server/ would map to their
 * CODEOWNERS teams, but W0 found no e2e-tests entry and the loop only edits
 * e2e-tests/** — so the only files it ever names are test files, and the
 * fallback (test-infra) is the honest answer until CODEOWNERS grows entries.
 */
export function resolveOwner(suspectFiles: string[]): string {
  const productTouched = suspectFiles.some(
    (f) => !f.startsWith("e2e-tests/") && /\.(ts|tsx|js|jsx|go)$/.test(f),
  );
  return productTouched ? "codeowners-of-changed-product-files" : "test-infra";
}