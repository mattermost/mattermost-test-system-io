import type { ClaudeVerdict, Decision, EvidenceFailure, Suggestion } from "./types.ts";
import { kindOf } from "./blame.ts";

export const WAIVE_CONFIDENCE = 0.85;

const FLAKY = new Set(["FLAKY_TEST", "FLAKY_INFRA", "FLAKY_SERVER"]);
const NEVER_WAIVE = new Set(["PR_REGRESSION", "INCONCLUSIVE", "TEST_DEBT", "BUILD_OR_ENV_ERROR"]);

/**
 * RELEASE / release-* never auto-waive (CMT / release trains stay fail-closed).
 * MAIN may waive confirmed flakes so required e2e-test/* checks on main go
 * green — otherwise Create Release Branches cannot push release-* from a
 * flaky main commit (no PR labels apply to that path).
 */
export function neverAutoWaive(runType: string, branch: string): boolean {
  const t = (runType || "").toUpperCase();
  if (t === "RELEASE") return true;
  const b = (branch || "").toLowerCase();
  return b.startsWith("release-") || b.startsWith("release/");
}

/** @deprecated use neverAutoWaive — kept for call sites during rename */
export function isProtectedRun(runType: string, branch: string): boolean {
  return neverAutoWaive(runType, branch);
}

/** Paths that every Detox run touches; editing them must not block flake waivers. */
export function isSharedHarness(path: string): boolean {
  const p = normalizePath(path);
  return (
    p.startsWith(".github/") ||
    p.startsWith("detox/e2e/support/") ||
    p.startsWith("detox/utils/") ||
    p === "detox/create_android_emulator.sh" ||
    p.endsWith(".md") ||
    /\.(test|spec)\.(ts|tsx|js|jsx)$/.test(p)
  );
}

/** True when the PR only touched CI/harness/docs — not product or the failing spec. */
export function isCIOnlyDiff(changedFiles: string[]): boolean {
  const files = changedFiles.map(normalizePath).filter(Boolean);
  if (files.length === 0) return false;
  return files.every(isSharedHarness);
}

