/**
 * When every failure is a waived flake (or pre-existing on the baseline),
 * the required PR check has to go green — otherwise triage is a comment
 * on a still-red merge button.
 *
 * Only `mode: gate` flips the original e2e-test/* contexts. shadow posts
 * e2e-test/ai-triage and leaves the merge-blocking row alone.
 */

export function parseContextList(raw: string): string[] {
  return raw
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function contextsToFlip(args: {
  mode: string;
  waived: boolean;
  hasFailures: boolean;
  explicit: string[];
  discovered: Array<{ context: string; state: string }>;
  triageContext: string;
}): string[] {
  if (args.mode !== "gate" || !args.waived || !args.hasFailures) return [];
  const explicit = args.explicit.filter((c) => c && c !== args.triageContext);
  if (explicit.length > 0) return [...new Set(explicit)];

  const red = args.discovered.filter(
    (d) =>
      d.context.startsWith("e2e-test/") &&
      d.context !== args.triageContext &&
      (d.state === "failure" || d.state === "error"),
  );
  return [...new Set(red.map((d) => d.context))];
}

export function flakeSuccessDescription(triageContext: string, summary: string): string {
  const prefix = `verified flaky — see ${triageContext}`;
  if (!summary) return prefix;
  const combined = `${prefix}: ${summary}`;
  return combined.length <= 140 ? combined : prefix;
}
