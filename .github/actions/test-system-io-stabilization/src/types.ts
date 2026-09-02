/** Queue entry shape served by GET /api/v1/triage/stabilization/queue. */
export interface QueueEntry {
  test_id: string;
  titles?: string[];
  runs?: number;
  failed?: number;
  flaky?: number;
  flips?: number;
  failure_rate?: number;
  flake_rate?: number;
  failing_since_commit?: string;
  promoted?: boolean;
  promoted_by?: string;
  promotion_source?: string;
  promotion_reason?: string;
}

export interface QueueResponse {
  repo: string;
  depth: number;
  promoted: QueueEntry[];
  ranked: QueueEntry[];
}

export type LoopAction =
  | { kind: "fix_pr"; testID: string; branch: string; prNumber: number }
  | { kind: "routed"; testID: string; owner: string; reason: string }
  | { kind: "budget_exhausted"; used: number; budget: number }
  | { kind: "attempts_exhausted"; testID: string; attempts: number; diagnosis: string }
  | { kind: "skipped"; testID: string; reason: string }
  | { kind: "queue_unavailable"; status: number };

/** The one thing the loop may edit — enforced before any write. */
export const EDITABLE_ROOT = "e2e-tests/";
export const STABILIZATION_LABEL = "e2e-stabilization";
export const BRANCH_PREFIX = "stabilization/";