function normalizePath(p: string): string {
  return p.replace(/^\.\//, "").replace(/\\/g, "/");
}

/** True when the PR changed the failing spec itself (not a vague substring). */
function pathsMatch(spec: string, changed: string): boolean {
  if (spec === changed) return true;
  if (spec.endsWith("/" + changed) || changed.endsWith("/" + spec)) return true;
  const specBase = spec.split("/").pop() || "";
  const changedBase = changed.split("/").pop() || "";
  return Boolean(specBase) && specBase === changedBase && specBase.includes(".");
}

/** Stack frames mention the changed file as a path, not as an accidental substring. */
function stackMentions(stack: string, changed: string): boolean {
  if (changed.length < 8) return false;
  if (stack.includes(changed)) return true;
  const base = changed.split("/").pop() || "";
  if (base.length < 8 || !base.includes(".")) return false;
  return new RegExp(`(?:^|[\\s(/])${escapeRegExp(base)}(?::\\d|\\)|$)`, "m").test(stack);
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export function diffOverlaps(changedFiles: string[], specFile?: string, stack?: string): boolean {
  const files = changedFiles.map(normalizePath).filter((f) => !isSharedHarness(f));
  if (specFile) {
    const spec = normalizePath(specFile);
    if (files.some((f) => pathsMatch(spec, f))) return true;
  }
  if (!stack) return false;
  return files.some((f) => stackMentions(stack, f));
}

export function hasAdjudicationEvidence(failure: EvidenceFailure): boolean {
  if ((failure.screenshots || []).length > 0) return true;
  if ((failure.error_message || "").trim().length > 0) return true;
  if ((failure.error_stack || "").trim().length > 0) return true;
  return false;
}

export function canWaive(args: {
  runType: string;
  branch: string;
  verdict: string;
  confidence: number;
  citations: string[];
  amnestyGranted?: boolean;
  diffOverlapsFailure: boolean;
}): { waived: boolean; reason: string } {
  if (neverAutoWaive(args.runType, args.branch)) {
    return { waived: false, reason: "release runs never auto-waive" };
  }
  if (NEVER_WAIVE.has(args.verdict)) {
    return { waived: false, reason: `${args.verdict} is not waivable` };
  }
  if (args.diffOverlapsFailure && FLAKY.has(args.verdict)) {
    return { waived: false, reason: "PR diff touches the failing area — attribution is ambiguous" };
  }
  if (args.amnestyGranted === false) {
    return { waived: false, reason: "amnesty denied" };
  }
  if (args.confidence < WAIVE_CONFIDENCE) {
    return { waived: false, reason: `confidence ${args.confidence} < ${WAIVE_CONFIDENCE}` };
  }
  const measured = args.citations.includes("this_run_recovered");
  if (!measured && args.citations.length < 2) {
    return { waived: false, reason: "need two independent citations (or in-run recovery)" };
  }
  if (FLAKY.has(args.verdict)) {
    return { waived: true, reason: args.verdict };
  }
  if (args.verdict === "MAIN_REGRESSION" && args.citations.includes("failing_on_baseline")) {
    return { waived: true, reason: "pre-existing on the baseline branch" };
  }
  return { waived: false, reason: `${args.verdict} is not waivable` };
}

export function decide(args: {
  failure: EvidenceFailure;
  runType: string;
  branch: string;
  changedFiles: string[];
  ai?: ClaudeVerdict;
}): Decision {
  const suggested: Suggestion = args.failure.suggested;
  const overlaps = diffOverlaps(args.changedFiles, args.failure.file, args.failure.error_stack);
  const merged = mergeModel(suggested, args.ai, overlaps, args.failure, args.changedFiles);
  const waiver = canWaive({
    runType: args.runType,
    branch: args.branch,
    verdict: merged.verdict,
    confidence: merged.confidence,
    citations: merged.citations,
    amnestyGranted: args.failure.amnesty?.granted,
    diffOverlapsFailure: overlaps,
  });
  return {
    ...merged,
    waived: waiver.waived,
    check_state: waiver.waived ? "success" : "failure",
    reason: waiver.waived ? merged.reason : `${merged.reason} (${waiver.reason})`,
    kind: kindOf(merged.verdict),
    member_count: 1,
  };
}

function mergeModel(
  suggested: Suggestion,
  ai: ClaudeVerdict | undefined,
  overlaps: boolean,
  failure: EvidenceFailure,
  changedFiles: string[],
): {
  verdict: string;
  confidence: number;
  reason: string;
  citations: string[];
  source: Decision["source"];
} {
  let merged: {
    verdict: string;
    confidence: number;
    reason: string;
    citations: string[];
    source: Decision["source"];
  };

  if (!ai) {
    merged = {
      verdict: suggested.verdict,
      confidence: suggested.confidence,
      reason: suggested.reason,
      citations: suggested.citations,
      source: "history",
    };
  } else if (overlaps && FLAKY.has(ai.verdict)) {
    merged = {
      verdict: suggested.verdict === "INCONCLUSIVE" ? "PR_REGRESSION" : suggested.verdict,
      confidence: Math.min(suggested.confidence, 0.6),
      reason: `${ai.reason} — ignored: PR diff overlaps the failing area`,
      citations: suggested.citations,
      source: "policy",
    };
  } else {
    merged = {
      verdict: ai.verdict,
      confidence: ai.confidence,
      reason: ai.reason,
      citations: unique([...suggested.citations, ...ai.citations]),
      source: "model",
    };
  }

  return enforceDecisiveVerdict(merged, failure, changedFiles, overlaps);
}

/**
 * With screenshots / error / stack in hand, refuse to leave the run as
 * INCONCLUSIVE. Also refuse PR_REGRESSION / TEST_DEBT when the PR only
 * touched CI/harness — that was poisoning #9996 dogfood.
 */
export function enforceDecisiveVerdict(
  merged: {
    verdict: string;
    confidence: number;
    reason: string;
    citations: string[];
    source: Decision["source"];
  },
  failure: EvidenceFailure,
  changedFiles: string[],
  overlaps: boolean,
): {
  verdict: string;
  confidence: number;
  reason: string;
  citations: string[];
  source: Decision["source"];
} {
  const evidence = hasAdjudicationEvidence(failure);
  const ciOnly = isCIOnlyDiff(changedFiles);
  const cites = [...merged.citations];

  if (
    evidence &&
    (failure.screenshots || []).length > 0 &&
    !cites.some((c) => /screenshot/i.test(c))
  ) {
    cites.push("screenshot");
  }
  if (evidence && (failure.error_message || "").trim() && !cites.some((c) => /error/i.test(c))) {
    cites.push("error_message");
  }

  // CI-only PR cannot be a product PR_REGRESSION / TEST_DEBT for a UI failure.
  if (
    !overlaps &&
    ciOnly &&
    (merged.verdict === "PR_REGRESSION" || merged.verdict === "TEST_DEBT")
  ) {
    return {
      verdict: "FLAKY_INFRA",
      confidence: Math.max(merged.confidence, WAIVE_CONFIDENCE),
      reason: `${merged.reason} — overridden: PR only touches CI/harness, not product code under test`,
      citations: unique([...cites, "ci_only_diff"]),
      source: "policy",
    };
  }

  // PR_REGRESSION means "this PR caused it" — impossible without product/spec overlap.
  if (!overlaps && merged.verdict === "PR_REGRESSION" && evidence) {
    const flakeKind = inferFlakeKind(failure.error_message || "", failure.error_stack || "");
    return {
      verdict: flakeKind,
      confidence: Math.max(merged.confidence, WAIVE_CONFIDENCE),
      reason: `${merged.reason} — overridden: PR does not touch this failure's product/spec area`,
      citations: unique([...cites, "no_product_overlap"]),
      source: "policy",
    };
  }

  // Mis-labeled TEST_DEBT on infra/server timeouts when this PR did not touch the failure.
  if (!overlaps && merged.verdict === "TEST_DEBT" && evidence) {
    const flakeKind = inferFlakeKind(failure.error_message || "", failure.error_stack || "");
    if (flakeKind === "FLAKY_INFRA" || flakeKind === "FLAKY_SERVER") {
      return {
        verdict: flakeKind,
        confidence: Math.max(merged.confidence, WAIVE_CONFIDENCE),
        reason: `${merged.reason} — overridden: infra/server signal with no product overlap`,
        citations: unique([...cites, "no_product_overlap"]),
        source: "policy",
      };
    }
  }

  if (merged.verdict === "INCONCLUSIVE" && evidence) {
    const flakeKind = inferFlakeKind(failure.error_message || "", failure.error_stack || "");
    return {
      verdict: flakeKind,
      confidence: Math.max(merged.confidence, WAIVE_CONFIDENCE),
      reason: `${merged.reason} — overridden: evidence present (error/screenshots/stack); INCONCLUSIVE forbidden`,
      citations: unique([...cites, "error_or_screenshot"]),
      source: "policy",
    };
  }

  return { ...merged, citations: unique(cites) };
}

function inferFlakeKind(error: string, stack: string): string {
  const text = `${error}\n${stack}`.toLowerCase();
  if (
    /enotfound|econnrefused|etimedout|dns|socket hang up|network request failed|server.*unreachable/.test(
      text,
    )
  ) {
    return "FLAKY_SERVER";
  }
  if (
    /emulator|simulator|metro|adb|device offline|bootstrap|connecttoserver|loginavailable|waitfor.*timeout/.test(
      text,
    )
  ) {
    return "FLAKY_INFRA";
  }
  return "FLAKY_TEST";
}

function unique(xs: string[]): string[] {
  return [...new Set(xs.filter(Boolean))];
}

export function rollup(decisions: Decision[]): {
  waived: boolean;
  verdict: string;
  state: "success" | "failure";
  description: string;
} {
  if (decisions.length === 0) {
    return { waived: true, verdict: "", state: "success", description: "no failures" };
  }
  const unwaived = decisions.filter((d) => !d.waived);
  if (unwaived.length === 0) {
    return {
      waived: true,
      verdict: decisions[0]!.verdict,
      state: "success",
      description: `${decisions.reduce((n, d) => n + d.member_count, 0)} failure(s) in ${decisions.length} cluster(s) classified as flaky/pre-existing`,
    };
  }
  const worst = unwaived[0]!;
  return {
    waived: false,
    verdict: worst.verdict,
    state: "failure",
    description: `${unwaived.length}/${decisions.length} cluster(s) unwaived (${worst.verdict})`,
  };
}
