/**
 * Attempt recording — every loop attempt lands in the TSIO ledger as a
 * TEST_DEBT verdict on the stabilization PR's own identity, so the audit
 * trail shows what the loop tried and why, per attempt.
 */
import type * as core from "@actions/core";

export interface LedgerPostFn {
  (baseURL: string, body: unknown): Promise<void>;
}

export function attemptVerdict(args: {
  repository: string;
  commitSHA: string;
  ghRunID: string;
  testID: string;
  attempt: number;
  outcome: string;
  diagnosis: string;
}): Record<string, unknown> {
  return {
    repository: args.repository,
    branch: "master",
    commit_sha: args.commitSHA,
    gh_run_id: args.ghRunID,
    verdicts: [
      {
        external_test_id: args.testID,
        verdict: "TEST_DEBT",
        confidence: 1,
        check_state: "failure",
        waived: false,
        root_cause: `stabilization attempt ${args.attempt}: ${args.outcome} — ${args.diagnosis}`,
        evidence: [
          { kind: "stabilization_attempt", attempt: args.attempt },
          { kind: "outcome", detail: args.outcome },
        ],
      },
    ],
  };
}

export async function recordAttempt(
  post: LedgerPostFn,
  baseURL: string,
  args: Parameters<typeof attemptVerdict>[0],
): Promise<void> {
  await post(baseURL, attemptVerdict(args));
}

export const _unused2 = undefined as unknown as typeof core;
