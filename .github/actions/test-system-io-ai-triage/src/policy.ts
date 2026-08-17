import type { ClaudeVerdict, Decision, EvidenceFailure, Suggestion } from "./types.ts";

export const WAIVE_CONFIDENCE = 0.85;

const FLAKY = new Set(["FLAKY_TEST", "FLAKY_INFRA", "FLAKY_SERVER"]);
const NEVER_WAIVE = new Set(["PR_REGRESSION", "INCONCLUSIVE", "TEST_DEBT", "BUILD_OR_ENV_ERROR"]);

export function isProtectedRun(runType: string, branch: string): boolean {
  const t = (runType || "").toUpperCase();
  if (t === "MAIN" || t === "MASTER" || t === "RELEASE") return true;
  const b = (branch || "").toLowerCase();
  return b === "main" || b === "master" || b.startsWith("release-") || b.startsWith("release/");
}

export function diffOverlaps(changedFiles: string[], specFile?: string, stack?: string): boolean {
  const files = changedFiles.filter((f) => !f.startsWith(".github/") && !f.endsWith(".md"));
  if (specFile) {
    const spec = specFile.replace(/^\.\//, "");
    if (files.some((f) => spec.endsWith(f) || f.endsWith(spec) || spec.includes(f))) return true;
  }
  if (!stack) return false;
  return files.some((f) => stack.includes(f));
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
  if (isProtectedRun(args.runType, args.branch)) {
    return { waived: false, reason: "baseline/release runs never auto-waive" };
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
  const merged = mergeModel(suggested, args.ai, overlaps);
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
  };
}

function mergeModel(
  suggested: Suggestion,
  ai: ClaudeVerdict | undefined,
  overlaps: boolean,
): {
  verdict: string;
  confidence: number;
  reason: string;
  citations: string[];
  source: Decision["source"];
} {
  if (!ai) {
    return {
      verdict: suggested.verdict,
      confidence: suggested.confidence,
      reason: suggested.reason,
      citations: suggested.citations,
      source: "history",
    };
  }

  // A model cannot green a failure the PR's own diff touches. History-backed
  // MAIN_REGRESSION (already failing on baseline) is still allowed through
  // canWaive; this only blocks an AI-invented flake reading.
  if (overlaps && FLAKY.has(ai.verdict)) {
    return {
      verdict: suggested.verdict === "INCONCLUSIVE" ? "PR_REGRESSION" : suggested.verdict,
      confidence: Math.min(suggested.confidence, 0.6),
      reason: `${ai.reason} — ignored: PR diff overlaps the failing area`,
      citations: suggested.citations,
      source: "policy",
    };
  }

  const citations = unique([...suggested.citations, ...ai.citations]);
  return {
    verdict: ai.verdict,
    confidence: ai.confidence,
    reason: ai.reason,
    citations,
    source: "model",
  };
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
      description: `${decisions.length} failure(s) classified as flaky/pre-existing`,
    };
  }
  const worst = unwaived[0]!;
  return {
    waived: false,
    verdict: worst.verdict,
    state: "failure",
    description: `${unwaived.length}/${decisions.length} unwaived (${worst.verdict})`,
  };
}
