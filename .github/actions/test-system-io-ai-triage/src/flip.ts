/**
 * Gate mode owns the original e2e-test/* commit status: green when every
 * failure is a waived flake, otherwise keep it red but rewrite the
 * description with product-bug vs test-bug blame. Separate ai-triage-*
 * rows are optional noise — skip them when originals are named.
 */

export function parseContextList(raw: string): string[] {
  return raw
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/** Original e2e-test/* contexts to rewrite after triage (gate only). */
export function contextsToUpdate(args: {
  mode: string;
  hasFailures: boolean;
  explicit: string[];
  discovered: Array<{ context: string; state: string }>;
  triageContext: string;
}): string[] {
  if (args.mode !== "gate" || !args.hasFailures) return [];
  const explicit = args.explicit.filter((c) => c && c !== args.triageContext);
  if (explicit.length > 0) return [...new Set(explicit)];

  const red = args.discovered.filter(
    (d) =>
      d.context.startsWith("e2e-test/") &&
      d.context !== args.triageContext &&
      !d.context.startsWith("e2e-test/ai-triage") &&
      (d.state === "failure" || d.state === "error"),
  );
  return [...new Set(red.map((d) => d.context))];
}

/** @deprecated use contextsToUpdate — flip only when waived */
export function contextsToFlip(args: {
  mode: string;
  waived: boolean;
  hasFailures: boolean;
  explicit: string[];
  discovered: Array<{ context: string; state: string }>;
  triageContext: string;
}): string[] {
  if (!args.waived) return [];
  return contextsToUpdate(args);
}

export type FailureBlame = "product bug" | "test bug";

/** PR/MAIN regressions are product; everything else that blocks merge is test-side. */
export function failureBlame(verdict: string): FailureBlame {
  if (verdict === "PR_REGRESSION" || verdict === "MAIN_REGRESSION") return "product bug";
  return "test bug";
}

export interface RunCounts {
  passed: number;
  failed: number;
  flaky?: number;
  skipped?: number;
}

/**
 * Parse pass/fail counts from an existing commit-status description so the
 * rewritten row keeps them. Two producers exist:
 *
 *   test-system-io-summary:  "99.8% passed (485/487), 2 failed, 5 specs"
 *   mobile tsio-report-status: "485 passed, 4 failed, 79 skipped"
 *
 * The ratio format is what the webapp's e2e-test/* rows carry — the headline
 * rate is `passed/(passed+failed)`, so failed = denominator − numerator.
 */
export function parseRunCounts(description: string | undefined | null): RunCounts | undefined {
  if (!description) return undefined;
  let passed: number | undefined;
  let failed: number | undefined;
  const ratio = description.match(/\((\d+)\/(\d+)\)/);
  if (ratio) {
    passed = Number(ratio[1]);
    failed = Number(ratio[2]) - passed;
  } else {
    const p = description.match(/(\d+)\s+passed/i);
    if (p) passed = Number(p[1]);
  }
  const f = description.match(/(\d+)\s+failed/i);
  if (f) failed = Number(f[1]);
  if (passed === undefined || failed === undefined) return undefined;
  const skipped = description.match(/(\d+)\s+skipped/i);
  return {
    passed,
    failed,
    skipped: skipped ? Number(skipped[1]) : undefined,
  };
}

/**
 * Description for the original e2e-test/* row (GitHub caps at 140 chars).
 * Prefer TSIO's deduped test stats (orchestration.tests) so flaky and skipped
 * survive the rewrite; parseRunCounts is only a fallback.
 */
export function originalStatusDescription(args: {
  counts?: RunCounts;
  failureCount?: number;
  waived: boolean;
  verdict: string;
}): string {
  const counts = args.counts;
  const head = counts
    ? [
        `${counts.passed} passed`,
        counts.flaky ? `${counts.flaky} flaky` : undefined,
        `${counts.failed} failed`,
        counts.skipped ? `${counts.skipped} skipped` : undefined,
      ]
        .filter((s) => s !== undefined)
        .join(", ")
    : args.failureCount !== undefined
      ? `${args.failureCount} failed`
      : "failures";

  if (args.waived) {
    return truncate(`${head} — waived as flaky`);
  }
  return truncate(`${head} — ${failureBlame(args.verdict)}`);
}

/** Kept for tests / older call sites that mention the triage context. */
export function flakeSuccessDescription(triageContext: string, summary: string): string {
  const prefix = `verified flaky — see ${triageContext}`;
  if (!summary) return prefix;
  const combined = `${prefix}: ${summary}`;
  return combined.length <= 140 ? combined : prefix;
}

function truncate(s: string): string {
  if (s.length <= 140) return s;
  return `${s.slice(0, 139)}…`;
}
